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
	info_count INTEGER NOT NULL,
	project_root TEXT,
	archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
	policy_digest TEXT,
	trace_id TEXT
);
CREATE TABLE IF NOT EXISTS findings (
	run_id INTEGER NOT NULL REFERENCES runs(id),
	rule_id TEXT NOT NULL,
	snippet_hash TEXT,
	fingerprint TEXT,
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
CREATE TABLE IF NOT EXISTS trace_attrs (
	trace_id TEXT NOT NULL,
	key TEXT NOT NULL,
	value TEXT NOT NULL,
	PRIMARY KEY (trace_id, key)
);
CREATE INDEX IF NOT EXISTS idx_runs_started ON runs(started_at);
CREATE INDEX IF NOT EXISTS idx_findings_run ON findings(run_id);
CREATE INDEX IF NOT EXISTS idx_snapshots_run ON metric_snapshots(run_id);
CREATE INDEX IF NOT EXISTS idx_trace_attrs_kv ON trace_attrs(key, value);
CREATE TABLE IF NOT EXISTS issue_states (
	fingerprint TEXT PRIMARY KEY,
	status TEXT NOT NULL CHECK (status IN ('open', 'confirmed', 'false_positive', 'accepted')),
	owner TEXT,
	reason TEXT,
	expires TEXT,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_issue_states_status ON issue_states(status);
CREATE TABLE IF NOT EXISTS store_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
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
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate store: %w", err)
	}
	return &Store{db: db}, nil
}

// migrate brings a database written by an earlier version up to the current
// schema. Every step is additive and idempotent, so opening an already-current
// store is a no-op rather than a rewrite.
//
// It is separate from Open because Open's job is opening, and because these
// steps have an order that is not obvious from the call sites: the columns are
// added before anything indexes or backfills them, and among the two findings
// columns snippet_hash must come first. The comment on that pair is the reason
// the order is load-bearing, not stylistic.
func migrate(db *sql.DB) error {
	steps := []func(*sql.DB) error{
		migrateRunsProjectRoot,
		migrateRunsArchived,
		migrateRunsPolicyDigest,
		migrateRunsTraceID,
		// snippet_hash must be migrated before fingerprint:
		// migrateFindingsFingerprint backfills each row by re-reading it
		// (findingForBackfill selects snippet_hash), so a legacy database that
		// lacks snippet_hash fails inside the fingerprint migration itself. The
		// issue list has the same dependency — it groups by fingerprint and
		// selects MAX(snippet_hash) — which is why the column cannot be optional.
		migrateFindingsSnippetHash,
		migrateFindingsFingerprint,
		// The covering index is created after the columns it names exist, rather
		// than in the schema literal: creating it with the base schema fails on a
		// store written before fingerprint existed ("no such column: fingerprint").
		createFindingsGroupIndex,
		// store_meta carries rows about the database rather than about a run, so
		// it has no migration of its own to run before it.
		migrateStoreMeta,
	}
	for _, step := range steps {
		if err := step(db); err != nil {
			return err
		}
	}
	return nil
}

