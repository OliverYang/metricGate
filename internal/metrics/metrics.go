// Package metrics computes structural metrics directly from the tree-sitter
// AST. It is policy-free: it only measures. Threshold enforcement lives in
// the engine. See docs/metrics.md for the exact definitions per language and
// their (documented) deviations from SonarSource's published specs.
package metrics

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"

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
	FanOut     int
	BoolParams int
	Cyclomatic int
	Cognitive  int
	MaxNesting int
	NPath      uint64
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
	MetricNPath      = "npath-complexity"

	// MetricFanOut counts the distinct callees of one function (Yourdon &
	// Constantine's fan-out: how many modules this module calls).
	MetricFanOut = "fan-out"
	// MetricBoolParams counts parameters declared bool/boolean — the
	// "flag argument" bad smell (Fowler): each extra bool doubles the
	// states the caller must reason about.
	MetricBoolParams = "boolean-params"

	// Class-level (CK family, Java only).
	MetricWMC  = "wmc"
	MetricNOM  = "nom"
	MetricLCOM = "lcom"
	MetricCBO  = "cbo"
	MetricDIT  = "dit"
)

// sharedMetrics are the identifiers every language produces. The CK family is
// Java-only and appended by IDsFor.
var sharedMetrics = []string{
	MetricCyclomatic, MetricCognitive, MetricFuncLength,
	MetricFileLength, MetricNesting, MetricParams, MetricNPath,
	MetricFanOut, MetricBoolParams, MetricMaintainability,
}

// javaOnlyMetrics are the CK class metrics, produced only for Java.
var javaOnlyMetrics = []string{MetricWMC, MetricNOM, MetricLCOM, MetricCBO, MetricDIT}

// IDsFor returns the metric identifiers a language produces, in a stable
// order.
//
// It exists because "which rules can fire" was previously knowable only by
// running the measurer: the rule catalogue held AST and architecture rules but
// no metric, so an audit of that catalogue was silent about the rules that
// produce most findings. engine.Catalog is its only consumer, and
// TestCatalogCoversEveryMeasuredMetric holds it to what a real run emits.
func IDsFor(lang string) []string {
	out := append([]string{}, sharedMetrics...)
	if lang == string(parser.LangJava) {
		out = append(out, javaOnlyMetrics...)
	}
	return out
}

// Titles is the one-line description of each metric, used wherever a metric is
// listed as a rule (the catalogue, a policy audit). It falls back to the id.
var Titles = map[string]string{
	MetricCyclomatic:      "Function cyclomatic complexity above threshold",
	MetricCognitive:       "Function cognitive complexity above threshold",
	MetricFuncLength:      "Function longer than threshold",
	MetricFileLength:      "File longer than threshold",
	MetricNesting:         "Function nests deeper than threshold",
	MetricParams:          "Function takes more parameters than threshold",
	MetricNPath:           "Function has more execution paths than threshold (NPATH)",
	MetricFanOut:          "Function calls more distinct callees than threshold (fan-out)",
	MetricBoolParams:      "Function takes more boolean parameters than threshold",
	MetricMaintainability: "Maintainability index below threshold",
	MetricWMC:             "Weighted methods per class above threshold",
	MetricNOM:             "Number of methods per class above threshold",
	MetricLCOM:            "Lack of cohesion of methods above threshold",
	MetricCBO:             "Coupling between object classes above threshold",
	MetricDIT:             "Depth of inheritance tree above threshold",
}

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
	// NPATH (Nejmeh 1988): the AT&T Bell Labs empirical limit of 200 is the
	// published convention (checkstyle default max is 200 as well).
	MetricNPath: {200, 200},

	// Fan-out (Yourdon & Constantine, structured design): a module should
	// call few others; the defaults stay gate-light and policies override
	// per language.
	MetricFanOut: {10, 20},
	// Boolean parameters (flag-argument smell, Fowler): each extra bool
	// doubles the hidden states of the call; two bools are already four.
	MetricBoolParams: {2, 4},

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

