package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"metricgate/internal/engine"
)

// cmdRank implements `metricgate rank f1.go f2.go ...` — the deterministic,
// execution-free best-of-N discriminator (research direction 2, Coder[Reviewer]
// lineage): each candidate implementation is checked independently and the
// candidates are ordered by (errors, warnings, name). No LLM, no test runs —
// ranking is a static verdict, complementary to execution-based sifting.
func cmdRank(args []string) {
	set := flag.NewFlagSet("rank", flag.ExitOnError)
	format := set.String("format", "text", "output format: text|json")
	config := set.String("config", "", "policy file (default: policy.yaml if present)")
	parseArgs(set, args)
	files := set.Args()
	if len(files) < 2 {
		fmt.Fprintln(os.Stderr, "rank: need at least 2 candidate files to compare")
		os.Exit(2)
	}
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			fail(fmt.Errorf("rank: %w", err))
		}
	}
	cfg, err := loadPolicy(*config)
	if err != nil {
		fail(err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		fail(err)
	}
	defer eng.Close()

	ranked := eng.RankCandidates(files)
	if *format == "json" {
		b, err := json.MarshalIndent(ranked, "", "  ")
		if err != nil {
			fail(err)
		}
		fmt.Println(string(b))
		return
	}
	fmt.Printf("%-46s %7s %9s %10s\n", "CANDIDATE", "ERRORS", "WARNINGS", "PARSE-ERR")
	for i, c := range ranked {
		fmt.Printf("%d. %-44s %7d %9d %10d\n", i+1, c.Name, c.Errors, c.Warns, len(c.Summary.ParseErrors))
	}
	best := ranked[0]
	fmt.Printf("\nbest: %s (%d errors / %d warnings)\n", best.Name, best.Errors, best.Warns)
	if n := len(best.Summary.Findings); n > 0 {
		fmt.Printf("best candidate findings (%d):\n", n)
		for _, f := range best.Summary.Findings {
			fmt.Printf("  [%s] %s: %s\n", f.Severity, f.RuleID, f.Message)
		}
	}
}
