package store

import (
	"fmt"
)

// RunTotalsByProject counts every non-archived run that carries a project
// identity, in one grouped query.
//
// The dashboard needs this per project card, and seven projects each paying for
// their own COUNT(*) is seven scans of the runs table on a database that is
// gigabytes of history. One GROUP BY answers all of them at once, and a project
// the map does not mention is one with no runs — the caller treats a missing key
// as zero rather than as an error.
func (s *Store) RunTotalsByProject() (map[string]int, error) {
	return s.countsByProject(`SELECT project_root, COUNT(*) FROM runs
		WHERE archived = 0 AND project_root IS NOT NULL AND project_root != ''
		GROUP BY project_root`)
}

// OpenIssueTotalsByProject counts the issues still open in the latest run of
// each project, in one grouped query.
//
// It is the "one run only" view the project cards show, computed without the
// dashboard ever opening a per-project query window: the run ids are narrowed
// to the newest run per project first, then grouped by fingerprint — which is
// what the issues query itself does to collapse a finding that appears many
// times inside one run — and only the fingerprints holding no verdict are
// counted.
//
// The verdict set matches how the store already reads verdicts back:
// statusForRow decides on IssueState.Status alone, so `expires` is not consulted
// and this query does not either. A stored "open" therefore stays open (someone
// reopened it on purpose) and an expired concession is likewise open.
func (s *Store) OpenIssueTotalsByProject() (map[string]int, error) {
	query := `WITH latest AS (
		SELECT project_root AS pr, MAX(id) AS rid FROM runs
		WHERE project_root IS NOT NULL AND project_root != '' AND archived = 0 GROUP BY project_root
	), cand AS (
		SELECT l.pr AS pr, f.fingerprint AS fp
		FROM findings f JOIN latest l ON f.run_id = l.rid GROUP BY l.pr, f.fingerprint
	)
	SELECT pr, COUNT(*) FROM cand WHERE cand.fp NOT IN
		(SELECT fingerprint FROM issue_states WHERE status IN ('confirmed','false_positive','accepted'))
	GROUP BY pr`
	return s.countsByProject(query)
}

// countsByProject runs a grouped query whose first column is a project root and
// whose second column is a count, and hands back the counts keyed by root.
func (s *Store) countsByProject(query string, args ...any) (map[string]int, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("count by project: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var root string
		var count int
		if err := rows.Scan(&root, &count); err != nil {
			return nil, fmt.Errorf("count by project: %w", err)
		}
		out[root] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("count by project: %w", err)
	}
	return out, nil
}
