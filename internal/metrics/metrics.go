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
	Classes   []ClassStats // CK family; empty for a language without classes
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

	// Class-level (CK family; see classes_lang.go for which languages
	// declare classes).
	MetricWMC  = "wmc"
	MetricNOM  = "nom"
	MetricLCOM = "lcom"
	MetricCBO  = "cbo"
	MetricDIT  = "dit"
)

// sharedMetrics are the identifiers every language produces. The CK family
// is appended by IDsFor for the languages that declare classes.
var sharedMetrics = []string{
	MetricCyclomatic, MetricCognitive, MetricFuncLength,
	MetricFileLength, MetricNesting, MetricParams, MetricNPath,
	MetricFanOut, MetricBoolParams, MetricMaintainability,
}

// classMetrics are the CK class metrics, produced by every language with a
// class spelling and by no other.
var classMetrics = []string{MetricWMC, MetricNOM, MetricLCOM, MetricCBO, MetricDIT}

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
	if HasClasses(lang) {
		out = append(out, classMetrics...)
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

	// CK family, produced for every language that declares classes. LCOM is
	// advisory by default (the original CK definition flags real designs
	// often); WMC/NOM/CBO/DIT warn high.
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
	if params := paramsNode(lang, fn); params != nil {
		st.Params = countParams(lang, params)
		st.BoolParams = countBoolParams(lang, src, params)
	}
	fillBodyMetrics(&st, lang, src, fn)
	return st
}

// fillBodyMetrics records the measures that need the function body: the
// callee count and the four control-flow scores. A declaration without a
// body (an interface method, a C prototype) contributes none of them.
func fillBodyMetrics(st *FuncStats, lang parser.LanguageID, src []byte, fn *sitter.Node) {
	body := fn.ChildByFieldName("body")
	if body == nil {
		return
	}
	st.Body = body
	st.FanOut = fanOut(lang, src, body)
	st.Cyclomatic, st.Cognitive, st.MaxNesting = irScore(lang, src, fn)
	st.NPath = irNPath(lang, src, fn)
}

// paramsNode resolves the parameter list of a function declaration. C/C++
// keep it inside the declarator chain, not on the function_definition itself.
func paramsNode(lang parser.LanguageID, fn *sitter.Node) *sitter.Node {
	if params := fn.ChildByFieldName("parameters"); params != nil {
		return params
	}
	if lang == parser.LangC || lang == parser.LangCpp {
		return cParamsNode(fn)
	}
	return nil
}

func funcName(src []byte, fn *sitter.Node) string {
	name := text(src, fn.ChildByFieldName("name"))
	if name == "" {
		// C/C++ function_definition has no name field: the name sits in the
		// declarator chain (cFuncName in c_adapter.go).
		name = cFuncName(fn, src)
	}
	if name == "" {
		return "<anonymous>"
	}
	return goReceiverName(src, fn, name)
}

// goReceiverName formats a Go method name as "T.Name" from the receiver
// field; other grammars have no receiver field and return the name as-is.
func goReceiverName(src []byte, fn *sitter.Node, name string) string {
	recv := fn.ChildByFieldName("receiver")
	if recv == nil {
		return name
	}
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
	return name
}

// cParamsNode finds the parameter_list of a C/C++ function_definition by
// walking its declarator chain to the function_declarator.
func cParamsNode(fn *sitter.Node) *sitter.Node {
	for d := fn.ChildByFieldName("declarator"); d != nil; {
		if d.Kind() == "function_declarator" {
			return d.ChildByFieldName("parameters")
		}
		d = d.ChildByFieldName("declarator")
	}
	return nil
}

// countParams counts declared parameters.
// Go: "a, b int" is two parameters, an unnamed "(int)" is one, a variadic
// declaration is one. Java: every formal_parameter / spread_parameter is one.
func countParams(lang parser.LanguageID, list *sitter.Node) int {
	// Whole-list grammars: every named child of the list is one parameter.
	switch lang {
	case parser.LangTS, parser.LangTSX, parser.LangJS:
		// JS/TS: identifier, required/optional_parameter, rest_pattern, ...
		return int(list.NamedChildCount())
	case parser.LangPy:
		// identifier, default_parameter, typed_parameter, splats, ...
		return int(list.NamedChildCount())
	case parser.LangRust:
		return countParamsRust(list)
	case parser.LangCS:
		// parameter, params-array parameter, arg-list ...
		return int(list.NamedChildCount())
	case parser.LangC, parser.LangCpp:
		// a parameter_declaration with one identifier is one parameter; an
		// abstract declaration (type only, unnamed) is one.
		return int(list.NamedChildCount())
	case parser.LangJava:
		return countParamsJava(list)
	}
	return countParamsGo(list)
}

// countParamsJava counts Java formal_parameter / spread_parameter children.
func countParamsJava(list *sitter.Node) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		switch list.NamedChild(i).Kind() {
		case "formal_parameter", "spread_parameter":
			n++
		}
	}
	return n
}

