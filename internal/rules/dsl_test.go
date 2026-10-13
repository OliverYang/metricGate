package rules

import (
	"strings"
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/model"
	"metricgate/internal/parser"
	"metricgate/internal/parsetest"
)

// dslParse parses src with the language's grammar (parsetest helper).
func dslFindings(t *testing.T, lang parser.LanguageID, src string, entries []config.DslRuleConfig) []model.Finding {
	t.Helper()
	return dslFindingsNamed(t, lang, "t."+string(lang), src, entries)
}

// dslFindingsNamed is dslFindings with an explicit file name, for rules whose
// `files` scope reads the path.
func dslFindingsNamed(t *testing.T, lang parser.LanguageID, name, src string, entries []config.DslRuleConfig) []model.Finding {
	t.Helper()
	p, err := parser.New(lang)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	rs := DslRules(string(lang), p.Lang, entries)
	if len(rs) == 0 {
		t.Fatal("DslRules produced no rules")
	}
	runner, err := NewRunner(string(lang), p.Lang, rs)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	tree, release := parsetest.Parse(t, lang, src)
	defer release()
	return runner.Run(name, []byte(src), tree, nil)
}

func findingsByRulePrefix(fs []model.Finding, prefix string) []model.Finding {
	var out []model.Finding
	for _, f := range fs {
		if len(f.RuleID) >= len(prefix) && f.RuleID[:len(prefix)] == prefix {
			out = append(out, f)
		}
	}
	return out
}

func TestDslGoTestNaming(t *testing.T) {
	// A real convention: Go test functions must start with Test.
	src := `package main

func helperName(t *testing.T) {}

func TestRealThing(t *testing.T) {
	t.Error("real")
}
`
	entries := []config.DslRuleConfig{{
		ID:         "go-test-name-prefix",
		Node:       "function_declaration",
		Field:      "name",
		MatchRegex: `^Test`,
		// Only test-named files carry the convention, so exclude nothing
		// here; instead we exercise match-on-hit semantics.
	}}
	// The rule as written fires on functions whose name starts with Test —
	// i.e. it *selects* conforming functions. To assert the convention we
	// invert: fire on every function_declaration name NOT starting with
	// Test is the exclude pattern's job.
	invert := entries[0]
	invert.ID = "go-test-name-convention"
	invert.MatchRegex = ""
	invert.ExcludeRegex = `^Test`
	fs := findingsByRulePrefix(dslFindings(t, parser.LangGo, src, []config.DslRuleConfig{invert}), "custom.go-test-name-convention")
	// helperName must be flagged; TestRealThing must not.
	if len(fs) != 1 {
		t.Fatalf("findings = %d, want 1 (helperName only): %+v", len(fs), fs)
	}
	if fs[0].Message == "" || !strings.Contains(fs[0].Message, "helperName") {
		t.Fatalf("finding does not name the offender: %q", fs[0].Message)
	}
	if fs[0].StartLine != 3 {
		t.Fatalf("StartLine = %d, want 3", fs[0].StartLine)
	}
}

func TestDslGoImportBan(t *testing.T) {
	// A real convention: this project must not import a hallucinated pkg.
	src := `package main

import "hallucinated/pkg"

func main() {
	_ = pkg.Thing
}
`
	entries := []config.DslRuleConfig{{
		ID:         "no-hallucinated-import",
		Node:       "import_spec",
		Field:      "path",
		MatchRegex: `hallucinated/`,
		Severity:   "error",
		Reason:     "hallucinated packages do not exist",
	}}
	fs := findingsByRulePrefix(dslFindings(t, parser.LangGo, src, entries), "custom.no-hallucinated-import")
	if len(fs) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(fs), fs)
	}
	if fs[0].Severity != model.SevError {
		t.Fatalf("severity = %s, want error", fs[0].Severity)
	}
}

