package rules

import (
	"fmt"
	"strings"

	"metricgate/internal/model"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// TSRules is the built-in TypeScript rule pack. Banned APIs live in policy
// (languages.typescript.banned) and are compiled by the engine, not here.
func TSRules() []*Rule {
	return []*Rule{
		tsEmptyBlock(),
		todoCommentRule("[(comment) @c]"),
		TSUnknownDependency(),
		jsChainDepth(),
		jsTestNoAssert(),
	}
}

// JSRules is the built-in JavaScript rule pack.
func JSRules() []*Rule {
	return []*Rule{
		jsEmptyBlock(),
		todoCommentRule("[(comment) @c]"),
		JSUnknownDependency(),
		jsChainDepth(),
		jsTestNoAssert(),
	}
}

// tsEmptyBlock flags empty control-flow blocks: branches, all loop forms and
// catch handlers. JS shares the control-flow grammar with TypeScript. The
// else arm is matched with a wildcard: the grammar's alternative field is a
// choice (statement / if_statement), so a statement_block-narrowed pattern
// is statically impossible there.
func tsEmptyBlock() *Rule {
	return newEmptyBlockRule(`
(if_statement consequence: (statement_block) @blk)
(if_statement alternative: (_) @blk)
(for_statement body: (statement_block) @blk)
(for_in_statement body: (statement_block) @blk)
(while_statement body: (statement_block) @blk)
(do_statement body: (statement_block) @blk)
(catch_clause body: (statement_block) @blk)
`)
}

// jsEmptyBlock is the JavaScript twin; the shared grammar means the query is
// identical, kept separate so the packs stay independently tunable.
func jsEmptyBlock() *Rule {
	return tsEmptyBlock()
}

// jsChainDepth flags call chains deeper than max_depth. In the JS/TS grammar
// a chain a.b(x).c(y) is nested call_expressions whose function field holds
// a member_expression; the depth walk crosses each member hop to count the
// calls, and nestedInChain keeps only the outermost link reportable.
func jsChainDepth() *Rule {
	return chainDepthRule(`
(call_expression
  function: (_) @fn) @chain
`, func(ctx *MatchCtx) int {
		next := func(n *sitter.Node) *sitter.Node {
			fn := n.ChildByFieldName("function")
			if fn != nil && fn.Kind() == "member_expression" {
				fn = fn.ChildByFieldName("object")
			}
			return fn
		}
		// The matched node is itself a call: count it, then each inner call
		// the chain descends into. a.b().c().d() = 4.
		depth := 1
		for cur := next(ctx.Captures["chain"]); cur != nil && cur.Kind() == "call_expression"; cur = next(cur) {
			depth++
		}
		return depth
	})
}

// jsTestNoAssert flags test functions that never call an assertion — the
// JS/TS twin of goTestNoAssert (testsmells.org, FSE'20 UT test smell).
// Detection is existence-based over the whole function subtree; the
// convention checked is jest/vitest/mocha style names: test/it/describe
// callbacks are out of scope (no type info), so the rule keys on function
// names starting with "test" or ending with "Test".
func jsTestNoAssert() *Rule {
	return &Rule{
		ID:              "test-no-assert",
		Title:           "Test function contains no assertion (UT test smell)",
		DefaultSeverity: model.SevWarning,
		DefaultEnabled:  true,
		Query: `
[
  (function_declaration
    name: (identifier) @name
    body: (statement_block) @body)
  (generator_function_declaration
    name: (identifier) @name
    body: (statement_block) @body)
] @testfunc
`,
		AnchorCapture: "testfunc",
		OnMatch: func(ctx *MatchCtx) *model.Finding {
			name := ctx.Text(ctx.Captures["name"])
			if !strings.HasPrefix(name, "test") && !strings.HasPrefix(name, "Test") && !strings.HasSuffix(name, "Test") {
				return nil
			}
			blk := ctx.Captures["body"]
			if jsBlockHasAssertion(ctx, blk) {
				return nil
			}
			return &model.Finding{
				StartLine: int(blk.StartPosition().Row) + 1,
				EndLine:   int(blk.EndPosition().Row) + 1,
				Message:   fmt.Sprintf("test %q has no assertion; add expect/assert/should (including inside nested callbacks), otherwise the test cannot fail", name),
			}
		},
	}
}

// jsBlockHasAssertion reports whether the block calls anything that looks
// like an assertion: any callee whose name contains assert/expect/should or
// a must prefix. AST-only and naming-based by design (deterministic, no
// type info); expect(...).toBe, assert.equal, t.truthy all match.
func jsBlockHasAssertion(ctx *MatchCtx, blk *sitter.Node) bool {
	found := false
	walkNodes(blk, func(n *sitter.Node) {
		if found || n.Kind() != "call_expression" {
			return
		}
		if fn := n.ChildByFieldName("function"); fn != nil {
			name := jsCalleeName(ctx, fn)
			lower := strings.ToLower(name)
			if strings.Contains(lower, "assert") || strings.Contains(lower, "expect") ||
				strings.Contains(lower, "should") || strings.HasPrefix(lower, "must") {
				found = true
			}
		}
	})
	return found
}

// jsCalleeName extracts the plain name a call is made through: the property
// of a member_expression, else the node's own text (identifier or call).
func jsCalleeName(ctx *MatchCtx, fn *sitter.Node) string {
	if fn.Kind() == "member_expression" {
		if prop := fn.ChildByFieldName("property"); prop != nil {
			return ctx.Text(prop)
		}
	}
	return ctx.Text(fn)
}

// TSUnknownDependency flags a TypeScript import outside the declared
// dependency set. TypeScript resolves node_modules like JavaScript, so the
// allow list comes from the policy (package-lock deps fed in by the engine
// when available); bare node builtins are always allowed.
func TSUnknownDependency() *Rule {
	return tsImportRule()
}

// JSUnknownDependency is the JavaScript twin.
func JSUnknownDependency() *Rule {
	return tsImportRule()
}

// tsImportRule builds the import rule over the JS/TS import grammar. Import
// sources are string literals on import_statement / export_statement; the
// plain require(...) call form is matched too.
func tsImportRule() *Rule {
	return unknownDepRule(
		`[
			(import_statement source: (string) @path)
			(export_statement source: (string) @path)
			(call_expression
				function: (identifier) @fn (#eq? @fn "require")
				arguments: (arguments (string) @path))
		]`,
		func(path string) string { return strings.Trim(path, "\"'`") },
		jsBuiltinImport,
	)
}

// jsBuiltinImport reports whether an import is a Node builtin module, which
// needs no declaration. The builtin set is a fixed list, not a heuristic: a
// bare name is exactly what an npm package specifier looks like, so "any
// name without a slash" would silently allow every hallucinated package.
// Relative paths and anything with a slash (scoped or nested packages) are
// never builtins.
func jsBuiltinImport(path string) bool {
	if strings.HasPrefix(path, "node:") {
		return true
	}
	if strings.HasPrefix(path, ".") || strings.HasPrefix(path, "/") {
		return false
	}
	if strings.Contains(path, "/") {
		return false
	}
	return nodeBuiltins[path]
}

// nodeBuiltins is the stable module list every currently maintained Node
// line exposes (Node 18+), used as the "platform, not declared" escape hatch
// of the import rule.
var nodeBuiltins = map[string]bool{
	"assert": true, "assert/strict": true,
	"async_hooks":         true,
	"buffer":              true,
	"child_process":       true,
	"cluster":             true,
	"console":             true,
	"constants":           true,
	"crypto":              true,
	"dgram":               true,
	"diagnostics_channel": true,
	"dns":                 true, "dns/promises": true,
	"domain": true,
	"events": true,
	"fs":     true, "fs/promises": true,
	"http": true, "http2": true,
	"https":     true,
	"inspector": true, "inspector/promises": true,
	"module": true,
	"net":    true,
	"os":     true,
	"path":   true, "path/posix": true, "path/win32": true,
	"perf_hooks":  true,
	"process":     true,
	"punycode":    true,
	"querystring": true,
	"readline":    true, "readline/promises": true,
	"repl":   true,
	"stream": true, "stream/consumers": true, "stream/promises": true, "stream/web": true,
	"string_decoder": true,
	"sys":            true,
	"test":           true, "test/reporters": true, "test/mock": true,
	"timers": true, "timers/promises": true,
	"tls":          true,
	"trace_events": true,
	"tty":          true,
	"url":          true,
	"util":         true, "util/types": true,
	"v8":             true,
	"vm":             true,
	"wasi":           true,
	"worker_threads": true,
	"zlib":           true,
}
