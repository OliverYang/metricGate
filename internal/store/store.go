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
	for _, root := range roots {
		if root == want {
			return root, nil
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
			return root, nil
		}
	}
	for _, root := range roots {
		if strings.HasSuffix(filepath.ToSlash(root), "/"+filepath.ToSlash(want)) {
			return root, nil
		}
	}
	if len(roots) == 0 {
		return "", fmt.Errorf("no project is recorded in this database yet — run `metricgate check` first")
	}
	return "", fmt.Errorf("no recorded project matches %q; recorded project root(s): %s", want, strings.Join(roots, ", "))
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
	q := `SELECT id, started_at, duration_ms, tool_version, COALESCE(git_commit, ''),
	             files, error_count, warning_count, info_count, COALESCE(project_root, ''),
	             COALESCE(policy_digest, ''), COALESCE(trace_id, ''), archived
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
			&r.PolicyDigest, &r.TraceID, &archivedValue); err != nil {
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
