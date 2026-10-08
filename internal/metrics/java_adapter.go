package metrics

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// javaLowerer lowers the tree-sitter-java CST into the language-agnostic IR.
//
// Node kinds and field names follow grammar.json of tree-sitter-java
// v0.23.5; grammar_probe_test.go dumps the real CST shape.
//
// As in the Go adapter, labeled jumps and recursion are not lowered: they are
// uncounted by design (docs/metrics.md), and `try`/`finally` need no case
// here because only a catch_clause scores and everything else is transparent.
type javaLowerer struct{}

// lowerFunc lowers one Java method or constructor into an IR Function.
func (javaLowerer) lowerFunc(n *sitter.Node, src []byte) Function {
	kind := "method"
	if n.Kind() == "constructor_declaration" {
		kind = "constructor"
	}
	return Function{
		Name: funcName(src, n),
		Kind: kind,
		Line: uint32(n.StartPosition().Row) + 1,
		Body: lowerField(javaLowerer{}, n, "body", src),
	}
}

// visit maps one CST node to zero or more IR nodes.
func (l javaLowerer) visit(n *sitter.Node, src []byte) []Node {
	switch n.Kind() {
	case "lambda_expression":
		// Scored into the enclosing method, one level deeper.
		return []Node{Lambda{Body: childrenIR(l, n, src)}}

	case "if_statement":
		return []Node{l.branch(n, src)}

	case "for_statement", "enhanced_for_statement", "while_statement", "do_statement":
		return []Node{Loop{
			Init: strayChildren(l, n, src, "body"),
			Body: lowerField(l, n, "body", src),
		}}

	case "switch_expression":
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

// branch lowers an if and its else / else-if chain, hanging off `alternative`.
func (l javaLowerer) branch(n *sitter.Node, src []byte) Branch {
	return Branch{
		Test:      lowerField(l, n, "condition", src),
		Then:      lowerField(l, n, "consequence", src),
		Alternate: javaAlternate(l, n, src),
	}
}

func javaAlternate(l javaLowerer, n *sitter.Node, src []byte) Node {
	alt := n.ChildByFieldName("alternative")
	if alt == nil {
		return nil
	}
	return singleOrGroup(l.visit(alt, src))
}

// switchStmt lowers a switch statement or expression. Colon style groups a
// run of labels with the statements that follow them; arrow style binds one
// label to one arm. Both contribute one cyclomatic path per non-default
// label, matching the `switch_label` counting this repo has always done.
func (l javaLowerer) switchStmt(n *sitter.Node, src []byte) Switch {
	s := Switch{Init: strayChildren(l, n, src, "body")}
	block := n.ChildByFieldName("body")
	if block == nil {
		return s
	}
	for i := uint(0); i < block.ChildCount(); i++ {
		group := block.Child(i)
		switch group.Kind() {
		case "switch_block_statement_group":
			s.Cases = append(s.Cases, javaGroup(l, group, src)...)
		case "switch_rule":
			s.Cases = append(s.Cases, javaRule(l, group, src))
		default:
			s.Init = append(s.Init, l.visit(group, src)...)
		}
	}
	return s
}

// javaGroup turns one colon-style group into a case per label, with the
// statements attached to the last label (fall-through labels only add a
// decision path, they hold no body of their own).
func javaGroup(l javaLowerer, group *sitter.Node, src []byte) []SwitchCase {
	var cases []SwitchCase
	for i := uint(0); i < group.ChildCount(); i++ {
		c := group.Child(i)
		if c.Kind() == "switch_label" {
			cases = append(cases, SwitchCase{IsDefault: hasToken(src, c, "default")})
			continue
		}
		if len(cases) == 0 {
			continue // grammar guarantees a label first; keep it non-fatal
		}
		last := &cases[len(cases)-1]
		last.Body = append(last.Body, l.visit(c, src)...)
	}
	return cases
}

// javaRule lowers an arrow-style `case X -> …` arm.
func javaRule(l javaLowerer, rule *sitter.Node, src []byte) SwitchCase {
	c := SwitchCase{}
	for i := uint(0); i < rule.ChildCount(); i++ {
		child := rule.Child(i)
		if child.Kind() == "switch_label" {
			c.IsDefault = hasToken(src, child, "default")
			continue
		}
		c.Body = append(c.Body, l.visit(child, src)...)
	}
	return c
}
