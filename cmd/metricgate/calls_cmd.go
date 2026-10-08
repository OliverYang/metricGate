package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"metricgate/internal/calllog"
)

// cmdCalls reduces the local MCP call log to the numbers that decide whether
// the gate is earning a place in an agent's loop: how often it is called, at
// which point in the loop, and whether a call that flagged something was
// followed by a call that passed.
//
// It reads one file and writes to stdout. Nothing here talks to a network, and
// nothing here is uploaded — the log exists on this machine because the
// operator named it on the MCP command line.
func cmdCalls(args []string) {
	set := flag.NewFlagSet("calls", flag.ExitOnError)
	logPath := set.String("log", calllog.DefaultPath, "call log written by `metricgate-mcp -calllog`")
	since := set.String("since", "", "only count calls at or after this date (2006-01-02 or RFC 3339)")
	format := set.String("format", "text", "output format: text|json")
	parseArgs(set, args)

	cutoff, err := calllog.ParseSince(*since)
	if err != nil {
		fail(err)
	}

	recs, skipped, err := calllog.Read(*logPath)
	if os.IsNotExist(err) {
		// A missing log is the default state, not a failure: instrumentation is
		// off until someone asks for it. Say how to turn it on and stop.
		fmt.Fprintf(os.Stderr,
			"note: no call log at %s — instrumentation is off by default.\n"+
				"      Start the MCP server with -calllog %s (or set METRICGATE_CALLLOG=1) to record calls.\n",
			*logPath, calllog.DefaultPath)
		recs, skipped = nil, 0
	} else if err != nil {
		fail(err)
	}

	summary := calllog.Summarize(calllog.FilterSince(recs, cutoff))
	summary.Log = *logPath
	summary.Skipped = skipped
	if err := emitCalls(os.Stdout, *format, summary); err != nil {
		fail(err)
	}
}

func emitCalls(w io.Writer, format string, s calllog.Summary) error {
	if format == "json" {
		data, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return err
		}
		_, err = w.Write(append(data, '\n'))
		return err
	}

	fmt.Fprintf(w, "metricgate calls — local MCP call log\n")
	fmt.Fprintf(w, "log: %s\n", s.Log)
	if s.Records == 0 {
		fmt.Fprintln(w, "calls: 0 — nothing recorded in this window")
		return nil
	}
	fmt.Fprintf(w, "calls: %d   sessions: %d   range: %s .. %s\n",
		s.Records, s.Sessions, s.First.Format("2006-01-02T15:04:05Z"), s.Last.Format("2006-01-02T15:04:05Z"))
	if s.Skipped > 0 {
		fmt.Fprintf(w, "skipped: %d unparseable line(s) — a torn write, most likely\n", s.Skipped)
	}
	if s.ToolError > 0 {
		fmt.Fprintf(w, "tool errors: %d (a failed call is never counted as a passed check)\n", s.ToolError)
	}

	writeCounts(w, "phase", phaseRows(s))
	writeToolTable(w, s)

	v := s.Verify
	fmt.Fprintf(w, "\nverification (the gate's own verdicts)\n")
	if v.Calls == 0 {
		fmt.Fprintln(w, "  no check_code / check_path calls in this window")
		return nil
	}
	fmt.Fprintf(w, "  verify calls            %4d\n", v.Calls)
	fmt.Fprintf(w, "  clean                   %4d\n", v.Clean)
	fmt.Fprintf(w, "  flagged                 %4d   intercept rate %s\n", v.Flagged, pct(v.InterceptRate))
	fmt.Fprintf(w, "  tasks                   %4d   (one per MCP server process)\n", v.Tasks)
	fmt.Fprintf(w, "  flagged at least once   %4d\n", v.TasksFlagged)
	fmt.Fprintf(w, "  resolved (last = PASS)  %4d   converge rate %s\n", v.TasksResolved, pct(v.ConvergeRate))
	fmt.Fprintf(w, "  clean first try         %4d\n", v.CleanFirstTry)
	fmt.Fprintln(w, "\nnote: 'tasks' is a proxy — one MCP server process is treated as one task, so a")
	fmt.Fprintln(w, "      session that runs several tasks counts once, and one that ends before the")
	fmt.Fprintln(w, "      agent repairs counts as unresolved even when the gate did its job.")
	return nil
}

// phaseRows renders the phase table in the order an agent walks the loop,
// rather than alphabetically: before → explore → verify → review reads as a
// workflow, which is what the numbers are about.
var phaseOrder = []calllog.Phase{
	calllog.PhaseBefore, calllog.PhaseExplore, calllog.PhaseVerify, calllog.PhaseReview, calllog.PhaseOther,
}

func phaseRows(s calllog.Summary) [][2]string {
	var rows [][2]string
	for _, p := range phaseOrder {
		if n := s.ByPhase[string(p)]; n > 0 {
			rows = append(rows, [2]string{string(p), fmt.Sprint(n)})
		}
	}
	return rows
}

func writeCounts(w io.Writer, header string, rows [][2]string) {
	width := len(header)
	for _, r := range rows {
		if len(r[0]) > width {
			width = len(r[0])
		}
	}
	fmt.Fprintf(w, "\n%-*s  calls\n", width, header)
	for _, r := range rows {
		fmt.Fprintf(w, "%-*s  %5s\n", width, r[0], r[1])
	}
}

// writeToolTable lists tools by call count, most-called first, ties broken by
// name so two runs over the same log print the same table.
func writeToolTable(w io.Writer, s calllog.Summary) {
	tools := make([]string, 0, len(s.ByTool))
	for t := range s.ByTool {
		tools = append(tools, t)
	}
	sort.Slice(tools, func(i, j int) bool {
		if s.ByTool[tools[i]] != s.ByTool[tools[j]] {
			return s.ByTool[tools[i]] > s.ByTool[tools[j]]
		}
		return tools[i] < tools[j]
	})
	width := len("tool")
	for _, t := range tools {
		if len(t) > width {
			width = len(t)
		}
	}
	fmt.Fprintf(w, "\n%-*s  calls  median\n", width, "tool")
	for _, t := range tools {
		fmt.Fprintf(w, "%-*s  %5d  %4d ms\n", width, t, s.ByTool[t], s.MedianMS[t])
	}
}

// pct renders a rate as a whole percentage. The counts it is derived from are
// always printed next to it, because a rate without its denominator invites
// reading 0% as "never happens" when it can also mean "never ran".
func pct(r float64) string { return fmt.Sprintf("%.0f%%", r*100) }
