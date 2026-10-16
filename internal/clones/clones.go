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

	// body are the AST nodes this fingerprint was taken from. It is unexported
	// because it is only valid while the tree is alive; the near-clone pass
	// uses it instead of re-locating the declaration in the tree.
	//
	// It is a slice because not every grammar wraps a body in one node: SQL and
	// SystemVerilog name it, but VB follows the header with a flat list of
	// `statement` children, and SystemVerilog splits declarations from
	// statements. Fingerprinting only the first of those would make unrelated
	// methods with the same opening line look like clones.
	body []*sitter.Node
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
//
// The kinds double as the source of truth for the shared declaration walker
// (internal/facts/lang.go indexes symbols off the same table), so a language
// cannot be covered by one pass and invisible to the other.
//
// C/C++ keep only function_definition: nested declarator forms (pointer,
// parenthesised) are still function_definition, so no extra kind is needed.
var declKinds = map[parser.LanguageID][]string{
	parser.LangGo:      {"function_declaration", "method_declaration"},
	parser.LangJava:    {"method_declaration", "constructor_declaration"},
	parser.LangTS:      {"function_declaration", "generator_function_declaration", "method_definition"},
	parser.LangTSX:     {"function_declaration", "generator_function_declaration", "method_definition"},
	parser.LangJS:      {"function_declaration", "generator_function_declaration", "method_definition"},
	parser.LangPy:      {"function_definition"},
	parser.LangC:       {"function_definition"},
	parser.LangCpp:     {"function_definition"},
	parser.LangCS:      {"method_declaration", "constructor_declaration", "destructor_declaration", "local_function_statement", "operator_declaration"},
	parser.LangRust:    {"function_item"},
	parser.LangSQL:     {"create_function", "create_procedure"},
	parser.LangVerilog: {"function_declaration", "task_declaration", "class_constructor_declaration"},
	parser.LangVB:      {"method_declaration", "constructor_declaration"},
}

// declBodies locates the body of a declaration for the grammars that keep it
// outside a `body` field, as a list of roots. Without it those declarations are
// silently skipped: the fingerprint pass would find nothing to hash and the
// clone report would claim the language has no duplication to find.
var declBodies = map[parser.LanguageID]func(n *sitter.Node) []*sitter.Node{
	parser.LangSQL:     sqlBodies,
	parser.LangVerilog: verilogBodies,
	parser.LangVB:      vbBodies,
}

// sqlBodies returns a SQL routine's body node.
func sqlBodies(n *sitter.Node) []*sitter.Node {
	if body := firstChildKind(n, "function_body", "procedure_body"); body != nil {
		return []*sitter.Node{body}
	}
	return nil
}

// verilogBodies returns the statements of a SystemVerilog routine. The
// `*_body_declaration` wrapper holds the header too, so hashing it would mix
// the signature into the fingerprint and make every routine look unique.
func verilogBodies(n *sitter.Node) []*sitter.Node {
	decl := firstChildKind(n, "function_body_declaration", "task_body_declaration")
	if decl == nil {
		return nil
	}
	return childKinds(decl, "function_statement_or_null", "statement_or_null")
}

// vbBodies returns the statements of a VB routine. VB has no body node: a
// method is its header followed by a flat list of `statement` children.
func vbBodies(n *sitter.Node) []*sitter.Node {
	return childKinds(n, "statement")
}

// childKinds returns every child of one of the given kinds, in source order.
func childKinds(n *sitter.Node, kinds ...string) []*sitter.Node {
	var out []*sitter.Node
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		for _, k := range kinds {
			if c.Kind() == k {
				out = append(out, c)
			}
		}
	}
	return out
}

// DeclKinds returns the function-like declaration node kinds of a language,
// or nil when the language has none. Other packages use it instead of keeping
// a second list that could drift out of step.
func DeclKinds(lang parser.LanguageID) []string { return declKinds[lang] }

// Enabled reports whether the config extracts anything at all; the zero
// Config is the documented "off" switch.
func (c Config) Enabled() bool { return c.MinLines > 0 && c.MinTokens > 0 }

// Extract collects the clone candidates of one parsed file. It runs in the
// parse workers, on a tree the metrics pass also walks.
//
// Each candidate carries the AST body node it was fingerprinted from, so the
// near-clone pass reuses the same node instead of re-locating the declaration
// by name and line — a lookup that silently fails for grammars whose
// declarations are unnamed (C/C++ declarator chains, C# fields).
func Extract(lang parser.LanguageID, path string, src []byte, tree *sitter.Tree, cfg Config) []Func {
	kinds := declKinds[lang]
	if len(kinds) == 0 || !cfg.Enabled() {
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
			if body := declBodiesOf(lang, n); len(body) > 0 {
				hash, tokens := fingerprint(src, body)
				out = append(out, Func{
					Lang:      string(lang),
					File:      path,
					Name:      funcName(src, n),
					StartLine: int(n.StartPosition().Row) + 1,
					EndLine:   int(n.EndPosition().Row) + 1,
					Hash:      hash,
					Tokens:    tokens,
					body:      body,
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
		name = declName(src, n)
	}
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

// declName names a declaration whose grammar keeps the name outside a field.
// SQL wraps it in an object_reference, SystemVerilog in a `*_identifier`
// wrapper (or, for `function new`, nowhere at all). Without this the clone
// report would list every such routine as "<anonymous>" and the anchor of a
// clone class would be unreadable.
func declName(src []byte, n *sitter.Node) string {
	switch n.Kind() {
	case "create_function", "create_procedure", "create_table", "create_view":
		if ref := firstChildKind(n, "object_reference"); ref != nil {
			return nodeText(src, ref.ChildByFieldName("name"))
		}
	case "class_constructor_declaration":
		return "new"
	case "function_declaration", "task_declaration", "class_declaration",
		"module_declaration", "interface_declaration", "package_declaration", "program_declaration":
		return firstDescendantText(src, n, "simple_identifier")
	}
	return ""
}

// firstChildKind returns the first child of one of the given kinds.
func firstChildKind(n *sitter.Node, kinds ...string) *sitter.Node {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		for _, k := range kinds {
			if c.Kind() == k {
				return c
			}
		}
	}
	return nil
}

// firstDescendantText returns the text of the first node of the given kind
// anywhere below n, in source order.
func firstDescendantText(src []byte, n *sitter.Node, kind string) string {
	if n == nil {
		return ""
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.Kind() == kind {
			return nodeText(src, c)
		}
		if s := firstDescendantText(src, c, kind); s != "" {
			return s
		}
	}
	return ""
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
func fingerprint(src []byte, roots []*sitter.Node) (hash uint64, tokens int) {
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
	for _, root := range roots {
		walk(root)
	}
	return hash, tokens
}

// declBodiesOf returns the body roots of one declaration: the named `body`
// field when the grammar has one, the language's own locator otherwise.
func declBodiesOf(lang parser.LanguageID, n *sitter.Node) []*sitter.Node {
	if body := n.ChildByFieldName("body"); body != nil {
		return []*sitter.Node{body}
	}
	if locate := declBodies[lang]; locate != nil {
		return locate(n)
	}
	return nil
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
