// Package store persists run summaries into a local SQLite database (pure
// Go driver, no CGO) so quality trends can be tracked across runs. The v0
// contract is write-only: reports and dashboards read it in v1.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
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
	info_count INTEGER NOT NULL
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
	message TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS metric_snapshots (
	run_id INTEGER NOT NULL REFERENCES runs(id),
	file TEXT NOT NULL,
	scope TEXT NOT NULL,
	metric TEXT NOT NULL,
	value INTEGER NOT NULL,
	warn_threshold INTEGER NOT NULL,
	err_threshold INTEGER NOT NULL
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
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// SaveRun writes one run with its findings and metric snapshots, returning
// the run id.
func (s *Store) SaveRun(summary *model.RunSummary, toolVersion, commit string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO runs
		(started_at, duration_ms, tool_version, git_commit, files, error_count, warning_count, info_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339), summary.Duration.Milliseconds(),
		toolVersion, commit, summary.Files,
		summary.Count(model.SevError), summary.Count(model.SevWarning), summary.Count(model.SevInfo))
	if err != nil {
		return 0, err
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	for _, f := range summary.Findings {
		if _, err := tx.Exec(`INSERT INTO findings
			(run_id, rule_id, severity, source, file, start_line, start_column, end_line, end_column, message)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			runID, f.RuleID, string(f.Severity), string(f.Source), f.File,
			f.StartLine, f.StartCol, f.EndLine, f.EndCol, f.Message); err != nil {
			return 0, err
		}
	}
	for _, m := range summary.Metrics {
		if _, err := tx.Exec(`INSERT INTO metric_snapshots
			(run_id, file, scope, metric, value, warn_threshold, err_threshold)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			runID, m.File, m.Scope, m.Metric, m.Value, m.Warn, m.Err); err != nil {
			return 0, err
		}
	}
	return runID, tx.Commit()
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
	ErrorCount   int
	WarningCount int
	InfoCount    int
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

// LatestRuns returns the most recent runs, newest first. limit <= 0 means
// "every run" (still capped at the database size; this is for tests and the
// `metricgate trends` default window of 20).
func (s *Store) LatestRuns(limit int) ([]RunRecord, error) {
	q := `SELECT id, started_at, duration_ms, tool_version, COALESCE(git_commit, ''),
	             files, error_count, warning_count, info_count
	      FROM runs ORDER BY started_at DESC, id DESC`
	if limit > 0 {
		q += ` LIMIT ?`
	}
	args := []any{}
	if limit > 0 {
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
		if err := rows.Scan(&r.ID, &r.StartedAt, &r.DurationMs, &r.ToolVersion, &r.GitCommit,
			&r.Files, &r.ErrorCount, &r.WarningCount, &r.InfoCount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RunFindings returns every finding recorded under runID, in insertion
// order (which is the order rules produced them in the original run).
func (s *Store) RunFindings(runID int64) ([]model.Finding, error) {
	rows, err := s.db.Query(`SELECT rule_id, severity, source, file, start_line, start_column,
	                                end_line, end_column, message
	                         FROM findings WHERE run_id = ? ORDER BY id`, runID)
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
