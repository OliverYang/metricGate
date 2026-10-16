package main

import (
	"strings"
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/engine"
	"metricgate/internal/gittest"
	"metricgate/internal/model"
	"path/filepath"
)

// TestRunCheckAddOnsReturnsTheFilteredSummary pins the wiring between the
// -diff filter and everything downstream of it. An earlier refactor of the
// check pipeline into check_cmd.go dropped the summary reassignment, so the
// filter still computed the right split and printed "0 new findings, 1
// pre-existing hidden", but the gate, the console report and the JSON
// envelope were all handed the unfiltered summary — a run that contradicted
// itself in two consecutive lines.
func TestRunCheckAddOnsReturnsTheFilteredSummary(t *testing.T) {
	root := gittest.InitRepo(t)
	gittest.Write(t, root, ".metricgate.yaml", "gate:\n  fail_on: error\nlanguages:\n  typescript:\n    metrics:\n      cognitive-complexity: {warn: 2, error: 2}\n")
	gittest.Write(t, root, "src/app.ts", "export function handle(input: string) {\n  if (input.includes(\"a\")) {\n    if (input.includes(\"b\")) {\n      return input.trim();\n    }\n  }\n  return \"\";\n}\n")
	gittest.Run(t, root, "add", "-A")
	gittest.Run(t, root, "commit", "-qm", "base")

	cfg, err := config.LoadFile(filepath.Join(root, ".metricgate.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	summary, err := eng.Run(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := summary.Count(model.SevError); got != 1 {
		t.Fatalf("fixture must carry one error finding, got %d", got)
	}

	// A clean tree: the only finding in the file predates HEAD, so the diff
	// filter must hide it and the gate must not see it.
	var out strings.Builder
	filtered := runCheckAddOns(cfg, eng, root, "HEAD", summary, &out, checkAddOnOpts{diffMode: true})
	if filtered == nil {
		t.Fatal("runCheckAddOns must return the summary the caller should keep using")
	}
	if got := len(filtered.Findings); got != 0 {
		t.Errorf("filtered summary keeps %d findings, want 0: the finding predates HEAD", got)
	}
	if !strings.Contains(out.String(), "1 pre-existing hidden") {
		t.Errorf("diff-only line should name the hidden finding: %s", out.String())
	}
}

// TestRunCheckAddOnsWithoutDiffKeepsTheSummary pins the other branch: with no
// -diff the same call must return the caller's own summary untouched.
func TestRunCheckAddOnsWithoutDiffKeepsTheSummary(t *testing.T) {
	cfg, _ := config.Default()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	summary, err := eng.Run(".")
	if err != nil {
		t.Fatal(err)
	}
	got := runCheckAddOns(cfg, eng, ".", "HEAD", summary, &strings.Builder{}, checkAddOnOpts{})
	if got != summary {
		t.Error("without -diff the caller's summary must come back unchanged")
	}
}
