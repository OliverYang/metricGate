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
	records, err := st.LatestRuns(limit)
	if err != nil {
		fail(err)
	}
	if len(records) == 0 {
		if *format == "json" {
			out := []byte(`{"runs":0,"series":[],"topRules":[]}` + "\n")
			os.Stdout.Write(out)
			return
		}
		fmt.Println("no runs stored yet — run `metricgate check` first")
		return
	}
	trend, err := st.SeverityTrend(limit)
	if err != nil {
		fail(err)
	}
	rules, err := st.RuleFrequency(limit)
	if err != nil {
		fail(err)
	}
	if err := emitTrends(os.Stdout, *format, records, trend, rules); err != nil {
		fail(err)
	}
}

// trendsPayload is the JSON shape of `metricgate trends -format json`. It is
// kept narrow on purpose: a local trend dump is meant to feed a chart, not
// to be parsed back into the engine. Adding a new field is safe (consumers
// ignore extras); renaming is a breaking change.
type trendsPayload struct {
	Runs     int                `json:"runs"`
	Range    *trendsRange       `json:"range,omitempty"`
	Series   []store.TrendPoint `json:"series"`
	TopRules []store.RuleCount  `json:"topRules"`
}

type trendsRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

func emitTrends(w io.Writer, format string, records []store.RunRecord, trend store.SeverityTrend, rules []store.RuleCount) error {
	if format == "json" {
		out := trendsPayload{
			Runs:     len(records),
			Series:   trend.Series,
			TopRules: rules,
		}
		if len(records) > 0 {
			// records are newest-first; series is oldest-first. The first
			// record is therefore the end of the range and the last record
			// is the start.
			out.Range = &trendsRange{
				Start: records[len(records)-1].StartedAt,
				End:   records[0].StartedAt,
			}
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return err
		}
		_, err = w.Write(append(data, '\n'))
		return err
	}
	fmt.Fprintf(w, "runs: %d\n", len(records))
	if len(records) > 0 {
		fmt.Fprintf(w, "range: %s .. %s\n", records[len(records)-1].StartedAt, records[0].StartedAt)
	}
	fmt.Fprintln(w, "severity trend (per run, oldest first):")
	for _, p := range trend.Series {
		fmt.Fprintf(w, "  %s  E=%d W=%d I=%d\n", p.StartedAt, p.Errors, p.Warnings, p.Infos)
	}
	if len(rules) == 0 {
		fmt.Fprintln(w, "top rules: (no findings recorded)")
		return nil
	}
	fmt.Fprintln(w, "top rules:")
	for _, r := range rules {
		fmt.Fprintf(w, "  %-40s %d\n", r.RuleID, r.Count)
	}
	return nil
}
