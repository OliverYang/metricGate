// Suppression directives: `metricgate: ignore` comments let developers
// silence specific findings in place, with the suppression visible in the
// report (Ignored count) instead of hidden.
//
// Syntax (inside // or /* */ comments):
//
//	metricgate: ignore <rule[,rule2...]> [reason...]   — line scope
//	metricgate: ignore-begin <rule[,rule2...]>          — range start
//	metricgate: ignore-end                              — range end
//
// Line scope covers the comment's own line; when the comment starts the
// line, the next non-blank line is covered too (put it directly above a
// declaration to silence its metric verdicts). Rule list may be `all` or
// omitted. Everything after the rule list is a free-form reason.
package engine

import (
	"regexp"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/model"
)

// suppression is one active directive region. The reason is kept so the
// policy audit can list each site with the words its author wrote, rather
// than only a count: a suppression nobody can read is indistinguishable from
// a bug.
type suppression struct {
	start, end int      // inclusive 1-based lines
	rules      []string // empty means every rule
	reason     string
}

var directiveRe = regexp.MustCompile(`(?i)metricgate:\s*ignore(-begin|-end)?\b\s*(.*)`)

// isCommentKind reports whether a node kind is a comment in some grammar.
//
// It matches by name instead of consulting a per-language table on purpose. A
// table has to be extended by hand for every new language, and the failure mode
// of forgetting is invisible: directives are simply never seen, so a silenced
// rule stays loud and nothing says why. That is exactly what happened — the
// table covered Go and Java, so `metricgate: ignore` did nothing in the other
// seven languages. Every grammar in use spells its comment kinds with the word
// "comment" (comment, line_comment, block_comment, doc_comment), and a kind
// that merely resembles it can only cause a second, identical region, which
// suppress() treats as one.
func isCommentKind(kind string) bool { return strings.Contains(kind, "comment") }

// text returns the source slice covered by a node.
func text(src []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return string(src[n.StartByte():n.EndByte()])
}

// buildSuppressions walks the tree and collects all suppression regions.
func buildSuppressions(src []byte, root *sitter.Node) []suppression {
	b := &suppressionBuilder{lines: strings.Split(string(src), "\n"), src: src}
	b.walk(root)
	b.closeOpen(int(root.EndPosition().Row) + 1)
	return b.out
}

// suppressionBuilder accumulates directive regions while walking the tree.
type suppressionBuilder struct {
	src   []byte
	lines []string
	out   []suppression

	openStart  int
	openEnd    int
	openRules  []string
	openReason string
}

func (b *suppressionBuilder) walk(n *sitter.Node) {
	if !isCommentKind(n.Kind()) {
		b.descend(n)
		return
	}
	b.onComment(n)
}

func (b *suppressionBuilder) descend(n *sitter.Node) {
	for i := uint(0); i < n.ChildCount(); i++ {
		b.walk(n.Child(i))
	}
}

// onComment parses one comment node and applies its directive, if any.
func (b *suppressionBuilder) onComment(n *sitter.Node) {
	m := directiveRe.FindStringSubmatch(text(b.src, n))
	if m == nil {
		return
	}
	b.directive(m[1], strings.TrimSpace(m[2]), int(n.StartPosition().Row)+1, n)
}

// directive applies one parsed directive. Anything after the rule list is a
// free-form reason.
func (b *suppressionBuilder) directive(kind, rest string, line int, n *sitter.Node) {
	rules, reason := parseRuleList(rest)
	switch kind {
	case "-begin":
		if b.openStart == 0 {
			b.openStart, b.openRules, b.openEnd, b.openReason = line, rules, 0, reason
		}
	case "-end":
		if b.openStart != 0 && b.openEnd == 0 {
			b.openEnd = line
			b.out = append(b.out, suppression{start: b.openStart, end: b.openEnd, rules: b.openRules, reason: b.openReason})
		}
	default:
		b.out = append(b.out, lineSuppression(b.lines, n, line, rules, reason))
	}
}

// closeOpen emits a begin region that never saw its end: it stays open until
// the end of the file.
func (b *suppressionBuilder) closeOpen(eof int) {
	if b.openStart != 0 && b.openEnd == 0 {
		b.out = append(b.out, suppression{start: b.openStart, end: eof, rules: b.openRules, reason: b.openReason})
	}
}

