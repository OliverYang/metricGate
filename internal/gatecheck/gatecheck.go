// Package gatecheck turns the two report-only acceptance checks into gate
// findings: impact scope (LC-403) and tests-touched (LC-404). It is
// deliberately separate from internal/impact and internal/scorecard — the
// gate layer decides what a report means, not what it contains.
package gatecheck

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"metricgate/internal/arch"
	"metricgate/internal/config"
	"metricgate/internal/diff"
	"metricgate/internal/impact"
	"metricgate/internal/model"
	"metricgate/internal/refs"
	"metricgate/internal/symbols"
)

// RuleIDs for the two gate-ized checks; severity overrides live under
// languages.<lang>.rules.<id>.severity like any other rule.
const (
	RuleIDImpactScope  = "impact-scope"
	RuleIDTestsTouched = "tests-touched"
)

// DefaultMaxAffectedFiles is the blast-radius cap when policy does not set
// one. Kept generous: the gate's first job is to catch runaway changes, not
// to police everyday edits.
const DefaultMaxAffectedFiles = 25

// Options carries the resolved policy knobs plus the shared inputs.
type Options struct {
	// MaxAffectedFiles caps the blast radius; 0 means use the default and a
	// negative value disables the check entirely.
	MaxAffectedFiles int
	ImpactSeverity   model.Severity // resolved from policy; error by default
	TestsSeverity    model.Severity // resolved from policy; warning by default
	WantImpact       bool
	WantTests        bool
	Changed          map[string][]diff.Range
	Root             string
}

// ChangedSymbols lists, deterministically, the symbols defined in changed
// files — the units whose blast radius the change moves.
func ChangedSymbols(defs *symbols.Index, changed map[string][]diff.Range) []string {
	if len(changed) == 0 {
		return nil
	}
	changedPaths := make(map[string]bool, len(changed))
	for p := range changed {
		changedPaths[p] = true
	}
	var names []string
	seen := make(map[string]bool)
	for _, d := range defs.All() {
		if !changedPaths[d.File] || seen[d.Name] {
			continue
		}
		seen[d.Name] = true
		names = append(names, d.Name)
	}
	sort.Strings(names)
	return names
}

// BlastRadiusCheck reports one finding per changed symbol whose affected
// file count exceeds the cap. The finding anchors on the symbol's definition
// line, with the head of the affected-file list carried in the message so
// the repair channel knows the scope without a second command.
func BlastRadiusCheck(defs *symbols.Index, uses *refs.Index, opt Options) []model.Finding {
	limit := opt.MaxAffectedFiles
	if limit == 0 {
		limit = DefaultMaxAffectedFiles
	}
	if limit < 0 || !opt.WantImpact {
		return nil
	}
	symNames := ChangedSymbols(defs, opt.Changed)
	if len(symNames) == 0 {
		return nil
	}
	sev := opt.ImpactSeverity
	if sev == "" {
		sev = model.SevError
	}
	var findings []model.Finding
	for _, name := range symNames {
		report := impact.Compute(name, defs, uses)
		if len(report.AffectedFiles) <= limit {
			continue
		}
		def := firstDef(report)
		findings = append(findings, model.Finding{
			RuleID:    RuleIDImpactScope,
			Severity:  sev,
			Source:    model.SrcRule,
			File:      def.File,
			StartLine: max(1, def.Line), EndLine: max(1, def.Line),
			Message: fmt.Sprintf("impact scope of %q exceeds the policy cap: %d affected files > %d (%s); narrow the change or raise the cap with an audited reason (LC-403)",
				name, len(report.AffectedFiles), limit, strings.Join(head(report.AffectedFiles, 5), ", ")),
			Evidence: &model.Evidence{Anchor: model.AnchorLineOnly},
		})
	}
	return findings
}

// firstDef picks the deterministic anchor definition for a report.
func firstDef(r impact.Report) symbols.Def {
	if len(r.Definitions) == 0 {
		return symbols.Def{}
	}
	best := r.Definitions[0]
	for _, d := range r.Definitions[1:] {
		if d.File < best.File || (d.File == best.File && d.Line < best.Line) {
			best = d
		}
	}
	return best
}

func head(files []string, n int) []string {
	if len(files) <= n {
		return files
	}
	return files[:n]
}

// TestsTouchedCheck reports one finding when logic files changed but no test
// file did. Default severity is warning (observation period); teams tighten
// it to error via the usual severity override.
func TestsTouchedCheck(opt Options) []model.Finding {
	if !opt.WantTests || len(opt.Changed) == 0 {
		return nil
	}
	if hasTestChange(opt.Changed) {
		return nil
	}
	files := sortedChangedLogic(opt.Changed)
	if len(files) == 0 {
		return nil
	}
	sev := opt.TestsSeverity
	if sev == "" {
		sev = model.SevWarning
	}
	return []model.Finding{{
		RuleID:    RuleIDTestsTouched,
		Severity:  sev,
		Source:    model.SrcRule,
		File:      files[0],
		StartLine: 1, EndLine: 1,
		Message: fmt.Sprintf("logic files changed but no test file did: %s; add or update a test covering the behaviour change (LC-404)",
			strings.Join(head(files, 4), ", ")),
		Evidence: &model.Evidence{Anchor: model.AnchorLineOnly},
	}}
}

