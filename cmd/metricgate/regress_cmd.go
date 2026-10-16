// Diff-mode add-ons that need the base revision: the "metric got worse"
// findings appended to a -diff run.
//
// The base-revision measurement itself does not live here: it is shared with
// the MCP entry point and lives in internal/baseline, so the two gates cannot
// answer "is this new?" from different numbers. What stays in the CLI shell is
// the part that turns deltas into human-facing findings.
package main

import (
	"metricgate/internal/baseline"
	"metricgate/internal/config"
	"metricgate/internal/diff"
	"metricgate/internal/model"
	"metricgate/internal/regress"
)

// applyRegressions appends "metric got worse" findings for the scopes the
// change touched and returns how many were added. Only measurements that
// intersect the diff are considered, so unrelated drift never surfaces.
//
// It returns the count rather than writing it, so the CLI shell owns every
// byte of human-facing output.
func applyRegressions(cfg *config.Config, base, root string, summary *model.RunSummary, changed map[string][]diff.Range) (int, error) {
	baseMetrics, err := baseline.FullSnapshot(cfg, base)
	if err != nil {
		return 0, err
	}
	current := baseline.RelativizeMetrics(summary.Metrics, root)
	deltas := regress.FilterToChanged(regress.Compare(baseMetrics, current), changed)
	findings := regress.Findings(deltas, model.SevWarning)
	summary.Findings = append(summary.Findings, findings...)
	model.SortFindings(summary.Findings)
	return len(findings), nil
}
