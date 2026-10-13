package metrics

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/parser"
)

// memberReader reports what one class-body member declares: the field names
// it introduces and whether it is a method. A member is never both.
type memberReader func(src []byte, member *sitter.Node) (fields []string, method bool)

// classSpec describes how one grammar spells a class-like declaration. The
// CK family is defined over classes, so a language without an entry here
// contributes no class metrics at all (Go: no classes).
type classSpec struct {
	// decls maps a declaration node kind to the ClassStats.Kind label.
	decls map[string]string
	// members maps a body-member node kind to its reader. Kinds absent from
	// the map are neither fields nor methods (access specifiers, comments,
	// nested type declarations).
	members map[string]memberReader
	// impls marks a language that keeps methods outside the type body (Rust
	// impl blocks); the measurer attributes those methods by type name.
	impls bool
	// super returns the single parent type name, "" when this grammar has no
	// inheritance in the position.
	super func(src []byte, decl *sitter.Node) string
	// types gathers the type names a declaration references (CBO). It is
	// called once per node with that node's parent, so a grammar can tell a
	// type position from a declaration name. nil for a language whose source
	// carries no static type references at all.
	types func(src []byte, n, parent *sitter.Node, refs map[string]bool)
}

// classSpecs is the per-language spelling of classes. Adding a language to
// parser.Supported() does not add it here: the CK family needs a member and
// inheritance story per grammar, and guessing one would fabricate metrics.
var classSpecs = map[parser.LanguageID]*classSpec{
	parser.LangJava: {
		decls: map[string]string{
			"class_declaration":     "class",
			"interface_declaration": "interface",
		},
		members: map[string]memberReader{
			"field_declaration":       declaratorFieldMember,
			"method_declaration":      methodMember,
			"constructor_declaration": methodMember,
		},
		super: javaSuper,
		types: typedRefs,
	},
	parser.LangTS: tsClassSpec(),
	// TSX shares TypeScript's class/interface shapes: JSX adds element
	// nodes inside method bodies but does not change what a member is.
	parser.LangTSX: tsClassSpec(),
	parser.LangJS: {
		decls: map[string]string{"class_declaration": "class"},
		members: map[string]memberReader{
			"field_definition":  jsFieldMember,
			"method_definition": methodMember,
		},
		super: heritageSuper,
		types: heritageTypes,
	},
	parser.LangPy: {
		decls: map[string]string{"class_definition": "class"},
		members: map[string]memberReader{
			"function_definition":  methodMember,
			"decorated_definition": pyDecoratedMember,
			"expression_statement": pyAssignMember,
		},
		super: pySuper,
		types: pyTypes,
	},
	parser.LangCpp: {
		decls: map[string]string{
			"class_specifier":  "class",
			"struct_specifier": "struct",
		},
		members: map[string]memberReader{
			"field_declaration":   cFieldMember,
			"function_definition": methodMember,
		},
		super: cppSuper,
		types: typedRefs,
	},
	parser.LangC: {
		decls: map[string]string{"struct_specifier": "struct"},
		members: map[string]memberReader{
			"field_declaration": cFieldMember,
		},
		types: typedRefs,
	},
	parser.LangCS: {
		decls: map[string]string{
			"class_declaration":     "class",
			"interface_declaration": "interface",
			"struct_declaration":    "struct",
			"record_declaration":    "record",
		},
		members: map[string]memberReader{
			"field_declaration":       declaratorFieldMember,
			"property_declaration":    nameFieldMember,
			"method_declaration":      methodMember,
			"constructor_declaration": methodMember,
			"destructor_declaration":  methodMember,
			"operator_declaration":    methodMember,
		},
		super: csSuper,
		types: csTypes,
	},
	parser.LangRust: {
		decls: map[string]string{
			"struct_item": "struct",
			"trait_item":  "trait",
			"enum_item":   "enum",
		},
		members: map[string]memberReader{
			"field_declaration":       nameFieldMember,
			"function_item":           methodMember,
			"function_signature_item": methodMember,
		},
		impls: true,
		types: typedRefs,
	},
}

