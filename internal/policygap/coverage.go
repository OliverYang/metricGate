package policygap

import (
	"sort"

	"metricgate/internal/arch"
	"metricgate/internal/model"
)

func auditLayers(in Input) (layers []LayerAudit, unlayered []string) {
	policy := in.Config.ArchPolicy()
	pkgs := append([]string(nil), in.Packages...)
	sort.Strings(pkgs)
	layerOf := arch.AssignLayers(policy.Layers, pkgs)

	byName := map[string][]string{}
	for _, p := range pkgs {
		if l, ok := layerOf[p]; ok {
			byName[l.Name] = append(byName[l.Name], p)
		} else {
			unlayered = append(unlayered, p)
		}
	}
	for _, l := range policy.Layers {
		layers = append(layers, LayerAudit{Name: l.Name, Packages: byName[l.Name]})
	}
	sort.Slice(layers, func(i, j int) bool { return layers[i].Name < layers[j].Name })
	return layers, unlayered
}

// hotRules reports metric rules firing on a large share of their scopes.
//
// Only metric rules qualify, because only they have an exact scope count: every
// measured value is recorded as a snapshot whether or not it crossed a
// threshold, so findings/scopes is a real rate. For a rule like a banned-API
// pattern the "scopes" would be the AST nodes it could have matched, which this
// package does not have — and inventing a denominator would produce a number
// that looks like a rate without being one.
func hotRules(summary *model.RunSummary) []HotRule {
	scopes := map[string]int{}
	for _, m := range summary.Metrics {
		scopes[m.Metric]++
	}
	fired := map[string]int{}
	for _, f := range summary.Findings {
		fired[f.RuleID]++
	}
	var out []HotRule
	for id, n := range fired {
		s, ok := scopes[id]
		if !ok || s == 0 || n < hotMinFindings {
			continue
		}
		share := float64(n) / float64(s)
		if share < hotShare {
			continue
		}
		out = append(out, HotRule{RuleID: id, Findings: n, Scopes: s, Share: share})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Share != out[j].Share {
			return out[i].Share > out[j].Share
		}
		return out[i].RuleID < out[j].RuleID
	})
	return out
}

// suppressed turns the per-rule suppression tally into a sorted list. A rule
// silenced many times is a rule the team has voted against; it belongs in the
// audit because the policy should either be fixed or the rule retired.
func suppressed(summary *model.RunSummary) []RuleCount {
	out := make([]RuleCount, 0, len(summary.IgnoredByRule))
	for id, n := range summary.IgnoredByRule {
		out = append(out, RuleCount{RuleID: id, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].RuleID < out[j].RuleID
	})
	return out
}

func contains(all []string, want string) bool {
	for _, s := range all {
		if s == want {
			return true
		}
	}
	return false
}

// HotShare is the threshold hotRules applies, exposed so a caller printing a
// report can state the number instead of implying the list is self-evident.
func HotShare() float64 { return hotShare }

// HotMinFindings is the volume floor hotRules applies, for the same reason.
func HotMinFindings() int { return hotMinFindings }
