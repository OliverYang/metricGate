// Architecture checking: the whole-graph half of a metricgate run.
//
// Every other stage looks at one file at a time; a dependency direction only
// becomes visible once the packages are assembled. This file bridges the
// whole-graph stage (internal/arch) into the unified findings pipeline, so
// layer violations, forbidden edges and import cycles flow through the same
// gate, SARIF report, suppression directives and trend store as everything
// else.
package engine

import (
	"path/filepath"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/arch"
	"metricgate/internal/config"
	"metricgate/internal/model"
	"metricgate/internal/parser"
	"metricgate/internal/rules"
)

// archFileOf describes one parsed file for the dependency graph. Go packages
// are identified by their directory (their import path is not written in the
// file); Java packages by their package clause, normalised to slashes.
func archFileOf(lang parser.LanguageID, root, path string, src []byte, tree *sitter.Tree) arch.File {
	raw := arch.Extract(lang, src, tree.RootNode())
	pkg := raw.Package
	java := lang == parser.LangJava
	if !java {
		pkg = packageID(root, path)
	}
	return arch.File{Name: path, Pkg: pkg, Java: java, Imports: raw.Imports}
}

// packageID is the graph node a Go file contributes: its directory relative to
// the run root, so a policy can name a package without repeating the module
// path that the imports carry.
func packageID(root, path string) string {
	dir := filepath.Dir(path)
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return filepath.ToSlash(dir)
	}
	return filepath.ToSlash(rel)
}

// checkArchitecture evaluates the policy's architecture section over the run's
// package graph, and returns the findings plus how many were suppressed by an
// inline directive on the import line.
func (e *Engine) checkArchitecture(files []arch.File, supsByFile map[string][]suppression) ([]model.Finding, int) {
	policy := e.cfg.ArchPolicy()
	if policy.Empty() || len(files) < 2 {
		return nil, 0
	}
	var (
		findings []model.Finding
		ignored  int
	)
	for _, v := range arch.Check(arch.Build(files), policy) {
		finding := e.archFinding(v)
		kept, n := suppress([]model.Finding{finding}, supsByFile[finding.File])
		if n > 0 {
			ignored += n
			continue
		}
		findings = append(findings, kept...)
	}
	return findings, ignored
}

// archFinding turns one violation into the shared finding model: same source
// tag and severity semantics as any rule finding.
func (e *Engine) archFinding(v arch.Violation) model.Finding {
	kind := "edges"
	if v.RuleID == arch.RuleImportCycle {
		kind = "cycles"
	}
	line := v.Line
	if line < 1 {
		line = 1
	}
	return model.Finding{
		RuleID:    v.RuleID,
		Severity:  e.cfg.ArchSeverity(kind),
		Source:    model.SrcRule,
		File:      v.File,
		StartLine: line,
		EndLine:   line,
		Message:   v.Message,
	}
}

// archRuleMetas registers the architecture checks in the SARIF rule catalogue,
// when (and only when) the policy asks for them.
func archRuleMetas(cfg *config.Config) []rules.Meta {
	if cfg.ArchPolicy().Empty() {
		return nil
	}
	return []rules.Meta{
		{ID: arch.RuleLayerViolation, Title: "Dependency crosses a declared layer boundary"},
		{ID: arch.RuleForbiddenEdge, Title: "Dependency direction forbidden by policy"},
		{ID: arch.RuleImportCycle, Title: "Package import cycle"},
	}
}
