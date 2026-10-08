package rules

import (
	"fmt"

	"metricgate/internal/model"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// maxChainDepth reads the max_depth option (an int from YAML, possibly
// float64 via some unmarshallers) with the published default of 3 —
// Lanza & Marinescu's chain-size detection strategy (> 3) from
// "Object-Oriented Metrics in Practice".
func maxChainDepth(opts map[string]any) int {
	v, ok := opts["max_depth"]
	if !ok {
		return 3
	}
	switch n := v.(type) {
	case int:
		if n > 0 {
			return n
		}
	case float64:
		if n > 0 {
			return int(n)
		}
	}
	return 3
}

// chainFinding builds the shared finding for a too-deep message chain.
func chainFinding(node *sitter.Node, depth, max int, ctx *MatchCtx) *model.Finding {
	return &model.Finding{
		StartLine: int(node.StartPosition().Row) + 1,
		EndLine:   int(node.EndPosition().Row) + 1,
		Message:   fmt.Sprintf("call chain of depth %d (max %d): extract the intermediate step into a named variable or function — each link in the chain couples this code to one more class's internal navigation", depth, max),
	}
}

// nestedInChain reports whether the matched node is itself the tail of a
// larger call chain — for Go, the operand of an enclosing selector; for
// Java, the object field of an enclosing method_invocation. Inner links
// of one offending chain must not be billed as separate findings. Node
// identity is compared by byte span — go-tree-sitter hands out fresh node
// values per traversal, so pointer == is unreliable.
func nestedInChain(node *sitter.Node) bool {
	parent := node.Parent()
	if parent == nil {
		return false
	}
	sameSpan := func(a, b *sitter.Node) bool {
		return a.StartByte() == b.StartByte() && a.EndByte() == b.EndByte()
	}
	switch node.Kind() {
	case "call_expression": // Go: chain hop is selector(function) wrapping the inner call as operand
		return parent.Kind() == "selector_expression" && sameSpan(parent.ChildByFieldName("operand"), node)
	case "method_invocation": // Java: chain links are method_invocations in the object field
		return parent.Kind() == "method_invocation" && sameSpan(parent.ChildByFieldName("object"), node)
	}
	return false
}
