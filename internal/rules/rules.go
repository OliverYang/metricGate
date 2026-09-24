// Package rules implements the pattern-rule layer. A rule is a tree-sitter
// query plus a small Go filter that decides whether a match is a finding.
// Predicates are deliberately kept out of the .scm patterns and expressed in
// Go so rules stay easy to test and to port across grammars.
package rules

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/model"
	"metricgate/internal/parser"
)

// MatchCtx is handed to a rule's OnMatch callback for every query match.
type MatchCtx struct {
	File     string
	Src      []byte
	Severity model.Severity
	Options  map[string]any
	Captures map[string]*sitter.Node // first node per capture name
}

// Text returns the source text covered by a node.
func (c *MatchCtx) Text(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return string(c.Src[n.StartByte():n.EndByte()])
}

// OptBool reads a boolean option, returning def when absent.
func (c *MatchCtx) OptBool(key string, def bool) bool {
	v, ok := c.Options[key]
	if !ok {
		return def
	}
	b, ok := v.(bool)
	if !ok {
		return def
	}
	return b
}

// OptStrings reads a string-list option ([]any from YAML).
func (c *MatchCtx) OptStrings(key string) []string {
	v, ok := c.Options[key]
	if !ok {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Rule is a declarative pattern rule.
type Rule struct {
	ID              string
	Title           string
	DefaultSeverity model.Severity
	DefaultEnabled  bool
	Query           string
	// AnchorCapture names the capture whose node span becomes the finding's
	// Evidence. Empty means the rule has no single AST span (a line_only
	// anchor is recorded instead). The runner attaches evidence centrally, so
	// rule callbacks stay focused on the match decision.
	AnchorCapture string
	OnMatch       func(ctx *MatchCtx) *model.Finding
}

// Policy resolves per-rule configuration overrides. Implemented by the engine.
type Policy interface {
	// RuleSeverity returns "" when the policy does not override the severity.
	RuleSeverity(lang, ruleID string) string
	// RuleEnabled returns nil when the policy does not override the flag.
	RuleEnabled(lang, ruleID string) *bool
	// RuleOptions returns nil when the policy has no options.
	RuleOptions(lang, ruleID string) map[string]any
}

// compiledRule pairs a rule with its pre-compiled query and capture indexes.
type compiledRule struct {
	rule   *Rule
	query  *sitter.Query
	names  map[string]uint
	capIdx []string // capture name by index, for building the map fast
}

// Runner executes the rules of one language over parsed files.
type Runner struct {
	lang     string
	compiled []*compiledRule
}

// NewRunner compiles all rule queries against a language at startup, so a
// bad pattern fails fast instead of mid-run.
func NewRunner(lang string, langPtr *sitter.Language, all []*Rule) (*Runner, error) {
	r := &Runner{lang: lang}
	for _, rule := range all {
		q, err := sitter.NewQuery(langPtr, rule.Query)
		if err != nil {
			return nil, err
		}
		cr := &compiledRule{rule: rule, query: q, names: map[string]uint{}}
		for i, name := range q.CaptureNames() {
			cr.names[name] = uint(i)
			cr.capIdx = append(cr.capIdx, name)
		}
		r.compiled = append(r.compiled, cr)
	}
	return r, nil
}

// Close releases compiled queries.
func (r *Runner) Close() {
	for _, cr := range r.compiled {
		cr.query.Close()
	}
}

// Enabled reports whether a rule should run under the given policy.
func (r *Runner) Enabled(p Policy, id string, def bool) bool {
	if p == nil {
		return def
	}
	if b := p.RuleEnabled(r.lang, id); b != nil {
		return *b
	}
	return def
}

// Severity resolves the effective severity for a rule.
func (r *Runner) Severity(p Policy, id string, def model.Severity) model.Severity {
	if p == nil {
		return def
	}
	if s := p.RuleSeverity(r.lang, id); s != "" {
		return model.Severity(s)
	}
	return def
}

// Run applies every enabled rule to one parsed file. The setup (enable
// check, severity, options) is kept in Run; per-rule cursor iteration and
// per-match capture handling live in helpers so Run stays under the
// cognitive-complexity gate.
func (r *Runner) Run(file string, src []byte, tree *sitter.Tree, p Policy) []model.Finding {
	var findings []model.Finding
	root := tree.RootNode()
	for _, cr := range r.compiled {
		rule := cr.rule
		if !r.Enabled(p, rule.ID, rule.DefaultEnabled) {
			continue
		}
		sev := r.Severity(p, rule.ID, rule.DefaultSeverity)
		opts := map[string]any(nil)
		if p != nil {
			opts = p.RuleOptions(r.lang, rule.ID)
		}
		findings = append(findings, runOneRule(rule, cr, root, src, file, sev, opts)...)
	}
	return findings
}

// runOneRule drives the tree-sitter cursor for one compiled rule and
// collects the findings produced. Cursor lifetime is owned here so the
// caller never has to remember to Close it.
func runOneRule(rule *Rule, cr *compiledRule, root *sitter.Node, src []byte, file string, sev model.Severity, opts map[string]any) []model.Finding {
	var out []model.Finding
	cursor := sitter.NewQueryCursor()
	matches := cursor.Matches(cr.query, root, src)
	for {
		m := matches.Next()
		if m == nil {
			break
		}
		caps := collectCaps(m, cr.capIdx)
		ctx := &MatchCtx{File: file, Src: src, Severity: sev, Options: opts, Captures: caps}
		if f := rule.OnMatch(ctx); f != nil {
			f.RuleID = rule.ID
			f.Severity = sev
			f.Source = model.SrcRule
			f.File = file
			attachEvidence(f, ctx, rule.AnchorCapture)
			out = append(out, *f)
		}
	}
	cursor.Close()
	return out
}

// collectCaps turns a tree-sitter match's capture slice into a name -> node
// map. Only the first node per capture name is kept, matching the rest of
// the engine's "first wins" expectation.
func collectCaps(m *sitter.QueryMatch, capIdx []string) map[string]*sitter.Node {
	caps := make(map[string]*sitter.Node, len(m.Captures))
	for _, cap := range m.Captures {
		name := "unknown"
		if int(cap.Index) < len(capIdx) {
			name = capIdx[cap.Index]
		}
		if _, seen := caps[name]; seen {
			continue
		}
		node := cap.Node
		caps[name] = &node
	}
	return caps
}

// attachEvidence records the finding's deterministic anchor. When the rule
// names an AST capture, the exact byte span and its hash are captured; the
// callback's own line/column region is kept as the authoritative editor
// location either way. Rules without a span get a line_only anchor so a
// consumer can tell "no byte proof" from "byte proof missing".
func attachEvidence(f *model.Finding, ctx *MatchCtx, capture string) {
	n := ctx.Captures[capture]
	if capture == "" || n == nil {
		f.Evidence = &model.Evidence{Anchor: model.AnchorLineOnly}
		return
	}
	f.Evidence = model.NewEvidence(ctx.Src, n.StartByte(), n.EndByte())
}

// Meta is rule metadata for reports (SARIF driver.rules).
type Meta struct {
	ID    string
	Title string
}

// Metas returns rule metadata for a rule set.
func Metas(all []*Rule) []Meta {
	metas := make([]Meta, 0, len(all))
	for _, r := range all {
		metas = append(metas, Meta{ID: r.ID, Title: r.Title})
	}
	return metas
}

// ForLanguage returns the built-in rule pack of a language, without the
// policy bans. Callers that check code use RulesFor instead.
func ForLanguage(id parser.LanguageID) []*Rule {
	switch id {
	case parser.LangGo:
		return GoRules()
	case parser.LangJava:
		return JavaRules()
	default:
		return nil
	}
}

// todoCommentRule builds the TODO/FIXME rule for a language's comment nodes.
func todoCommentRule(commentQuery string) *Rule {
	return &Rule{
		ID:              "todo-comment",
		Title:           "TODO/FIXME comment left in code",
		DefaultSeverity: model.SevInfo,
		DefaultEnabled:  false,
		Query:           commentQuery,
		AnchorCapture:   "c",
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

// newEmptyBlockRule builds the language-neutral empty-block rule over the
// per-language query: both grammars flag the same way, only the query and
// the block kinds they reach differ.
func newEmptyBlockRule(query string) *Rule {
	return &Rule{
		ID:              "empty-block",
		Title:           "Empty control-flow block",
		DefaultSeverity: model.SevWarning,
		DefaultEnabled:  true,
		Query:           query,
		AnchorCapture:   "blk",
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