// parseRuleList splits the comma-separated rule field from the reason that
// follows it. Empty or "all" means every rule; everything after the first
// whitespace group is the author's free-form justification.
func parseRuleList(rest string) (rules []string, reason string) {
	if rest == "" {
		return nil, "" // all rules, no reason
	}
	field := rest
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		field, reason = rest[:i], strings.TrimSpace(rest[i+1:])
	}
	for _, r := range strings.Split(field, ",") {
		r = strings.TrimSpace(r)
		if r == "" || strings.EqualFold(r, "all") {
			return nil, reason
		}
		rules = append(rules, r)
	}
	return rules, reason
}

// lineStartBlank reports whether the comment begins its line (nothing but
// whitespace before it).
func lineStartBlank(lines []string, n *sitter.Node) bool {
	row, col := int(n.StartPosition().Row), int(n.StartPosition().Column)
	if row >= len(lines) || col > len(lines[row]) {
		return false
	}
	return strings.TrimSpace(lines[row][:col]) == ""
}

// nextNonBlankLine returns the 1-based number of the first line after the
// given 1-based line that contains any non-whitespace character, or 0.
func nextNonBlankLine(lines []string, line int) int {
	for i := line; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return i + 1
		}
	}
	return 0
}

// countSuppressed records suppressed findings on the summary.
//
// It exists so the four places that apply directives (per-file analysis,
// architecture, SDP and clone checks) do not each re-implement the merge — and
// so a future whole-graph stage cannot silently forget to carry the split.
func countSuppressed(summary *model.RunSummary, n int, byRule map[string]int) {
	if n == 0 {
		return
	}
	summary.Ignored += n
	if len(byRule) == 0 {
		return
	}
	if summary.IgnoredByRule == nil {
		summary.IgnoredByRule = map[string]int{}
	}
	for id, k := range byRule {
		summary.IgnoredByRule[id] += k
	}
}

// suppress filters findings through the suppression regions, returning the
// kept findings, how many were suppressed, and the same total split by rule.
//
// The split exists for the policy audit: a rule that is silenced over and over
// is a rule the team has voted against, and that signal is invisible in the
// total — one number cannot distinguish "20 stale TODOs" from "20 ignored
// architecture violations".
func suppress(findings []model.Finding, sups []suppression) (kept []model.Finding, ignored int, byRule map[string]int) {
	for _, f := range findings {
		dropped := false
		for _, s := range sups {
			if f.StartLine < s.start || f.StartLine > s.end {
				continue
			}
			if len(s.rules) == 0 {
				dropped = true
				break
			}
			for _, r := range s.rules {
				if r == f.RuleID {
					dropped = true
					break
				}
			}
			if dropped {
				break
			}
		}
		if dropped {
			ignored++
			if byRule == nil {
				byRule = map[string]int{}
			}
			byRule[f.RuleID]++
			continue
		}
		kept = append(kept, f)
	}
	return kept, ignored, byRule
}

// lineSuppression builds the line-scope suppression for a directive comment:
// its own line, plus the next non-blank line when the comment starts its
// own line (put it directly above a declaration to cover its verdicts).
func lineSuppression(lines []string, n *sitter.Node, line int, rules []string, reason string) suppression {
	start, end := line, line
	if lineStartBlank(lines, n) {
		if nl := nextNonBlankLine(lines, line); nl > line {
			end = nl
		}
	}
	return suppression{start: start, end: end, rules: rules, reason: reason}
}

// Directive is one suppression directive as a reader sees it: where it was
// written, what it silences, and why. It is the per-site form of the
// summary's IgnoredByRule counts, so a team can audit the words behind the
// numbers — the ledger's owner/reason/expiry discipline applied to inline
// suppressions, which are otherwise anonymous.
type Directive struct {
	File   string   `json:"file"`
	Start  int      `json:"start"`
	End    int      `json:"end"`
	Rules  []string `json:"rules,omitempty"` // empty means every rule
	Reason string   `json:"reason,omitempty"`
}

// Directives lists every suppression directive in one file's source. The
// engine applies the same parse it applies while filtering, so a directive
// the gate honoured is a directive this reports — they cannot drift.
func Directives(src []byte, root *sitter.Node) []Directive {
	sups := buildSuppressions(src, root)
	out := make([]Directive, 0, len(sups))
	for _, s := range sups {
		out = append(out, Directive{File: "", Start: s.start, End: s.end, Rules: s.rules, Reason: s.reason})
	}
	return out
}
