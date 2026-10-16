// Parameter counting, per grammar.
//
// Split out of metrics.go when the three-language campaign (iter-14) pushed
// that file past its length budget: these are leaf helpers with no state, and
// they are the part of the package that grows one function per grammar quirk.
// See docs/metrics.md for the per-language definitions.
package metrics

import (
	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/parser"
)

// countParams counts declared parameters.
// Go: "a, b int" is two parameters, an unnamed "(int)" is one, a variadic
// declaration is one. Java: every formal_parameter / spread_parameter is one.
func countParams(lang parser.LanguageID, list *sitter.Node) int {
	// Whole-list grammars: every named child of the list is one parameter.
	switch lang {
	case parser.LangTS, parser.LangTSX, parser.LangJS:
		// JS/TS: identifier, required/optional_parameter, rest_pattern, ...
		return int(list.NamedChildCount())
	case parser.LangPy:
		// identifier, default_parameter, typed_parameter, splats, ...
		return int(list.NamedChildCount())
	case parser.LangRust:
		return countParamsRust(list)
	case parser.LangCS:
		// parameter, params-array parameter, arg-list ...
		return int(list.NamedChildCount())
	case parser.LangC, parser.LangCpp:
		// a parameter_declaration with one identifier is one parameter; an
		// abstract declaration (type only, unnamed) is one.
		return int(list.NamedChildCount())
	case parser.LangJava:
		return countParamsJava(list)
	case parser.LangSQL:
		return countParamsSQL(list)
	case parser.LangVerilog:
		return countParamsVerilog(list)
	}
	return countParamsGo(list)
}

// countParamsJava counts Java formal_parameter / spread_parameter children.
func countParamsJava(list *sitter.Node) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		switch list.NamedChild(i).Kind() {
		case "formal_parameter", "spread_parameter":
			n++
		}
	}
	return n
}

// countParamsRust counts parameter / self_parameter / variadic_parameter.
func countParamsRust(list *sitter.Node) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		switch list.NamedChild(i).Kind() {
		case "parameter", "self_parameter", "variadic_parameter":
			n++
		}
	}
	return n
}

// countParamsGo counts Go grouped declarations name-by-name: "a, b int" is
// two parameters, an unnamed "(int)" is one, a variadic declaration is one.
func countParamsGo(list *sitter.Node) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		decl := list.NamedChild(i)
		if decl.Kind() != "parameter_declaration" {
			n++ // variadic_parameter_declaration and friends
			continue
		}
		ids := 0
		for j := uint(0); j < decl.NamedChildCount(); j++ {
			if decl.NamedChild(j).Kind() == "identifier" {
				ids++
			}
		}
		if ids == 0 {
			n++ // unnamed parameter: (int)
		} else {
			n += ids
		}
	}
	return n
}

// countBoolParams counts parameters declared bool (Go) or boolean (Java).
// The flag-argument smell: every bool doubles the hidden states the caller
// must reason about, so a two-bool call site already hides four modes.
// Go grouped declarations count every name in the group: `a, b bool` is
// two bool parameters, not one.
func countBoolParams(lang parser.LanguageID, src []byte, list *sitter.Node) int {
	// Untyped JavaScript has no boolean annotations; TypeScript and the
	// Go/Java walk below each get their own counter (see countBoolParamsTS
	// and countBoolParamsClassic).
	switch lang {
	case parser.LangJS, parser.LangPy, parser.LangC, parser.LangCpp:
		// Untyped JavaScript and Python have no boolean annotations; C/C++
		// spells bool via stdbool or _Bool but declarations are type-only
		// positionally: count every parameter whose type is exactly bool.
		if lang == parser.LangC || lang == parser.LangCpp {
			return countBoolParamsC(src, list)
		}
		return 0
	case parser.LangTS, parser.LangTSX:
		return countBoolParamsTS(src, list)
	case parser.LangRust:
		return countBoolParamsRust(src, list)
	case parser.LangCS:
		return countBoolParamsCS(src, list)
	case parser.LangSQL:
		// SQL's boolean is a column type, not a call-site flag: the smell the
		// metric models does not exist in a language without boolean arguments
		// that a caller chooses between.
		return 0
	case parser.LangVerilog:
		// SystemVerilog has no boolean parameter type at all.
		return countBoolParamsVerilog(list)
	case parser.LangVB:
		// VB spells a type as `As Boolean` inside an as_clause, and its
		// case-insensitivity means the spelling can be any casing.
		return countBoolParamsVB(src, list)
	default:
		return countBoolParamsClassic(lang, src, list)
	}
}

// countBoolParamsC is the C/C++ arm: a parameter_declaration is bool when
// its type child (skipping const/volatile qualifiers) spells bool or
// _Bool. A C++ parameter declared `bool b` is a parameter_declaration with
// a primitive_type child "bool".
func countBoolParamsC(src []byte, list *sitter.Node) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		p := list.NamedChild(i)
		if p.Kind() != "parameter_declaration" && p.Kind() != "optional_parameter_declaration" && p.Kind() != "variadic_parameter_declaration" {
			continue
		}
		if cIsBoolType(src, p) {
			n++
		}
	}
	return n
}

