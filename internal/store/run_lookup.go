package store

// Single-run lookups, kept apart from the windowed listings in read.go. A
// window answers "which runs are in scope"; these answer "what was this one
// run", which callers reach for when they hold a run id from a finding rather
// than from a listing — the source view resolves a finding's relative path
// against the root of the run that recorded it.

// runColumns is the runs column list in the order scanRuns expects it. It is a
// constant rather than repeated per query because it is the one place a new
// provenance column is added, and a query that forgets it fails at Scan time
// rather than silently reading the wrong field into the wrong struct member.
const runColumns = `id, started_at, duration_ms, tool_version, COALESCE(git_commit, ''),
	             files, error_count, warning_count, info_count, COALESCE(project_root, ''),
	             COALESCE(policy_digest, ''), COALESCE(trace_id, ''), archived`

// RunByID returns one run by primary key, and whether it exists. Callers that
// need the context a finding was recorded in — the project root its relative
// file path is anchored to — read it here rather than re-deriving it from the
// window, because a finding can be older than every run still listed.
func (s *Store) RunByID(id int64) (RunRecord, bool, error) {
	runs, err := s.scanRuns(`SELECT `+runColumns+` FROM runs WHERE id = ?`, []any{id})
	if err != nil {
		return RunRecord{}, false, err
	}
	if len(runs) == 0 {
		return RunRecord{}, false, nil
	}
	return runs[0], true, nil
}
