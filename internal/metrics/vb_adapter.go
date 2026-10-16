package metrics

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// vbLowerer lowers the tree-sitter-vb-dotnet CST into the language-agnostic
// IR.
//
// Node kinds follow github.com/CodeAnt-AI/tree-sitter-vb-dotnet (vendored in
// third_party/, see go.mod's replace). Like the Verilog grammar it carries
// very few fields: a method header is followed by a flat run of `statement`
// children and `elseif_clause` / `else_clause` are siblings of the `if`, not
// fields of it.
//
// Lowered decision points:
//
//   - if_statement: the then branch, every elseif_clause as a nested Branch,
//     and else_clause as the alternate.
//   - select_case_statement: each case_clause is an arm, case_else_block the
//     default.
//   - for_statement / for_each_statement / while_statement / do_statement.
//   - try_statement: the catch_block becomes a Catch.
//   - ternary_expression (VB's `If(c, a, b)` operator).
//
// One deliberate gap: VB's logical operators (`AndAlso` / `OrElse`) do not
// survive into the CST as tokens. The grammar's operator rule is a
// case-insensitive keyword token, but in practice the node comes out with two
// `expression` children and no operator child at all, so a Logical run cannot
// be told apart from an ordinary binary use. The adapter therefore lowers no
// Logical at all rather than guessing from text; the resulting under-count of
// cyclomatic complexity for `AndAlso` chains is recorded in docs/metrics.md.
type vbLowerer struct{}

// lowerFunc lowers one method_declaration / constructor_declaration. The body
// is not a field: it is the run of `statement` children after the header.
func (vbLowerer) lowerFunc(n *sitter.Node, src []byte) Function {
	return Function{
		Name: vbDeclName(src, n),
		Kind: vbDeclKind(n),
		Line: uint32(n.StartPosition().Row) + 1,
		Body: vbLowerBody(n, vbLowerer{}, src),
	}
}

// visit maps one CST node to zero or more IR nodes.
func (l vbLowerer) visit(n *sitter.Node, src []byte) []Node {
	switch n.Kind() {
	case "if_statement":
		return []Node{l.conditional(n, src)}

	case "select_case_statement":
		return []Node{l.selectCase(n, src)}

	case "for_statement", "for_each_statement", "while_statement", "do_statement":
		return []Node{l.loop(n, src)}

	case "try_statement":
		return l.tryStatement(n, src)

	case "ternary_expression":
		return []Node{l.ternary(n, src)}

	default:
		return childrenIR(l, n, src)
	}
}

// conditional lowers an if / elseif / else chain. The grammar keeps
// elseif_clause and else_clause as siblings of the if, so the chain is built
// from the child list rather than from fields. Nested else-ifs arrive as a
// chain of Branches, which the engine walks at the same nesting level.
func (l vbLowerer) conditional(n *sitter.Node, src []byte) Branch {
	b := Branch{}
	var thenSeen bool
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		switch c.Kind() {
		case "expression":
			if !thenSeen && b.Test == nil {
				b.Test = l.visit(c, src)
			}
		case "statement":
			if !thenSeen {
				b.Then = append(b.Then, l.visit(c, src)...)
				thenSeen = true
			}
		case "elseif_clause":
			// An else-if is a Branch with its own condition and body; it
			// becomes the current alternate, chaining any later clauses onto
			// its own alternate.
			b.Alternate = l.attachAlternate(b.Alternate, l.elseIf(c, src))
		case "else_clause":
			var body []Node
			for j := uint(0); j < c.NamedChildCount(); j++ {
				body = append(body, l.visit(c.NamedChild(j), src)...)
			}
			b.Alternate = l.attachAlternate(b.Alternate, singleOrGroup(body))
		}
	}
	return b
}

// elseIf lowers one elseif_clause into a Branch.
func (l vbLowerer) elseIf(n *sitter.Node, src []byte) Branch {
	b := Branch{}
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		switch c.Kind() {
		case "expression":
			if b.Test == nil {
				b.Test = l.visit(c, src)
			}
		case "statement":
			b.Then = append(b.Then, l.visit(c, src)...)
		}
	}
	return b
}

// attachAlternate hangs a later clause off the end of the alternate chain, so
// `if / elseif / elseif / else` becomes one nested Branch spine.
func (l vbLowerer) attachAlternate(cur Node, next Node) Node {
	b, ok := cur.(Branch)
	if !ok {
		return next
	}
	b.Alternate = l.attachAlternate(b.Alternate, next)
	return b
}

