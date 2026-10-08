package metrics

import (
	"testing"

	"metricgate/internal/parser"
)

// These cases pin the scoring table in engine.go, including the deviations
// docs/metrics.md records on purpose. legacy_diff_test.go proves the IR path
// agrees with the retired walk; this file proves the retired walk's semantics
// are what the documentation says they are — the differential alone would
// happily preserve a shared mistake.

func TestEngineScoringRules(t *testing.T) {
	cases := []struct {
		name           string
		body           []Node
		cyc, cog, nest int
		npath          uint64
	}{
		{
			// McCabe starts every unit at 1; straight code adds nothing.
			name: "straight line", body: []Node{Group{Group{}}},
			cyc: 1, npath: 1,
		},
		{
			name: "if at top level", body: []Node{Branch{}},
			cyc: 2, cog: 1, nest: 1, // +1+0
			npath: 2, // NP(then)=1 + 1 fall-through
		},
		{
			name: "if inside if", body: []Node{Branch{Then: []Node{Branch{}}}},
			cyc: 3, cog: 3, nest: 2, // +1+0, then +1+1
			npath: 3, // 2×... no: NP(then)=2, +1 → 3
		},
		{
			// Deviation: the white paper charges an else-if a flat +1, so it
			// would be 3 here. Each link of the chain pays for its depth.
			name: "else-if chain nests", body: []Node{Branch{Alternate: Branch{}}},
			cyc: 3, cog: 3, nest: 2, // +1+0, then +1+1
			npath: 3, // NP(then) 1 + NP(else) 2 − 1 fall-through + 1 header
		},
		{
			// Deviation: a ternary is a cyclomatic path but no cognitive
			// decision, and it opens no nesting level.
			name: "ternary is cyclomatic only",
			body: []Node{Conditional{Test: []Node{Logical{Operands: []Node{Group{}, Group{}}}}}},
			cyc:  3, cog: 1, // the logical run inside the condition still counts
			npath: 3, // 2 branches + 1 logical operator in the test
		},
		{
			name: "loop header counts at loop depth",
			body: []Node{Loop{Init: []Node{Logical{Operands: []Node{Group{}, Group{}}}}}},
			cyc:  3, cog: 2, nest: 1, // +1+0 for the loop, +1 for the run inside it
			npath: 3, // NP(body) 1 + NP(header) 1 + 1
		},
		{
			// `default` opens no path, so only the first arm adds cyclomatic.
			name: "switch arms", body: []Node{Switch{Cases: []SwitchCase{{}, {IsDefault: true}}}},
			cyc: 2, cog: 1, nest: 1,
			npath: 2, // Σ NP(arms) = 1+1; default opens no extra path either way
		},
		{
			// Deviation: a handler is a decision, not another layer — it costs
			// a flat +1 however deep it sits and opens no cyclomatic path.
			name: "catch is flat",
			body: []Node{Loop{Body: []Node{Catch{Body: []Node{Catch{}}}}}},
			cyc:  2, cog: 3, nest: 1, // loop +1+0, each catch +1
			npath: 2, // the catches add no path: 1 + 1 loop-overhead
		},
		{
			name: "logical run folds", body: []Node{Logical{Operands: []Node{Group{}, Group{}, Group{}}}},
			cyc: 3, cog: 1, // one point for `a && b && c`, two paths joined
			npath: 1, // operators only score inside condition slots
		},
		{
			// `a && b || c` is two runs, so two points and two paths.
			name: "different operator starts a new run",
			body: []Node{Logical{Operands: []Node{Logical{Operands: []Node{Group{}, Group{}}}, Group{}}}},
			cyc:  3, cog: 2,
			npath: 1,
		},
		{
			name: "lambda costs nothing but nests", body: []Node{Lambda{Body: []Node{Branch{}}}},
			cyc: 2, cog: 2, nest: 2, // the if is one level deeper
			npath: 2, // deviation: the closure's paths multiply into the unit
		},
		{
			// A closure must not raise the level of the statements after it.
			name: "lambda leaves its siblings alone",
			body: []Node{Lambda{Body: []Node{Branch{}}}, Branch{}},
			cyc:  3, cog: 3, nest: 2, // +1+1 inside the closure, +1+0 after it
			npath: 4, // 2 (closure) × 2 (following if)
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := analyzeFunction(Function{Name: "f", Body: tc.body})
			assertReport(t, rep, tc.cyc, tc.cog, tc.nest, tc.npath)
		})
	}
}

// assertReport checks one engine verdict against the expected scoring table.
func assertReport(t *testing.T, rep FunctionReport, cyc, cog, nest int, npath uint64) {
	t.Helper()
	if int(rep.Cyclomatic) != cyc {
		t.Errorf("cyclomatic = %d, want %d", rep.Cyclomatic, cyc)
	}
	if int(rep.Cognitive) != cog {
		t.Errorf("cognitive = %d, want %d", rep.Cognitive, cog)
	}
	if int(rep.MaxNesting) != nest {
		t.Errorf("maxNesting = %d, want %d", rep.MaxNesting, nest)
	}
	if rep.NPath != npath {
		t.Errorf("npath = %d, want %d", rep.NPath, npath)
	}
}

// deviationCase is one end-to-end fixture: a snippet plus the numbers the
// documented deviations require it to produce.
type deviationCase struct {
	name           string
	lang           parser.LanguageID
	src            string
	cyc, cog, nest int
}

