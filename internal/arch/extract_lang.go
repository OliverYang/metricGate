package arch

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Import extraction for the seven languages whose clause shapes differ from
// Go's and Java's.
//
// The layer check is the flagship capability — "packages may only depend in
// this direction" — and it is only as good as the import list it is given.
// Leaving these languages out did not produce a warning; it produced a graph
// with no edges, which reads exactly like a repository with no violations.
//
// Imports are recorded with their line because a violation must be anchored
// where the dependency was written.

// extractPy reads Python's import statements. The module tree is mapped to the
// file's directory, the same approximation Go uses (see packageID), because
// Python's package identity is its __init__.py ancestor and the arch graph
// keys packages by path anyway.
func extractPy(src []byte, root *sitter.Node) Raw {
	var out Raw
	if root == nil {
		return out
	}
	for _, decl := range children(root) {
		switch decl.Kind() {
		case "import_statement":
			out.Imports = append(out.Imports, pyImports(src, decl)...)
		case "import_from_statement":
			// Relative imports ("from . import x") name no resolvable
			// package, and an edge to nothing is noise; skip them. The
			// module is dotted exactly like an `import` statement's, so it
			// gets the same treatment — a dotted path would never match the
			// directory-shaped package name and the edge would vanish.
			m := decl.ChildByFieldName("module_name")
			if m != nil && m.Kind() != "relative_import" {
				out.Imports = append(out.Imports, Import{Path: slash(text(src, m)), Line: line(m)})
			}
		case "class_definition":
			out.Types++
			if pyAbstractClass(src, decl) {
				out.Abstract++
			}
		}
	}
	return out
}

// pyImports collects the modules of one `import a, b.c as d` statement. An
// aliased import carries the module in its name field; the alias is not part
// of the dependency.
func pyImports(src []byte, decl *sitter.Node) []Import {
	var out []Import
	for _, c := range children(decl) {
		switch c.Kind() {
		case "dotted_name":
			out = append(out, Import{Path: slash(text(src, c)), Line: line(c)})
		case "aliased_import":
			if n := c.ChildByFieldName("name"); n != nil {
				out = append(out, Import{Path: slash(text(src, n)), Line: line(c)})
			}
		}
	}
	return out
}

// pyAbstractClass reports whether a class declares an @abstractmethod member.
// Python has no abstract keyword, so the decorator is the only marker the
// source carries.
func pyAbstractClass(src []byte, class *sitter.Node) bool {
	body := class.ChildByFieldName("body")
	if body == nil {
		return false
	}
	for _, member := range children(body) {
		if member.Kind() != "decorated_definition" {
			continue
		}
		for _, d := range children(member) {
			if d.Kind() == "decorator" && strings.Contains(text(src, d), "abstractmethod") {
				return true
			}
		}
	}
	return false
}

// extractTS and extractJS share one scanner: the two grammars agree on every
// node name used here, and TypeScript only adds declaration kinds.
func extractTS(src []byte, root *sitter.Node) Raw { return extractJSFamily(src, root, true) }
func extractJS(src []byte, root *sitter.Node) Raw { return extractJSFamily(src, root, false) }

func extractJSFamily(src []byte, root *sitter.Node, ts bool) Raw {
	var out Raw
	if root == nil {
		return out
	}
	for _, decl := range children(root) {
		switch decl.Kind() {
		case "import_statement", "export_statement":
			if s := decl.ChildByFieldName("source"); s != nil {
				out.Imports = append(out.Imports, Import{Path: unquote(text(src, s)), Line: line(s)})
			}
			// `export class C {}` / `export interface I {}` declare a type at
			// the top level; the declaration hangs off the export wrapper.
			jsTypeDecl(&out, decl.ChildByFieldName("declaration"), ts)
		case "lexical_declaration", "variable_declaration":
			out.Imports = append(out.Imports, jsRequires(src, decl)...)
		default:
			jsTypeDecl(&out, decl, ts)
		}
	}
	return out
}

// jsTypeDecl counts one type declaration for Martin's abstractness census.
func jsTypeDecl(out *Raw, decl *sitter.Node, ts bool) {
	if decl == nil {
		return
	}
	switch decl.Kind() {
	case "class_declaration":
		out.Types++
	case "abstract_class_declaration", "interface_declaration":
		if ts {
			out.Types++
			out.Abstract++
		}
	case "enum_declaration", "type_alias_declaration":
		if ts {
			out.Types++
		}
	}
}

// jsRequires collects CommonJS requires: `const x = require("pkg")`. ESM
// imports arrive as import_statement and never reach here.
func jsRequires(src []byte, decl *sitter.Node) []Import {
	var out []Import
	for _, c := range children(decl) {
		if path, line, ok := requireCall(src, c); ok {
			out = append(out, Import{Path: path, Line: line})
		}
	}
	return out
}

// requireCall reports the module path and line of one `require("...")`
// declarator, if the node is one.
func requireCall(src []byte, c *sitter.Node) (string, int, bool) {
	if c.Kind() != "variable_declarator" {
		return "", 0, false
	}
	v := c.ChildByFieldName("value")
	if v == nil || v.Kind() != "call_expression" {
		return "", 0, false
	}
	fn := v.ChildByFieldName("function")
	if fn == nil || text(src, fn) != "require" {
		return "", 0, false
	}
	args := v.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return "", 0, false
	}
	return unquote(text(src, args.NamedChild(0))), line(v), true
}

// extractC reads C includes and the type census; extractCpp adds classes and
// descends namespaces, whose contents are types at the same graph level.
func extractC(src []byte, root *sitter.Node) Raw   { return extractCFamily(src, root, false) }
func extractCpp(src []byte, root *sitter.Node) Raw { return extractCFamily(src, root, true) }

