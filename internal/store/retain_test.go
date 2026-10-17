package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Retention tests insert rows directly instead of going through SaveRun: the
// policy is expressed in time and project scope, and SaveRun stamps
// started_at with the wall clock, so a test built on it could not place a run
// on either side of a boundary.
const retainTestNow = "2026-10-16T12:00:00Z"

func retainNow(t *testing.T) time.Time {
	t.Helper()
	now, err := time.Parse(time.RFC3339, retainTestNow)
	if err != nil {
		t.Fatal(err)
	}
	return now
}

func newRetainStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// insertRun writes one run with an explicit timestamp and project root, plus
// one finding and one metric snapshot, so a test can assert that the children
// went with it.
func insertRun(t *testing.T, st *Store, startedAt, projectRoot string, archived bool) int64 {
	t.Helper()
	res, err := st.db.Exec(`INSERT INTO runs
		(started_at, duration_ms, tool_version, files, error_count, warning_count, info_count, project_root, archived)
		VALUES (?, 1, 'test', 1, 0, 1, 0, ?, ?)`,
		startedAt, nullableProjectRoot(projectRoot), boolToInt(archived))
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO findings (run_id, rule_id, severity, source, file, start_line, start_column, end_line, end_column, message)
		VALUES (?, 'sample', 'warning', 'rule', 'a.go', 1, 1, 1, 1, 'm')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO metric_snapshots (run_id, file, scope, metric, value, warn_threshold, err_threshold)
		VALUES (?, 'a.go', '', 'file-length', 10, 400, 800)`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func countRows(t *testing.T, st *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := st.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func runIDs(t *testing.T, st *Store) []int64 {
	t.Helper()
	rows, err := st.db.Query(`SELECT id FROM runs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func assertNoOrphans(t *testing.T, st *Store) {
	t.Helper()
	for _, table := range []string{"findings", "metric_snapshots"} {
		got := countRows(t, st, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE run_id NOT IN (SELECT id FROM runs)`, table))
		if got != 0 {
			t.Errorf("%s has %d orphan row(s) after prune", table, got)
		}
	}
}

// A zero or negative policy keeps everything, and must not write at all.
func TestPruneUnlimitedKeepsEverything(t *testing.T) {
	st := newRetainStore(t)
	for i := 0; i < 5; i++ {
		insertRun(t, st, fmt.Sprintf("2026-10-0%dT00:00:00Z", i+1), "/repo", false)
	}
	for _, p := range []RetainPolicy{{}, {Runs: -1}, {Days: -30}} {
		report, err := st.Prune(p)
		if err != nil {
			t.Fatal(err)
		}
		if report.Runs != 0 || report.Findings != 0 || report.Snapshots != 0 {
			t.Errorf("policy %+v removed rows: %+v", p, report)
		}
		if report.Kept != 5 {
			t.Errorf("policy %+v kept = %d, want 5", p, report.Kept)
		}
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM runs`); got != 5 {
		t.Errorf("runs = %d, want 5", got)
	}
}

// assertReport checks a prune report against expected counts in one place, so a
// test that names the numbers it expects does not spend five if-blocks reading
// them back.
func assertReport(t *testing.T, got PruneReport, want PruneReport) {
	t.Helper()
	if got != want {
		t.Errorf("report = %+v, want %+v", got, want)
	}
}

// The core contract: the three tables lose exactly the same set, so nothing is
// left pointing at a run that no longer exists.
func TestPruneRemovesRunsWithTheirChildren(t *testing.T) {
	st := newRetainStore(t)
	for i := 1; i <= 10; i++ {
		insertRun(t, st, fmt.Sprintf("2026-09-%02dT00:00:00Z", i), "/repo", false)
	}
	report, err := st.Prune(RetainPolicy{Runs: 3})
	if err != nil {
		t.Fatal(err)
	}
	// Every retired run carried exactly one finding and one snapshot, so the
	// report's counts are the row counts the deletes have to match.
	assertReport(t, report, PruneReport{Runs: 7, Findings: 7, Snapshots: 7, Kept: 3})
	if got := countRows(t, st, `SELECT COUNT(*) FROM findings`); got != 3 {
		t.Errorf("findings left = %d, want 3", got)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM metric_snapshots`); got != 3 {
		t.Errorf("snapshots left = %d, want 3", got)
	}
	assertNoOrphans(t, st)
	// The survivors are the newest three.
	ids := runIDs(t, st)
	if len(ids) != 3 || ids[0] != 8 || ids[2] != 10 {
		t.Errorf("surviving ids = %v, want [8 9 10]", ids)
	}
}

// The references in the schema are documentation: without PRAGMA
// foreign_keys the delete would leave orphans if it only touched runs. This
// pins that the connection really is in that mode, so the explicit deletes in
// Prune are load-bearing rather than belt and braces.
func TestPruneDoesNotRelyOnForeignKeys(t *testing.T) {
	st := newRetainStore(t)
	var fk int
	if err := st.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 0 {
		t.Fatalf("PRAGMA foreign_keys = %d, want 0: the schema references are not enforced, so Prune's explicit child deletes are required", fk)
	}
	insertRun(t, st, "2026-01-01T00:00:00Z", "/repo", false)
	insertRun(t, st, "2026-01-02T00:00:00Z", "/repo", false)
	// Deleting the parent alone leaves the children behind — the failure mode
	// Prune exists to avoid.
	if _, err := st.db.Exec(`DELETE FROM runs WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM findings WHERE run_id = 1`); got != 1 {
		t.Fatalf("findings left behind = %d, want 1 (documents that no cascade happens)", got)
	}
}

// Archiving is an operator decision; automatic retention must not undo it.
func TestPruneKeepsArchivedRuns(t *testing.T) {
	st := newRetainStore(t)
	insertRun(t, st, "2026-01-01T00:00:00Z", "/repo", true)
	for i := 2; i <= 6; i++ {
		insertRun(t, st, fmt.Sprintf("2026-01-%02dT00:00:00Z", i), "/repo", false)
	}
	report, err := st.Prune(RetainPolicy{Runs: 2})
	if err != nil {
		t.Fatal(err)
	}
	if report.Runs != 3 {
		t.Errorf("Runs = %d, want 3 (the archived one and the two newest survive)", report.Runs)
	}
	// 6 runs, 3 retired: id 1 (archived, exempt) and the two newest survive.
	if report.Kept != 3 {
		t.Errorf("Kept = %d, want 3", report.Kept)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM runs WHERE archived = 1`); got != 1 {
		t.Errorf("archived runs left = %d, want 1", got)
	}
	assertNoOrphans(t, st)
}

// Every project with runs keeps its newest run, and the unassigned bucket
// counts as one project. The bound itself is per store, not per project, so a
// project with more runs than the bound still loses its older ones.
func TestPruneKeepsOneRunPerProject(t *testing.T) {
	st := newRetainStore(t)
	// /stale is quiet: both its runs are older than every /busy run.
	staleOldest := insertRun(t, st, "2026-01-01T00:00:00Z", "/stale", false)
	staleNewest := insertRun(t, st, "2026-01-02T00:00:00Z", "/stale", false)
	// Unassigned runs are one bucket of their own.
	unassigned := insertRun(t, st, "2026-01-03T00:00:00Z", "", false)
	for i := 1; i <= 17; i++ {
		insertRun(t, st, fmt.Sprintf("2026-02-%02dT00:00:00Z", i), "/busy", false)
	}
	report, err := st.Prune(RetainPolicy{Runs: 5})
	if err != nil {
		t.Fatal(err)
	}
	// 17 /busy runs, 5 kept: 12 retire. /stale loses its older run but keeps its
	// newest, and the unassigned bucket keeps its only run.
	if report.Runs != 13 || report.Kept != 7 {
		t.Errorf("report = %+v, want 13 retired and 7 kept", report)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM runs WHERE project_root = '/stale'`); got != 1 {
		t.Errorf("/stale runs left = %d, want 1", got)
	}
	for _, tc := range []struct {
		name string
		id   int64
		want int
	}{
		{"quiet project's newest run", staleNewest, 1},
		{"quiet project's older run", staleOldest, 0},
		{"unassigned bucket's only run", unassigned, 1},
	} {
		if got := countRows(t, st, `SELECT COUNT(*) FROM runs WHERE id = ?`, tc.id); got != tc.want {
			t.Errorf("%s (id %d) present = %d, want %d", tc.name, tc.id, got, tc.want)
		}
	}
	assertNoOrphans(t, st)
}

// An archived run is not a project's live run. RunTotalsByProject counts only
// non-archived rows, so if the per-project rule picked the project's newest row
// regardless of archive state, it would protect the archived run and let the
// live ones go — the card would read "empty" even though a run is still stored.
func TestPruneKeepsOneLiveRunPerProjectNotAnArchivedOne(t *testing.T) {
	st := newRetainStore(t)
	// The project's newest row is archived, and its live rows are old enough to
	// fall outside the bound.
	live := insertRun(t, st, "2026-01-02T00:00:00Z", "/repo", false)
	insertRun(t, st, "2026-01-01T00:00:00Z", "/repo", false)
	archived := insertRun(t, st, "2026-04-01T00:00:00Z", "/repo", true)
	for i := 1; i <= 6; i++ {
		insertRun(t, st, fmt.Sprintf("2026-03-%02dT00:00:00Z", i), "/busy", false)
	}
	report, err := st.Prune(RetainPolicy{Runs: 5})
	if err != nil {
		t.Fatal(err)
	}
	// Kept: the archived run (never retired), the five newest, and /repo's
	// newest LIVE run.
	if got := countRows(t, st, `SELECT COUNT(*) FROM runs WHERE archived = 0 AND project_root = '/repo'`); got != 1 {
		t.Fatalf("live /repo runs left = %d, want 1 (report %+v)", got, report)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM runs WHERE id = ?`, live); got != 1 {
		t.Errorf("/repo's newest live run %d was retired", live)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM runs WHERE id = ?`, archived); got != 1 {
		t.Errorf("archived run %d was retired", archived)
	}
	assertNoOrphans(t, st)
}

// The days bound is independent of the runs bound and uses the injected clock.
func TestPruneDaysBound(t *testing.T) {
	st := newRetainStore(t)
	insertRun(t, st, "2026-07-01T00:00:00Z", "/repo", false) // 107 days before now
	insertRun(t, st, "2026-09-20T00:00:00Z", "/repo", false) // 26 days before now
	insertRun(t, st, "2026-10-15T00:00:00Z", "/repo", false) // 1 day before now
	report, err := st.Prune(RetainPolicy{Days: 30, Now: retainNow(t)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Runs != 1 {
		t.Errorf("Runs = %d, want 1 (only the July run is older than 30 days)", report.Runs)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM runs WHERE started_at = '2026-07-01T00:00:00Z'`); got != 0 {
		t.Error("the out-of-window run survived")
	}
	assertNoOrphans(t, st)
}

// Both bounds together keep the union: a run survives if either bound covers
// it, which is what "runs: N, days: D" has to mean for the two to be usable
// at once.
func TestPruneBoundsAreUnioned(t *testing.T) {
	st := newRetainStore(t)
	recent := insertRun(t, st, "2026-10-15T00:00:00Z", "/repo", false) // inside 30 days
	newest := insertRun(t, st, "2026-10-16T00:00:00Z", "/repo", false)
	for i := 1; i <= 8; i++ {
		insertRun(t, st, fmt.Sprintf("2026-08-%02dT00:00:00Z", i), "/repo", false)
	}
	report, err := st.Prune(RetainPolicy{Runs: 2, Days: 30, Now: retainNow(t)})
	if err != nil {
		t.Fatal(err)
	}
	// The two newest satisfy the run bound; the 2026-10-15 run is inside the
	// day window even though it is third newest.
	for _, id := range []int64{recent, newest} {
		if got := countRows(t, st, `SELECT COUNT(*) FROM runs WHERE id = ?`, id); got != 1 {
			t.Errorf("run %d was retired but a bound covers it", id)
		}
	}
	// 10 runs: the 8 August ones are outside both bounds, and the 2026-10-15 run
	// is third newest but inside the 30-day window, so it survives the run bound.
	if report.Runs != 8 || report.Kept != 2 {
		t.Errorf("report = %+v, want 8 retired and 2 kept", report)
	}
	assertNoOrphans(t, st)
}

// The reported counts are the rows actually deleted, and -dry-run predicts the
// same numbers without touching the database.
func TestPruneCountsMatchDelete(t *testing.T) {
	st := newRetainStore(t)
	for i := 1; i <= 12; i++ {
		insertRun(t, st, fmt.Sprintf("2026-03-%02dT00:00:00Z", i), "/repo", false)
	}
	p := RetainPolicy{Runs: 4}
	predicted, err := st.PruneCounts(p)
	if err != nil {
		t.Fatal(err)
	}
	before := runIDs(t, st)
	actual, err := st.Prune(p)
	if err != nil {
		t.Fatal(err)
	}
	if predicted != actual {
		t.Errorf("dry-run predicted %+v, prune did %+v", predicted, actual)
	}
	gone := len(before) - countRows(t, st, `SELECT COUNT(*) FROM runs`)
	if gone != actual.Runs {
		t.Errorf("%d run rows disappeared but the report says %d", gone, actual.Runs)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM findings`); got != len(before)-actual.Runs {
		t.Errorf("findings left = %d, want %d", got, len(before)-actual.Runs)
	}
	assertNoOrphans(t, st)
}

// Nothing to retire means nothing is written: the empty case must not spend a
// delete transaction (or a commit) on a store that is already within bounds.
func TestPruneWithinBoundsIsANoop(t *testing.T) {
	st := newRetainStore(t)
	insertRun(t, st, "2026-10-15T00:00:00Z", "/repo", false)
	insertRun(t, st, "2026-10-16T00:00:00Z", "/repo", false)
	report, err := st.Prune(RetainPolicy{Runs: 10})
	if err != nil {
		t.Fatal(err)
	}
	if report.Runs != 0 || report.Findings != 0 || report.Snapshots != 0 || report.Kept != 2 {
		t.Errorf("report = %+v, want no removals and Kept 2", report)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM findings`); got != 2 {
		t.Errorf("findings = %d, want 2", got)
	}
}

// PruneCounts must not write: the CLI's -dry-run is the documented first step,
// and an operator checking a policy on a production store must be able to
// trust it.
func TestPruneCountsDoesNotWrite(t *testing.T) {
	st := newRetainStore(t)
	for i := 1; i <= 6; i++ {
		insertRun(t, st, fmt.Sprintf("2026-04-%02dT00:00:00Z", i), "/repo", false)
	}
	before := runIDs(t, st)
	if _, err := st.PruneCounts(RetainPolicy{Runs: 1}); err != nil {
		t.Fatal(err)
	}
	after := runIDs(t, st)
	if len(before) != len(after) {
		t.Fatalf("PruneCounts changed the run table: %v -> %v", before, after)
	}
	if got := countRows(t, st, `SELECT COUNT(*) FROM findings`); got != 6 {
		t.Errorf("findings = %d, want 6", got)
	}
}

// TestRetentionAgreesWithPrune is the assertion that makes Retention usable:
// the count the cockpit shows and the rows Prune actually removes have to
// describe the same selection. They are written as two different WHERE clauses
// (one keeps, one deletes), so nothing but a test stops them from drifting.
func TestRetentionAgreesWithPrune(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy RetainPolicy
	}{
		{"runs bound", RetainPolicy{Runs: 3}},
		{"days bound", RetainPolicy{Days: 30}},
		{"both bounds", RetainPolicy{Runs: 2, Days: 30}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newRetainStore(t)
			seedRetentionFixture(t, st)

			tc.policy.Now = retainNow(t)
			before, err := st.Retention(tc.policy)
			if err != nil {
				t.Fatal(err)
			}
			if before.Total != len(retentionFixture) {
				t.Errorf("Total = %d, want %d", before.Total, len(retentionFixture))
			}
			report, err := st.Prune(tc.policy)
			if err != nil {
				t.Fatal(err)
			}
			// Kept is the survivor count, so it is directly comparable with what
			// Prune leaves behind: the two must agree row for row.
			if before.Kept != report.Kept {
				t.Errorf("Retention said %d runs survive, Prune left %d (removed %d of %d)",
					before.Kept, report.Kept, report.Runs, before.Total)
			}
		})
	}
}

