// dsl.go compiles the project-authored rule DSL: policy.yaml entries that
// assert a naming/pattern invariant over a tree-sitter node kind.
//
// The DSL closes the gap between "a convention everyone agrees on" and
// "a rule the gate enforces": teams kept having AST-expressible conventions
// (test names, identifier prefixes, call shapes) that no built-in rule
// covered, and inline `metricgate: ignore` is the wrong tool for a rule the
// project wants enforced, not suppressed.
//
// Shape (policy.yaml, per language section or global via languages:):
//
//	custom_rules:
//	  - id: go-test-names
//	    node: function_declaration
//	    field: name            # optional child field to capture
//	    match_regex: '^Test'   # optional: fire only when text matches
//	    exclude_regex: '_test' # optional: suppress when text matches
//	    severity: warning      # default warning; error may gate the build
//	    reason: "test functions must start with Test"
//
// A rule whose node kind does not exist in the target grammar is dropped,
// not fatal: policy authors iterate across languages, and one typo must
// never take down the whole check run. Validity is checked against the real
// grammar via a trial compile, so an Impossible-pattern typo is caught the
// same way.
package rules

import (
	"fmt"
	"regexp"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/config"
	"metricgate/internal/model"
)

// DslRules compiles the policy's custom_rules for one language against that
// language's grammar. Invalid entries are dropped (returns nil for that
// entry) — the same tolerance BansFromConfig applies to policy bans: a bad
// entry must never break a run.
func DslRules(lang string, langPtr *sitter.Language, entries []config.DslRuleConfig) []*Rule {
	var out []*Rule
	for _, e := range entries {
		r, ok := dslRule(lang, langPtr, e)
		if ok {
			out = append(out, r)
		}
	}
	return out
}

func dslRule(lang string, langPtr *sitter.Language, e config.DslRuleConfig) (*Rule, bool) {
	id := strings.TrimSpace(e.ID)
	if id == "" || !dslIDRe.MatchString(id) {
		return nil, false
	}
	node := strings.TrimSpace(e.Node)
	if node == "" {
		return nil, false
	}
	query, ok := dslQuery(node, strings.TrimSpace(e.Field), langPtr)
	if !ok {
		return nil, false
	}
	matchRe, excludeRe, ok := dslRegexes(e)
	if !ok {
		return nil, false
	}
	sev, ok := dslSeverity(e.Severity)
	if !ok {
		return nil, false
	}
	ruleID := "custom." + id
	return &Rule{
		ID:              ruleID,
		Title:           orDefault(strings.TrimSpace(e.Title), id),
		DefaultSeverity: sev,
		DefaultEnabled:  true,
		Query:           query,
		AnchorCapture:   "m",
		OnMatch:         dslOnMatch(lang, id, e, matchRe, excludeRe),
	}, true
}

// dslQuery builds the rule's query: the node kind narrowed to a child field
// when one is named. A trial compile is the validator for both — a failed
// compile means the kind or field does not exist in this grammar. The failed
// query is not closed: on error the binding returns a partially-initialized
// value whose Close would dereference nil.
func dslQuery(node, field string, langPtr *sitter.Language) (string, bool) {
	var query string
	if field != "" {
		query = fmt.Sprintf("(%s %s: (_) @cap) @m", node, field)
	} else {
		query = fmt.Sprintf("(%s) @m", node)
	}
	q, err := sitter.NewQuery(langPtr, query)
	if err != nil {
		return "", false
	}
	q.Close()
	return query, true
}

// dslRegexes compiles the optional match/exclude patterns; either failing to
// compile invalidates the rule.
func dslRegexes(e config.DslRuleConfig) (*regexp.Regexp, *regexp.Regexp, bool) {
	var matchRe, excludeRe *regexp.Regexp
	var err error
	if s := strings.TrimSpace(e.MatchRegex); s != "" {
		if matchRe, err = regexp.Compile(s); err != nil {
			return nil, nil, false
		}
	}
	if s := strings.TrimSpace(e.ExcludeRegex); s != "" {
		if excludeRe, err = regexp.Compile(s); err != nil {
			return nil, nil, false
		}
	}
	return matchRe, excludeRe, true
}

