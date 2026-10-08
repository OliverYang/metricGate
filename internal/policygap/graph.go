package policygap

import (
	"metricgate/internal/arch"
	"metricgate/internal/clones"
	"metricgate/internal/config"
	"metricgate/internal/deps"
	"metricgate/internal/rules"
)

func classifyGraph(in Input, g graphRule, n int) RuleAudit {
	a := RuleAudit{RuleID: g.ID, Title: g.Title, Findings: n}
	switch {
	case n > 0:
		a.Status = StatusFiring
		return a
	case g.OffReason(in.Config) != "":
		a.Status = StatusDisabled
		a.Detail = g.OffReason(in.Config)
		return a
	case !g.Armed(in):
		a.Status = StatusUndeclared
		a.Detail = "declare " + g.Arms
		if !g.InCheck {
			a.Status = StatusFlagOnly
			a.Detail = "only runs with " + g.Arms
		}
		return a
	}
	a.Status = StatusArmed
	return a
}

// graphRule is a rule that does not belong to a per-language pack: it fires
// over the whole graph, over a diff, or only when a pass is declared.
type graphRule struct {
	ID    string
	Title string
	// Arms is the policy key — or the flag — that makes the rule able to fire.
	Arms string
	// InCheck reports whether a plain `check` run exercises it. A rule that
	// needs a flag would otherwise look dead in every audit.
	InCheck bool
	// Armed reports whether this policy arms the rule.
	Armed func(in Input) bool
	// OffReason reports the policy key that switched the rule off, or "" when
	// it was never switched off — only a deliberate switch is StatusDisabled;
	// never declaring the section is StatusUndeclared, the gap worth closing.
	OffReason func(cfg *config.Config) string
}

var graphRules = []graphRule{
	{
		ID: arch.RuleLayerViolation, Title: "Dependency crosses a declared layer boundary",
		Arms: "architecture.layers", InCheck: true,
		Armed:     func(in Input) bool { return len(in.Config.ArchPolicy().Layers) > 0 },
		OffReason: func(*config.Config) string { return "" },
	},
	{
		ID: arch.RuleForbiddenEdge, Title: "Dependency direction forbidden by policy",
		Arms: "architecture.forbid", InCheck: true,
		Armed:     func(in Input) bool { return len(in.Config.ArchPolicy().Forbids) > 0 },
		OffReason: func(*config.Config) string { return "" },
	},
	{
		ID: arch.RuleImportCycle, Title: "Package import cycle",
		Arms: "architecture.layers or architecture.forbid", InCheck: true,
		Armed:     func(in Input) bool { return !in.Config.ArchPolicy().Empty() },
		OffReason: func(*config.Config) string { return "" },
	},
	{
		ID: arch.RuleSdp, Title: "Package depends on a less stable package",
		Arms: "architecture.sdp", InCheck: true,
		Armed: func(in Input) bool { return in.Config.Architecture.SDP != "off" },
		OffReason: func(cfg *config.Config) string {
			if cfg.Architecture.SDP == "off" {
				return "architecture.sdp: off"
			}
			return ""
		},
	},
	{
		ID: rules.RuleUnknownDependency, Title: "Import outside the declared dependency allowlist",
		Arms: "dependencies.allow / dependencies.self (or a go.mod)", InCheck: true,
		Armed: func(in Input) bool { return len(deps.ForRoot(in.Root, in.Config).Prefixes()) > 0 },
		// An empty allowlist means the rule cannot fire at all, which is a
		// missing declaration rather than a decision to silence it.
		OffReason: func(*config.Config) string { return "" },
	},
	{
		ID: clones.RuleFunctionClone, Title: "Function duplicates an existing function (Type-2 clone)",
		Arms: "clones.enabled", InCheck: true,
		Armed: func(in Input) bool { return in.Config.ClonePolicy().Enabled() },
		OffReason: func(cfg *config.Config) string {
			if !cfg.ClonePolicy().Enabled() {
				return "clones.enabled: false"
			}
			return ""
		},
	},
	{
		ID: "metric-regression", Title: "Metric got worse than the base revision",
		Arms: "check -diff -regress", InCheck: false,
		Armed:     func(Input) bool { return false },
		OffReason: func(*config.Config) string { return "" },
	},
}

// GraphRule is one whole-graph rule and whether a policy arms it, for a caller
// that only wants to describe the policy and not run it.
type GraphRule struct {
	ID      string `json:"rule_id"`
	Title   string `json:"title"`
	ArmedBy string `json:"armed_by"`
	InCheck bool   `json:"in_check"`
	Armed   bool   `json:"armed"`
}

// GraphRules reports every whole-graph rule for a policy. It is the same table
// the audit classifies, so `rules explain list` and `rules gaps` cannot
// disagree about which architecture rules are live.
func GraphRules(cfg *config.Config, root string) []GraphRule {
	in := Input{Root: root, Config: cfg}
	out := make([]GraphRule, 0, len(graphRules))
	for _, g := range graphRules {
		out = append(out, GraphRule{
			ID: g.ID, Title: g.Title, ArmedBy: g.Arms,
			InCheck: g.InCheck, Armed: g.Armed(in),
		})
	}
	return out
}

// disabledReason reports why the policy switched a per-language rule off, or
// ("", false). Whole-graph rules are answered by graphRule.OffReason instead,
