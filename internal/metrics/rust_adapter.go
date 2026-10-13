package metrics

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// rustLowerer lowers the tree-sitter-rust CST into the language-agnostic IR.
//
// Node kinds and field names follow node-types.json of
// tree-sitter-rust v0.23.2.
//
// As in the other adapters, labeled break/continue and recursion are not
// lowered: they are uncounted by design (docs/metrics.md). Rust's expression
// orientation means if/match are expressions: they still lower to the same
// Branch/Switch IR as the statement languages.
type rustLowerer struct{}

// lowerFunc lowers one function_item into an IR Function.
func (rustLowerer) lowerFunc(n *sitter.Node, src []byte) Function {
	return Function{
		Name: text(src, n.ChildByFieldName("name")),
		Kind: "function",
		Line: uint32(n.StartPosition().Row) + 1,
		Body: lowerField(rustLowerer{}, n, "body", src),
	}
}

// visit maps one CST node to zero or more IR nodes.
func (l rustLowerer) visit(n *sitter.Node, src []byte) []Node {
	switch n.Kind() {
	case "closure_expression":
		// An inner function is scored into the enclosing unit, one level
		// deeper, like Go func_literal / Java lambda_expression.
		return []Node{Lambda{Body: childrenIR(l, n, src)}}

	case "if_expression":
		return []Node{l.branch(n, src)}

	case "for_expression", "while_expression", "loop_expression":
		// Init carries the header material outside the body: the iterator
		// and pattern of a for, the condition of a while (which may be a
		// let_condition for while-let).
		return []Node{Loop{
			Init: strayChildren(l, n, src, "body"),
			Body: lowerField(l, n, "body", src),
		}}

	case "match_expression":
		return []Node{l.matchExpr(n, src)}

	case "binary_expression":
		if op := text(src, n.ChildByFieldName("operator")); op == "&&" || op == "||" {
			return []Node{Logical{Operands: rustLogicalOperands(l, n, src)}}
		}
		return childrenIR(l, n, src)

	default:
		return childrenIR(l, n, src)
	}
}

// branch lowers an if and its else / else-if chain. Rust conditions may be
// let_condition nodes (if-let); they lower as the Test.
func (l rustLowerer) branch(n *sitter.Node, src []byte) Branch {
	b := Branch{
		Test: lowerField(l, n, "condition", src),
		Then: lowerField(l, n, "consequence", src),
	}
	if alt := n.ChildByFieldName("alternative"); alt != nil {
		// The alternative field value is the `else` keyword followed by the
		// block or nested if_expression; visit skips the anonymous `else`.
		b.Alternate = singleOrGroup(l.visit(alt, src))
	}
	return b
}

// matchExpr lowers a match. Arms are the match_arm children of the
// match_block: a `_` wildcard pattern opens no path, a guard
// (match_pattern's condition field) lowers into the arm body like an
// implicit branch test.
func (l rustLowerer) matchExpr(n *sitter.Node, src []byte) Switch {
	s := Switch{Init: append(
		[]Node{Group(lowerField(l, n, "value", src))},
		strayChildren(l, n, src, "value", "body")...,
	)}
	body := n.ChildByFieldName("body")
	if body == nil {
		return s
	}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		arm := body.NamedChild(i)
		if arm.Kind() != "match_arm" {
			continue
		}
		pat := arm.ChildByFieldName("pattern")
		isDefault := false
		if pat != nil && pat.NamedChildCount() > 0 && pat.NamedChild(0).Kind() == "_" {
			isDefault = true
		}
		armBody := childrenIR(l, arm, src)
		if cond := arm.ChildByFieldName("pattern"); cond != nil {
			// The guard is the condition field of the match_pattern grandchild.
			if mp := cond.Child(0); mp != nil && mp.Kind() == "match_pattern" {
				if g := mp.ChildByFieldName("condition"); g != nil {
					armBody = []Node{Branch{Test: []Node{singleOrGroup(l.visit(g, src))}, Then: armBody}}
				}
			}
		}
		s.Cases = append(s.Cases, SwitchCase{IsDefault: isDefault, Body: armBody})
	}
	return s
}

// rustLogicalOperands flattens an && / || tree into its operand IR.
func rustLogicalOperands(l rustLowerer, n *sitter.Node, src []byte) []Node {
	var out []Node
	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		if node.Kind() == "binary_expression" {
			op := text(src, node.ChildByFieldName("operator"))
			if op == "&&" || op == "||" {
				walk(node.ChildByFieldName("left"))
				walk(node.ChildByFieldName("right"))
				return
			}
		}
		out = append(out, singleOrGroup(l.visit(node, src)))
	}
	walk(n)
	return out
}
