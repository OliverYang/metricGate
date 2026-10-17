package metrics

import (
	"testing"

	"metricgate/internal/parser"
)

// A module's function in JavaScript and TypeScript is usually written as a
// binding, not a declaration: `export const handler = (x) => {…}`. The grammar
// makes that a variable_declarator holding an arrow_function, so no kind set
// can see it as a unit — and the same kind is also an inline closure that must
// NOT become one. Before this was fixed, a file of arrow functions reported
// zero functions, zero complexity, and passed every function-level threshold by
// being invisible; docs/metrics.md recorded the blindness as a design note.
//
// These tests pin both halves: the module-level binding is measured, and the
// closure inside it still folds into its enclosing unit.

func namedFunc(t *testing.T, st *FileStats, name string) FuncStats {
	t.Helper()
	for _, f := range st.Functions {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no function named %q measured in %d functions (%+v)", name, len(st.Functions), funcNames(st))
	return FuncStats{}
}

func funcNames(st *FileStats) []string {
	var out []string
	for _, f := range st.Functions {
		out = append(out, f.Name)
	}
	return out
}

func TestJSModuleArrowFunctionIsAUnit(t *testing.T) {
	// The shape from the bug report: a decision and a loop inside an arrow.
	st := analyze(t, parser.LangTS, `export const handler = (x: number) => { if (x>1) {return 1;} for (let i=0;i<3;i++){} return 0; }`)
	fn := namedFunc(t, st, "handler")
	// cc = 1 + if + for = 3; cognitive = if(1) + for(2) = 3, one level each.
	if fn.Cyclomatic != 3 {
		t.Errorf("cyclomatic = %d, want 3 (base + if + for)", fn.Cyclomatic)
	}
	if fn.Cognitive < 2 {
		t.Errorf("cognitive = %d, want >= 2 (if and for both score)", fn.Cognitive)
	}
	if fn.Params != 1 {
		t.Errorf("params = %d, want 1", fn.Params)
	}
	if fn.StartLine != 1 {
		t.Errorf("start line = %d, want 1", fn.StartLine)
	}
}

// The same body written as a declaration must measure the same way — otherwise
// the fix only moved the blindness to the other spelling.
func TestJSNamedAndArrowSpellingsAgree(t *testing.T) {
	body := `{ if (x>1) {return 1;} return 0; }`
	decl := analyze(t, parser.LangTS, `export function named(x: number) `+body)
	arrow := analyze(t, parser.LangTS, `export const named = (x: number) => `+body)
	if len(decl.Functions) != 1 || len(arrow.Functions) != 1 {
		t.Fatalf("function count: decl=%d arrow=%d, want 1 each", len(decl.Functions), len(arrow.Functions))
	}
	if decl.Functions[0].Cyclomatic != arrow.Functions[0].Cyclomatic {
		t.Errorf("cyclomatic differs by spelling: function %d vs arrow %d",
			decl.Functions[0].Cyclomatic, arrow.Functions[0].Cyclomatic)
	}
}

// An inline closure is not a unit of its own. This is the Go func_literal rule
// (engine.go's nesting bonus) and it must survive the fix: measuring every
// arrow would turn `a.map(f)` into two functions and double-count the body.
func TestJSInlineClosureStaysFolded(t *testing.T) {
	st := analyze(t, parser.LangTS, `export function outer(a: number[]) { return a.map((y) => { if (y) return 1; return 0; }); }`)
	if len(st.Functions) != 1 {
		t.Fatalf("functions = %v, want only outer", funcNames(st))
	}
	fn := st.Functions[0]
	if fn.Name != "outer" {
		t.Errorf("measured %q, want outer", fn.Name)
	}
	// The closure's if still scores inside outer: folded, not dropped.
	if fn.Cyclomatic < 2 {
		t.Errorf("outer cyclomatic = %d, want >= 2 (the closure's if must still count)", fn.Cyclomatic)
	}
}

// A local binding inside a function is a local, not module surface.
func TestJSLocalArrowBindingIsNotAUnit(t *testing.T) {
	st := analyze(t, parser.LangJS, `function outer() { const inner = () => 1; return inner; }`)
	if len(st.Functions) != 1 {
		t.Fatalf("functions = %v, want only outer", funcNames(st))
	}
}

// `export default () => …` has no name to report under. Inventing one would
// put a fake identifier into every snapshot of a file that has none.
func TestJSDefaultExportArrowIsNotNamed(t *testing.T) {
	st := analyze(t, parser.LangTS, `export default (x: number) => { if (x) return 1; return 0; }`)
	for _, f := range st.Functions {
		if f.Name == "" || f.Name == "<anonymous>" {
			t.Errorf("measured an unnamed unit %q; a default export has no name to report", f.Name)
		}
	}
}

func TestJSFunctionExpressionAndAsyncArrowAreUnits(t *testing.T) {
	fe := analyze(t, parser.LangJS, `const g = function(x) { if (x) return 1; return 0; }`)
	if len(fe.Functions) != 1 || fe.Functions[0].Name != "g" {
		t.Fatalf("function_expression: got %+v, want one unit named g", funcNames(fe))
	}
	if fe.Functions[0].Cyclomatic != 2 {
		t.Errorf("function_expression cyclomatic = %d, want 2", fe.Functions[0].Cyclomatic)
	}
	as := analyze(t, parser.LangJS, `export const load = async (url) => { if (url) return 1; return 0; }`)
	if len(as.Functions) != 1 || as.Functions[0].Name != "load" {
		t.Fatalf("async arrow: got %+v, want one unit named load", funcNames(as))
	}
	if as.Functions[0].Cyclomatic != 2 {
		t.Errorf("async arrow cyclomatic = %d, want 2", as.Functions[0].Cyclomatic)
	}
}

// `x => x + 1` keeps its single parameter in the `parameter` field as a bare
// pattern, with no formal_parameters list to count.
func TestJSSingleParamArrowWithoutParens(t *testing.T) {
	st := analyze(t, parser.LangJS, `const f = x => x + 1`)
	fn := namedFunc(t, st, "f")
	if fn.Params != 1 {
		t.Errorf("params = %d, want 1 (the grammar holds it in `parameter`, not a list)", fn.Params)
	}
	if fn.BoolParams != 0 {
		t.Errorf("bool params = %d, want 0 (unparenthesized arrows cannot carry a type annotation)", fn.BoolParams)
	}
}

// TypeScript's boolean-parameter smell must reach the arrow spelling too: the
// counter walks a formal_parameters list, and an arrow's list is the same node
// kind as a declaration's.
func TestJSArrowParamsCountBooleans(t *testing.T) {
	st := analyze(t, parser.LangTS, `export const load = async (url: string, ok: boolean) => { if (url) return 1; return 0; }`)
	fn := namedFunc(t, st, "load")
	if fn.Params != 2 {
		t.Errorf("params = %d, want 2", fn.Params)
	}
	if fn.BoolParams != 1 {
		t.Errorf("bool params = %d, want 1", fn.BoolParams)
	}
}

// The fix must apply to all three grammars that share the shape, and TSX is the
// one where a module-level arrow is the normal way to write a component.
func TestJSUnitRuleCoversJSAndBothTypeScriptGrammars(t *testing.T) {
	src := `const handler = (x) => { if (x) {return 1;} return 0; }`
	for _, lang := range []parser.LanguageID{parser.LangJS, parser.LangTS} {
		st := analyze(t, lang, src)
		if len(st.Functions) != 1 {
			t.Errorf("%s: functions = %d, want 1", lang, len(st.Functions))
		}
	}
	stx := analyze(t, parser.LangTSX, `export const Panel = (p: {n: number}) => { if (p.n) return <div/>; return null; }`)
	if len(stx.Functions) != 1 || stx.Functions[0].Name != "Panel" {
		t.Errorf("tsx: got %+v, want one unit named Panel", funcNames(stx))
	}
}

// A file of nothing but arrow functions must not measure as an empty file —
// that was the original symptom, and file-level metrics read the function list.
func TestJSAllArrowFileIsNotInvisible(t *testing.T) {
	st := analyze(t, parser.LangTS, `export const a = (x: number) => x + 1
export const b = (x: number) => x + 2
const c = (x: number) => x + 3`)
	if len(st.Functions) != 3 {
		t.Fatalf("functions = %v, want a, b and c", funcNames(st))
	}
}
