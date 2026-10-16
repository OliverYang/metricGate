// Package stubs detects unfinished code: empty function bodies, Go
// panic("not implemented") bodies and Java UnsupportedOperationException
// throws. The inventory answers "how much of this codebase (or this LLM's
// output) is scaffolding?" and feeds repair worklists.
package stubs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/parser"
	"metricgate/internal/selection"
	"metricgate/internal/sources"
)

// Kinds of incompleteness.
const (
	KindEmpty       = "empty-body"
	KindPanicStub   = "panic-not-implemented"
	KindUnsupported = "unsupported-exception"
)

// Item is one detected stub.
type Item struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Symbol string `json:"symbol,omitempty"`
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
	// At is the canonical definition of Symbol elsewhere or in place,
	// "file:line (signature)" — filled by callers that hold a symbol
	// index (the repair worklist shows where the body to finish lives).
	At string `json:"at,omitempty"`
}

// stubPhrases marks Go panic bodies as deliberate placeholders.
var stubPhrases = []string{"not implemented", "unimplemented", "todo", "fixme"}

// Detect scans one parsed file for stubs.
func Detect(file string, src []byte, tree *sitter.Tree, lang parser.LanguageID) []Item {
	var items []Item
	root := tree.RootNode()
	switch lang {
	case parser.LangGo:
		items = detectGo(file, src, root)
	case parser.LangJava:
		items = detectJava(file, src, root)
	case parser.LangTS, parser.LangTSX, parser.LangJS:
		items = detectJS(file, src, root)
	case parser.LangPy:
		items = detectPy(file, src, root)
	case parser.LangC, parser.LangCpp:
		items = detectCLike(file, src, root)
	case parser.LangCS:
		items = detectCS(file, src, root)
	case parser.LangRust:
		items = detectRust(file, src, root)
	case parser.LangVB:
		items = detectVB(file, src, root)
	case parser.LangVerilog:
		items = detectVerilog(file, src, root)
	case parser.LangSQL:
		// SQL has no stub surface this grammar can see: the routine body is a
		// single query, there is no `throw` equivalent, and the PL/pgSQL
		// `RAISE EXCEPTION` form does not parse (it lands in an ERROR node).
		// Spelled out rather than left to the missing default so that "not
		// implemented" stays distinguishable from "nothing found".
		return nil
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Line < items[j].Line })
	return items
}

// Scan walks root (file or directory) and returns the stub inventory.
func Scan(root string) ([]Item, error) {
	files, err := sources.Files(root)
	if err != nil {
		return nil, err
	}
	parsers := map[parser.LanguageID]*parser.Parser{}
	defer func() {
		for _, p := range parsers {
			p.Close()
		}
	}()

	var out []Item
	for _, path := range files {
		lang, ok := parser.LanguageForFile(filepath.Base(path))
		if !ok {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		tree, err := parseTree(parsers, lang, src)
		if err != nil {
			continue
		}
		for _, item := range Detect(path, src, tree, lang) {
			item.File = selection.SanitizePath(root, item.File)
			out = append(out, item)
		}
		tree.Close()
	}
	return out, nil
}

// parseTree returns a parsed tree using the shared per-language parser.
func parseTree(parsers map[parser.LanguageID]*parser.Parser, lang parser.LanguageID, src []byte) (*sitter.Tree, error) {
	pp := parsers[lang]
	if pp == nil {
		fresh, err := parser.New(lang)
		if err != nil {
			return nil, err
		}
		pp = fresh
		parsers[lang] = pp
	}
	return pp.Parse(src)
}

func text(src []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return string(src[n.StartByte():n.EndByte()])
}

func line(n *sitter.Node) int { return int(n.StartPosition().Row) + 1 }

// firstChildOfKind returns the first child of one of the given kinds.
func firstChildOfKind(n *sitter.Node, kinds ...string) *sitter.Node {
	if n == nil {
		return nil
	}
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
			return text(src, c)
		}
		if s := firstDescendantText(src, c, kind); s != "" {
			return s
		}
	}
	return ""
}

