package policygap

import (
	"testing"

	"metricgate/internal/arch"
	"metricgate/internal/clones"
	"metricgate/internal/config"
	"metricgate/internal/engine"
	"metricgate/internal/model"
	"metricgate/internal/rules"
)

// These tests hold the audit to the distinction it exists for: a rule that is
// quiet because the policy never armed it, a rule that is quiet because the
// policy switched it off, and a rule that is quiet because it found nothing
// are three different answers, and collapsing them is how a gate that reports
// nothing gets mistaken for a codebase that is clean.

const basePolicy = `
version: 1
languages:
  go:
    rules:
      empty-block:
        enabled: false
`

func testInput(t *testing.T, policy string) Input {
	t.Helper()
	cfg, err := config.Load([]byte(policy))
	if err != nil {
		t.Fatal(err)
	}
	return Input{
		Root:      ".",
		Config:    cfg,
		Catalog:   engine.Catalog(cfg),
		Summary:   &model.RunSummary{},
		Languages: []string{"go"},
	}
}

func auditOf(t *testing.T, rep Report, id string) RuleAudit {
	t.Helper()
	for _, r := range rep.Rules {
		if r.RuleID == id {
			return r
		}
	}
	t.Fatalf("rule %q missing from the audit: %+v", id, rep.Rules)
	return RuleAudit{}
}

func assertStatus(t *testing.T, rep Report, id string, want Status) {
	t.Helper()
	got := auditOf(t, rep, id)
	if got.Status != want {
		t.Errorf("%s = %s, want %s (detail: %q)", id, got.Status, want, got.Detail)
	}
}

func TestArchitectureRulesAreUndeclaredWhenThePolicyHasNoArchitecture(t *testing.T) {
	rep := Analyze(testInput(t, basePolicy))
	for _, id := range []string{arch.RuleLayerViolation, arch.RuleForbiddenEdge, arch.RuleImportCycle} {
		assertStatus(t, rep, id, StatusUndeclared)
	}
}

func TestArchitectureRulesAreArmedOnceDeclared(t *testing.T) {
	in := testInput(t, basePolicy+`
architecture:
  layers:
    - name: core
      packages: ["internal/*"]
      may_depend_on: [core]
`)
	rep := Analyze(in)
	assertStatus(t, rep, arch.RuleLayerViolation, StatusArmed)
	assertStatus(t, rep, arch.RuleImportCycle, StatusArmed)
	// forbid was still never declared, so that one stays a gap.
	assertStatus(t, rep, arch.RuleForbiddenEdge, StatusUndeclared)
}

func TestSdpOffIsADecisionNotAGap(t *testing.T) {
	in := testInput(t, basePolicy+"architecture:\n  sdp: off\n")
	rep := Analyze(in)
	assertStatus(t, rep, arch.RuleSdp, StatusDisabled)
	if got := auditOf(t, rep, arch.RuleSdp).Detail; got == "" {
		t.Error("a disabled rule must say what switched it off")
	}
}

func TestDisabledRuleIsNotReportedAsAGap(t *testing.T) {
	rep := Analyze(testInput(t, basePolicy))
	assertStatus(t, rep, "empty-block", StatusDisabled)
}

func TestFiringRuleWinsOverTheLanguagesThatCannotFire(t *testing.T) {
	// Findings are tallied per rule id, so a rule that fired in Go must not
	// also be reported as firing for a language with no scanned files.
	in := testInput(t, basePolicy)
	in.Summary.Findings = []model.Finding{
		{RuleID: "cyclomatic-complexity", Severity: model.SevWarning},
		{RuleID: "cyclomatic-complexity", Severity: model.SevWarning},
		{RuleID: "cyclomatic-complexity", Severity: model.SevError},
	}
	rep := Analyze(in)
	seen := 0
	for _, r := range rep.Rules {
		if r.RuleID == "cyclomatic-complexity" {
			seen++
			if r.Findings != 3 {
				t.Errorf("findings = %d, want 3", r.Findings)
			}
		}
	}
	if seen != 1 {
		t.Errorf("cyclomatic-complexity appears %d times, want 1", seen)
	}
}

func TestRuleWithoutScannedFilesIsReportedOnce(t *testing.T) {
	in := testInput(t, basePolicy)
	rep := Analyze(in)
	seen := 0
	for _, r := range rep.Rules {
		if r.RuleID == "wmc" {
			seen++
			assertStatus(t, rep, "wmc", StatusNoFiles)
		}
	}
	if seen != 1 {
		t.Errorf("wmc appears %d times, want 1 in a repository with no Java files", seen)
	}
}

func TestMetricWithoutThresholdsIsReportOnly(t *testing.T) {
	// maintainability ships with warn=0/error=0: enabled, measured, and
	// structurally unable to produce a finding.
	rep := Analyze(testInput(t, basePolicy))
	assertStatus(t, rep, "maintainability", StatusReportOnly)
}

func TestMetricWithThresholdsIsArmed(t *testing.T) {
	rep := Analyze(testInput(t, `
version: 1
languages:
  go:
    metrics:
      maintainability:
        warn: 10
        error: 20
`))
	assertStatus(t, rep, "maintainability", StatusArmed)
}

