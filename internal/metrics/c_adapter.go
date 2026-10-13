package metrics

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// cLowerer lowers the tree-sitter-c CST (shared with tree-sitter-cpp) into the
// language-agnostic IR.
//
// Node kinds and field names follow node-types.json of tree-sitter-c v0.23.4;
// tree-sitter-cpp v0.23.4 reuses the C statement/loop/switch shapes and adds
// its own extras lowered by cppLowerer.
//
// As in the other adapters, labeled break/continue/goto and recursion are not
// lowered: they are uncounted by design (docs/metrics.md). C switch is
// fall-through: every case_statement lowers as one arm whose body carries the
// statements up to the next case.
type cLowerer struct {
	// cpp marks the C++ grammar, whose extra node kinds lower in visitCpp.
	cpp bool
}

// lowerFunc lowers one C function_definition into an IR Function. The
// function's name is the innermost declarator identifier inside the
// function_declarator (a plain name, a pointer declarator, or an array form).
func (l cLowerer) lowerFunc(n *sitter.Node, src []byte) Function {
	return Function{
		Name: cFuncName(n, src),
		Kind: "function",
		Line: uint32(n.StartPosition().Row) + 1,
		Body: lowerField(l, n, "body", src),
	}
}

// cFuncName resolves the declarator chain of a C function_definition to a
// name: declarator → (pointer/array declarators) → function_declarator →
// its inner declarator → identifier or field_identifier (methods).
func cFuncName(n *sitter.Node, src []byte) string {
	decl := n.ChildByFieldName("declarator")
	for d := decl; d != nil; {
		if d.Kind() == "function_declarator" {
			inner := d.ChildByFieldName("declarator")
			if inner == nil {
				return "<anonymous>"
			}
			return text(src, inner)
		}
		next := d.ChildByFieldName("declarator")
		if next == nil {
			return text(src, d)
		}
		d = next
	}
	return "<anonymous>"
}

// visit maps one CST node to zero or more IR nodes.
func (l cLowerer) visit(n *sitter.Node, src []byte) []Node {
	switch n.Kind() {
	case "if_statement":
		return []Node{l.branch(n, src)}

	case "for_statement", "for_range_loop", "while_statement", "do_statement":
		// Init carries the header material outside the body: condition,
		// update, initializer clause (or the range declaration/right).
		return []Node{Loop{
			Init: strayChildren(l, n, src, "body"),
			Body: lowerField(l, n, "body", src),
		}}

	case "switch_statement":
		return []Node{l.switchStmt(n, src)}

	case "conditional_expression":
		return []Node{Conditional{
			Test:      lowerField(l, n, "condition", src),
			Then:      lowerField(l, n, "consequence", src),
			Alternate: lowerField(l, n, "alternative", src),
		}}

	case "binary_expression":
		if op := text(src, n.ChildByFieldName("operator")); op == "&&" || op == "||" {
			return []Node{Logical{Operands: cLogicalOperands(l, n, src)}}
		}
		return childrenIR(l, n, src)
	}
	if l.cpp {
		return l.visitCpp(n, src)
	}
	return childrenIR(l, n, src)
}

// branch lowers an if and its else / else-if chain.
func (l cLowerer) branch(n *sitter.Node, src []byte) Branch {
	b := Branch{
		Test: lowerField(l, n, "condition", src),
		Then: lowerField(l, n, "consequence", src),
	}
	if alt := n.ChildByFieldName("alternative"); alt != nil {
		b.Alternate = singleOrGroup(l.visit(alt, src))
	}
	return b
}

// switchStmt lowers a switch. The body compound_statement's case_statement
// children are the arms: a case with a value opens one path, a bare
// case_statement (the `default` token) opens none. Statements before the
// first case belong to Init.
func (l cLowerer) switchStmt(n *sitter.Node, src []byte) Switch {
	s := Switch{Init: lowerField(l, n, "condition", src)}
	body := n.ChildByFieldName("body")
	if body == nil {
		return s
	}
	cur := -1
	flush := func() {
		cur = -1
	}
	for i := uint(0); i < body.ChildCount(); i++ {
		arm := body.Child(i)
		if arm == nil || arm.Kind() != "case_statement" {
			if cur >= 0 {
				s.Cases[cur].Body = append(s.Cases[cur].Body, l.visit(arm, src)...)
			}
			continue
		}
		s.Cases = append(s.Cases, SwitchCase{
			IsDefault: arm.ChildByFieldName("value") == nil,
			Body:      nil,
		})
		cur = len(s.Cases) - 1
	}
	flush()
	return s
}

// cLogicalOperands flattens an && / || tree into its operand IR, recursing
// through nested binary_expressions with the same or tighter precedence.
func cLogicalOperands(l cLowerer, n *sitter.Node, src []byte) []Node {
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

// visitCpp lowers the C++-only constructs. C shares everything else.
func (l cLowerer) visitCpp(n *sitter.Node, src []byte) []Node {
	switch n.Kind() {
	case "lambda_expression":
		// An inner function is scored into the enclosing unit, one level
		// deeper, like Go func_literal / Java lambda_expression.
		return []Node{Lambda{Body: childrenIR(l, n, src)}}

	case "try_statement":
		// try body is the 'body' field; catch clauses are named children.
		nodes := lowerField(l, n, "body", src)
		for i := uint(0); i < n.NamedChildCount(); i++ {
			c := n.NamedChild(i)
			if c.Kind() == "catch_clause" {
				nodes = append(nodes, Catch{Body: lowerField(l, c, "body", src)})
			}
		}
		return []Node{Group(nodes)}

	default:
		return childrenIR(l, n, src)
	}
}
