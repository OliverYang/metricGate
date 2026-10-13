// Package policygap answers the question a finding list cannot: "what is this
// policy not covering?"
//
// A gate that reports nothing is indistinguishable, from its output alone, from
// a gate that has nothing to check. Every rule that produced no finding is
// either armed and clean (fine), switched off on purpose (a decision), not
// applicable here (no files of its language), or — the case this package exists
// for — silent because the policy never declared the section that arms it.
// Collapsing those four into "0 findings" is how a tool quietly loses most of
// its value while still reporting PASS on every run.
//
// It is a pure package: it reads a catalogue, a run summary, a package list and
// a policy, and returns a report. It prints nothing and writes nothing.
package policygap

import (
	"metricgate/internal/config"
	"metricgate/internal/model"
	"metricgate/internal/owners"
	"metricgate/internal/rules"
)

// Status is one rule's verdict in an audit. The non-firing statuses are the
// point of the package: they are the different reasons a rule can be quiet, and
// they call for opposite responses — only one of them is a gap to close.
type Status string

const (
	// StatusFiring: the rule produced findings in this run.
	StatusFiring Status = "firing"
	// StatusArmed: enabled and applicable, and found nothing. No action.
	StatusArmed Status = "armed"
	// StatusDisabled: the policy switched it off. A decision, not a gap.
	StatusDisabled Status = "disabled"
	// StatusUndeclared: silent because the policy never declared the section
	// that arms it. This is the gap worth acting on.
	StatusUndeclared Status = "undeclared"
	// StatusReportOnly: enabled, but no threshold is set, so it can never
	// produce a finding. Not a gap and not a decision — a rule that is wired
	// up and switched on without anything to compare against.
	StatusReportOnly Status = "report_only"
	// StatusFlagOnly: only runs under a flag a plain `check` does not pass, so
	// "no findings" says nothing about it either way.
	StatusFlagOnly Status = "flag_only"
	// StatusNoFiles: no scanned file of the rule's language.
	StatusNoFiles Status = "no_files"
)

// hotShare is the share of a metric's measured scopes above which the report
// calls the rule hot. A metric that trips on more than a quarter of everything
// it measures is usually reporting a threshold that no longer describes the
// code — not a quarter of the codebase that is independently bad.
const hotShare = 0.25

// hotMinFindings keeps "2 of 3 scopes" out of the list: a share only means
// something with volume behind it.
const hotMinFindings = 3

// Input is everything an audit reads. It is a struct rather than a long
// argument list so a caller can see, in one place, what the verdict depends on.
type Input struct {
	Root      string
	Config    *config.Config
	Catalog   []rules.Meta // every rule that can fire under this policy
	Summary   *model.RunSummary
	Packages  []string // dependency-graph nodes
	Languages []string // languages the run actually scanned
	// Codeowners routes the audit's firing rules to the people CODEOWNERS
	// names. It lives in Input rather than as a second argument because the
	// audit's value is that it reads one run: a gap list you must re-derive
	// from a separate lookup is a gap list nobody sends. An empty map disables
	// the section, which is honest rather than silently empty.
	Codeowners owners.Map
}

// RuleAudit is one rule's verdict.
type RuleAudit struct {
	RuleID   string `json:"rule_id"`
	Title    string `json:"title,omitempty"`
	Language string `json:"language,omitempty"`
	Status   Status `json:"status"`
	Findings int    `json:"findings"`
	// Detail says why a rule is not firing, or how to arm it. Empty for a rule
	// that is firing or an armed rule that found nothing.
	Detail string `json:"detail,omitempty"`
}

// HotRule is a metric rule firing on a large share of everything it measures.
type HotRule struct {
	RuleID   string  `json:"rule_id"`
	Findings int     `json:"findings"`
	Scopes   int     `json:"scopes"`
	Share    float64 `json:"share"`
}

// RuleCount is one rule's tally, for suppression counts.
type RuleCount struct {
	RuleID string `json:"rule_id"`
	Count  int    `json:"count"`
}

// LayerAudit is one declared layer and the packages it actually covers.
type LayerAudit struct {
	Name     string   `json:"name"`
	Packages []string `json:"packages"`
}

// Report is a policy-completeness audit of one run.
type Report struct {
	Root       string       `json:"root"`
	Files      int          `json:"files"`
	Packages   int          `json:"packages"`
	Catalogued int          `json:"catalogued"`
	Firing     int          `json:"firing"`
	Rules      []RuleAudit  `json:"rules"`
	Layers     []LayerAudit `json:"layers,omitempty"`
	Unlayered  []string     `json:"unlayered_packages,omitempty"`
	Hot        []HotRule    `json:"hot_rules,omitempty"`
	Suppressed []RuleCount  `json:"suppressed,omitempty"`
	// Owners routes the firing rules to the inboxes CODEOWNERS names. An owner
	// with no rules is not listed, and a rule with no owner is not listed either
	// — Unattributed is that count, reported rather than hidden in a bucket.
	Owners []OwnerGroup `json:"owners,omitempty"`
	// Unattributed counts catalogued rules that fired nowhere and so cannot be
	// routed. It is deliberately separate from an unowned file: a silent rule
	// is not a missing owner, it is a rule that had nothing to say.
	Unattributed int `json:"unattributed,omitempty"`
}

// Analyze audits one run. Every list in the result is sorted, so two audits of
// the same tree produce byte-identical output.
func Analyze(in Input) Report {
	fired := ruleCounts(in.Summary)
	rep := Report{
		Root:       in.Root,
		Files:      in.Summary.Files,
		Packages:   len(in.Packages),
		Catalogued: len(in.Catalog),
	}
	rep.Rules = auditRules(in, fired)
	rep.Catalogued = len(rep.Rules)
	rep.Layers, rep.Unlayered = auditLayers(in)
	rep.Hot = hotRules(in.Summary)
	rep.Suppressed = suppressed(in.Summary)
	rep.Owners, rep.Unattributed = auditOwners(in, rep.Rules)
	for _, r := range rep.Rules {
		if r.Findings > 0 {
			rep.Firing++
		}
	}
	return rep
}

// ruleCounts tallies findings by rule id.
func ruleCounts(summary *model.RunSummary) map[string]int {
	out := map[string]int{}
	for _, f := range summary.Findings {
		out[f.RuleID]++
	}
	return out
}

// auditRules classifies every per-language catalogued rule, then every
// whole-graph rule.
//
// The catalogue is not the whole story for the second group: it lists an
// architecture rule only once the policy arms it, and never lists
// unknown-dependency at all. So a whole-graph rule is classified from the
// graphRules table instead, which knows both whether the policy armed it and
