package store

import (
	"path/filepath"
	"testing"
	"time"

	"metricgate/internal/model"
)

// saveSnapshotRun stores one run with two metric snapshots, forcing the run
// timestamp so the ordering in the trend is deterministic.
func saveSnapshotRun(t *testing.T, st *Store, started time.Time, warn int, values ...int) {
	t.Helper()
	id, err := st.saveRunForTimestamp(&model.RunSummary{Files: 1}, "test", "abc123", started)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		if err := st.insertSnapshot(id, "a.go", "function", "cognitive-complexity", v, warn, 25); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMetricTrendAggregatesAndThresholdStep(t *testing.T) {
	pts := thresholdExperimentSeries(t)
	if len(pts) != 2 {
		t.Fatalf("series length = %d, want 2", len(pts))
	}
	first, second := pts[0], pts[1]
	if first.Samples != 3 || first.P50 != 12 || first.P90 != 15 || first.Max != 15 {
		t.Fatalf("first point wrong: %+v", first)
	}
	if first.Warn != 15 || first.Violated != 1 {
		t.Fatalf("first threshold/violated = %d/%d, want 15/1", first.Warn, first.Violated)
	}
	// After the change: warn=10, same distribution → 12 and 15 violate, a 1→2 step.
	if second.Warn != 10 || second.Violated != 2 {
		t.Fatalf("second threshold/violated = %d/%d, want 10/2", second.Warn, second.Violated)
	}
}

// TestMetricTrendDistributionInvariant pins the experiment's control variable:
// when only the threshold changes, the distribution columns must not move —
// otherwise a "violation step" would be confounded with a real code change.
func TestMetricTrendDistributionInvariant(t *testing.T) {
	pts := thresholdExperimentSeries(t)
	first, second := pts[0], pts[1]
	if second.P50 != first.P50 || second.P90 != first.P90 || second.Max != first.Max {
		t.Fatalf("distribution moved when only the threshold changed: %+v vs %+v", first, second)
	}
}

// thresholdExperimentSeries builds the two-run fixture: values {9,12,15}
// checked at warn 15, then the identical values at warn 10.
func thresholdExperimentSeries(t *testing.T) []MetricPoint {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	base := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	saveSnapshotRun(t, st, base, 15, 9, 12, 15)
	saveSnapshotRun(t, st, base.Add(time.Minute), 10, 9, 12, 15)
	mt, err := st.MetricTrend(0)
	if err != nil {
		t.Fatal(err)
	}
	if mt.Runs != 2 {
		t.Fatalf("runs = %d, want 2", mt.Runs)
	}
	return mt.Series["cognitive-complexity"]
}

func TestMetricTrendWindowAndEmptyStore(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// Empty store: zero report, non-nil series so JSON reads "[]"-style, not null.
	mt, err := st.MetricTrend(0)
	if err != nil {
		t.Fatal(err)
	}
	if mt.Runs != 0 || len(mt.Series) != 0 {
		t.Fatalf("empty store trend = %+v", mt)
	}

	base := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		saveSnapshotRun(t, st, base.Add(time.Duration(i)*time.Minute), 15, 5)
	}
	mt, err = st.MetricTrend(2) // only the 2 most recent runs
	if err != nil {
		t.Fatal(err)
	}
	if mt.Runs != 2 {
		t.Fatalf("windowed runs = %d, want 2", mt.Runs)
	}
	pts := mt.Series["cognitive-complexity"]
	if len(pts) != 2 || pts[0].Started != base.Add(time.Minute).Format(time.RFC3339) {
		t.Fatalf("window picked wrong runs: %+v", pts)
	}
	// The oldest run in the window is the run omitted from the series? No: a
	// run with snapshots for the metric appears once per run.
	if pts[0].Samples != 1 || pts[1].Samples != 1 {
		t.Fatalf("samples per point = %+v", pts)
	}
}

func TestMetricTrendMetricIsolation(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.saveRunForTimestamp(&model.RunSummary{}, "test", "abc", time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.insertSnapshot(id, "a.go", "function", "cognitive-complexity", 5, 15, 25); err != nil {
		t.Fatal(err)
	}
	if err := st.insertSnapshot(id, "a.go", "function", "cyclomatic-complexity", 2, 15, 25); err != nil {
		t.Fatal(err)
	}
	mt, err := st.MetricTrend(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(mt.Series) != 2 {
		t.Fatalf("series = %d metrics, want 2", len(mt.Series))
	}
	if mt.Series["cognitive-complexity"][0].P50 != 5 || mt.Series["cyclomatic-complexity"][0].P50 != 2 {
		t.Fatalf("metrics mixed together: %+v", mt.Series)
	}
}

// saveRunForTimestamp is the test seam for run timestamps: production runs
// always write time.Now(), but trend ordering needs deterministic stamps.
func (s *Store) saveRunForTimestamp(summary *model.RunSummary, toolVersion, commit string, started time.Time) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO runs
		(started_at, duration_ms, tool_version, git_commit, files, error_count, warning_count, info_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		started.UTC().Format(time.RFC3339), summary.Duration.Milliseconds(),
		toolVersion, commit, summary.Files,
		summary.Count(model.SevError), summary.Count(model.SevWarning), summary.Count(model.SevInfo))
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// insertSnapshot adds one metric snapshot to an existing run (test seam).
func (s *Store) insertSnapshot(runID int64, file, scope, metric string, value, warn, errT int) error {
	_, err := s.db.Exec(`INSERT INTO metric_snapshots
		(run_id, file, scope, metric, value, warn_threshold, err_threshold)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, runID, file, scope, metric, value, warn, errT)
	return err
}
