package cockpit

// The catalogue half of the rules API: which languages and rules the tool can
// run, what the Chinese copy covers, and which fields the page reads. These are
// separated from rules_api_test.go because they assert the inventory contract
// (13 languages, 61 rules, one row per field the frontend reads), while that
// file asserts the handler contract (scope resolution, 400s, severity
// precedence) — and the inventory grows with every language pack.

import (
	"encoding/json"
	"testing"

	"metricgate/internal/parser"
	"metricgate/internal/rules"
)

// TestRuleChineseTablesCoverEveryBuiltinRule is the only thing standing between
// the Chinese catalogue and a half-translated page. The tables are keyed by rule
// id and written by hand, so an id that never got a row would render as English
// on a page that otherwise promises Chinese — a defect no other test can see,
// because the fallback is deliberate and silent.
//
// It walks the packs rather than a copied list: a copied list is a third
// inventory that drifts, and the whole point is to catch the rule nobody
// remembered.
func TestRuleChineseTablesCoverEveryBuiltinRule(t *testing.T) {
	checked := builtinRuleIDs(t)
	for id := range checked {
		title, description := ruleChineseCopy(id)
		if title == "" || description == "" {
			t.Errorf("rule %q is missing Chinese copy (title %q, description %q)", id, title, description)
		}
	}
	// The tables must not grow rows for rules that no longer exist: a stale row
	// is a translation of something the tool stopped detecting.
	for _, table := range []map[string]string{ruleTitlesZh, ruleDescriptionsZh} {
		for id := range table {
			if !checked[id] {
				t.Errorf("the Chinese tables carry %q, which no language's pack has", id)
			}
		}
	}
}

// builtinRuleIDs walks every registered language's pack and returns the rule ids
// it found. Walking is shared by three tests, and each of them would otherwise
// repeat the nil-rule guard that makes the walk itself the interesting part.
func builtinRuleIDs(t *testing.T) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	for _, lang := range parser.Supported() {
		for _, rule := range rules.ForLanguage(lang) {
			if rule == nil {
				continue
			}
			found[rule.ID] = true
		}
	}
	if len(found) == 0 {
		t.Fatal("no rules walked: the packs are empty, so this test proves nothing")
	}
	return found
}

// TestRulesCatalogueCoversEveryRegisteredLanguage pins the change that made the
// page a catalogue: the rows are the tool's inventory, not the policy's. A
// policy that configures one language must still be answered with all thirteen,

// TestRulesGroupsSeparateConfiguredFromCatalogued is the distinction the filter
// menu is built on: "the policy wrote this language" and "the tool has rules for
// this language" are different facts, and collapsing them is what made an
// 13-language tool look like a 3-language one.
func TestRulesGroupsSeparateConfiguredFromCatalogued(t *testing.T) {
	root := rulesPolicyRepo(t, rulesGoPolicy)
	chdir(t, root)
	srv := newLocalCockpit(t)

	view := getRulesView(t, srv, "/api/rules?scope=all")
	groups := groupsByLanguage(view.LanguageGroups)
	// The policy wrote go and cobol; cobol has no pack, so exactly go is
	// configured.
	if !groups["go"].Configured {
		t.Error("go is in the policy, so its group must say so")
	}
	if groups["java"].Configured {
		t.Error("java is not in the policy, so its group must not claim to be configured")
	}
	if groups["java"].EnabledCount == 0 {
		t.Error("java's rules are built in and on by default; the group must count them as enabled")
	}
	for _, lang := range view.Languages {
		if lang == "cobol" {
			t.Fatalf("languages = %v: a language with no rule pack must not appear", view.Languages)
		}
	}
	if configuredGroups(view.LanguageGroups) >= len(view.Languages) {
		t.Errorf("configured = %d of %d languages: the catalogue must list unconfigured languages too",
			configuredGroups(view.LanguageGroups), len(view.Languages))
	}
}

// TestRulesCarriesThePackAttributesThePageShows pins the fields that were
// missing from the old payload: without them the page could only render a rule's
// name and threshold, and "why is this rule even here" had no answer on screen.
func TestRulesCarriesThePackAttributesThePageShows(t *testing.T) {
	root := rulesPolicyRepo(t, rulesGoPolicy)
	chdir(t, root)
	srv := newLocalCockpit(t)

	view := getRulesView(t, srv, "/api/rules?scope=all")

	// todo-comment is the one built-in rule that defaults off, so it is the
	// pair that proves enabled and defaultEnabled are reported separately.
	todo, ok := findRule(view, "go", "todo-comment")
	if !ok {
		t.Fatal("todo-comment must be in the catalogue")
	}
	if todo.DefaultEnabled {
		t.Error("todo-comment ships disabled; defaultEnabled must say so")
	}
	if todo.AnchorCapture == "" {
		t.Error("todo-comment has a single capture, so anchorCapture must name it")
	}
	// unknown-dependency is query-backed: the page renders its pattern, so an
	// empty query would leave that column blank for a rule that has one.
	dep, ok := findRule(view, "go", "unknown-dependency")
	if !ok {
		t.Fatal("unknown-dependency must be in the catalogue")
	}
	if dep.Query == "" {
		t.Error("unknown-dependency is matched by a tree-sitter query; query must carry it")
	}
	// call-chain-depth is the rule most likely to be decided in Go rather than by
	// a query, so it is the one worth pinning: measured 2026-10-16, it is
	// query-backed like every other built-in (61 of 61 have a query). The page
	// therefore shows a pattern for it, and a regression that dropped the query
	// field would leave that column blank.
	chain, ok := findRule(view, "go", "call-chain-depth")
	if !ok {
		t.Fatal("call-chain-depth must be in the catalogue")
	}
	if chain.Query == "" {
		t.Error("call-chain-depth is matched by a tree-sitter query; query must carry it")
	}
}

