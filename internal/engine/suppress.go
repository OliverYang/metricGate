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
	"metricgate/internal/parser"
)

// suppression is one active directive region.
type suppression struct {
	start, end int      // inclusive 1-based lines
	rules      []string // empty means every rule
}

var directiveRe = regexp.MustCompile(`(?i)metricgate:\s*ignore(-begin|-end)?\b\s*(.*)`)

// commentKinds lists comment node kinds per language.
var commentKinds = map[parser.LanguageID][]string{
	parser.LangGo:   {"comment"},
	parser.LangJava: {"line_comment", "block_comment"},
}

// text returns the source slice covered by a node.
func text(src []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return string(src[n.StartByte():n.EndByte()])
}

// buildSuppressions walks the tree and collects all suppression regions.
func buildSuppressions(lang parser.LanguageID, src []byte, root *sitter.Node) []suppression {
	kinds, ok := commentKinds[lang]
	if !ok {
		return nil
	}
	isComment := map[string]bool{}
	for _, k := range kinds {
		isComment[k] = true
	}
	lines := strings.Split(string(src), "\n")

	var out []suppression
	var openStart, openEnd int
	var openRules []string
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if !isComment[n.Kind()] {
			for i := uint(0); i < n.ChildCount(); i++ {
				walk(n.Child(i))
			}
			return
		}
		line := int(n.StartPosition().Row) + 1
		m := directiveRe.FindStringSubmatch(text(src, n))
		if m == nil {
			return
		}
		kind, rest := m[1], strings.TrimSpace(m[2])
		switch kind {
		case "-begin":
			if openStart == 0 {
				openStart, openRules, openEnd = line, parseRuleList(rest), 0
			}
		case "-end":
			if openStart != 0 && openEnd == 0 {
				openEnd = line
				out = append(out, suppression{start: openStart, end: openEnd, rules: openRules})
			}
		default:
			out = append(out, lineSuppression(lines, n, line, parseRuleList(rest)))
		}

	}
	walk(root)
	if openStart != 0 && openEnd == 0 {
		// Unmatched begin stays open until EOF.
		out = append(out, suppression{start: openStart, end: int(root.EndPosition().Row) + 1, rules: openRules})
	}
	return out
}

// parseRuleList splits the comma-separated rule field; empty or "all" means
// every rule. Anything after the first whitespace group is a reason.
func parseRuleList(rest string) []string {
	if rest == "" {
		return nil // all
	}
	field := rest
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		field = rest[:i]
	}
	var rules []string
	for _, r := range strings.Split(field, ",") {
		r = strings.TrimSpace(r)
		if r == "" || strings.EqualFold(r, "all") {
			return nil
		}
		rules = append(rules, r)
	}
	return rules
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

// suppress filters findings through the suppression regions, returning the
// kept findings and how many were suppressed.
func suppress(findings []model.Finding, sups []suppression) (kept []model.Finding, ignored int) {
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
			continue
		}
		kept = append(kept, f)
	}
	return kept, ignored
}

// lineSuppression builds the line-scope suppression for a directive comment:
// its own line, plus the next non-blank line when the comment starts its
// own line (put it directly above a declaration to cover its verdicts).
func lineSuppression(lines []string, n *sitter.Node, line int, rules []string) suppression {
	start, end := line, line
	if lineStartBlank(lines, n) {
		if nl := nextNonBlankLine(lines, line); nl > line {
			end = nl
		}
	}
	return suppression{start: start, end: end, rules: rules}
}
