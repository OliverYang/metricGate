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
// Java, the object field of an enclosing method_invocation; for C#, the
// expression field of an enclosing member_access_expression. Inner links
// of one offending chain must not be billed as separate findings. Node
// identity is compared by byte span — go-tree-sitter hands out fresh node
// values per traversal, so pointer == is unreliable.
// chainHopFields maps a call node kind to the enclosing node kind and field
// that mark the call as an inner link of a longer chain. Go, Java and C# all
// express a hop the same way — an outer node whose operand/object/expression
// field is exactly this call — so they share one table instead of three
// near-identical switch arms.
var chainHopFields = map[string]struct{ parent, field string }{
	"call_expression":       {"selector_expression", "operand"},
	"method_invocation":     {"method_invocation", "object"},
	"invocation_expression": {"member_access_expression", "expression"},
}

// sameSpan compares node identity by byte span — go-tree-sitter hands out
// fresh node values per traversal, so pointer == is unreliable.
func sameSpan(a, b *sitter.Node) bool {
	return a != nil && b != nil && a.StartByte() == b.StartByte() && a.EndByte() == b.EndByte()
}

func nestedInChain(node *sitter.Node) bool {
	parent := node.Parent()
	if parent == nil {
		return false
	}
	if hop, ok := chainHopFields[node.Kind()]; ok {
		return parent.Kind() == hop.parent && sameSpan(parent.ChildByFieldName(hop.field), node)
	}
	switch node.Kind() {
	case "method_call", "tf_call": // Verilog: both nest through subroutine_call inside a primary
		return verilogNestedInChain(node, parent)
	case "invocation": // SQL and VB: the inner call is an argument or the receiver of the outer
		return invocationNestedInChain(node, parent)
	}
	return false
}

// invocationNestedInChain reports whether a SQL or VB invocation is an inner
// link of a longer chain.
func invocationNestedInChain(node, parent *sitter.Node) bool {
	if parent.Kind() == "term" { // SQL argument
		return true
	}
	// VB receiver: invocation <- expression <- member_access(object:)
	return parent.Kind() == "expression" && parent.Parent() != nil &&
		parent.Parent().Kind() == "member_access" &&
		sameSpan(parent.Parent().ChildByFieldName("object"), parent)
}

// verilogNestedInChain reports whether a Verilog call is an inner link of a
// longer chain. Every call is wrapped in subroutine_call ->
// function_subroutine_call -> primary, so that wrapper says nothing on its
// own: an argument-nested call and a chain hop have the same shape up to the
// primary. What separates them is the primary's parent — a chain hop sits
// directly in the receiver of the next call, while an argument sits in an
// expression inside a list_of_arguments_parent.
func verilogNestedInChain(node, parent *sitter.Node) bool {
	if parent.Kind() != "subroutine_call" || parent.Parent() == nil ||
		parent.Parent().Kind() != "function_subroutine_call" {
		return false
	}
	primary := parent.Parent().Parent()
	if primary == nil || primary.Kind() != "primary" {
		return false
	}
	// Guard against a call whose own text is a prefix of a sibling call.
	if node.StartByte() < primary.StartByte() || node.EndByte() > primary.EndByte() {
		return false
	}
	recv := primary.Parent()
	return recv != nil && (recv.Kind() == "method_call" || recv.Kind() == "tf_call")
}
