// Regression detection for the check command: the "did this change make an
// existing measurement worse?" half of diff-aware gating.
//
// It needs a second engine run over the base revision, which is why it lives
// in the CLI shell rather than in the engine: building the base tree is a git
// operation, and the engine stays a pure library with no process boundary.
package main

import (
	"metricgate/internal/config"
	"metricgate/internal/diff"
	"metricgate/internal/engine"
	"metricgate/internal/git"
	"metricgate/internal/model"
	"metricgate/internal/regress"
	"metricgate/internal/selection"
)

// baseMetricSnapshot runs the engine over the tree at the base revision and
// returns its metric snapshots with root-relative paths.
//
// The base tree comes from `git archive`, so it is the committed state rather
// than the working tree — an uncommitted change correctly shows up as a
// regression instead of being compared against itself.
func baseMetricSnapshot(cfg *config.Config, base string) ([]model.MetricResult, error) {
	dir, cleanup, err := git.Archive("", git.ResolveBase("", base))
	if err != nil {
		return nil, err
	}
	defer cleanup()
	eng, err := engine.New(cfg)
	if err != nil {
		return nil, err
	}
	defer eng.Close()
	summary, err := eng.Run(dir)
	if err != nil {
		return nil, err
	}
	return relativizeMetrics(summary.Metrics, dir), nil
}

// relativizeMetrics rewrites every snapshot path relative to root, so the
// base and working-tree snapshots share one key space (and no machine
// absolute path survives the comparison).
func relativizeMetrics(ms []model.MetricResult, root string) []model.MetricResult {
	out := make([]model.MetricResult, len(ms))
	for i, m := range ms {
		m.File = selection.SanitizePath(root, m.File)
		out[i] = m
	}
	return out
}

// applyRegressions appends "metric got worse" findings for the scopes the
// change touched and returns how many were added. Only measurements that
// intersect the diff are considered, so unrelated drift never surfaces.
//
// It returns the count rather than writing it, so the CLI shell owns every
// byte of human-facing output.
func applyRegressions(cfg *config.Config, base, root string, summary *model.RunSummary, changed map[string][]diff.Range) (int, error) {
	baseMetrics, err := baseMetricSnapshot(cfg, base)
	if err != nil {
		return 0, err
	}
	current := relativizeMetrics(summary.Metrics, root)
	deltas := regress.FilterToChanged(regress.Compare(baseMetrics, current), changed)
	findings := regress.Findings(deltas, model.SevWarning)
	summary.Findings = append(summary.Findings, findings...)
	model.SortFindings(summary.Findings)
	return len(findings), nil
}
