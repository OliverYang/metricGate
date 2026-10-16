package metrics

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// verilogLowerer lowers the tree-sitter-verilog CST into the language-agnostic
// IR. The grammar covers SystemVerilog as well as Verilog, so classes and
// their methods are lowered too.
//
// Node kinds follow github.com/tree-sitter/tree-sitter-verilog v1.0.3. The
// striking property of this grammar is that **none of the declaration or
// statement nodes carry fields**: `conditional_statement` holds its condition
// in a `cond_predicate` child and its branches in `statement_or_null`
// children, with `if`/`else` as anonymous tokens. Every decision below is
// therefore made on child kinds and anonymous token text, and the helpers
// below say so explicitly.
//
// Lowered decision points:
//
//   - conditional_statement: if / else-if chain, else as the alternate.
//   - case_statement: each case_item is an arm; the arm whose first child is
//     the anonymous `default` token is the default.
//   - loop_statement: for / while / repeat. The header (for_initialization,
//     condition, for_step) is Init, the trailing statement_or_null is Body.
//   - conditional_expression: the `cond ? a : b` operator.
//   - expression: `&&` / `||` runs become Logical, as in the C family.
//
// `seq_block` (begin/end) is a plain Group: it carries no decision of its own.
// Labeled jumps and recursion are uncounted by design, as everywhere else.
type verilogLowerer struct{}

// lowerFunc lowers one function_declaration / task_declaration /
// class_constructor_declaration. None of them has a body field: the name sits
// in the *_identifier child of the *_body_declaration, and the statements are
// that node's function_statement_or_null / statement_or_null children.
func (verilogLowerer) lowerFunc(n *sitter.Node, src []byte) Function {
	return Function{
		Name: verilogDeclName(src, n),
		Kind: verilogDeclKind(n),
		Line: uint32(n.StartPosition().Row) + 1,
		Body: verilogLowerBody(n, verilogLowerer{}, src),
	}
}

// visit maps one CST node to zero or more IR nodes.
func (l verilogLowerer) visit(n *sitter.Node, src []byte) []Node {
	switch n.Kind() {
	case "conditional_statement":
		return []Node{l.conditional(n, src)}

	case "case_statement":
		return []Node{l.caseStatement(n, src)}

	case "loop_statement":
		return []Node{l.loop(n, src)}

	case "conditional_expression":
		return []Node{l.ternary(n, src)}

	case "expression":
		if op := verilogBinaryOp(src, n); op == "&&" || op == "||" {
			return []Node{Logical{Operands: verilogLogicalOperands(l, n, src, op)}}
		}
		return childrenIR(l, n, src)

	default:
		return childrenIR(l, n, src)
	}
}

// conditional lowers an if / else-if chain. The condition lives in the
// cond_predicate child, the then branch in the first statement_or_null, and
// the else branch (a plain statement_or_null, or a nested
// conditional_statement for `else if`) in the second.
func (l verilogLowerer) conditional(n *sitter.Node, src []byte) Branch {
	var branches []*sitter.Node
	var test []Node
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		switch c.Kind() {
		case "cond_predicate":
			test = l.visit(c, src)
		case "statement_or_null":
			branches = append(branches, c)
		}
	}
	b := Branch{Test: test}
	if len(branches) > 0 {
		b.Then = l.visit(branches[0], src)
	}
	if len(branches) > 1 {
		b.Alternate = singleOrGroup(l.visit(branches[1], src))
	}
	return b
}

// caseStatement lowers a case / casex / casez statement. Each case_item is
// one arm; the item whose first child is the anonymous `default` token is the
// default arm, and every other named child of an item is its body.
func (l verilogLowerer) caseStatement(n *sitter.Node, src []byte) Switch {
	s := Switch{}
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c.Kind() != "case_item" {
			continue
		}
		arm := SwitchCase{IsDefault: hasToken(src, c, "default")}
		for j := uint(0); j < c.NamedChildCount(); j++ {
			child := c.NamedChild(j)
			if child.Kind() == "case_item_expression" {
				continue // the label, not the body
			}
			arm.Body = append(arm.Body, l.visit(child, src)...)
		}
		s.Cases = append(s.Cases, arm)
	}
	return s
}

// loop lowers a for / while / repeat statement. The grammar puts the keyword,
// the parenthesised header and the body in one node with no fields, so the
// header children become Init and the trailing statement_or_null becomes Body.
func (l verilogLowerer) loop(n *sitter.Node, src []byte) Loop {
	loop := Loop{}
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c.Kind() == "statement_or_null" {
			loop.Body = append(loop.Body, l.visit(c, src)...)
			continue
		}
		loop.Init = append(loop.Init, l.visit(c, src)...)
	}
	return loop
}

