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
		out = append(out, Detect(path, src, tree, lang)...)
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
