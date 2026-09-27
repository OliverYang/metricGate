// Package metrics provides language-agnostic complexity metrics.
package metrics

import (
	"fmt"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
)

// GoAdapter lowers Go AST into the language-agnostic IR for complexity scoring.
// It depends only on tree-sitter-go and cccc-core's IR—no CLI dependencies.
type GoAdapter struct {
	parser *sitter.Parser
}

// NewGoAdapter creates a new Go adapter with its own parser instance.
func NewGoAdapter() (*GoAdapter, error) {
	p := sitter.NewParser()
	if err := p.SetLanguage(sitter.NewLanguage(tree_sitter_go.Language())); err != nil {
		p.Close()
		return nil, fmt.Errorf("set Go grammar: %w", err)
	}
	return &GoAdapter{parser: p}, nil
}

// Close releases the underlying C parser.
func (a *GoAdapter) Close() { a.parser.Close() }

// AnalyzeSource parses source and scores it via the core engine.
// This is the convenience entry point used by the CLI.
func (a *GoAdapter) AnalyzeSource(path string, src []byte) (FileReport, error) {
	tree := a.parser.Parse(src, nil)
	if tree == nil {
		return FileReport{}, fmt.Errorf("parse failed")
	}
	defer tree.Close()

	nodes := a.lowerToIR(tree.RootNode(), src)
	engine := NewEngine()
	return engine.AnalyzeFile(path, nodes), nil
}

// lowerToIR converts the tree-sitter AST into the language-agnostic IR.
func (a *GoAdapter) lowerToIR(node *sitter.Node, src []byte) []Node {
	var result []Node

	for node != nil && !node.IsError() {
		child := a.visitNode(node, src)
		result = append(result, child...)
		node = node.NextSibling()
	}

	return result
}

// visitNode processes a single tree-sitter node and returns its IR representation.
func (a *GoAdapter) visitNode(node *sitter.Node, src []byte) []Node {
	switch node.Kind() {
	case "function_declaration":
		return a.lowerFuncDecl(node, src)
	case "function_literal":
		return a.lowerFuncLit(node, src)
	case "if_statement":
		return a.lowerIf(node, src)
	case "for_statement":
		return a.lowerFor(node, src)
	case "range_clause":
		return a.lowerRange(node, src)
	case "switch_statement":
		return a.lowerSwitch(node, src)
	case "type_switch_statement":
		return a.lowerTypeSwitch(node, src)
	case "select_statement":
		return a.lowerSelect(node, src)
	case "break_statement", "continue_statement", "goto_statement":
		return a.lowerJump(node, src)
	case "call_expression":
		return a.lowerCall(node, src)
	case "binary_expression":
		return a.lowerBinaryExpr(node, src)
	default:
		// Transparent container: recurse into children
		return a.lowerChildren(node, src)
	}
}

// lowerFuncDecl lowers a function declaration to IR.Function.
func (a *GoAdapter) lowerFuncDecl(node *sitter.Node, src []byte) []Node {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return a.lowerChildren(node, src)
	}
	name := string(src[nameNode.StartByte():nameNode.EndByte()])

	line := uint32(node.StartPosition().Row + 1)
	kind := "method"
	if node.ChildByFieldName("receiver") == nil {
		kind = "function"
	}

	bodyNode := node.ChildByFieldName("body")
	if bodyNode == nil || bodyNode.IsError() {
		// Bodyless declaration (e.g., external stub): skip
		return nil
	}

	body := a.lowerBlock(bodyNode, src)
	return []Node{Function{Name: name, Kind: kind, Line: line, Body: body}}
}

// lowerFuncLit lowers a function literal (closure) to IR.Function.
func (a *GoAdapter) lowerFuncLit(node *sitter.Node, src []byte) []Node {
	name := "<closure>"
	line := uint32(node.StartPosition().Row + 1)
	kind := "closure"

	bodyNode := node.ChildByFieldName("body")
	if bodyNode == nil || bodyNode.IsError() {
		return nil
	}

	body := a.lowerBlock(bodyNode, src)
	return []Node{Function{Name: name, Kind: kind, Line: line, Body: body}}
}