// dslSeverity maps the policy severity string; an unknown word invalidates
// the rule rather than silently downgrading to warning.
func dslSeverity(s string) (model.Severity, bool) {
	switch strings.TrimSpace(s) {
	case "error":
		return model.SevError, true
	case "note", "info":
		return model.SevInfo, true
	case "", "warning":
		return model.SevWarning, true
	default:
		return "", false
	}
}

// dslOnMatch builds the match callback: assert the pattern over the captured
// text (@cap when a field is set, else the whole node). The closure stays thin
// on purpose — the file scope, the two regex modes and the message are each a
// named helper, so the rule's shape reads as one sentence.
func dslOnMatch(lang, id string, e config.DslRuleConfig, matchRe, excludeRe *regexp.Regexp) func(*MatchCtx) *model.Finding {
	ruleID := "custom." + id
	files := e.Files
	reason := strings.TrimSpace(e.Reason)
	use := strings.TrimSpace(e.Use)
	return func(ctx *MatchCtx) *model.Finding {
		textNode := dslTextNode(ctx)
		if textNode == nil {
			return nil
		}
		if len(files) > 0 && !fileMatches(files, ctx.File) {
			return nil
		}
		text := ctx.Text(textNode)
		if !dslViolates(text, matchRe, excludeRe) {
			return nil
		}
		return dslFinding(ruleID, dslMessage(lang, id, text, reason, use), ctx, textNode)
	}
}

// dslTextNode picks the node the assertion reads: the named child field when
// the rule set one, else the whole matched node.
func dslTextNode(ctx *MatchCtx) *sitter.Node {
	if n := ctx.Captures["cap"]; n != nil {
		return n
	}
	return ctx.Captures["m"]
}

// dslViolates applies the two regexes under the mode the policy chose.
func dslViolates(text string, matchRe, excludeRe *regexp.Regexp) bool {
	if matchRe == nil && excludeRe != nil {
		// Compliance-assertion mode (naming constraints): with no match_regex,
		// exclude_regex asserts what conforming text looks like — text that
		// fails the pattern is the violation. This is the only reading under
		// which a lone exclude_regex can be meaningful; as a carve-out it
		// would fire on everything else.
		return !excludeRe.MatchString(text)
	}
	// Violation-pattern mode: match_regex selects the violation,
	// exclude_regex carves out exceptions.
	if excludeRe != nil && excludeRe.MatchString(text) {
		return false
	}
	return matchRe == nil || matchRe.MatchString(text)
}

// dslMessage is the finding text: the policy's reason when it gave one, else
// a description that names the rule, with the suggested replacement appended.
func dslMessage(lang, id, text, reason, use string) string {
	msg := orDefault(reason, fmt.Sprintf("%s: %q matches custom rule %s", lang, text, id))
	if use != "" {
		msg += " — use " + use
	}
	return msg
}

// dslFinding anchors the finding at the asserted node.
func dslFinding(ruleID, msg string, ctx *MatchCtx, n *sitter.Node) *model.Finding {
	return &model.Finding{
		RuleID:    ruleID,
		Severity:  ctx.Severity,
		Source:    model.SrcRule,
		File:      ctx.File,
		StartLine: int(n.StartPosition().Row) + 1,
		StartCol:  int(n.StartPosition().Column) + 1,
		EndLine:   int(n.EndPosition().Row) + 1,
		EndCol:    int(n.EndPosition().Column) + 1,
		Message:   msg,
	}
}

// dslIDRe keeps rule ids to greppable slug form: they surface in findings,
// policy reviews, and the accept ledger.
var dslIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}
