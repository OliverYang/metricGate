package rules

import (
	"fmt"
	"strings"

	"metricgate/internal/model"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// goTestNoAssert flags test functions that cannot fail — the Go twin of
// javaTestNoAssert (testsmells.org, FSE'20 UT test smell): the test exercises
// code but verifies nothing, so a regression walks straight through it.
// Detection is existence-based over the whole function subtree, so assertions
// inside t.Run subtest closures count. panic() counts as a deliberate failure
// assertion, and so does handing the test's *testing.T to a helper.
//
// Scope is Test* only. A Benchmark* cannot fail by design — it reports
// ns/op and the toolchain treats a benchmark that never calls b.Error as
// working. Iter-29's recalibration replayed 289 real commits and found that
// including benchmarks produced 18 of the 233 surviving findings, each one
// telling the author to add `t.Error` to a function whose parameter is `b`
// (dev-cockpit's BenchmarkQuickOpenQuery, 18 commits in a row). A warning that
// is unactionable on 100% of its occurrences is not a finding, and the corpus
// suite showed the same shape: 14 of 19 survivors were benchmarks.
// A benchmark that measures nothing is a real smell, but it is a different
// one ("the setup never runs") and would need its own rule.
func goTestNoAssert() *Rule {
	return &Rule{
		ID:              "test-no-assert",
		Title:           "Test function contains no assertion (UT test smell)",
		DefaultSeverity: model.SevWarning,
		DefaultEnabled:  true,
		Query: `
(function_declaration
  name: (identifier) @name
  parameters: (parameter_list) @params
  body: (block) @body) @testfunc
`,
		AnchorCapture: "testfunc",
		OnMatch: func(ctx *MatchCtx) *model.Finding {
			name := ctx.Text(ctx.Captures["name"])
			if !strings.HasPrefix(name, "Test") {
				return nil
			}
			// TestMain(m *testing.M) is a harness, not a test: require the
			// *testing.T parameter go vet mandates for the Test* prefix.
			params := ctx.Text(ctx.Captures["params"])
			if !strings.Contains(params, "*testing.T") {
				return nil
			}
			blk := ctx.Captures["body"]
			if goBlockHasAssertion(ctx, blk, testingTParam(params)) {
				return nil
			}
			return &model.Finding{
				StartLine: int(blk.StartPosition().Row) + 1,
				EndLine:   int(blk.EndPosition().Row) + 1,
				Message:   fmt.Sprintf("test %q has no assertion; add t.Error/t.Fatal/assert/require (including inside t.Run closures), otherwise the test cannot fail", name),
			}
		},
	}
}

// goBlockHasAssertion reports whether the block contains anything that can
// fail the test. Two ways qualify, and both are naming-based on purpose
// (deterministic, no type info):
//
//  1. a call whose own name is in the assertion vocabulary — isAssertionCallee;
//  2. a call that receives the test's *testing.T value as an argument, which is
//     how a project's own helper gets the power to fail the test.
//
// The second tier is what makes the rule usable on a real codebase: a test that
// reads `wantVis(t, sym, "package")` does assert, and a rule that says
// otherwise is not finding a smell but teaching people to distrust the finding.
// The repo's own test suite was 8-for-8 false positives before this existed.
//
// It is a *capability* test, not a proof: a helper that takes *testing.T and
// only calls t.Log will hide the smell. That is the same trust the rule already
// places in `require(t, cond)`, and it is the direction the rule should err in,
// because the cost of a false positive here is a warning nobody reads.
//
// t.Parallel, t.Log and t.Run do NOT count on their own: they are receivers,
// and a receiver is never an argument to itself.
func goBlockHasAssertion(ctx *MatchCtx, blk *sitter.Node, testName string) bool {
	found := false
	walkNodes(blk, func(n *sitter.Node) {
		if found || n.Kind() != "call_expression" {
			return
		}
		if fn := n.ChildByFieldName("function"); fn != nil && isAssertionCallee(ctx, fn) {
			found = true
			return
		}
		if args := n.ChildByFieldName("arguments"); args != nil && passesValue(ctx, args, testName) {
			found = true
		}
	})
	return found
}

// passesValue reports whether an argument list holds the bare identifier name.
// It matches the argument itself, not an expression that mentions it, so
// `run(t)` counts and `run(1)` does not.
func passesValue(ctx *MatchCtx, args *sitter.Node, name string) bool {
	if name == "" {
		return false
	}
	for i := uint(0); i < args.NamedChildCount(); i++ {
		arg := args.NamedChild(i)
		if arg.Kind() == "identifier" && ctx.Text(arg) == name {
			return true
		}
	}
	return false
}

// testingTParam is the name a test function's *testing.T parameter goes by,
// which is almost always t and sometimes tb or tx. Returning "" means the
// signature has no such parameter, so no call can pass it. A *testing.B is not
// looked for: benchmarks are out of scope (see goTestNoAssert).
func testingTParam(params string) string {
	for _, field := range strings.Split(strings.Trim(params, "()"), ",") {
		field = strings.TrimSpace(field)
		if !strings.HasSuffix(field, "*testing.T") {
			continue
		}
		name := strings.TrimSpace(strings.TrimSuffix(field, "*testing.T"))
		// A variadic or unnamed parameter has no identifier to match against.
		if name == "" || strings.HasPrefix(name, "...") {
			return ""
		}
		return name
	}
	return ""
}

// isAssertionCallee matches the Go assertion vocabulary against a call's
// function node: any callee whose name contains assert/require, a must
// prefix, exact testing.{Error,Errorf,Fatal,Fatalf,Fail,FailNow}, or panic().
func isAssertionCallee(ctx *MatchCtx, fn *sitter.Node) bool {
	name := ctx.Text(fn)
	if fn.Kind() == "selector_expression" {
		// strip the receiver: t.Errorf -> Errorf
		if sel := fn.ChildByFieldName("field"); sel != nil {
			name = ctx.Text(sel)
		}
	}
	lower := strings.ToLower(name)
	return strings.Contains(lower, "assert") || strings.Contains(lower, "require") ||
		strings.HasPrefix(lower, "must") ||
		name == "Error" || name == "Errorf" || name == "Fatal" || name == "Fatalf" ||
		name == "Fail" || name == "FailNow" || name == "panic"
}
