package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"metricgate/internal/config"
	"metricgate/internal/store"
)

// trendsFlags is the parsed command line. A struct rather than six
// dereferenced pointers, so the steps below read as a sequence.
type trendsFlags struct {
	root       string
	configPath string
	runs       int
	format     string
	metrics    bool
	out        string
	since      string
	project    string
}

// parseTrendsFlags consumes the trends command line.
func parseTrendsFlags(args []string) trendsFlags {
	set := flag.NewFlagSet("trends", flag.ExitOnError)
	configPath := set.String("config", "", "policy file (used only to locate store.path)")
	runs := set.Int("runs", 20, "how many of the most recent runs to include (0 = all)")
	format := set.String("format", "text", "output format: text|json")
	metrics := set.Bool("metrics", false, "metric-dimension trend: per-run distribution (p50/p90/max) and thresholds per metric — use to compare before/after a threshold change")
	out := set.String("out", "", "write the report to this file instead of stdout")
	since := set.String("since", "", "keep only runs started at or after this time (RFC3339, or a bare date/date+time in the local zone)")
	project := set.String("project", "", "keep only runs recorded for this project root (default: every project)")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	return trendsFlags{
		root: root, configPath: *configPath, runs: *runs, format: *format,
		metrics: *metrics, out: *out, since: *since, project: *project,
	}
}

// cmdTrends reads the local trend database (see `store:` in policy.yaml) and
// prints a deterministic, JSON-friendly summary: the most recent run window
// as a severity series and the rules that fired most often in that window.
//
// It is read-only; the only thing that mutates the database is `metricgate
// check` when `store.enabled: true` is set. Failures here are about the
// database being unavailable, never about what the database contains.
func cmdTrends(args []string) {
	f := parseTrendsFlags(args)
	cfg, err := loadPolicyFor(f.configPath, f.root)
	if err != nil {
		fail(err)
	}
	st, err := store.Open(cfg.Store.Path)
	if err != nil {
		// The message names the file, because "run `metricgate check` first" is
		// only actionable when the operator can see which path is being opened.
		fail(fmt.Errorf("open store: %w (run `metricgate check` first to create %s)", err, cfg.Store.Path))
	}
	defer st.Close()
	window := trendWindow(f, st)
	noteStoreDisabled(cfg, st)
	var buf bytes.Buffer
	if err := renderTrends(&buf, st, window, f); err != nil {
		fail(err)
	}
	writeTrends(f, window, &buf)
}

// trendWindow turns the flags into one store window. A -since or -project the
// database cannot honour is an error rather than a silently wider report: the
// operator asked for a scope, and answering a different question is the worse
// failure of the two.
func trendWindow(f trendsFlags, st *store.Store) store.Window {
	window := store.Window{Limit: f.runs}
	if f.since != "" {
		at, err := parseSinceArg(f.since)
		if err != nil {
			fail(err)
		}
		window.Since = at
	}
	if f.project != "" {
		root, err := st.ProjectRoot(f.project)
		if err != nil {
			fail(err)
		}
		window.ProjectRoot = root
	}
	return window
}

// noteStoreDisabled tells the truth about a store the policy says is off but the
// database still has rows in.
//
// `check` writes a run whenever the store file is open, so claiming "you are not
// storing anything" is wrong the moment history.db holds records — and it is
// exactly the moment a reader has the most to lose from a false claim. Count
// first, then tell them the truth about the count.
func noteStoreDisabled(cfg *config.Config, st *store.Store) {
	if cfg.Store.Enabled {
		return
	}
	n, err := storeRunCount(st)
	if err == nil && n > 0 {
		fmt.Fprintf(os.Stderr,
			"note: store.enabled is false in policy.yaml, so `check` stores nothing new — the database still holds %d run(s) from earlier\n", n)
		return
	}
	fmt.Fprintln(os.Stderr, "note: store.enabled is false in policy.yaml — `check` will store nothing new; the trend database is empty")
}

// renderTrends writes one report into buf. Rendering into a buffer rather than
// straight into the target means a failed write never leaves a half-written
// file behind where a complete one used to be.
func renderTrends(buf *bytes.Buffer, st *store.Store, window store.Window, f trendsFlags) error {
	if f.metrics {
		mt, err := st.MetricTrendIn(window)
		if err != nil {
			return err
		}
		return emitMetricTrend(buf, f.format, mt)
	}
	report, err := st.ReportIn(window)
	if err != nil {
		return err
	}
	if report.Runs == 0 {
		return emitEmptyTrends(buf, f.format, window)
	}
	return emitTrends(buf, f.format, report)
}

