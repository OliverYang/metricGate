// Package metrics computes structural metrics directly from the tree-sitter
// AST. It is policy-free: it only measures. Threshold enforcement lives in
// the engine. See docs/metrics.md for the exact definitions and their
// (documented) deviations from SonarSource's published specs.
package metrics

import (
	"bytes"
	"fmt"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// FuncStats carries every per-function measurement in one pass.
type FuncStats struct {
	Name       string
	StartLine  int // 1-based
	EndLine    int // 1-based inclusive
	Length     int // lines
	Params     int
	Cyclomatic int
	Cognitive  int
	MaxNesting int
}

// FileStats is the result of measuring one file.
type FileStats struct {
	File      string
	Lines     int
	Functions []FuncStats
}

// Metric identifiers produced by this package.
const (
	MetricCyclomatic = "cyclomatic-complexity"
	MetricCognitive  = "cognitive-complexity"
	MetricFuncLength = "function-length"
	MetricFileLength = "file-length"
	MetricNesting    = "nesting-depth"
	MetricParams     = "params-count"
)

// text returns the source slice covered by a node.
func text(src []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return string(src[n.StartByte():n.EndByte()])
}

// AnalyzeFile measures one parsed file.
func AnalyzeFile(file string, src []byte, tree *sitter.Tree) *FileStats {
	fs := &FileStats{File: file, Lines: bytes.Count(src, []byte{'\n'}) + 1}
	root := tree.RootNode()

	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		switch n.Kind() {
		case "function_declaration", "method_declaration":
			fs.Functions = append(fs.Functions, measureFunc(src, n))
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return fs
}

// measureFunc computes all function-level metrics for one declaration.
func measureFunc(src []byte, fn *sitter.Node) FuncStats {
	st := FuncStats{
		Name:      funcName(src, fn),
		StartLine: int(fn.StartPosition().Row) + 1,
		EndLine:   int(fn.EndPosition().Row) + 1,
	}
	st.Length = st.EndLine - st.StartLine + 1
	if params := fn.ChildByFieldName("parameters"); params != nil {
		st.Params = countParams(params)
	}
	if body := fn.ChildByFieldName("body"); body != nil {
		st.Cyclomatic = cyclomatic(body, src)
		st.Cognitive, st.MaxNesting = cognitive(body, src)
	}
	return st
}

// countParams counts declared parameters: "a, b int" is two parameters, an
// unnamed "(int)" is one, and a variadic declaration is one.
func countParams(list *sitter.Node) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		decl := list.NamedChild(i)
		switch decl.Kind() {
		case "parameter_declaration":
			ids := 0
			for j := uint(0); j < decl.NamedChildCount(); j++ {
				if decl.NamedChild(j).Kind() == "identifier" {
					ids++
				}
			}
			if ids == 0 {
				n++ // unnamed parameter: (int)
			} else {
				n += ids
			}
		default:
			n++ // variadic_parameter_declaration and friends
		}
	}
	return n
}

func funcName(src []byte, fn *sitter.Node) string {
	name := text(src, fn.ChildByFieldName("name"))
	if recv := fn.ChildByFieldName("receiver"); recv != nil && name != "" {
		// Render methods as "(*T).Name" style shorthand.
		r := strings.TrimSpace(text(src, recv))
		r = strings.TrimPrefix(r, "(")
		r = strings.TrimSuffix(r, ")")
		// "(t *T)" — keep the type token and drop the pointer star.
		if fields := strings.Fields(r); len(fields) > 0 {
			r = fields[len(fields)-1]
		}
		r = strings.TrimPrefix(r, "*")
		return fmt.Sprintf("%s.%s", r, name)
	}
	if name == "" {
		return "<anonymous>"
	}
	return name
}

// cyclomatic counts decision points per the definition in docs/metrics.md:
// 1 + every if/for, every case (including select communication cases) and
// every && / || operator.
func cyclomatic(body *sitter.Node, src []byte) int {
	cc := 1
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		switch n.Kind() {
		case "if_statement", "for_statement", "expression_case", "type_case", "communication_case":
			cc++
		case "binary_expression":
			if op := text(src, n.ChildByFieldName("operator")); op == "&&" || op == "||" {
				cc++
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(body)
	return cc
}

// cognitive computes a cognitive complexity and the deepest nesting reached,
// in one walk. nesting is 0 at the function body top level.
func cognitive(body *sitter.Node, src []byte) (cog, maxNest int) {
	var walk func(n *sitter.Node, nesting int)
	walk = func(n *sitter.Node, nesting int) {
		if n == nil {
			return
		}
		inner := nesting
		switch n.Kind() {
		case "if_statement", "for_statement", "expression_switch_statement", "type_switch_statement", "select_statement":
			cog += 1 + nesting
			inner = nesting + 1
		case "func_literal":
			// Nested functions add a nesting level but no flat bonus (see
			// docs/metrics.md for the deviation note).
			inner = nesting + 1
		case "binary_expression":
			if op := text(src, n.ChildByFieldName("operator")); op == "&&" || op == "||" {
				parent := n.Parent()
				if parent == nil || parent.Kind() != "binary_expression" ||
					text(src, parent.ChildByFieldName("operator")) != op {
					// Start of a run of the same logical operator.
					cog++
				}
			}
		}
		if nesting > maxNest {
			maxNest = nesting
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i), inner)
		}
	}
	walk(body, 0)
	return cog, maxNest
}
