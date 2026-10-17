package store

import (
	"fmt"
	"path/filepath"
	"strings"

	"metricgate/internal/model"
)

// --- read APIs (P2 v1) ----------------------------------------------------
//
// These queries power `metricgate trends` and any dashboard tool built on
// top of the local store. They are pure reads; the write side above is the
// one and only place that mutates the schema. Trends are derived on read
// rather than precomputed, so an existing database does not need a backfill
// when a new aggregation is added.

// RunRecord is one stored run, in display-ready units.
type RunRecord struct {
	ID           int64
	StartedAt    string // RFC3339 UTC
	DurationMs   int64
	ToolVersion  string
	GitCommit    string
	Files        int
	ProjectRoot  string
	PolicyDigest string
	TraceID      string
	// Invocation is the command line that produced this run, or empty when the
	// run predates the column or the caller did not supply one.
	Invocation   string
	ErrorCount   int
	WarningCount int
	InfoCount    int
	Archived     bool
}

// TrendPoint is one step of a per-run severity series.
type TrendPoint struct {
	StartedAt string // RFC3339 UTC
	Errors    int
	Warnings  int
	Infos     int
}

// SeverityTrend is the run-to-run severity series (oldest first).
type SeverityTrend struct {
	Runs   int
	Series []TrendPoint
}

// RuleCount is the aggregated frequency of a single rule over a window of
// runs. "Count" is the number of findings of that rule, summed across all
// runs in the window — a high value means the rule is firing often.
type RuleCount struct {
	RuleID string
	Count  int
}

// TrendRange is the first and last run timestamp in a report window.
type TrendRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// TrendReport is the deterministic, JSON-friendly summary of the recent trend
// window: a severity series (oldest first), the range it spans, and the rules
// that fired most often. It is shared by the CLI `trends` verb and the MCP
// server so the two payloads cannot drift. Adding a field is safe (consumers
// ignore extras); renaming is a breaking change.
type TrendReport struct {
	Runs     int          `json:"runs"`
	Range    *TrendRange  `json:"range,omitempty"`
	Series   []TrendPoint `json:"series"`
	TopRules []RuleCount  `json:"topRules"`
	// Window states the scope the series and rule counts were folded over, so a
	// report read next to a hand-exported one says which runs it covered
	// instead of leaving the reader to guess from "runs": 20.
	Window string `json:"window"`
}

// Report folds the most recent limit runs (limit <= 0 means every run) into one
// report. A store with no runs yields a zero report, not an error; the series
// and rule lists are always non-nil so the JSON reads "[]" rather than "null".
func (s *Store) Report(limit int) (TrendReport, error) {
	return s.ReportIn(Window{Limit: limit})
}

// ReportIn is Report scoped by a Window.
//
// A report is assembled from three queries, and the window must reach all three
// or the parts disagree: a series that stops 90 days ago next to a top-rules
// list that spans every run in the database is the shape of a silently wrong
// answer. So the window is applied once, up front, and handed to each query.
func (s *Store) ReportIn(w Window) (TrendReport, error) {
	records, err := s.LatestRunsIn(w)
	if err != nil {
		return TrendReport{}, err
	}
	trend, err := s.SeverityTrendIn(w)
	if err != nil {
		return TrendReport{}, err
	}
	rules, err := s.RuleFrequencyIn(w)
	if err != nil {
		return TrendReport{}, err
	}
	out := TrendReport{Runs: len(records), Series: trend.Series, TopRules: rules, Window: w.String()}
	if out.Series == nil {
		out.Series = []TrendPoint{}
	}
	if out.TopRules == nil {
		out.TopRules = []RuleCount{}
	}
	if len(records) > 0 {
		// records are newest-first; series is oldest-first. The first record
		// is therefore the end of the range and the last record is the start.
		out.Range = &TrendRange{Start: records[len(records)-1].StartedAt, End: records[0].StartedAt}
	}
	return out, nil
}

// LatestRunsIn returns the runs the window admits, newest first. It is the one
// projection callers should use: it takes archive mode from the caller, so
// "which runs are in scope" is never accidental.
func (s *Store) LatestRunsIn(w Window) ([]RunRecord, error) {
	return s.latestRunsScoped(w, 2)
}

func (s *Store) ReportForProject(limit int, projectRoot string) (TrendReport, error) {
	return s.reportForScope(Window{Limit: limit, ProjectRoot: projectRoot}, false)
}

// ReportArchivedForProject folds archived runs for one project into a report.
func (s *Store) ReportArchivedForProject(limit int, projectRoot string) (TrendReport, error) {
	return s.reportForScope(Window{Limit: limit, ProjectRoot: projectRoot}, true)
}

func (s *Store) ReportUnassigned(limit int) (TrendReport, error) {
	return s.reportForScope(Window{Limit: limit, Unassigned: true}, false)
}