// cIsBoolType reports whether the leading type of a C declaration node is
// bool/_Bool, looking past qualifiers at named children that are types.
func cIsBoolType(src []byte, decl *sitter.Node) bool {
	for i := uint(0); i < decl.NamedChildCount(); i++ {
		c := decl.NamedChild(i)
		switch c.Kind() {
		case "type_qualifier": // const/volatile: keep looking
			continue
		case "primitive_type":
			t := text(src, c)
			return t == "bool" || t == "_Bool"
		default:
			return false
		}
	}
	return false
}

// countBoolParamsRust is the Rust arm: a parameter is bool when its type
// field is the primitive_type "bool".
func countBoolParamsRust(src []byte, list *sitter.Node) int {
	return countBoolParamsTyped(src, list, "primitive_type", "bool")
}

// countBoolParamsCS is the C# arm: a parameter is bool when its type child
// is the predefined_type "bool".
func countBoolParamsCS(src []byte, list *sitter.Node) int {
	return countBoolParamsTyped(src, list, "predefined_type", "bool")
}

// countBoolParamsTyped counts parameters whose type field is the named type
// kind spelled as want. Rust and C# share the shape; only the kind of the
// type node differs.
func countBoolParamsTyped(src []byte, list *sitter.Node, kind, want string) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		p := list.NamedChild(i)
		if p.Kind() != "parameter" {
			continue
		}
		if t := p.ChildByFieldName("type"); t != nil && t.Kind() == kind && text(src, t) == want {
			n++
		}
	}
	return n
}

// countBoolParamsClassic is the Go/Java arm of the boolean-parameter count.
func countBoolParamsClassic(lang parser.LanguageID, src []byte, list *sitter.Node) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		n += classicParamCount(lang, src, list.NamedChild(i))
	}
	return n
}

// classicParamCount counts the bool parameters one node of a Go/Java
// parameter list declares, descending through grouping nodes.
func classicParamCount(lang parser.LanguageID, src []byte, node *sitter.Node) int {
	switch node.Kind() {
	case "parameter_declaration", "formal_parameter":
		if !classicBoolType(lang, src, node) {
			return 0
		}
		return classicGroupSize(node)
	case "spread_parameter":
		// Variadic bool stays uncounted (documented edge, same as before).
		return 0
	}
	n := 0
	for i := uint(0); i < node.NamedChildCount(); i++ {
		n += classicParamCount(lang, src, node.NamedChild(i))
	}
	return n
}

// classicBoolType reports whether a Go/Java parameter declaration is
// bool-typed: Go spells it the type_identifier "bool", Java boolean_type.
func classicBoolType(lang parser.LanguageID, src []byte, decl *sitter.Node) bool {
	for i := uint(0); i < decl.NamedChildCount(); i++ {
		c := decl.NamedChild(i)
		if lang == parser.LangJava {
			if c.Kind() == "boolean_type" {
				return true
			}
			continue
		}
		if c.Kind() == "type_identifier" && text(src, c) == "bool" {
			return true
		}
	}
	return false
}

// classicGroupSize counts the names a bool-typed declaration introduces:
// `a, b bool` is two bool parameters, not one. An unnamed parameter counts
// once, matching countParams.
func classicGroupSize(decl *sitter.Node) int {
	ids := 0
	for i := uint(0); i < decl.NamedChildCount(); i++ {
		if decl.NamedChild(i).Kind() == "identifier" {
			ids++
		}
	}
	if ids == 0 {
		return 1
	}
	return ids
}

// countBoolParamsTS is the TypeScript arm of the boolean-parameter count: the
// annotation is a type_annotation child of the parameter; a parameter is bool
// when that annotation is the type_identifier `boolean`.
//
// The annotation is read through its inner type node, not compared at the
// annotation itself: `a: boolean` parses as type_annotation → predefined_type
// → boolean, so a check for a `boolean` type_identifier never matched and every
// TypeScript function reported zero boolean parameters.
func countBoolParamsTS(src []byte, list *sitter.Node) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		if tsTypeName(src, list.NamedChild(i)) == "boolean" {
			n++
		}
	}
	return n
}

// tsTypeName returns the spelled type of one TypeScript parameter, or "" when
// it has no annotation. The type field of a required/optional parameter is a
// type_annotation wrapper, so the inner type node is what names the type.
func tsTypeName(src []byte, param *sitter.Node) string {
	ann := param.ChildByFieldName("type")
	if ann == nil {
		return ""
	}
	if ann.Kind() == "type_annotation" {
		if inner := ann.NamedChild(0); inner != nil {
			return text(src, inner)
		}
		return ""
	}
	return text(src, ann)
}
