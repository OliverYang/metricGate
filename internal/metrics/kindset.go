// Package metrics computes structural metrics directly from the tree-sitter
// AST. It is policy-free: it only measures. Threshold enforcement lives in
// the engine. See docs/metrics.md for the exact definitions per language and
// their (documented) deviations from SonarSource's published specs.
package metrics

import (
	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/parser"
)

// NodeKindSet is a typed set of node kinds for a specific language grammar.
// It provides efficient O(n) membership testing without string allocations.
//
// Design inspiration from ast-metrics' TypeSet, but simplified for metricgate's
// needs. The key benefit is centralized definition of node kinds, eliminating
// repetition across metrics functions and making language-specific behavior
// explicit and testable.
type NodeKindSet struct {
	Language *sitter.Language
	Kinds    []string
}

// Has reports whether the given node's kind is in this set.
// Safe to call with nil node or nil set.
func (s *NodeKindSet) Has(n *sitter.Node) bool {
	if s == nil || n == nil || s.Language == nil {
		return false
	}
	for _, k := range s.Kinds {
		if n.Kind() == k {
			return true
		}
	}
	return false
}

// NewKindSet creates a new NodeKindSet with the given language and kinds.
func NewKindSet(lang *sitter.Language, kinds ...string) *NodeKindSet {
	return &NodeKindSet{
		Language: lang,
		Kinds:    kinds,
	}
}

// -----------------------------------------------------------------------------
// Language profiles using NodeKindSet (replaces map[string]bool patterns)
// -----------------------------------------------------------------------------

var (
	// Function-like units per language. These are the only node kinds this
	// file names: which constructs are *decision* points belongs to the IR
	// adapters (go_adapter.go, java_adapter.go), so a language's grammar
	// knowledge lives in exactly one place.
	goFuncKinds   = NewKindSet(parser.TsGo, "function_declaration", "method_declaration")
	javaFuncKinds = NewKindSet(parser.TsJava, "method_declaration", "constructor_declaration")
	// tsFuncKinds serves both the .ts and .tsx grammars: TSX is TypeScript
	// with JSX elements added, and the constructs a class declares are the
	// same. The two grammars only differ where a JSX node can appear.
	tsFuncKinds  = NewKindSet(parser.TsTypescript, "function_declaration", "generator_function_declaration", "method_definition", "class_declaration", "abstract_class_declaration")
	tsxFuncKinds = NewKindSet(parser.TsTsx, "function_declaration", "generator_function_declaration", "method_definition", "class_declaration", "abstract_class_declaration")
	jsFuncKinds  = NewKindSet(parser.TsJavascript, "function_declaration", "generator_function_declaration", "method_definition", "class_declaration")
	// jsNamedFuncUnit is shared by all three grammars: the shape it recognises
	// is the same in JS, TS and TSX, and the TS-only type annotations do not
	// change which node is a module-level binding.
	jsNamedFuncUnit = isJSModuleFunc
	pyFuncKinds     = NewKindSet(parser.TsPython, "function_definition", "class_definition")
	cFuncKinds      = NewKindSet(parser.TsC, "function_definition")
	cppFuncKinds    = NewKindSet(parser.TsCpp, "function_definition")
	csFuncKinds     = NewKindSet(parser.TsCSharp, "method_declaration", "constructor_declaration", "destructor_declaration", "operator_declaration", "local_function_statement")
	rustFuncKinds   = NewKindSet(parser.TsRust, "function_item")
	// sqlFuncKinds names the two declarative units SQL actually has. A table
	// or a view is not a unit of behaviour, and the grammar's
	// `function_declaration` is a PL/SQL local assignment, not a function.
	sqlFuncKinds = NewKindSet(parser.TsSQL, "create_function", "create_procedure")
	// verilogFuncKinds names function_declaration, task_declaration and the
	// class method. A module is a file-level container, not a unit of
	// behaviour, so it is deliberately absent — the same call Go's
	// function_declaration and a SQL table get.
	verilogFuncKinds = NewKindSet(parser.TsVerilog, "function_declaration", "task_declaration", "class_constructor_declaration")
	// vbFuncKinds names the method and constructor declarations. A
	// class_block is a type container, not a unit of behaviour.
	vbFuncKinds = NewKindSet(parser.TsVB, "method_declaration", "constructor_declaration")
)

// LangProfile holds the node kinds a language measures functions on.
type LangProfile struct {
	FuncKinds *NodeKindSet
	// NamedFuncUnit covers the function units a grammar does not spell as a
	// declaration. JS and TS bind a module's function to a name with
	// `const f = (x) => {…}`, which is a variable_declarator holding a
	// function expression: no kind set can see it, because the same kind
	// (`arrow_function`) is also an inline closure that must stay folded into
	// its enclosing unit. nil means "the kind set is the whole answer", which
	// is true for every other language.
	NamedFuncUnit func(n *sitter.Node) bool
}

// HasFunc reports whether n opens a function-like unit in this language. A nil
// profile — a language that declares no function kinds — reports false.
func (p *LangProfile) HasFunc(n *sitter.Node) bool {
	if p == nil {
		return false
	}
	if p.FuncKinds.Has(n) {
		return true
	}
	return p.NamedFuncUnit != nil && p.NamedFuncUnit(n)
}

// langProfiles is the per-language function-kind table, the single source of
// truth for AST traversal. A language missing from this map declares no
// function kinds at all: it never inherits another language's table. An
// omission is a red test in TestLanguageParity, not a silent degradation.
var langProfiles = map[parser.LanguageID]*LangProfile{
	parser.LangGo:      {FuncKinds: goFuncKinds},
	parser.LangJava:    {FuncKinds: javaFuncKinds},
	parser.LangTS:      {FuncKinds: tsFuncKinds, NamedFuncUnit: jsNamedFuncUnit},
	parser.LangTSX:     {FuncKinds: tsxFuncKinds, NamedFuncUnit: jsNamedFuncUnit},
	parser.LangJS:      {FuncKinds: jsFuncKinds, NamedFuncUnit: jsNamedFuncUnit},
	parser.LangPy:      {FuncKinds: pyFuncKinds},
	parser.LangC:       {FuncKinds: cFuncKinds},
	parser.LangCpp:     {FuncKinds: cppFuncKinds},
	parser.LangCS:      {FuncKinds: csFuncKinds},
	parser.LangRust:    {FuncKinds: rustFuncKinds},
	parser.LangSQL:     {FuncKinds: sqlFuncKinds},
	parser.LangVerilog: {FuncKinds: verilogFuncKinds},
	parser.LangVB:      {FuncKinds: vbFuncKinds},
}

// ProfileFor returns the function-kind profile of a language, or nil when the
// language declares none. Callers treat nil as "measure nothing".
func ProfileFor(lang parser.LanguageID) *LangProfile {
	return langProfiles[lang]
}