func (s *Store) ReportArchivedUnassigned(limit int) (TrendReport, error) {
	return s.reportForScope(Window{Limit: limit, Unassigned: true}, true)
}

func (s *Store) reportForScope(w Window, archived bool) (TrendReport, error) {
	archiveMode := 0
	if archived {
		archiveMode = 1
	}
	records, err := s.latestRunsScoped(w, archiveMode)
	if err != nil {
		return TrendReport{}, err
	}
	return s.reportForRecords(records, w, archiveMode)
}

// reportForRecords builds the report from an already-scoped record list, then
// re-scopes the rule-frequency query with the same window.
//
// It cannot reuse the records alone, because a finding count is a JOIN over
// runs, and the same window that selected the records must select the runs the
// counts aggregate — two different scopes in one report is the shape of a
// silently wrong answer.
func (s *Store) reportForRecords(records []RunRecord, w Window, archiveMode int) (TrendReport, error) {
	out := TrendReport{Runs: len(records), Series: make([]TrendPoint, 0, len(records)), TopRules: []RuleCount{}, Window: w.String()}
	for i := len(records) - 1; i >= 0; i-- {
		run := records[i]
		out.Series = append(out.Series, TrendPoint{StartedAt: run.StartedAt, Errors: run.ErrorCount, Warnings: run.WarningCount, Infos: run.InfoCount})
	}
	if len(records) > 0 {
		out.Range = &TrendRange{Start: records[len(records)-1].StartedAt, End: records[0].StartedAt}
	}
	rules, err := s.RuleFrequencyScoped(w, archiveMode)
	if err != nil {
		return TrendReport{}, err
	}
	out.TopRules = rules
	return out, nil
}