// HasClasses reports whether a language has a class spelling and therefore
// produces the CK family. The engine's catalogue and the context pack use it
// so they never advertise a class metric a language cannot emit.
// tsClassSpec is the TypeScript class census, shared with TSX: the two
// grammars agree on every declaration kind, so the census lives once.
func tsClassSpec() *classSpec {
	return &classSpec{
		decls: map[string]string{
			"class_declaration":          "class",
			"abstract_class_declaration": "class",
			"interface_declaration":      "interface",
		},
		members: map[string]memberReader{
			"public_field_definition":   jsFieldMember,
			"property_signature":        jsFieldMember,
			"method_definition":         methodMember,
			"method_signature":          methodMember,
			"abstract_method_signature": methodMember,
		},
		super: heritageSuper,
		types: tsTypes,
	}
}

func HasClasses(lang string) bool {
	return classSpecs[parser.LanguageID(lang)] != nil
}

// classDeclKinds are the declaration kinds that open a new class scope in
// any supported grammar. A reference inside one belongs to that declaration,
// which is measured on its own.
var classDeclKinds = map[string]bool{
	"class_declaration": true, "interface_declaration": true,
	"abstract_class_declaration": true, "class_definition": true,
	"class_specifier": true, "struct_specifier": true,
	"struct_declaration": true, "record_declaration": true,
	"struct_item": true, "trait_item": true, "enum_item": true,
	"impl_item": true,
}

// methodMember marks a body member that is a method or constructor.
func methodMember(src []byte, n *sitter.Node) ([]string, bool) {
	return nil, true
}

// nameFieldMember reads a member whose declared name sits in the `name`
// field (TypeScript signatures, C# properties, Rust fields).
func nameFieldMember(src []byte, n *sitter.Node) ([]string, bool) {
	if name := text(src, n.ChildByFieldName("name")); name != "" {
		return []string{name}, false
	}
	return nil, false
}

// jsFieldMember reads a TypeScript or JavaScript class field. TypeScript
// uses the `name` field for every field form; JavaScript's field_definition
// carries the name in the `property` field (including `#private` fields).
func jsFieldMember(src []byte, n *sitter.Node) ([]string, bool) {
	if fields, _ := nameFieldMember(src, n); len(fields) > 0 {
		return fields, false
	}
	if name := text(src, n.ChildByFieldName("property")); name != "" {
		return []string{name}, false
	}
	return nil, false
}

// declaratorFieldMember reads a Java or C# field declaration through its
// declarators (`int a, b;` declares two fields). C# nests the declarators one
// level deeper, inside a variable_declaration node.
func declaratorFieldMember(src []byte, n *sitter.Node) ([]string, bool) {
	names := declaratorNames(src, n)
	if len(names) == 0 {
		for i := uint(0); i < n.NamedChildCount(); i++ {
			if c := n.NamedChild(i); c.Kind() == "variable_declaration" {
				names = append(names, declaratorNames(src, c)...)
			}
		}
	}
	return names, false
}

// pyDecoratedMember reads a decorated Python method: the decorator wraps the
// function_definition in a `definition` field.
func pyDecoratedMember(src []byte, n *sitter.Node) ([]string, bool) {
	if d := n.ChildByFieldName("definition"); d != nil && d.Kind() == "function_definition" {
		return nil, true
	}
	return nil, false
}

// pyAssignMember reads a Python class-body statement. Python has no field
// declaration node: a field is an assignment whose left side is a plain
// identifier (`count: int = 0`).
func pyAssignMember(src []byte, n *sitter.Node) ([]string, bool) {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		a := n.NamedChild(i)
		if a.Kind() != "assignment" {
			continue
		}
		if left := a.ChildByFieldName("left"); left != nil && left.Kind() == "identifier" {
			return []string{text(src, left)}, false
		}
	}
	return nil, false
}

// cFieldMember reads a C or C++ field_declaration. A function_declarator
// makes it a method declaration (no body, so WMC counts the minimum 1);
// anything else declares data through a declarator chain.
func cFieldMember(src []byte, n *sitter.Node) ([]string, bool) {
	decl := n.ChildByFieldName("declarator")
	if decl == nil {
		return nil, false
	}
	if decl.Kind() == "function_declarator" {
		return nil, true
	}
	if name := cDeclaratorName(src, decl); name != "" {
		return []string{name}, false
	}
	return nil, false
}