// countParamsRust counts parameter / self_parameter / variadic_parameter.
func countParamsRust(list *sitter.Node) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		switch list.NamedChild(i).Kind() {
		case "parameter", "self_parameter", "variadic_parameter":
			n++
		}
	}
	return n
}

// countParamsGo counts Go grouped declarations name-by-name: "a, b int" is
// two parameters, an unnamed "(int)" is one, a variadic declaration is one.
func countParamsGo(list *sitter.Node) int {
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
// Go grouped declarations count every name in the group: `a, b bool` is
// two bool parameters, not one.
func countBoolParams(lang parser.LanguageID, src []byte, list *sitter.Node) int {
	// Untyped JavaScript has no boolean annotations; TypeScript and the
	// Go/Java walk below each get their own counter (see countBoolParamsTS
	// and countBoolParamsClassic).
	switch lang {
	case parser.LangJS, parser.LangPy, parser.LangC, parser.LangCpp:
		// Untyped JavaScript and Python have no boolean annotations; C/C++
		// spells bool via stdbool or _Bool but declarations are type-only
		// positionally: count every parameter whose type is exactly bool.
		if lang == parser.LangC || lang == parser.LangCpp {
			return countBoolParamsC(src, list)
		}
		return 0
	case parser.LangTS, parser.LangTSX:
		return countBoolParamsTS(src, list)
	case parser.LangRust:
		return countBoolParamsRust(src, list)
	case parser.LangCS:
		return countBoolParamsCS(src, list)
	default:
		return countBoolParamsClassic(lang, src, list)
	}
}

// countBoolParamsC is the C/C++ arm: a parameter_declaration is bool when
// its type child (skipping const/volatile qualifiers) spells bool or
// _Bool. A C++ parameter declared `bool b` is a parameter_declaration with
// a primitive_type child "bool".
func countBoolParamsC(src []byte, list *sitter.Node) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		p := list.NamedChild(i)
		if p.Kind() != "parameter_declaration" && p.Kind() != "optional_parameter_declaration" && p.Kind() != "variadic_parameter_declaration" {
			continue
		}
		if cIsBoolType(src, p) {
			n++
		}
	}
	return n
}

// cIsBoolType reports whether the leading type of a C declaration node is
// bool/_Bool, looking past qualifiers at named children that are types.
func cIsBoolType(src []byte, decl *sitter.Node) bool {
	for i := uint(0); i < decl.NamedChildCount(); i++ {
		c := decl.NamedChild(i)
		switch c.Kind() {
		case "type_qualifier": // const/volatile: keep looking
			continue
		case "primitive_type":
			t := text(src, c)
			return t == "bool" || t == "_Bool"
		default:
			return false
		}
	}
	return false
}

// countBoolParamsRust is the Rust arm: a parameter is bool when its type
// field is the primitive_type "bool".
func countBoolParamsRust(src []byte, list *sitter.Node) int {
	return countBoolParamsTyped(src, list, "primitive_type", "bool")
}

// countBoolParamsCS is the C# arm: a parameter is bool when its type child
// is the predefined_type "bool".
func countBoolParamsCS(src []byte, list *sitter.Node) int {
	return countBoolParamsTyped(src, list, "predefined_type", "bool")
}

// countBoolParamsTyped counts parameters whose type field is the named type
// kind spelled as want. Rust and C# share the shape; only the kind of the
// type node differs.
func countBoolParamsTyped(src []byte, list *sitter.Node, kind, want string) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		p := list.NamedChild(i)
		if p.Kind() != "parameter" {
			continue
		}
		if t := p.ChildByFieldName("type"); t != nil && t.Kind() == kind && text(src, t) == want {
			n++
		}
	}
	return n
}

// countBoolParamsClassic is the Go/Java arm of the boolean-parameter count.
func countBoolParamsClassic(lang parser.LanguageID, src []byte, list *sitter.Node) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		n += classicParamCount(lang, src, list.NamedChild(i))
	}
	return n
}

// classicParamCount counts the bool parameters one node of a Go/Java
// parameter list declares, descending through grouping nodes.
func classicParamCount(lang parser.LanguageID, src []byte, node *sitter.Node) int {
	switch node.Kind() {
	case "parameter_declaration", "formal_parameter":
		if !classicBoolType(lang, src, node) {
			return 0
		}
		return classicGroupSize(node)
	case "spread_parameter":
		// Variadic bool stays uncounted (documented edge, same as before).
		return 0
	}
	n := 0
	for i := uint(0); i < node.NamedChildCount(); i++ {
		n += classicParamCount(lang, src, node.NamedChild(i))
	}
	return n
}

