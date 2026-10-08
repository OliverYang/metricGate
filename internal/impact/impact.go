// Package impact answers one question deterministically: if this symbol
// changes, what else is affected?
//
// It joins the definition index (internal/symbols) with the reference index
// (internal/refs) for a single name and reports the definition sites, every
// use site, and the set of files that would have to be revisited. It is the
// "blast radius" an agent needs before renaming a function or changing a
// signature — answered locally, in milliseconds, for zero tokens.
//
// The answer is explicitly AST-only: see refs.ConfidenceASTOnly for what that
// does and does not cover.
package impact

import (
	"sort"

	"metricgate/internal/refs"
	"metricgate/internal/symbols"
)

// ConfidenceASTOnly marks a report produced without type resolution: the
// reference set is name-based, so a call made through an interface, a
// reflection lookup or a dynamically built name can be missed.
const ConfidenceASTOnly = "ast-only"

// Report is the deterministic answer to "what does this symbol touch?".
type Report struct {
	Symbol        string        `json:"symbol"`
	Confidence    string        `json:"confidence"`
	Definitions   []symbols.Def `json:"definitions,omitempty"`
	References    []refs.Ref    `json:"references,omitempty"`
	AffectedFiles []string      `json:"affectedFiles,omitempty"`
	Calls         int           `json:"calls"`
	TypeUses      int           `json:"typeUses"`
	Note          string        `json:"note,omitempty"`
}

// Compute joins the definition index with the reference index for one name.
func Compute(name string, defs *symbols.Index, uses *refs.Index) Report {
	report := Report{Symbol: name, Confidence: ConfidenceASTOnly}
	report.Definitions = defs.Lookup(name)
	report.References = uses.Lookup(name)
	for _, r := range report.References {
		switch r.Kind {
		case refs.KindCall:
			report.Calls++
		case refs.KindType:
			report.TypeUses++
		}
	}
	report.AffectedFiles = affectedFiles(report.References)
	report.Note = noteFor(report)
	return report
}

// noteFor states what the report cannot tell the caller, so a thin answer is
// never mistaken for "nothing depends on this".
func noteFor(r Report) string {
	switch {
	case len(r.Definitions) == 0 && len(r.References) == 0:
		return "no definition or reference found; check the spelling (near-miss names are suggested)"
	case len(r.Definitions) == 0:
		return "no definition in this tree; the symbol is external (dependency or generated code)"
	case len(r.References) == 0:
		return "no AST-visible reference; the symbol may be unused, or only reached via interface dispatch or reflection"
	default:
		return ""
	}
}

// affectedFiles is the sorted set of files that reference the name.
func affectedFiles(rs []refs.Ref) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rs {
		if r.File == "" || seen[r.File] {
			continue
		}
		seen[r.File] = true
		out = append(out, r.File)
	}
	sort.Strings(out)
	return out
}
