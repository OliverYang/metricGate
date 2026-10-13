package metrics

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// pyLowerer lowers the tree-sitter-python CST into the language-agnostic IR.
//
// Node kinds and field names follow node-types.json of tree-sitter-python
// v0.25.0.
//
// As in the Go, Java and JS adapters, labeled break/continue and recursion
// are not lowered: they are uncounted by design (docs/metrics.md).
type pyLowerer struct{}

// lowerFunc lowers one Python definition into an IR Function.
func (pyLowerer) lowerFunc(n *sitter.Node, src []byte) Function {
	kind := "function"
	if n.Kind() == "class_definition" {
		kind = "class"
	}
	return Function{
		Name: funcName(src, n),
		Kind: kind,
		Line: uint32(n.StartPosition().Row) + 1,
		Body: lowerField(pyLowerer{}, n, "body", src),
	}
}

// visit maps one CST node to zero or more IR nodes.
func (pyLowerer) visit(n *sitter.Node, src []byte) []Node {
	switch n.Kind() {
	case "lambda":
		// An inner lambda is scored into the enclosing unit, one level
		// deeper, like Go func_literal / JS arrow_function.
		return []Node{Lambda{Body: childrenIR(pyLowerer{}, n, src)}}

	case "if_statement", "elif_clause":
		// An elif is a nested branch living in the parent if's
		// alternative; lowering it as a Branch keeps else-if chains
		// counted like every other language. Both kinds use the same
		// field names (condition/consequence/alternative).
		return []Node{pyBranch(n, src)}

	case "for_statement", "while_statement":
		// Init carries the header material outside the body: the loop
		// variables and iterable, or the trailing else clause of
		// while/for ... else.
		return []Node{pyLoop(n, src, "body", "alternative")}

	case "except_clause":
		// Python's except is the analogue of Java/JS catch: one
		// alternate path per clause. The grammar has no body field —
		// the trailing block is just the clause's block child, so the
		// filter (except ValueError as e) lowers alongside it and the
		// last block child is the handler body.
		return []Node{pyExcept(n, src)}

	case "with_statement":
		// A with block scopes its context expression; the body is the
		// scoped unit. Init carries the with_clause (context managers)
		// outside the body.
		return []Node{pyLoop(n, src, "body")}

	case "match_statement":
		return []Node{pyMatch(n, src)}

	case "conditional_expression":
		return []Node{pyConditional(n, src)}

	case "boolean_operator":
		return []Node{pyLogical(n, src)}

	default:
		return childrenIR(pyLowerer{}, n, src)
	}
}

// pyExcept lowers an except_clause: the handler body is the clause's block
// child; the filter expression (except ValueError as e) lowers inside it.
func pyExcept(n *sitter.Node, src []byte) Node {
	for i := n.NamedChildCount(); i > 0; i-- {
		c := n.NamedChild(i - 1)
		if c.Kind() == "block" {
			return Catch{Body: childrenIR(pyLowerer{}, c, src)}
		}
	}
	return Group(childrenIR(pyLowerer{}, n, src))
}

// pyLoop builds the loop/scoped-unit node: Init carries the header material
// outside the body.
func pyLoop(n *sitter.Node, src []byte, body string, except ...string) Node {
	return Loop{
		Init: strayChildren(pyLowerer{}, n, src, append(except, body)...),
		Body: lowerField(pyLowerer{}, n, body, src),
	}
}

// pyBranch lowers an if_statement or elif_clause and its else/elif chain.
// Both kinds use the same field names (condition/consequence/alternative).
func pyBranch(n *sitter.Node, src []byte) Branch {
	b := Branch{
		Test: lowerField(pyLowerer{}, n, "condition", src),
		Then: lowerField(pyLowerer{}, n, "consequence", src),
	}
	if alt := n.ChildByFieldName("alternative"); alt != nil {
		b.Alternate = singleOrGroup(pyLowerer{}.visit(alt, src))
	}
	return b
}

// pyMatch lowers a match statement like a switch: each case_clause opens
// one path. Case guards (if ... after the pattern) lower inside the arm body.
func pyMatch(n *sitter.Node, src []byte) Switch {
	s := Switch{Init: pyMatchInit(n, src)}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		arm := n.NamedChild(i)
		if arm.Kind() != "case_clause" {
			continue
		}
		s.Cases = append(s.Cases, SwitchCase{
			IsDefault: pyCaseIsDefault(arm),
			Body:      childrenIR(pyLowerer{}, arm, src),
		})
	}
	return s
}

// pyMatchInit carries the match subject into the switch init, in front of
// any material that is not a case clause.
func pyMatchInit(n *sitter.Node, src []byte) []Node {
	init := strayChildren(pyLowerer{}, n, src, "body")
	if subj := n.ChildByFieldName("subject"); subj != nil {
		init = append([]Node{Group(pyLowerer{}.visit(subj, src))}, init...)
	}
	return init
}

// pyCaseIsDefault reports whether a case_clause is the catch-all arm: the
// wildcard pattern `case _`. A case with a guard (if ... after the pattern)
// is still an open path, not a default.
func pyCaseIsDefault(arm *sitter.Node) bool {
	pat := arm.ChildByFieldName("pattern")
	return pat != nil && pat.Kind() == "wildcard_pattern"
}

// pyConditional lowers the ternary expression. The node has no fields:
// children are [then, condition, else] in source order
// (a <then> if <condition> else <else>).
func pyConditional(n *sitter.Node, src []byte) Node {
	kids := childrenIR(pyLowerer{}, n, src)
	if len(kids) != 3 {
		return Group(kids)
	}
	return Conditional{
		Then:      kids[:1],
		Test:      kids[1:2],
		Alternate: kids[2:],
	}
}

// pyLogical folds an and/or chain into a Logical node like JS && / ||.
func pyLogical(n *sitter.Node, src []byte) Node {
	if op := text(src, n.ChildByFieldName("operator")); op == "and" || op == "or" {
		return Logical{Operands: pyLogicalOperands(n, src, op)}
	}
	return Group(childrenIR(pyLowerer{}, n, src))
}

// pyLogicalOperands flattens a run of the same boolean operator
// (`a and b and c` → three operands), stopping at a different operator or a
// non-boolean_operator node.
func pyLogicalOperands(n *sitter.Node, src []byte, op string) []Node {
	if n.Kind() != "boolean_operator" || text(src, n.ChildByFieldName("operator")) != op {
		return []Node{Group(pyLowerer{}.visit(n, src))}
	}
	var out []Node
	for _, field := range []string{"left", "right"} {
		if c := n.ChildByFieldName(field); c != nil {
			out = append(out, pyLogicalOperands(c, src, op)...)
		}
	}
	return out
}
