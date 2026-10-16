package metrics

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// sqlLowerer lowers the tree-sitter-sql CST into the language-agnostic IR.
//
// Node kinds and field names follow node-types.json of
// github.com/DerekStride/tree-sitter-sql (gh-pages pseudo-version, the only
// revision that ships the generated src/parser.c).
//
// The grammar is SQL/DDL with a thin procedural layer, so the lowered surface
// is small and the gaps are documented rather than papered over (see
// docs/metrics.md, "Which languages produce which metrics"):
//
//   - The only loop is while_statement; `IF … THEN` is not in the grammar at
//     all, so an if/else chain contributes no decision point.
//   - `case` is the CASE expression: its arms are separated by the anonymous
//     keyword_when / keyword_else tokens, not by case_item nodes.
//
// As in the other adapters, labeled jumps and recursion are not lowered: they
// are uncounted by design.
type sqlLowerer struct{}

// lowerFunc lowers one create_function / create_procedure into an IR Function.
// Neither node carries a body field: the name lives in the child
// object_reference and the body in the child function_body / procedure_body.
func (sqlLowerer) lowerFunc(n *sitter.Node, src []byte) Function {
	return Function{
		Name: sqlDeclName(src, n),
		Kind: sqlDeclKind(n),
		Line: uint32(n.StartPosition().Row) + 1,
		Body: lowerChildren(sqlBody(n), sqlLowerer{}, src),
	}
}

// visit maps one CST node to zero or more IR nodes.
func (l sqlLowerer) visit(n *sitter.Node, src []byte) []Node {
	switch n.Kind() {
	case "while_statement":
		return []Node{l.whileLoop(n, src)}

	case "case":
		return []Node{l.caseExpr(n, src)}

	case "binary_expression":
		if op := text(src, n.ChildByFieldName("operator")); op == "and" || op == "or" {
			return []Node{Logical{Operands: sqlLogicalOperands(l, n, src, op)}}
		}
		return childrenIR(l, n, src)

	default:
		return childrenIR(l, n, src)
	}
}

// whileLoop lowers `WHILE cond (statement | BEGIN statement* END)`. The
// condition is everything before the body block.
func (l sqlLowerer) whileLoop(n *sitter.Node, src []byte) Loop {
	loop := Loop{}
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c.Kind() == "statement" {
			loop.Body = append(loop.Body, l.visit(c, src)...)
			continue
		}
		loop.Init = append(loop.Init, l.visit(c, src)...)
	}
	return loop
}

// caseExpr lowers a CASE expression. Arms run from one keyword_when to the
// next; keyword_else opens the default arm. The optional selector before the
// first keyword_when is Init, as is the CASE keyword itself.
func (l sqlLowerer) caseExpr(n *sitter.Node, src []byte) Switch {
	s := Switch{}
	inArm := false
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		switch {
		case c.Kind() == "keyword_when":
			s.Cases = append(s.Cases, SwitchCase{})
			inArm = true
		case c.Kind() == "keyword_else":
			s.Cases = append(s.Cases, SwitchCase{IsDefault: true})
			inArm = true
		case c.Kind() == "keyword_end" || c.Kind() == "keyword_case":
			// Structural tokens: nothing to score.
		case inArm:
			arm := &s.Cases[len(s.Cases)-1]
			arm.Body = append(arm.Body, l.visit(c, src)...)
		default:
			s.Init = append(s.Init, l.visit(c, src)...)
		}
	}
	return s
}

// sqlLogicalOperands flattens an AND / OR run into its operand IR. The
// grammar spells the boolean operators as binary_expression with an `and` /
// `or` operator field, so the flattening mirrors logicalOperands but without
// the precedence widening the C-family adapters need.
func sqlLogicalOperands(l sqlLowerer, n *sitter.Node, src []byte, op string) []Node {
	if n.Kind() != "binary_expression" || text(src, n.ChildByFieldName("operator")) != op {
		return []Node{Group(l.visit(n, src))}
	}
	var out []Node
	for _, field := range []string{"left", "right"} {
		if c := n.ChildByFieldName(field); c != nil {
			out = append(out, sqlLogicalOperands(l, c, src, op)...)
		}
	}
	return out
}

// lowerChildren lowers the children of a body container (function_body,
// procedure_body). Those nodes carry no fields at all, so the body cannot be
// reached through ChildByFieldName.
func lowerChildren(n *sitter.Node, l nodeLowerer, src []byte) []Node {
	if n == nil {
		return nil
	}
	return childrenIR(l, n, src)
}

// sqlDeclName reads the declared name of a create_function / create_procedure.
// The name is not a field of the declaration: it sits inside the child
// object_reference, whose own `name` field holds the identifier.
func sqlDeclName(src []byte, n *sitter.Node) string {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.Kind() != "object_reference" {
			continue
		}
		if name := text(src, c.ChildByFieldName("name")); name != "" {
			return name
		}
	}
	return "<anonymous>"
}

// sqlDeclKind names the unit: a procedure is not a function.
func sqlDeclKind(n *sitter.Node) string {
	if n.Kind() == "create_procedure" {
		return "procedure"
	}
	return "function"
}

// sqlParamsNode returns the function_arguments child of a create_function /
// create_procedure. The list is not a field of the declaration.
func sqlParamsNode(n *sitter.Node) *sitter.Node {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); c.Kind() == "function_arguments" {
			return c
		}
	}
	return nil
}

// countParamsSQL counts one parameter per function_argument child. The
// function_arguments node has no other named children, so the count is exact
// even though the parentheses around the list are anonymous.
func countParamsSQL(list *sitter.Node) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		if list.NamedChild(i).Kind() == "function_argument" {
			n++
		}
	}
	return n
}

// sqlBody returns the procedural body of a create_function /
// create_procedure, or nil for a language-SQL function with only an
// expression body.
func sqlBody(n *sitter.Node) *sitter.Node {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		switch n.NamedChild(i).Kind() {
		case "function_body", "procedure_body":
			return n.NamedChild(i)
		}
	}
	return nil
}
