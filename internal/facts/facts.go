// Package facts extracts structural facts (package, imports, exported
// symbols with their exact signatures) from parsed source files.
//
// Facts power the constraint context handed to an LLM before code
// generation: the model gets the exact symbols it must align with instead
// of hallucinating signatures — the cheapest way to prevent API-level
// hallucination.
package facts

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/parser"
	"metricgate/internal/sources"
)

// Symbol is one declaration worth constraining an LLM with.
type Symbol struct {
	Kind       string   `json:"kind"` // function|method|struct|interface|class|field|type
	Name       string   `json:"name"`
	Receiver   string   `json:"receiver,omitempty"` // Go method receiver base type
	Signature  string   `json:"signature"`          // single-line normalized signature text
	Visibility string   `json:"visibility"`         // exported|package|private
	StartLine  int      `json:"startLine"`
	Children   []Symbol `json:"children,omitempty"` // interface method sets, class members
}

// FileFacts is the fact sheet of one source file.
type FileFacts struct {
	File     string   `json:"file"`
	Language string   `json:"language"`
	Package  string   `json:"package,omitempty"`
	Imports  []string `json:"imports,omitempty"`
	Symbols  []Symbol `json:"symbols"`
}

// Extract pulls the facts out of one parsed file.
func Extract(file string, src []byte, tree *sitter.Tree, lang parser.LanguageID) *FileFacts {
	f := &FileFacts{File: file, Language: string(lang)}
	root := tree.RootNode()
	switch lang {
	case parser.LangGo:
		extractGo(f, src, root)
	case parser.LangJava:
		extractJava(f, src, root)
	}
	return f
}

// Collect walks root (file or directory) and returns the fact sheets of
// every supported file, sorted by path.
func Collect(root string) ([]*FileFacts, error) {
	files, err := sources.Files(root)
	if err != nil {
		return nil, err
	}
	return parseFacts(files)
}

