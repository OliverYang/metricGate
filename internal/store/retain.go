package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Retention: the store has no delete path of its own, so a database that is
// written on every check grows without bound (measured 2026-10-16: 2.39 GB,
// 1410 runs, 21.1M metric snapshots). This file is the only place that removes
// history, and it removes it in one transaction so a reader never observes a
// run without its findings.

// RetainPolicy is a retention policy in the form the store consumes. The zero
// value keeps everything: Runs and Days are independent bounds and 0 means
// "unlimited" for each, so "newest 500 runs" and "last 90 days" are both
// expressible and neither is implied by the other.
type RetainPolicy struct {
	// Runs keeps the newest N runs (0 = unlimited).
	Runs int
	// Days keeps runs started within the last D days (0 = unlimited).
	Days int
	// Now overrides the clock, for tests that need a fixed boundary. Zero means
	// the current time, so production callers never set it.
	Now time.Time
}

// Unlimited reports whether the policy retires nothing. A caller must not run
// a prune in that case: with no bound every run is kept, and a delete pass
// would only spend a write transaction on an empty set.
func (p RetainPolicy) Unlimited() bool { return p.Runs <= 0 && p.Days <= 0 }

func (p RetainPolicy) now() time.Time {
	if p.Now.IsZero() {
		return time.Now().UTC()
	}
	return p.Now.UTC()
}

// PruneReport says what a prune did, or what one would do. The counts are the
// rows actually removed, not an estimate: the CLI prints them and the cockpit
// shows them, so a number that disagrees with the database would be a new lie
// in the place this iteration exists to make honest.
// Kept is the number of runs remaining after the operation the report
// describes: after the prune for Prune, and after the prune it predicts for
// PruneCounts. Both methods answer the same question, so the same field can be
// compared between them.
type PruneReport struct {
	Runs      int
	Findings  int
	Snapshots int
	Kept      int
}

// Vacuum rewrites the database file, returning the pages a prune freed.
//
// pruneSelection renders the subquery selecting the runs a policy retires.
//
// It is one SELECT reused by every count and every delete rather than a list
// of ids, for two reasons. SQLite bounds the number of bound parameters, and a
// repository with a hundred thousand runs would exceed it. And the three
// deletes must agree on exactly the same set: reusing one subquery is what
// makes "no orphan rows" structural instead of something the caller has to
// remember.
//
// The rules, in the order they appear:
//
//   - archived = 0: archiving a run is an explicit operator decision, so
//     automatic retention never overrides it.
//   - newest N kept: ORDER BY started_at DESC, id DESC makes the boundary
//     deterministic when two runs share a second (they do — a check is fast).
//   - one run per project kept: the dashboard's project cards distinguish
//     "has findings" / "only runs" / "empty" from the run history, so deleting
//     a project's last run would silently move its card to the empty state.
//     An empty or NULL project_root is one bucket, matching how
//     project_totals.go groups. The bucket's newest run is read from the
//     non-archived rows only, because RunTotalsByProject counts only those:
//     protecting an archived run would leave the card with nothing to count
//     and move it to the empty state anyway, which is the outcome this rule
//     exists to prevent. "Newest" uses the same (started_at, id) key as the
//     run bound rather than MAX(id), so the two rules cannot disagree about
//     which run is newest when rows were not inserted in time order.
//   - started_at < cutoff: text comparison is safe because every row this
//     store writes is UTC RFC3339 (fixed width, lexicographic order).
func pruneSelection(p RetainPolicy) (string, []any) {
	where := []string{"archived = 0"}
	bounds, args := retentionBounds(p)
	for _, b := range bounds {
		where = append(where, b.del)
	}
	where = append(where, newestPerProjectClause)
	return "SELECT id FROM runs WHERE " + strings.Join(where, " AND "), args
}

// newestPerProjectClause selects the runs that have a newer live run in their
// own project bucket — the exact complement of the "one run per project" floor
// rule. It is a shared constant because two selections depend on it: the delete
// selection excludes these runs, and Retention counts them as survivors. Two
// copies would be two chances to disagree about which run is newest, and the
// disagreement would show up as a prune that keeps a run the dashboard never
// listed (or the reverse).
const newestPerProjectClause = `EXISTS (SELECT 1 FROM runs newer WHERE newer.archived = 0
		AND COALESCE(NULLIF(newer.project_root, ''), '') = COALESCE(NULLIF(runs.project_root, ''), '')
		AND (newer.started_at > runs.started_at OR (newer.started_at = runs.started_at AND newer.id > runs.id)))`

