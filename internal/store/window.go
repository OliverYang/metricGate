package store

import (
	"strings"
	"time"
)

// Window scopes a trend query. A limit alone answers "the most recent N runs",
// which is the wrong question the moment the operator wants a date range: the
// last 20 runs may span three months or the last hour, and neither number says
// anything about when they happened.
//
// The zero value is unchanged behaviour: every run, no lower bound.
type Window struct {
	// Limit is how many of the most recent runs to include (0 = all).
	Limit int
	// Since keeps only runs started at or after this instant. The zero time
	// means "no lower bound".
	Since time.Time
	// ProjectRoot keeps only runs recorded under one verified project root.
	// Empty means every project (and the legacy runs with no project at all).
	ProjectRoot string
	// Unassigned keeps only runs recorded under no project identity — the store's
	// unknown-project bucket, which is what a shared database shows for runs
	// written before project roots were recorded, or written from outside any git
	// root. It is a separate flag rather than an empty ProjectRoot because the
	// two questions are opposite: "every project" vs "only the unassigned runs".
	// When both are set, Unassigned wins, and that pairing is a caller bug.
	Unassigned bool
}

// String renders the window for a report header, so the number a reader sees is
// always next to what it counted. Empty for the zero window (the common case)
// rather than printing "since the zero timestamp".
func (w Window) String() string {
	parts := []string{}
	if w.Limit > 0 {
		parts = append(parts, "last "+itoa(w.Limit)+" run(s)")
	}
	if !w.Since.IsZero() {
		parts = append(parts, "since "+w.Since.UTC().Format(time.RFC3339))
	}
	if w.ProjectRoot != "" {
		parts = append(parts, "project "+w.ProjectRoot)
	}
	if w.Unassigned {
		parts = append(parts, "unassigned runs")
	}
	if len(parts) == 0 {
		return "all runs"
	}
	return strings.Join(parts, ", ")
}

// itoa keeps the header free of a strconv import for one integer.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

// runFilter builds the WHERE clause shared by the runs-side trend queries: the
// optional lower bound on started_at, and the optional project scope.
//
// Both live in one place because a query that filtered by date but not by
// project would answer "how did this repository trend in the last 90 days?"
// with the numbers of every other repository in the shared database — the
// failure mode of a store several projects write to (the Cockpit keeps one
// database per operator, not one per repository).
func (w Window) runFilter() (clause string, args []any) {
	where, filterArgs := w.runScope(2)
	if where == "" {
		return "", nil
	}
	return ` WHERE ` + where, filterArgs
}

// metricFilter is the snapshot-side twin of runFilter: the metric snapshots
// carry no project_root of their own (they inherit it from their run), so a
// project or date scope has to be applied to the run they belong to.
func (w Window) metricFilter() (clause string, args []any) {
	where, filterArgs := w.runScope(2)
	if where == "" {
		return "", nil
	}
	return ` WHERE ` + where, filterArgs
}

// runScope renders the window's own conditions (no archive mode, no limit) as a
// clause fragment without the leading WHERE, ready to be embedded in a larger
// condition list. It is the shared core of runFilter, metricFilter and
// (*Store).runScope, so the runs side and the snapshot side of one scope are
// literally the same predicate text.
func (w Window) runScope(archiveMode int) (clause string, args []any) {
	var conds []string
	if !w.Since.IsZero() {
		conds = append(conds, `started_at >= ?`)
		args = append(args, w.Since.UTC().Format(time.RFC3339))
	}
	if w.ProjectRoot != "" {
		conds = append(conds, `project_root = ?`)
		args = append(args, w.ProjectRoot)
	}
	if w.Unassigned {
		conds = append(conds, `(project_root IS NULL OR project_root = '')`)
	}
	return strings.Join(conds, ` AND `), args
}
