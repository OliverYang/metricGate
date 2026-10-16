package cockpit

// The rule catalogue is what replaced the flat list of policy keys: a row per
// rule the tool can run, carrying the threshold that actually applies and where
// it came from. Three things decide whether that page is honest, and all three
// are asserted here rather than left to the UI: the catalogue is non-empty for a
// real policy, it reads the policy of the scope being asked about (not the one
// the server happens to be sitting in), and a scope outside the whitelist is
// still a 400 rather than an empty catalogue that looks like "no rules exist".

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"metricgate/internal/gittest"
	"metricgate/internal/model"
	"metricgate/internal/store"
)

// rulesPolicyRepo builds a repository whose policy configures go (with an
// explicit threshold) and a language no rule pack exists for, so the catalogue
// has to skip the unknown key without turning it into an empty group.
func rulesPolicyRepo(t *testing.T, body string) string {
	t.Helper()
	root := gittest.InitRepo(t)
	gittest.Write(t, root, ".metricgate.yaml", body)
	gittest.Write(t, root, "svc/a.go", "package svc\n\nfunc Bad() int { return 1 }\n")
	gittest.Run(t, root, "add", "-A")
	gittest.Run(t, root, "commit", "-qm", "rules fixture")
	return root
}

const rulesGoPolicy = `version: 1
languages:
  go:
    rules:
      call-chain-depth:
        severity: error
    metrics:
      cyclomatic-complexity: {warn: 3, error: 5}
  cobol:
    metrics:
      file-length: {warn: 10, error: 20}
`

func getRulesView(t *testing.T, srv localCockpit, target string) rulesView {
	t.Helper()
	res := srv.get(target)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d for %s", res.StatusCode, target)
	}
	view := decodeRulesView(t, res)
	return view
}

// TestRulesCatalogueIsNotEmptyForTheGlobalScope pins the one thing that made the
// old rules page useless: it must answer with real rows, not an empty array that
// reads as "this repository has no rules".
func TestRulesCatalogueIsNotEmptyForTheGlobalScope(t *testing.T) {
	root := rulesPolicyRepo(t, rulesGoPolicy)
	chdir(t, root)
	srv := newLocalCockpit(t)

	view := getRulesView(t, srv, "/api/rules?scope=all")

	if len(view.Rules) == 0 {
		t.Fatal("the catalogue must list the built-in rules of every configured language")
	}
	if len(view.Languages) == 0 {
		t.Fatal("the configured languages must be listed so the UI can group the rows")
	}
	for _, lang := range view.Languages {
		if lang == "cobol" {
			t.Fatalf("languages = %v: a language with no rule pack must not appear as an empty group", view.Languages)
		}
	}
	// Every row must be answerable: a rule with no configured severity is a rule
	// the UI cannot colour.
	for _, r := range view.Rules {
		if r.ID == "" || r.Language == "" || r.Severity == "" {
			t.Fatalf("rule row = %+v: id, language and severity are all required", r)
		}
	}
}

// TestRulesHonoursAnExplicitSeverity checks the merge itself: the policy wrote
// a severity for a rule, and the catalogue must report that value and its
// provenance rather than the pack's default.
func TestRulesHonoursAnExplicitSeverity(t *testing.T) {
	root := rulesPolicyRepo(t, rulesGoPolicy)
	chdir(t, root)
	srv := newLocalCockpit(t)

	view := getRulesView(t, srv, "/api/rules?scope=all")

	entry, ok := findRule(view, "go", "call-chain-depth")
	if !ok {
		t.Fatal("call-chain-depth must be in the catalogue")
	}
	if entry.Severity != "error" {
		t.Errorf("severity = %q, want the policy's error", entry.Severity)
	}
	if entry.PolicySource == "" || entry.PolicySource == "builtin" {
		t.Errorf("policySource = %q, want the policy file to be named", entry.PolicySource)
	}
	if entry.Reason == "" {
		t.Error("reason must say why this value applies")
	}
	if entry.Title == "" {
		t.Error("title is the one human-readable sentence about what the rule detects; it must come from the pack")
	}
}

// TestRulesReadsThePolicyOfTheScopedProject is the scope contract: the catalogue
// for another repository is that repository's, so the page can never grade a
// project against thresholds belonging to the one the server was started in.
func TestRulesReadsThePolicyOfTheScopedProject(t *testing.T) {
	first := rulesPolicyRepo(t, rulesGoPolicy)
	chdir(t, first)
	other := rulesPolicyRepo(t, `version: 1
languages:
  go:
    metrics:
      cyclomatic-complexity: {warn: 99, error: 199}
`)
	dbPath := filepath.Join(t.TempDir(), "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// The scope whitelist is the run history, so the second repository has to be
	// in it for a scope naming it to be anything other than a 400.
	if _, err := st.SaveRunForProject(&model.RunSummary{Files: 1}, "test", "", other); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	global := getRulesView(t, newLocalCockpit(t), "/api/rules?scope=all")
	scoped := getRulesView(t, localCockpit{handler: Handler("", dbPath), base: "http://127.0.0.1"},
		"/api/rules?scope="+other)

	if scoped.PolicyPath == global.PolicyPath {
		t.Fatalf("policyPath = %q for both scopes: the scoped project's own policy must be read", scoped.PolicyPath)
	}
	if filepath.Dir(scoped.PolicyPath) != other {
		t.Errorf("policyPath = %q, want a file inside %s", scoped.PolicyPath, other)
	}
	entry, ok := findRule(scoped, "go", "call-chain-depth")
	if !ok {
		t.Fatal("call-chain-depth must be in the scoped catalogue")
	}
	// The scoped policy never wrote this rule, so the catalogue must not claim
	// it did: the value falls back to the pack's own default instead.
	if entry.Severity != "warning" {
		t.Errorf("scoped severity = %q, want the built-in default because the other policy left the rule alone", entry.Severity)
	}
	if entry.PolicySource == "policy_file" {
		t.Errorf("scoped policySource = %q: the other repository's policy never wrote this rule, so it must not claim to", entry.PolicySource)
	}
}

// TestRulesRejectsAScopeOutsideTheWhitelist: an unknown scope is a 400 with the
// known-projects message, exactly like every other scoped endpoint. An empty
// catalogue here would tell the operator "this project has no rules", which is
// the answer to a question nobody asked.
func TestRulesRejectsAScopeOutsideTheWhitelist(t *testing.T) {
	root := rulesPolicyRepo(t, rulesGoPolicy)
	chdir(t, root)
	srv := newLocalCockpit(t)

	res := srv.get("/api/rules?scope=/etc")
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a scope no run history knows", res.StatusCode)
	}
	body := decodeErrorBody(t, res)
	if body == "" {
		t.Fatal("the error body must carry the message, not an empty one")
	}
	if !strings.Contains(body, "unknown scope") {
		t.Errorf("error body = %q, want the same unknown-scope message the other endpoints send", body)
	}
}

// decodeErrorBody reads a failure response the way the browser does: a JSON
// object with an "error" key. The message text is what the UI surfaces, so a
// body that is not that shape silently degrades to "请求失败 (400)".
func decodeErrorBody(t *testing.T, res *http.Response) string {
	t.Helper()
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("error body must be JSON: %v", err)
	}
	return payload.Error
}

func findRule(view rulesView, language, id string) (ruleViewEntry, bool) {
	for _, r := range view.Rules {
		if r.Language == language && r.ID == id {
			return r, true
		}
	}
	return ruleViewEntry{}, false
}

func decodeRulesView(t *testing.T, res *http.Response) rulesView {
	t.Helper()
	var view rulesView
	if err := json.NewDecoder(res.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	return view
}
