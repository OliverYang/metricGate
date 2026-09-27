// Package metrics provides language-agnostic complexity metrics.
package metrics

import (
	"fmt"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
)

// JavaAdapter lowers Java AST into the language-agnostic IR for complexity scoring.
// It depends only on tree-sitter-java and cccc-core's IR—no CLI dependencies.
type JavaAdapter struct {
	parser *sitter.Parser
}

// NewJavaAdapter creates a new Java adapter with its own parser instance.
func NewJavaAdapter() (*JavaAdapter, error) {
	p := sitter.NewParser()
	if err := p.SetLanguage(sitter.NewLanguage(tree_sitter_java.Language())); err != nil {
		p.Close()
		return nil, fmt.Errorf("set Java grammar: %w", err)
	}
	return &JavaAdapter{parser: p}, nil
}

// Close releases the underlying C parser.
func (a *JavaAdapter) Close() { a.parser.Close() }

// AnalyzeSource parses source and scores it via the core engine.
// This is the convenience entry point used by the CLI.
func (a *JavaAdapter) AnalyzeSource(path string, src []byte) (FileReport, error) {
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
func (a *JavaAdapter) lowerToIR(node *sitter.Node, src []byte) []Node {
	var result []Node

	for node != nil && !node.IsError() {
		child := a.visitNode(node, src)
		result = append(result, child...)
		node = node.NextSibling()
	}

	return result
}

// visitNode processes a single tree-sitter node and returns its IR representation.
func (a *JavaAdapter) visitNode(node *sitter.Node, src []byte) []Node {
	switch node.Kind() {
	case "method_declaration":
		return a.lowerMethodDecl(node, src)
	case "constructor_declaration":
		return a.lowerConstructor(node, src)
	case "if_statement":
		return a.lowerIf(node, src)
	case "for_statement":
		return a.lowerFor(node, src)
	case "enhanced_for_statement":
		return a.lowerEnhancedFor(node, src)
	case "while_statement":
		return a.lowerWhile(node, src)
	case "conditional_expression":
		return a.lowerConditional(node, src)
	case "do_statement":
		return a.lowerDoWhile(node, src)
	case "switch_statement":
		return a.lowerSwitch(node, src)
	case "try_statement":
		return a.lowerTry(node, src)
	case "break_statement", "continue_statement":
		return a.lowerJump(node, src)
	case "identifier", "class_identifier":
		// Potential function call target - handled in call_expression
		return a.lowerChildren(node, src)
	case "primary_expression":
		return a.lowerPrimaryExpr(node, src)
	default:
		// Transparent container: recurse into children
		return a.lowerChildren(node, src)
	}
}

// lowerPrimaryExpr handles method calls and field access for recursion detection.
func (a *JavaAdapter) lowerPrimaryExpr(node *sitter.Node, src []byte) []Node {
	// Look for method invocation inside primary expression
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "method_invocation" {
			return a.lowerCall(node, src)
		}
	}
	return a.lowerChildren(node, src)
}

// lowerMethodDecl lowers a method declaration to IR.Function.
func (a *JavaAdapter) lowerMethodDecl(node *sitter.Node, src []byte) []Node {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil || nameNode.IsError() {
		return a.lowerChildren(node, src)
	}
	name := string(src[nameNode.StartByte():nameNode.EndByte()])

	line := uint32(node.StartPosition().Row + 1)
	bodyNode := node.ChildByFieldName("body")

	if bodyNode == nil || bodyNode.IsError() {
		return nil
	}

	body := a.lowerBlock(bodyNode, src)
	return []Node{Function{Name: name, Kind: "method", Line: line, Body: body}}
}

// lowerConstructor lowers a constructor to IR.Function.
func (a *JavaAdapter) lowerConstructor(node *sitter.Node, src []byte) []Node {
	identNode := node.ChildByFieldName("identifier")
	if identNode == nil || identNode.IsError() {
		return a.lowerChildren(node, src)
	}
	name := string(src[identNode.StartByte():identNode.EndByte()])

	line := uint32(node.StartPosition().Row + 1)
	bodyNode := node.ChildByFieldName("body")

	if bodyNode == nil || bodyNode.IsError() {
		return nil
	}

	body := a.lowerBlock(bodyNode, src)
	return []Node{Function{Name: name, Kind: "constructor", Line: line, Body: body}}
}

// lowerIf lowers an if/else statement to IR.Branch.
func (a *JavaAdapter) lowerIf(node *sitter.Node, src []byte) []Node {
	test := a.lowerChildren(node.ChildByFieldName("condition"), src)
	then := a.lowerBlock(node.ChildByFieldName("block"), src)

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
			elseBody := a.lowerBlock(elseClause.ChildByFieldName("block"), src)
			group := Group(elseBody)
			alternate = group
		}
	}

	return []Node{Branch{Test: test, Then: then, Alternate: alternate}}
}

// lowerFor lowers a traditional for loop to IR.Loop.
func (a *JavaAdapter) lowerFor(node *sitter.Node, src []byte) []Node {
	body := a.lowerBlock(node.ChildByFieldName("block"), src)
	return []Node{Loop{Body: body}}
}