// createFindingsGroupIndex declares the covering index the issues query walks to
// produce de-duplicated groups in fingerprint order.
//
// It is load-bearing rather than an optimisation: without it the planner walks
// idx_findings_run and materialises every group before the outer LIMIT can stop
// it. Measured on the real 2.4GB database, unassigned 60-run window: 0.38s with
// this index, 10.75s without it. It existed in hand-built databases but was
// never declared here, so a fresh clone planned the slow way; declaring it is
// what makes the fast plan reproducible rather than accidental.
//
// CREATE INDEX on a 1.2M-row table takes about 20s the first time and is a
// no-op afterwards, so it is paid once per database rather than per run.
func createFindingsGroupIndex(db *sql.DB) error {
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_findings_group ON findings(
		fingerprint, rule_id, severity, source, file, start_line, start_column,
		end_line, end_column, message, run_id, snippet_hash)`)
	return err
}

// migrateStoreMeta adds the key/value table that carries facts about the
// database itself, starting with the last retention pass (meta.go). It exists
// for databases written before this table was in the schema literal: a legacy
// store has every other table and no place to put the record.
func migrateStoreMeta(db *sql.DB) error {
	if hasTable(db, "store_meta") {
		return nil
	}
	_, err := db.Exec(`CREATE TABLE store_meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`)
	return err
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
	// Attrs are the whitelisted trace attributes (model, prompt digest, …)
	// attached to TraceID. They are stored in their own table keyed by trace
	// id, not on the run row: the attributes describe the unit of work, not
	// this particular run, so two runs under one trace share them.
	Attrs []Attr
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

	if err := saveRunChildren(tx, runID, summary, meta); err != nil {
		return 0, err
	}
	return runID, tx.Commit()
}

// saveRunChildren writes the rows that hang off one run: its findings, its
// metric snapshots and the trace attributes. It is split out of saveRun so the
// run row's own insert reads as one statement, and so the three per-row loops
// do not each count against saveRun's complexity.
func saveRunChildren(tx *sql.Tx, runID int64, summary *model.RunSummary, meta RunMeta) error {
	for _, f := range summary.Findings {
		if _, err := tx.Exec(`INSERT INTO findings
			(run_id, rule_id, severity, source, file, start_line, start_column, end_line, end_column, message, trace_id, snippet_hash, fingerprint)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			runID, f.RuleID, string(f.Severity), string(f.Source), f.File,
			f.StartLine, f.StartCol, f.EndLine, f.EndCol, f.Message,
			nullableText(meta.TraceID), nullableText(snippetHashOf(f)), Fingerprint(f)); err != nil {
			return err
		}
	}
	for _, m := range summary.Metrics {
		if _, err := tx.Exec(`INSERT INTO metric_snapshots
			(run_id, file, scope, metric, value, warn_threshold, err_threshold, trace_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			runID, m.File, m.Scope, m.Metric, m.Value, m.Warn, m.Err, nullableText(meta.TraceID)); err != nil {
			return err
		}
	}
	// Trace attributes describe the unit of work, so they are only meaningful
	// with a trace id. Writing them without one would create rows no report
	// can ever join back to a run.
	if meta.TraceID != "" {
		if err := saveTraceAttrs(tx, meta.TraceID, meta.Attrs); err != nil {
			return err
		}
	}
	return nil
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

// migrateFindingsSnippetHash and migrateFindingsFingerprint add the two
// finding columns the issue key and its anchor depend on. Both are nullable and
// empty for rows written before they existed, which is the honest migration: a
// pre-column row cannot be given a digest it never recorded, so those rows keep
// the weaker line-scoped key rather than being silently re-keyed. Each runs on
// every Open but is a no-op after the first, and each adds the column whether
// the table came from this schema or from a legacy database's own.
func migrateFindingsSnippetHash(db *sql.DB) error {
	if hasColumn(db, "findings", "snippet_hash") {
		return nil
	}
	_, err := db.Exec(`ALTER TABLE findings ADD COLUMN snippet_hash TEXT`)
	return err
}

func migrateFindingsFingerprint(db *sql.DB) error {
	if !hasColumn(db, "findings", "fingerprint") {
		_, err := db.Exec(`ALTER TABLE findings ADD COLUMN fingerprint TEXT`)
		if err != nil {
			return err
		}
	}
	// rowid, not id: the findings table has no explicit primary key, so its
	// stable storage key is SQLite's own rowid.
	rows, err := db.Query(`SELECT rowid FROM findings
	                       WHERE fingerprint IS NULL OR fingerprint = ''
	                       ORDER BY rowid LIMIT 20000`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		f, ok, err := findingForBackfill(db, id)
		if err != nil || !ok {
			continue
		}
		if _, err := db.Exec(`UPDATE findings SET fingerprint = ? WHERE rowid = ?`,
			Fingerprint(f), id); err != nil {
			return err
		}
	}
	return nil
}

// hasTable reports whether the schema carries a table, so a migration against a
// legacy database that never had one is a no-op rather than a failure.
func hasTable(db *sql.DB, name string) bool {
	var found string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&found)
	return err == nil
}

// hasColumn reports whether table carries column, so each migration runs once
// and its check lives next to it.
func hasColumn(db *sql.DB, table, column string) bool {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

// findingForBackfill reads the one row needed to recompute its issue key.
func findingForBackfill(db *sql.DB, id int64) (model.Finding, bool, error) {
	var f model.Finding
	var snippet sql.NullString
	err := db.QueryRow(`SELECT rule_id, file, start_line, snippet_hash FROM findings WHERE rowid = ?`, id).
		Scan(&f.RuleID, &f.File, &f.StartLine, &snippet)
	if err == sql.ErrNoRows {
		return f, false, nil
	}
	if err != nil {
		return f, false, err
	}
	if snippet.String != "" {
		f.Evidence = &model.Evidence{Anchor: model.AnchorExact, SnippetHash: snippet.String}
	}
	return f, true, nil
}

// snippetHashOf returns the finding's byte-span digest, or "" when the finding
// carries no exact anchor. Only exact anchors get a digest: hashing nothing and
// calling it exact is how a triage key starts lying.
func snippetHashOf(f model.Finding) string {
	if f.Evidence == nil || f.Evidence.SnippetHash == "" {
		return ""
	}
	return f.Evidence.SnippetHash
}
