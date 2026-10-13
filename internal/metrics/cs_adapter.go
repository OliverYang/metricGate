package metrics

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// csLowerer lowers the tree-sitter-c-sharp CST into the language-agnostic IR.
//
// Node kinds and field names follow node-types.json of
// tree-sitter-c-sharp v0.23.5 (based on the Roslyn grammar).
//
// As in the other adapters, labeled jumps and recursion are not lowered: they
// are uncounted by design (docs/metrics.md). C# switch sections are
// fall-through like C: each switch_section lowers as one arm.
type csLowerer struct{}

// lowerFunc lowers one declaration into an IR Function.
func (csLowerer) lowerFunc(n *sitter.Node, src []byte) Function {
	kind := "function"
	switch n.Kind() {
	case "method_declaration", "local_function_statement":
		kind = "function"
	case "constructor_declaration", "destructor_declaration":
		kind = "method"
	}
	return Function{
		Name: text(src, n.ChildByFieldName("name")),
		Kind: kind,
		Line: uint32(n.StartPosition().Row) + 1,
		Body: lowerField(csLowerer{}, n, "body", src),
	}
}

// visit maps one CST node to zero or more IR nodes.
func (l csLowerer) visit(n *sitter.Node, src []byte) []Node {
	switch n.Kind() {
	case "lambda_expression", "anonymous_method_expression", "local_function_statement":
		// An inner function is scored into the enclosing unit, one level
		// deeper, like Go func_literal / Java lambda_expression.
		return []Node{Lambda{Body: childrenIR(l, n, src)}}

	case "if_statement":
		return []Node{l.branch(n, src)}

	case "for_statement", "foreach_statement", "while_statement", "do_statement", "using_statement", "lock_statement", "fixed_statement":
		// Header forms count as loops: their bodies repeat (using/lock
		// repeat zero or one time but wrap a scope, like Java try-with-
		// resources, so Loop keeps the nesting honest).
		return []Node{Loop{
			Init: strayChildren(l, n, src, "body"),
			Body: lowerField(l, n, "body", src),
		}}

	case "switch_statement":
		return []Node{l.switchStmt(n, src)}

	case "switch_expression":
		// `x switch { pat => expr, ... }`: the scrutinee is Init, each
		// switch_expression_arm is a case. A discard-pattern arm is default.
		return []Node{l.switchExpr(n, src)}

	case "conditional_expression":
		return []Node{Conditional{
			Test:      lowerField(l, n, "condition", src),
			Then:      lowerField(l, n, "consequence", src),
			Alternate: lowerField(l, n, "alternative", src),
		}}

	case "try_statement":
		// try body is the 'body' field; catch/finally clauses are children.
		return []Node{Group(csTryBody(l, n, src))}

	case "binary_expression":
		if op := text(src, n.ChildByFieldName("operator")); op == "&&" || op == "||" {
			return []Node{Logical{Operands: csLogicalOperands(l, n, src)}}
		}
		return childrenIR(l, n, src)

	default:
		return childrenIR(l, n, src)
	}
}

// branch lowers an if and its else / else-if chain.
func (l csLowerer) branch(n *sitter.Node, src []byte) Branch {
	b := Branch{
		Test: lowerField(l, n, "condition", src),
		Then: lowerField(l, n, "consequence", src),
	}
	if alt := n.ChildByFieldName("alternative"); alt != nil {
		b.Alternate = singleOrGroup(l.visit(alt, src))
	}
	return b
}

// switchStmt lowers a classic switch. The switch_body's switch_section
// children are the arms; a section whose leading children are a `case` /
// `default` token decides the arm kind. Statements of a section lower inside
// that arm (C# sections are fall-through like C).
func (l csLowerer) switchStmt(n *sitter.Node, src []byte) Switch {
	s := Switch{Init: lowerField(l, n, "value", src)}
	body := n.ChildByFieldName("body")
	if body == nil {
		return s
	}
	cur := -1
	for i := uint(0); i < body.ChildCount(); i++ {
		sec := body.Child(i)
		if sec == nil || sec.Kind() != "switch_section" {
			continue
		}
		isDefault := false
		for j := uint(0); j < sec.ChildCount(); j++ {
			k := sec.Child(j).Kind()
			if k == "case" {
				break
			}
			if k == "default" {
				isDefault = true
				break
			}
		}
		s.Cases = append(s.Cases, SwitchCase{
			IsDefault: isDefault,
			Body:      childrenIR(l, sec, src),
		})
		cur = len(s.Cases) - 1
	}
	_ = cur
	return s
}

// csArmIsDefault reports whether a switch_expression_arm is the discard
// arm: its pattern child is the unnamed `_` token (node kind "discard").
func csArmIsDefault(arm *sitter.Node) bool {
	for i := uint(0); i < arm.ChildCount(); i++ {
		k := arm.Child(i).Kind()
		if k == "discard" {
			return true
		}
		if k == "constant_pattern" || k == "var_pattern" || k == "declaration_pattern" || k == "recursive_pattern" || k == "relational_pattern" || k == "or_pattern" {
			return false
		}
	}
	return false
}

// csLogicalOperands flattens an && / || tree into its operand IR.
func csLogicalOperands(l csLowerer, n *sitter.Node, src []byte) []Node {
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

// firstExprChildren lowers the leading expression children of a node (before
// the switch keyword), used as the scrutinee of a switch_expression.
func (l csLowerer) firstExprChildren(n *sitter.Node, src []byte) []Node {
	var out []Node
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c.Kind() == "switch" {
			break
		}
		out = append(out, l.visit(c, src)...)
	}
	if len(out) == 0 {
		return nil
	}
	return []Node{Group(out)}
}

// switchExpr lowers a switch_expression: the scrutinee is Init, each
// switch_expression_arm is a case. A discard-pattern arm is default.
func (l csLowerer) switchExpr(n *sitter.Node, src []byte) Switch {
	s := Switch{Init: l.firstExprChildren(n, src)}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		a := n.NamedChild(i)
		if a.Kind() != "switch_expression_arm" {
			continue
		}
		s.Cases = append(s.Cases, SwitchCase{
			IsDefault: csArmIsDefault(a),
			Body:      childrenIR(l, a, src),
		})
	}
	return s
}

// csTryBody lowers a try_statement: the 'body' field, then each catch as a
// Catch node and each finally clause's block inline (finally always runs).
func csTryBody(l csLowerer, n *sitter.Node, src []byte) []Node {
	nodes := lowerField(l, n, "body", src)
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch c.Kind() {
		case "catch_clause":
			nodes = append(nodes, Catch{Body: lowerField(l, c, "body", src)})
		case "finally_clause":
			nodes = append(nodes, childrenIR(l, c, src)...)
		}
	}
	return nodes
}
