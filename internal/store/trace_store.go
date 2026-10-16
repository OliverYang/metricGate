package store

import (
	"database/sql"
	"sort"
)

// This file holds the trace side of the store: the attribute table the X2
// feature writes, and the queries that turn a trace id or an attribute value
// into the run scope a report needs. It lives apart from store.go because
// store.go is already at its file-length budget, and because "everything the
// store knows about traces" reads better as one file than as four islands
// between the run queries.

// Attr is one key/value pair attached to a trace. It mirrors trace.Attr but
// lives here so the store owns its own persistence type and a caller that only
// writes a run does not have to import the vocabulary package.
type Attr struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// saveTraceAttrs upserts one trace's attributes. The same trace commonly spans
// several runs (one task, many checks), so the second run must update rather
// than fail on the primary key — the latest value is the one the operator most
// recently asserted.
func saveTraceAttrs(tx *sql.Tx, traceID string, attrs []Attr) error {
	for _, a := range attrs {
		if _, err := tx.Exec(`INSERT INTO trace_attrs (trace_id, key, value) VALUES (?, ?, ?)
			ON CONFLICT(trace_id, key) DO UPDATE SET value = excluded.value`,
			traceID, a.Key, a.Value); err != nil {
			return err
		}
	}
	return nil
}

// TraceAttrs returns the attributes recorded for one trace id, sorted by key.
// An unknown trace id yields an empty slice, not an error: a trace whose run
// predates the attribute feature is indistinguishable from one that simply had
// no attributes set, and both are legal.
func (s *Store) TraceAttrs(traceID string) ([]Attr, error) {
	if traceID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT key, value FROM trace_attrs WHERE trace_id = ? ORDER BY key`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attr
	for rows.Next() {
		var a Attr
		if err := rows.Scan(&a.Key, &a.Value); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Traces returns the distinct trace ids in the run history, newest first by
// their most recent run. Empty trace ids (runs recorded without a trace) are
// excluded: "no trace" is a legal state, not a trace named "".
func (s *Store) Traces() ([]string, error) {
	return s.queryStrings(`SELECT trace_id FROM runs
		WHERE trace_id IS NOT NULL AND trace_id != ''
		GROUP BY trace_id ORDER BY MAX(started_at) DESC, trace_id ASC`)
}

// ResolveTraceAttr maps a whitelisted attribute key/value to the trace ids that
// carry it, so a `trends -model X` filter can be turned into a Window.TraceIDs
// scope without the caller writing SQL. Matching is exact on value.
func (s *Store) ResolveTraceAttr(key, value string) ([]string, error) {
	return s.queryStrings(`SELECT trace_id FROM trace_attrs WHERE key = ? AND value = ? ORDER BY trace_id`, key, value)
}

// TraceFilter is one trace-scoping clause: either a literal trace id (Key
// empty) or an attribute match (Key set). The CLI's -trace/-model/-prompt-digest
// flags and the MCP tool's three arguments are both lists of these, so the
// resolution lives here once instead of being re-implemented per surface.
type TraceFilter struct {
	Key   string
	Value string
}

// ResolveTraceFilters turns a set of filters into the trace ids they select,
// unioned (a run matching any filter is kept). It returns nil when no filter
// carries a value — the caller's signal for "every trace" — and a non-nil slice
// (possibly empty) otherwise, so a filter that matches nothing is a real "no
// runs" answer rather than a silent widening back to everything.
func (s *Store) ResolveTraceFilters(filters []TraceFilter) ([]string, error) {
	if !anyTraceFilter(filters) {
		return nil, nil
	}
	seen := map[string]bool{}
	ids := []string{}
	for _, f := range filters {
		found, err := s.resolveOneFilter(f)
		if err != nil {
			return nil, err
		}
		for _, id := range found {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// anyTraceFilter reports whether any filter carries a value.
func anyTraceFilter(filters []TraceFilter) bool {
	for _, f := range filters {
		if f.Value != "" {
			return true
		}
	}
	return false
}

// resolveOneFilter turns one filter into the trace ids it names: the literal
// value for a bare -trace, or the attribute lookup for a keyed filter. An empty
// value resolves to nothing.
func (s *Store) resolveOneFilter(f TraceFilter) ([]string, error) {
	if f.Value == "" {
		return nil, nil
	}
	if f.Key == "" {
		return []string{f.Value}, nil
	}
	return s.ResolveTraceAttr(f.Key, f.Value)
}

// queryStrings runs a single-column text query and collects the rows. Project
// roots, trace ids and attribute matches differ only in their SQL, so the
// scan/collect/close boilerplate lives here once instead of being copy-pasted
// per query — the duplication the clone detector would otherwise flag.
func (s *Store) queryStrings(q string, args ...any) ([]string, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
