// Bridging between the fact sheets and the definition index: one Def per
// indexed symbol (types, classes and their methods included), plus the
// similar-symbol hints rendered into the context pack and the repair
// worklists keyed by symbol name.
package facts

import (
	"fmt"
	"sort"

	"metricgate/internal/symbols"
)

// Defs flattens the fact sheet into indexable definitions. Methods carry
// their receiver so "same name on two types" stays distinguishable; class
// symbols surface their members as standalone definitions.
func Defs(f *FileFacts) []symbols.Def {
	var out []symbols.Def
	base := symbols.Def{
		File:      f.File,
		Language:  f.Language,
		Package:   f.Package,
		Signature: "",
	}
	for _, s := range f.Symbols {
		def := base
		def.Name = s.Name
		def.Kind = s.Kind
		def.Receiver = s.Receiver
		def.Line = s.StartLine
		def.Signature = s.Signature
		def.Visibility = s.Visibility
		out = append(out, def)
		for _, c := range s.Children {
			child := base
			child.Name = c.Name
			child.Kind = c.Kind
			child.Line = c.StartLine
			child.Signature = c.Signature
			child.Visibility = c.Visibility
			child.Receiver = s.Name // interface/class member of that type
			out = append(out, child)
		}
	}
	return out
}

// Index builds a definition index over the fact sheets.
func Index(files []*FileFacts) *symbols.Index {
	var defs []symbols.Def
	for _, f := range files {
		defs = append(defs, Defs(f)...)
	}
	return symbols.Build(defs)
}

// SimilarSymbols renders the "existing symbols you may have meant" hints:
// for every symbol the file declares that already has a same-named or
// near-named twin elsewhere, point at it — the cheapest guard against a
// near-miss reinvention of an existing API.
func SimilarSymbols(files []*FileFacts, ix *symbols.Index, max int) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range files {
		for _, s := range f.Symbols {
			if s.Name == "" {
				continue
			}
			hint := symbolHint(f, s, ix, max)
			if hint == "" || seen[hint] {
				continue
			}
			seen[hint] = true
			out = append(out, hint)
		}
	}
	sort.Strings(out)
	return out
}

// symbolHint renders one symbol's cross-file hint, "" when none applies.
// Precedence: exact same name elsewhere > near-miss twin elsewhere.
func symbolHint(f *FileFacts, s Symbol, ix *symbols.Index, max int) string {
	if twin := ix.Lookup(s.Name); len(twin) > 0 {
		elsewhere := make([]symbols.Def, 0, len(twin))
		for _, d := range twin {
			if d.File != f.File {
				elsewhere = append(elsewhere, d)
			}
		}
		if len(elsewhere) > 0 {
			return fmt.Sprintf("symbol %q also defined at %s — align with or reuse it",
				s.Name, defAt(elsewhere[0]))
		}
	}
	for _, m := range ix.SimilarElsewhere(s.Name, f.File, max) {
		if m.Score > scoreNearMiss {
			continue
		}
		return fmt.Sprintf("no symbol %q outside this file; similar: %s — reuse or align it instead of a new name",
			s.Name, defAt(m.Def))
	}
	return ""
}

// scoreNearMiss caps which matches count as near-misses: same name in a
// different case, or a one-key typo.
const scoreNearMiss = 3

// defAt renders "file:line (signature)" for one definition.
func defAt(d symbols.Def) string {
	sig := d.Signature
	if sig == "" {
		sig = d.Kind
	}
	if len(sig) > 90 {
		sig = sig[:90] + "…"
	}
	return fmt.Sprintf("%s:%d (%s)", d.File, d.Line, sig)
}
