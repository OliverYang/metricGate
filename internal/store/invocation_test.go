package store

import (
	"path/filepath"
	"testing"

	"metricgate/internal/model"
)

// The invocation column is provenance written by one code path and read by
// another (the CLI writes it, Cockpit reads it), so the round trip is the
// contract. Two states matter and they are not the same: a run recorded with a
// command line, and a run the store knows nothing about — which includes every
// row written before the column existed.

func TestRunInvocationRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	const line = "metricgate check . -arch-base arch.json"
	id, err := st.SaveRunFull(&model.RunSummary{Files: 3}, "test", "abc", "/repo/x",
		RunMeta{Invocation: line})
	if err != nil {
		t.Fatal(err)
	}

	run, ok, err := st.RunByID(id)
	if err != nil || !ok {
		t.Fatalf("RunByID(%d) ok=%v err=%v", id, ok, err)
	}
	if run.Invocation != line {
		t.Fatalf("invocation round trip = %q, want %q", run.Invocation, line)
	}
}

// An empty invocation is stored as NULL, so a legacy row and a deliberate empty
// both read back as "" — and the UI has one honest label for both instead of
// two ways of saying nothing.
func TestRunWithoutInvocationReadsBackEmpty(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	id, err := st.SaveRunFull(&model.RunSummary{Files: 1}, "test", "abc", "/repo/x", RunMeta{})
	if err != nil {
		t.Fatal(err)
	}
	run, ok, err := st.RunByID(id)
	if err != nil || !ok {
		t.Fatalf("RunByID(%d) ok=%v err=%v", id, ok, err)
	}
	if run.Invocation != "" {
		t.Fatalf("invocation = %q, want empty", run.Invocation)
	}

	// A NULL cell must coalesce, not scan into a string as an error: the
	// windowed listing is the same scan path, and a row that fails to scan
	// takes the whole run history down with it.
	runs, err := st.LatestRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Invocation != "" {
		t.Fatalf("LatestRuns returned %+v, want one run with an empty invocation", runs)
	}
}

// A database opened on a store written before this column must gain it and keep
// its rows, because every existing local history is in that shape — including
// the one this repository is developed against.
func TestMigrationAddsInvocationToLegacyRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`ALTER TABLE runs DROP COLUMN invocation`); err != nil {
		t.Skipf("sqlite build cannot drop a column: %v", err)
	}
	if _, err := st.db.Exec(`INSERT INTO runs
		(started_at, duration_ms, tool_version, git_commit, files, error_count, warning_count, info_count, project_root, archived)
		VALUES ('2026-10-17T00:00:00Z', 12, 'v', 'c', 1, 0, 1, 0, '/repo', 0)`); err != nil {
		t.Fatal(err)
	}
	st.Close()

	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after a missing invocation column: %v", err)
	}
	defer st2.Close()
	runs, err := st2.LatestRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("legacy row lost: %+v", runs)
	}
	if runs[0].Invocation != "" {
		t.Fatalf("legacy invocation = %q, want empty", runs[0].Invocation)
	}
}