// lowerEnhancedFor lowers a foreach loop to IR.Loop.
func (a *JavaAdapter) lowerEnhancedFor(node *sitter.Node, src []byte) []Node {
	body := a.lowerBlock(node.ChildByFieldName("block"), src)
	return []Node{Loop{Body: body}}
}

// lowerWhile lowers a while loop to IR.Loop.
func (a *JavaAdapter) lowerWhile(node *sitter.Node, src []byte) []Node {
	body := a.lowerBlock(node.ChildByFieldName("block"), src)
	return []Node{Loop{Body: body}}
}

// lowerDoWhile lowers a do-while loop to IR.Loop.
func (a *JavaAdapter) lowerDoWhile(node *sitter.Node, src []byte) []Node {
	body := a.lowerBlock(node.ChildByFieldName("block"), src)
	return []Node{Loop{Body: body}}
}

// lowerConditional lowers a ternary expression to IR.Conditional.
func (a *JavaAdapter) lowerConditional(node *sitter.Node, src []byte) []Node {
	test := a.lowerChildren(node.ChildByFieldName("condition"), src)
	then := a.lowerChildren(node.ChildByFieldName("true_expression"), src)
	alternate := a.lowerChildren(node.ChildByFieldName("false_expression"), src)
	return []Node{Conditional{Test: test, Then: then, Alternate: alternate}}
}

// lowerSwitch lowers a switch statement to IR.Switch.
func (a *JavaAdapter) lowerSwitch(node *sitter.Node, src []byte) []Node {
	cases := a.lowerCaseBlock(node.ChildByFieldName("switch_block"), src)
	return []Node{Switch{Cases: cases}}
}

// lowerTry lowers a try/catch/finally to IR.Catch.
func (a *JavaAdapter) lowerTry(node *sitter.Node, src []byte) []Node {
	var catches []Node

	catchClauseList := node.ChildByFieldName("catch_clause_list")
	if catchClauseList != nil && !catchClauseList.IsError() {
		for i := uint(0); i < catchClauseList.ChildCount(); i++ {
			catchClause := catchClauseList.Child(i)
			catchBody := a.lowerBlock(catchClause.ChildByFieldName("block"), src)
			catches = append(catches, Catch{Body: catchBody})
		}
	}

	// If there are catches, report them; finally is transparent for complexity
	if len(catches) > 0 {
		return catches
	}

	// No catches - just walk the block
	block := a.lowerBlock(node.ChildByFieldName("block"), src)
	return block
}

// lowerJump lowers break/continue statements to IR.Jump.
func (a *JavaAdapter) lowerJump(node *sitter.Node, src []byte) []Node {
	// Java break/continue can have labels: break label; continue label;
	labeled := false
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "identifier" && child.Parent().Kind() == "label" {
			labeled = true
			break
		}
	}
	return []Node{Jump{Labeled: labeled}}
}

// lowerCall lowers a method call to IR.Call for recursion detection.
func (a *JavaAdapter) lowerCall(node *sitter.Node, src []byte) []Node {
	var callee *string

	ident := node.ChildByFieldName("identifier")
	if ident != nil && !ident.IsError() {
		name := string(src[ident.StartByte():ident.EndByte()])
		callee = &name
	} else if selector := node.ChildByFieldName("selector_expression"); selector != nil && !selector.IsError() {
		// Method call on object: obj.method()
		if selIdent := selector.ChildByFieldName("identifier"); selIdent != nil && !selIdent.IsError() {
			name := string(src[selIdent.StartByte():selIdent.EndByte()])
			callee = &name
		}
	}

	return []Node{Call{Callee: callee}}
}

// lowerCaseBlock lowers switch case clauses to []SwitchCase.
func (a *JavaAdapter) lowerCaseBlock(node *sitter.Node, src []byte) []SwitchCase {
	var cases []SwitchCase
	if node == nil || node.IsError() {
		return cases
	}

	for i := uint(0); i < node.ChildCount(); i++ {
		rule := node.Child(i)
		if rule.Kind() != "rule" {
			continue
		}

		// Check for default label
		isDefault := false
		for j := uint(0); j < rule.ChildCount(); j++ {
			label := rule.Child(j)
			if label.Kind() == "label" && hasToken(src, label, "default") {
				isDefault = true
				break
			}
		}

		// Get the block body
		block := rule.ChildByFieldName("block")
		body := a.lowerBlock(block, src)

		cases = append(cases, SwitchCase{IsDefault: isDefault, Body: body})
	}

	return cases
}

// lowerBlock lowers a block statement to []Node.
func (a *JavaAdapter) lowerBlock(node *sitter.Node, src []byte) []Node {
	if node == nil || node.IsError() {
		return nil
	}
	return a.lowerChildren(node, src)
}

// lowerChildren recurses into all children and collects their IR representations.
func (a *JavaAdapter) lowerChildren(node *sitter.Node, src []byte) []Node {
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
