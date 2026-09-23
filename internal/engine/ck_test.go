package engine

import (
	"strings"
	"testing"

	"metricgate/internal/model"
)

// The Java CK metrics flow through the same metric pipeline: snapshots are
// recorded per class and threshold crossings become findings anchored on
// the class declaration.

// ckSummary runs the engine over the CK fixture.
func ckSummary(t *testing.T) *model.RunSummary {
	t.Helper()
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	summary, err := eng.Run("testdata/ck")
	if err != nil {
		t.Fatal(err)
	}
	return summary
}

// assertLCOMSnapshot verifies the per-class metric snapshot was recorded.
func assertLCOMSnapshot(t *testing.T, summary *model.RunSummary) {
	t.Helper()
	for _, m := range summary.Metrics {
		if m.Metric == "lcom" && m.Scope == "SplitBrain" {
			return
		}
	}
	t.Error("no lcom snapshot recorded for SplitBrain")
}

// assertLCOMFinding verifies the threshold crossing: anchored on the class
// declaration, sourced from the metric pipeline.
func assertLCOMFinding(t *testing.T, summary *model.RunSummary) {
	t.Helper()
	for _, f := range summary.Findings {
		if f.RuleID != "lcom" {
			continue
		}
		if !strings.HasSuffix(f.File, "Split.java") {
			t.Errorf("lcom finding in %s, want Split.java", f.File)
		}
		if f.StartLine != 5 {
			t.Errorf("lcom anchored at line %d, want the class declaration (5)", f.StartLine)
		}
		if f.Source != model.SrcMetric {
			t.Errorf("lcom source = %s, want metric", f.Source)
		}
		return
	}
	t.Error("SplitBrain crossed the default lcom threshold but no finding was reported")
}

func TestClassMetricsEndToEnd(t *testing.T) {
	summary := ckSummary(t)
	assertLCOMSnapshot(t, summary)
	assertLCOMFinding(t, summary)
}
