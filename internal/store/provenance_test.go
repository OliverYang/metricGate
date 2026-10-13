package store

import (
	"path/filepath"
	"testing"

	"metricgate/internal/model"
)

// TestSaveRunFullCarriesPolicyDigestAndTrace pins the two optional provenance
// fields across all three tables. They are optional in the sense that an
// absent one is empty, not in the sense that a present one may be dropped: a
// trace that reaches the runs table but not the findings makes "which unit of
// work produced this finding" unanswerable for exactly the rows that matter.
func TestSaveRunFullCarriesPolicyDigestAndTrace(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	summary := &model.RunSummary{
		Files:    3,
		Findings: []model.Finding{{RuleID: "cyclomatic-complexity", Severity: model.SevError, Source: model.SrcMetric, File: "a.go", StartLine: 7, EndLine: 9, Message: "too complex"}},
		Metrics:  []model.MetricResult{{Metric: "cyclomatic-complexity", File: "a.go", Scope: "big", Value: 12, Warn: 10, Err: 15, StartLine: 7, EndLine: 9}},
	}
	_, err = st.SaveRunFull(summary, "0.1.0", "abc123", "/repo-x", RunMeta{
		PolicyDigest: "sha256:deadbeef",
		TraceID:      "trace-42",
	})
	if err != nil {
		t.Fatal(err)
	}

	runs, err := st.LatestRuns(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if runs[0].PolicyDigest != "sha256:deadbeef" || runs[0].TraceID != "trace-42" {
		t.Fatalf("run provenance = %+v", runs[0])
	}

	// The findings and snapshots carry the same trace, which is what makes the
	// join work in the direction that matters: from a finding back to the run
	// and the session that produced it.
	var traceCount int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM findings WHERE trace_id = ?`, "trace-42").Scan(&traceCount); err != nil {
		t.Fatal(err)
	}
	if traceCount != 1 {
		t.Errorf("findings with the trace = %d, want 1", traceCount)
	}
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM metric_snapshots WHERE trace_id = ?`, "trace-42").Scan(&traceCount); err != nil {
		t.Fatal(err)
	}
	if traceCount != 1 {
		t.Errorf("snapshots with the trace = %d, want 1", traceCount)
	}
}

// TestSaveRunWithoutProvenanceLeavesItEmpty is the backward-compatibility
// contract: a caller that has no trace and no digest passes RunMeta{} and
// gets rows that behave exactly as they did before the columns existed.
func TestSaveRunWithoutProvenanceLeavesItEmpty(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.SaveRun(&model.RunSummary{Files: 1}, "0.1.0", ""); err != nil {
		t.Fatal(err)
	}
	runs, err := st.LatestRuns(1)
	if err != nil {
		t.Fatal(err)
	}
	if runs[0].TraceID != "" || runs[0].PolicyDigest != "" {
		t.Fatalf("empty provenance must stay empty, got %+v", runs[0])
	}
}

// TestOpenMigratesLegacyStoreWithoutProvenance proves an old database gains
// the new columns on open rather than failing: the 568MB production store has
// neither, and refusing to open it would take the trend views with it.
func TestOpenMigratesLegacyStoreWithoutProvenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Drop the provenance columns the way a database written before they
	// existed would look, then reopen: the migration must restore them.
	for _, stmt := range []string{
		`ALTER TABLE runs DROP COLUMN policy_digest`,
		`ALTER TABLE runs DROP COLUMN trace_id`,
		`ALTER TABLE findings DROP COLUMN trace_id`,
		`ALTER TABLE metric_snapshots DROP COLUMN trace_id`,
	} {
		if _, err := legacy.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	legacy.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("reopening a legacy store: %v", err)
	}
	defer st.Close()
	// A row written while the columns were absent is not readable at all
	// (the migration made the table writable again, not the old row
	// meaningful), so the check is that the store opens and accepts new rows
	// with provenance — which is what a 568MB legacy store needs.
	if _, err := st.SaveRunFull(&model.RunSummary{Files: 2}, "0.1.0", "", "/repo-y", RunMeta{TraceID: "t2", PolicyDigest: "sha256:aa"}); err != nil {
		t.Fatalf("writing to a migrated store: %v", err)
	}
	runs, err := st.LatestRuns(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	if runs[0].TraceID != "t2" {
		t.Errorf("new row trace = %q, want t2", runs[0].TraceID)
	}
	if runs[0].PolicyDigest != "sha256:aa" {
		t.Errorf("new row digest = %q, want sha256:aa", runs[0].PolicyDigest)
	}
}
