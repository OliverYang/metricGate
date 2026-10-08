package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

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
	configPath := set.String("config", "policy.yaml", "policy file (used only to locate store.path)")
	runs := set.Int("runs", 20, "how many of the most recent runs to include (0 = all)")
	format := set.String("format", "text", "output format: text|json")
	parseArgs(set, args)

	cfg, err := loadPolicy(*configPath)
	if err != nil {
		fail(err)
	}
	if !cfg.Store.Enabled {
		fmt.Fprintln(os.Stderr,
			"note: store.enabled is false in policy.yaml — `trends` will only see runs from prior enabled windows")
	}
	st, err := store.Open(cfg.Store.Path)
	if err != nil {
		fail(fmt.Errorf("open store: %w (run `metricgate check` first to create %s)", err, cfg.Store.Path))
	}
	defer st.Close()

	limit := *runs
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
