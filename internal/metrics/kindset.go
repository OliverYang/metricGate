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
	// Function-like units per language
	goFuncKinds   = NewKindSet(parser.TsGo, "function_declaration", "method_declaration")
	javaFuncKinds = NewKindSet(parser.TsJava, "method_declaration", "constructor_declaration")

	// Branch points (cyclomatic +1, cognitive +1+nesting)
	goBranchKinds   = NewKindSet(parser.TsGo, "if_statement", "for_statement")
	javaBranchKinds = NewKindSet(parser.TsJava, "if_statement", "for_statement", "enhanced_for_statement", "while_statement", "do_statement")

	// Switch statements (cyclomatic counts cases, cognitive +1+nesting)
	goSwitchKinds   = NewKindSet(parser.TsGo, "expression_switch_statement", "type_switch_statement", "select_statement")
	javaSwitchKinds = NewKindSet(parser.TsJava, "switch_expression")

	// Case labels (cyclomatic +1; Java `default` excluded via token check)
	goCaseKinds   = NewKindSet(parser.TsGo, "expression_case", "type_case", "communication_case")
	javaCaseKinds = NewKindSet(parser.TsJava, "switch_label")

	// Flat cognitive complexity (no nesting increase)
	goFlatCog     = NewKindSet(parser.TsGo) // empty for Go
	javaFlatCog   = NewKindSet(parser.TsJava, "catch_clause")
	goLambdaKinds = NewKindSet(parser.TsGo, "func_literal")
	javaLambdaKinds = NewKindSet(parser.TsJava, "lambda_expression")
)

// LangProfile holds all node kind sets for a language.
type LangProfile struct {
	FuncKinds   *NodeKindSet
	BranchKinds *NodeKindSet
	SwitchKinds *NodeKindSet
	CaseKinds   *NodeKindSet
	FlatCog     *NodeKindSet
	LambdaKinds *NodeKindSet
	TernaryKind string // "" when language has no ternary
}

var (
	goProfile = &LangProfile{
		FuncKinds:   goFuncKinds,
		BranchKinds: goBranchKinds,
		SwitchKinds: goSwitchKinds,
		CaseKinds:   goCaseKinds,
		FlatCog:     goFlatCog,
		LambdaKinds: goLambdaKinds,
		TernaryKind: "", // Go has no ternary
	}

	javaProfile = &LangProfile{
		FuncKinds:   javaFuncKinds,
		BranchKinds: javaBranchKinds,
		SwitchKinds: javaSwitchKinds,
		CaseKinds:   javaCaseKinds,
		FlatCog:     javaFlatCog,
		LambdaKinds: javaLambdaKinds,
		TernaryKind: "ternary_expression",
	}
)

// ProfileFor returns the language-specific node kind sets.
// This is the single source of truth for AST traversal logic.
func ProfileFor(lang parser.LanguageID) *LangProfile {
	switch lang {
	case parser.LangGo:
		return goProfile
	case parser.LangJava:
		return javaProfile
	default:
		return goProfile // fallback
	}
}
