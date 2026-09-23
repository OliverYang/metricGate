package rules

import (
	"strings"

	"metricgate/internal/model"
)

// GoRules is the v0 rule set for Go. Each rule is one tree-sitter query plus
// a filter callback; severities and options come from policy overrides.
func GoRules() []*Rule {
	return []*Rule{
		noFmtPrint(),
		emptyBlock(),
		panicCall(),
		todoComment(),
	}
}

// noFmtPrint bans direct fmt.Print* calls in production code. This is the
// canonical "banned API" constraint: the same shape later carries an
// organisation's internal allow/deny lists.
func noFmtPrint() *Rule {
	return &Rule{
		ID:              "no-fmt-print",
		Title:           "Direct fmt.Print* call; use the project logger",
		DefaultSeverity: model.SevWarning,
		DefaultEnabled:  true,
		Query: `
(call_expression
  function: (selector_expression
    operand: (identifier) @pkg
    field: (field_identifier) @fn)) @call
`,
		OnMatch: func(ctx *MatchCtx) *model.Finding {
			if ctx.Text(ctx.Captures["pkg"]) != "fmt" {
				return nil
			}
			fn := ctx.Text(ctx.Captures["fn"])
			if !strings.HasPrefix(fn, "Print") {
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
				Message:   "fmt." + fn + " writes directly to stdout; use the project logger",
			}
		},
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

// panicCall flags direct panic() in non-test code.
func panicCall() *Rule {
	return &Rule{
		ID:              "panic-call",
		Title:           "Direct panic() call outside tests",
		DefaultSeverity: model.SevWarning,
		DefaultEnabled:  true,
		Query: `
(call_expression
  function: (identifier) @fn) @call
`,
		OnMatch: func(ctx *MatchCtx) *model.Finding {
			if ctx.Text(ctx.Captures["fn"]) != "panic" {
				return nil
			}
			if ctx.OptBool("allow_tests", true) && strings.HasSuffix(ctx.File, "_test.go") {
				return nil
			}
			return &model.Finding{
				StartLine: int(ctx.Captures["call"].StartPosition().Row) + 1,
				EndLine:   int(ctx.Captures["call"].EndPosition().Row) + 1,
				Message:   "direct panic() call; return an error so callers can decide",
			}
		},
	}
}

// todoComment reports TODO/FIXME comments (disabled by default).
func todoComment() *Rule {
	return &Rule{
		ID:              "todo-comment",
		Title:           "TODO/FIXME comment left in code",
		DefaultSeverity: model.SevInfo,
		DefaultEnabled:  false,
		Query: `
(comment) @c
`,
		OnMatch: func(ctx *MatchCtx) *model.Finding {
			c := ctx.Text(ctx.Captures["c"])
			what := ""
			switch {
			case strings.Contains(c, "FIXME"):
				what = "FIXME"
			case strings.Contains(c, "TODO"):
				what = "TODO"
			default:
				return nil
			}
			return &model.Finding{
				StartLine: int(ctx.Captures["c"].StartPosition().Row) + 1,
				EndLine:   int(ctx.Captures["c"].EndPosition().Row) + 1,
				Message:   what + " comment found; track it in an issue",
			}
		},
	}
}