// deviationCases is a package-level table (not a local one) so the test that
// runs it stays short; the fixtures are long, the assertions are not.
var deviationCases = []deviationCase{
	{
		name: "go else-if chain nests",
		lang: parser.LangGo,
		// Three links, charged at depth 0, 1 and 2. The white paper would
		// report 3 cognitive points for this function.
		src: `package x

func Classify(a int) int {
	if a == 1 {
		return 1
	} else if a == 2 {
		return 2
	} else if a == 3 {
		return 3
	} else {
		return 4
	}
}
`,
		cyc: 4, cog: 6, nest: 3,
	},
	{
		name: "go labeled jumps and recursion are uncounted",
		lang: parser.LangGo,
		// Only the two loops and the if are charged: the labeled continue,
		// the labeled break and the recursive call add nothing (v0).
		src: `package x

func Sum(xs []int) int {
Loop:
	for {
		for _, x := range xs {
			if x > 0 {
				continue Loop
			}
		}
		break Loop
	}
	return Sum(xs)
}
`,
		cyc: 4, cog: 6, nest: 3,
	},
	{
		name: "java ternary and catch",
		lang: parser.LangJava,
		// The ternary adds a path but no cognitive point; each handler is a
		// flat +1 and neither opens a nesting level.
		src: `package x;

class T {
    int run(int a) {
        int r = a > 0 ? a : -a;
        try {
            r++;
        } catch (Exception e) {
            r--;
        } catch (Exception e) {
            r += 2;
        }
        return r;
    }
}
`,
		cyc: 2, cog: 2,
	},
	{
		name: "java lambda nests",
		lang: parser.LangJava,
		// The lambda itself is free; the if inside it is one level deeper.
		src: `package x;

class T {
    void run() {
        list.forEach(a -> {
            if (a > 0) {
                report(a);
            }
        });
    }
}
`,
		cyc: 2, cog: 2, nest: 2,
	},
}

// TestDocumentedDeviationsEndToEnd runs the same rules through the adapters, so
// a lowering change cannot quietly turn a documented deviation back into the
// white paper's number.
func TestDocumentedDeviationsEndToEnd(t *testing.T) {
	for _, tc := range deviationCases {
		t.Run(tc.name, func(t *testing.T) {
			fs := analyze(t, tc.lang, tc.src)
			if len(fs.Functions) != 1 {
				t.Fatalf("functions = %d, want 1", len(fs.Functions))
			}
			fn := fs.Functions[0]
			if fn.Cyclomatic != tc.cyc {
				t.Errorf("cyclomatic = %d, want %d", fn.Cyclomatic, tc.cyc)
			}
			if fn.Cognitive != tc.cog {
				t.Errorf("cognitive = %d, want %d", fn.Cognitive, tc.cog)
			}
			if fn.MaxNesting != tc.nest {
				t.Errorf("maxNesting = %d, want %d", fn.MaxNesting, tc.nest)
			}
		})
	}
}

// npathCases pins the NPATH scoring table against the two worked examples
// from the checkstyle reference page (Nejmeh 1988's metric, AT&T limit 200):
// seven independent ternaries multiply 2^7, and a loop over three 6-way
// switches adds 1 for the loop itself.
var npathCases = []struct {
	name  string
	lang  parser.LanguageID
	src   string
	fn    string // the declaration the case's expectation is about
	npath uint64
}{
	{
		name: "checkstyle example 1: seven ternaries multiply",
		lang: parser.LangJava,
		src: `class Example1 {
  void foo(int a, int b, int t, int m, int n) {
    print(a > b ? bar() : baz());
    print(t > 1 ? bar() : baz());
    print(t > 2 ? bar() : baz());
    print(t > 3 ? bar() : baz());
    print(t > 4 ? bar() : baz());
    print(t > 5 ? bar() : baz());
    print(m > n ? baz() : bar());
  } // foo
  abstract String baz();
  abstract void print(String s);
}`,
		fn:    "foo",
		npath: 128, // 2^7
	},
	{
		name: "checkstyle example 2: three switches inside a loop",
		lang: parser.LangJava,
		src: `class Example2 {
  void boo(int b) {
    for (int i = 0; i < b; i++) {
      switch (i) {
        case 1: print(f(i) && g(i) ? bar() : baz());
        default: print(f(i) || g(i) ? bar() : baz());
      }
      switch (i - 1) {
        case 1: print(f(i) && g(i) ? bar() : baz());
        default: print(f(i) || g(i) ? bar() : baz());
      }
      switch (i + 1) {
        case 1: print(f(i) && g(i) ? bar() : baz());
        default: print(f(i) || g(i) ? bar() : baz());
      }
    }
  } // boo
  abstract String baz();
  abstract void print(String s);
}`,
		fn:    "boo",
		npath: 217, // 6^3 + 1
	},
	{
		name: "go: logical operators in conditions multiply through",
		lang: parser.LangGo,
		src: `package x

func classify(a, b int) int {
	if a > 0 && b > 0 {
		return 1
	}
	for i := 0; i < a; i++ {
		if i > b || a > 100 {
			return i
		}
	}
	return 0
}`,
		// if: NP(then) 1 + ops 1 + 1 = 3; loop: NP(body) 3 + header 0 + 1 = 4.
		// Sequential statements multiply: 3 × 4 × 1 (the return) = 12.
		fn:    "classify",
		npath: 12,
	},
}

func TestNPathEndToEnd(t *testing.T) {
	for _, tc := range npathCases {
		t.Run(tc.name, func(t *testing.T) {
			fs := analyze(t, tc.lang, tc.src)
			var got uint64 = 1 << 63 // sentinel: missing function can never pass
			for _, fn := range fs.Functions {
				if fn.Name == tc.fn {
					got = fn.NPath
				}
			}
			if got == 1<<63 {
				t.Fatalf("function %q not found in %d functions", tc.fn, len(fs.Functions))
			}
			if got != tc.npath {
				t.Errorf("npath(%s) = %d, want %d", tc.fn, got, tc.npath)
			}
		})
	}
}
