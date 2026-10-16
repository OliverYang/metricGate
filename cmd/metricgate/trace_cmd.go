package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"metricgate/internal/calllog"
	"metricgate/internal/store"
	"metricgate/internal/traceview"
)

// cmdTrace answers one question with one call: "everything that happened under
// this trace id." A trace is the key that ties a unit of work — one task, one
// PR, one agent session — across the five tables the tool already writes (the
// run history, the findings, the metric snapshots, the trace attributes and
// the MCP call log). Before this verb the five existed and nothing joined them.
//
// It is read-only, and the join happens in internal/traceview so the MCP
// `get_trace` tool and this verb cannot drift.
func cmdTrace(args []string) {
	f := parseTraceFlags(args)
	cfg, err := loadPolicyFor(f.configPath, f.root)
	if err != nil {
		fail(err)
	}
	view := traceview.Collect(cfg.Store.Path, f.logPath, f.traceID)
	var buf bytes.Buffer
	if err := emitTrace(&buf, f.format, view); err != nil {
		fail(err)
	}
	os.Stdout.Write(buf.Bytes())
}

// traceFlags is the parsed trace command line, extracted so cmdTrace stays a
// short sequence rather than a function that both parses and renders.
type traceFlags struct {
	root       string
	configPath string
	format     string
	logPath    string
	traceID    string
}

// parseTraceFlags consumes the trace command line. The trace id is the
// subject, so it is the first positional argument; the path is optional and
// second — matching the usage line and the way a reader types it ("trace
// <id>"), rather than forcing them to name a directory they are standing in.
func parseTraceFlags(args []string) traceFlags {
	set := flag.NewFlagSet("trace", flag.ExitOnError)
	configPath := set.String("config", "", "policy file (used only to locate store.path)")
	format := set.String("format", "text", "output format: text|json")
	logPath := set.String("log", calllog.DefaultPath, "MCP call log written by `metricgate-mcp -calllog`")
	parseArgs(set, args)
	if set.NArg() == 0 {
		fail(fmt.Errorf("usage: metricgate trace <traceID> [path] — the id comes from $METRICGATE_TRACE_ID on the runs you want to inspect"))
	}
	root := "."
	if set.NArg() > 1 {
		root = set.Arg(1)
	}
	return traceFlags{root: root, configPath: *configPath, format: *format, logPath: *logPath, traceID: set.Arg(0)}
}

// emitTrace renders the view as text or JSON. The text form leads with the
// totals, then the attributes, then runs, then calls — the order a reader
// reconstructs the story in.
func emitTrace(w io.Writer, format string, view traceview.View) error {
	if format == "json" {
		data, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return err
		}
		_, err = w.Write(append(data, '\n'))
		return err
	}
	fmt.Fprintf(w, "trace %s\n", view.TraceID)
	if view.Totals.Runs == 0 && view.Totals.Calls == 0 && len(view.Attrs) == 0 {
		fmt.Fprintln(w, "nothing recorded under this trace id — check $METRICGATE_TRACE_ID was set when the runs and calls were made")
		return nil
	}
	emitTraceAttrs(w, view.Attrs)
	emitTraceTotals(w, view.Totals)
	emitTraceRuns(w, view.Runs)
	emitTraceCalls(w, view.Calls)
	return nil
}

func emitTraceAttrs(w io.Writer, attrs []store.Attr) {
	if len(attrs) == 0 {
		return
	}
	fmt.Fprintln(w, "\nattributes:")
	for _, a := range attrs {
		fmt.Fprintf(w, "  %-18s %s\n", a.Key, a.Value)
	}
}

func emitTraceTotals(w io.Writer, t traceview.Totals) {
	fmt.Fprintf(w, "\nruns: %d   errors: %d   warnings: %d   notes: %d\n", t.Runs, t.Errors, t.Warnings, t.Infos)
	fmt.Fprintf(w, "calls: %d", t.Calls)
	if t.Calls > 0 {
		fmt.Fprintf(w, "   verify calls: %d   flagged: %d   resolved: %d", t.Verify.Calls, t.Verify.Flagged, t.Verify.TasksResolved)
	}
	fmt.Fprintln(w)
}

func emitTraceRuns(w io.Writer, runs []traceview.Run) {
	if len(runs) == 0 {
		return
	}
	fmt.Fprintln(w, "\nruns (oldest first):")
	for _, run := range runs {
		fmt.Fprintf(w, "  #%d  %s  E=%d W=%d I=%d  files=%d", run.ID, run.StartedAt, run.ErrorCount, run.WarningCount, run.InfoCount, run.Files)
		if run.PolicyDigest != "" {
			fmt.Fprintf(w, "  policy=%s", run.PolicyDigest)
		}
		fmt.Fprintln(w)
		for _, f := range run.Findings {
			mark := ""
			if f.Accepted {
				mark = "  [accepted]"
			}
			fmt.Fprintf(w, "      %s %s:%d  %s%s\n", f.Severity, f.File, f.Line, f.RuleID, mark)
		}
	}
}

func emitTraceCalls(w io.Writer, calls []calllog.Record) {
	if len(calls) == 0 {
		return
	}
	fmt.Fprintln(w, "\ncalls (oldest first):")
	for _, c := range calls {
		verdict := c.Verdict
		if c.Err != "" {
			verdict = "ERROR: " + c.Err
		}
		fmt.Fprintf(w, "  %s  %-18s %-8s %s\n", c.Time.Format("15:04:05"), c.Tool, c.Phase, verdict)
	}
}