// selectCase lowers a Select Case statement. Each case_clause is an arm and
// case_else_block is the default.
func (l vbLowerer) selectCase(n *sitter.Node, src []byte) Switch {
	s := Switch{}
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		switch c.Kind() {
		case "expression":
			s.Init = append(s.Init, l.visit(c, src)...)
		case "case_block":
			for j := uint(0); j < c.NamedChildCount(); j++ {
				if cl := c.NamedChild(j); cl.Kind() == "case_clause" {
					s.Cases = append(s.Cases, SwitchCase{Body: l.caseClauseBody(cl, src)})
				}
			}
		case "case_else_block":
			s.Cases = append(s.Cases, SwitchCase{IsDefault: true, Body: l.caseClauseBody(c, src)})
		}
	}
	return s
}

// caseClauseBody lowers the statements of one case arm, skipping the label
// expression that selects it.
func (l vbLowerer) caseClauseBody(n *sitter.Node, src []byte) []Node {
	var out []Node
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c.Kind() == "statement" {
			out = append(out, l.visit(c, src)...)
		}
	}
	return out
}

// loop lowers a For / For Each / While / Do loop. The header expression is
// Init and the trailing `statement` child is Body.
func (l vbLowerer) loop(n *sitter.Node, src []byte) Loop {
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

// tryStatement lowers Try/Catch/Finally. Each catch_block becomes a Catch and
// the finally block is a plain Group at the same nesting level, matching how
// the C-family adapters treat it.
func (l vbLowerer) tryStatement(n *sitter.Node, src []byte) []Node {
	var out []Node
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		switch c.Kind() {
		case "catch_block":
			out = append(out, Catch{Body: l.caseClauseBody(c, src)})
		default:
			out = append(out, l.visit(c, src)...)
		}
	}
	return out
}

// ternary lowers VB's `If(cond, a, b)` operator. The node has no fields: the
// three expressions are positional children.
func (l vbLowerer) ternary(n *sitter.Node, src []byte) Conditional {
	var exprs []*sitter.Node
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); c.Kind() == "expression" {
			exprs = append(exprs, c)
		}
	}
	c := Conditional{}
	if len(exprs) > 0 {
		c.Test = l.visit(exprs[0], src)
	}
	if len(exprs) > 1 {
		c.Then = l.visit(exprs[1], src)
	}
	if len(exprs) > 2 {
		c.Alternate = l.visit(exprs[2], src)
	}
	return c
}

// vbLowerBody lowers the statement run of a method or constructor, in source
// order.
func vbLowerBody(n *sitter.Node, l nodeLowerer, src []byte) []Node {
	var out []Node
	for _, body := range vbBodyNodes(n) {
		out = append(out, l.visit(body, src)...)
	}
	return out
}

// vbDeclName reads a declaration's name. VB member names are
// case-insensitive, so the name is normalised to lower case: `Me.Value` and
// `me.value` are the same member, and comparing them as written would make a
// rename look like two different symbols. A constructor has no name field and
// is reported as `New`, the spelling VB itself uses.
func vbDeclName(src []byte, n *sitter.Node) string {
	if n.Kind() == "constructor_declaration" {
		return "new"
	}
	if name := text(src, n.ChildByFieldName("name")); name != "" {
		return lowerASCII(name)
	}
	return "<anonymous>"
}

// vbDeclKind names the unit: a constructor is not a method, and a property is
// a third thing again.
func vbDeclKind(n *sitter.Node) string {
	switch n.Kind() {
	case "constructor_declaration":
		return "constructor"
	case "property_declaration":
		return "property"
	}
	return "function"
}

// lowerASCII lower-cases the ASCII letters of s. VB's case-insensitivity is
// defined over the invariant culture, which for identifiers means ASCII.
func lowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

// countBoolParamsVB counts a VB parameter list's boolean flags. VB spells the
// type `As Boolean` and its case-insensitivity means the spelling can be any
// casing, so the comparison lower-cases both sides. Leaving this to the Go/Java
// default made every VB boolean parameter invisible — the flag-argument smell
// then never fired for VB even though the metric is live for it.
func countBoolParamsVB(src []byte, list *sitter.Node) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		p := list.NamedChild(i)
		if p.Kind() != "parameter" {
			continue
		}
		if vbParamIsBool(src, p) {
			n++
		}
	}
	return n
}

// vbParamIsBool reports whether one VB parameter is typed Boolean. The
// type lives in an as_clause child whose `type` field is a `type` wrapper
// around the primitive_type — so the spelling is compared as text, and
// case-insensitively because VB's keywords and type names are too. Comparing
// only the wrapper's text keeps `As List(Of Boolean)` (a generic list) from
// counting as a boolean flag.
func vbParamIsBool(src []byte, p *sitter.Node) bool {
	for i := uint(0); i < p.NamedChildCount(); i++ {
		c := p.NamedChild(i)
		if c.Kind() != "as_clause" {
			continue
		}
		t := c.ChildByFieldName("type")
		return t != nil && equalFoldASCII(text(src, t), "Boolean")
	}
	return false
}

// equalFoldASCII compares two strings for ASCII case-insensitive equality.
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
