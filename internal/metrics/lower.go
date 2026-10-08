package metrics

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// nodeLowerer maps one grammar's CST nodes onto IR nodes. Each supported
// language implements it; everything mechanical about walking children lives
// here instead, so an adapter only states how its own constructs lower.
type nodeLowerer interface {
	visit(n *sitter.Node, src []byte) []Node
	lowerFunc(n *sitter.Node, src []byte) Function
}

// childrenIR lowers every child of n in source order.
func childrenIR(l nodeLowerer, n *sitter.Node, src []byte) []Node {
	var out []Node
	for i := uint(0); i < n.ChildCount(); i++ {
		out = append(out, l.visit(n.Child(i), src)...)
	}
	return out
}

// lowerField lowers the child held by one named field.
func lowerField(l nodeLowerer, n *sitter.Node, field string, src []byte) []Node {
	c := n.ChildByFieldName(field)
	if c == nil {
		return nil
	}
	return l.visit(c, src)
}

// strayChildren lowers every child *except* those reachable through the given
// field names. Adapters need it because a statement usually carries scoring
// material outside the fields they name — a Go if-initializer, a Java
// `else` keyword — and lowering only the named fields would drop it.
func strayChildren(l nodeLowerer, n *sitter.Node, src []byte, except ...string) []Node {
	skip := make(map[string]bool, len(except))
	for _, f := range except {
		skip[f] = true
	}
	var out []Node
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil || skip[n.FieldNameForChild(uint32(i))] {
			continue
		}
		out = append(out, l.visit(c, src)...)
	}
	return out
}

// singleOrGroup collapses a lowered child list into the one Node a Branch
// alternate needs: an else-if arrives as a lone Branch, an else body as a
// Group that the engine walks transparently at the current nesting level.
func singleOrGroup(nodes []Node) Node {
	if len(nodes) == 1 {
		return nodes[0]
	}
	return Group(nodes)
}

// logicalOperands flattens a run of the same operator (`a && b && c` → three
// operands). It stops at anything that is not a member of the run, including
// a run of the *other* operator, which lowers to its own Logical.
//
// A non-member always yields exactly one operand, even when it lowers to no
// IR of its own: the engine derives the cyclomatic count from the operand
// count (n operands join n−1 paths), and an ordinary comparison like `a > 0`
// is a real operand despite carrying nothing scorable. Wrapping in a Group
// keeps whatever the operand did contain.
func logicalOperands(l nodeLowerer, n *sitter.Node, src []byte, op string) []Node {
	if n.Kind() != "binary_expression" || text(src, n.ChildByFieldName("operator")) != op {
		return []Node{Group(l.visit(n, src))}
	}
	var out []Node
	for _, field := range []string{"left", "right"} {
		if c := n.ChildByFieldName(field); c != nil {
			out = append(out, logicalOperands(l, c, src, op)...)
		}
	}
	return out
}
