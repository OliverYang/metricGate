// Package refs indexes cross-file symbol references so an agent can ask
// "who uses this, and what might break if I change it?" deterministically,
// in milliseconds and for zero tokens.
//
// The index is deliberately AST-only and name-based: no type resolution, no
// data flow, no call graph through interfaces. That means it can miss a call
// made through dynamic dispatch, reflection or a dynamically built name —
// every consumer is told so (`Confidence` is "ast-only") rather than being
// handed a complete-looking call graph that is not one.
//
// What it captures is the high-value, low-noise subset: call sites (with the
// textual receiver) and type references. Bare value identifiers are not
// indexed — they are too noisy to be worth a reference, and the index exists
// to answer "what breaks", not "where is this letter used".
package refs

import (
	"cmp"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/model"
	"metricgate/internal/parser"
)

// Reference kinds.
const (
	KindCall = "call" // a call site: f(...) or recv.f(...)
	KindType = "type" // a type reference: a declaration, field, parameter, new T()
)

// Ref is one use site of a name.
type Ref struct {
	Name      string `json:"name"`
	Receiver  string `json:"receiver,omitempty"` // textual selector receiver, e.g. "s" or "cfg"
	Kind      string `json:"kind"`               // call | type
	File      string `json:"file"`
	Line      int    `json:"line"`
	Enclosing string `json:"enclosing,omitempty"` // enclosing function/method name
}

// Extract pulls the references out of one parsed file in source order.
// Ordering for lookups is established by Build, which is the only consumer
// that needs it; walking order is deterministic either way.
func Extract(file string, src []byte, tree *sitter.Tree, lang parser.LanguageID) []Ref {
	w := &walker{file: file, src: src, skip: map[uint]bool{}}
	switch lang {
	case parser.LangGo:
		w.walkGo(tree.RootNode(), "")
	case parser.LangJava:
		w.walkJava(tree.RootNode(), "")
	case parser.LangTS, parser.LangTSX, parser.LangJS:
		w.walkJS(tree.RootNode(), "")
	case parser.LangPy:
		w.walkPy(tree.RootNode(), "")
	case parser.LangC, parser.LangCpp:
		w.walkC(tree.RootNode(), "")
	case parser.LangCS:
		w.walkCS(tree.RootNode(), "")
	case parser.LangRust:
		w.walkRust(tree.RootNode(), "")
	case parser.LangSQL, parser.LangVerilog, parser.LangVB:
		// No reference surface yet. Spelled out rather than left to the
		// missing default so that "not implemented" is distinguishable from
		// "walked and found nothing": a language silently absent here looks
		// identical to a language with no cross-references.
		return nil
	}
	return w.out
}

// walker is the per-file state of one extraction pass: the file it is
// reading, the declaration-name positions that must not count as uses, and
// the references found so far. Keeping it in a struct keeps every walk
// method's signature to (node, enclosing function).
type walker struct {
	file string
	src  []byte
	skip map[uint]bool
	out  []Ref
}

// text is the source slice covered by a node in this walker's file.
func (w *walker) text(n *sitter.Node) string { return text(w.src, n) }

// add records one reference unless the name is empty.
func (w *walker) add(name, kind, receiver, enclosing string, n *sitter.Node) {
	if name == "" {
		return
	}
	w.out = append(w.out, Ref{
		Name: name, Receiver: receiver, Kind: kind,
		File: w.file, Line: line(n), Enclosing: enclosing,
	})
}

// typeRef records a type_identifier unless it is a declaration name.
func (w *walker) typeRef(n *sitter.Node, enclosing string) {
	if w.skip[n.StartByte()] {
		return
	}
	w.add(w.text(n), KindType, "", enclosing, n)
}

// skipName marks a declaration's name node so it is never read as a use.
func (w *walker) skipName(n *sitter.Node) {
	if name := n.ChildByFieldName("name"); name != nil {
		w.skip[name.StartByte()] = true
	}
}

// heritageNameKinds are the node kinds that spell a heritage base as a plain
// name *and* that no other branch of the walk already records. TypeScript's
// `implements I` is deliberately absent: it uses `type_identifier`, which
// walkJS already turns into a type reference, so listing it here would count
// the same base twice. Anything not listed — a factory call
// (`class A extends mixin(B)`), a computed expression, a keyword argument — is
// skipped rather than guessed at, because a wrong base fabricates impact that
// does not exist.
var heritageNameKinds = map[string]bool{
	"identifier":             true, // Python/JS/TS `extends X`
	"attribute":              true, // Python: module.Class
	"member_expression":      true, // JS/TS: module.Class
	"scoped_type_identifier": true, // TS: Namespace.Class
}

// heritageRefs records one KindType reference per named base type of a class's
// heritage clause. Inheriting from a type is a use of it, and it is the
// coarsest edge an impact query can follow: changing a base class reaches
// every subclass, which no call-site edge can express.
//
// The candidates are collected by the walker that knows its grammar's shape
// (Python spells them as an argument_list, JS/TS as class_heritage) and are
// filtered here, so all three languages share one definition of "a base that
// can be followed".
func (w *walker) heritageRefs(candidates []*sitter.Node, enclosing string) {
	for _, base := range candidates {
		if base == nil || !heritageNameKinds[base.Kind()] || w.skip[base.StartByte()] {
			continue
		}
		name, recv := baseName(w.text(base))
		w.add(name, KindType, recv, enclosing, base)
	}
}

// baseName splits a heritage base into the name an index is keyed by and its
// textual qualifier. `Base` yields (Base, ""); `mixins.Other` yields
// (Other, mixins) so a qualified base is still findable under its own name,
// matching how every other reference in this package is recorded.
func baseName(text string) (name, recv string) {
	text = oneline(text, 60)
	if dot := strings.LastIndex(text, "."); dot >= 0 {
		return text[dot+1:], text[:dot]
	}
	return text, ""
}

// Index maps symbol names to their use sites.
type Index struct {
	byName map[string][]Ref
	total  int
}

// Build indexes the given references. The index is immutable once built.
//
// The input is ordered once here and then bucketed, so every per-name bucket
// is already sorted — sorting per bucket would repeat the same key list in
// two places for no gain.
func Build(rs []Ref) *Index {
	ordered := append([]Ref(nil), rs...)
	model.SortByKeys(ordered,
		func(a, b Ref) int { return strings.Compare(a.File, b.File) },
		func(a, b Ref) int { return cmp.Compare(a.Line, b.Line) },
		func(a, b Ref) int { return strings.Compare(a.Kind, b.Kind) },
		func(a, b Ref) int { return strings.Compare(a.Name, b.Name) },
	)
	ix := &Index{byName: make(map[string][]Ref, len(ordered))}
	for _, r := range ordered {
		if r.Name == "" {
			continue
		}
		ix.byName[r.Name] = append(ix.byName[r.Name], r)
		ix.total++
	}
	return ix
}

// Len is the number of indexed references.
func (ix *Index) Len() int {
	if ix == nil {
		return 0
	}
	return ix.total
}

// Lookup returns every reference to exactly that name, in file order.
func (ix *Index) Lookup(name string) []Ref {
	if ix == nil || name == "" {
		return nil
	}
	return ix.byName[name]
}

// text returns the source slice covered by a node.
func text(src []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return string(src[n.StartByte():n.EndByte()])
}

// line converts a node position to a 1-based line number.
func line(n *sitter.Node) int { return int(n.StartPosition().Row) + 1 }

// oneline collapses whitespace and truncates (rune-safe) to max runes.
func oneline(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if max > 0 && len(r) > max {
		r = append(r[:max], []rune("…")...)
	}
	return string(r)
}
