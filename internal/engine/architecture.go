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

// archFileOf describes one parsed file for the dependency graph. A package is
// either declared in the file (Java, C#: the package/namespace clause; Python:
// see below) or implied by its location (Go, C/C++, TypeScript/JavaScript,
// Rust: the directory, because nothing in the file names its own import path).
func archFileOf(lang parser.LanguageID, root, path string, src []byte, tree *sitter.Tree) arch.File {
	raw := arch.Extract(lang, src, tree.RootNode())
	pkg := declaredPackage(lang, raw, root, path)
	return arch.File{Name: path, Pkg: pkg, Style: arch.ImportStyle(lang), Imports: raw.Imports}
}

// declaredPackage picks the graph node a file contributes: the declared
// package name when the language has one that is unambiguous, the file's
// directory otherwise.
//
// Python is the awkward case. It does declare `package.module` names, but they
// start at the import root, which is not known from the file alone — a
// directory-relative node at least matches the imports under a src/ root, and
// is the same approximation Go already lives with. So it takes the directory.
func declaredPackage(lang parser.LanguageID, raw arch.Raw, root, path string) string {
	switch lang {
	case parser.LangJava, parser.LangCS:
		return raw.Package
	default:
		return packageID(root, path)
	}
}

// typeCensus reports a file's type declarations for Martin's abstractness.
// The arch extractor already walks the top level for package and imports;
// this reads its census so the graph pass needs no extra traversal.
func typeCensus(lang parser.LanguageID, src []byte, tree *sitter.Tree) arch.TypeKinds {
	raw := arch.Extract(lang, src, tree.RootNode())
	return arch.TypeKinds{Abstract: raw.Abstract, Total: raw.Types}
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

// checkArchitecture evaluates the policy's architecture section over the
// run's package graph and appends the findings to the summary, honouring
// inline directives on the import lines. The Martin package metrics ride the
// same graph: the snapshots are always recorded, and the SDP findings follow
// them unless the policy switches them off with `sdp: off`.
//
// It returns the graph's package nodes even when nothing was checked. A policy
// audit needs them: the commonest way a policy goes quiet is a layer
// declaration that covers no package, and that produces zero findings — so the
// packages are the only witness.
func (e *Engine) checkArchitecture(summary *model.RunSummary, files []arch.File, kinds map[string]arch.TypeKinds, supsByFile map[string][]suppression) []string {
	g := arch.Build(files)
	if len(files) < 2 {
		return g.Packages()
	}
	policy := e.cfg.ArchPolicy()
	if !policy.Empty() {
		for _, v := range arch.Check(g, policy) {
			finding := e.archFinding(v)
			kept, n, byRule := suppress([]model.Finding{finding}, supsByFile[finding.File])
			if n > 0 {
				countSuppressed(summary, n, byRule)
				continue
			}
			summary.Findings = append(summary.Findings, kept...)
		}
	}

	ms := arch.ComputePkgMetrics(g, kinds, arch.PkgOptions{SDPTolerance: e.cfg.Architecture.SDPTolerance})
	for _, m := range ms {
		w, er := e.thresholdsByLang("go", "java")
		if w > 0 || er > 0 {
			summary.Metrics = append(summary.Metrics,
				model.MetricResult{Metric: "pkg-ca", File: "packages/" + m.Pkg, Value: m.Ca, Warn: w, Err: er},
				model.MetricResult{Metric: "pkg-ce", File: "packages/" + m.Pkg, Value: m.Ce, Warn: w, Err: er})
		}
		summary.Metrics = append(summary.Metrics,
			model.MetricResult{Metric: "pkg-i", File: "packages/" + m.Pkg, Value: int(m.I * 100), Warn: 0, Err: 0},
			model.MetricResult{Metric: "pkg-a", File: "packages/" + m.Pkg, Value: int(m.A * 100), Warn: 0, Err: 0},
			model.MetricResult{Metric: "pkg-d", File: "packages/" + m.Pkg, Value: int(m.D * 100), Warn: 0, Err: 0},
		)
	}
	e.appendSdpFindings(summary, g, ms, supsByFile)
	return g.Packages()
}

// appendSdpFindings turns the package metrics' stable-dependencies violations
// into findings on the shared pipeline, honouring inline directives. The
// snapshots above are recorded either way; `sdp: off` only silences the gate.
func (e *Engine) appendSdpFindings(summary *model.RunSummary, g *arch.Graph, ms []arch.PkgMetrics, supsByFile map[string][]suppression) {
	if e.cfg.Architecture.SDP == "off" {
		return
	}
	for _, v := range arch.SdpViolations(g, ms) {
		finding := model.Finding{
			RuleID:    v.RuleID,
			Severity:  e.cfg.ArchSeverity("sdp"),
			Source:    model.SrcRule,
			File:      v.File,
			StartLine: v.Line,
			EndLine:   v.Line,
			Message:   v.Message,
		}
		kept, n, byRule := suppress([]model.Finding{finding}, supsByFile[finding.File])
		if n > 0 {
			countSuppressed(summary, n, byRule)
			continue
		}
		summary.Findings = append(summary.Findings, kept...)
	}
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

// archRuleMetas registers the architecture checks in the SARIF rule catalogue.
// Layer/forbid/cycle entries appear only when the policy declares layers or
// forbids; the SDP entry is registered whenever the pass can emit it (matching
// what the findings can contain), because it needs no architecture section.
func archRuleMetas(cfg *config.Config) []rules.Meta {
	var metas []rules.Meta
	if !cfg.ArchPolicy().Empty() {
		metas = append(metas,
			rules.Meta{ID: arch.RuleLayerViolation, Title: "Dependency crosses a declared layer boundary"},
			rules.Meta{ID: arch.RuleForbiddenEdge, Title: "Dependency direction forbidden by policy"},
			rules.Meta{ID: arch.RuleImportCycle, Title: "Package import cycle"},
		)
	}
	if cfg.Architecture.SDP != "off" {
		metas = append(metas, rules.Meta{ID: arch.RuleSdp, Title: "Package depends on a less stable package"})
	}
	return metas
}
