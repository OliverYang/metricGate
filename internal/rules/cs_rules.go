package rules

import (
	"fmt"
	"strings"

	"metricgate/internal/model"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// CSRules is the built-in C# rule pack. Banned APIs live in policy
// (languages.csharp.banned) and are compiled by the engine, not here.
func CSRules() []*Rule {
	return []*Rule{
		csEmptyBlock(),
		todoCommentRule("[(comment) @c]"),
		CSUnknownDependency(),
		csChainDepth(),
		csTestNoAssert(),
	}
}

// csEmptyBlock flags empty control-flow blocks: branches (if consequence /
// alternative — alternative is a choice type, so the wildcard form is
// required), loop bodies (foreach included), the else of an if, and the
// try/catch bodies. A block holding only a comment still counts as empty.
func csEmptyBlock() *Rule {
	return newEmptyBlockRule(`
(if_statement consequence: (block) @blk)
(if_statement alternative: (_) @blk)
(for_statement body: (block) @blk)
(foreach_statement body: (block) @blk)
(while_statement body: (block) @blk)
(do_statement body: (block) @blk)
(try_statement body: (block) @blk)
(catch_clause body: (block) @blk)
(finally_clause (block) @blk)
`)
}

// csChainDepth flags call chains deeper than max_depth. In the C# grammar a
// chain a.B().C() nests invocation_expression nodes whose function field
// holds a member_access_expression; the depth walk crosses each member hop
// to count the calls, and nestedInChain keeps only the outermost link
// reportable.
func csChainDepth() *Rule {
	return chainDepthRule(`
(invocation_expression
  function: (_) @fn) @chain
`, func(ctx *MatchCtx) int {
		next := func(n *sitter.Node) *sitter.Node {
			fn := n.ChildByFieldName("function")
			if fn != nil && fn.Kind() == "member_access_expression" {
				fn = fn.ChildByFieldName("expression")
			}
			return fn
		}
		// The matched node is itself a call: count it, then each inner call
		// the chain descends into. a.B().C().D() = 4.
		depth := 1
		for cur := next(ctx.Captures["chain"]); cur != nil && cur.Kind() == "invocation_expression"; cur = next(cur) {
			depth++
		}
		return depth
	})
}

// CSUnknownDependency flags a C# using-directive outside the .NET base
// class library and the declared dependency set. C# has no lockfile the
// engine reads, so the allow list is whatever the policy declares; the
// System.* namespaces are always allowed.
func CSUnknownDependency() *Rule {
	return unknownDepRule(
		`[(using_directive (qualified_name) @path)]`,
		func(path string) string { return path },
		csBCLNamespace,
	)
}

// csBCLNamespace reports whether a namespace belongs to the .NET base class
// library, which needs no declaration. (Alias-qualified usings — global:: —
// and using static are out of scope for the warning.) The top-level namespace roots cover
// the BCL across .NET Framework / .NET (Core) versions; deeper namespaces
// (System.Collections.Generic) match by root.
func csBCLNamespace(path string) bool {
	root := path
	if i := strings.IndexByte(path, '.'); i >= 0 {
		root = path[:i]
	}
	switch root {
	case "System", "Microsoft", "Azure", "NuGet":
		return true
	}
	return false
}

// csTestNoAssert flags test methods that never call an assertion — the C#
// twin of goTestNoAssert (testsmells.org, FSE'20 UT test smell). The
// convention checked is xUnit/NUnit/MSTest naming: methods named Test*
// (or ending with Test/_Test) that contain no assertion call.
func csTestNoAssert() *Rule {
	return &Rule{
		ID:              "test-no-assert",
		Title:           "Test method contains no assertion (UT test smell)",
		DefaultSeverity: model.SevWarning,
		DefaultEnabled:  true,
		Query: `
(method_declaration
  name: (identifier) @name
  body: (block) @body) @testfunc
`,
		AnchorCapture: "testfunc",
		OnMatch: func(ctx *MatchCtx) *model.Finding {
			name := ctx.Text(ctx.Captures["name"])
			if !strings.HasPrefix(name, "Test") && !strings.HasPrefix(name, "test") &&
				!strings.HasSuffix(name, "Test") && !strings.HasSuffix(name, "_Test") {
				return nil
			}
			blk := ctx.Captures["body"]
			if csBlockHasAssertion(ctx, blk) {
				return nil
			}
			return &model.Finding{
				StartLine: int(blk.StartPosition().Row) + 1,
				EndLine:   int(blk.EndPosition().Row) + 1,
				Message:   fmt.Sprintf("test %q has no assertion; add Assert/Should*/Expect calls (including inside nested lambdas), otherwise the test cannot fail", name),
			}
		},
	}
}

// csBlockHasAssertion reports whether the block calls anything that looks
// like an assertion: Assert.*, Should*, Verify, Expect, Contains, Throws,
// or FluentAssertions-style Should. AST-only and naming-based by design
// (deterministic, no type info).
func csBlockHasAssertion(ctx *MatchCtx, blk *sitter.Node) bool {
	found := false
	walkNodes(blk, func(n *sitter.Node) {
		if found {
			return
		}
		if n.Kind() != "invocation_expression" {
			return
		}
		fn := n.ChildByFieldName("function")
		if fn == nil {
			return
		}
		name := strings.ToLower(csCalleeName(ctx, fn))
		for _, kw := range []string{"assert", "should", "expect", "verify", "throws", "contains"} {
			if strings.Contains(name, kw) {
				found = true
				return
			}
		}
	})
	return found
}

// csCalleeName extracts the plain name a call is made through: the name of
// a member_access chain, else the node's own text.
func csCalleeName(ctx *MatchCtx, fn *sitter.Node) string {
	if fn.Kind() == "member_access_expression" {
		if name := fn.ChildByFieldName("name"); name != nil {
			return ctx.Text(name)
		}
	}
	return ctx.Text(fn)
}
