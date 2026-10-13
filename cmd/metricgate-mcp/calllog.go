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
	// trace is the ambient trace id, read once at startup so every call from
	// this process is attributed the same way whatever the worker that
	// produced it was doing.
	trace string

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
	if trace := traceID(); trace != "" {
		fmt.Fprintf(os.Stderr, "metricgate-mcp: tagging calls with trace %q\n", trace)
	}
	// One MCP server process serves one agent session in practice, so a
	// process-scoped id is what groups a task's calls. It is a proxy, and the
	// summary documents how it can be wrong.
	return &callLogger{
		path:    path,
		session: time.Now().UTC().Format("20060102T150405Z") + "-" + strconv.Itoa(os.Getpid()),
		trace:   traceID(),
	}
}

// traceID returns the ambient trace id, or "" when the operator set none.
//
// An empty trace is a legal state, not an error: a repository checked by hand
// has no unit of work to attribute its calls to, and inventing one would make
// every later aggregation lie about how many sessions there were.
func traceID() string {
	return strings.TrimSpace(os.Getenv("METRICGATE_TRACE_ID"))
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

func (l *callLogger) recordProject(in any) (id, path, name string) {
	if hasVirtualName(in) {
		return "", "", ""
	}
	locatorPath := projectPath(in)
	if locatorPath == "" {
		var err error
		locatorPath, err = os.Getwd()
		if err != nil {
			return "", "", ""
		}
	} else if !filepath.IsAbs(locatorPath) {
		var err error
		locatorPath, err = filepath.Abs(locatorPath)
		if err != nil {
			return "", "", ""
		}
	}
	root := verifiedProjectRoot(locatorPath)
	if root == "" {
		return "", "", ""
	}
	return root, root, filepath.Base(root)
}

func hasVirtualName(in any) bool {
	v := reflect.ValueOf(in)
	if v.Kind() != reflect.Struct {
		return false
	}
	name := v.FieldByName("Name")
	return name.IsValid() && name.Kind() == reflect.String && name.String() != ""
}

func projectPath(in any) string {
	v := reflect.ValueOf(in)
	if v.Kind() != reflect.Struct {
		return ""
	}
	field := v.FieldByName("Path")
	if !field.IsValid() || field.Kind() != reflect.String {
		return ""
	}
	return field.String()
}

func verifiedProjectRoot(path string) string {
	if !filepath.IsAbs(path) {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return ""
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return ""
	}
	if !info.IsDir() {
		resolved = filepath.Dir(resolved)
	}
	for dir := resolved; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
	}
}

// outcomeCarrier is how the wrapper reads a handler's result without knowing
// its static type. Only the tools that render a verdict implement it; the rest
// log zeroed counts, which is correct — they have no verdict to record.
type outcomeCarrier interface {
	callOutcome() (findings, errors, warnings int, verdict string)
}

// wrapTool returns h with the gate preamble and call recording layered on.
// The gate preamble rides on every response (see gate.go); recording is
// active only when an operator enabled the call log.
func wrapTool[In, Out any](g *gateWatcher, h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return wrapGate(g, h)
}

// wrapLogged records every call to the local call log and otherwise passes
// the handler through unchanged.
func wrapLogged[In, Out any](l *callLogger, tool string, h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	if l == nil || l.path == "" {
		return h
	}
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		start := time.Now()
		res, out, err := h(ctx, req, in)
		rec := newRecord(l, calllog.Record{Time: start.UTC(), Session: l.session, TraceID: l.trace, Tool: tool, Target: locator(in), MS: time.Since(start).Milliseconds()}, in, out, err)
		// Fail-open on purpose. A tool call must never fail because the log
		// could not be written; the first failure is reported once so a
		// misconfigured path is not silent, then the log goes quiet.
		if err := calllog.Append(l.path, rec); err != nil {
			l.warnOnce(err)
		}
		return res, out, err
	}
}

// newRecord assembles the log entry for one finished call. Project identity
// is taken from the input when it names a trustworthy path, else from the
// output.
func newRecord[In, Out any](l *callLogger, rec calllog.Record, in In, out Out, err error) calllog.Record {
	rec.ProjectID, rec.ProjectPath, rec.ProjectName = l.recordProject(in)
	if oc, ok := any(out).(outcomeCarrier); ok {
		rec.Findings, rec.Errors, rec.Warnings, rec.Verdict = oc.callOutcome()
	}
	if rec.ProjectID == "" {
		rec.ProjectID, rec.ProjectPath, rec.ProjectName = l.recordProject(out)
	}
	if err != nil {
		rec.Err = err.Error()
	}
	return rec
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
