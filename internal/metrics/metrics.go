// Package metrics computes structural metrics directly from the tree-sitter
// AST. It is policy-free: it only measures. Threshold enforcement lives in
// the engine. See docs/metrics.md for the exact definitions per language and
// their (documented) deviations from SonarSource's published specs.
package metrics

import (
	"bytes"
	"fmt"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/parser"
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
	// Maintainability (B-2): Halstead volume of the body and this
	// function's MI. Body is retained for the census walk only.
	Body   *sitter.Node `json:"-"`
	Volume int
	MI     float64
}

// FileStats is the result of measuring one file.
type FileStats struct {
	File      string
	Lines     int
	Functions []FuncStats
	Classes   []ClassStats // Java only (CK family); empty for Go
	MI        float64      // worst function MI, 0 when no functions (see mi.go)
	MIScope   string       // the function that scored the MI
	MILine    int          // its declaration line
}

// Metric identifiers produced by this package.
const (
	MetricCyclomatic = "cyclomatic-complexity"
	MetricCognitive  = "cognitive-complexity"
	MetricFuncLength = "function-length"
	MetricFileLength = "file-length"
	MetricNesting    = "nesting-depth"
	MetricParams     = "params-count"

	// Class-level (CK family, Java only).
	MetricWMC  = "wmc"
	MetricNOM  = "nom"
	MetricLCOM = "lcom"
	MetricCBO  = "cbo"
	MetricDIT  = "dit"
)

// DefaultThresholds is the built-in warn/error pair per metric — the single
// source of truth shared by the engine and the context renderer. Policies
// override these per language.
var DefaultThresholds = map[string]struct {
	Warn  int
	Error int
}{
	MetricCyclomatic: {10, 15},
	MetricCognitive:  {15, 25},
	MetricFuncLength: {50, 100},
	MetricFileLength: {400, 800},
	MetricNesting:    {4, 6},
	MetricParams:     {5, 8},

	// CK family, Java only. LCOM is advisory by default (the original CK
	// definition flags real designs often); WMC/NOM/CBO/DIT warn high.
	MetricWMC:  {40, 60},
	MetricNOM:  {20, 30},
	MetricLCOM: {20, 40},
	MetricCBO:  {15, 25},
	MetricDIT:  {5, 8},

	// Maintainability debt = 100 - MI (function-level; see mi.go).
	// Report-only by default: MI is a design smell for the metrics table
	// and the trend store, not a gate verdict (design round 3, FP clause).
	// A policy may set warn/error to gate it.
	MetricMaintainability: {0, 0},
}

// langProfile declares the grammar-specific node kinds the metric walks
// care about. This is the single place where node names live.
type langProfile struct {
	funcKinds   map[string]bool // function-like units
	branchKinds map[string]bool // cyclomatic +1; cognitive +1+nesting; nesting++
	switchKinds map[string]bool // cognitive +1+nesting (cyclomatic counts cases instead)
	caseKinds   map[string]bool // cyclomatic +1 (Java: `default` labels excluded)
	flatCog     map[string]bool // cognitive +1 flat (Java catch)
	lambdaKinds map[string]bool // nesting++ only (Go func literals, Java lambdas)
	ternaryKind string          // cyclomatic +1, or "" when the language has none
}

func kindSet(kindList ...string) map[string]bool {
	m := make(map[string]bool, len(kindList))
	for _, k := range kindList {
		m[k] = true
	}
	return m
}

var profiles = map[parser.LanguageID]langProfile{
	parser.LangGo: {
		funcKinds:   kindSet("function_declaration", "method_declaration"),
		branchKinds: kindSet("if_statement", "for_statement"),
		switchKinds: kindSet("expression_switch_statement", "type_switch_statement", "select_statement"),
		caseKinds:   kindSet("expression_case", "type_case", "communication_case"),
		flatCog:     kindSet(),
		lambdaKinds: kindSet("func_literal"),
	},
	parser.LangJava: {
		funcKinds:   kindSet("method_declaration", "constructor_declaration"),
		branchKinds: kindSet("if_statement", "for_statement", "enhanced_for_statement", "while_statement", "do_statement"),
		switchKinds: kindSet("switch_expression"),
		caseKinds:   kindSet("switch_label"),
		flatCog:     kindSet("catch_clause"),
		ternaryKind: "ternary_expression",
		lambdaKinds: kindSet("lambda_expression"),
	},
}

func profileFor(lang parser.LanguageID) langProfile {
	if p, ok := profiles[lang]; ok {
		return p
	}
	return profiles[parser.LangGo]
}

// text returns the source slice covered by a node.
func text(src []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return string(src[n.StartByte():n.EndByte()])
}

// logicOp returns "&&"/"||" for logical binary expressions, "" otherwise.
// Go names the operator field; Java emits the operator as an anonymous child.
func logicOp(src []byte, n *sitter.Node) string {
	if op := n.ChildByFieldName("operator"); op != nil {
		if t := text(src, op); t == "&&" || t == "||" {
			return t
		}
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if !c.IsNamed() {
			if t := text(src, c); t == "&&" || t == "||" {
				return t
			}
		}
	}
	return ""
}

// hasToken reports whether one of the node's anonymous children is exactly
// the given token (e.g. the `default` keyword inside a Java switch label).
func hasToken(src []byte, n *sitter.Node, tok string) bool {
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if !c.IsNamed() && text(src, c) == tok {
			return true
		}
	}
	return false
}

