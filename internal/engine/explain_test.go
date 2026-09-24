package engine

import (
	"testing"

	"metricgate/internal/config"
)

func TestExplainReportsEffectivePolicyValuesAndSources(t *testing.T) {
	cfg, err := config.LoadWithSource([]byte(`version: 1
exclude: ["testdata"]
languages:
  go:
    rules:
      empty-block:
        enabled: false
        severity: error
    metrics:
      cyclomatic-complexity: {warn: 7, error: 12}
`), "policy_file", "team.yaml")
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	report := eng.ExplainPath("src/sample.go")
	if report.Selection.Status != "included" || report.Language != "go" {
		t.Fatalf("selection = %+v language=%q", report.Selection, report.Language)
	}
	items := map[string]ExplainItem{}
	for _, item := range report.Items {
		items[item.Kind+":"+item.ID] = item
	}
	assertEffectiveRule(t, items["rule:empty-block"])
	assertEffectiveMetric(t, items["metric:cyclomatic-complexity"])
	assertDefaultMetricSource(t, items["metric:function-length"])
	assertCloneConstraint(t, items["constraint:clones"])
}

func assertEffectiveRule(t *testing.T, empty ExplainItem) {
	t.Helper()
	if empty.ID == "" || empty.Enabled || empty.Severity != "error" {
		t.Errorf("effective empty-block = %+v", empty)
	}
	if empty.Source != "policy_file" || empty.SourcePath != "team.yaml" || !empty.Explicit {
		t.Errorf("empty-block provenance = %+v", empty)
	}
}

func assertEffectiveMetric(t *testing.T, cc ExplainItem) {
	t.Helper()
	if cc.Warn != 7 || cc.Error != 12 || cc.Source != "policy_file" || !cc.Explicit {
		t.Errorf("effective cyclomatic metric = %+v", cc)
	}
}

func assertDefaultMetricSource(t *testing.T, m ExplainItem) {
	t.Helper()
	if m.Source != "builtin" || m.Explicit {
		t.Errorf("default metric source = %+v, want builtin/non-explicit", m)
	}
}

func assertCloneConstraint(t *testing.T, clone ExplainItem) {
	t.Helper()
	if clone.ID == "" || !clone.Enabled || clone.Source != "builtin" || clone.Explicit {
		t.Errorf("clone constraint = %+v", clone)
	}
}

func TestExplainExcludedPathHasNoApplicableRules(t *testing.T) {
	cfg, err := config.LoadWithSource([]byte("version: 1\nexclude: [testdata]\n"), "policy_file", "p.yaml")
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	got := eng.ExplainPath("testdata/hidden.go")
	if got.Selection.Status != "excluded" || got.Selection.MatchedExclude != "testdata" || len(got.Items) != 0 {
		t.Errorf("excluded explain = %+v", got)
	}
}
