// The policy view the rules run against: the configuration document plus the
// run-scoped inputs that come from the repository instead of the policy.
package engine

import (
	"metricgate/internal/config"
	"metricgate/internal/deps"
	"metricgate/internal/rules"
)

// policy is the configuration view the rules run against: the policy document
// plus the run-scoped dependency allowlist, which no rule can derive from the
// document alone.
func (e *Engine) policy() rules.Policy {
	return allowPolicy{cfg: e.cfg, allow: e.depAllow}
}

// ensureDeps fills the dependency allowlist when Run was not the entry point
// (CheckSource checks an in-memory snippet against the working directory).
func (e *Engine) ensureDeps() {
	if e.depAllow == nil {
		e.depAllow = deps.ForRoot(".", e.cfg).Prefixes()
	}
}

// allowPolicy is config.Config plus the repository-derived dependency
// allowlist. It exists so the one rule whose answer depends on the repository
// gets that input without every other rule having to know about it.
type allowPolicy struct {
	cfg   *config.Config
	allow []string
}

func (p allowPolicy) RuleSeverity(lang, ruleID string) string {
	return p.cfg.RuleSeverity(lang, ruleID)
}
func (p allowPolicy) RuleEnabled(lang, ruleID string) *bool { return p.cfg.RuleEnabled(lang, ruleID) }

// RuleOptions injects the allowlist into the unknown-dependency rule.
func (p allowPolicy) RuleOptions(lang, ruleID string) map[string]any {
	opts := p.cfg.RuleOptions(lang, ruleID)
	if ruleID != rules.RuleUnknownDependency || len(p.allow) == 0 {
		return opts
	}
	merged := make(map[string]any, len(opts)+1)
	for k, v := range opts {
		merged[k] = v
	}
	list := make([]any, len(p.allow))
	for i, s := range p.allow {
		list[i] = s
	}
	merged["allow"] = list
	return merged
}