func TestDslPythonIdentifier(t *testing.T) {
	src := "def camelCaseName():\n    pass\n"
	entries := []config.DslRuleConfig{{
		ID:           "py-snake-case",
		Node:         "function_definition",
		Field:        "name",
		ExcludeRegex: `^[a-z]+(_[a-z0-9]+)*$`,
		Languages:    []string{"python"},
	}}
	fs := findingsByRulePrefix(dslFindings(t, parser.LangPy, src, entries), "custom.py-snake-case")
	if len(fs) != 1 {
		t.Fatalf("findings = %d, want 1 (camelCaseName): %+v", len(fs), fs)
	}
}

func TestDslInvalidEntriesDropped(t *testing.T) {
	p, err := parser.New(parser.LangGo)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	entries := []config.DslRuleConfig{
		{ID: "bad-node", Node: "not_a_real_node_kind"},                   // unknown kind
		{ID: "Bad_ID_Upper", Node: "call_expression"},                    // id slug violation
		{ID: "", Node: "call_expression"},                                // no id
		{ID: "no-node"},                                                  // no node kind
		{ID: "bad-regex", Node: "call_expression", MatchRegex: "("},      // bad regex
		{ID: "bad-sev", Node: "call_expression", Severity: "fatal"},      // bad severity
		{ID: "bad-field", Node: "call_expression", Field: "not_a_field"}, // unknown field
	}
	if rs := DslRules("go", p.Lang, entries); len(rs) != 0 {
		t.Fatalf("invalid entries survived: %d rules", len(rs))
	}
}

func TestDslValidRuleCompilesAndFires(t *testing.T) {
	p, err := parser.New(parser.LangGo)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	entries := []config.DslRuleConfig{{ID: "direct-print", Node: "call_expression", Field: "function", MatchRegex: `^fmt\.Print`}}
	rs := DslRules("go", p.Lang, entries)
	if len(rs) != 1 {
		t.Fatalf("valid entry dropped: %d rules", len(rs))
	}
	if rs[0].ID != "custom.direct-print" {
		t.Fatalf("rule id = %s, want custom.direct-print", rs[0].ID)
	}
	fs := findingsByRulePrefix(dslFindings(t, parser.LangGo, "package main\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n", entries), "custom.direct-print")
	if len(fs) != 1 {
		t.Fatalf("direct print not flagged: %+v", fs)
	}
}

// TestDslFileScopeRestrictsRule covers the `files` predicate: a rule scoped to
// test files must not reach production code, and one without the scope must
// behave exactly as before (the whole point of making it optional).
func TestDslFileScopeRestrictsRule(t *testing.T) {
	entry := config.DslRuleConfig{
		ID:           "test-only-naming",
		Node:         "function_declaration",
		Files:        []string{"**/*_test.go"},
		MatchRegex:   `^func\s+test[A-Za-z0-9_]*\s*\(`,
		ExcludeRegex: `^func\s+Test`,
	}
	// The file name is what dslFindings passes through Runner.Run, so the same
	// source is checked under a test name and a production name.
	src := "package main\n\nfunc testHelper(t *testing.T) {}\n"
	if fs := findingsByRulePrefix(dslFindingsNamed(t, parser.LangGo, "internal/x/a_test.go", src, []config.DslRuleConfig{entry}), "custom.test-only-naming"); len(fs) != 1 {
		t.Fatalf("test file: findings = %d, want 1: %+v", len(fs), fs)
	}
	if fs := findingsByRulePrefix(dslFindingsNamed(t, parser.LangGo, "internal/x/a.go", src, []config.DslRuleConfig{entry}), "custom.test-only-naming"); len(fs) != 0 {
		t.Fatalf("production file: findings = %d, want 0 (files scope must exclude it): %+v", len(fs), fs)
	}
	// Unscoped rules keep firing everywhere — no behavior change for policies
	// that never set files.
	entry.Files = nil
	if fs := findingsByRulePrefix(dslFindingsNamed(t, parser.LangGo, "internal/x/a.go", src, []config.DslRuleConfig{entry}), "custom.test-only-naming"); len(fs) != 1 {
		t.Fatalf("unscoped rule: findings = %d, want 1: %+v", len(fs), fs)
	}
}
