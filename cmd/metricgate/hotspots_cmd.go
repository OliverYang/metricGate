package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"metricgate/internal/hotspots"
	"metricgate/internal/metrics"
	"metricgate/internal/model"
)

// cmdHotspots ranks churn × maintainability-debt hotspots from git history
// and the current engine MI snapshot (design doc B-5).
func cmdHotspots(args []string) {
	set := flag.NewFlagSet("hotspots", flag.ExitOnError)
	since := set.String("since", "3.months ago", "git history window (e.g. 90.days ago)")
	maxN := set.Int("max", 10, "top N files")
	format := set.String("format", "text", "text|json")
	configPath := set.String("config", "policy.yaml", "policy file for MI computation")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	_, eng, summary := runEngine(*configPath, root)
	defer eng.Close()

	mis := worstFileMIs(summary.Metrics)
	h, err := hotspots.Compute(hotspots.Options{Since: *since, Root: root, MIs: mis, Max: *maxN})
	if err != nil {
		fail(err)
	}
	renderHotspots(h, *format)
}

func worstFileMIs(snaps []model.MetricResult) map[string]float64 {
	mis := make(map[string]float64)
	for _, m := range snaps {
		if m.Metric == metrics.MetricMaintainability {
			mi := 100 - float64(m.Value) // snapshots store debt (100-MI)
			if prev, ok := mis[m.File]; !ok || mi < prev {
				mis[m.File] = mi // worst function in the file
			}
		}
	}
	return mis
}

func renderHotspots(h []hotspots.FileHotspot, format string) {
	if format == "json" {
		data, err := json.MarshalIndent(h, "", "  ")
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(append(data, '\n'))
		return
	}
	printHotspotsText(h)
}

func printHotspotsText(h []hotspots.FileHotspot) {
	if len(h) == 0 {
		fmt.Println("no changed files in the selected history window")
		return
	}
	fmt.Printf("%-48s %10s %7s %8s\n", "FILE", "CHURN", "MI", "SCORE")
	for _, f := range h {
		mi := "-"
		if f.MI > 0 {
			mi = fmt.Sprintf("%.0f", f.MI)
		}
		fmt.Printf("%-48s %10d %7s %8.1f\n", f.File, f.Churn, mi, f.Score)
	}
}
