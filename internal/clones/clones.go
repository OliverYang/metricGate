// Package clones detects Type-2 function clones: functions whose bodies are
// identical once identifiers, type names and literals are normalised away.
//
// This is the deterministic half of the "reuse before you generate" rule
// for LLM coding: when the model writes a function that already exists
// elsewhere in the run — even under a different name — the gate points at
// the original instead of letting the copy merge.
//
// The pipeline mirrors the architecture pass: per-file extraction happens
// inside the parse workers (a 64-bit fingerprint per function), and the
// cross-file grouping runs once over the collected hashes at the end of
// the run.
package clones

import (
	"sort"
	"strconv"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/parser"
)

// RuleFunctionClone is the stable rule id reported for clone members.
const RuleFunctionClone = "function-clone"

// Config tunes when two functions are considered clones.
type Config struct {
	MinLines  int // minimum physical lines of a function
	MinTokens int // minimum normalised tokens in the body
}

// Default is the out-of-the-box sensitivity. It is deliberately shallow:
// short functions are cheap to review by eye, while anything past ten
// physical lines appearing twice is worth consolidating.
var Default = Config{MinLines: 10, MinTokens: 60}

// Func is one extracted function that participates in clone matching.
type Func struct {
	Lang      string
	File      string
	Name      string
	StartLine int // 1-based
	EndLine   int // 1-based inclusive
	Hash      uint64
	Tokens    int // normalised token count of the body
}

// Group is one clone class: functions whose normalised bodies hashed to the
// same value. Funcs is sorted by file and line, so the first member is the
// stable anchor every other member is reported against.
type Group struct {
	Lang  string
	Hash  uint64
	Funcs []Func
}

// declKinds are the function-like declaration node kinds per language.
// Bodies (not whole declarations) are fingerprinted, so renaming a
// parameter does not hide a clone — only changing the logic does.
var declKinds = map[parser.LanguageID][]string{
	parser.LangGo:   {"function_declaration", "method_declaration"},
	parser.LangJava: {"method_declaration", "constructor_declaration"},
}

// Enabled reports whether the config extracts anything at all; the zero
// Config is the documented "off" switch.
func (c Config) Enabled() bool { return c.MinLines > 0 && c.MinTokens > 0 }