func extractCFamily(src []byte, root *sitter.Node, cpp bool) Raw {
	var out Raw
	if root == nil {
		return out
	}
	for _, decl := range children(root) {
		if decl.Kind() == "preproc_include" {
			out.Imports = append(out.Imports, cIncludes(src, decl)...)
		}
	}
	cTypeCensus(&out, src, root)
	return out
}

// cIncludes reads one #include, quoted or angled, with its delimiters
// stripped. Both forms are kept: unlike the unknown-dependency rule — where a
// quoted include is by definition repo-local and therefore never an undeclared
// external dependency — the architecture graph is exactly the place a local
// header matters, because it is the edge between two of our own packages.
// Includes that name no package in the run drop out in Build, as all imports do.
func cIncludes(src []byte, decl *sitter.Node) []Import {
	p := decl.ChildByFieldName("path")
	if p == nil {
		return nil
	}
	return []Import{{Path: strings.Trim(text(src, p), "<>\""), Line: line(p)}}
}

// cTypeCensus counts type declarations recursively, descending namespaces and
// classes but never function bodies.
func cTypeCensus(out *Raw, src []byte, n *sitter.Node) {
	for _, c := range children(n) {
		switch c.Kind() {
		case "function_definition", "compound_statement":
			continue
		case "struct_specifier", "union_specifier", "enum_specifier", "class_specifier":
			if c.ChildByFieldName("name") != nil {
				out.Types++
			}
		case "type_definition":
			out.Types++
		}
		cTypeCensus(out, src, c)
	}
}

// extractCS reads C#'s usings and namespace, and counts its type declarations.
// The namespace becomes the package name because that is the unit C#'s
// dependency direction is declared in.
//
// The name keeps its dots. C# namespaces are dotted, not slashed, and a
// namespaced file is its own package whatever directory it sits in — so the
// only thing that can match a `using` is the dotted form. Everything
// downstream compares this against using paths, so both sides must use the
// same spelling.
func extractCS(src []byte, root *sitter.Node) Raw {
	var out Raw
	if root == nil {
		return out
	}
	csWalk(&out, src, root, "")
	return out
}

func csWalk(out *Raw, src []byte, n *sitter.Node, ns string) {
	for _, c := range children(n) {
		switch c.Kind() {
		case "namespace_declaration":
			name := ns
			if f := c.ChildByFieldName("name"); f != nil {
				name = text(src, f)
			}
			if out.Package == "" {
				out.Package = name
			}
			csWalk(out, src, c, name)
			continue
		case "using_directive":
			if p := csUsingPath(src, c); p != "" {
				out.Imports = append(out.Imports, Import{Path: p, Line: line(c)})
			}
			continue
		case "interface_declaration":
			out.Types++
			out.Abstract++
			continue
		case "class_declaration", "struct_declaration", "record_declaration", "enum_declaration":
			out.Types++
			if csIsAbstract(src, c) {
				out.Abstract++
			}
			continue
		}
		csWalk(out, src, c, ns)
	}
}

// csUsingPath reads a using directive's target. `using X = A.B;` carries both
// the alias and the target, and the qualified name is the dependency.
func csUsingPath(src []byte, c *sitter.Node) string {
	var id string
	for _, x := range children(c) {
		switch x.Kind() {
		case "qualified_name":
			return text(src, x)
		case "identifier":
			if id == "" {
				id = text(src, x)
			}
		}
	}
	return id
}

// csIsAbstract reports an explicit abstract modifier. C# also makes every
// interface member implicitly abstract, but those are not type declarations.
func csIsAbstract(src []byte, c *sitter.Node) bool {
	for _, x := range children(c) {
		if x.Kind() == "modifier" && text(src, x) == "abstract" {
			return true
		}
	}
	return false
}

// extractRust reads use declarations and counts type declarations. Rust's
// crate-internal module tree is approximated by the file's directory, the
// same way Go's is.
func extractRust(src []byte, root *sitter.Node) Raw {
	var out Raw
	if root == nil {
		return out
	}
	for _, decl := range children(root) {
		switch decl.Kind() {
		case "use_declaration":
			if a := decl.ChildByFieldName("argument"); a != nil {
				if p := rustUsePath(src, a); p != "" {
					out.Imports = append(out.Imports, Import{Path: p, Line: line(a)})
				}
			}
		case "struct_item", "enum_item", "trait_item", "union_item", "type_item":
			out.Types++
		}
	}
	return out
}

// rustUsePath rebuilds the dotted path of a use declaration. The grammar nests
// one scoped_identifier per `::`, so the path is reassembled from the inside
// out; `crate::` is stripped because the crate root is the repository itself.
func rustUsePath(src []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	switch n.Kind() {
	case "scoped_identifier":
		prefix := rustUsePath(src, n.ChildByFieldName("path"))
		name := text(src, n.ChildByFieldName("name"))
		return joinScope(prefix, name)
	case "scoped_use_list", "use_list", "use_as_clause":
		return rustUsePath(src, n.ChildByFieldName("path"))
	case "identifier":
		return text(src, n)
	case "crate":
		return ""
	case "self", "super":
		return ""
	}
	return ""
}

func joinScope(prefix, name string) string {
	if prefix == "" {
		return name
	}
	if name == "" {
		return prefix
	}
	return prefix + "::" + name
}

// unquote strips the quotes a JS/TS string literal is written with.
func unquote(s string) string {
	return strings.Trim(s, "\"'`")
}
