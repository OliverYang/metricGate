package store

import (
	"path/filepath"
	"testing"
	"time"

	"metricgate/internal/model"
)

func TestSaveRun(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	summary := &model.RunSummary{
		Files:    2,
		Duration: 5 * time.Millisecond,
		Findings: []model.Finding{{
			RuleID: "no-fmt-print", Severity: model.SevWarning, Source: model.SrcRule,
			File: "a.go", StartLine: 3, EndLine: 3, Message: "m",
		}},
		Metrics: []model.MetricResult{
			{Metric: "file-length", File: "a.go", Value: 10, Warn: 400, Err: 800},
			{Metric: "cyclomatic-complexity", File: "a.go", Scope: "A", Value: 8, Warn: 10, Err: 15},
		},
	}
	id, err := st.SaveRun(summary, "test", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Errorf("run id = %d, want 1", id)
	}
	n, err := st.Runs()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("runs = %d, want 1", n)
	}
}