// Retention reports how many runs a policy keeps right now.
//
// It is the cheap half of PruneCounts: counting findings and metric snapshots
// scans the two largest tables in the database (7s on a 21M-row history), while
// the runs-only selection reads an index and answers in milliseconds. The
// dashboard repaints on every request, so it gets the number that stays honest
// at that price; `store prune -dry-run` is where the row counts are paid for.
//
// Kept and Total are taken in one query so a concurrent check cannot make the
// pair describe two different databases — the failure this whole iteration
// exists to stop printing.
type Retention struct {
	Kept  int `json:"kept"`
	Total int `json:"total"`
}

func (s *Store) Retention(p RetainPolicy) (Retention, error) {
	if p.Unlimited() {
		total, err := s.Runs()
		return Retention{Kept: total, Total: total}, err
	}
	bounds, args := retentionBounds(p)
	// A run survives when any of these holds:
	//   - it is archived: retention never overrides an explicit operator decision;
	//   - it sits inside a bound that is set. The bound clauses are OR'd, not
	//     AND'd, because the delete selection removes a run only when it falls
	//     outside *every* bound — a run inside the newest-N window survives even
	//     if it is older than the day cutoff, and the other way round;
	//   - it has no newer live run in its own project bucket, which is the
	//     survivor of the one-run-per-project floor rule.
	keep := []string{"archived = 1"}
	for _, b := range bounds {
		keep = append(keep, b.keep)
	}
	keep = append(keep, "NOT "+newestPerProjectClause)
	var r Retention
	err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM runs WHERE `+strings.Join(keep, " OR ")+
		`), (SELECT COUNT(*) FROM runs)`, args...).Scan(&r.Kept, &r.Total)
	if err != nil {
		return Retention{}, fmt.Errorf("retention counts: %w", err)
	}
	return r, nil
}

// boundClauses carries both faces of one retention bound: the condition that
// selects runs for deletion, and the condition that selects them for survival.
// They are produced together, from the same argument, because a bound written
// once and negated by hand is a bound that can be negated wrongly — and the
// symptom would be a `kept` count that disagrees with what Prune actually
// removes, which is precisely the class of number this iteration exists to
// stop printing.
type boundClauses struct {
	del  string
	keep string
}

// retentionBounds renders the two configurable bounds, delete-face and
// keep-face, with their arguments in the order the delete faces use them.
func retentionBounds(p RetainPolicy) ([]boundClauses, []any) {
	var bounds []boundClauses
	var args []any
	if p.Runs > 0 {
		newest := "SELECT id FROM runs ORDER BY started_at DESC, id DESC LIMIT ?"
		bounds = append(bounds, boundClauses{
			del:  "id NOT IN (" + newest + ")",
			keep: "id IN (" + newest + ")",
		})
		args = append(args, p.Runs)
	}
	if p.Days > 0 {
		cutoff := p.now().AddDate(0, 0, -p.Days).Format(time.RFC3339)
		bounds = append(bounds, boundClauses{del: "started_at < ?", keep: "started_at >= ?"})
		args = append(args, cutoff)
	}
	return bounds, args
}

// Prune retires the runs a policy excludes, together with their findings and
// metric snapshots, in one transaction.
//
// The child deletes are explicit. The schema declares
// `run_id INTEGER NOT NULL REFERENCES runs(id)` but the connection never sets
// `PRAGMA foreign_keys = ON`, so those references are documentation: deleting a
// run row alone would leave its findings and snapshots behind, which grows the
// file instead of shrinking it. See pruneDelete for the statement order.
func (s *Store) Prune(p RetainPolicy) (PruneReport, error) {
	if p.Unlimited() {
		kept, err := s.Runs()
		return PruneReport{Kept: kept}, err
	}
	sel, args := pruneSelection(p)
	tx, err := s.db.Begin()
	if err != nil {
		return PruneReport{}, err
	}
	defer tx.Rollback()

	report, err := pruneCount(tx, sel, args)
	if err != nil {
		return PruneReport{}, err
	}
	if report.Runs == 0 {
		// Nothing to retire: commit nothing, and do not spend a delete pass on
		// an empty set. Kept still has to be reported, because "nothing was
		// removed" and "nothing is left" are different answers.
		return report, pruneKept(tx, &report)
	}
	if err := pruneDelete(tx, sel, args, report); err != nil {
		return PruneReport{}, err
	}
	if err := pruneKept(tx, &report); err != nil {
		return PruneReport{}, err
	}
	// The record is written in the same transaction as the delete, so a rollback
	// cannot leave a claim that history was trimmed when it was not. A pass that
	// retired nothing writes nothing: prunedLastWrite answers "was history ever
	// trimmed, and how much", and a no-op would erase the last real answer.
	if err := recordPrune(tx, PruneRecord{
		At:        p.now(),
		Runs:      report.Runs,
		Findings:  report.Findings,
		Snapshots: report.Snapshots,
		Kept:      report.Kept,
	}); err != nil {
		return PruneReport{}, err
	}
	return report, tx.Commit()
}

// pruneKept fills in how many runs remain. It is one query in one place because
// both exits from Prune report the same field, and a report that answered this
// differently depending on which exit it took would be the kind of number this
// iteration exists to stop printing.
func pruneKept(tx *sql.Tx, report *PruneReport) error {
	return tx.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&report.Kept)
}

// pruneDelete removes the rows of one selection, children first.
//
// The three deletes are one table each rather than a cascade. The schema
// declares `run_id INTEGER NOT NULL REFERENCES runs(id)` but the connection
// never sets `PRAGMA foreign_keys = ON`, so those references are documentation:
// deleting a run row alone would leave its findings and snapshots behind, which
// grows the file instead of shrinking it. The order is children first, parents
// last, and each step is checked against the count taken from the same
// subquery — a disagreement means the two statements saw different sets, which
// is a correctness bug whose number must not be reported as if it were fine.
func pruneDelete(tx *sql.Tx, sel string, args []any, want PruneReport) error {
	for _, step := range []struct {
		what string
		stmt string
		want int
	}{
		{"findings", `DELETE FROM findings WHERE run_id IN (` + sel + `)`, want.Findings},
		{"metric_snapshots", `DELETE FROM metric_snapshots WHERE run_id IN (` + sel + `)`, want.Snapshots},
		{"runs", `DELETE FROM runs WHERE id IN (` + sel + `)`, want.Runs},
	} {
		res, err := tx.Exec(step.stmt, args...)
		if err != nil {
			return fmt.Errorf("prune %s: %w", step.what, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("prune %s: %w", step.what, err)
		}
		if int(n) != step.want {
			return fmt.Errorf("prune %s: deleted %d rows but counted %d", step.what, n, step.want)
		}
	}
	return nil
}

// PruneCounts reports what Prune would remove without removing anything. It is
// the implementation behind `-dry-run`: the same selection, the same counts,
// read-only.
//
// Kept is the number of runs that would REMAIN, not the number present now, so
// it means the same thing as the Kept in a Prune report. A dry run that
// answered "kept 1410" while the prune it describes leaves 206 would be
// exactly the kind of number this iteration exists to stop printing.
func (s *Store) PruneCounts(p RetainPolicy) (PruneReport, error) {
	kept, err := s.Runs()
	if err != nil {
		return PruneReport{}, err
	}
	if p.Unlimited() {
		return PruneReport{Kept: kept}, nil
	}
	sel, args := pruneSelection(p)
	tx, err := s.db.Begin()
	if err != nil {
		return PruneReport{}, err
	}
	defer tx.Rollback()
	report, err := pruneCount(tx, sel, args)
	if err != nil {
		return PruneReport{}, err
	}
	report.Kept = kept - report.Runs
	return report, nil
}

// pruneCount counts the rows the selection covers. A transaction is used even
// for counting so the three counts are mutually consistent (a concurrent
// writer inserting a run mid-count would otherwise be able to make the
// findings count and the runs count disagree).
func pruneCount(tx *sql.Tx, sel string, args []any) (PruneReport, error) {
	var report PruneReport
	for _, q := range []struct {
		stmt string
		into *int
	}{
		{`SELECT COUNT(*) FROM runs WHERE id IN (` + sel + `)`, &report.Runs},
		{`SELECT COUNT(*) FROM findings WHERE run_id IN (` + sel + `)`, &report.Findings},
		{`SELECT COUNT(*) FROM metric_snapshots WHERE run_id IN (` + sel + `)`, &report.Snapshots},
	} {
		if err := tx.QueryRow(q.stmt, args...).Scan(q.into); err != nil {
			return PruneReport{}, err
		}
	}
	return report, nil
}

// Vacuum rewrites the database file, returning the pages a prune freed.
//
// A DELETE marks pages free; it does not shorten the file. Measured on a
// 2.39 GB store: after deleting 1204 runs and ~20M snapshots the file is
// unchanged, and only VACUUM brings it down. That is why the vacuum is a
// separate, explicitly requested step: it needs free disk equal to the
// database and holds a write lock for the whole rewrite, which is not
// something a check should do on the way out.
func (s *Store) Vacuum() error {
	_, err := s.db.Exec(`VACUUM`)
	return err
}
