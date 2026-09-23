package rules

import (
	"metricgate/internal/model"
)

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
	return &Rule{
		ID:              "empty-block",
		Title:           "Empty control-flow block",
		DefaultSeverity: model.SevWarning,
		DefaultEnabled:  true,
		Query: `
(if_statement consequence: (block) @blk)
(if_statement alternative: (block) @blk)
(for_statement body: (block) @blk)
`,
		OnMatch: func(ctx *MatchCtx) *model.Finding {
			blk := ctx.Captures["blk"]
			if blk.NamedChildCount() != 0 {
				return nil
			}
			return &model.Finding{
				StartLine: int(blk.StartPosition().Row) + 1,
				EndLine:   int(blk.EndPosition().Row) + 1,
				Message:   "empty block; handle the case or leave a comment explaining why it is empty",
			}
		},
	}
}

// todoComment reports TODO/FIXME comments (disabled by default).
func todoComment() *Rule { return todoCommentRule("(comment) @c") }
