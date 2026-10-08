package rules

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// GoRules is the built-in Go rule pack. Banned APIs live in policy
// (languages.go.banned) and are compiled by the engine, not here.
func GoRules() []*Rule {
	return []*Rule{
		emptyBlock(),
		todoComment(),
		GoUnknownDependency(),
		goChainDepth(),
	}
}

// goChainDepth flags call chains deeper than max_depth. In the Go grammar
// a chain a.b(x).c(y) is nested call_expressions; the outermost one is
// the chain root. The query matches every call, so OnMatch walks the
// function field down to measure the chain's depth and reports the
// outermost node of offending chains only (inner links are subsumed).
// goChainDepth flags call chains deeper than max_depth. In the Go grammar
// a chain c.A().B().D() alternates call_expression — function:
// selector_expression — operand: call_expression; the depth walk crosses
// each selector hop to count the calls, and nestedInChain (chain.go)
// keeps only the outermost link reportable.
func goChainDepth() *Rule {
	return chainDepthRule(`
(call_expression
  function: (_) @fn) @chain
`, func(ctx *MatchCtx) int {
		next := func(n *sitter.Node) *sitter.Node {
			fn := n.ChildByFieldName("function")
			if fn != nil && fn.Kind() == "selector_expression" {
				fn = fn.ChildByFieldName("operand")
			}
			return fn
		}
		// The matched node is itself a call: count it, then each inner call
		// the chain descends into. c.A().B().D().A() = 4.
		depth := 1
		for cur := next(ctx.Captures["chain"]); cur != nil && cur.Kind() == "call_expression"; cur = next(cur) {
			depth++
		}
		return depth
	})
}

// emptyBlock flags control-flow blocks that do nothing, which usually hides
// a swallowed error or a missing case.
func emptyBlock() *Rule {
	return newEmptyBlockRule(`
(if_statement consequence: (block) @blk)
(if_statement alternative: (block) @blk)
(for_statement body: (block) @blk)
`)
}

// todoComment reports TODO/FIXME comments (disabled by default).
func todoComment() *Rule { return todoCommentRule("(comment) @c") }
