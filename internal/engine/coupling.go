// Change-coupling gate: the process-level half of the architecture check.
//
// internal/arch asks "is this dependency direction legal?" over the import
// graph. This asks a question the graph cannot: "are these two files moving
// as one unit?" A cross-layer pair that habitually changes together is
// architectural erosion already in progress — the layering rule still passes,
// and the boundary is gone in practice.
//
// The findings ride the shared pipeline (same severities, suppression
// directives, SARIF catalogue and trend store as everything else). Unlike the
// per-file stages this one needs git history, so it lives behind -diff and a
// policy opt-in, and it refuses to report from a shallow clone.
package engine

import (
	"sort"
	"strconv"

	"metricgate/internal/config"
	"metricgate/internal/coupling"
	"metricgate/internal/diff"
	"metricgate/internal/model"
	"metricgate/internal/rules"
)

// RuleIDCoupling is the rule reported for a cross-layer co-change pair.
const RuleIDCoupling = "change-coupling"

// CouplingEnabled reports whether the policy turned the change-coupling gate
// on. The CLI asks before doing any history work, so a repository that never
// opted in pays nothing.
func (e *Engine) CouplingEnabled() bool { return e.cfg.Architecture.Coupling.Enabled }

// CouplingSince is the history window the gate analyzes (empty = all history).
// The CLI needs it to build the git port without reading the policy itself.
func (e *Engine) CouplingSince() string { return e.cfg.Architecture.Coupling.Since }

// CouplingHistory is the git history the gate reasons over, behind a port so
// the engine core never links the git adapter.
//
// The layering rule this satisfies is not bureaucracy: internal/git shells out
// to a process, and the whole point of the core/adapter split is that the
// checker core stays a pure library. Coupling is a second consumer of the same
// history (hotspots is the first), so the seam already had a natural shape.
//
// The port speaks coupling.Commit, not git.Commit, so that the core links only
// the pure analysis package: an adapter type in the signature would drag the
// adapter back into the core's import graph.
type CouplingHistory interface {
	// IsShallow reports whether the repository has truncated history.
	IsShallow() bool
	// Commits returns the commits inside the window, newest first.
	Commits() ([]coupling.Commit, error)
}

// CouplingFindings reports the changed files that habitually change together
// across a declared layer boundary. Only pairs with BOTH files in the current
// change set are reported: the gate's unit is the change under review, so a
// pre-existing coupling elsewhere in the repository is not this change's
// problem. That also makes the check naturally -diff-shaped.
//
// root is the repository root (the history and the layer patterns are read
// from there), changed is the -diff change set, and history is the injected
// git port (nil means the caller never wired one, so the gate stays quiet).
func (e *Engine) CouplingFindings(root string, changed map[string][]diff.Range, history CouplingHistory) []model.Finding {
	gate := e.cfg.Architecture.Coupling
	if !gate.Enabled || history == nil {
		return nil
	}
	if history.IsShallow() {
		// Refusing is the point: a shallow clone would silently undercount
		// every pair, and an undercounted gate is a gate that lies.
		return nil
	}
	commits, err := history.Commits()
	if err != nil {
		return nil
	}
	report := coupling.Analyze(commits, coupling.Options{
		MaxCommitFiles: gate.MaxCommitFiles,
		MinCount:       gate.MinCount,
		Exclude:        e.cfg.Architecture.CouplingExclude,
		LayerOf:        coupling.LayerResolver(e.cfg.Architecture.Layers),
		// The gate's unit is the boundary crossing, so the analysis keeps only
		// cross-layer pairs; the change set filters them further.
		CrossLayerOnly: true,
		Max:            0, // every pair; the change set is the filter
	})
	return couplingToFindings(report, changed, e.cfg.CouplingSeverity())
}

// couplingToFindings keeps the cross-layer pairs whose two files are both in
// the change set, one finding per pair, anchored on the first file.
func couplingToFindings(report coupling.Report, changed map[string][]diff.Range, sev model.Severity) []model.Finding {
	var out []model.Finding
	for _, p := range report.Pairs {
		if !p.CrossLayer || !bothChanged(p, changed) {
			continue
		}
		out = append(out, model.Finding{
			RuleID:    RuleIDCoupling,
			Severity:  sev,
			Source:    model.SrcRule,
			File:      p.A,
			StartLine: 1,
			EndLine:   1,
			Message:   couplingMessage(p),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Message < out[j].Message
	})
	return out
}

// bothChanged reports whether the pair straddles the change set. A file in
// the change set with no line ranges is a whole-file change (a new file), so
// presence is the test, not a non-empty range list.
func bothChanged(p coupling.Pair, changed map[string][]diff.Range) bool {
	_, okA := changed[p.A]
	_, okB := changed[p.B]
	return okA && okB
}

// couplingMessage states the observation and the boundary it crosses, in the
// terms the repair channel needs: the two files, how often they moved
// together, and the layer names.
func couplingMessage(p coupling.Pair) string {
	return "changed together in " + strconv.Itoa(p.Count) + " commits across the " +
		p.Layers[0] + " -> " + p.Layers[1] + " boundary without a declared dependency: " +
		p.A + " <-> " + p.B
}

// couplingRuleMeta registers the rule in the SARIF catalogue when the policy
// enables the gate, matching what the findings can contain.
func couplingRuleMeta(cfg *config.Config) []rules.Meta {
	if !cfg.Architecture.Coupling.Enabled {
		return nil
	}
	return []rules.Meta{{
		ID:    RuleIDCoupling,
		Title: "Files habitually change together across a layer boundary",
	}}
}