// lowerIf lowers an if/else statement to IR.Branch.
func (a *GoAdapter) lowerIf(node *sitter.Node, src []byte) []Node {
	test := a.lowerChildren(node.ChildByFieldName("condition"), src)
	then := a.lowerBlock(node.ChildByFieldName("body"), src)

	var alternate Node
	elseClause := node.ChildByFieldName("else_clause")
	if elseClause != nil && !elseClause.IsError() {
		elif := elseClause.ChildByFieldName("if_statement")
		if elif != nil && !elif.IsError() {
			// else-if: recursively lower as Branch
			elseIfNodes := a.lowerIf(elif, src)
			if len(elseIfNodes) > 0 {
				if b, ok := elseIfNodes[0].(Branch); ok {
					alternate = b
				}
			}
		} else {
			// plain else: wrap body in Group
			elseBody := a.lowerBlock(elseClause.ChildByFieldName("statement_list"), src)
			group := Group(elseBody)
			alternate = group
		}
	}

	return []Node{Branch{Test: test, Then: then, Alternate: alternate}}
}

// lowerFor lowers a for loop to IR.Loop.
func (a *GoAdapter) lowerFor(node *sitter.Node, src []byte) []Node {
	body := a.lowerBlock(node.ChildByFieldName("body"), src)
	return []Node{Loop{Body: body}}
}

// lowerRange lowers a range clause to IR.Loop.
func (a *GoAdapter) lowerRange(node *sitter.Node, src []byte) []Node {
	body := a.lowerBlock(node.ChildByFieldName("body"), src)
	return []Node{Loop{Body: body}}
}

// lowerSwitch lowers a switch statement to IR.Switch.
func (a *GoAdapter) lowerSwitch(node *sitter.Node, src []byte) []Node {
	cases := a.lowerCaseBlock(node.ChildByFieldName("case_block"), src)
	return []Node{Switch{Cases: cases}}
}

// lowerTypeSwitch lowers a type switch statement to IR.Switch.
func (a *GoAdapter) lowerTypeSwitch(node *sitter.Node, src []byte) []Node {
	cases := a.lowerCaseBlock(node.ChildByFieldName("case_block"), src)
	return []Node{Switch{Cases: cases}}
}

// lowerSelect lowers a select statement to IR.Switch.
func (a *GoAdapter) lowerSelect(node *sitter.Node, src []byte) []Node {
	var cases []SwitchCase
	for i := uint(0); i < node.ChildCount(); i++ {
		clause := node.Child(i)
		if clause.Kind() == "communication_clause" {
			body := a.lowerChildren(clause, src)
			isDefault := false
			if clause.ChildByFieldName("default") != nil {
				isDefault = true
			}
			cases = append(cases, SwitchCase{IsDefault: isDefault, Body: body})
		}
	}
	return []Node{Switch{Cases: cases}}
}

// lowerJump lowers break/continue/goto statements to IR.Jump.
func (a *GoAdapter) lowerJump(node *sitter.Node, src []byte) []Node {
	labeled := node.ChildByFieldName("label") != nil
	return []Node{Jump{Labeled: labeled}}
}

// lowerCall lowers a function call to IR.Call for recursion detection.
func (a *GoAdapter) lowerCall(node *sitter.Node, src []byte) []Node {
	var callee *string

	ident := node.ChildByFieldName("identifier")
	if ident != nil && !ident.IsError() {
		name := string(src[ident.StartByte():ident.EndByte()])
		callee = &name
	} else if selector := node.ChildByFieldName("selector_expression"); selector != nil && !selector.IsError() {
		// Method call: extract method name from selector
		if selSel := selector.ChildByFieldName("selector"); selSel != nil && !selSel.IsError() {
			if ident := selSel.ChildByFieldName("identifier"); ident != nil && !ident.IsError() {
				name := string(src[ident.StartByte():ident.EndByte()])
				callee = &name
			}
		}
	}
	return []Node{Call{Callee: callee}}
}