func TestHotRulesNeedVolumeBehindTheShare(t *testing.T) {
	in := testInput(t, basePolicy)
	in.Summary.Findings = []model.Finding{
		{RuleID: "cyclomatic-complexity"}, {RuleID: "cyclomatic-complexity"}, {RuleID: "cyclomatic-complexity"},
	}
	in.Summary.Metrics = []model.MetricResult{
		{Metric: "cyclomatic-complexity", Scope: "a"}, {Metric: "cyclomatic-complexity", Scope: "b"},
		{Metric: "cyclomatic-complexity", Scope: "c"}, {Metric: "cyclomatic-complexity", Scope: "d"},
	}
	rep := Analyze(in)
	if len(rep.Hot) != 1 {
		t.Fatalf("hot rules = %+v, want cyclomatic-complexity", rep.Hot)
	}
	if rep.Hot[0].Scopes != 4 || rep.Hot[0].Findings != 3 {
		t.Errorf("hot = %+v, want 3/4", rep.Hot[0])
	}
}

func TestHotRulesIgnoreSmallCounts(t *testing.T) {
	// 2 of 3 scopes is a 67% share, but two findings say nothing about a
	// threshold; the volume floor keeps it out of the list.
	in := testInput(t, basePolicy)
	in.Summary.Findings = []model.Finding{{RuleID: "cyclomatic-complexity"}, {RuleID: "cyclomatic-complexity"}}
	in.Summary.Metrics = []model.MetricResult{
		{Metric: "cyclomatic-complexity", Scope: "a"},
		{Metric: "cyclomatic-complexity", Scope: "b"},
		{Metric: "cyclomatic-complexity", Scope: "c"},
	}
	if rep := Analyze(in); len(rep.Hot) != 0 {
		t.Errorf("hot rules = %+v, want none below the volume floor", rep.Hot)
	}
}

func TestSuppressedCountsArePerRule(t *testing.T) {
	in := testInput(t, basePolicy)
	in.Summary.IgnoredByRule = map[string]int{"function-clone": 4, "file-length": 1}
	rep := Analyze(in)
	if len(rep.Suppressed) != 2 {
		t.Fatalf("suppressed = %+v", rep.Suppressed)
	}
	if rep.Suppressed[0].RuleID != "function-clone" || rep.Suppressed[0].Count != 4 {
		t.Errorf("suppressed[0] = %+v, want function-clone/4", rep.Suppressed[0])
	}
}

func TestLayersReportCoverageAndGaps(t *testing.T) {
	in := testInput(t, basePolicy+`
architecture:
  layers:
    - name: core
      packages: ["internal/arch", "internal/model"]
      may_depend_on: [core]
`)
	in.Packages = []string{"internal/arch", "internal/model", "internal/bundle"}
	rep := Analyze(in)
	if len(rep.Layers) != 1 || rep.Layers[0].Name != "core" {
		t.Fatalf("layers = %+v", rep.Layers)
	}
	if len(rep.Layers[0].Packages) != 2 {
		t.Errorf("layer packages = %v, want the two declared ones", rep.Layers[0].Packages)
	}
	if len(rep.Unlayered) != 1 || rep.Unlayered[0] != "internal/bundle" {
		t.Errorf("unlayered = %v, want internal/bundle", rep.Unlayered)
	}
}

func TestAnalyzeIsDeterministic(t *testing.T) {
	in := testInput(t, basePolicy+`
architecture:
  layers:
    - name: core
      packages: ["internal/*"]
      may_depend_on: [core]
`)
	in.Packages = []string{"internal/arch", "internal/model", "internal/bundle"}
	in.Summary.Findings = []model.Finding{{RuleID: "file-length"}}
	a := Analyze(in)
	b := Analyze(in)
	if len(a.Rules) != len(b.Rules) {
		t.Fatalf("%d rules vs %d", len(a.Rules), len(b.Rules))
	}
	for i := range a.Rules {
		if a.Rules[i] != b.Rules[i] {
			t.Fatalf("rule %d differs: %+v vs %+v", i, a.Rules[i], b.Rules[i])
		}
	}
}

func TestGraphRulesMirrorThePolicy(t *testing.T) {
	cfg, err := config.Load([]byte(basePolicy))
	if err != nil {
		t.Fatal(err)
	}
	armed := map[string]bool{}
	inCheck := map[string]bool{}
	for _, g := range GraphRules(cfg, ".") {
		armed[g.ID] = g.Armed
		inCheck[g.ID] = g.InCheck
	}
	for id, want := range map[string]bool{
		arch.RuleLayerViolation:     false, // no architecture.layers declared
		arch.RuleForbiddenEdge:      false,
		clones.RuleFunctionClone:    true,
		rules.RuleUnknownDependency: true, // armed by the go.mod allowlist here
		"metric-regression":         false,
	} {
		if armed[id] != want {
			t.Errorf("%s armed = %v, want %v", id, armed[id], want)
		}
	}
	if inCheck["metric-regression"] {
		t.Error("metric-regression must be marked as needing a flag")
	}
}
