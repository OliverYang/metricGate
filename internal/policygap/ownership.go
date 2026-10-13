package policygap

import (
	"sort"

	"metricgate/internal/model"
	"metricgate/internal/owners"
)

// OwnerGroup is the slice of the audit that belongs to one owner: the rules
// whose findings landed in the files CODEOWNERS assigned to them.
//
// A rule can appear under several owners, and that is correct rather than a
// double count — one rule firing in two teams' files is two conversations.
type OwnerGroup struct {
	Owner    string   `json:"owner"`
	RuleIDs  []string `json:"rule_ids"`
	Findings int      `json:"findings"`
}

// auditOwners routes the audit's findings to the owners of the files they
// landed in, so a gap list can be sent instead of read by whoever happens to
// run the command.
//
// Only a rule that actually fired has a file to route. A rule that found
// nothing has no location, and handing a silent rule to an owner would be a
// guess wearing a report's clothes; those are counted and named instead. The
// count is returned rather than hidden inside a group, because a routing table
// with a secret gap is the thing this section exists to prevent.
func auditOwners(in Input, catalogued []RuleAudit) (groups []OwnerGroup, unattributed int) {
	if in.Codeowners.Empty() {
		return nil, len(catalogued)
	}
	byOwner := map[string]map[string]bool{}
	findings := map[string]int{}
	unattributed = len(catalogued)
	for _, a := range catalogued {
		if a.Findings > 0 {
			unattributed--
		}
	}
	for _, f := range in.Summary.Findings {
		for _, o := range in.Codeowners.OwnersOf(f.File) {
			if byOwner[o] == nil {
				byOwner[o] = map[string]bool{}
			}
			byOwner[o][f.RuleID] = true
			findings[o]++
		}
	}
	for o, ids := range byOwner {
		list := make([]string, 0, len(ids))
		for id := range ids {
			list = append(list, id)
		}
		sort.Strings(list)
		groups = append(groups, OwnerGroup{Owner: o, RuleIDs: list, Findings: findings[o]})
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Findings != groups[j].Findings {
			return groups[i].Findings > groups[j].Findings
		}
		return groups[i].Owner < groups[j].Owner
	})
	return groups, unattributed
}

// findingFiles lists the distinct files that carry at least one finding, for the
// unowned share. The audit reports files, not locations, because the routing
// question is per file: one unowned file is one missing inbox however many
// findings sit in it.
func findingFiles(summary *model.RunSummary) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range summary.Findings {
		if seen[f.File] {
			continue
		}
		seen[f.File] = true
		out = append(out, f.File)
	}
	sort.Strings(out)
	return out
}

// UnownedFindings reports how many of the files carrying findings match no
// CODEOWNERS rule: the part of the audit nobody owns, which is the number that
// decides whether the owner list above is a routing table or decoration.
func UnownedFindings(in Input) (unowned, total int) {
	if in.Codeowners.Empty() {
		return 0, 0
	}
	return in.Codeowners.UnknownShare(findingFiles(in.Summary))
}

// ownersOfRule returns the owners of the files one rule fired in, for a caller
// that wants per-rule routing rather than the grouped view.
func ownersOfRule(in Input, ruleID string) []string {
	var files []string
	for _, f := range in.Summary.Findings {
		if f.RuleID == ruleID {
			files = append(files, f.File)
		}
	}
	return in.Codeowners.Owners(files)
}

// silence keeps the owners import honest for readers of this file: the routing
// vocabulary is the same one inline bans and custom rules already use, so a
// CODEOWNERS pattern behaves like a policy file pattern rather than like a
// third dialect.
var _ = owners.Unknown
