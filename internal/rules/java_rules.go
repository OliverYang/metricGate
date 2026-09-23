package rules

import (
	"strings"

	"metricgate/internal/model"
)

// JavaRules is the v0 rule set for Java: one banned-API rule, one logging
// hygiene rule, the empty-block rule and the shared TODO rule.
func JavaRules() []*Rule {
	return []*Rule{
		systemOutPrint(),
		printStackTrace(),
		javaEmptyBlock(),
		todoCommentRule("[(line_comment) @c (block_comment) @c]"),
	}
}

// systemOutPrint bans System.out/err.print* calls. This is the Java member
// of the banned-API rule family (the Go one is no-fmt-print).
func systemOutPrint() *Rule {
	return &Rule{
		ID:              "system-out-print",
		Title:           "Direct System.out/err.print* call; use the project logger",
		DefaultSeverity: model.SevWarning,
		DefaultEnabled:  true,
		Query: `
(method_invocation
  object: (field_access
    object: (identifier) @sys
    field: (identifier) @stream)
  name: (identifier) @fn) @call
`,
		OnMatch: func(ctx *MatchCtx) *model.Finding {
			if ctx.Text(ctx.Captures["sys"]) != "System" {
				return nil
			}
			stream := ctx.Text(ctx.Captures["stream"])
			if stream != "out" && stream != "err" {
				return nil
			}
			fn := ctx.Text(ctx.Captures["fn"])
			if !strings.HasPrefix(fn, "print") {
				return nil
			}
			for _, allow := range ctx.OptStrings("allowed_files") {
				if strings.HasSuffix(ctx.File, allow) {
					return nil
				}
			}
			return &model.Finding{
				StartLine: int(ctx.Captures["call"].StartPosition().Row) + 1,
				EndLine:   int(ctx.Captures["call"].EndPosition().Row) + 1,
				Message:   "System." + stream + "." + fn + " writes directly to the console; use the project logger",
			}
		},
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
	return &Rule{
		ID:              "empty-block",
		Title:           "Empty control-flow block",
		DefaultSeverity: model.SevWarning,
		DefaultEnabled:  true,
		Query: `
(if_statement consequence: (block) @blk)
(if_statement alternative: (block) @blk)
(for_statement body: (block) @blk)
(while_statement body: (block) @blk)
(enhanced_for_statement body: (block) @blk)
(catch_clause body: (block) @blk)
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