// TestRetentionUnlimitedKeepsEverything pins the default configuration: no
// bound means every run is kept, including archived ones, and the count says
// so without touching the database.
func TestRetentionUnlimitedKeepsEverything(t *testing.T) {
	st := newRetainStore(t)
	seedRetentionFixture(t, st)
	got, err := st.Retention(RetainPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	want := Retention{Kept: len(retentionFixture), Total: len(retentionFixture)}
	if got != want {
		t.Errorf("Retention = %+v, want %+v", got, want)
	}
}

// retentionFixture is the shape TestRetentionAgreesWithPrune and the other
// retention tests share: several runs in one project, one in another, one
// archived, and one timestamp far enough back that the day bound separates it.
var retentionFixture = []struct {
	at       string
	project  string
	archived bool
}{
	{"2026-10-16T11:00:00Z", "/repo", false},
	{"2026-10-15T11:00:00Z", "/repo", false},
	{"2026-10-14T11:00:00Z", "/repo", false},
	{"2026-10-13T11:00:00Z", "/repo", false},
	{"2026-10-12T11:00:00Z", "/repo", true},
	{"2026-09-01T11:00:00Z", "/other", false},
	{"2026-07-01T11:00:00Z", "", false},
}

func seedRetentionFixture(t *testing.T, st *Store) {
	t.Helper()
	for _, run := range retentionFixture {
		insertRun(t, st, run.at, run.project, run.archived)
	}
}