// classicBoolType reports whether a Go/Java parameter declaration is
// bool-typed: Go spells it the type_identifier "bool", Java boolean_type.
func classicBoolType(lang parser.LanguageID, src []byte, decl *sitter.Node) bool {
	for i := uint(0); i < decl.NamedChildCount(); i++ {
		c := decl.NamedChild(i)
		if lang == parser.LangJava {
			if c.Kind() == "boolean_type" {
				return true
			}
			continue
		}
		if c.Kind() == "type_identifier" && text(src, c) == "bool" {
			return true
		}
	}
	return false
}

// classicGroupSize counts the names a bool-typed declaration introduces:
// `a, b bool` is two bool parameters, not one. An unnamed parameter counts
// once, matching countParams.
func classicGroupSize(decl *sitter.Node) int {
	ids := 0
	for i := uint(0); i < decl.NamedChildCount(); i++ {
		if decl.NamedChild(i).Kind() == "identifier" {
			ids++
		}
	}
	if ids == 0 {
		return 1
	}
	return ids
}

// countBoolParamsTS is the TypeScript arm of the boolean-parameter count: the
// annotation is a type_annotation child of the parameter; a parameter is bool
// when that annotation is the type_identifier `boolean`.
//
// The annotation is read through its inner type node, not compared at the
// annotation itself: `a: boolean` parses as type_annotation → predefined_type
// → boolean, so a check for a `boolean` type_identifier never matched and every
// TypeScript function reported zero boolean parameters.
func countBoolParamsTS(src []byte, list *sitter.Node) int {
	n := 0
	for i := uint(0); i < list.NamedChildCount(); i++ {
		if tsTypeName(src, list.NamedChild(i)) == "boolean" {
			n++
		}
	}
	return n
}

// tsTypeName returns the spelled type of one TypeScript parameter, or "" when
// it has no annotation. The type field of a required/optional parameter is a
// type_annotation wrapper, so the inner type node is what names the type.
func tsTypeName(src []byte, param *sitter.Node) string {
	ann := param.ChildByFieldName("type")
	if ann == nil {
		return ""
	}
	if ann.Kind() == "type_annotation" {
		if inner := ann.NamedChild(0); inner != nil {
			return text(src, inner)
		}
		return ""
	}
	return text(src, ann)
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
	return len(callsOf(callSpellingFor(lang), src, body))
}

// callSpelling is how one grammar spells a call and a construction.
type callSpelling struct {
	callKind    string
	calleeField string
	newKind     string
}

// callSpellingFor resolves the node kinds a language uses for a call site and
// for a constructor invocation. Go and Java construct through call sites
// already covered, so newKind stays empty for them.
func callSpellingFor(lang parser.LanguageID) callSpelling {
	sp := callSpelling{callKind: "call_expression", calleeField: "function"}
	switch lang {
	case parser.LangJava:
		sp.callKind, sp.calleeField = "method_invocation", "name"
	case parser.LangCS:
		sp.callKind, sp.calleeField, sp.newKind = "invocation_expression", "function", "object_creation_expression"
	case parser.LangTS, parser.LangTSX, parser.LangJS:
		sp.newKind = "new_expression" // `new Foo()` has constructor field, no function field
	case parser.LangPy:
		sp.newKind = "call" // Python constructs via `Foo(...)`: same call kind
	}
	return sp
}

// callsOf collects the distinct call targets of one body. Function-literal
// invocations (Go) and constructor calls through a function field (Java) are
// not behavior-of-another-unit calls and are excluded.
func callsOf(sp callSpelling, src []byte, body *sitter.Node) map[string]bool {
	seen := map[string]bool{}
	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		if node.Kind() == sp.callKind {
			if callee := node.ChildByFieldName(sp.calleeField); isCallTarget(callee) {
				seen[flattenWS(text(src, callee))] = true
			}
		}
		if sp.newKind != "" && node.Kind() == sp.newKind {
			if ctor := node.ChildByFieldName("constructor"); ctor != nil {
				seen[flattenWS("new "+text(src, ctor))] = true
			}
		}
		for i := uint(0); i < node.NamedChildCount(); i++ {
			walk(node.NamedChild(i))
		}
	}
	walk(body)
	return seen
}

// isCallTarget reports whether a call's callee names another unit. A
// function literal is the enclosing unit's own body, not a call to one.
func isCallTarget(callee *sitter.Node) bool {
	if callee == nil {
		return false
	}
	switch callee.Kind() {
	case "function_literal", "arrow_function", "function_expression":
		return false
	}
	return true
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
	case parser.LangTS, parser.LangTSX:
		return jsLowerer{ts: true}
	case parser.LangJS:
		return jsLowerer{}
	case parser.LangPy:
		return pyLowerer{}
	case parser.LangC:
		return cLowerer{}
	case parser.LangCpp:
		return cLowerer{cpp: true}
	case parser.LangCS:
		return csLowerer{}
	case parser.LangRust:
		return rustLowerer{}
	default:
		return goLowerer{}
	}
}