// Extract collects the clone candidates of one parsed file. It runs in the
// parse workers, on a tree the metrics pass also walks.
func Extract(lang parser.LanguageID, path string, src []byte, tree *sitter.Tree, cfg Config) []Func {
	kinds, ok := declKinds[lang]
	if !ok || !cfg.Enabled() {
		return nil
	}
	isDecl := map[string]bool{}
	for _, k := range kinds {
		isDecl[k] = true
	}
	var out []Func
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if isDecl[n.Kind()] {
			if body := n.ChildByFieldName("body"); body != nil {
				hash, tokens := fingerprint(src, body)
				out = append(out, Func{
					Lang:      string(lang),
					File:      path,
					Name:      funcName(src, n),
					StartLine: int(n.StartPosition().Row) + 1,
					EndLine:   int(n.EndPosition().Row) + 1,
					Hash:      hash,
					Tokens:    tokens,
				})
			}
			return // nested function literals stay out: declarations only
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(tree.RootNode())
	return out
}

// funcName names a declaration for reports, "<anonymous>" when nameless.
func funcName(src []byte, n *sitter.Node) string {
	name := nodeText(src, n.ChildByFieldName("name"))
	if name == "" {
		return "<anonymous>"
	}
	// Go methods: receiver "(t *T)" → "T.Name".
	if recv := n.ChildByFieldName("receiver"); recv != nil {
		r := strings.TrimSpace(nodeText(src, recv))
		r = strings.TrimPrefix(r, "(")
		r = strings.TrimSuffix(r, ")")
		if fields := strings.Fields(r); len(fields) > 0 {
			r = fields[len(fields)-1]
		}
		if r = strings.TrimPrefix(r, "*"); r != "" {
			name = r + "." + name
		}
	}
	return name
}

// fnv1a is the 64-bit FNV-1a round function.
func fnv1a(h uint64, b byte) uint64 {
	return (h ^ uint64(b)) * 1099511628211
}

// idMark separates normalised tokens inside the hash stream; it never
// appears in source text, so the concatenation is unambiguous.
const idMark byte = 0x1f

// fingerprint hashes the normalised token stream of one body. Identifier-
// like tokens (any named leaf) collapse to a single placeholder so renamed
// copies still match; operators and punctuation keep their exact text so
// `a+b` never equals `a-b`. Comments (tree-sitter "extra" nodes) are
// skipped.
func fingerprint(src []byte, body *sitter.Node) (hash uint64, tokens int) {
	emit := func(s string) {
		tokens++
		hash = fnv1a(hash, idMark)
		for i := 0; i < len(s); i++ {
			hash = fnv1a(hash, s[i])
		}
	}
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil || n.IsExtra() {
			return
		}
		if n.ChildCount() == 0 { // a leaf token
			if n.IsNamed() {
				emit("id") // identifiers, types, literals: all one shape
			} else {
				emit(nodeText(src, n)) // operators and punctuation: exact
			}
			return
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(body)
	return hash, tokens
}

// nodeText returns the source slice covered by a node.
func nodeText(src []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return string(src[n.StartByte():n.EndByte()])
}

// Find partitions extracted functions into clone classes and returns the
// classes with two or more members (same language, same normalised body),
// sorted so output is stable between runs.
func Find(funcs []Func, cfg Config) []Group {
	if !cfg.Enabled() || len(funcs) < 2 {
		return nil
	}
	type key struct {
		lang string
		hash uint64
	}
	byKey := map[key][]Func{}
	for _, f := range funcs {
		if !bigEnough(f, cfg) {
			continue
		}
		k := key{f.Lang, f.Hash}
		byKey[k] = append(byKey[k], f)
	}
	out := make([]Group, 0, len(byKey))
	for k, members := range byKey {
		if len(members) < 2 {
			continue
		}
		out = append(out, Group{Lang: k.lang, Hash: k.hash, Funcs: sortFuncs(members)})
	}
	sortGroups(out)
	return out
}

// bigEnough applies the size gates: too-short or token-poor functions are
// cheap to review by eye and never reported.
func bigEnough(f Func, cfg Config) bool {
	return f.EndLine-f.StartLine+1 >= cfg.MinLines && f.Tokens >= cfg.MinTokens
}

// lessFunc orders functions by file, then line, then name.
func lessFunc(a, b Func) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.StartLine != b.StartLine {
		return a.StartLine < b.StartLine
	}
	return a.Name < b.Name
}

func sortFuncs(members []Func) []Func {
	sort.Slice(members, func(i, j int) bool { return lessFunc(members[i], members[j]) })
	return members
}

// sortGroups orders classes by their anchor so reports are stable.
func sortGroups(groups []Group) {
	sort.Slice(groups, func(i, j int) bool {
		return lessFunc(groups[i].Funcs[0], groups[j].Funcs[0])
	})
}

// Summary renders one line per clone class for the LLM context pack —
// the "reuse this, don't regenerate it" hint.
func Summary(groups []Group) []string {
	lines := make([]string, 0, len(groups))
	for _, g := range groups {
		a := g.Funcs[0]
		others := make([]string, 0, len(g.Funcs)-1)
		for _, m := range g.Funcs[1:] {
			others = append(others, m.File+":"+strconv.Itoa(m.StartLine))
		}
		lines = append(lines,
			"clone of "+a.File+":"+strconv.Itoa(a.StartLine)+" ("+a.Name+") also at "+strings.Join(others, ", ")+" — reuse it, do not reimplement")
	}
	return lines
}
