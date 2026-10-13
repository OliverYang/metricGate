package metrics

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// typedRefs collects the type names a declaration references. It is the CBO
// input for every grammar that spells a type reference as a type_identifier,
// qualified_name or scoped_type_identifier (Java, TypeScript, C, C++, C#,
// Rust). Nested type declarations are their own class scope and are not
// counted as coupling of the enclosing one.
func typedRefs(src []byte, n, parent *sitter.Node, refs map[string]bool) {
	switch n.Kind() {
	case "type_identifier", "scoped_type_identifier", "qualified_name":
		name := lastDottedName(text(src, n))
		if name != "" && !nonClassNames[name] {
			refs[name] = true
		}
	}
}

// tsTypes counts TypeScript type references. TypeScript spells `implements`
// targets as type_identifier but leaves the `extends` target a bare
// identifier, so the heritage position has to be read explicitly.
func tsTypes(src []byte, n, parent *sitter.Node, refs map[string]bool) {
	heritageTypes(src, n, parent, refs)
	typedRefs(src, n, parent, refs)
}

// heritageTypes counts a JavaScript or TypeScript `extends` target. It is the
// whole CBO input for JavaScript, whose source carries no other type
// reference: a class that extends something is coupled to it.
func heritageTypes(src []byte, n, parent *sitter.Node, refs map[string]bool) {
	if n.Kind() != "identifier" || parent == nil {
		return
	}
	switch parent.Kind() {
	case "extends_clause", "class_heritage":
		if name := lastDottedName(text(src, n)); name != "" {
			refs[name] = true
		}
	}
}

// pyTypes collects the type names a Python declaration references. Python
// has no type_identifier: coupling appears as a base class, an annotation or
// a constructor call. Only capitalised bare names outside a declaration-name
// position are taken — the convention that distinguishes a type from a
// variable without a type system.
func pyTypes(src []byte, n, parent *sitter.Node, refs map[string]bool) {
	if n.Kind() != "identifier" || fieldIn(parent, n) == "name" {
		return
	}
	if name := text(src, n); isTypeName(name) {
		refs[name] = true
	}
}

// csTypes collects the type names a C# declaration references. A C# member
// name is an identifier too (`public int P { get; set; }` declares P), so
// only type positions count: a `type`/`returns` field, a generic argument, or
// a base_list entry. Built-in types are predefined_type nodes and never
// reach here.
func csTypes(src []byte, n, parent *sitter.Node, refs map[string]bool) {
	switch n.Kind() {
	case "identifier", "qualified_name", "generic_name":
	default:
		return
	}
	if !csTypePosition(parent, n) {
		return
	}
	if name := lastDottedName(text(src, n)); name != "" && !nonClassNames[name] {
		refs[name] = true
	}
}

// csTypePosition reports whether a node sits where C# expects a type name.
func csTypePosition(parent, n *sitter.Node) bool {
	if parent == nil {
		return false
	}
	if parent.Kind() == "base_list" {
		return true
	}
	switch fieldIn(parent, n) {
	case "type", "returns", "type_arguments":
		return true
	}
	return false
}

// fieldIn reports the field name a child occupies in its parent, "" when the
// position is unnamed or the parent is unknown.
func fieldIn(parent, child *sitter.Node) string {
	if parent == nil || child == nil {
		return ""
	}
	for i := uint(0); i < parent.ChildCount(); i++ {
		if sameNode(parent.Child(i), child) {
			return parent.FieldNameForChild(uint32(i))
		}
	}
	return ""
}

// sameNode reports whether two nodes denote the same position in the tree.
func sameNode(a, b *sitter.Node) bool {
	return a.StartByte() == b.StartByte() && a.EndByte() == b.EndByte() && a.Kind() == b.Kind()
}

// isTypeName reports whether a bare identifier follows the naming convention
// for a type (upper camel case, or an all-caps acronym). It is the only
// signal available without a type system, and it errs toward silence: a
// lowercase class name is missed rather than a variable counted as coupling.
func isTypeName(name string) bool {
	if name == "" {
		return false
	}
	r := rune(name[0])
	if r < 'A' || r > 'Z' {
		return false
	}
	return name != "True" && name != "False" && name != "None"
}

// nonClassNames are names that are never an object coupling: the primitives
// and built-in scalar types of the supported languages.
var nonClassNames = map[string]bool{
	"int": true, "long": true, "short": true, "byte": true,
	"char": true, "boolean": true, "float": true, "double": true,
	"void": true, "var": true, "string": true, "bool": true,
	"uint": true, "uint64": true, "int64": true, "int32": true,
	"uint32": true, "usize": true, "isize": true, "f32": true,
	"f64": true, "i8": true, "i16": true, "i32": true, "i64": true,
	"u8": true, "u16": true, "u32": true, "u64": true,
	"object": true, "dynamic": true, "decimal": true, "sbyte": true,
	"nint": true, "nuint": true, "any": true, "number": true,
	"unknown": true, "never": true, "undefined": true,
	"null": true, "nil": true,
}
