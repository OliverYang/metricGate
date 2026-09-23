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
