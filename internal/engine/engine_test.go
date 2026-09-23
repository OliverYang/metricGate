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

func ruleCounts(s *model.RunSummary) map[string]int {
	counts := map[string]int{}
	for _, f := range s.Findings {
		if f.Source == model.SrcRule {
			counts[f.RuleID]++
		}
	}
	return counts
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
	if s.Files != 1 {
		t.Fatalf("files = %d, want 1", s.Files)
	}
	if n := len(s.ParseErrors); n != 0 {
		t.Fatalf("unexpected parse errors: %v", s.ParseErrors)
	}

	counts := ruleCounts(s)
	want := map[string]int{
		"no-fmt-print": 1,
		"empty-block":  2,
		"panic-call":   1,
	}
	for id, n := range want {
		if counts[id] != n {
			t.Errorf("%s = %d, want %d (all: %v)", id, counts[id], n, counts)
		}
	}
	if s.Count(model.SevError) != 0 {
		t.Errorf("errors = %d, want 0", s.Count(model.SevError))
	}

	// params-count: Many takes 7 params, warn threshold is 5.
	var paramsFinding bool
	for _, f := range s.Findings {
		if f.RuleID == "params-count" && strings.Contains(f.Message, "Many") {
			paramsFinding = true
		}
	}
	if !paramsFinding {
		t.Error("expected params-count finding for Many")
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
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	s, err := eng.Run("testdata")
	if err != nil {
		t.Fatal(err)
	}
	counts := ruleCounts(s)
	if counts["todo-comment"] != 1 {
		t.Errorf("todo-comment = %d, want 1 (all: %v)", counts["todo-comment"], counts)
	}
}