func hasTestChange(changed map[string][]diff.Range) bool {
	for path := range changed {
		if isTestPath(path) {
			return true
		}
	}
	return false
}

func sortedChangedLogic(changed map[string][]diff.Range) []string {
	var out []string
	for path := range changed {
		if !isTestPath(path) {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// isTestPath is the same cross-language heuristic as scorecard: Go's
// _test.go suffix, a /test/ path segment, or a Test.java suffix.
func isTestPath(path string) bool {
	base := filepath.Base(path)
	if strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "Test.java") {
		return true
	}
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		if seg == "test" || seg == "tests" {
			return true
		}
	}
	return false
}

// Resolve reads the two rules' policy knobs: veto state, the impact cap
// (languages.<lang>.rules.impact-scope.options.max_affected_files) and the
// severities. The CLI flags decide activation; policy can only refine or
// veto (explicit enabled:false). Both gates are repo-wide, so the first
// configured language wins.
func Resolve(c *config.Config) (opts Options) {
	opts.MaxAffectedFiles = 0 // 0 = default inside the checks
	opts.WantImpact = true
	opts.WantTests = true
	for _, lang := range []string{"go", "java"} {
		sec, ok := c.Languages[lang]
		if !ok {
			continue
		}
		resolveImpact(sec, c, lang, &opts)
		resolveTests(sec, c, lang, &opts)
		return opts
	}
	return opts
}

// resolveImpact reads impact-scope policy knobs into opts.
func resolveImpact(sec config.LanguageConfig, c *config.Config, lang string, opts *Options) {
	rc, ok := sec.Rules[RuleIDImpactScope]
	if !ok {
		return
	}
	if rc.Enabled != nil && !*rc.Enabled {
		opts.WantImpact = false
	}
	if v, ok := rc.Options["max_affected_files"]; ok {
		if n, ok := toInt(v); ok {
			opts.MaxAffectedFiles = n
		}
	}
	if sev := c.RuleSeverity(lang, RuleIDImpactScope); sev != "" {
		opts.ImpactSeverity = model.Severity(sev)
	}
}

// resolveTests reads tests-touched policy knobs into opts.
func resolveTests(sec config.LanguageConfig, c *config.Config, lang string, opts *Options) {
	rc, ok := sec.Rules[RuleIDTestsTouched]
	if !ok {
		return
	}
	if rc.Enabled != nil && !*rc.Enabled {
		opts.WantTests = false
	}
	if sev := c.RuleSeverity(lang, RuleIDTestsTouched); sev != "" {
		opts.TestsSeverity = model.Severity(sev)
	}
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	}
	return 0, false
}

// RuleIDArchDrift is the third gate-ized check: the working tree's package
// dependency shape (arch snapshot) differs from the blessed snapshot. Unlike
// impact-scope/tests-touched it is not diff-scoped — a shape change is
// repo-wide by definition.
const RuleIDArchDrift = "arch-drift"

// DefaultArchSeverity is deliberately error: the whole point of wiring
// arch diff into the gate is that drift blocks until a human re-blesses
// (`arch snapshot`) or reverts. Policy may relevel to warning for an
// observation period.
const DefaultArchSeverity = model.SevError

// ArchDriftCheck turns a snapshot diff into gate findings — one per drift
// item, so the repair channel sees exactly which directions/packages moved.
// severity is resolved by the caller (policy may relevel).
func ArchDriftCheck(d arch.Diff, sev model.Severity) []model.Finding {
	if sev == "" {
		sev = DefaultArchSeverity
	}
	items := len(d.Added) + len(d.Removed) + len(d.PkgsNew) + len(d.PkgsGone)
	if items == 0 {
		return nil
	}
	finding := func(msg string) model.Finding {
		return model.Finding{
			RuleID:   RuleIDArchDrift,
			Severity: sev,
			Source:   model.SrcRule,
			File:     "(architecture)", // repo-level: no single file owns a shape change
			Message:  msg,
		}
	}
	var out []model.Finding
	for _, e := range d.Added {
		out = append(out, finding(fmt.Sprintf("architecture drift: new dependency direction %s -> %s; if intended, re-bless with `metricgate arch snapshot`, otherwise revert", e.From, e.To)))
	}
	for _, e := range d.Removed {
		out = append(out, finding(fmt.Sprintf("architecture drift: dependency direction removed %s -> %s; if intended (decoupling), re-bless with `metricgate arch snapshot`", e.From, e.To)))
	}
	for _, p := range d.PkgsNew {
		out = append(out, finding(fmt.Sprintf("architecture drift: new package %s is not in the blessed shape; declare it in policy layers or re-bless", p)))
	}
	for _, p := range d.PkgsGone {
		out = append(out, finding(fmt.Sprintf("architecture drift: package %s disappeared from the blessed shape; re-bless if the removal is intended", p)))
	}
	return out
}
