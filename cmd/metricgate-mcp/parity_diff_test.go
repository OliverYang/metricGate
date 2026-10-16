package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"metricgate/internal/model"
)

// runMCP asks the real server, over its stdio transport, to gate a root. Going
// over the wire rather than calling the handler in process is the point: the
// parity being claimed is between the two surfaces an operator actually uses,
// so the transport, the schema binding and the JSON shape are all part of what
// this checks.
func runMCP(t *testing.T, mcpBin, dir, root string) gateReport {
	t.Helper()
	out := startMCP(t, mcpBin, dir).callCheckPath(root)
	return gateReport{
		Findings: out.Findings, Files: out.Files, Errors: out.Errors,
		Warnings: out.Warnings, Hidden: out.Hidden, Unchanged: out.Unchanged,
	}
}

// TestDiffOnlyMatchesCLIOnEveryRootShape compares the two gate surfaces on both
// root shapes, because the two shapes are not equivalent.
//
// At the worktree root, the run-relative finding paths and the base-snapshot
// paths coincide, so the full ratchet fires and metric findings whose value did
// not move are dropped. Under a subdirectory, engine.Run keys findings relative
// to the run root while baseline.Snapshot keys the base values relative to the
// worktree, so the join misses and the value half of the ratchet degrades to
// keep-all -- pre-existing in the CLI, and reached identically by both surfaces
// now that they share one function. See the 遗留 note in
// docs/iterations/iter-20-mcp-cli-ratchet-parity.md. Testing only the root would
// hide that; testing only the subdirectory would let a divergence at the root,
// where both surfaces are actually used, slip past.
func TestDiffOnlyMatchesCLIOnEveryRootShape(t *testing.T) {
	releaseBuildGuard(t)
	cliBin, mcpBin := buildGatePair(t)
	dir := newDirtyTree(t)

	for _, root := range []string{".", "basepkg"} {
		t.Run("root="+root, func(t *testing.T) {
			cli := runCLI(t, cliBin, dir, root)
			mcp := runMCP(t, mcpBin, dir, root)
			assertSameGate(t, cli, mcp)
			assertNonVacuous(t, root, cli)
		})
	}
}

// assertSameGate is the acceptance check for iter-20: for one tree and one base
// revision, the two gate surfaces must hand back the same survivors and the same
// account of what they filtered.
func assertSameGate(t *testing.T, cli, mcp gateReport) {
	t.Helper()

	if cli.Files == 0 || mcp.Files == 0 {
		t.Fatalf("one surface measured no files (CLI=%d MCP=%d); nothing was gated", cli.Files, mcp.Files)
	}
	if cli.Files != mcp.Files {
		t.Errorf("different file counts for the same root: CLI=%d MCP=%d", cli.Files, mcp.Files)
	}

	onlyCLI, onlyMCP := diffTuples(tuplesOf(cli.Findings), tuplesOf(mcp.Findings))
	if len(onlyCLI) > 0 || len(onlyMCP) > 0 {
		t.Errorf("the two entry points gated the same tree differently (base=HEAD):\n  CLI only: %s\n  MCP only: %s\n  CLI=%d findings, MCP=%d findings",
			formatTuples(onlyCLI), formatTuples(onlyMCP), len(cli.Findings), len(mcp.Findings))
	}

	// The counters are compared as well. A surface could report the same
	// survivors while hiding a different number of findings, and `hidden` plus
	// `unchanged` are exactly the fields that say how much the gate declined to
	// show -- a clean verdict with a wrong counter is still a lie.
	if mcp.Hidden != cli.Hidden {
		t.Errorf("hidden count: MCP=%d CLI=%d", mcp.Hidden, cli.Hidden)
	}
	if mcp.Unchanged != cli.Unchanged {
		t.Errorf("unchanged count: MCP=%d CLI=%d", mcp.Unchanged, cli.Unchanged)
	}
	if mcp.Errors != cli.Errors || mcp.Warnings != cli.Warnings {
		t.Errorf("severity counts: MCP errors=%d warnings=%d, CLI errors=%d warnings=%d",
			mcp.Errors, mcp.Warnings, cli.Errors, cli.Warnings)
	}
}

// assertNonVacuous fails when a passing comparison would prove nothing: if the
// fixture produced nothing to filter, "the two surfaces agree" is only a
// statement about empty sets.
func assertNonVacuous(t *testing.T, root string, cli gateReport) {
	t.Helper()
	if cli.Hidden == 0 {
		t.Errorf("no pre-existing finding was hidden by the line filter; the fixture does not exercise it")
	}
	if len(cli.Findings) == 0 {
		t.Error("the filtered run reported no findings at all; there was no signal to gate")
	}
	// The value-delta half only survives at the worktree root, so that is the
	// shape where its absence is a defect rather than a documented degradation.
	if root == "." && cli.Unchanged == 0 {
		t.Errorf("no metric finding was dropped as unchanged, so this run cannot distinguish the "+
			"value-delta ratchet from the line-only filter: %d findings kept, %d hidden",
			len(cli.Findings), cli.Hidden)
	}
}

// TestDiffOnlyDropsUnchangedMetrics pins the half of the story a finding-set
// comparison cannot tell: at the worktree root a metric finding whose value did
// not move must be dropped. Without it, "parity" could be reached by making the
// CLI stop applying the baseline -- the direction that would quietly weaken the
// gate while turning this test green.
func TestDiffOnlyDropsUnchangedMetrics(t *testing.T) {
	releaseBuildGuard(t)
	cliBin, _ := buildGatePair(t)
	dir := newDirtyTree(t)

	full := runFullCLI(t, cliBin, dir, ".")
	diff := runCLI(t, cliBin, dir, ".")
	if len(full.Findings) <= len(diff.Findings) {
		t.Fatalf("the ratchet filtered nothing: full=%d diff=%d", len(full.Findings), len(diff.Findings))
	}
	if diff.Unchanged == 0 {
		t.Errorf("full run has %d findings, -diff has %d, but 0 metrics were dropped as unchanged: "+
			"the whole gap was attributed to the line filter alone", len(full.Findings), len(diff.Findings))
	}
	if dropped := unchangedMetricDrops(full, diff); len(dropped) == 0 {
		t.Errorf("no metric finding on the dirtied file disappeared from the filtered run, so the "+
			"value-delta path never fired (full=%d diff=%d unchanged=%d)",
			len(full.Findings), len(diff.Findings), diff.Unchanged)
	}
	// b.go is untouched, so none of its findings may survive.
	for _, f := range diff.Findings {
		if strings.Contains(filepath.ToSlash(f.File), "b.go") {
			t.Errorf("untouched file survived -diff: %s:%d %s", f.File, f.StartLine, f.RuleID)
		}
	}
}

// unchangedMetricDrops lists the metric-sourced findings present in the full run
// and absent from the filtered one, restricted to the file the fixture dirtied:
// a finding on that file cannot have been hidden by the line filter, so its
// absence is the value comparison's work.
func unchangedMetricDrops(full, diff gateReport) []string {
	kept := map[string]bool{}
	for _, f := range diff.Findings {
		kept[findingKey(f)] = true
	}
	var drops []string
	for _, f := range full.Findings {
		if f.Source != model.SrcMetric || !strings.Contains(filepath.ToSlash(f.File), "a.go") {
			continue
		}
		if !kept[findingKey(f)] {
			drops = append(drops, fmt.Sprintf("%s:%d", f.RuleID, f.StartLine))
		}
	}
	return drops
}

func findingKey(f model.Finding) string {
	return fmt.Sprintf("%s|%s|%d", f.RuleID, filepath.ToSlash(f.File), f.StartLine)
}
