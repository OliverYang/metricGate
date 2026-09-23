// Duplicate-code checking: the second whole-graph stage of a metricgate run.
//
// Like architecture checking, clone detection only makes sense across the
// run as a whole — a duplicate usually lives in a different file than its
// original. This file bridges internal/clones into the unified findings
// pipeline so function clones flow through the same gate, SARIF report,
// suppression directives and trend store as everything else.
package engine

import (
	"fmt"

	"metricgate/internal/clones"
	"metricgate/internal/config"
	"metricgate/internal/model"
	"metricgate/internal/rules"
)

// checkClones groups the run's extracted functions into clone classes and
// appends one finding per member that is not the class anchor. The anchor
// (sorted-first member) stays clean: it is the original every clone is
// reported against. Inline directives on the duplicate suppress it.
func (e *Engine) checkClones(summary *model.RunSummary, funcs []clones.Func, supsByFile map[string][]suppression) {
	cfg := e.cfg.ClonePolicy()
	if !cfg.Enabled() {
		return
	}
	for _, g := range clones.Find(funcs, cfg) {
		anchor := g.Funcs[0]
		for _, m := range g.Funcs[1:] {
			f := e.cloneFinding(anchor, m)
			kept, n := suppress([]model.Finding{f}, supsByFile[f.File])
			if n > 0 {
				summary.Ignored += n
				continue
			}
			summary.Findings = append(summary.Findings, kept...)
		}
	}
}

// cloneFinding turns one clone member into the shared finding model. The
// finding is anchored on the duplicate (m), pointing at the original.
func (e *Engine) cloneFinding(anchor, m clones.Func) model.Finding {
	return model.Finding{
		RuleID:    clones.RuleFunctionClone,
		Severity:  model.SevWarning,
		Source:    model.SrcRule,
		File:      m.File,
		StartLine: m.StartLine,
		EndLine:   m.EndLine,
		Message: fmt.Sprintf("function is a Type-2 clone of %s:%d (%s); reuse or parameterise it instead of duplicating",
			anchor.File, anchor.StartLine, anchor.Name),
	}
}

// cloneRuleMeta registers the duplicate-code check in the SARIF rule
// catalogue. The pass is on unless the policy disables it, so the meta is
// registered unconditionally (matching what the findings can contain).
func cloneRuleMeta(cfg *config.Config) rules.Meta {
	_ = cfg // severity is fixed at warning today; kept for symmetry with archRuleMetas
	return rules.Meta{
		ID:    clones.RuleFunctionClone,
		Title: "Function duplicates an existing function (Type-2 clone)",
	}
}