// ternary lowers `cond ? a : b`. The node has no fields: the condition is the
// cond_predicate child and the two arms are the expression children after the
// `?`.
func (l verilogLowerer) ternary(n *sitter.Node, src []byte) Conditional {
	c := Conditional{}
	var arms []*sitter.Node
	for i := uint(0); i < n.ChildCount(); i++ {
		child := n.Child(i)
		switch child.Kind() {
		case "cond_predicate":
			c.Test = l.visit(child, src)
		case "expression":
			arms = append(arms, child)
		}
	}
	if len(arms) > 0 {
		c.Then = l.visit(arms[0], src)
	}
	if len(arms) > 1 {
		c.Alternate = l.visit(arms[1], src)
	}
	return c
}

// verilogLogicalOperands flattens an `&&` / `||` run. The grammar spells both
// operators as an anonymous token between two expression children, so there
// is no operator field to compare: the run is recognised by looking at the
// token between the operands.
func verilogLogicalOperands(l verilogLowerer, n *sitter.Node, src []byte, op string) []Node {
	if n.Kind() != "expression" || verilogBinaryOp(src, n) != op {
		return []Node{Group(l.visit(n, src))}
	}
	var out []Node
	for i := uint(0); i < n.NamedChildCount(); i++ {
		out = append(out, verilogLogicalOperands(l, n.NamedChild(i), src, op)...)
	}
	return out
}

// verilogBinaryOp returns the anonymous operator token of a binary
// expression, or "" when the node is not one. `expression` is both the binary
// and the grouping node in this grammar, so a two-operand shape with an
// operator token in the middle is what identifies a binary use.
func verilogBinaryOp(src []byte, n *sitter.Node) string {
	if n.Kind() != "expression" || n.NamedChildCount() != 2 {
		return ""
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if !c.IsNamed() {
			if t := text(src, c); t == "&&" || t == "||" {
				return t
			}
		}
	}
	return ""
}

// verilogLowerBody lowers the statement run of a declaration, in source order.
func verilogLowerBody(n *sitter.Node, l nodeLowerer, src []byte) []Node {
	var out []Node
	for _, body := range verilogBodyNodes(n) {
		out = append(out, l.visit(body, src)...)
	}
	return out
}

// verilogDeclName reads a declaration's name. The grammar wraps every
// identifier in a `*_identifier -> *_identifier -> simple_identifier` chain
// (function_identifier, task_identifier, class_identifier, ...), so the first
// simple_identifier in source order is the name.
func verilogDeclName(src []byte, n *sitter.Node) string {
	if n.Kind() == "class_constructor_declaration" {
		return "new"
	}
	if name := firstDescendantText(src, n, "simple_identifier"); name != "" {
		return name
	}
	return "<anonymous>"
}

// verilogDeclKind names the unit: a task is not a function, and a class
// constructor is a third thing again.
func verilogDeclKind(n *sitter.Node) string {
	switch n.Kind() {
	case "task_declaration":
		return "task"
	case "class_constructor_declaration":
		return "constructor"
	}
	return "function"
}

// verilogParamsNode returns the tf_port_list of a declaration, or nil.
func verilogParamsNode(n *sitter.Node) *sitter.Node {
	decl := firstChildKind(n, "function_body_declaration", "task_body_declaration")
	if decl == nil {
		decl = n
	}
	return firstChildKind(decl, "tf_port_list")
}

// countParamsVerilog counts one parameter per tf_port_item child. An empty
// `()` yields zero; the parentheses themselves are anonymous.
func countParamsVerilog(list *sitter.Node) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		switch list.NamedChild(i).Kind() {
		case "tf_port_item1", "tf_port_item", "tf_port_declaration":
			n++
		}
	}
	return n
}

// countBoolParamsVerilog reports 0: SystemVerilog has no boolean parameter
// type, so the "flag argument" smell the metric models does not exist. The
// four-state bit type a caller chooses between is not the same construct.
func countBoolParamsVerilog(*sitter.Node) int { return 0 }

// firstDescendantText returns the text of the first node of the given kind in
// source order, or "".
func firstDescendantText(src []byte, n *sitter.Node, kind string) string {
	if n == nil {
		return ""
	}
	if n.Kind() == kind {
		return text(src, n)
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		if t := firstDescendantText(src, n.Child(i), kind); t != "" {
			return t
		}
	}
	return ""
}
