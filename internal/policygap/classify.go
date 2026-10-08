package policygap

import (
	"fmt"
	"sort"
	"strings"

	"metricgate/internal/config"
	"metricgate/internal/engine"
	"metricgate/internal/metrics"
	"metricgate/internal/rules"
)

func auditRules(in Input, fired map[string]int) []RuleAudit {
	var out []RuleAudit
	for _, m := range in.Catalog {
		if m.Language == "" {
			continue // whole-graph: graphRules owns it
		}
		out = append(out, classify(in, m, fired[m.ID]))
	}
	for _, g := range graphRules {
		out = append(out, classifyGraph(in, g, fired[g.ID]))
	}
	out = collapseByID(out, fired)
	sort.Slice(out, func(i, j int) bool {
		if out[i].RuleID != out[j].RuleID {
			return out[i].RuleID < out[j].RuleID
		}
		return out[i].Language < out[j].Language
	})
	return out
}

// collapseByID gives every rule exactly one verdict.
//
// The catalogue holds one entry per rule *per language*, and a whole-graph rule
// can appear in both the catalogue and the graphRules table. Reporting all of
// them turns one question — "can this rule fire?" — into several lines that
// disagree. So the entries of one rule id are merged: the most alive status
// wins, and the finding count is the rule's total.
func collapseByID(all []RuleAudit, fired map[string]int) []RuleAudit {
	byID := map[string][]RuleAudit{}
	var order []string
	for _, a := range all {
		if _, ok := byID[a.RuleID]; !ok {
			order = append(order, a.RuleID)
		}
		byID[a.RuleID] = append(byID[a.RuleID], a)
	}
	out := make([]RuleAudit, 0, len(order))
	for _, id := range order {
		out = append(out, collapseOne(id, byID[id], fired[id]))
	}
	return out
}

// collapseOne merges one rule's entries, keeping the most alive status: a rule
// that can fire is described by what it did, not by the language in which it
// cannot.
func collapseOne(id string, group []RuleAudit, n int) RuleAudit {
	best, applicable := -1, 0
	for i, a := range group {
		if a.Status == StatusNoFiles {
			continue
		}
		applicable++
		if best < 0 || statusRank(a) < statusRank(group[best]) {
			best = i
		}
	}
	if best < 0 {
		one := group[0]
		one.Language = ""
		one.Findings = 0
		one.Detail = "no scanned " + strings.Join(languagesOf(group), "/") + " files"
		return one
	}
	merged := group[best]
	merged.Findings = n
	if applicable > 1 {
		// The count is now the rule's total, so naming one language would
		// misattribute it.
		merged.Language = ""
	}
	return merged
}

// statusRank orders statuses by how much the rule can still do: lower wins.
func statusRank(a RuleAudit) int {
	switch a.Status {
	case StatusFiring:
		return 0
	case StatusArmed:
		return 1
	case StatusReportOnly:
		return 2
	case StatusDisabled:
		return 3
	case StatusUndeclared:
		return 4
	case StatusFlagOnly:
		return 5
	default:
		return 6
	}
}

// languagesOf lists a rule's languages in a stable order.
func languagesOf(group []RuleAudit) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range group {
		if a.Language != "" && !seen[a.Language] {
			seen[a.Language] = true
			out = append(out, a.Language)
		}
	}
	sort.Strings(out)
	return out
}

// classify decides why one catalogued rule fired or did not.
func classify(in Input, m rules.Meta, n int) RuleAudit {
	a := RuleAudit{RuleID: m.ID, Title: m.Title, Language: m.Language, Findings: n}
	// Applicability is checked before the finding count: findings are tallied
	// per rule id, so a rule that fired in Go would otherwise look like it also
	// fired for Java, in a repository where no Java file was scanned.
	if m.Language != "" && !contains(in.Languages, m.Language) {
		a.Status = StatusNoFiles
		a.Findings = 0
		a.Detail = "no scanned " + m.Language + " files"
		return a
	}
	if n > 0 {
		a.Status = StatusFiring
		return a
	}
	if reason, off := disabledReason(in.Config, m); off {
		a.Status = StatusDisabled
		a.Detail = reason
		return a
	}
	// A metric with both thresholds at 0 is on but toothless: every measured
	// value is recorded and none can cross a line. Maintainability ships that
	// way on purpose, which is exactly why it is its own status — an armed rule
	// that found nothing and a rule that cannot find anything are different
	// answers to "is this covered?".
	if isMetric(m.Language, m.ID) {
		// The engine's own resolution, not the policy's raw numbers: a policy
		// that never mentions a metric leaves the built-in threshold in force,
		// and reading the policy alone would call that toothless.
		if warn, errAt := engine.EffectiveThresholds(in.Config, m.Language, m.ID); warn == 0 && errAt == 0 {
			a.Status = StatusReportOnly
			a.Detail = "no threshold set (warn=0, error=0)"
			return a
		}
	}
	a.Status = StatusArmed
	return a
}

// isMetric reports whether an id is one of a language's threshold metrics.
// MetricThresholds answers 0,0 for any id it does not know, so without this
// check every AST rule would look toothless.
func isMetric(lang, id string) bool {
	for _, m := range metrics.IDsFor(lang) {
		if m == id {
			return true
		}
	}
	return false
}

// classifyGraph decides why one whole-graph rule fired or did not. A quiet one
// is either switched off (a decision), never armed (a gap), or unreachable
func disabledReason(cfg *config.Config, m rules.Meta) (string, bool) {
	if enabled := cfg.RuleEnabled(m.Language, m.ID); enabled != nil && !*enabled {
		return fmt.Sprintf("languages.%s.rules.%s.enabled: false", m.Language, m.ID), true
	}
	if _, _, enabled := cfg.MetricThresholds(m.Language, m.ID); enabled != nil && !*enabled {
		return fmt.Sprintf("languages.%s.metrics.%s.enabled: false", m.Language, m.ID), true
	}
	return "", false
}

// auditLayers reports which packages the declared layers actually cover. It
// calls arch.AssignLayers rather than re-implementing pattern matching, so the
