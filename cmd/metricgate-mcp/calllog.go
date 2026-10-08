package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"metricgate/internal/calllog"
)

// callLogger appends one JSON line per tool call to a local file. It is the
// measurement apparatus for the only question this project cannot answer from
// its own test suite: does an agent that is offered the gate actually call it,
// and does calling it change the code it produces?
//
// It is off unless an operator turns it on (see newCallLogger), it never opens
// a socket, and it never sees the code a caller passed in — see the locator
// allow-list below.
type callLogger struct {
	path    string
	session string

	mu     sync.Mutex
	warned bool
}

// newCallLogger turns the -calllog flag (or METRICGATE_CALLLOG) into a logger.
// Anything that is not an explicit request returns nil, and a nil logger
// disables instrumentation entirely rather than writing to a default location:
// a tool that quietly starts recording what an agent touched has changed its
// own data posture without being asked to.
func newCallLogger(setting string) *callLogger {
	path := ""
	switch strings.ToLower(strings.TrimSpace(setting)) {
	case "", "0", "off", "false", "no":
		return nil
	case "1", "on", "true", "yes":
		path = calllog.DefaultPath
	default:
		path = setting
	}
	// The operator named this file, so create the directory it needs — once, at
	// startup, where a failure is visible — instead of leaving Append to fail
	// on every call. A failure here is still not fatal: Append reports it.
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "metricgate-mcp: call log directory %s: %v\n", dir, err)
		}
	}
	fmt.Fprintf(os.Stderr, "metricgate-mcp: recording tool calls to %s\n", path)
	// One MCP server process serves one agent session in practice, so a
	// process-scoped id is what groups a task's calls. It is a proxy, and the
	// summary documents how it can be wrong.
	return &callLogger{
		path:    path,
		session: time.Now().UTC().Format("20060102T150405Z") + "-" + strconv.Itoa(os.Getpid()),
	}
}

// locatorFields is the ordered allow-list of argument fields the call log may
// record, paired with the key they are written under.
//
// It is an allow-list, not a deny-list, and the field carrying source — `Code`
// — is deliberately absent. Recording it would turn this file into a store of
// every snippet an agent wrote, which is exactly what internal/calllog's first
// constraint forbids. A new tool whose argument struct adds a `Path` field is
// instrumented automatically; one whose only locator is called something else
// simply logs no target, which is a gap in the log rather than a leak.
var locatorFields = []struct{ field, key string }{
	{"Path", "path"},
	{"StorePath", "store"},
	{"Query", "query"},
	{"Metric", "metric"},
	{"Name", "name"},
}

// locator renders a call's identifying arguments as space-separated k=v pairs,
// in the fixed order of locatorFields, so the same call always logs the same
// string.
func locator(in any) string {
	v := reflect.ValueOf(in)
	if v.Kind() != reflect.Struct {
		return ""
	}
	var parts []string
	for _, lf := range locatorFields {
		f := v.FieldByName(lf.field)
		if !f.IsValid() || f.Kind() != reflect.String {
			continue
		}
		if s := f.String(); s != "" {
			parts = append(parts, lf.key+"="+s)
		}
	}
	return strings.Join(parts, " ")
}

// outcomeCarrier is how the wrapper reads a handler's result without knowing
// its static type. Only the tools that render a verdict implement it; the rest
// log zeroed counts, which is correct — they have no verdict to record.
type outcomeCarrier interface {
	callOutcome() (findings, errors, warnings int, verdict string)
}

// wrapTool returns h, recording every call. With logging off it returns h
// itself, so the disabled path costs nothing.
//
// It is a free function rather than a method because Go does not allow type
// parameters on methods, and the handler's In/Out types are what let the
// wrapper stay in the typed MCP API instead of falling back to `any`.
func wrapTool[In, Out any](l *callLogger, tool string, h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	if l == nil || l.path == "" {
		return h
	}
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		start := time.Now()
		res, out, err := h(ctx, req, in)
		rec := calllog.Record{
			Time:    start.UTC(),
			Session: l.session,
			Tool:    tool,
			Target:  locator(in),
			MS:      time.Since(start).Milliseconds(),
		}
		if oc, ok := any(out).(outcomeCarrier); ok {
			rec.Findings, rec.Errors, rec.Warnings, rec.Verdict = oc.callOutcome()
		}
		if err != nil {
			rec.Err = err.Error()
		}
		// Fail-open on purpose. A tool call must never fail because the log
		// could not be written; the first failure is reported once so a
		// misconfigured path is not silent, then the log goes quiet.
		if err := calllog.Append(l.path, rec); err != nil {
			l.warnOnce(err)
		}
		return res, out, err
	}
}

func (l *callLogger) warnOnce(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.warned {
		return
	}
	l.warned = true
	fmt.Fprintf(os.Stderr, "metricgate-mcp: call log %s: %v (further write errors are suppressed)\n", l.path, err)
}
