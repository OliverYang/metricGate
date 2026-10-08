package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"metricgate/internal/config"
	"metricgate/internal/engine"
	"metricgate/internal/facts"
	"metricgate/internal/model"
	"metricgate/internal/repomap"
)

// cmdMap ranks the repository's files by reference importance: the whole-repo
// answer to "which files should I read first?", computed from the definitions
// and use sites the parse pass already has. With -risk (the default) each file
// also carries its static risk score, so an important file that is also
// dangerous is visible in one view.
func cmdMap(args []string) {
	set := flag.NewFlagSet("map", flag.ExitOnError)
	configPath := set.String("config", "policy.yaml", "policy file")
	n := set.Int("n", 25, "maximum files to list")
	maxChars := set.Int("max-chars", 4000, "output budget in bytes (0 = unlimited)")
	format := set.String("format", "text", "text|json")
	withRisk := set.Bool("risk", true, "annotate each file with its static risk score (runs the checker)")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	cfg, err := loadPolicy(*configPath)
	if err != nil {
		fail(err)
	}
	findings, mets := riskInputs(cfg, root, *withRisk)
	scores, err := facts.RankedFiles(root, cfg, findings, mets)
	if err != nil {
		fail(err)
	}
	if *n > 0 && len(scores) > *n {
		scores = scores[:*n]
	}
	if *format == "json" {
		data, err := json.MarshalIndent(scores, "", "  ")
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(append(data, '\n'))
		return
	}
	fmt.Print(repomap.Render(scores, *maxChars))
}

// riskInputs runs the checker to supply the risk signal, unless the caller
// turned it off; nil inputs simply leave every file's risk at zero.
func riskInputs(cfg *config.Config, root string, enabled bool) ([]model.Finding, []model.MetricResult) {
	if !enabled {
		return nil, nil
	}
	eng, err := engine.New(cfg)
	if err != nil {
		fail(err)
	}
	defer eng.Close()
	summary, err := eng.Run(root)
	if err != nil {
		fail(err)
	}
	return summary.Findings, summary.Metrics
}
