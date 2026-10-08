package metrics

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// goLowerer lowers the tree-sitter-go CST into the language-agnostic IR.
//
// Node kinds and field names follow grammar.json of tree-sitter-go v0.25.0;
// grammar_probe_test.go dumps the real CST shape and is the reference when the
// grammar moves.
//
// Two constructs the IR can express are deliberately *not* lowered: labeled
// break/continue/goto and recursion. docs/metrics.md records both as "not
// counted in v0", so lowering them would move every stored Cognitive score
// without a policy decision behind it.
type goLowerer struct{}

// lowerFunc lowers one Go declaration into an IR Function carrying its body.
// Parameters and results are skipped: a func type in a signature is a type,
// not scored code.
func (goLowerer) lowerFunc(n *sitter.Node, src []byte) Function {
	kind := "function"
	if n.ChildByFieldName("receiver") != nil {
		kind = "method"
	}
	return Function{
		Name: funcName(src, n),
		Kind: kind,
		Line: uint32(n.StartPosition().Row) + 1,
		Body: lowerField(goLowerer{}, n, "body", src),
	}
}

// visit maps one CST node to zero or more IR nodes.
func (l goLowerer) visit(n *sitter.Node, src []byte) []Node {
	switch n.Kind() {
	case "func_literal":
		// A closure is scored into the enclosing function, one level deeper.
		// Body and signature types nest alike, so all children are taken.
		return []Node{Lambda{Body: childrenIR(l, n, src)}}

	case "if_statement":
		return []Node{l.branch(n, src)}

	case "for_statement":
		// Every Go loop is a for_statement: a counted loop holds a for_clause
		// and a range loop a range_clause, both unnamed children.
		return []Node{Loop{
			Init: strayChildren(l, n, src, "body"),
			Body: lowerField(l, n, "body", src),
		}}

	case "expression_switch_statement", "type_switch_statement", "select_statement":
		return []Node{l.switchStmt(n, src)}

	case "binary_expression":
		if op := text(src, n.ChildByFieldName("operator")); op == "&&" || op == "||" {
			return []Node{Logical{Operands: logicalOperands(l, n, src, op)}}
		}
		return childrenIR(l, n, src)

	default:
		return childrenIR(l, n, src)
	}
}

// branch lowers an if, its else and any else-if chain. The chain hangs off
// the `alternative` field, which is either another if_statement or a block.
func (l goLowerer) branch(n *sitter.Node, src []byte) Branch {
	// Go allows an initializer (`if x := f(); x > 0`) as an unnamed child, so
	// the strays join the condition rather than being dropped.
	cond := lowerField(l, n, "condition", src)
	cond = append(cond, strayChildren(l, n, src, "condition", "consequence", "alternative")...)

	b := Branch{Test: cond, Then: lowerField(l, n, "consequence", src)}
	if alt := n.ChildByFieldName("alternative"); alt != nil {
		b.Alternate = singleOrGroup(l.visit(alt, src))
	}
	return b
}

// switchStmt splits a Go switch/select into its scrutinee and its arms. The
// arms are unnamed children of the switch node; default_case opens no path.
func (l goLowerer) switchStmt(n *sitter.Node, src []byte) Switch {
	var init []Node
	var cases []SwitchCase

	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		switch c.Kind() {
		case "expression_case", "type_case", "communication_case":
			cases = append(cases, SwitchCase{Body: childrenIR(l, c, src)})
		case "default_case":
			cases = append(cases, SwitchCase{IsDefault: true, Body: childrenIR(l, c, src)})
		default:
			init = append(init, l.visit(c, src)...)
		}
	}
	return Switch{Init: init, Cases: cases}
}
