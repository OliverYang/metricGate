package rules

import (
	"testing"

	"metricgate/internal/parser"
)

// The Go test-no-assert family lives in its own file: the rule has been retuned
// three times (name-only, then helper capability, then dropping benchmarks),
// each time against measurements from real suites, and each retune needs its own
// fixture rather than an ever-growing shared source string.

// goTestsSrc is the vocabulary floor: which call shapes the rule counts as an
// assertion, and which functions it refuses to look at at all.
const goTestsSrc = `package t

import "testing"

func helper(x int) int { return x }

func TestNoAssert(t *testing.T) {
	cases := []struct {
		name  string
		input []int
	}{
		{"empty", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := helper(len(c.input))
			t.Log("got", got)
		})
	}
}

func TestWithError(t *testing.T) {
	if got := helper(1); got != 1 {
		t.Errorf("helper(1) = %d, want 1", got)
	}
}

func TestWithRequire(t *testing.T) {
	require(t, helper(2) == 2)
}

func require(t *testing.T, ok bool) { t.Helper(); if !ok { t.Fatal("bad") } }

func TestWithPanic(t *testing.T) {
	defer func() { recover() }()
	panic("expected")
}

func TestMain(m *testing.M) { }

func helperNotATest(x int) int { return x }
`

func TestGoTestNoAssert(t *testing.T) {
	fs := findingsByRule(runRules(t, parser.LangGo, goTestsSrc), "test-no-assert")
	if len(fs) != 1 {
		t.Fatalf("findings = %d, want 1 (only TestNoAssert; got %+v)", len(fs), fs)
	}
	if !contains(fs[0].Message, "TestNoAssert") {
		t.Errorf("message %q should name the offending test", fs[0].Message)
	}
}

// A project's own helper is an assertion when it is handed the *testing.T:
// that is the capability to fail the test, and it is how most real Go suites
// assert. Without this the rule flags a quarter of a working test file
// (this repository's own suite was 8-for-8 false positives), and a rule that
// cries wolf trains people to ignore the ones that are real.
const goHelperAssertSrc = `package t

import "testing"

func wantEqual(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}

func TestUsesHelper(t *testing.T) {
	wantEqual(t, 1, 1)
}

func TestUsesHelperOnSubtest(t *testing.T) {
	t.Run("inner", func(t *testing.T) {
		wantEqual(t, 2, 2)
	})
}

func TestHelperWithoutT(t *testing.T) {
	// describe(1) cannot fail the test: it never received the means to.
	describe(1)
}

func describe(x int) int { return x }

func TestOnlyLogs(t *testing.T) {
	t.Log("nothing checked")
}

func TestParallelOnly(t *testing.T) {
	t.Parallel()
}
`

func TestGoTestNoAssertHelperCapability(t *testing.T) {
	fs := findingsByRule(runRules(t, parser.LangGo, goHelperAssertSrc), "test-no-assert")
	var names []string
	for _, f := range fs {
		names = append(names, f.Message)
	}
	// Three flagged: the helper that never received T, the log-only test, and
	// the test whose only statement is t.Parallel().
	if len(fs) != 3 {
		t.Fatalf("findings = %d, want 3 (%v)", len(fs), names)
	}
	for _, want := range []string{"TestHelperWithoutT", "TestOnlyLogs", "TestParallelOnly"} {
		if !anyContains(names, want) {
			t.Errorf("%s should be flagged; got %v", want, names)
		}
	}
	for _, ok := range []string{"TestUsesHelper", "TestUsesHelperOnSubtest"} {
		if anyContains(names, ok) {
			t.Errorf("%s must not be flagged (it hands *testing.T to a helper); got %v", ok, names)
		}
	}
}

// A differently named parameter is still the test's own value: matching the
// type, not the letter "t", is what keeps tb/tx/worker-style suites from being
// false positives.
func TestGoTestNoAssertNamesAnyTParam(t *testing.T) {
	src := `package t

import "testing"

func check(tb *testing.T, ok bool) { tb.Helper(); if !ok { tb.Error("bad") } }

func TestRenamedParam(t *testing.T) {
	check(t, true)
}

func TestPassesAnAlias(t *testing.T) {
	tt := t
	check(tt, true)
}
`
	fs := findingsByRule(runRules(t, parser.LangGo, src), "test-no-assert")
	// One flagged: the alias. The rule matches the *testing.T parameter by
	// name, so a differently named parameter works and an aliased handle does
	// not — chasing aliases would mean data flow, which this rule has
	// deliberately never done. The alias case is the documented floor of the
	// rule's precision, recorded here so a future relaxation has something to
	// compare against.
	if len(fs) != 1 {
		t.Fatalf("findings = %d, want 1 (%+v)", len(fs), fs)
	}
	if !contains(fs[0].Message, "TestPassesAnAlias") {
		t.Errorf("message %q should name the test that passes an alias", fs[0].Message)
	}
}

// Benchmarks are out of scope, and this test exists because the scope used to
// include them: replaying dev-cockpit's 289 commits under the iter-29 rule left
// 233 findings, and 18 of them came from one Benchmark function
// (internal/filesystem/quickopen_bench_test.go:45) told to add `t.Error` to a
// signature whose parameter is `b`. Across a five-package corpus the same shape
// accounted for 14 of 19 survivors. A benchmark reports ns/op; not asserting is
// what makes it work, so there is no finding here to miss. TestMain is excluded
// by the signature gate and is kept here so both exclusions are stated in one
// place.
func TestGoTestNoAssertIgnoresBenchmarks(t *testing.T) {
	src := `package t

import (
	"os"
	"testing"
)

func BenchmarkQuickOpenQuery(b *testing.B) {
	ix := NewIndex(items)
	for i := 0; i < b.N; i++ {
		ix.query("terminal")
	}
}

func BenchmarkSizes(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_ = len(items)
	}
}

func TestMain(m *testing.M) {
	setup()
	os.Exit(m.Run())
}
`
	fs := findingsByRule(runRules(t, parser.LangGo, src), "test-no-assert")
	if len(fs) != 0 {
		t.Fatalf("findings = %d, want 0: benchmarks and TestMain cannot fail by design (%+v)", len(fs), fs)
	}
}

func anyContains(names []string, needle string) bool {
	for _, n := range names {
		if contains(n, needle) {
			return true
		}
	}
	return false
}
