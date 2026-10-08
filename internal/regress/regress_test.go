package regress

import (
	"strings"
	"testing"

	"metricgate/internal/diff"
	"metricgate/internal/metrics"
	"metricgate/internal/model"
)

func mr(metric, file, scope string, value int) model.MetricResult {
	return model.MetricResult{Metric: metric, File: file, Scope: scope, Value: value, StartLine: 10, EndLine: 20}
}

func TestCompareReportsOnlyWorseValues(t *testing.T) {
	base := []model.MetricResult{
		mr(metrics.MetricCognitive, "a.go", "Foo", 8),
		mr(metrics.MetricCyclomatic, "a.go", "Bar", 5),
		mr(metrics.MetricCyclomatic, "gone.go", "Old", 3),
	}
	cur := []model.MetricResult{
		mr(metrics.MetricCognitive, "a.go", "Foo", 9),    // rose: report
		mr(metrics.MetricCyclomatic, "a.go", "Bar", 5),   // equal: skip
		mr(metrics.MetricCyclomatic, "new.go", "New", 4), // new: skip
	}
	got := Compare(base, cur)
	if len(got) != 1 {
		t.Fatalf("got %d deltas, want 1: %+v", len(got), got)
	}
	if got[0].Metric != metrics.MetricCognitive || got[0].Base != 8 || got[0].Current != 9 {
		t.Errorf("delta = %+v", got[0])
	}
}

func TestCompareIsDeterministicAndSorted(t *testing.T) {
	base := []model.MetricResult{
		mr(metrics.MetricCognitive, "b.go", "Z", 1),
		mr(metrics.MetricCognitive, "a.go", "Y", 1),
		mr(metrics.MetricCyclomatic, "a.go", "X", 1),
	}
	cur := []model.MetricResult{
		mr(metrics.MetricCyclomatic, "a.go", "X", 2),
		mr(metrics.MetricCognitive, "b.go", "Z", 2),
		mr(metrics.MetricCognitive, "a.go", "Y", 2),
	}
	got := Compare(base, cur)
	if len(got) != 3 {
		t.Fatalf("got %d deltas", len(got))
	}
	want := []string{"a.go", "a.go", "b.go"}
	for i, w := range want {
		if got[i].File != w {
			t.Fatalf("delta[%d].File = %s, want %s (order: %+v)", i, got[i].File, w, got)
		}
	}
	// Same file, same start line: the metric name breaks the tie, so the
	// emitted order is reproducible rather than map-iteration dependent.
	if got[0].Metric != metrics.MetricCognitive || got[1].Metric != metrics.MetricCyclomatic {
		t.Errorf("same line: metric tie-break wrong: %+v", got)
	}
}

func TestFilterToChangedKeepsTouchedScopesOnly(t *testing.T) {
	deltas := []Delta{
		{File: "a.go", Metric: metrics.MetricCognitive, StartLine: 10, EndLine: 20},
		{File: "b.go", Metric: metrics.MetricCognitive, StartLine: 1, EndLine: 5},
	}
	changed := map[string][]diff.Range{"a.go": {{Start: 15, End: 16}}}
	got := FilterToChanged(deltas, changed)
	if len(got) != 1 || got[0].File != "a.go" {
		t.Fatalf("got %+v, want only a.go", got)
	}
	if got := FilterToChanged(deltas, nil); len(got) != 0 {
		t.Errorf("no changed ranges must keep nothing, got %+v", got)
	}
}

func TestFindingsCarryRuleAndLineOnlyAnchor(t *testing.T) {
	fs := Findings([]Delta{{Metric: metrics.MetricCognitive, File: "a.go", Scope: "Foo", Base: 8, Current: 9, StartLine: 12, EndLine: 30}}, model.SevWarning)
	if len(fs) != 1 {
		t.Fatalf("got %d findings", len(fs))
	}
	f := fs[0]
	if f.RuleID != RuleID || f.Severity != model.SevWarning || f.Source != model.SrcMetric {
		t.Errorf("finding = %+v", f)
	}
	if f.StartLine != 12 || f.EndLine != 30 {
		t.Errorf("anchor = %d..%d, want 12..30", f.StartLine, f.EndLine)
	}
	if f.Evidence == nil || f.Evidence.Anchor != model.AnchorLineOnly {
		t.Errorf("evidence = %+v, want line_only", f.Evidence)
	}
	if !strings.Contains(f.Message, "8 → 9") {
		t.Errorf("message = %q, want the 8 → 9 delta", f.Message)
	}
}

func TestFindingsUnanchoredScopeDefaultsToLineOne(t *testing.T) {
	fs := Findings([]Delta{{Metric: metrics.MetricFileLength, File: "a.go", Base: 100, Current: 200}}, model.SevWarning)
	if fs[0].StartLine != 1 || fs[0].EndLine != 1 {
		t.Errorf("anchor = %d..%d, want 1..1", fs[0].StartLine, fs[0].EndLine)
	}
}

func TestMaintainabilityMessageUsesDebtAndMI(t *testing.T) {
	fs := Findings([]Delta{{Metric: metrics.MetricMaintainability, File: "a.go", Base: 12, Current: 20}}, model.SevWarning)
	msg := fs[0].Message
	if !strings.Contains(msg, "12 → 20") || !strings.Contains(msg, "MI 88 → 80") {
		t.Errorf("message = %q, want debt and MI both", msg)
	}
}