// parseFacts parses every file and extracts its facts, reusing one parser
// per language and skipping unreadable/unparsable files.
func parseFacts(files []string) ([]*FileFacts, error) {
	parsers := map[parser.LanguageID]*parser.Parser{}
	defer closeParsers(parsers)

	var out []*FileFacts
	for _, path := range files {
		lang, ok := parser.LanguageForFile(filepath.Base(path))
		if !ok {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		f, err := parseFile(parsers, lang, path, src)
		if err == nil {
			out = append(out, f)
		}
	}
	return out, nil
}

func closeParsers(parsers map[parser.LanguageID]*parser.Parser) {
	for _, p := range parsers {
		p.Close()
	}
}

// parseFile parses one file with the shared per-language parser.
func parseFile(parsers map[parser.LanguageID]*parser.Parser, lang parser.LanguageID, path string, src []byte) (*FileFacts, error) {
	pp := parsers[lang]
	if pp == nil {
		fresh, err := parser.New(lang)
		if err != nil {
			return nil, err
		}
		pp = fresh
		parsers[lang] = pp
	}
	tree, err := pp.Parse(src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	return Extract(path, src, tree, lang), nil
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

// goVis: Go visibility is decided by the first rune of the name.
func goVis(name string) string {
	if name == "" {
		return "private"
	}
	for _, r := range name {
		if unicode.IsUpper(r) {
			return "exported"
		}
		return "private"
	}
	return "private"
}

// goReceiver reduces "(pp *Parser)" to "Parser".

// effectiveVis combines name and receiver visibility: a symbol is visible
// at most as much as the least visible of the two.
func effectiveVis(nameVis, recvVis string) string {
	order := map[string]int{"exported": 2, "package": 1, "private": 0}
	if order[recvVis] < order[nameVis] {
		return recvVis
	}
	return nameVis
}
func goReceiver(src []byte, n *sitter.Node) string {
	r := text(src, n.ChildByFieldName("receiver"))
	r = strings.TrimPrefix(r, "(")
	r = strings.TrimSuffix(r, ")")
	if fields := strings.Fields(r); len(fields) > 0 {
		r = fields[len(fields)-1]
	}
	return strings.TrimPrefix(r, "*")
}

// goSig is the declaration text up to (excluding) the body, on one line.
func goSig(src []byte, decl, body *sitter.Node) string {
	end := decl.EndByte()
	if body != nil {
		end = body.StartByte()
	}
	return oneline(string(src[decl.StartByte():end]), 200)
}

func extractGo(f *FileFacts, src []byte, root *sitter.Node) {
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		switch n.Kind() {
		case "package_clause":
			for i := uint(0); i < n.ChildCount(); i++ {
				if c := n.Child(i); c.Kind() == "package_identifier" {
					f.Package = text(src, c)
				}
			}
			return
		case "import_declaration":
			collectGoImports(f, src, n)
			return
		case "function_declaration":
			name := text(src, n.ChildByFieldName("name"))
			f.Symbols = append(f.Symbols, Symbol{
				Kind:       "function",
				Name:       name,
				Signature:  goSig(src, n, n.ChildByFieldName("body")),
				Visibility: goVis(name),
				StartLine:  line(n),
			})
			return
		case "method_declaration":
			name := text(src, n.ChildByFieldName("name"))
			recv := goReceiver(src, n)
			f.Symbols = append(f.Symbols, Symbol{
				Kind:      "method",
				Name:      name,
				Receiver:  recv,
				Signature: goSig(src, n, n.ChildByFieldName("body")),
				// A method is visible at most as much as its receiver type:
				// Get on unexported memStore is not public surface.
				Visibility: effectiveVis(goVis(name), goVis(recv)),
				StartLine:  line(n),
			})
			return
		case "type_declaration":
			collectGoTypes(f, src, n)
			return
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
}

func collectGoImports(f *FileFacts, src []byte, n *sitter.Node) {
	var walk func(x *sitter.Node)
	walk = func(x *sitter.Node) {
		if x.Kind() == "import_spec" {
			imp := strings.Trim(text(src, x.ChildByFieldName("path")), "\"`")
			if a := x.ChildByFieldName("name"); a != nil {
				imp = text(src, a) + " " + imp
			}
			f.Imports = append(f.Imports, imp)
			return
		}
		for i := uint(0); i < x.ChildCount(); i++ {
			walk(x.Child(i))
		}
	}
	walk(n)
}

func collectGoTypes(f *FileFacts, src []byte, n *sitter.Node) {
	var walk func(x *sitter.Node)
	walk = func(x *sitter.Node) {
		if strings.HasPrefix(x.Kind(), "type_spec") {
			name := text(src, x.ChildByFieldName("name"))
			t := x.ChildByFieldName("type")
			tk := ""
			if t != nil {
				tk = t.Kind()
			}
			sym := Symbol{Kind: tk, Name: name, Visibility: goVis(name), StartLine: line(x)}
			switch tk {
			case "struct_type":
				sym.Signature = "type " + name + " " + oneline(text(src, t), 220)
			case "interface_type":
				sym.Kind = "interface"
				sym.Signature = "type " + name + " interface"
				sym.Children = goInterfaceMethods(src, t)
			default:
				sym.Kind = "type"
				if t != nil {
					sym.Signature = "type " + name + " " + oneline(text(src, t), 120)
				}
			}
			f.Symbols = append(f.Symbols, sym)
			return
		}
		for i := uint(0); i < x.ChildCount(); i++ {
			walk(x.Child(i))
		}
	}
	walk(n)
}

// goInterfaceMethods collects the method set of an interface type.
func goInterfaceMethods(src []byte, iface *sitter.Node) []Symbol {
	var out []Symbol
	var walk func(x *sitter.Node)
	walk = func(x *sitter.Node) {
		if x.Kind() == "method_elem" {
			name := text(src, x.ChildByFieldName("name"))
			out = append(out, Symbol{
				Kind:       "method",
				Name:       name,
				Signature:  oneline(text(src, x), 160),
				Visibility: goVis(name),
				StartLine:  line(x),
			})
			return
		}
		for i := uint(0); i < x.ChildCount(); i++ {
			walk(x.Child(i))
		}
	}
	walk(iface)
	return out
}

// javaMods finds the modifiers node of a declaration (field name is
// "modifiers" in tree-sitter-java, with a positional fallback).
func javaMods(n *sitter.Node) *sitter.Node {
	if m := n.ChildByFieldName("modifiers"); m != nil {
		return m
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		if c := n.Child(i); c.Kind() == "modifiers" {
			return c
		}
	}
	return nil
}

// javaVis maps Java modifiers to visibility. Interface members are public
// by default.
func javaVis(src []byte, mods *sitter.Node, inInterface bool) string {
	t := text(src, mods)
	switch {
	case strings.Contains(t, "public"), strings.Contains(t, "protected"):
		return "exported"
	case strings.Contains(t, "private"):
		return "private"
	case inInterface:
		return "exported"
	default:
		return "package"
	}
}

// javaSig is the declaration text up to (excluding) the body; abstract and
// interface declarations end at the semicolon instead.
func javaSig(src []byte, m *sitter.Node) string {
	body := m.ChildByFieldName("body")
	if body == nil {
		return strings.TrimSuffix(oneline(text(src, m), 200), ";")
	}
	return oneline(string(src[m.StartByte():body.StartByte()]), 200)
}

func extractJava(f *FileFacts, src []byte, root *sitter.Node) {
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		switch n.Kind() {
		case "package_declaration":
			p := oneline(text(src, n), 0)
			p = strings.TrimSuffix(strings.TrimPrefix(p, "package "), ";")
			f.Package = p
			return
		case "import_declaration":
			f.Imports = append(f.Imports, oneline(text(src, n), 0))
			return
		case "class_declaration", "interface_declaration":
			kind := "class"
			inInterface := n.Kind() == "interface_declaration"
			if inInterface {
				kind = "interface"
			}
			name := text(src, n.ChildByFieldName("name"))
			sym := Symbol{
				Kind:       kind,
				Name:       name,
				Visibility: javaVis(src, javaMods(n), false),
				StartLine:  line(n),
			}
			if body := n.ChildByFieldName("body"); body != nil {
				end := body.StartByte()
				sym.Signature = oneline(string(src[n.StartByte():end]), 160)
				sym.Children = javaMembers(src, body, inInterface)
			} else {
				sym.Signature = oneline(text(src, n), 160)
			}
			f.Symbols = append(f.Symbols, sym)
			return
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
}

func javaMembers(src []byte, body *sitter.Node, inInterface bool) []Symbol {
	var out []Symbol
	for i := uint(0); i < body.NamedChildCount(); i++ {
		m := body.NamedChild(i)
		vis := javaVis(src, javaMods(m), inInterface)
		switch m.Kind() {
		case "method_declaration", "constructor_declaration":
			kind := "method"
			if m.Kind() == "constructor_declaration" {
				kind = "constructor"
			}
			out = append(out, Symbol{
				Kind:       kind,
				Name:       text(src, m.ChildByFieldName("name")),
				Signature:  javaSig(src, m),
				Visibility: vis,
				StartLine:  line(m),
			})
		case "field_declaration", "constant_declaration":
			out = append(out, Symbol{
				Kind:       "field",
				Signature:  oneline(text(src, m), 160),
				Visibility: vis,
				StartLine:  line(m),
			})
		}
	}
	return out
}
