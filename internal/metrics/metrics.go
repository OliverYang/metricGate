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
	// function's MI. Bodies is retained for the census walk only.
	//
	// It is a slice because not every grammar wraps a body in one node:
	// VB.NET follows a method header with a flat run of `statement`
	// children and SystemVerilog splits its body across several
	// function_statement_or_null children. Measuring only the first would
	// silently under-count both fan-out and Halstead volume.
	Bodies []*sitter.Node `json:"-"`
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
		if prof.HasFunc(n) {
			fs.Functions = append(fs.Functions, measureFunc(lang, src, n))
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
		if len(fn.Bodies) == 0 {
			continue
		}
		hal := CountHalstead(src, fn.Bodies...)
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
func measureFunc(lang parser.LanguageID, src []byte, fn *sitter.Node) FuncStats {
	st := FuncStats{
		Name:      measureName(lang, src, fn),
		StartLine: int(fn.StartPosition().Row) + 1,
		EndLine:   int(fn.EndPosition().Row) + 1,
	}
	st.Length = st.EndLine - st.StartLine + 1
	if params := paramsNode(lang, fn); params != nil {
		st.Params = countParams(lang, params)
		st.BoolParams = countBoolParams(lang, src, params)
	} else if jsSingleParamNode(fn) != nil {
		// `x => x + 1` holds its one parameter in the `parameter` field as a
		// bare pattern with no list node to count. It cannot carry a type
		// annotation — TypeScript requires the parentheses for that — so the
		// boolean count is zero by construction, not by omission.
		st.Params = 1
	}
	fillBodyMetrics(&st, lang, src, fn)
	return st
}

// fillBodyMetrics records the measures that need the function body: the
// callee count and the four control-flow scores. A declaration without a
// body (an interface method, a C prototype) contributes none of them, and so
// does a language that registers neither a call spelling nor an IR lowerer.
func fillBodyMetrics(st *FuncStats, lang parser.LanguageID, src []byte, fn *sitter.Node) {
	bodies := declBodies(lang, fn)
	if len(bodies) == 0 {
		return
	}
	st.Bodies = bodies
	if sp := callSpellingFor(lang); sp != nil {
		st.FanOut = fanOut(*sp, src, bodies...)
	}
	lowerer := lowererFor(lang)
	if lowerer == nil {
		return
	}
	st.Cyclomatic, st.Cognitive, st.MaxNesting = irScore(lowerer, src, fn)
	st.NPath = irNPath(lowerer, src, fn)
}

// declBodies resolves the body containers of a declaration. Most grammars
// expose one node through the `body` field. SQL keeps it in a child instead.
// Verilog and VB.NET have no body node at all — their body is the run of
// statement children — so they return that run, because measuring only its
// first element would under-count fan-out and Halstead volume.
func declBodies(lang parser.LanguageID, fn *sitter.Node) []*sitter.Node {
	if body := fn.ChildByFieldName("body"); body != nil {
		return []*sitter.Node{body}
	}
	switch lang {
	case parser.LangSQL:
		if body := sqlBody(fn); body != nil {
			return []*sitter.Node{body}
		}
	case parser.LangVerilog:
		return verilogBodyNodes(fn)
	case parser.LangVB:
		return vbBodyNodes(fn)
	}
	return nil
}

// verilogBodyNodes returns the statement runs of a SystemVerilog declaration.
// The grammar has no body field. A function keeps its statements in
// function_statement_or_null children and a task in statement_or_null ones,
// both under the *_body_declaration child that also holds the header — except
// for a class constructor, which has no *_body_declaration at all and holds
// its statements directly.
func verilogBodyNodes(n *sitter.Node) []*sitter.Node {
	if direct := childKinds(n, "function_statement_or_null", "statement_or_null"); len(direct) > 0 {
		return direct
	}
	decl := firstChildKind(n, "function_body_declaration", "task_body_declaration")
	if decl == nil {
		return nil
	}
	return childKinds(decl, "function_statement_or_null", "statement_or_null")
}

// vbBodyNodes returns the statement run of a VB.NET method or constructor.
// The grammar has no body field at all: the header is followed by a flat list
// of `statement` children.
func vbBodyNodes(n *sitter.Node) []*sitter.Node {
	return childKinds(n, "statement")
}

// firstChildKind returns the first named child whose kind is one of kinds.
func firstChildKind(n *sitter.Node, kinds ...string) *sitter.Node {
	if n == nil {
		return nil
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		for _, k := range kinds {
			if c.Kind() == k {
				return c
			}
		}
	}
	return nil
}

// childKinds returns every named child whose kind is one of kinds, in source
// order.
func childKinds(n *sitter.Node, kinds ...string) []*sitter.Node {
	if n == nil {
		return nil
	}
	var out []*sitter.Node
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		for _, k := range kinds {
			if c.Kind() == k {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// paramsNode resolves the parameter list of a function declaration. C/C++
// keep it inside the declarator chain, not on the function_definition itself;
// SQL keeps it in the function_arguments child node.
func paramsNode(lang parser.LanguageID, fn *sitter.Node) *sitter.Node {
	if params := fn.ChildByFieldName("parameters"); params != nil {
		return params
	}
	switch lang {
	case parser.LangC, parser.LangCpp:
		return cParamsNode(fn)
	case parser.LangSQL:
		return sqlParamsNode(fn)
	case parser.LangVerilog:
		return verilogParamsNode(fn)
	}
	return nil
}

// funcName resolves the declared name of a function-like unit from the `name`
// field, with the C/C++ declarator chain as the fallback.
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

// measureName is the naming entry point of the measurement walk. It prefers
// the language's own name resolution and only then falls back to funcName, so
// a grammar that keeps the name in a child node (SQL) is named the same way
// its IR lowerer names it.
func measureName(lang parser.LanguageID, src []byte, fn *sitter.Node) string {
	if name := langDeclName(lang, src, fn); name != "" {
		return name
	}
	return funcName(src, fn)
}

// langDeclName is the per-language fallback for grammars that keep a
// declaration's name in a child node rather than a field. Returning "" means
// "this language has no such fallback", and the caller keeps looking.
func langDeclName(lang parser.LanguageID, src []byte, fn *sitter.Node) string {
	switch lang {
	case parser.LangJS, parser.LangTS, parser.LangTSX:
		// `const f = () => {…}` keeps its name on the declarator, and only for
		// the module-level spelling: an inline closure is anonymous because it
		// is not a unit of its own (isJSModuleFunc).
		return jsDeclName(src, fn)
	case parser.LangSQL:
		return sqlDeclName(src, fn)
	case parser.LangVerilog:
		return verilogDeclName(src, fn)
	case parser.LangVB:
		return vbDeclName(src, fn)
	}
	return ""
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

// fanOut counts the distinct callees invoked inside one function body —
// Yourdon & Constantine's fan-out. The callee identity is the whitespace-
// stripped spelling of the call target: Go uses the function field of a
// call_expression (identifier "Do" or selector "pkg.Do" — different
// targets stay distinct), Java uses the name identifier of a
// method_invocation (chained calls x.a().b() contribute a and b).
// Function-literal invocations (Go) and constructor calls (Java) are not
// behavior-of-another-unit calls and are excluded.
// fanOut counts the distinct call targets across every body node of a
// declaration. A declaration with a single body node is the common case; the
// slice covers the grammars that split a body across several statements.
func fanOut(sp callSpelling, src []byte, bodies ...*sitter.Node) int {
	return len(callsOf(sp, src, bodies...))
}

// callSpelling is how one grammar spells a call and a construction.
type callSpelling struct {
	callKind    string
	calleeField string
	newKind     string
}

// callSpellings maps a language to how its grammar spells a call and a
// construction. A language missing here has no call spelling: fan-out is not
// measured for it rather than being counted with Go's node kinds.
var callSpellings = map[parser.LanguageID]callSpelling{
	parser.LangGo:   {callKind: "call_expression", calleeField: "function"},
	parser.LangJava: {callKind: "method_invocation", calleeField: "name"},
	parser.LangCS: {callKind: "invocation_expression", calleeField: "function",
		newKind: "object_creation_expression"},
	parser.LangTS: {callKind: "call_expression", calleeField: "function",
		newKind: "new_expression"},
	parser.LangTSX: {callKind: "call_expression", calleeField: "function",
		newKind: "new_expression"},
	parser.LangJS: {callKind: "call_expression", calleeField: "function",
		newKind: "new_expression"},
	parser.LangPy: {callKind: "call_expression", calleeField: "function",
		newKind: "call"},
	parser.LangC:    {callKind: "call_expression", calleeField: "function"},
	parser.LangCpp:  {callKind: "call_expression", calleeField: "function"},
	parser.LangRust: {callKind: "call_expression", calleeField: "function"},
	// SQL spells a call as an `invocation` whose target is the `unit` field.
	// The grammar only fills `unit` for the `fn(a) FROM t` form; the plain
	// `fn(a)` form leaves it empty, so callsOf falls back to the child
	// object_reference (see sqlCallee).
	parser.LangSQL: {callKind: "invocation", calleeField: "unit"},
	// SystemVerilog's function call has no fields: the callee is the first
	// named child (simple_identifier) and the arguments follow. The method
	// call form is not registered here because the grammar only parses it for
	// a parenthesised receiver, which real code rarely writes.
	parser.LangVerilog: {callKind: "tf_call", calleeField: ""},
	// VB's call is an `invocation` whose target is the callee; construction is
	// `New X()`, a new_expression whose type field names the type.
	parser.LangVB: {callKind: "invocation", calleeField: "target",
		newKind: "new_expression"},
}

// callSpellingFor returns how a language spells a call site and a constructor
// invocation, or nil when the language registers no spelling. Go and Java
// construct through call sites already covered, so newKind stays empty there.
func callSpellingFor(lang parser.LanguageID) *callSpelling {
	sp, ok := callSpellings[lang]
	if !ok {
		return nil
	}
	return &sp
}

// callsOf collects the distinct call targets of one body. Function-literal
// invocations (Go) and constructor calls through a function field (Java) are
// not behavior-of-another-unit calls and are excluded.
func callsOf(sp callSpelling, src []byte, bodies ...*sitter.Node) map[string]bool {
	seen := map[string]bool{}
	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		if node.Kind() == sp.callKind {
			if callee := callCallee(sp, node); isCallTarget(callee) {
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
	for _, body := range bodies {
		if body != nil {
			walk(body)
		}
	}
	return seen
}

// callCallee resolves a call site's target. The field named by the spelling is
// the usual answer; SQL's invocation only fills `unit` for one of its two
// syntactic forms, so a missing field falls back to the first named child,
// which is the object_reference in both.
func callCallee(sp callSpelling, node *sitter.Node) *sitter.Node {
	if sp.calleeField != "" {
		if callee := node.ChildByFieldName(sp.calleeField); callee != nil {
			return callee
		}
	}
	switch sp.callKind {
	case "invocation":
		// SQL only fills `unit` for the `fn(a) FROM t` form; VB always fills
		// `target`. The first named child is the callee in both.
		if node.NamedChildCount() > 0 {
			return node.NamedChild(0)
		}
	case "tf_call":
		// SystemVerilog has no fields on a call: the callee is the leading
		// simple_identifier and the argument list follows.
		if node.NamedChildCount() > 0 {
			return node.NamedChild(0)
		}
	}
	return nil
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
func irScore(lowerer nodeLowerer, src []byte, fn *sitter.Node) (cyc, cog, maxNest int) {
	rep := analyzeFunction(lowerer.lowerFunc(fn, src))
	return int(rep.Cyclomatic), int(rep.Cognitive), int(rep.MaxNesting)
}

// irNPath lowers one declaration and returns its NPATH path count.
func irNPath(lowerer nodeLowerer, src []byte, fn *sitter.Node) uint64 {
	return analyzeFunction(lowerer.lowerFunc(fn, src)).NPath
}

// langLowerers maps a language to its IR lowerer. A language missing here has
// no control-flow lowering: it contributes no cyclomatic, cognitive, nesting
// or NPATH score rather than being scored with Go's node kinds. An omission
// is a red test in TestLanguageParity, not a silent degradation.
var langLowerers = map[parser.LanguageID]nodeLowerer{
	parser.LangGo:      goLowerer{},
	parser.LangJava:    javaLowerer{},
	parser.LangTS:      jsLowerer{ts: true},
	parser.LangTSX:     jsLowerer{ts: true},
	parser.LangJS:      jsLowerer{},
	parser.LangPy:      pyLowerer{},
	parser.LangC:       cLowerer{},
	parser.LangCpp:     cLowerer{cpp: true},
	parser.LangCS:      csLowerer{},
	parser.LangRust:    rustLowerer{},
	parser.LangSQL:     sqlLowerer{},
	parser.LangVerilog: verilogLowerer{},
	parser.LangVB:      vbLowerer{},
}

// lowererFor returns the IR lowerer of a language, or nil when it has none.
func lowererFor(lang parser.LanguageID) nodeLowerer {
	return langLowerers[lang]
}