// compact strips all whitespace so multi-line empty bodies normalize to "{}".
func compact(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func funcName(src []byte, fn *sitter.Node) string {
	name := text(src, fn.ChildByFieldName("name"))
	if recv := fn.ChildByFieldName("receiver"); recv != nil {
		r := strings.TrimSpace(text(src, recv))
		r = strings.TrimPrefix(r, "(")
		r = strings.TrimSuffix(r, ")")
		if fields := strings.Fields(r); len(fields) > 0 {
			r = fields[len(fields)-1]
		}
		return strings.TrimPrefix(r, "*") + "." + name
	}
	return name
}

func detectGo(file string, src []byte, root *sitter.Node) []Item {
	var items []Item
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		switch n.Kind() {
		case "function_declaration", "method_declaration":
			sym := funcName(src, n)
			body := n.ChildByFieldName("body")
			if body == nil {
				return
			}
			bt := text(src, body)
			if compact(bt) == "{}" {
				items = append(items, Item{File: file, Line: line(body), Symbol: sym, Kind: KindEmpty})
				return
			}
			if msg := panicStubMessage(src, body); msg != "" {
				items = append(items, Item{File: file, Line: line(body), Symbol: sym, Kind: KindPanicStub, Detail: msg})
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return items
}

// panicStubMessage returns the panic argument when the body contains a
// panic whose message marks it as a placeholder.
func panicStubMessage(src []byte, body *sitter.Node) string {
	var msg string
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if msg != "" {
			return
		}
		if n.Kind() == "call_expression" {
			if m := stubPanicArg(src, n); m != "" {
				msg = m
				return
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(body)
	return msg
}

// stubPanicArg returns the argument text when the call is panic(<stub phrase>).
func stubPanicArg(src []byte, call *sitter.Node) string {
	fn := call.ChildByFieldName("function")
	if fn == nil || text(src, fn) != "panic" {
		return ""
	}
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return ""
	}
	for i := uint(0); i < args.ChildCount(); i++ {
		a := args.Child(i)
		if a.Kind() != "interpreted_string_literal" && a.Kind() != "raw_string_literal" {
			continue
		}
		s := strings.ToLower(strings.Trim(text(src, a), "\"`"))
		for _, phrase := range stubPhrases {
			if strings.Contains(s, phrase) {
				return strings.TrimSpace(text(src, a))
			}
		}
	}
	return ""
}

func detectJava(file string, src []byte, root *sitter.Node) []Item {
	var items []Item
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		switch n.Kind() {
		case "method_declaration", "constructor_declaration":
			sym := text(src, n.ChildByFieldName("name"))
			body := n.ChildByFieldName("body")
			if body == nil {
				return
			}
			bt := text(src, body)
			switch {
			case compact(bt) == "{}":
				items = append(items, Item{File: file, Line: line(body), Symbol: sym, Kind: KindEmpty})
			case strings.Contains(bt, "UnsupportedOperationException"):
				items = append(items, Item{File: file, Line: line(body), Symbol: sym, Kind: KindUnsupported})
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return items
}

func detectJS(file string, src []byte, root *sitter.Node) []Item {
	var items []Item
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		switch n.Kind() {
		case "function_declaration", "generator_function_declaration", "method_definition":
			sym := text(src, n.ChildByFieldName("name"))
			body := n.ChildByFieldName("body")
			if body == nil {
				return
			}
			bt := text(src, body)
			if compact(bt) == "{}" {
				items = append(items, Item{File: file, Line: line(body), Symbol: sym, Kind: KindEmpty})
			}
		case "throw_statement":
			// `throw new Error('not implemented')` is the JS placeholder idiom.
			if msg := jsThrowStubMessage(src, n); msg != "" {
				items = append(items, Item{File: file, Line: line(n), Kind: KindUnsupported, Detail: msg})
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return items
}

// jsThrowStubMessage returns the thrown message when a throw statement
// throws an Error whose message marks it as a placeholder.
func jsThrowStubMessage(src []byte, n *sitter.Node) string {
	// new_expression carries no message field; scan its string children.
	var msg string
	var walk func(*sitter.Node)
	walk = func(cur *sitter.Node) {
		if msg != "" {
			return
		}
		if cur.Kind() == "string" {
			s := strings.ToLower(strings.Trim(text(src, cur), "\"'`"))
			for _, phrase := range stubPhrases {
				if strings.Contains(s, phrase) {
					msg = strings.TrimSpace(text(src, cur))
					return
				}
			}
		}
		for i := uint(0); i < cur.ChildCount(); i++ {
			walk(cur.Child(i))
		}
	}
	walk(n)
	return msg
}

// detectPy is the Python arm: a definition whose body holds only pass or
// Ellipsis (...) is an empty stub, and `raise NotImplementedError` is the
// Python placeholder idiom.
func detectPy(file string, src []byte, root *sitter.Node) []Item {
	var items []Item
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		walkPyNode(file, src, n, &items)
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return items
}

// walkPyNode appends the stub items one Python CST node contributes.
func walkPyNode(file string, src []byte, n *sitter.Node, items *[]Item) {
	switch n.Kind() {
	case "function_definition":
		sym := text(src, n.ChildByFieldName("name"))
		body := n.ChildByFieldName("body")
		if body != nil && pyBodyIsPlaceholder(src, body) {
			*items = append(*items, Item{File: file, Line: line(body), Symbol: sym, Kind: KindEmpty})
		}
	case "raise_statement":
		if pyRaiseStubMessage(src, n) {
			*items = append(*items, Item{File: file, Line: line(n), Kind: KindUnsupported, Detail: "NotImplementedError"})
		}
	}
}

// pyBodyIsPlaceholder reports whether a block body is only pass/Ellipsis
// statements (plus docstrings, which carry no behavior either).
func pyBodyIsPlaceholder(src []byte, body *sitter.Node) bool {
	any := false
	for i := uint(0); i < body.NamedChildCount(); i++ {
		c := body.NamedChild(i)
		switch c.Kind() {
		case "pass_statement", "expression_statement":
			if t := strings.TrimSpace(text(src, c)); t == "pass" || t == "..." {
				any = true
				continue
			}
			// A docstring is a bare string expression statement.
			if c.NamedChildCount() == 1 && c.NamedChild(0).Kind() == "string" {
				any = true
				continue
			}
			return false
		default:
			return false
		}
	}
	return any
}

// pyRaiseStubMessage reports whether a raise statement raises
// NotImplementedError (the Python placeholder idiom).
func pyRaiseStubMessage(src []byte, n *sitter.Node) bool {
	found := false
	var walk func(*sitter.Node)
	walk = func(cur *sitter.Node) {
		if found {
			return
		}
		if cur.Kind() == "identifier" && text(src, cur) == "NotImplementedError" {
			found = true
			return
		}
		for i := uint(0); i < cur.ChildCount(); i++ {
			walk(cur.Child(i))
		}
	}
	walk(n)
	return found
}

// detectCLike is the C/C++ arm: a definition whose compound body is empty
// `{}` is an empty stub, and a body returning a sentinel (NULL/-1) is left
// alone — C has no throw idiom, so only the empty-body shape is detected.
func detectCLike(file string, src []byte, root *sitter.Node) []Item {
	var items []Item
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		switch n.Kind() {
		case "function_definition":
			sym := cSymbolName(src, n)
			body := n.ChildByFieldName("body")
			if body == nil {
				return
			}
			if compact(text(src, body)) == "{}" {
				items = append(items, Item{File: file, Line: line(body), Symbol: sym, Kind: KindEmpty})
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return items
}

// cSymbolName names a C function definition via its declarator chain
// (function_definition has no name field; the identifier sits inside the
// function_declarator).
func cSymbolName(src []byte, fn *sitter.Node) string {
	decl := fn.ChildByFieldName("declarator")
	for decl != nil && decl.Kind() == "pointer_declarator" {
		decl = decl.NamedChild(0)
	}
	if decl == nil || decl.Kind() != "function_declarator" {
		return ""
	}
	if name := decl.ChildByFieldName("declarator"); name != nil {
		return text(src, name)
	}
	return ""
}

// detectCS is the C# arm: a method whose block body is empty is an empty
// stub, and a body throwing NotImplementedException is the C# placeholder
// idiom.
func detectCS(file string, src []byte, root *sitter.Node) []Item {
	var items []Item
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		switch n.Kind() {
		case "method_declaration", "constructor_declaration", "local_function_statement":
			sym := text(src, n.ChildByFieldName("name"))
			body := n.ChildByFieldName("body")
			if body == nil {
				return
			}
			bt := text(src, body)
			switch {
			case compact(bt) == "{}":
				items = append(items, Item{File: file, Line: line(body), Symbol: sym, Kind: KindEmpty})
			case strings.Contains(bt, "NotImplementedException"):
				items = append(items, Item{File: file, Line: line(body), Symbol: sym, Kind: KindUnsupported})
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return items
}

// detectRust is the Rust arm: a function whose block body is empty is an
// empty stub, and a body with an unimplemented!/todo! macro is the Rust
// placeholder idiom.
// detectVB finds VB placeholders: a routine whose header is followed by no
// statements at all, and the `Throw New NotImplementedException()` idiom.
//
// VB has no body node, so "empty" means "no `statement` children" — the same
// flat list the metrics and clone passes read.
func detectVB(file string, src []byte, root *sitter.Node) []Item {
	var items []Item
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		switch n.Kind() {
		case "method_declaration", "constructor_declaration":
			sym := text(src, n.ChildByFieldName("name"))
			if sym == "" && n.Kind() == "constructor_declaration" {
				sym = "New"
			}
			if noStatementChildren(n) {
				items = append(items, Item{File: file, Line: line(n), Symbol: sym, Kind: KindEmpty})
			}
		case "throw_statement":
			if msg := vbThrowStubMessage(src, n); msg != "" {
				items = append(items, Item{File: file, Line: line(n), Kind: KindUnsupported, Detail: msg})
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return items
}

// noStatementChildren reports whether a VB routine has an empty body.
func noStatementChildren(n *sitter.Node) bool {
	for i := uint(0); i < n.ChildCount(); i++ {
		if n.Child(i).Kind() == "statement" {
			return false
		}
	}
	return true
}

// vbThrowStubMessage returns the exception name when a Throw statement names
// one of the placeholder exceptions.
func vbThrowStubMessage(src []byte, n *sitter.Node) string {
	bt := text(src, n)
	for _, name := range []string{"NotImplementedException", "NotSupportedException"} {
		if strings.Contains(bt, name) {
			return name
		}
	}
	return ""
}

// detectVerilog finds SystemVerilog placeholders: a routine whose body
// declaration holds no statements, and the `$fatal`/`$error` stub idiom.
//
// A routine with no statements is legal and common in interface and class
// bodies, which is exactly why it is worth reporting.
func detectVerilog(file string, src []byte, root *sitter.Node) []Item {
	var items []Item
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		switch n.Kind() {
		case "function_declaration", "task_declaration", "class_constructor_declaration":
			sym := verilogSymbolName(src, n)
			if decl := firstChildOfKind(n, "function_body_declaration", "task_body_declaration"); decl != nil {
				if noVerilogStatements(decl) {
					items = append(items, Item{File: file, Line: line(decl), Symbol: sym, Kind: KindEmpty})
				}
			}
		case "system_tf_call":
			if msg := verilogFatalMessage(src, n); msg != "" {
				items = append(items, Item{File: file, Line: line(n), Kind: KindUnsupported, Detail: msg})
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return items
}

// noVerilogStatements reports whether a body declaration holds no statements.
// The header lives in the same node, so the test is on the statement kinds
// rather than on the child count.
func noVerilogStatements(decl *sitter.Node) bool {
	for i := uint(0); i < decl.ChildCount(); i++ {
		switch decl.Child(i).Kind() {
		case "function_statement_or_null", "statement_or_null", "block_item_declaration":
			return false
		}
	}
	return true
}

// verilogFatalMessage returns the system task name when a call is one of the
// simulation-fatal placeholders.
func verilogFatalMessage(src []byte, n *sitter.Node) string {
	name := text(src, firstChildOfKind(n, "system_tf_identifier"))
	switch name {
	case "$fatal", "$error", "$stop":
		return name
	}
	return ""
}

// verilogSymbolName reads the identifier out of a SystemVerilog declaration,
// which nests it behind a wrapper node rather than a field.
func verilogSymbolName(src []byte, n *sitter.Node) string {
	if n.Kind() == "class_constructor_declaration" {
		return "new"
	}
	decl := firstChildOfKind(n, "function_body_declaration", "task_body_declaration")
	if decl == nil {
		return ""
	}
	if id := firstChildOfKind(decl, "function_identifier", "task_identifier"); id != nil {
		return firstDescendantText(src, id, "simple_identifier")
	}
	return ""
}

// detectRust scans one parsed file for stubs.
func detectRust(file string, src []byte, root *sitter.Node) []Item {
	var items []Item
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		switch n.Kind() {
		case "function_item":
			sym := text(src, n.ChildByFieldName("name"))
			body := n.ChildByFieldName("body")
			if body == nil {
				return
			}
			bt := text(src, body)
			switch {
			case compact(bt) == "{}":
				items = append(items, Item{File: file, Line: line(body), Symbol: sym, Kind: KindEmpty})
			case strings.Contains(bt, "unimplemented!") || strings.Contains(bt, "todo!"):
				items = append(items, Item{File: file, Line: line(body), Symbol: sym, Kind: KindUnsupported})
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return items
}