// text returns the source slice covered by a node.
func text(src []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return string(src[n.StartByte():n.EndByte()])
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
	prof := ProfileFor(lang)
	fs := &FileStats{File: file, Lines: bytes.Count(src, []byte{'\n'}) + 1}

	measureClasses(lang, src, tree.RootNode(), fs)

	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if prof.FuncKinds.Has(n) {
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
func measureFunc(lang parser.LanguageID, prof *LangProfile, src []byte, fn *sitter.Node) FuncStats {
	st := FuncStats{
		Name:      funcName(src, fn),
		StartLine: int(fn.StartPosition().Row) + 1,
		EndLine:   int(fn.EndPosition().Row) + 1,
	}
	st.Length = st.EndLine - st.StartLine + 1
	if params := fn.ChildByFieldName("parameters"); params != nil {
		st.Params = countParams(lang, params)
		st.BoolParams = countBoolParams(lang, src, params)
	}
	if body := fn.ChildByFieldName("body"); body != nil {
		st.Body = body
		st.FanOut = fanOut(lang, src, body)
		st.Cyclomatic, st.Cognitive, st.MaxNesting = irScore(lang, src, fn)
		st.NPath = irNPath(lang, src, fn)
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

// countBoolParams counts parameters declared bool (Go) or boolean (Java).
// The flag-argument smell: every bool doubles the hidden states the caller
// must reason about, so a two-bool call site already hides four modes.
func countBoolParams(lang parser.LanguageID, src []byte, list *sitter.Node) int {
	boolKind := "type_identifier" // Go: bool is spelled as a type_identifier
	if lang == parser.LangJava {
		boolKind = "boolean_type"
	}
	n := 0
	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		if node.Kind() == boolKind && (lang == parser.LangJava || text(src, node) == "bool") {
			n++
			return
		}
		for i := uint(0); i < node.NamedChildCount(); i++ {
			walk(node.NamedChild(i))
		}
	}
	for i := uint(0); i < list.NamedChildCount(); i++ {
		walk(list.NamedChild(i))
	}
	return n
}

// fanOut counts the distinct callees invoked inside one function body —
// Yourdon & Constantine's fan-out. The callee identity is the whitespace-
// stripped spelling of the call target: Go uses the function field of a
// call_expression (identifier "Do" or selector "pkg.Do" — different
// targets stay distinct), Java uses the name identifier of a
// method_invocation (chained calls x.a().b() contribute a and b).
// Function-literal invocations (Go) and constructor calls (Java) are not
// behavior-of-another-unit calls and are excluded.
func fanOut(lang parser.LanguageID, src []byte, body *sitter.Node) int {
	callKind, calleeField := "call_expression", "function"
	if lang == parser.LangJava {
		callKind, calleeField = "method_invocation", "name"
	}
	seen := map[string]bool{}
	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		if node.Kind() == callKind {
			if callee := node.ChildByFieldName(calleeField); callee != nil && callee.Kind() != "function_literal" {
				seen[flattenWS(text(src, callee))] = true
			}
		}
		for i := uint(0); i < node.NamedChildCount(); i++ {
			walk(node.NamedChild(i))
		}
	}
	walk(body)
	return len(seen)
}

// flattenWS removes all whitespace, so a folded multi-line call target
// compares equal to its one-line spelling.
func flattenWS(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// irScore lowers one declaration into the IR and scores it, returning
// cyclomatic, cognitive and the deepest nesting reached. All decision-point
// knowledge lives in the language adapters and the engine; this package only
// knows which node kinds open a function.
func irScore(lang parser.LanguageID, src []byte, fn *sitter.Node) (cyc, cog, maxNest int) {
	rep := analyzeFunction(lowererFor(lang).lowerFunc(fn, src))
	return int(rep.Cyclomatic), int(rep.Cognitive), int(rep.MaxNesting)
}

// irNPath lowers one declaration and returns its NPATH path count.
func irNPath(lang parser.LanguageID, src []byte, fn *sitter.Node) uint64 {
	return analyzeFunction(lowererFor(lang).lowerFunc(fn, src)).NPath
}

// lowererFor returns the IR lowerer of a language.
func lowererFor(lang parser.LanguageID) nodeLowerer {
	switch lang {
	case parser.LangJava:
		return javaLowerer{}
	default:
		return goLowerer{}
	}
}
