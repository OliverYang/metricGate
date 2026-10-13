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
