package metrics

import (
	"testing"

	"metricgate/internal/parser"
)

// iter-30 census: which spellings of behaviour a grammar puts in a file, and
// which of them metricGate measures as a unit.
//
// JavaScript and TypeScript needed a structural fix (760f498) because a module's
// function is usually a *binding*, not a declaration, and no kind set can see a
// binding. The first question here is whether the other languages have the same
// hole. They do not, and the reason is worth keeping: in Java, C++, C#, Python
// and Rust, code written as `name = <closure>` at file scope is a closure, and
// metricGate's standing rule is that closures fold into the enclosing unit — the
// rule Go's func_literal follows and that `var helper = func(){}` obeys (case
// TestFileScopeClosureBindingsAreNotUnits, which pins Go alongside them so the
// consistency itself is under test, not just the foreign languages).
//
// The second question found a real gap, and it is not the one expected: C#
// property accessors and Java initializer blocks hold decision points that land
// in NO unit at all. They are not "invisible bindings" — they are branches
// counted nowhere, which is a different defect with a different fix. Those cases
// pin today's behaviour so the gap cannot widen unnoticed, and so nobody closes
// it without touching this file and docs/metrics.md.

// Every language's declaration spelling must measure as a unit. If a kind set
// stops matching one of these, the file it lives in loses its function-level
// thresholds entirely.
func TestDeclarationSpellingsAreUnitsPerLanguage(t *testing.T) {
	cases := []struct {
		lang parser.LanguageID
		name string
		src  string
		want int
	}{
		{parser.LangC, "plain function", `int helper(int x) { if (x > 1) { return 1; } return 0; }`, 1},
		{parser.LangCpp, "free function", `int helper(int x) { if (x > 1) { return 1; } return 0; }`, 1},
		{parser.LangCpp, "out-of-class definition", "class Foo { public: int bar(int x); };\nint Foo::bar(int x) { if (x > 1) { return 1; } return 0; }", 1},
		{parser.LangCpp, "operator()", `struct F { int operator()(int x) { if (x > 1) { return 1; } return 0; } };`, 1},
		{parser.LangCpp, "template function", `template <class T> T twice(T x) { if (x > 1) { return x + x; } return 0; }`, 1},
		{parser.LangCS, "method", `class C { int Helper(int x) { if (x > 1) { return 1; } return 0; } }`, 1},
		{parser.LangCS, "expression-bodied method", `class C { int Helper(int x) => x > 1 ? 1 : 0; }`, 1},
		{parser.LangCS, "async method", `class C { async Task<int> Go(int x) { if (x > 1) { return 1; } return 0; } }`, 1},
		{parser.LangJava, "method", `class C { int helper(int x) { if (x > 1) { return 1; } return 0; } }`, 1},
		{parser.LangPy, "def", "def helper(x):\n    if x > 1:\n        return 1\n    return 0\n", 1},
		{parser.LangRust, "fn", `fn helper(x: i32) -> i32 { if x > 1 { return 1 } 0 }`, 1},
		{parser.LangRust, "impl method", `struct S; impl S { fn helper(&self, x: i32) -> i32 { if x > 1 { return 1 } 0 } }`, 1},
		{parser.LangGo, "func", "package p\n\nfunc helper(x int) int { if x > 1 { return 1 }\n return 0 }\n", 1},
	}
	for _, c := range cases {
		st := analyze(t, c.lang, c.src)
		if len(st.Functions) != c.want {
			t.Errorf("%s %s: FUNCS=%d names=%v, want %d", c.lang, c.name, len(st.Functions), funcNames(st), c.want)
		}
	}
}

// A C declaration whose initialiser is a closure is not a function definition,
// and the C grammar agrees: `int (*helper)(int)` declares a pointer, and the
// body — if the file writes one at all — belongs to a function_definition.
// This case exists because the probe asked "does C have the JS hole" and the
// answer is that C has no function-valued expression to bind.
func TestCFunctionPointerDeclarationIsStillOneUnit(t *testing.T) {
	st := analyze(t, parser.LangC, `
int helper(int x);
int helper(int x) { if (x > 1) { return 1; } return 0; }
`)
	if len(st.Functions) != 1 {
		t.Fatalf("FUNCS=%d names=%v, want 1 (prototype + definition is one unit)", len(st.Functions), funcNames(st))
	}
	if namedFunc(t, st, "helper").Cyclomatic != 2 {
		t.Errorf("cyclomatic = %d, want 2 (base + if)", namedFunc(t, st, "helper").Cyclomatic)
	}
}

