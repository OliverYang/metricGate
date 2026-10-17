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
	// A module-level `const f = () => {…}` carries its name on the declarator,
	// not on the function node, so funcName cannot see it.
	name := funcName(src, n)
	if name == "<anonymous>" {
		if decl := jsDeclName(src, n); decl != "" {
			name = decl
		}
	}
	return Function{
		Name: name,
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

// — Function units that a module-level declaration names —
//
// The grammar writes a module's function in two shapes that mean the same
// thing: `function handler(x) {…}` and `export const handler = (x) => {…}`.
// Only the first is a `function_declaration`, so a kind-set alone cannot see
// the second, and for years it was not measured at all: a file of arrow
// functions reported zero functions, zero complexity, and passed every
// function-level threshold by being invisible.
//
// The fix cannot be "measure every arrow function", because
// `arr.map((y) => y * 2)` is a Go func_literal in disguise and belongs to its
// enclosing unit (that nesting rule is engine.go's, and it is right). So the
// test is structural: a function expression counts as its own unit exactly
// when a declaration at the top of the module binds it to a name.
var jsModuleFuncKinds = map[string]bool{
	"arrow_function":      true,
	"function_expression": true,
	"generator_function":  true,
}

// isJSModuleFunc reports whether n is a function expression named by a
// module-level `const`/`let`/`var`, in either the plain or the exported
// spelling (an export_statement sits between the declaration and the program).
//
// `export default () => …` is deliberately NOT a unit: it has no name to
// report under, and inventing one would put a fake identifier in every
// snapshot of a file that has none.
func isJSModuleFunc(n *sitter.Node) bool {
	if n == nil || !jsModuleFuncKinds[n.Kind()] {
		return false
	}
	decl := n.Parent()
	if decl == nil || decl.Kind() != "variable_declarator" {
		return false
	}
	// Identity is compared by span, not by pointer: the binding returns a fresh
	// node wrapper for the same underlying each time it is asked.
	value := decl.ChildByFieldName("value")
	if value == nil || value.StartByte() != n.StartByte() || value.EndByte() != n.EndByte() {
		return false
	}
	list := decl.Parent()
	if list == nil || (list.Kind() != "lexical_declaration" && list.Kind() != "variable_declaration") {
		return false
	}
	// A module's declaration is either bare or exported; export_statement is the
	// only wrapper that keeps the binding at module level. Anything else (a
	// statement_block, a switch arm) is a local, and a local belongs to the
	// function that declares it.
	owner := list.Parent()
	if owner == nil {
		return false
	}
	return owner.Kind() == "program" || owner.Kind() == "export_statement"
}

// jsDeclName is the naming fallback for a module-level function expression:
// the grammar keeps the name on the declarator, not on the function.
func jsDeclName(src []byte, fn *sitter.Node) string {
	if !isJSModuleFunc(fn) {
		return ""
	}
	name := fn.Parent().ChildByFieldName("name")
	if name == nil {
		return ""
	}
	return text(src, name)
}

// jsSingleParamNode returns the one parameter of an unparenthesized arrow,
// which the grammar holds in the `parameter` field as a bare pattern instead
// of a formal_parameters list.
func jsSingleParamNode(fn *sitter.Node) *sitter.Node {
	if fn.Kind() != "arrow_function" {
		return nil
	}
	return fn.ChildByFieldName("parameter")
}
