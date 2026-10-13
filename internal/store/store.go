// Package store persists run summaries into a local SQLite database (pure
// Go driver, no CGO) so quality trends can be tracked across runs. The v0
// contract is write-only: reports and dashboards read it in v1.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"metricgate/internal/model"
)

// Store is a handle to the trend database.
type Store struct {
	db *sql.DB
}

// schema is applied on every Open; it is additive-only by design so older
// databases keep working as the tool evolves.
const schema = `
CREATE TABLE IF NOT EXISTS runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	started_at TEXT NOT NULL,
	duration_ms INTEGER NOT NULL,
	tool_version TEXT NOT NULL,
	git_commit TEXT,
	files INTEGER NOT NULL,
	error_count INTEGER NOT NULL,
	warning_count INTEGER NOT NULL,
	info_count INTEGER NOT NULL,
	project_root TEXT,
	archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
	policy_digest TEXT,
	trace_id TEXT
);
CREATE TABLE IF NOT EXISTS findings (
	run_id INTEGER NOT NULL REFERENCES runs(id),
	rule_id TEXT NOT NULL,
	severity TEXT NOT NULL,
	source TEXT NOT NULL,
	file TEXT NOT NULL,
	start_line INTEGER NOT NULL,
	start_column INTEGER NOT NULL,
	end_line INTEGER NOT NULL,
	end_column INTEGER NOT NULL,
	message TEXT NOT NULL,
	trace_id TEXT
);
CREATE TABLE IF NOT EXISTS metric_snapshots (
	run_id INTEGER NOT NULL REFERENCES runs(id),
	file TEXT NOT NULL,
	scope TEXT NOT NULL,
	metric TEXT NOT NULL,
	value INTEGER NOT NULL,
	warn_threshold INTEGER NOT NULL,
	err_threshold INTEGER NOT NULL,
	trace_id TEXT
);
CREATE INDEX IF NOT EXISTS idx_runs_started ON runs(started_at);
CREATE INDEX IF NOT EXISTS idx_findings_run ON findings(run_id);
CREATE INDEX IF NOT EXISTS idx_snapshots_run ON metric_snapshots(run_id);
`

// Open creates the database file (and parent directory) and applies the
// schema.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create store dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrateRunsProjectRoot(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate store: %w", err)
	}
	if err := migrateRunsArchived(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate store: %w", err)
	}
	if err := migrateRunsPolicyDigest(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate store: %w", err)
	}
	if err := migrateRunsTraceID(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate store: %w", err)
	}
	return &Store{db: db}, nil
}

// migrateRunsPolicyDigest adds the nullable policy fingerprint column. Older
// databases keep working: a row written before this column exists simply
// reports an empty digest, which is what "we do not know" looks like.
func migrateRunsPolicyDigest(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(runs)`)
	if err != nil {
		return err
	}
	hasPolicyDigest := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == "policy_digest" {
			hasPolicyDigest = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if hasPolicyDigest {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE runs ADD COLUMN policy_digest TEXT`)
	return err
}

