package metrics

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// jsLowerer lowers the tree-sitter-javascript CST (the base grammar of
// tree-sitter-typescript) into the language-agnostic IR.
//
// Node kinds and field names follow node-types.json of tree-sitter-javascript
// v0.23.1 and tree-sitter-typescript v0.23.2; grammar_probe_test.go dumps the
// real CST shape.
//
// As in the Go and Java adapters, labeled break/continue and recursion are not
// lowered: they are uncounted by design (docs/metrics.md). JS switch fall-
// through is transparent: each switch_case's body lowers inside that arm.
type jsLowerer struct {
	// ts marks the TypeScript grammar, where type-level syntax adds node
	// kinds that carry no runtime control flow. Kind scoring is identical;
	// only parameter counting and func-kind sets differ.
	ts bool
}

// lowerFunc lowers one JS/TS declaration into an IR Function.
func (l jsLowerer) lowerFunc(n *sitter.Node, src []byte) Function {
	kind := "function"
	switch n.Kind() {
	case "method_definition", "method_signature":
		kind = "method"
	case "class_declaration", "abstract_class_declaration":
		kind = "class"
	}
	return Function{
		Name: funcName(src, n),
		Kind: kind,
		Line: uint32(n.StartPosition().Row) + 1,
		Body: lowerField(l, n, "body", src),
	}
}

// visit maps one CST node to zero or more IR nodes.
func (l jsLowerer) visit(n *sitter.Node, src []byte) []Node {
	switch n.Kind() {
	case "arrow_function", "function_expression", "function_declaration", "generator_function", "generator_function_declaration", "method_definition":
		// An inner function is scored into the enclosing unit, one level
		// deeper, like Go func_literal / Java lambda_expression.
		return []Node{Lambda{Body: childrenIR(l, n, src)}}

	case "if_statement":
		return []Node{l.branch(n, src)}

	case "for_statement", "for_in_statement", "while_statement", "do_statement":
		// Init carries the header material outside the body: condition,
		// increment, iterator, range source.
		return []Node{Loop{
			Init: strayChildren(l, n, src, "body"),
			Body: lowerField(l, n, "body", src),
		}}

	case "switch_statement":
		return []Node{l.switchStmt(n, src)}

	case "ternary_expression":
		return []Node{Conditional{
			Test:      lowerField(l, n, "condition", src),
			Then:      lowerField(l, n, "consequence", src),
			Alternate: lowerField(l, n, "alternative", src),
		}}

	case "catch_clause":
		return []Node{Catch{Body: lowerField(l, n, "body", src)}}

	case "binary_expression":
		if op := text(src, n.ChildByFieldName("operator")); op == "&&" || op == "||" {
			return []Node{Logical{Operands: logicalOperands(l, n, src, op)}}
		}
		return childrenIR(l, n, src)

	default:
		return childrenIR(l, n, src)
	}
}

// branch lowers an if and its else / else-if chain.
func (l jsLowerer) branch(n *sitter.Node, src []byte) Branch {
	b := Branch{
		Test: lowerField(l, n, "condition", src),
		Then: lowerField(l, n, "consequence", src),
	}
	if alt := n.ChildByFieldName("alternative"); alt != nil {
		b.Alternate = singleOrGroup(l.visit(alt, src))
	}
	return b
}

// switchStmt lowers a switch. Arms are the named children of the unnamed
// switch_body: each switch_case opens one path, switch_default opens none.
func (l jsLowerer) switchStmt(n *sitter.Node, src []byte) Switch {
	s := Switch{Init: strayChildren(l, n, src, "value")}
	if value := n.ChildByFieldName("value"); value != nil {
		s.Init = append([]Node{Group(l.visit(value, src))}, s.Init...)
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		arm := n.NamedChild(i)
		switch arm.Kind() {
		case "switch_case":
			s.Cases = append(s.Cases, SwitchCase{
				Body: childrenIR(l, arm, src),
			})
		case "switch_default":
			s.Cases = append(s.Cases, SwitchCase{
				IsDefault: true,
				Body:      childrenIR(l, arm, src),
			})
		}
	}
	return s
}