// RuleFrequencyScoped counts rule frequencies inside one archive mode and one
// window. It is the single place the rule-count query gets its scope, so
// Report, ReportIn and every reportFor* sibling agree by construction.
func (s *Store) RuleFrequencyScoped(w Window, archiveMode int) ([]RuleCount, error) {
	// Archive mode and project scope are two different questions: archive mode
	// is not part of Window, because a caller that wants archived scope states
	// it explicitly rather than leaving it implicit in a struct field.
	conds, args := s.runScope(archiveMode, w)
	inner := `SELECT id FROM runs WHERE ` + strings.Join(conds, ` AND `) + ` ORDER BY started_at DESC, id DESC`
	if w.Limit > 0 {
		inner += ` LIMIT ?`
		args = append(args, w.Limit)
	}
	query := `SELECT rule_id, COUNT(*) FROM findings WHERE run_id IN (` + inner + `)
	      GROUP BY rule_id ORDER BY COUNT(*) DESC, rule_id ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RuleCount
	for rows.Next() {
		var rule RuleCount
		if err := rows.Scan(&rule.RuleID, &rule.Count); err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

// ProjectRoot resolves an operator-supplied -project value to a root that is
// actually recorded in the run history.
//
// It deliberately does not treat an unknown value as a literal filter: a typo
// would then produce "0 runs" for a database that holds hundreds, which reads
// as "no work happened" rather than "no such project". Matching is exact
// first, then by path suffix (the repository name, as in the Cockpit's project
// picker), and a refusal lists every recorded root, because a refusal with
// nothing to choose from is a dead end.
func (s *Store) ProjectRoot(want string) (string, error) {
	want = strings.TrimSpace(want)
	if want == "" {
		return "", nil
	}
	roots, err := s.ProjectRoots()
	if err != nil {
		return "", err
	}
	if match, ok := matchProjectRoot(roots, want); ok {
		return match, nil
	}
	if len(roots) == 0 {
		return "", fmt.Errorf("no project is recorded in this database yet — run `metricgate check` first")
	}
	return "", fmt.Errorf("no recorded project matches %q; recorded project root(s): %s", want, strings.Join(roots, ", "))
}

// matchProjectRoot applies the three matching rules in order — exact, then
// canonical (symlink-resolved), then path suffix — and reports the first root
// that matches. Splitting them out keeps the three passes readable and stops
// the caller from becoming one deeply-nested loop.
func matchProjectRoot(roots []string, want string) (string, bool) {
	for _, root := range roots {
		if root == want {
			return root, true
		}
	}
	// A recorded root is what `git rev-parse` canonicalized (/private/tmp/x on
	// macOS), while an operator types the path their shell shows (/tmp/x). Exact
	// match fails on a path that is not wrong, so the resolver compares
	// canonical forms next — otherwise a correct project is refused for the
	// reason that the OS hides a symlink in it.
	canonical := canonicalPath(want)
	for _, root := range roots {
		if canonicalPath(root) == canonical {
			return root, true
		}
	}
	for _, root := range roots {
		if strings.HasSuffix(filepath.ToSlash(root), "/"+filepath.ToSlash(want)) {
			return root, true
		}
	}
	return "", false
}

// canonicalPath resolves symlinks so two spellings of one directory compare
// equal. It falls back to the cleaned path when the OS refuses the resolution:
// a path that cannot be resolved is still comparable on its own spelling, and
// refusing the whole lookup would be a worse answer than a literal compare.
func canonicalPath(path string) string {
	if path == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(resolved)
}

// ProjectRoots returns all non-empty project roots in the run history.
func (s *Store) ProjectRoots() ([]string, error) {
	return s.queryStrings(`SELECT DISTINCT project_root FROM runs
		WHERE project_root IS NOT NULL AND project_root != '' ORDER BY project_root`)
}

// LatestRuns returns the most recent runs, newest first. limit <= 0 means
// "every run" (still capped at the database size; this is for tests and the
// `metricgate trends` default window of 20).
func (s *Store) LatestRuns(limit int) ([]RunRecord, error) {
	return s.latestRunsScoped(Window{Limit: limit}, 2)
}

// LatestRunsForProject returns recent runs for exactly one verified project root.
func (s *Store) LatestRunsForProject(limit int, projectRoot string) ([]RunRecord, error) {
	return s.latestRunsScoped(Window{Limit: limit, ProjectRoot: projectRoot}, 0)
}

// LatestArchivedRunsForProject returns archived runs for exactly one project.
func (s *Store) LatestArchivedRunsForProject(limit int, projectRoot string) ([]RunRecord, error) {
	return s.latestRunsScoped(Window{Limit: limit, ProjectRoot: projectRoot}, 1)
}

// LatestUnassignedRuns returns active runs without a project identity.
func (s *Store) LatestUnassignedRuns(limit int) ([]RunRecord, error) {
	return s.latestRunsScoped(Window{Limit: limit, Unassigned: true}, 0)
}

// LatestArchivedUnassignedRuns returns archived runs without a project identity.
func (s *Store) LatestArchivedUnassignedRuns(limit int) ([]RunRecord, error) {
	return s.latestRunsScoped(Window{Limit: limit, Unassigned: true}, 1)
}

// latestRunsScoped is the one place a runs query is constructed: archive mode,
// window scope, ordering and limit all come from the same call, so a listing and
// a count can never disagree about which runs were in scope.
func (s *Store) latestRunsScoped(w Window, archiveMode int) ([]RunRecord, error) {
	conditions, args := s.runScope(archiveMode, w)
	q := `SELECT ` + runColumns + `
	      FROM runs`
	if len(conditions) > 0 {
		q += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	q += ` ORDER BY started_at DESC, id DESC`
	if w.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, w.Limit)
	}
	return s.scanRuns(q, args)
}

// countRunsScoped counts the runs a window selects without loading them. It
// shares runScope with latestRunsScoped, which is what makes a count and the
// list under it the same set: "598 runs have no project identity" and the rows
// that follow cannot disagree about which runs those are.
func (s *Store) countRunsScoped(w Window, archiveMode int) (int, error) {
	conditions, args := s.runScope(archiveMode, w)
	q := `SELECT COUNT(*) FROM runs`
	if len(conditions) > 0 {
		q += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	var n int
	err := s.db.QueryRow(q, args...).Scan(&n)
	return n, err
}

// UnassignedRunCount is how much of the run history has no project identity:
// the number that decides whether a per-project picture is trustworthy at all.
// A UI that shows only a total leaves the reader unable to tell "nothing is
// attributed" from "everything is".
func (s *Store) UnassignedRunCount(archiveMode int) (int, error) {
	return s.countRunsScoped(Window{Unassigned: true}, archiveMode)
}

// scanRuns executes one runs query and scans the rows. The column list is the
// one place a new provenance column is added, so every runs query stays in step.
func (s *Store) scanRuns(q string, args []any) ([]RunRecord, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunRecord
	for rows.Next() {
		var r RunRecord
		var archivedValue int
		if err := rows.Scan(&r.ID, &r.StartedAt, &r.DurationMs, &r.ToolVersion, &r.GitCommit,
			&r.Files, &r.ErrorCount, &r.WarningCount, &r.InfoCount, &r.ProjectRoot,
			&r.PolicyDigest, &r.TraceID, &r.Invocation, &archivedValue); err != nil {
			return nil, err
		}
		r.Archived = archivedValue != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// runScope turns an archive mode plus a window into one set of WHERE conditions
// and the argument list that goes with them, in a fixed order. Both the run
// listing and the rule-count subquery build their scope here, which is why the
// two cannot drift apart: drift looks like a series that stops in March next
// to a top-rules list spanning every run in the database.
//
// archiveMode: 0 active, 1 archived, 2 any.
func (s *Store) runScope(archiveMode int, w Window) ([]string, []any) {
	conditions := []string{}
	args := []any{}
	if archiveMode < 2 {
		conditions = append(conditions, fmt.Sprintf("archived = %d", archiveMode))
	}
	windowClause, windowArgs := w.runScope(archiveMode)
	if windowClause != "" {
		conditions = append(conditions, windowClause)
	}
	args = append(args, windowArgs...)
	return conditions, args
}

// RunFindings returns every finding recorded under runID, in insertion
// order (which is the order rules produced them in the original run).
func (s *Store) RunFindings(runID int64) ([]model.Finding, error) {
	rows, err := s.db.Query(`SELECT rule_id, severity, source, file, start_line, start_column,
	                                end_line, end_column, message
	                         FROM findings WHERE run_id = ? ORDER BY rowid`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Finding
	for rows.Next() {
		var f model.Finding
		var sev, src string
		if err := rows.Scan(&f.RuleID, &sev, &src, &f.File,
			&f.StartLine, &f.StartCol, &f.EndLine, &f.EndCol, &f.Message); err != nil {
			return nil, err
		}
		f.Severity = model.Severity(sev)
		f.Source = model.Source(src)
		out = append(out, f)
	}
	return out, rows.Err()
}

// SeverityTrend returns the per-run error/warning/info series covering the
// most recent limit runs (oldest first, so the output reads as a chart).
// limit <= 0 means "all runs".
func (s *Store) SeverityTrend(limit int) (SeverityTrend, error) {
	return s.SeverityTrendIn(Window{Limit: limit})
}

// SeverityTrendIn is SeverityTrend scoped by a Window: a date lower bound, a
// project root, or both. A window with only Limit set is exactly SeverityTrend,
// so the two cannot drift.
//
// The filter is applied to the INNER query, before the LIMIT: "the last 20 runs
// " applies to the runs the window admits, not to the whole table. Filtering
// outside instead would take the 20 most recent runs of every project and then
// hide the ones the window excludes — a series that silently ends at the wrong
// end.
func (s *Store) SeverityTrendIn(w Window) (SeverityTrend, error) {
	where, filterArgs := w.runFilter()
	innerLimit := ""
	if w.Limit > 0 {
		innerLimit = ` LIMIT ?`
	}
	// Limit is applied after the window, so the argument order is: the
	// window's own bind values, then the limit.
	args := append(append([]any{}, filterArgs...), w.Limit)
	if innerLimit == "" {
		args = filterArgs
	}
	q := `SELECT started_at, error_count, warning_count, info_count FROM (
	          SELECT id, started_at, error_count, warning_count, info_count
	          FROM runs` + where + ` ORDER BY started_at DESC, id DESC` + innerLimit + `
	      ) ORDER BY started_at ASC, id ASC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return SeverityTrend{}, err
	}
	defer rows.Close()
	var t SeverityTrend
	for rows.Next() {
		var p TrendPoint
		if err := rows.Scan(&p.StartedAt, &p.Errors, &p.Warnings, &p.Infos); err != nil {
			return SeverityTrend{}, err
		}
		t.Series = append(t.Series, p)
	}
	if err := rows.Err(); err != nil {
		return SeverityTrend{}, err
	}
	t.Runs = len(t.Series)
	return t, nil
}