// AnalyzeFile measures one parsed file of the given language.
func AnalyzeFile(file string, src []byte, tree *sitter.Tree, lang parser.LanguageID) *FileStats {
	prof := profileFor(lang)
	fs := &FileStats{File: file, Lines: bytes.Count(src, []byte{'\n'}) + 1}

	measureClasses(lang, src, tree.RootNode(), fs, prof)

	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if prof.funcKinds[n.Kind()] {
			fs.Functions = append(fs.Functions, measureFunc(lang, prof, src, n))
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(tree.RootNode())

	// Function-level Maintainability Index (design doc B-2, as corrected
	// by its own dogfood check: at file scale the ln(LOC) term dominates
	// and the classic 65/85 bands are unreachable, so MI is computed per
	// function — the Visual Studio code-metrics scope — and the file
	// reports its worst function). Functions absent -> 0 (not measured).
	fs.MI = 0
	worst := 100.0
	for i := range fs.Functions {
		fn := &fs.Functions[i]
		if fn.Body == nil {
			continue
		}
		hal := CountHalstead(src, fn.Body)
		fn.Volume = int(hal.Volume() + 0.5)
		fn.MI = MaintainabilityIndex(fn.Volume, fn.Cyclomatic, fn.Length)
		if fn.MI < worst {
			worst = fn.MI
			fs.MI = fn.MI
			fs.MIScope = fn.Name
			fs.MILine = fn.StartLine
		}
	}
	if len(fs.Functions) == 0 {
		fs.MI = 0
	}
	return fs
}

// measureFunc computes all function-level metrics for one declaration.
func measureFunc(lang parser.LanguageID, prof langProfile, src []byte, fn *sitter.Node) FuncStats {
	st := FuncStats{
		Name:      funcName(src, fn),
		StartLine: int(fn.StartPosition().Row) + 1,
		EndLine:   int(fn.EndPosition().Row) + 1,
	}
	st.Length = st.EndLine - st.StartLine + 1
	if params := fn.ChildByFieldName("parameters"); params != nil {
		st.Params = countParams(lang, params)
	}
	if body := fn.ChildByFieldName("body"); body != nil {
		st.Body = body
		st.Cyclomatic = cyclomatic(prof, body, src)
		st.Cognitive, st.MaxNesting = cognitive(prof, body, src)
	}
	return st
}

func funcName(src []byte, fn *sitter.Node) string {
	name := text(src, fn.ChildByFieldName("name"))
	if name == "" {
		return "<anonymous>"
	}
	// Go methods: receiver "(t *T)" → "T.Name".
	if recv := fn.ChildByFieldName("receiver"); recv != nil {
		r := strings.TrimSpace(text(src, recv))
		r = strings.TrimPrefix(r, "(")
		r = strings.TrimSuffix(r, ")")
		if fields := strings.Fields(r); len(fields) > 0 {
			r = fields[len(fields)-1]
		}
		r = strings.TrimPrefix(r, "*")
		if r != "" {
			return fmt.Sprintf("%s.%s", r, name)
		}
	}
	return name
}

// countParams counts declared parameters.
// Go: "a, b int" is two parameters, an unnamed "(int)" is one, a variadic
// declaration is one. Java: every formal_parameter / spread_parameter is one.
func countParams(lang parser.LanguageID, list *sitter.Node) int {
	if lang == parser.LangJava {
		n := 0
		for i := uint(0); i < list.NamedChildCount(); i++ {
			switch list.NamedChild(i).Kind() {
			case "formal_parameter", "spread_parameter":
				n++
			}
		}
		return n
	}
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		decl := list.NamedChild(i)
		if decl.Kind() != "parameter_declaration" {
			n++ // variadic_parameter_declaration and friends
			continue
		}
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
	}
	return n
}

// cyclomatic: 1 + decision points. Per language the decision points are the
// branch kinds, the case entries (Java `default` opens no new path), the
// ternary operator (Java) and the && / || operators. See docs/metrics.md.
func cyclomatic(prof langProfile, body *sitter.Node, src []byte) int {
	cc := 1
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		kind := n.Kind()
		switch {
		case prof.caseKinds[kind] && hasToken(src, n, "default"):
			// `default:` opens no new path.
		case prof.branchKinds[kind], prof.caseKinds[kind], prof.ternaryKind == kind:
			cc++
		case kind == "binary_expression":
			if logicOp(src, n) != "" {
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

// cognitive computes cognitive complexity and the deepest nesting reached,
// in one walk. nesting is 0 at the function body top level.
func cognitive(prof langProfile, body *sitter.Node, src []byte) (cog, maxNest int) {
	var walk func(n *sitter.Node, nesting int)
	walk = func(n *sitter.Node, nesting int) {
		if n == nil {
			return
		}
		inner := nesting
		kind := n.Kind()
		switch {
		case prof.branchKinds[kind], prof.switchKinds[kind]:
			cog += 1 + nesting
			inner = nesting + 1
		case prof.flatCog[kind]:
			// catch: +1 flat, no nesting change.
			cog++
		case prof.lambdaKinds[kind]:
			// Nested functions add a nesting level but no flat bonus.
			inner = nesting + 1
		case kind == "binary_expression":
			if op := logicOp(src, n); op != "" {
				parent := n.Parent()
				if parent == nil || parent.Kind() != "binary_expression" || logicOp(src, parent) != op {
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
