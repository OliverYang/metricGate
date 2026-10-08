package rules

import (
	"fmt"
	"strings"

	"metricgate/internal/model"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// goTestNoAssert flags test functions that never call an assertion — the
// Go twin of javaTestNoAssert (testsmells.org, FSE'20 UT test smell): the
// test exercises code but verifies nothing, so it cannot fail. Detection
// is existence-based over the whole function subtree, so assertions inside
// t.Run subtest closures count. panic() counts as a deliberate failure
// assertion; go vet enforces the Test*/Benchmark* signature.
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
			if !strings.HasPrefix(name, "Test") && !strings.HasPrefix(name, "Benchmark") {
				return nil
			}
			// TestMain(m *testing.M) is a harness, not a test: require the
			// testing.T/B parameter go vet mandates for the prefix.
			params := ctx.Text(ctx.Captures["params"])
			if !strings.Contains(params, "*testing.T") && !strings.Contains(params, "*testing.B") {
				return nil
			}
			blk := ctx.Captures["body"]
			if goBlockHasAssertion(ctx, blk) {
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

// goBlockHasAssertion reports whether the block calls anything that looks
// like an assertion; the vocabulary lives in isAssertionCallee. AST-only
// and naming-based by design (deterministic, no type info). t.Parallel and
// t.Log do NOT count.
func goBlockHasAssertion(ctx *MatchCtx, blk *sitter.Node) bool {
	found := false
	walkNodes(blk, func(n *sitter.Node) {
		if found || n.Kind() != "call_expression" {
			return
		}
		if fn := n.ChildByFieldName("function"); fn != nil && isAssertionCallee(ctx, fn) {
			found = true
		}
	})
	return found
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
