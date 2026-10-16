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
	// TraceIDs keeps only runs attributed to one of these traces. A host that
	// wants "everything I did for prompt v3" first resolves the attribute to a
	// set of trace ids (Store.ResolveTraceAttr) and then scopes the report to
	// them.
	//
	// nil means "every trace" (the common case). A non-nil slice means "only
	// these traces" — including an empty non-nil slice, which means the caller
	// asked to scope by trace and nothing matched. That distinction is the
	// whole point: a -model filter that resolves to no trace must answer "no
	// runs", never silently widen back to every run.
	TraceIDs []string
}

// String renders the window for a report header, so the number a reader sees is
// always next to what it counted. Empty for the zero window (the common case)
// rather than printing "since the zero timestamp".
func (w Window) String() string {
	parts := []string{}
	if w.Limit > 0 {
		parts = append(parts, "last "+itoa(w.Limit)+" "+runNoun(w.Limit))
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
	if w.TraceIDs != nil {
		if len(w.TraceIDs) == 0 {
			parts = append(parts, "no matching trace")
		} else {
			parts = append(parts, "trace "+strings.Join(w.TraceIDs, ","))
		}
	}
	if len(parts) == 0 {
		return "all runs"
	}
	return strings.Join(parts, ", ")
}

// runNoun is the pluralised unit of a window limit: one run is "run", every
// other count is "runs". Without it a window of 1 rendered as "last 1 run(s)",
// a form no other sentence in the UI uses.
func runNoun(n int) string {
	if n == 1 {
		return "run"
	}
	return "runs"
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
	if w.TraceIDs != nil {
		// A non-nil slice scopes to exactly these traces. An empty one means
		// "no trace matched", which must select nothing: the literal predicate
		// `trace_id IN ()` is invalid SQL, so the always-false form is used
		// instead. trace_id is nullable, so an IN over real ids correctly
		// excludes pre-column runs rather than matching NULL.
		if len(w.TraceIDs) == 0 {
			return strings.Join(append(conds, `1 = 0`), ` AND `), args
		}
		placeholders := make([]string, len(w.TraceIDs))
		for i, id := range w.TraceIDs {
			placeholders[i] = `?`
			args = append(args, id)
		}
		conds = append(conds, `trace_id IN (`+strings.Join(placeholders, `, `)+`)`)
	}
	return strings.Join(conds, ` AND `), args
}
