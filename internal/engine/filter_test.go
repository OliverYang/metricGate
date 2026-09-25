package engine

import (
	"testing"

	"metricgate/internal/diff"
	"metricgate/internal/metrics"
	"metricgate/internal/model"
)

func TestFilterSummary(t *testing.T) {
	changed := map[string][]diff.Range{
		"a.go":     {{Start: 5, End: 7}},
		"new/b.go": {{Start: 1, End: 20}},
	}
	s := &model.RunSummary{
		Findings: []model.Finding{
			{RuleID: "r-old", File: "a.go", StartLine: 2, EndLine: 2},
			{RuleID: "r-new", File: "a.go", StartLine: 6, EndLine: 6},
			{RuleID: metrics.MetricCyclomatic, Source: model.SrcMetric, File: "a.go", StartLine: 1, EndLine: 10},
			{RuleID: metrics.MetricFileLength, Source: model.SrcMetric, File: "a.go", StartLine: 1, EndLine: 1},
			{RuleID: metrics.MetricFileLength, Source: model.SrcMetric, File: "new/b.go", StartLine: 1, EndLine: 1},
			{RuleID: "r-untouched", File: "other/c.go", StartLine: 1, EndLine: 1},
		},
		Metrics: []model.MetricResult{
			{Metric: metrics.MetricFileLength, File: "a.go", Value: 100},
			{Metric: metrics.MetricFileLength, File: "new/b.go", Value: 20},
		},
	}

	out, hidden := FilterSummary(s, changed)
	if hidden != 3 {
		t.Errorf("hidden = %d, want 3 (r-old, a.go file-length, r-untouched)", hidden)
	}

	got := map[string]bool{}
	for _, f := range out.Findings {
		got[f.RuleID+"/"+f.File] = true
	}
	for _, want := range []string{
		"r-new/a.go",                           // rule on a changed line
		metrics.MetricCyclomatic + "/a.go",     // touched function keeps its metric verdict
		metrics.MetricFileLength + "/new/b.go", // brand-new file reports file-length
	} {
		if !got[want] {
			t.Errorf("finding %q missing; kept = %v", want, got)
		}
	}
	if len(out.Findings) != 3 {
		t.Errorf("kept = %d findings, want 3", len(out.Findings))
	}
	// Metric snapshots must never be filtered.
	if len(out.Metrics) != 2 {
		t.Errorf("metrics = %d, want 2 (unfiltered)", len(out.Metrics))
	}
}