// cDeclaratorName walks a C/C++ declarator chain (pointer, array, reference
// declarators) down to the declared identifier. It returns "" for a
// function_declarator, which is a method and not a data member.
func cDeclaratorName(src []byte, n *sitter.Node) string {
	for d := n; d != nil; {
		switch d.Kind() {
		case "field_identifier", "identifier", "type_identifier":
			return text(src, d)
		case "function_declarator":
			return ""
		}
		d = d.ChildByFieldName("declarator")
	}
	return ""
}

// javaSuper reads `extends Base` (a superclass field) or, for an interface,
// the first `extends_interfaces` entry. Java resolves only the first parent:
// the CK depth is a chain, not a graph.
func javaSuper(src []byte, decl *sitter.Node) string {
	if sc := decl.ChildByFieldName("superclass"); sc != nil {
		return firstTypeName(src, sc)
	}
	for i := uint(0); i < decl.NamedChildCount(); i++ {
		if child := decl.NamedChild(i); child.Kind() == "extends_interfaces" {
			return firstTypeName(src, child)
		}
	}
	return ""
}

// heritageSuper reads a TypeScript or JavaScript `extends` clause. The clause
// sits in a positional class_heritage child, not a field, and TypeScript
// wraps it in an extends_clause while JavaScript carries the identifier
// directly. `implements` is deliberately not a parent: it is a contract, not
// an inheritance chain.
func heritageSuper(src []byte, decl *sitter.Node) string {
	for i := uint(0); i < decl.NamedChildCount(); i++ {
		child := decl.NamedChild(i)
		if child.Kind() != "class_heritage" {
			continue
		}
		return firstTypeName(src, child)
	}
	return ""
}

// pySuper reads a Python base list. Only a plain name or a dotted name is a
// parent this name-based measurer can follow; a keyword argument such as
// `metaclass=Meta` configures the class and is not a base.
func pySuper(src []byte, decl *sitter.Node) string {
	supers := decl.ChildByFieldName("superclasses")
	if supers == nil {
		return ""
	}
	for i := uint(0); i < supers.NamedChildCount(); i++ {
		child := supers.NamedChild(i)
		if child.Kind() == "keyword_argument" {
			continue
		}
		if name := lastDottedName(text(src, child)); name != "" {
			return name
		}
	}
	return ""
}

// cppSuper reads the first base in a C++ base_class_clause. The clause also
// carries access specifiers (public/protected/private) and, in a
// template_specifier, its own arguments, so only a plain type name is taken.
func cppSuper(src []byte, decl *sitter.Node) string {
	for i := uint(0); i < decl.NamedChildCount(); i++ {
		if child := decl.NamedChild(i); child.Kind() == "base_class_clause" {
			return firstTypeName(src, child)
		}
	}
	return ""
}

// csSuper reads the first entry of a C# base_list. A base_list has no
// fields: its named children are the base type names themselves, so the
// first identifier that is not a primitive is the parent.
func csSuper(src []byte, decl *sitter.Node) string {
	for i := uint(0); i < decl.NamedChildCount(); i++ {
		if child := decl.NamedChild(i); child.Kind() == "base_list" {
			return firstTypeName(src, child)
		}
	}
	return ""
}

// firstTypeName returns the first type-ish name inside a node, or "" when
// the node carries none. It accepts both the type_identifier spelling and a
// bare identifier (JavaScript heritage, C# base_list).
func firstTypeName(src []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	switch n.Kind() {
	case "type_identifier", "identifier", "scoped_type_identifier", "qualified_name":
		return lastDottedName(text(src, n))
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		if name := firstTypeName(src, n.Child(i)); name != "" {
			return name
		}
	}
	return ""
}

// lastDottedName reduces a possibly qualified type name to its last segment
// (`App.Util.Helper` → `Helper`, `geo::Shape` → `Shape`) so a parent name
// matches the declaration index, which stores bare names.
func lastDottedName(name string) string {
	if i := strings.LastIndexAny(name, ".:"); i >= 0 && i+1 < len(name) {
		return name[i+1:]
	}
	return name
}
