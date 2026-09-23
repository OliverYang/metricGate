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
}

// Extract reads the package clause and the imports of one parsed file.
// Unlike the facts extractor it keeps the import line, because an
// architecture violation has to be anchored where the dependency was written.
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
	walk(root, func(n *sitter.Node) bool {
		switch n.Kind() {
		case "package_clause":
			if id := childByKind(n, "package_identifier"); id != nil {
				out.Package = text(src, id)
			}
		case "import_spec":
			if path := n.ChildByFieldName("path"); path != nil {
				out.Imports = append(out.Imports, Import{
					Path: strings.Trim(text(src, path), "\"`"),
					Line: line(path),
				})
			}
			return false // an import spec never nests another one
		}
		return true
	})
	return out
}

func extractJava(src []byte, root *sitter.Node) Raw {
	var out Raw
	if root == nil {
		return out
	}
	walk(root, func(n *sitter.Node) bool {
		switch n.Kind() {
		case "package_declaration": // tree-sitter-java's name for the clause
			out.Package = slash(dottedName(src, n))
			return false
		case "import_declaration":
			path := dottedName(src, n)
			if path == "" {
				return false
			}
			out.Imports = append(out.Imports, Import{Path: slash(path), Line: line(n)})
			return false
		}
		return true
	})
	return out
}

// dottedName returns the package name a Java package/import clause carries: the
// scoped identifier when the name is qualified, the plain identifier when not.
func dottedName(src []byte, n *sitter.Node) string {
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
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

func text(src []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return string(src[n.StartByte():n.EndByte()])
}

func line(n *sitter.Node) int { return int(n.StartPosition().Row) + 1 }

func childByKind(n *sitter.Node, kind string) *sitter.Node {
	for i := uint(0); i < n.ChildCount(); i++ {
		if c := n.Child(i); c.Kind() == kind {
			return c
		}
	}
	return nil
}

// walk visits every node depth-first; visit returns false to prune a subtree.
func walk(n *sitter.Node, visit func(*sitter.Node) bool) {
	if !visit(n) {
		return
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		walk(n.Child(i), visit)
	}
}
