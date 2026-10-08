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
)

// LangProfile holds the node kinds a language measures functions on.
type LangProfile struct {
	FuncKinds *NodeKindSet
}

var (
	goProfile = &LangProfile{
		FuncKinds: goFuncKinds,
	}

	javaProfile = &LangProfile{
		FuncKinds: javaFuncKinds,
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
