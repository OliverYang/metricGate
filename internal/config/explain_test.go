package config

import (
	"testing"
)

func TestExplainPath_Included(t *testing.T) {
	cfg, err := LoadWithSource([]byte(`version: 1
exclude: ["vendor"]
languages:
  go:
    rules:
      empty-block:
        enabled: true
        severity: error
    metrics:
      cyclomatic-complexity: {warn: 7, error: 12}
`), "policy_file", "team.yaml")
	if err != nil {
		t.Fatal(err)
	}

	decision := cfg.ExplainPath("src/main.go")
	if decision.Status != "included" {
		t.Errorf("status = %q, want included", decision.Status)
	}
	if decision.Language != "go" {
		t.Errorf("language = %q, want go", decision.Language)
	}
	if decision.Reason != "included" {
		t.Errorf("reason = %q, want included", decision.Reason)
	}

	// Check effective policy
	if len(decision.RuleIDs) == 0 {
		t.Error("no rule IDs found")
	}

	foundEmptyBlock := false
	for _, id := range decision.RuleIDs {
		if id == "empty-block" {
			foundEmptyBlock = true
			break
		}
	}
	if !foundEmptyBlock {
		t.Error("empty-block not in RuleIDs")
	}

	// Source may be policy_file or builtin depending on ExplicitKeys tracking
	source, ok := decision.EffectivePolicy["empty-block"]
	if !ok {
		t.Error("empty-block not in EffectivePolicy")
	} else if source != "policy_file" && source != "builtin" {
		t.Errorf("empty-block source = %q, want policy_file or builtin", source)
	}
}

func TestExplainPath_ExcludedByPattern(t *testing.T) {
	cfg, err := LoadWithSource([]byte(`version: 1
exclude: ["testdata", "vendor/*"]
languages:
  go: {}
`), "policy_file", "p.yaml")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path     string
		pattern  string
		expected string
	}{
		{"testdata/foo.go", "testdata", "excluded"},
		{"vendor/bar.go", "vendor/*", "excluded"},
		{"src/baz.go", "", "included"},
	}

	for _, c := range cases {
		decision := cfg.ExplainPath(c.path)
		if decision.Status != c.expected {
			t.Errorf("%s: status = %q, want %q", c.path, decision.Status, c.expected)
		}
		if c.pattern != "" && decision.MatchedPattern != c.pattern {
			t.Errorf("%s: MatchedPattern = %q, want %q", c.path, decision.MatchedPattern, c.pattern)
		}
	}
}

func TestExplainPath_UnsupportedLanguage(t *testing.T) {
	cfg, _ := LoadWithSource([]byte("version: 1\nlanguages:\n  go: {}\n"), "builtin", "")

	decision := cfg.ExplainPath("foo.js")
	if decision.Status != "unsupported" {
		t.Errorf("status = %q, want unsupported", decision.Status)
	}
	if decision.Language != "unknown" {
		t.Errorf("language = %q, want unknown", decision.Language)
	}
}

func TestExplainRule_EnabledDisabled(t *testing.T) {
	cfg, err := LoadWithSource([]byte(`version: 1
languages:
  go:
    rules:
      todo-comment:
        enabled: true
        severity: warning
      empty-block:
        enabled: false
        severity: error
    metrics:
      function-length: {warn: 50, error: 100}
`), "policy_file", "team.yaml")
	if err != nil {
		t.Fatal(err)
	}

	rule := cfg.ExplainRule("go", "todo-comment")
	if !rule.Enabled {
		t.Error("todo-comment should be enabled")
	}
	if rule.Severity != "warning" {
		t.Errorf("severity = %q, want warning", rule.Severity)
	}

	rule = cfg.ExplainRule("go", "empty-block")
	if rule.Enabled {
		t.Error("empty-block should be disabled")
	}

	metric := cfg.ExplainRule("go", "function-length")
	if !metric.Enabled {
		t.Error("function-length metric should be enabled by default")
	}
	if metric.Thresholds.Warn != 50 || metric.Thresholds.Error != 100 {
		t.Errorf("thresholds = %+v, want warn=50 error=100", metric.Thresholds)
	}
}

func TestExplainRule_NotFound(t *testing.T) {
	cfg, _ := LoadWithSource([]byte("version: 1\nlanguages:\n  go: {}\n"), "builtin", "")

	rule := cfg.ExplainRule("go", "nonexistent-rule")
	if rule.Enabled {
		t.Error("nonexistent rule should be disabled")
	}
	if rule.Reason != "rule_not_found" {
		t.Errorf("reason = %q, want rule_not_found", rule.Reason)
	}
}

func TestExplainRule_LanguageNotFound(t *testing.T) {
	cfg, _ := LoadWithSource([]byte("version: 1\nlanguages:\n  go: {}\n"), "builtin", "")

	rule := cfg.ExplainRule("java", "some-rule")
	if rule.Enabled {
		t.Error("rule for unknown language should be disabled")
	}
	if rule.Reason != "language_not_configured" {
		t.Errorf("reason = %q, want language_not_configured", rule.Reason)
	}
}

func TestExplainDecision_EffectivePolicySources(t *testing.T) {
	cfg, err := LoadWithSource([]byte(`version: 1
languages:
  go:
    rules:
      custom-rule:
        enabled: true
        severity: warning
    metrics:
      file-length: {warn: 400, error: 800}
`), "policy_file", "team.yaml")
	if err != nil {
		t.Fatal(err)
	}

	decision := cfg.ExplainPath("main.go")

	// Both custom rule and metric should have a source defined
	if src, ok := decision.EffectivePolicy["custom-rule"]; !ok {
		t.Error("custom-rule not in EffectivePolicy")
	} else if src != "policy_file" && src != "builtin" {
		t.Errorf("custom-rule source = %q, want policy_file or builtin", src)
	}

	if src, ok := decision.EffectivePolicy["metric:file-length"]; !ok {
		t.Error("metric:file-length not in EffectivePolicy")
	} else if src != "policy_file" && src != "builtin" {
		t.Errorf("metric:file-length source = %q, want policy_file or builtin", src)
	}
}
