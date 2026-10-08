package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"metricgate/internal/dist"
)

// cmdStats prints the distribution of every measured metric across its scopes:
// the percentiles that answer "how bad is this codebase overall?", plus the
// worst scopes that answer "where is the worst of it?". It complements `check`
// (which only reports threshold crossings) and `metrics` (which lists per-file
// aggregates) by describing the shape of the whole population.
func cmdStats(args []string) {
	set := flag.NewFlagSet("stats", flag.ExitOnError)
	configPath := set.String("config", "policy.yaml", "policy file")
	topN := set.Int("top", 5, "worst scopes to list per metric (0 = none)")
	format := set.String("format", "text", "text|json")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	_, eng, summary := runEngine(*configPath, root)
	defer eng.Close()

	reports := dist.Reports(summary.Metrics, *topN)
	if *format == "json" {
		data, err := json.MarshalIndent(reports, "", "  ")
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(append(data, '\n'))
		return
	}
	printStats(reports)
}

func printStats(reports []dist.Report) {
	if len(reports) == 0 {
		fmt.Println("no metrics measured — check the policy's metrics section")
		return
	}
	fmt.Printf("%-26s %6s %5s %5s %5s %5s %5s %7s\n", "METRIC", "COUNT", "MIN", "P50", "P90", "P95", "MAX", "MEAN")
	for _, r := range reports {
		fmt.Printf("%-26s %6d %5d %5d %5d %5d %5d %7.1f\n",
			r.Metric, r.Count, r.Min, r.P50, r.P90, r.P95, r.Max, r.Mean)
	}
	for _, r := range reports {
		printTopScopes(r)
	}
}

// printTopScopes lists one metric's worst scopes. The maintainability snapshot
// stores debt (100-MI), so its top list is the files whose worst function is
// least maintainable.
func printTopScopes(r dist.Report) {
	if len(r.Top) == 0 {
		return
	}
	fmt.Printf("\nworst by %s:\n", r.Metric)
	for _, s := range r.Top {
		fmt.Printf("  %5d  %s:%d", s.Value, s.File, s.Line)
		if s.Scope != "" {
			fmt.Printf("  %s", s.Scope)
		}
		fmt.Println()
	}
}