// migrateRunsTraceID adds the nullable trace column shared by runs, findings
// and metric_snapshots, so a run can later be tied back to the LLM session,
// PR or task that produced it. An absent trace is a legal state: the column is
// simply empty and every query treats it as "unknown".
func migrateRunsTraceID(db *sql.DB) error {
	for _, table := range []string{"runs", "findings", "metric_snapshots"} {
		rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
		if err != nil {
			return err
		}
		hasTrace := false
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				rows.Close()
				return err
			}
			if name == "trace_id" {
				hasTrace = true
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if hasTrace {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN trace_id TEXT`); err != nil {
			return err
		}
	}
	return nil
}

func migrateRunsArchived(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(runs)`)
	if err != nil {
		return err
	}
	hasArchived := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == "archived" {
			hasArchived = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if hasArchived {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE runs ADD COLUMN archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1))`)
	return err
}

func migrateRunsProjectRoot(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(runs)`)
	if err != nil {
		return err
	}
	hasProjectRoot := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == "project_root" {
			hasProjectRoot = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if hasProjectRoot {
		return nil
	}
	_, err = db.Exec(`ALTER TABLE runs ADD COLUMN project_root TEXT`)
	return err
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// SaveRun writes one run with its findings and metric snapshots, returning
// the run id. Runs saved through this legacy entry point remain unassigned.
func (s *Store) SaveRun(summary *model.RunSummary, toolVersion, commit string) (int64, error) {
	return s.saveRun(summary, toolVersion, commit, "", RunMeta{})
}

// SaveRunForProject writes a run associated with a verified Git worktree root.
func (s *Store) SaveRunForProject(summary *model.RunSummary, toolVersion, commit, projectRoot string) (int64, error) {
	return s.saveRun(summary, toolVersion, commit, projectRoot, RunMeta{})
}

// SaveRunFull writes a run with every piece of provenance the gate knows: the
// verified project root, the policy fingerprint and the trace id. Callers that
// have no trace pass RunMeta{} and get exactly the old behaviour.
func (s *Store) SaveRunFull(summary *model.RunSummary, toolVersion, commit, projectRoot string, meta RunMeta) (int64, error) {
	return s.saveRun(summary, toolVersion, commit, projectRoot, meta)
}

func (s *Store) SetArchived(runID int64, projectRoot string, archived bool) error {
	return s.SetRunsArchived([]int64{runID}, projectRoot, archived)
}

// SetRunsArchived atomically changes archive state for runs in one project scope.
func (s *Store) SetRunsArchived(runIDs []int64, projectRoot string, archived bool) error {
	if len(runIDs) == 0 || len(runIDs) > 100 {
		return fmt.Errorf("run selection must contain between 1 and 100 IDs")
	}
	if projectRoot != "" && !filepath.IsAbs(projectRoot) {
		return fmt.Errorf("project root must be absolute")
	}
	unique := make(map[int64]struct{}, len(runIDs))
	for _, id := range runIDs {
		if id <= 0 {
			return fmt.Errorf("run IDs must be positive")
		}
		if _, exists := unique[id]; exists {
			return fmt.Errorf("run IDs must be unique")
		}
		unique[id] = struct{}{}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current := boolToInt(!archived)
	for _, runID := range runIDs {
		query := `UPDATE runs SET archived = ? WHERE id = ? AND archived = ?`
		args := []any{boolToInt(archived), runID, current}
		if projectRoot == "" {
			query += ` AND (project_root IS NULL OR project_root = '')`
		} else {
			query += ` AND project_root = ?`
			args = append(args, projectRoot)
		}
		result, err := tx.Exec(query, args...)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return fmt.Errorf("run not found in requested scope or archive state already changed")
		}
	}
	return tx.Commit()
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// RunMeta carries the optional provenance attached to one stored run: the
// policy fingerprint (which version of the rules produced this verdict) and
// the trace id (which session/PR/task produced the run). Both are optional and
// both are plain strings, so an absent one is simply empty.
type RunMeta struct {
	PolicyDigest string
	TraceID      string
}

// nullableText maps an empty optional string to SQL NULL.
func nullableText(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (s *Store) saveRun(summary *model.RunSummary, toolVersion, commit, projectRoot string, meta RunMeta) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO runs
		(started_at, duration_ms, tool_version, git_commit, files, error_count, warning_count, info_count, project_root, policy_digest, trace_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339), summary.Duration.Milliseconds(),
		toolVersion, commit, summary.Files,
		summary.Count(model.SevError), summary.Count(model.SevWarning), summary.Count(model.SevInfo),
		nullableProjectRoot(projectRoot), nullableText(meta.PolicyDigest), nullableText(meta.TraceID))
	if err != nil {
		return 0, err
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	for _, f := range summary.Findings {
		if _, err := tx.Exec(`INSERT INTO findings
			(run_id, rule_id, severity, source, file, start_line, start_column, end_line, end_column, message, trace_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			runID, f.RuleID, string(f.Severity), string(f.Source), f.File,
			f.StartLine, f.StartCol, f.EndLine, f.EndCol, f.Message, nullableText(meta.TraceID)); err != nil {
			return 0, err
		}
	}
	for _, m := range summary.Metrics {
		if _, err := tx.Exec(`INSERT INTO metric_snapshots
			(run_id, file, scope, metric, value, warn_threshold, err_threshold, trace_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			runID, m.File, m.Scope, m.Metric, m.Value, m.Warn, m.Err, nullableText(meta.TraceID)); err != nil {
			return 0, err
		}
	}
	return runID, tx.Commit()
}

func nullableProjectRoot(root string) any {
	if root == "" {
		return nil
	}
	return root
}

// Runs returns the number of stored runs; used by tests and diagnostics.
func (s *Store) Runs() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&n)
	return n, err
}

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
}

// Report folds the most recent limit runs (limit <= 0 means every run) into one
// report. A store with no runs yields a zero report, not an error; the series
// and rule lists are always non-nil so the JSON reads "[]" rather than "null".
func (s *Store) Report(limit int) (TrendReport, error) {
	records, err := s.LatestRuns(limit)
	if err != nil {
		return TrendReport{}, err
	}
	trend, err := s.SeverityTrend(limit)
	if err != nil {
		return TrendReport{}, err
	}
	rules, err := s.RuleFrequency(limit)
	if err != nil {
		return TrendReport{}, err
	}
	out := TrendReport{Runs: len(records), Series: trend.Series, TopRules: rules}
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

func (s *Store) ReportForProject(limit int, projectRoot string) (TrendReport, error) {
	return s.reportForScope(limit, projectRoot, false)
}

// ReportArchivedForProject folds archived runs for one project into a report.
func (s *Store) ReportArchivedForProject(limit int, projectRoot string) (TrendReport, error) {
	return s.reportForScope(limit, projectRoot, true)
}

func (s *Store) ReportUnassigned(limit int) (TrendReport, error) {
	return s.reportForScope(limit, "", false)
}

func (s *Store) ReportArchivedUnassigned(limit int) (TrendReport, error) {
	return s.reportForScope(limit, "", true)
}

func (s *Store) reportForScope(limit int, projectRoot string, archived bool) (TrendReport, error) {
	archiveMode := 0
	if archived {
		archiveMode = 1
	}
	records, err := s.latestRunsState(limit, projectRoot, true, archiveMode)
	if err != nil {
		return TrendReport{}, err
	}
	if projectRoot == "" {
		return s.reportForRecords(records, fmt.Sprintf("archived = %d AND (project_root IS NULL OR project_root = '')", archiveMode), nil, limit)
	}
	return s.reportForRecords(records, fmt.Sprintf("archived = %d AND project_root = ?", archiveMode), []any{projectRoot}, limit)
}

func (s *Store) reportForRecords(records []RunRecord, projectClause string, projectArgs []any, limit int) (TrendReport, error) {
	out := TrendReport{Runs: len(records), Series: make([]TrendPoint, 0, len(records)), TopRules: []RuleCount{}}
	for i := len(records) - 1; i >= 0; i-- {
		run := records[i]
		out.Series = append(out.Series, TrendPoint{StartedAt: run.StartedAt, Errors: run.ErrorCount, Warnings: run.WarningCount, Infos: run.InfoCount})
	}
	if len(records) > 0 {
		out.Range = &TrendRange{Start: records[len(records)-1].StartedAt, End: records[0].StartedAt}
	}
	query := `SELECT rule_id, COUNT(*) FROM findings WHERE run_id IN (
		SELECT id FROM runs WHERE ` + projectClause + ` ORDER BY started_at DESC, id DESC`
	args := append([]any{}, projectArgs...)
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	query += `) GROUP BY rule_id ORDER BY COUNT(*) DESC, rule_id ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return TrendReport{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var rule RuleCount
		if err := rows.Scan(&rule.RuleID, &rule.Count); err != nil {
			return TrendReport{}, err
		}
		out.TopRules = append(out.TopRules, rule)
	}
	return out, rows.Err()
}

// ProjectRoots returns all non-empty project roots in the run history.
func (s *Store) ProjectRoots() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT project_root FROM runs
		WHERE project_root IS NOT NULL AND project_root != '' ORDER BY project_root`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roots []string
	for rows.Next() {
		var root string
		if err := rows.Scan(&root); err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	return roots, rows.Err()
}

// LatestRuns returns the most recent runs, newest first. limit <= 0 means
// "every run" (still capped at the database size; this is for tests and the
// `metricgate trends` default window of 20).
func (s *Store) LatestRuns(limit int) ([]RunRecord, error) {
	return s.latestRunsState(limit, "", false, 2)
}

// LatestRunsForProject returns recent runs for exactly one verified project root.
func (s *Store) LatestRunsForProject(limit int, projectRoot string) ([]RunRecord, error) {
	return s.latestRunsState(limit, projectRoot, true, 0)
}

// LatestArchivedRunsForProject returns archived runs for exactly one project.
func (s *Store) LatestArchivedRunsForProject(limit int, projectRoot string) ([]RunRecord, error) {
	return s.latestRunsState(limit, projectRoot, true, 1)
}

// LatestUnassignedRuns returns active runs without a project identity.
func (s *Store) LatestUnassignedRuns(limit int) ([]RunRecord, error) {
	return s.latestRunsState(limit, "", true, 0)
}

// LatestArchivedUnassignedRuns returns archived runs without a project identity.
func (s *Store) LatestArchivedUnassignedRuns(limit int) ([]RunRecord, error) {
	return s.latestRunsState(limit, "", true, 1)
}

func (s *Store) latestRuns(limit int, projectRoot string, filter bool) ([]RunRecord, error) {
	return s.latestRunsState(limit, projectRoot, filter, 0)
}

// archiveMode: 0 active, 1 archived, 2 any.
func (s *Store) latestRunsState(limit int, projectRoot string, filter bool, archiveMode int) ([]RunRecord, error) {
	q := `SELECT id, started_at, duration_ms, tool_version, COALESCE(git_commit, ''),
	             files, error_count, warning_count, info_count, COALESCE(project_root, ''),
	             COALESCE(policy_digest, ''), COALESCE(trace_id, ''), archived
	      FROM runs`
	args := []any{}
	conditions := []string{}
	if archiveMode < 2 {
		conditions = append(conditions, fmt.Sprintf("archived = %d", archiveMode))
	}
	if filter {
		if projectRoot == "" {
			conditions = append(conditions, `(project_root IS NULL OR project_root = '')`)
		} else {
			conditions = append(conditions, `project_root = ?`)
			args = append(args, projectRoot)
		}
	}
	if len(conditions) > 0 {
		q += ` WHERE ` + strings.Join(conditions, ` AND `)
	}
	q += ` ORDER BY started_at DESC, id DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
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
			&r.PolicyDigest, &r.TraceID, &archivedValue); err != nil {
			return nil, err
		}
		r.Archived = archivedValue != 0
		out = append(out, r)
	}
	return out, rows.Err()
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
	innerLimit := ""
	args := []any{}
	if limit > 0 {
		innerLimit = ` LIMIT ?`
		args = append(args, limit)
	}
	q := `SELECT started_at, error_count, warning_count, info_count FROM (
	          SELECT id, started_at, error_count, warning_count, info_count
	          FROM runs ORDER BY started_at DESC, id DESC` + innerLimit + `
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
	runArg := ""
	args := []any{}
	if limit > 0 {
		runArg = ` WHERE run_id IN (
		    SELECT id FROM runs ORDER BY started_at DESC, id DESC LIMIT ?
		)`
		args = append(args, limit)
	}
	q := `SELECT rule_id, COUNT(*) FROM findings` + runArg + `
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