// TestRulesPayloadCarriesChineseCopyOnEveryRow proves the merge happened on the
// wire, not just in the tables: the coverage test can pass while the handler
// forgets to look the copy up.
func TestRulesPayloadCarriesChineseCopyOnEveryRow(t *testing.T) {
	root := rulesPolicyRepo(t, rulesGoPolicy)
	chdir(t, root)
	srv := newLocalCockpit(t)

	view := getRulesView(t, srv, "/api/rules?scope=all")
	for _, r := range view.Rules {
		if r.TitleZh == "" || r.DescriptionZh == "" {
			t.Fatalf("rule %s/%s is missing Chinese copy in the payload", r.Language, r.ID)
		}
	}
}

// TestRulePayloadCarriesEveryFieldThePageReads is the contract the frontend
// renders against. A field dropped here becomes a blank column there, and the
// page cannot tell "absent" apart from "empty", so the key set is pinned rather
// than spot-checked.
func TestRulePayloadCarriesEveryFieldThePageReads(t *testing.T) {
	root := rulesPolicyRepo(t, rulesGoPolicy)
	chdir(t, root)
	srv := newLocalCockpit(t)

	var payload struct {
		Rules []map[string]any `json:"rules"`
	}
	body := getRaw(t, srv, "/api/rules?scope=all")
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode /api/rules: %v", err)
	}
	if len(payload.Rules) == 0 {
		t.Fatal("no rules in the payload; this check would pass vacuously")
	}
	want := []string{
		"alternateQuery", "anchorCapture", "defaultEnabled", "descriptionZh", "enabled",
		"error", "id", "language", "note", "origin", "policySource", "query",
		"reason", "severity", "title", "titleZh", "warn",
	}
	for i, row := range payload.Rules {
		for _, key := range want {
			if _, ok := row[key]; !ok {
				t.Fatalf("rule %d is missing %q in the payload", i, key)
			}
		}
		for key := range row {
			if !containsString(want, key) {
				t.Errorf("rule %d carries %q, which no page code reads; drop it or document it", i, key)
			}
		}
	}
}

// TestRulesReportsAnUnconfiguredLanguageAsSuch is the honest-label contract: a

// TestRulesDistinguishesDisabledFromUnconfigured is the distinction the whole
// page hangs on, and the one the objective got wrong: "not configured" is not
// "not running". Every language carries exactly one rule that is off by default
// (todo-comment), so a catalogue that collapsed the two states would show 44
// rules as off while 34 of them are actively firing.
func TestRulesDistinguishesDisabledFromUnconfigured(t *testing.T) {
	root := rulesPolicyRepo(t, rulesGoPolicy)
	chdir(t, root)
	srv := newLocalCockpit(t)

	view := getRulesView(t, srv, "/api/rules?scope=all")

	enabled, disabled := 0, 0
	for _, r := range view.Rules {
		if r.Enabled {
			enabled++
		} else {
			disabled++
		}
	}
	if len(view.Rules) != 61 {
		t.Fatalf("rules = %d, want the full 13-language pack of 61", len(view.Rules))
	}
	if disabled != 13 {
		t.Errorf("disabled = %d, want 13 (one todo-comment per language)", disabled)
	}
	if enabled != 48 {
		t.Errorf("enabled = %d, want 48: the rules of unconfigured languages run at their pack default", enabled)
	}
	// The unconfigured languages' rules are mostly *on*, which is exactly what
	// makes "unconfigured == disabled" a dangerous reading.
	onInUnconfigured := 0
	for _, r := range view.Rules {
		if r.Reason == "language_not_configured" && r.Enabled {
			onInUnconfigured++
		}
	}
	if onInUnconfigured == 0 {
		t.Error("rules of unconfigured languages are running by default; a page that marks them off misleads")
	}
}

// groupsByLanguage indexes the catalogue's language rows by language.
func groupsByLanguage(groups []languageGroup) map[string]languageGroup {
	out := map[string]languageGroup{}
	for _, g := range groups {
		out[g.Language] = g
	}
	return out
}

// configuredGroups counts the languages the policy actually named.
func configuredGroups(groups []languageGroup) int {
	n := 0
	for _, g := range groups {
		if g.Configured {
			n++
		}
	}
	return n
}

func keysOfGroups(groups []languageGroup) []string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		out = append(out, g.Language)
	}
	return out
}
