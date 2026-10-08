package store

import (
	"path/filepath"
	"testing"
	"time"

	"metricgate/internal/model"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// An empty store must yield a zero report with non-nil slices, so the JSON
// reads "[]" rather than "null" and a consumer never has to special-case it.
func TestReportOnEmptyStore(t *testing.T) {
	got, err := openTestStore(t).Report(0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runs != 0 || got.Range != nil {
		t.Errorf("empty report = %+v, want 0 runs and no range", got)
	}
	if got.Series == nil || got.TopRules == nil {
		t.Errorf("series/topRules must be non-nil, got %+v", got)
	}
}

func TestReportSpansTheStoredRuns(t *testing.T) {
	st := openTestStore(t)
	for _, p := range severityTrendPlans {
		sum := &model.RunSummary{Files: 1, Duration: time.Millisecond, Findings: buildFindings(p.errs, p.warns, p.infos)}
		if _, err := st.SaveRun(sum, "0.1.0", "deadbeef"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Report(0)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runs != len(severityTrendPlans) {
		t.Fatalf("Runs = %d, want %d", got.Runs, len(severityTrendPlans))
	}
	if len(got.Series) != len(severityTrendPlans) {
		t.Fatalf("Series len = %d, want %d", len(got.Series), len(severityTrendPlans))
	}
	if got.Range == nil || got.Range.Start == "" || got.Range.End == "" {
		t.Fatalf("Range = %+v, want both ends set", got.Range)
	}
	if got.Range.Start > got.Range.End {
		t.Errorf("range start %s is after end %s", got.Range.Start, got.Range.End)
	}
}

// The limit must cap the window on every aggregation, not just the run list.
func TestReportRespectsLimit(t *testing.T) {
	st := openTestStore(t)
	for i := 0; i < 4; i++ {
		sum := &model.RunSummary{Files: 1, Duration: time.Millisecond, Findings: buildFindings(1, 0, 0)}
		if _, err := st.SaveRun(sum, "0.1.0", "deadbeef"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.Report(2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runs != 2 || len(got.Series) != 2 {
		t.Errorf("limit 2 -> runs %d, series %d; want 2 and 2", got.Runs, len(got.Series))
	}
}
