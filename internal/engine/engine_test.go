package engine

import (
	"strings"
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/model"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Exclude = nil // include testdata/
	return cfg
}

func countsByLang(s *model.RunSummary) (goCounts, javaCounts map[string]int) {
	goCounts, javaCounts = map[string]int{}, map[string]int{}
	for _, f := range s.Findings {
		if f.Source != model.SrcRule {
			continue
		}
		if strings.HasSuffix(f.File, ".go") {
			goCounts[f.RuleID]++
		} else {
			javaCounts[f.RuleID]++
		}
	}
	return goCounts, javaCounts
}

func checkCounts(t *testing.T, want, got map[string]int, label string) {
	t.Helper()
	for id, n := range want {
		if got[id] != n {
			t.Errorf("%s %s = %d, want %d (all: %v)", label, id, got[id], n, got)
		}
	}
}

func TestRunOnTestdata(t *testing.T) {
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	s, err := eng.Run("testdata")
	if err != nil {
		t.Fatal(err)
	}
	// sample.go + java/Sample.java + the arch fixture (3) + the archsup fixture (2)
	if s.Files != 7 {
		t.Fatalf("files = %d, want 7", s.Files)
	}
	if n := len(s.ParseErrors); n != 0 {
		t.Fatalf("unexpected parse errors: %v", s.ParseErrors)
	}
	if s.Count(model.SevError) != 0 {
		t.Errorf("errors = %d, want 0", s.Count(model.SevError))
	}

	goCounts, javaCounts := countsByLang(s)
	checkCounts(t, map[string]int{
		"banned-fmt-Print*": 1,
		"empty-block":       2,
		"banned-panic":      1,
	}, goCounts, "go")
	checkCounts(t, map[string]int{
		"banned-System-out-print*": 1,
		"print-stack-trace":        1,
		"empty-block":              2,
	}, javaCounts, "java")

	var paramsFinding bool
	for _, f := range s.Findings {
		if f.RuleID == "params-count" && strings.Contains(f.Message, "many") {
			paramsFinding = true
		}
	}
	if !paramsFinding {
		t.Error("expected params-count finding for Sample.many")
	}
}

func TestTodoRuleToggle(t *testing.T) {
	cfg := testConfig(t)
	enabled := true
	cfg.Languages["go"] = config.LanguageConfig{
		Rules: map[string]config.RuleConfig{
			"todo-comment": {Enabled: &enabled},
		},
	}
	cfg.Languages["java"] = config.LanguageConfig{
		Rules: map[string]config.RuleConfig{
			"todo-comment": {Enabled: &enabled},
		},
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	s, err := eng.Run("testdata")
	if err != nil {
		t.Fatal(err)
	}
	goCounts, javaCounts := countsByLang(s)
	if goCounts["todo-comment"] != 1 {
		t.Errorf("go todo-comment = %d, want 1", goCounts["todo-comment"])
	}
	if javaCounts["todo-comment"] != 1 {
		t.Errorf("java todo-comment = %d, want 1", javaCounts["todo-comment"])
	}
}