// The rule metricGate applies everywhere: a closure bound to a name is still a
// closure. Go is in this table on purpose — if someone "fixes" Java to measure
// its lambda fields without touching Go, the two languages part ways and the
// site says one thing about closures and another about fields.
func TestFileScopeClosureBindingsAreNotUnits(t *testing.T) {
	cases := []struct {
		lang parser.LanguageID
		name string
		src  string
	}{
		{parser.LangGo, "var f = func(){}", "package p\n\nvar helper = func(x int) int { if x > 1 { return 1 }; return 0 }\n"},
		{parser.LangJava, "Function field", "import java.util.function.Function;\nclass C { static final Function<Integer,Integer> HELPER = x -> { if (x > 1) { return 1; } return 0; }; }\n"},
		{parser.LangCS, "Func field", "using System;\nclass C { static Func<int,int> Helper = (x) => { if (x > 1) { return 1; } return 0; }; }\n"},
		{parser.LangCpp, "auto lambda", `auto helper = [](int x) { if (x > 1) { return 1; } return 0; };`},
		{parser.LangCpp, "std::function binding", "#include <functional>\nconst std::function<int(int)> helper = [](int x) { if (x > 1) { return 1; } return 0; };\n"},
		{parser.LangPy, "lambda assignment", "helper = lambda x: 1 if x > 1 else 0\n"},
		{parser.LangRust, "const fn pointer", "const HELPER: fn(i32) -> i32 = |x| { if x > 1 { return 1 } 0 };\n"},
	}
	for _, c := range cases {
		st := analyze(t, c.lang, c.src)
		if len(st.Functions) != 0 {
			t.Errorf("%s %s: FUNCS=%d names=%v, want 0 — a bound closure folds into its enclosing scope, it is not a unit of its own",
				c.lang, c.name, len(st.Functions), funcNames(st))
		}
	}
}

// The gap found by the iter-30 census, pinned as a gap.
//
// A C# property accessor is a real method to the compiler, and a Java static
// initializer is code that runs. Both hold decision points. Neither is in a
// function's body, so no unit counts them: the `if` in a getter and the loop in
// a setter contribute to no cyclomatic, cognitive, npath or nesting number
// anywhere in the snapshot, and a file written entirely as properties reports
// zero functions — which suppresses even the maintainability index, since
// AnalyzeFile sets MI to 0 when there are no units and engine.go only emits a
// maintainability snapshot when MI > 0.
//
// This is NOT the JS bug and must not be fixed by copying isJSModuleFunc: the
// branches here are not in a binding that could be promoted, they are in a
// construct no unit contains at all. Fixing it means adding accessor_declaration
// and static_initializer to the unit census (and deciding what name they get,
// since accessors are anonymous), which is a threshold-moving change to every
// C# and Java file in every existing snapshot — a policy decision, not a bugfix.
//
// Verified, not asserted: putting "accessor_declaration" into csFuncKinds makes
// this test fail with names=[get set] — the accessors become units named after
// their keywords, which is the fake-identifier problem the JS fix refused for
// `export default`. A real fix has to name them `P.get` first.
func TestAccessorAndInitializerBranchesAreCountedNowhere(t *testing.T) {
	cases := []struct {
		lang parser.LanguageID
		name string
		src  string
	}{
		{parser.LangCS, "getter with if and setter with loop", `class C { int P { get { if (true) { } } set { for(;;) { } } } }`},
		{parser.LangCS, "top-level statements", "using System; Console.WriteLine(1); if (true) { }\n"},
		{parser.LangJava, "static initializer", "class C { static { if (true) { } } }\n"},
		{parser.LangJava, "instance initializer", "class C { { if (true) { } } }\n"},
	}
	for _, c := range cases {
		st := analyze(t, c.lang, c.src)
		if len(st.Functions) != 0 {
			t.Errorf("%s %s: FUNCS=%d names=%v — the census expected the gap to still exist; if this now measures, update docs/metrics.md and re-check every Java/C# threshold",
				c.lang, c.name, len(st.Functions), funcNames(st))
		}
		if st.MI != 0 {
			t.Errorf("%s %s: MI=%v with no units; MI is only defined over measured functions", c.lang, c.name, st.MI)
		}
	}
}

// The same class written with a method next to the initializer must still
// measure the method — the gap is about the initializer, not about the file
// becoming invisible the way the JS bug did.
func TestInitializerGapDoesNotHideSiblingMethods(t *testing.T) {
	st := analyze(t, parser.LangJava, "class C { static { if (true) { } } int f() { return 1; } }\n")
	if len(st.Functions) != 1 {
		t.Fatalf("FUNCS=%d names=%v, want 1 (the method is measured even though the static block is not)", len(st.Functions), funcNames(st))
	}
	namedFunc(t, st, "f")
}
