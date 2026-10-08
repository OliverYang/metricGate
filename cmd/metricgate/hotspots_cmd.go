package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"metricgate/internal/hotspots"
)

// cmdHotspots ranks churn × maintainability-debt hotspots from git history
// and the current engine MI snapshot (design doc B-5). Each hotspot also
// carries the static risk score (findings + debt), so the churn view and the
// "how much is already wrong" view sit side by side.
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

	h, err := hotspots.Compute(hotspots.FromSummary(root, *since, *maxN, summary))
	if err != nil {
		fail(err)
	}
	renderHotspots(h, *format)
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
	fmt.Printf("%-48s %10s %7s %6s %8s\n", "FILE", "CHURN", "MI", "RISK", "SCORE")
	for _, f := range h {
		mi := "-"
		if f.MI > 0 {
			mi = fmt.Sprintf("%.0f", f.MI)
		}
		fmt.Printf("%-48s %10d %7s %6.1f %8.1f\n", f.File, f.Churn, mi, f.Risk, f.Score)
	}
}
