package rules

import (
	"metricgate/internal/model"
)

// JavaRules is the built-in Java rule pack. Banned APIs live in policy
// (languages.java.banned) and are compiled by the engine, not here.
func JavaRules() []*Rule {
	return []*Rule{
		printStackTrace(),
		javaEmptyBlock(),
		todoCommentRule("[(line_comment) @c (block_comment) @c]"),
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
