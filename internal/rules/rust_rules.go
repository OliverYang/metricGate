package rules

import (
	"fmt"
	"strings"

	"metricgate/internal/model"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// RustRules is the built-in Rust rule pack. Banned APIs live in policy
// (languages.rust.banned) and are compiled by the engine, not here.
func RustRules() []*Rule {
	return []*Rule{
		rustEmptyBlock(),
		todoCommentRule("[(line_comment) (block_comment)] @c"),
		RustUnknownDependency(),
		rustChainDepth(),
		rustTestNoAssert(),
	}
}

// rustEmptyBlock flags empty control-flow blocks: if consequence /
// alternative (alternative is a choice type — if/else chains — so the
// wildcard form is required), loop bodies and match arms with empty blocks.
// A block holding only a comment still counts as empty.
func rustEmptyBlock() *Rule {
	return newEmptyBlockRule(`
(if_expression consequence: (block) @blk)
(if_expression alternative: (_) @blk)
(for_expression body: (block) @blk)
(while_expression body: (block) @blk)
(loop_expression body: (block) @blk)
`)
}

// rustChainDepth flags call chains deeper than max_depth. In the Rust
// grammar a chain a.b().c() nests call nodes whose function field holds a
// field_expression; the depth walk crosses each field hop to count the
// calls, and nestedInChain keeps only the outermost link reportable.
func rustChainDepth() *Rule {
	return chainDepthRule(`
(call_expression
  function: (_) @fn) @chain
`, func(ctx *MatchCtx) int {
		next := func(n *sitter.Node) *sitter.Node {
			fn := n.ChildByFieldName("function")
			if fn != nil && fn.Kind() == "field_expression" {
				fn = fn.ChildByFieldName("value")
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

// RustUnknownDependency flags a Rust `use` outside the standard library and
// the declared dependency set. The allow list comes from the policy (Cargo
// dependencies fed in by the engine when available); std/core/alloc crates
// are always allowed.
func RustUnknownDependency() *Rule {
	return unknownDepRule(
		`[(use_declaration argument: (_) @path)]`,
		rustCratePath,
		rustStdlibImport,
	)
}

// rustCratePath reduces a use tree to its crate root: scoped_identifier
// (crate::item), scoped_use_list, identifier, use_list (multi-crate use
// a::{b, c} — the first element wins, which is conservative for roots that
// are std and may miss a second std crate, acceptable for a warning rule).
func rustCratePath(path string) string {
	path = strings.TrimPrefix(strings.TrimSpace(path), "pub ")
	path = strings.TrimSpace(path)
	// use_list braces: take the element before the first comma.
	if i := strings.IndexAny(path, ",{"); i >= 0 {
		path = strings.TrimSpace(path[:i])
	}
	// Crate root: the segment before the first :: (self/super/crate included).
	if i := strings.Index(path, "::"); i >= 0 {
		return path[:i]
	}
	return path
}

// rustStdlibImport reports whether a crate is shipped with the Rust
// distribution (std and its façades, plus the always-available core/alloc),
// which needs no declaration. crate-relative uses (crate::, self::, super::)
// are inside the repo and never external dependencies.
func rustStdlibImport(path string) bool {
	switch path {
	case "crate", "self", "super", "std", "core", "alloc", "proc_macro", "test":
		return true
	}
	return false
}

// rustTestNoAssert flags #[test] functions that never call an assertion —
// the Rust twin of goTestNoAssert (testsmells.org, FSE'20 UT test smell).
// The convention checked is the attribute #[test] / #[tokio::test] and
// friends, not the name: Rust test names follow no assert-bearing prefix.
func rustTestNoAssert() *Rule {
	return &Rule{
		ID:              "test-no-assert",
		Title:           "Test function contains no assertion (UT test smell)",
		DefaultSeverity: model.SevWarning,
		DefaultEnabled:  true,
		Query: `
(function_item
  name: (identifier) @name
  body: (block) @body) @testfunc
`,
		AnchorCapture: "testfunc",
		OnMatch: func(ctx *MatchCtx) *model.Finding {
			fn := ctx.Captures["testfunc"]
			if !rustIsTestFunc(ctx, fn) {
				return nil
			}
			name := ctx.Text(ctx.Captures["name"])
			blk := ctx.Captures["body"]
			if rustBlockHasAssertion(ctx, blk) {
				return nil
			}
			return &model.Finding{
				StartLine: int(blk.StartPosition().Row) + 1,
				EndLine:   int(blk.EndPosition().Row) + 1,
				Message:   fmt.Sprintf("test %q has no assertion; add assert!/assert_eq!/assert_ne!/panic!/unwrap (including inside nested closures), otherwise the test cannot fail", name),
			}
		},
	}
}

// rustIsTestFunc reports whether a function_item carries a #[test]-family
// attribute (#[test], #[tokio::test], #[rstest], #[should_panic], ...).
// Attributes are siblings of the function inside the enclosing item list;
// the nearest preceding non-trivia sibling decides.
func rustIsTestFunc(ctx *MatchCtx, fn *sitter.Node) bool {
	attr := rustTestAttribute(ctx, fn)
	// #[test], #[tokio::test], #[rstest], #[should_panic], #[test_case(...)]
	return strings.Contains(attr, "test")
}

// rustTestAttribute returns the text of the #[...] attribute that governs
// the function, or "" when the preceding item is not an attribute.
func rustTestAttribute(ctx *MatchCtx, fn *sitter.Node) string {
	if fn == nil {
		return ""
	}
	for prev := fn.PrevNamedSibling(); prev != nil; prev = prev.PrevNamedSibling() {
		switch prev.Kind() {
		case "line_comment", "block_comment":
			continue // comments do not break attribute adjacency
		case "attribute_item":
			return ctx.Text(prev)
		default:
			return ""
		}
	}
	return ""
}

// rustBlockHasAssertion reports whether the block asserts anything:
// assert!/assert_eq!/assert_ne!/panic!/todo!/unimplemented! macros,
// unwrap/expect on results, or calls whose name contains assert. AST-only
// and naming-based by design (deterministic, no type info).
func rustBlockHasAssertion(ctx *MatchCtx, blk *sitter.Node) bool {
	found := false
	walkNodes(blk, func(n *sitter.Node) {
		if found {
			return
		}
		switch n.Kind() {
		case "macro_invocation":
			found = rustMacroAsserts(ctx, n)
		case "call_expression":
			if fn := n.ChildByFieldName("function"); fn != nil {
				found = strings.Contains(strings.ToLower(rustCalleeName(ctx, fn)), "assert")
			}
		case "field_expression":
			found = rustFieldPanics(ctx, n)
		}
	})
	return found
}

// rustMacroAsserts reports whether a macro_invocation is an assertion or a
// deliberate panic: assert!/assert_eq!/assert_ne!/panic!/todo!/unimplemented!.
func rustMacroAsserts(ctx *MatchCtx, n *sitter.Node) bool {
	m := n.ChildByFieldName("macro")
	if m == nil {
		return false
	}
	name := strings.ToLower(ctx.Text(m))
	return strings.HasPrefix(name, "assert") || name == "panic" ||
		name == "todo" || name == "unimplemented"
}

// rustFieldPanics reports whether a field_expression is an unwrap()/expect()
// call, which terminates the test on failure and so counts as an assertion.
func rustFieldPanics(ctx *MatchCtx, n *sitter.Node) bool {
	f := n.ChildByFieldName("field")
	if f == nil {
		return false
	}
	switch strings.ToLower(ctx.Text(f)) {
	case "unwrap", "expect":
		return true
	}
	return false
}

// rustCalleeName extracts the plain name a call is made through: the field
// of a field_expression chain, else the node's own text.
func rustCalleeName(ctx *MatchCtx, fn *sitter.Node) string {
	if fn.Kind() == "field_expression" {
		if f := fn.ChildByFieldName("field"); f != nil {
			return ctx.Text(f)
		}
	}
	return ctx.Text(fn)
}
