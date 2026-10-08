package hotspots

import (
	"math"
	"testing"

	"metricgate/internal/metrics"
	"metricgate/internal/model"
)

func TestWorstFileMIsPicksWorstFunction(t *testing.T) {
	mets := []model.MetricResult{
		{Metric: metrics.MetricCyclomatic, File: "a.go", Value: 999},     // not MI: ignored
		{Metric: metrics.MetricMaintainability, File: "a.go", Value: 40}, // MI 60
		{Metric: metrics.MetricMaintainability, File: "a.go", Value: 70}, // MI 30, worst
		{Metric: metrics.MetricMaintainability, File: "b.go", Value: 10}, // MI 90
	}
	got := WorstFileMIs(mets)
	if got["a.go"] != 30 {
		t.Errorf("a.go MI = %.0f, want 30 (the worst function)", got["a.go"])
	}
	if got["b.go"] != 90 {
		t.Errorf("b.go MI = %.0f, want 90", got["b.go"])
	}
}

// FromSummary must wire both the MI and the risk inputs from one summary, so
// the CLI and the MCP server cannot derive them differently.
func TestFromSummaryWiresMIAndRisk(t *testing.T) {
	summary := &model.RunSummary{
		Findings: []model.Finding{{File: "a.go", Severity: model.SevError}},
		Metrics: []model.MetricResult{
			{Metric: metrics.MetricMaintainability, File: "a.go", Value: 70},
		},
	}
	opt := FromSummary(".", "30.days ago", 5, summary)
	if opt.Since != "30.days ago" || opt.Root != "." || opt.Max != 5 {
		t.Errorf("options not carried through: %+v", opt)
	}
	if opt.MIs["a.go"] != 30 {
		t.Errorf("MIs = %v, want a.go=30", opt.MIs)
	}
	// 1 error + debt 70 -> 100*(0.7*0.5 + 0.3*0.7) = 56
	if r := opt.Risk["a.go"]; math.Abs(r-56) > 1e-6 {
		t.Errorf("Risk = %v, want a.go=56", opt.Risk)
	}
}
