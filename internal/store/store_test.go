package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	"metricgate/internal/model"
)

func TestRunFindingsUsesSQLiteRowID(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	summary := &model.RunSummary{Findings: []model.Finding{{RuleID: "no-fmt-print", Severity: model.SevWarning, Source: model.SrcRule, File: "a.go", StartLine: 3, EndLine: 3, Message: "m"}}}
	runID, err := st.SaveRun(summary, "test", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := st.RunFindings(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].RuleID != "no-fmt-print" {
		t.Fatalf("RunFindings() = %+v", findings)
	}
}

func TestSaveRun(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.SaveRun(&model.RunSummary{Files: 2}, "test", "abc123")
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

func TestSaveRunForProjectAndUnassigned(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	saveTestRun(t, st, "")
	saveTestRun(t, st, "/repo-a")
	saveTestRun(t, st, "/repo-b")
	assertRunRoots(t, st)
	assertProjectReport(t, st)
}

func saveTestRun(t *testing.T, st *Store, root string) {
	t.Helper()
	summary := &model.RunSummary{Files: 1, Findings: []model.Finding{{RuleID: "sample", Severity: model.SevWarning, Source: model.SrcRule, File: "a.go"}}}
	var err error
	if root == "" {
		_, err = st.SaveRun(summary, "test", "")
	} else {
		_, err = st.SaveRunForProject(summary, "test", "", root)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func assertRunRoots(t *testing.T, st *Store) {
	t.Helper()
	unassigned, err := st.LatestUnassignedRuns(20)
	if err != nil || len(unassigned) != 1 || unassigned[0].ProjectRoot != "" {
		t.Fatalf("unassigned runs = %+v, err=%v", unassigned, err)
	}
	repoRuns, err := st.LatestRunsForProject(20, "/repo-a")
	if err != nil || len(repoRuns) != 1 || repoRuns[0].ProjectRoot != "/repo-a" {
		t.Fatalf("project runs = %+v, err=%v", repoRuns, err)
	}
}

func assertProjectReport(t *testing.T, st *Store) {
	t.Helper()
	report, err := st.ReportForProject(20, "/repo-a")
	if err != nil {
		t.Fatal(err)
	}
	if report.Runs != 1 || len(report.Series) != 1 || len(report.TopRules) != 1 || report.TopRules[0].Count != 1 {
		t.Fatalf("project report = %+v", report)
	}
}

func TestOpenMigratesLegacyRunsWithoutAssigningProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT, started_at TEXT NOT NULL,
		duration_ms INTEGER NOT NULL, tool_version TEXT NOT NULL, git_commit TEXT,
		files INTEGER NOT NULL, error_count INTEGER NOT NULL, warning_count INTEGER NOT NULL,
		info_count INTEGER NOT NULL
	);
	INSERT INTO runs (started_at,duration_ms,tool_version,files,error_count,warning_count,info_count)
	VALUES ('2026-01-01T00:00:00Z',0,'old',1,0,0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	assertLegacyMigration(t, path)
}

func assertLegacyMigration(t *testing.T, path string) {
	t.Helper()
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := st.LatestUnassignedRuns(10)
	if err != nil || len(runs) != 1 || runs[0].ProjectRoot != "" {
		t.Fatalf("migrated runs = %+v, err=%v", runs, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
}

// TestProjectRootResolvesASymlinkedSpelling covers the macOS trap: `git
// rev-parse` records the canonical root (/private/tmp/x), while an operator
// types the path their shell shows (/tmp/x). The exact match fails, but the
// value is not wrong, so it must resolve rather than be refused — a refusal
// here reads as "no such project" for a project that has runs.
func TestProjectRootResolvesASymlinkedSpelling(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dir := t.TempDir()
	if _, err := st.SaveRunForProject(&model.RunSummary{Files: 1}, "test", "abc", dir); err != nil {
		t.Fatal(err)
	}
	recorded, err := st.ProjectRoot(dir)
	if err != nil {
		t.Fatalf("ProjectRoot(%q) failed on the recorded spelling: %v", dir, err)
	}
	// A path the OS hides a symlink in is the shape the resolver has to survive;
	// on a tree with no symlinks the fallback (cleaned literal compare) is what
	// the same assertion exercises.
	got, err := st.ProjectRoot(filepath.Join(canonicalPath(dir), "."))
	if err != nil {
		t.Fatalf("ProjectRoot(rewritten) failed: %v", err)
	}
	if got != recorded {
		t.Errorf("ProjectRoot(rewritten) = %q, want %q", got, recorded)
	}
}

// TestProjectRootRefusesAnUnknownRoot states the honest failure instead of
// returning an empty filter: "0 runs" for a database with rows is the message
// this exists to prevent.
func TestProjectRootRefusesAnUnknownRoot(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.SaveRunForProject(&model.RunSummary{Files: 1}, "test", "abc", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ProjectRoot("/definitely/not/recorded"); err == nil {
		t.Errorf("ProjectRoot(/definitely/not/recorded) resolved; want a refusal naming the recorded roots")
	}
}
