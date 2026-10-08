package rules

import (
	"fmt"
	"strings"

	"metricgate/internal/model"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// JavaRules is the built-in Java rule pack. Banned APIs live in policy
// (languages.java.banned) and are compiled by the engine, not here.
func JavaRules() []*Rule {
	return []*Rule{
		printStackTrace(),
		javaEmptyBlock(),
		silentCatch(),
		JavaUnknownDependency(),
		todoCommentRule("[(line_comment) @c (block_comment) @c]"),
		javaChainDepth(),
		javaTestNoAssert(),
	}
}

// javaChainDepth flags message chains deeper than max_depth. In the Java
// grammar a chain x.a().b() is nested method_invocations (the object field
// holds the rest of the chain); the query matches every invocation and
// OnMatch walks the object field down to measure depth, reporting the
// outermost node of offending chains only.
func javaChainDepth() *Rule {
	return chainDepthRule(`
(method_invocation) @chain
`, func(ctx *MatchCtx) int {
		node := ctx.Captures["chain"]
		depth := 0
		cur := node
		for cur != nil && cur.Kind() == "method_invocation" {
			depth++
			cur = cur.ChildByFieldName("object")
		}
		return depth
	})
}

// javaTestNoAssert flags test methods that never call an assertion — the
// UT test smell (testsmells.org, FSE'20 test-smell catalogue): the test
// exercises code but verifies nothing, so it cannot fail. Detection is
// existence-based: the method body must reference no assert* invocation
// and no fail(); a throw or expected-exception comment is a legitimate
// alternative and is honored.
func javaTestNoAssert() *Rule {
	return &Rule{
		ID:              "test-no-assert",
		Title:           "Test method contains no assertion (UT test smell)",
		DefaultSeverity: model.SevWarning,
		DefaultEnabled:  true,
		Query: `
(method_declaration
  name: (identifier) @name
  body: (block) @body) @testmethod
`,
		AnchorCapture: "testmethod",
		OnMatch: func(ctx *MatchCtx) *model.Finding {
			name := ctx.Text(ctx.Captures["name"])
			if !isJUnitTestName(name) {
				return nil
			}
			blk := ctx.Captures["body"]
			if blockHasAssertion(ctx, blk) || blockThrows(ctx, blk) {
				return nil
			}
			return &model.Finding{
				StartLine: int(blk.StartPosition().Row) + 1,
				EndLine:   int(blk.EndPosition().Row) + 1,
				Message:   fmt.Sprintf("test %q has no assertion; add Assert/hamcrest/assertThat/assertEquals/fail or declare the expected exception, otherwise the test cannot fail", name),
			}
		},
	}
}

// isJUnitTestName recognizes the JUnit naming conventions: names starting
// with "test"/"Test" or ending with "Test" ("shouldReturnFoo" is NOT a
// test name by this rule — requiring @Test annotation metadata would need
// annotation capture support beyond this rule's scope).
func isJUnitTestName(name string) bool {
	return strings.HasPrefix(name, "test") || strings.HasPrefix(name, "Test") || strings.HasSuffix(name, "Test")
}

// blockHasAssertion reports whether the block invokes anything that looks
// like an assertion: any identifier named assert*/Assert*/fail/expect in
// call position. AST-only and naming-based by design (deterministic, no
// type info); Assert.assertTrue, assertThat, fail("x") all match.
func blockHasAssertion(ctx *MatchCtx, blk *sitter.Node) bool {
	found := false
	walkNodes(blk, func(n *sitter.Node) {
		if found {
			return
		}
		if n.Kind() == "method_invocation" {
			if nameNode := n.ChildByFieldName("name"); nameNode != nil {
				name := ctx.Text(nameNode)
				if strings.HasPrefix(name, "assert") || strings.HasPrefix(name, "Assert") || name == "fail" || strings.HasPrefix(name, "expect") {
					found = true
				}
			}
		}
	})
	return found
}

// blockThrows reports whether the block contains a throw statement — an
// explicit failure or expected-exception style, which counts as verification.
func blockThrows(ctx *MatchCtx, blk *sitter.Node) bool {
	found := false
	walkNodes(blk, func(n *sitter.Node) {
		if n.Kind() == "throw_statement" {
			found = true
		}
	})
	return found
}

// walkNodes visits every node in the subtree in depth-first order.
func walkNodes(n *sitter.Node, fn func(*sitter.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for i := uint(0); i < n.NamedChildCount(); i++ {
		walkNodes(n.NamedChild(i), fn)
	}
}

// printStackTrace flags e.printStackTrace(): the stack goes to stdout
// instead of the logging pipeline.
func printStackTrace() *Rule {
	return &Rule{
		ID:              "print-stack-trace",
		Title:           "printStackTrace() bypasses the logging pipeline",
		DefaultSeverity: model.SevWarning,
		DefaultEnabled:  true,
		Query: `
(method_invocation
  name: (identifier) @fn) @call
`,
		AnchorCapture: "call",
		OnMatch: func(ctx *MatchCtx) *model.Finding {
			if ctx.Text(ctx.Captures["fn"]) != "printStackTrace" {
				return nil
			}
			return &model.Finding{
				StartLine: int(ctx.Captures["call"].StartPosition().Row) + 1,
				EndLine:   int(ctx.Captures["call"].EndPosition().Row) + 1,
				Message:   "log the exception with the project logger instead of printStackTrace()",
			}
		},
	}
}

// javaEmptyBlock flags empty control-flow blocks: branches, all loop forms
// and catch handlers.
func javaEmptyBlock() *Rule {
	return newEmptyBlockRule(`
(if_statement consequence: (block) @blk)
(if_statement alternative: (block) @blk)
(for_statement body: (block) @blk)
(while_statement body: (block) @blk)
(enhanced_for_statement body: (block) @blk)
(catch_clause body: (block) @blk)
`)
}

// silentCatch flags catch blocks that swallow the exception: the block has
// statements but never references the caught exception, never throws, and
// never logs. Referencing the parameter (e.getMessage() into a response,
// wrapping into another exception, passing to a handler) means the
// exception's information flows somewhere visible; an unreferenced parameter
// is the deterministic proxy for "swallowed". Empty catch blocks stay
// empty-block's territory to avoid double counting.
func silentCatch() *Rule {
	return &Rule{
		ID:              "silent-catch",
		Title:           "Caught exception is never referenced, thrown or logged (swallowed)",
		DefaultSeverity: model.SevWarning,
		DefaultEnabled:  true,
		Query: `
(catch_clause
  (catch_formal_parameter) @param
  body: (block) @blk) @catch
`,
		AnchorCapture: "catch",
		OnMatch: func(ctx *MatchCtx) *model.Finding {
			blk := ctx.Captures["blk"]
			if blk.NamedChildCount() == 0 {
				return nil // empty block: empty-block rule's territory
			}
			name := catchParamName(ctx, ctx.Captures["param"])
			if name == "" {
				return nil // fail open: cannot pin the parameter spelling
			}
			if blockHandlesException(ctx, blk, name) {
				return nil
			}
			return &model.Finding{
				StartLine: int(blk.StartPosition().Row) + 1,
				EndLine:   int(blk.EndPosition().Row) + 1,
				Message:   "exception \"" + name + "\" is caught but never referenced: no log, no rethrow, no use; log it, wrap and throw, or pass it to a handler",
			}
		},
	}
}

// catchParamName extracts the declared variable name of a catch_formal_parameter.
// tree-sitter-java v0.23.5 puts the name on the `name:` field identifier
// directly (multi-catch "A | B e" keeps a single declarator identifier).
func catchParamName(ctx *MatchCtx, param *sitter.Node) string {
	if param == nil {
		return ""
	}
	if name := param.ChildByFieldName("name"); name != nil {
		return ctx.Text(name)
	}
	return ""
}

// blockHandlesException walks the block subtree for the three handling
// signals: a reference to the exception parameter, a throw statement, or a
// log-facade call. Spelling-based reference matching may miss shadowed
// bindings (fail-open toward clean).
func blockHandlesException(ctx *MatchCtx, blk *sitter.Node, param string) bool {
	handled := false
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if handled || n == nil {
			return
		}
		switch n.Kind() {
		case "identifier":
			if ctx.Text(n) == param {
				handled = true
				return
			}
		case "throw_statement":
			handled = true
			return
		case "method_invocation":
			if isLogCall(ctx, n) {
				handled = true
				return
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(blk)
	return handled
}

// isLogCall reports whether the invocation looks like a logging facade call
// (trace/debug/info/warn/warning/error/fatal).
func isLogCall(ctx *MatchCtx, n *sitter.Node) bool {
	name := n.ChildByFieldName("name")
	if name == nil {
		return false
	}
	switch ctx.Text(name) {
	case "trace", "debug", "info", "warn", "warning", "error", "fatal":
		return true
	}
	return false
}
