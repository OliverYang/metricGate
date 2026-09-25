package main

import (
	"flag"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"metricgate/internal/engine"
)

// cmdExplain implements `metricgate explain` command for preview/debug.
func cmdExplain(args []string) {
	set := flag.NewFlagSet("explain", flag.ExitOnError)
	configPath := set.String("config", "policy.yaml", "policy file")
	format := set.String("format", "text", "text|json")
	preview := set.Bool("preview", false, "show what would be scanned")
	set.Parse(args)

	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}

	cfg, err := loadPolicy(*configPath)
	if err != nil {
		fail(err)
	}

	eng, err := engine.New(cfg)
	if err != nil {
		fail(err)
	}
	defer eng.Close()

	if *preview || root == "." {
		cmdExplainPreview(eng, root, *format)
		return
	}

	cmdExplainOne(eng, root, *format)
}

func cmdExplainPreview(eng *engine.Engine, root, format string) {
	decisions, err := eng.ExplainAllPaths(root)
	if err != nil {
		fail(fmt.Errorf("explain paths: %w", err))
	}

	if format == "json" {
		data, err := json.MarshalIndent(decisions, "", "  ")
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(append(data, '\n'))
		return
	}

	// Text format
	included, excluded, unsupported := 0, 0, 0
	for _, d := range decisions {
		switch d.Status {
		case "included":
			included++
		case "excluded":
			excluded++
		case "unsupported":
			unsupported++
		}
	}

	fmt.Printf("Preview for: %s\n", root)
	fmt.Printf("  Included:    %d\n", included)
	fmt.Printf("  Excluded:    %d\n", excluded)
	fmt.Printf("  Unsupported: %d\n", unsupported)
	fmt.Println()

	if excluded > 0 {
		fmt.Println("Excluded files:")
		for _, d := range decisions {
			if d.Status == "excluded" {
				fmt.Printf("  [%s] %s (%s)\n", d.MatchedPattern, d.Path, d.Reason)
			}
		}
		fmt.Println()
	}

	if unsupported > 0 {
		fmt.Println("Unsupported files:")
		for _, d := range decisions {
			if d.Status == "unsupported" {
				fmt.Printf("  [%s] %s (%s)\n", d.Language, d.Path, d.Reason)
			}
		}
		fmt.Println()
	}

	fmt.Println("Included files (first 10):")
	count := 0
	for _, d := range decisions {
		if d.Status == "included" && count < 10 {
			fmt.Printf("  %s (%s)\n", d.Path, d.Language)
			count++
		}
	}
	if included > 10 {
		fmt.Printf("  ... and %d more\n", included-10)
	}
}

func cmdExplainOne(eng *engine.Engine, path, format string) {
	relPath, err := filepath.Rel(".", path)
	if err != nil {
		relPath = path
	}

	decision := eng.ExplainPath(relPath)

	if format == "json" {
		data, err := json.MarshalIndent(decision, "", "  ")
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(append(data, '\n'))
		return
	}

	fmt.Printf("Path: %s\n", decision.Path)
	fmt.Printf("  Status:        %s\n", decision.Status)
	fmt.Printf("  Reason:        %s\n", decision.Reason)
	fmt.Printf("  Language:      %s\n", decision.Language)
	if decision.MatchedPattern != "" {
		fmt.Printf("  Matched:       %s\n", decision.MatchedPattern)
	}
	if decision.Err != nil {
		fmt.Printf("  Error:         %v\n", decision.Err)
	}

	if len(decision.RuleIDs) > 0 {
		fmt.Printf("  Rules (%d):\n", len(decision.RuleIDs))
		for i, ruleID := range decision.RuleIDs {
			if i >= 10 {
				fmt.Printf("    ... and %d more\n", len(decision.RuleIDs)-10)
				break
			}
			source := decision.PolicySource[ruleID]
			fmt.Printf("    - %s (source: %s)\n", ruleID, source)
		}
	}
}
