package rules

import ()

// GoRules is the built-in Go rule pack. Banned APIs live in policy
// (languages.go.banned) and are compiled by the engine, not here.
func GoRules() []*Rule {
	return []*Rule{
		emptyBlock(),
		todoComment(),
	}
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
