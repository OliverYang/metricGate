package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"metricgate/internal/store"
)

// cmdTrends reads the local trend database (see `store:` in policy.yaml) and
// prints a deterministic, JSON-friendly summary: the most recent run window
// as a severity series and the rules that fired most often in that window.
//
// It is read-only; the only thing that mutates the database is `metricgate
// check` when `store.enabled: true` is set. Failures here are about the
// database being unavailable, never about what the database contains.
func cmdTrends(args []string) {
	set := flag.NewFlagSet("trends", flag.ExitOnError)
	configPath := set.String("config", "", "policy file (used only to locate store.path)")
	runs := set.Int("runs", 20, "how many of the most recent runs to include (0 = all)")
	format := set.String("format", "text", "output format: text|json")
	metrics := set.Bool("metrics", false, "metric-dimension trend: per-run distribution (p50/p90/max) and thresholds per metric — use to compare before/after a threshold change")
	parseArgs(set, args)

	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	cfg, err := loadPolicyFor(*configPath, root)
	if err != nil {
		fail(err)
	}
	if !cfg.Store.Enabled {
		// The note has to stay true. `check` writes a run whenever the store
		// file is open, so claiming "you are not storing anything" is wrong
		// the moment history.db holds records — and it is exactly the moment a
		// reader has the most to lose from a false claim. Count first, then tell
		// them the truth about the count.
		st, err := store.Open(cfg.Store.Path)
		if err == nil {
			n, cerr := storeRunCount(st)
			st.Close()
			if cerr == nil && n > 0 {
				fmt.Fprintf(os.Stderr,
					"note: store.enabled is false in policy.yaml, so `check` stores nothing new — the database still holds %d run(s) from earlier\n", n)
			} else {
				fmt.Fprintln(os.Stderr, "note: store.enabled is false in policy.yaml — `check` will store nothing new; the trend database is empty")
			}
		}
	}
	st, err := store.Open(cfg.Store.Path)
	if err != nil {
		fail(fmt.Errorf("open store: %w (run `metricgate check` first to create %s)", err, cfg.Store.Path))
	}
	defer st.Close()

	limit := *runs
	if *metrics {
		mt, err := st.MetricTrend(limit)
		if err != nil {
			fail(err)
		}
		if err := emitMetricTrend(os.Stdout, *format, mt); err != nil {
			fail(err)
		}
		return
	}
	report, err := st.Report(limit)
	if err != nil {
		fail(err)
	}
	if report.Runs == 0 {
		if *format == "json" {
			os.Stdout.Write([]byte(`{"runs":0,"series":[],"topRules":[]}` + "\n"))
			return
		}
		fmt.Println("no runs stored yet — run `metricgate check` first")
		return
	}
	if err := emitTrends(os.Stdout, *format, report); err != nil {
		fail(err)
	}
}

func emitTrends(w io.Writer, format string, report store.TrendReport) error {
	if format == "json" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		_, err = w.Write(append(data, '\n'))
		return err
	}
	fmt.Fprintf(w, "runs: %d\n", report.Runs)
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