// RuleFrequency aggregates finding occurrences by rule_id across the most
// recent limit runs. Rules are returned in descending count order, breaking
// ties alphabetically on rule id for stable output.
func (s *Store) RuleFrequency(limit int) ([]RuleCount, error) {
	return s.RuleFrequencyIn(Window{Limit: limit})
}

// RuleFrequencyIn is RuleFrequency scoped by a Window.
//
// The scope lives in the innermost subquery, for the same reason as
// SeverityTrendIn: the limit selects runs AFTER the window has filtered them.
//
// The always-on-wrapper clause is built the same way in both functions, so a
// limit of 0 ("every run") and a limit of N produce identical SQL shapes here
// and in SeverityTrendIn; the window bind values come first, then the limit.
func (s *Store) RuleFrequencyIn(w Window) ([]RuleCount, error) {
	where, filterArgs := w.runFilter()
	inner := `SELECT id FROM runs` + where
	args := append([]any{}, filterArgs...)
	if w.Limit > 0 {
		inner += ` ORDER BY started_at DESC, id DESC LIMIT ?`
		args = append(args, w.Limit)
	}
	q := `SELECT rule_id, COUNT(*) FROM findings WHERE run_id IN (` + inner + `)
	      GROUP BY rule_id ORDER BY COUNT(*) DESC, rule_id ASC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RuleCount
	for rows.Next() {
		var rc RuleCount
		if err := rows.Scan(&rc.RuleID, &rc.Count); err != nil {
			return nil, err
		}
		out = append(out, rc)
	}
	return out, rows.Err()
}
