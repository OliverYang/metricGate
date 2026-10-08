// Package regress compares two metric snapshots and reports the measurements
// that got worse between a base revision and the working tree.
//
// It is the "regression" half of diff-aware gating. metricgate's -diff mode
// answers "did you introduce a violation?"; this package answers "did you
// make an existing measurement worse?" — a function whose cognitive
// complexity rises from 8 to 9 is a regression even though it never crossed
// a threshold, and a naive threshold gate would stay silent about it.
//
// Everything here is pure: snapshots in, deltas out. Producing the base
// snapshot (git archive + a second engine run) is the caller's job, so this
// package stays free of any process boundary.
package regress

import (
	"cmp"
	"fmt"
	"strings"

	"metricgate/internal/diff"
	"metricgate/internal/metrics"
	"metricgate/internal/model"
	"metricgate/internal/rules"
)

// RuleID is the finding id for a measurement that got worse.
const RuleID = "metric-regression"

// Meta is the SARIF rule descriptor for the regression check. The engine
// itself never emits these findings — they need a base snapshot, which only
// the CLI can build — but the meta must exist so a summary that carries them
// still has a rule descriptor.
func Meta() rules.Meta {
	return rules.Meta{ID: RuleID, Title: "Metric got worse than the base revision"}
}

// Delta is one measurement that got worse between the base revision and the
// working tree. Base and Current are the raw snapshot values (for
// maintainability they are the stored debt, not the MI).
type Delta struct {
	Metric    string `json:"metric"`
	File      string `json:"file"`
	Scope     string `json:"scope,omitempty"`
	Base      int    `json:"base"`
	Current   int    `json:"current"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
}

// key identifies one measured value: the same file + scope + metric triple.
type key struct {
	file, scope, metric string
}

func keyOf(m model.MetricResult) key { return key{m.File, m.Scope, m.Metric} }

// Compare returns the regressions between two snapshots: measurements present
// in both whose current value is strictly greater than the base value.
//
// Values that only appear (new code) or only disappear (deleted code) are not
// regressions: new code has no base to regress from, and a removed function
// cannot be "worse". The output order is deterministic so two runs over the
// same inputs emit identical findings.
func Compare(base, current []model.MetricResult) []Delta {
	before := make(map[key]int, len(base))
	for _, m := range base {
		before[keyOf(m)] = m.Value
	}
	var out []Delta
	for _, m := range current {
		prev, ok := before[keyOf(m)]
		if !ok || m.Value <= prev {
			continue
		}
		out = append(out, Delta{
			Metric: m.Metric, File: m.File, Scope: m.Scope,
			Base: prev, Current: m.Value,
			StartLine: m.StartLine, EndLine: m.EndLine,
		})
	}
	model.SortByKeys(out,
		func(a, b Delta) int { return strings.Compare(a.File, b.File) },
		func(a, b Delta) int { return cmp.Compare(a.StartLine, b.StartLine) },
		func(a, b Delta) int { return strings.Compare(a.Metric, b.Metric) },
		func(a, b Delta) int { return strings.Compare(a.Scope, b.Scope) },
	)
	return out
}

// FilterToChanged keeps only the deltas whose measured scope intersects a
// changed range: a regression is reported when the agent touched the code it
// made worse ("you touched it, you own it"), not when unrelated code drifted.
func FilterToChanged(deltas []Delta, changed map[string][]diff.Range) []Delta {
	var out []Delta
	for _, d := range deltas {
		if diff.Intersects(changed[d.File], d.StartLine, d.EndLine) {
			out = append(out, d)
		}
	}
	return out
}

// Findings renders deltas as findings, anchored at the measured scope. The
// anchor is honestly line_only: a metric is not an AST node, so there is no
// byte span to hash.
func Findings(deltas []Delta, sev model.Severity) []model.Finding {
	out := make([]model.Finding, 0, len(deltas))
	for _, d := range deltas {
		out = append(out, model.Finding{
			RuleID: RuleID, Severity: sev, Source: model.SrcMetric,
			File:      d.File,
			StartLine: max(1, d.StartLine), EndLine: max(1, d.EndLine),
			Message:  message(d),
			Evidence: &model.Evidence{Anchor: model.AnchorLineOnly},
		})
	}
	return out
}

// message renders one regression line. Maintainability is special-cased: its
// snapshot is the debt (100-MI), so "rose" is correct for the debt but the
// reader also wants the MI that fell.
func message(d Delta) string {
	where := d.Scope
	if where == "" {
		where = d.File
	}
	if d.Metric == metrics.MetricMaintainability {
		return fmt.Sprintf("maintainability debt of %q rose %d → %d (MI %d → %d); the change made it harder to maintain",
			where, d.Base, d.Current, 100-d.Base, 100-d.Current)
	}
	return fmt.Sprintf("%s of %q rose %d → %d (regression vs base); bring it back down or justify it",
		d.Metric, where, d.Base, d.Current)
}