// writeTrends sends the rendered report to stdout or to the -out file, and says
// where it went. It uses the same 0600 mode as the bundle writer, because a
// findings export is the same kind of artifact.
func writeTrends(f trendsFlags, window store.Window, buf *bytes.Buffer) {
	if f.out != "" {
		if err := os.WriteFile(f.out, buf.Bytes(), 0o600); err != nil {
			fail(err)
		}
		fmt.Fprintf(os.Stderr, "wrote trends to %s (%s; %d bytes)\n", f.out, window.String(), buf.Len())
		return
	}
	if _, err := os.Stdout.Write(buf.Bytes()); err != nil {
		fail(err)
	}
}

// parseSinceArg accepts an RFC3339 timestamp, or the two shapes a person
// actually types: a bare date (YYYY-MM-DD) and date plus time separated by a
// space. Both are read in the local zone, because "since 2026-10-01" means
// midnight where the operator is, not where the database's timestamps are (UTC).
func parseSinceArg(raw string) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if at, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return at, nil
	}
	if at, err := time.ParseInLocation("2006-01-02", trimmed, time.Local); err == nil {
		return at, nil
	}
	if at, err := time.ParseInLocation("2006-01-02 15:04:05", trimmed, time.Local); err == nil {
		return at, nil
	}
	return time.Time{}, fmt.Errorf("-since %q is not a time I can read; want RFC3339 (2026-10-01T00:00:00Z), a date (2026-10-01) or date+time (2026-10-01 09:30:00)", raw)
}

// emitEmptyTrends keeps the empty-store answer in one place, now that the
// answer depends on the window as well as on the store being empty: "no runs in
// this window" is a different statement from "no runs at all".
func emitEmptyTrends(w io.Writer, format string, window store.Window) error {
	empty := store.TrendReport{Runs: 0, Series: []store.TrendPoint{}, TopRules: []store.RuleCount{}, Window: window.String()}
	if format == "json" {
		data, err := json.MarshalIndent(empty, "", "  ")
		if err != nil {
			return err
		}
		_, err = w.Write(append(data, '\n'))
		return err
	}
	fmt.Fprintf(w, "no runs stored in this window (%s) — widen it (trends -runs 0 -since 1970-01-01) or run `metricgate check` first\n", window.String())
	return nil
}

// emitTrends now receives the window-bearing report itself, so a report whose
// series starts at run #5 and a window that says "all runs" cannot be printed
// as if the two agreed.
func emitTrends(w io.Writer, format string, report store.TrendReport) error {
	if format == "json" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		_, err = w.Write(append(data, '\n'))
		return err
	}
	fmt.Fprintf(w, "runs: %d (%s)\n", report.Runs, report.Window)
	if report.Range != nil {
		fmt.Fprintf(w, "range: %s .. %s\n", report.Range.Start, report.Range.End)
	}
	fmt.Fprintln(w, "severity trend (per run, oldest first):")
	for _, p := range report.Series {
		fmt.Fprintf(w, "  %s  E=%d W=%d I=%d\n", p.StartedAt, p.Errors, p.Warnings, p.Infos)
	}
	if len(report.TopRules) == 0 {
		fmt.Fprintln(w, "top rules: (no findings recorded)")
		return nil
	}
	fmt.Fprintln(w, "top rules:")
	for _, r := range report.TopRules {
		fmt.Fprintf(w, "  %-40s %d\n", r.RuleID, r.Count)
	}
	return nil
}

// emitMetricTrend renders the metric-dimension trend. The text form prints
// one block per metric so a threshold change reads as a step in the warn
// column next to the movement in p90/max.
func emitMetricTrend(w io.Writer, format string, mt store.MetricTrend) error {
	if format == "json" {
		data, err := json.MarshalIndent(mt, "", "  ")
		if err != nil {
			return err
		}
		_, err = w.Write(append(data, '\n'))
		return err
	}
	if len(mt.Series) == 0 {
		fmt.Fprintln(w, "no metric snapshots stored yet — run `metricgate check` with store.enabled")
		return nil
	}
	names := make([]string, 0, len(mt.Series))
	for m := range mt.Series {
		names = append(names, m)
	}
	sort.Strings(names)
	fmt.Fprintf(w, "runs: %d, metrics: %d\n", mt.Runs, len(names))
	for _, m := range names {
		fmt.Fprintf(w, "\n%s:\n", m)
		fmt.Fprintf(w, "  %-20s %8s %6s %6s %6s %8s %6s %8s\n", "run", "started", "p50", "p90", "max", "samples", "warn", "violated")
		for _, p := range mt.Series[m] {
			fmt.Fprintf(w, "  #%-19d %8s %6d %6d %6d %8d %6d %8d\n",
				p.RunID, p.Started[:19], p.P50, p.P90, p.Max, p.Samples, p.Warn, p.Violated)
		}
	}
	return nil
}

// storeRunCount reports how many runs the database already holds. The note is
// about the database's contents, so it asks the database rather than inferring
// from the config — which is the whole bug being fixed.
func storeRunCount(st *store.Store) (int, error) {
	return st.Runs()
}
