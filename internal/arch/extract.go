package arch

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/parser"
)

// Raw is what one file says about itself before identities are normalised.
type Raw struct {
	// Package is the declared package: a slash-separated name for Java
	// ("com/foo/bar"), the plain package clause for Go (whose graph identity is
	// its directory, supplied by the caller).
	Package string
	Imports []Import
	// Type census for Martin's abstractness: total type declarations in
	// the file and how many are abstract (Go interfaces; Java interfaces
	// and abstract classes).
	Types    int
	Abstract int
}

// Extract reads the package clause and the imports of one parsed file.
//
// Unlike the facts extractor it keeps the import line, because an architecture
// violation has to be anchored where the dependency was written. It scans the
// top level of the tree only: in both grammars the package clause and every
// import declaration are direct children of the file node, so a deep walk
// would cost a full extra traversal per file for nothing.
func Extract(lang parser.LanguageID, src []byte, root *sitter.Node) Raw {
	switch lang {
	case parser.LangGo:
		return extractGo(src, root)
	case parser.LangJava:
		return extractJava(src, root)
	default:
		return Raw{}
	}
}

func extractGo(src []byte, root *sitter.Node) Raw {
	var out Raw
	if root == nil {
		return out
	}
	for _, decl := range children(root) {
		switch decl.Kind() {
		case "package_clause":
			if id := childByKind(decl, "package_identifier"); id != nil {
				out.Package = text(src, id)
			}
		case "type_declaration":
			for _, spec := range children(decl) {
				if !strings.HasPrefix(spec.Kind(), "type_spec") {
					continue
				}
				out.Types++
				if t := spec.ChildByFieldName("type"); t != nil && t.Kind() == "interface_type" {
					out.Abstract++
				}
			}
		case "import_declaration":
			out.Imports = append(out.Imports, goImports(src, decl)...)
		}
	}
	return out
}

// goImports collects the import specs of one declaration, grouped or not.
func goImports(src []byte, decl *sitter.Node) []Import {
	specs := children(decl)
	if list := childByKind(decl, "import_spec_list"); list != nil {
		specs = children(list)
	}
	var out []Import
	for _, spec := range specs {
		if spec.Kind() != "import_spec" {
			continue
		}
		path := spec.ChildByFieldName("path")
		if path == nil {
			continue
		}
		out = append(out, Import{
			Path: strings.Trim(text(src, path), "\"`"),
			Line: line(path),
		})
	}
	return out
}

func extractJava(src []byte, root *sitter.Node) Raw {
	var out Raw
	if root == nil {
		return out
	}
	for _, decl := range children(root) {
		switch decl.Kind() {
		case "package_declaration": // tree-sitter-java's name for the clause
			out.Package = slash(dottedName(src, decl))
		case "import_declaration":
			if path := dottedName(src, decl); path != "" {
				out.Imports = append(out.Imports, Import{Path: slash(path), Line: line(decl)})
			}
		case "class_declaration", "interface_declaration":
			out.Types++
			if decl.Kind() == "interface_declaration" {
				out.Abstract++
				break
			}
			if mods := decl.ChildByFieldName("modifiers"); mods != nil &&
				strings.Contains(text(src, mods), "abstract") {
				out.Abstract++
			}
		}
	}
	return out
}

// dottedName returns the name a Java package/import clause carries: the scoped
// identifier when qualified, the plain identifier when not.
func dottedName(src []byte, n *sitter.Node) string {
	for _, c := range children(n) {
		switch c.Kind() {
		case "scoped_identifier", "identifier":
			return text(src, c)
		}
	}
	return ""
}

// slash turns a Java dotted package name into the segment form the matcher
// uses, so "com.foo.bar" and "com/foo/bar" name one package.
func slash(dotted string) string { return strings.ReplaceAll(dotted, ".", "/") }

func children(n *sitter.Node) []*sitter.Node {
	out := make([]*sitter.Node, 0, n.ChildCount())
	for i := uint(0); i < n.ChildCount(); i++ {
		out = append(out, n.Child(i))
	}
	return out
}

func childByKind(n *sitter.Node, kind string) *sitter.Node {
	for _, c := range children(n) {
		if c.Kind() == kind {
			return c
		}
	}
	return nil
}

func text(src []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return string(src[n.StartByte():n.EndByte()])
}

func line(n *sitter.Node) int { return int(n.StartPosition().Row) + 1 }