// lowerBinaryExpr handles && and || logical operators.
func (a *GoAdapter) lowerBinaryExpr(node *sitter.Node, src []byte) []Node {
	op := node.ChildByFieldName("operator")
	if op == nil || op.IsError() {
		return a.lowerChildren(node, src)
	}

	opStr := string(src[op.StartByte():op.EndByte()])
	if opStr != "&&" && opStr != "||" {
		return a.lowerChildren(node, src)
	}

	// Fold runs of same-operator into single Logical node
	logicalOp := LogicalAnd
	if opStr == "||" {
		logicalOp = LogicalOr
	}

	operands := a.collectLogicalOperands(node, src, logicalOp)
	return []Node{Logical{Op: logicalOp, Operands: operands}}
}

// collectLogicalOperands flattens a run of same-operator logical expressions.
func (a *GoAdapter) collectLogicalOperands(node *sitter.Node, src []byte, op LogicalOp) []Node {
	var operands []Node
	a.flattenLogical(node, src, op, &operands)
	return operands
}

// flattenLogical recursively collects operands of the same operator.
func (a *GoAdapter) flattenLogical(node *sitter.Node, src []byte, targetOp LogicalOp, operands *[]Node) {
	if node.Kind() == "binary_expression" {
		op := node.ChildByFieldName("operator")
		if op == nil || op.IsError() {
			return
		}

		opStr := string(src[op.StartByte():op.EndByte()])
		currentOp := LogicalAnd
		if opStr == "||" {
			currentOp = LogicalOr
		}

		if currentOp == targetOp {
			// Same operator: recurse into both sides
			x := node.ChildByFieldName("x_expression")
			y := node.ChildByFieldName("y_expression")
			if x != nil && !x.IsError() {
				a.flattenLogical(x, src, targetOp, operands)
			}
			if y != nil && !y.IsError() {
				a.flattenLogical(y, src, targetOp, operands)
			}
		} else {
			// Different operator: treat this node as a Group
			*operands = append(*operands, Group(a.lowerChildren(node, nil)))
		}
	} else {
		// Leaf operand: wrap in Group
		*operands = append(*operands, Group(a.lowerChildren(node, nil)))
	}
}

// lowerCaseBlock lowers switch case clauses to []SwitchCase.
func (a *GoAdapter) lowerCaseBlock(node *sitter.Node, src []byte) []SwitchCase {
	var cases []SwitchCase
	if node == nil || node.IsError() {
		return cases
	}

	for i := uint(0); i < node.ChildCount(); i++ {
		clause := node.Child(i)
		if clause.Kind() == "case_clause" {
			body := a.lowerChildren(clause, src)
			// Empty expression list means default
			isDefault := true
			for j := uint(0); j < clause.ChildCount(); j++ {
				if clause.Child(j).Kind() != "statement_list" {
					isDefault = false
					break
				}
			}
			cases = append(cases, SwitchCase{IsDefault: isDefault, Body: body})
		}
	}
	return cases
}

// lowerBlock lowers a block statement to []Node.
func (a *GoAdapter) lowerBlock(node *sitter.Node, src []byte) []Node {
	if node == nil || node.IsError() {
		return nil
	}
	return a.lowerChildren(node, src)
}

// lowerChildren recurses into all children and collects their IR representations.
func (a *GoAdapter) lowerChildren(node *sitter.Node, src []byte) []Node {
	var result []Node
	if node == nil || node.IsError() {
		return result
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		childIR := a.visitNode(child, src)
		result = append(result, childIR...)
	}
	return result
}
