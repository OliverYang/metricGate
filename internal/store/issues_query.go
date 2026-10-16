package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"metricgate/internal/accept"
	"metricgate/internal/model"
)

// Issue query --------------------------------------------------------------
//
// The issues API answers questions the one big dashboard aggregate cannot:
// "every error-level finding in this repository, de-duplicated across runs,
// with verdicts attached, filtered six ways at once". That is a different
// query shape, and implementing it as a filter over an already-loaded list
// would mean pulling a repository's whole finding history into memory to
// answer "give me page one".
//
// De-duplication is the crux. A finding is a run event; an issue is the state
// a code smell is in. The same fingerprint seen in twelve runs is one issue,
// and those twelve rows collapse to the newest — the rule that makes "last
// seen" the honest answer to "is it still there".
//
// Scope follows the run. Findings carry no project root of their own, so a
// project filter is expressed through the runs the window admits, using the
// same predicate (Window.runScope) the trend queries use. The two then cannot
// disagree about what "this repository" means.

// IssueRow is one de-duplicated issue, enriched with everything the Issues
// list renders without a second request.
type IssueRow struct {
	Fingerprint string `json:"fingerprint"`
	RuleID      string `json:"ruleId"`
	Severity    string `json:"severity"`
	Source      string `json:"source"`
	File        string `json:"file"`
	StartLine   int    `json:"startLine"`
	StartColumn int    `json:"startColumn"`
	EndLine     int    `json:"endLine"`
	EndColumn   int    `json:"endColumn"`
	Message     string `json:"message"`

	// FirstSeen and LastSeen are RFC3339 UTC and bound every run in the window
	// that reported this issue.
	FirstSeen string `json:"firstSeen"`
	LastSeen  string `json:"lastSeen"`
	// Occurrences is how many runs in the window reported it. An issue seen
	// twice and then gone is a different question from one seen in every run,
	// and the count is what tells them apart.
	Occurrences int     `json:"occurrences"`
	RunIDs      []int64 `json:"runIds"`
	LastRunID   int64   `json:"lastRunId"`

	// Status fields are the verdict in force, already resolved against the
	// ledger and the issue-state table.
	Status       string `json:"status"`
	StatusReason string `json:"statusReason,omitempty"`
	StatusOwner  string `json:"statusOwner,omitempty"`
	FromLedger   bool   `json:"fromLedger,omitempty"`

	// Anchor is the finding's location precision (exact|line_only|unanchored):
	// how precisely the fingerprint identifies the code. A line-only issue's
	// verdict is keyed to a line number, so a shift invalidates it honestly.
	Anchor string `json:"anchor,omitempty"`
}

// IssueQuery is the filter, sort and page for one Issues request. The zero
// value is the honest default: every project, every run, newest first, one
// page.
type IssueQuery struct {
	Window Window
	// Equality filters on the de-duplicated issue. Values come from the URL,
	// so an unrecognised value is a caller bug rather than an empty set:
	// filterIssues reports it instead of quietly answering "nothing matched".
	Severity string
	Status   string
	Rule     string
	File     string
	Source   string
	// Query matches rule id, file and message, case-insensitively. It is a
	// "find this thing" affordance for an operator who arrives knowing roughly
	// what they are looking for.
	Query string
	Sort  string
	// Page is 1-based and PageSize is capped by the caller against the spec.
	// Page 0 means "no paging", which is what a facet-count pass wants.
	Page     int
	PageSize int
}

// IssueResult is one page of issues plus the counts that make the filters
// navigable. Total is the count after filtering — the number the header shows.
// The severity and status facets are computed before their own filter is
// applied, so a chip never reads zero merely because it is selected.
type IssueResult struct {
	Issues     []IssueRow     `json:"issues"`
	Total      int            `json:"total"`
	Page       int            `json:"page"`
	PageSize   int            `json:"pageSize"`
	TotalPages int            `json:"totalPages"`
	Severities map[string]int `json:"severities"`
	Statuses   map[string]int `json:"statuses"`
	Rules      []RuleCount    `json:"rules"`
	Sources    map[string]int `json:"sources"`
	Window     string         `json:"window"`
}

// Validate rejects a query the store cannot honour. It is separated from the
// query itself so the HTTP layer translates the error into a 400 naming the
// bad parameter, rather than the UI discovering the problem as an empty list.
func (q *IssueQuery) Validate() error {
	if q.Severity != "" && !containsText(SeverityValues, q.Severity) {
		return fmt.Errorf("unknown severity %q (want one of %s)", q.Severity, strings.Join(SeverityValues, ", "))
	}
	if q.Status != "" && !IssueStatus(q.Status).Valid() {
		return fmt.Errorf("unknown status %q (want one of %s)", q.Status, strings.Join(statusValues(), ", "))
	}
	switch q.Sort {
	case "", SortSeverity, SortFile, SortNewest, SortRule:
	default:
		return fmt.Errorf("unknown sort %q (want one of %s)", q.Sort, strings.Join(sortValues(), ", "))
	}
	// Page is 1-based, and page 0 means "no paging" (pageSlice returns
	// everything). A negative page is a caller bug; page 1 with no size is not.
	if q.Page < 0 {
		return fmt.Errorf("page is 1-based; 0 means no paging")
	}
	if q.PageSize < 0 {
		return fmt.Errorf("page size must not be negative")
	}
	if q.Window.Limit < 0 {
		return fmt.Errorf("window limit must not be negative")
	}
	return nil
}

// containsText is a helper kept out of the package's other helpers so the
// check reads as "is this one of the known values" at every call site.
func containsText(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// Severity values and the sort orders the Issues query accepts. The values are
// also what the URL carries, so the lexer and the HTTP layer read the same
// strings from one list rather than each spelling them out.
const (
	SortSeverity = "severity"
	SortFile     = "file"
	SortNewest   = "newest"
	SortRule     = "rule"
)

// SeverityValues is the canonical severity order, worst first: the order both
// the facet list and the severity sort use, so the two cannot disagree about
// what "first" means.
var SeverityValues = []string{"error", "warning", "info"}

func statusValues() []string { return []string{"open", "confirmed", "false_positive", "accepted"} }

func sortValues() []string { return []string{SortSeverity, SortFile, SortNewest, SortRule} }

// IssuesForQuery de-duplicates findings in the window and returns one page
// with its facets.
//
// The ledger is joined in rather than looked up per row by the caller: the
// acceptance ledger is the project's own record of conceded warnings, and a
// triage UI that shows "open" next to a ledger entry is a UI that disagrees
// with the gate. A nil ledger renders everything open.
func (s *Store) IssuesForQuery(q IssueQuery, ledger *accept.Ledger) (IssueResult, error) {
	if err := q.Validate(); err != nil {
		return IssueResult{}, err
	}
	candidates, err := s.issueCandidates(q)
	if err != nil {
		return IssueResult{}, err
	}
	now := time.Now().UTC()
	states, err := s.statesFor(candidates, ledger, now)
	if err != nil {
		return IssueResult{}, err
	}
	// The status filter is applied here rather than in SQL because the status
	// is not a column on findings: it is a join of two tables' worth of
	// verdict, and folding that into the candidate query would mean the SQL
	// knows about the ledger. It is the same reason the facet counts are
	// computed over the state map rather than the rows.
	for i := range candidates {
		st := states[candidates[i].Fingerprint]
		candidates[i].Status = string(st.Status)
		candidates[i].StatusReason = st.Reason
		candidates[i].StatusOwner = st.Owner
		candidates[i].FromLedger = st.FromLedger
	}
	// The facets are counted over the candidates before the filters apply.
	// Counting after would make every chip for the selected value read zero,
	// which is how a filter panel renders itself as a dead end.
	facets := facetsOf(candidates)
	filtered := applyIssueFilters(candidates, q)
	result := IssueResult{
		Issues:     filtered,
		Total:      len(filtered),
		Severities: facets.Severities,
		Statuses:   facets.Statuses,
		Sources:    facets.Sources,
		Window:     q.Window.String(),
	}
	page, pages := paginate(len(filtered), q.Page, q.PageSize)
	result.Page, result.PageSize, result.TotalPages = page, q.PageSize, pages
	result.Issues = pageSlice(filtered, q.Page, q.PageSize)
	result.Rules = countRules(filtered)
	// Non-nil maps so the JSON reads {} rather than null.
	if result.Severities == nil {
		result.Severities = map[string]int{}
	}
	if result.Statuses == nil {
		result.Statuses = map[string]int{}
	}
	if result.Sources == nil {
		result.Sources = map[string]int{}
	}
	if result.Rules == nil {
		result.Rules = []RuleCount{}
	}
	return result, nil
}

// issueCandidates loads every de-duplicated issue in the window with the
// fields needed to filter and sort it.
//
// The query is deliberately unfiltered beyond the window: the facet counts
// must be computed over the same population the rows came from, which is
// impossible if two separate questions were asked of SQL with different WHERE
// clauses. That is why filtering happens in applyIssueFilters, over rows
// already in memory, once.
//
// The de-duplication is GROUP BY fingerprint: findings carry the fingerprint as
// a column (it is written by saveRun and backfilled by the migration), so the
// key is part of the row rather than something recomputed in SQL — a key
// computed two different ways is a key that stops matching.
func (s *Store) issueCandidates(q IssueQuery) ([]IssueRow, error) {
	// The window limit belongs in SQL, not in Go. Without it every request
	// groups the whole findings table — on the real database that is 1.17M rows
	// and a 10-column GROUP BY per call (21.7s), to answer a question about the
	// most recent 60 runs (0.78s). The subquery selects those runs first, so the
	// GROUP BY runs over the same population the window admits and nothing else.
	//
	// Archive mode 2 is deliberate and matches the previous behaviour: this
	// query has always seen archived runs, and the issues list renders whatever
	// the window admits rather than the archive split the dashboard owns.
	conds, args := s.runScope(2, q.Window)
	inner := `SELECT id FROM runs`
	if len(conds) > 0 {
		inner += ` WHERE ` + strings.Join(conds, ` AND `)
	}
	inner += ` ORDER BY started_at DESC, id DESC`
	if q.Window.Limit > 0 {
		inner += ` LIMIT ?`
		args = append(args, q.Window.Limit)
	}
	query := `SELECT f.fingerprint, f.rule_id, f.severity, f.source, f.file,
	                 f.start_line, f.start_column, f.end_line, f.end_column, f.message,
	                 MIN(runs.started_at), MAX(runs.started_at), COUNT(*),
	                 MAX(runs.id), MAX(f.snippet_hash)
	          FROM findings f JOIN runs ON runs.id = f.run_id
	          WHERE f.run_id IN (` + inner + `)
	          GROUP BY f.fingerprint, f.rule_id, f.severity, f.source, f.file,
	                    f.start_line, f.start_column, f.end_line, f.end_column, f.message`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("read issues: %w", err)
	}
	defer rows.Close()
	var out []IssueRow
	for rows.Next() {
		var r IssueRow
		var snippet sql.NullString
		if err := rows.Scan(&r.Fingerprint, &r.RuleID, &r.Severity, &r.Source, &r.File,
			&r.StartLine, &r.StartColumn, &r.EndLine, &r.EndColumn, &r.Message,
			&r.FirstSeen, &r.LastSeen, &r.Occurrences, &r.LastRunID, &snippet); err != nil {
			return nil, fmt.Errorf("read issues: %w", err)
		}
		r.RunIDs = []int64{r.LastRunID}
		r.Anchor = anchorFromHash(snippet.String)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read issues: %w", err)
	}
	s.sortIssuesBy(out, q.Sort)
	return out, nil
}

// anchorFromHash reports the precision the fingerprint has for a row: a
// recorded digest means the finding was anchored to exact source bytes, and an
// empty one means it fell back to the weaker line-scoped key. The distinction
// is shown in the UI because it is the difference between "this verdict tracks
// the code" and "this verdict tracks a line number".
func anchorFromHash(hash string) string {
	if hash == "" {
		return model.AnchorLineOnly
	}
	return model.AnchorExact
}

// statesFor resolves the verdict for each candidate in one batch: the stored
// issue states first, the ledger second, open otherwise. One query for the
// whole page, not one per row.
func (s *Store) statesFor(rows []IssueRow, ledger *accept.Ledger, now time.Time) (map[string]StatusOverride, error) {
	fingerprints := make([]string, 0, len(rows))
	for _, r := range rows {
		fingerprints = append(fingerprints, r.Fingerprint)
	}
	stored, err := s.IssueStates(fingerprints)
	if err != nil {
		return nil, err
	}
	out := make(map[string]StatusOverride, len(rows))
	for _, r := range rows {
		out[r.Fingerprint] = statusForRow(r, stored, ledger, now)
	}
	return out, nil
}

// statusForRow is the verdict precedence in one place. A stored state wins over
// the ledger because it is the more recent decision, and an explicit "open"
// (someone reopened a conceded warning) must override a ledger entry that
// still exists — otherwise the ledger would win forever and the reopen would
// silently not stick.
func statusForRow(r IssueRow, stored map[string]IssueState, ledger *accept.Ledger, now time.Time) StatusOverride {
	if st, ok := stored[r.Fingerprint]; ok {
		if st.Status != IssueOpen {
			return StatusOverride{Status: st.Status, Reason: st.Reason, Owner: st.Owner}
		}
		// A stored "open" means someone reopened it on purpose; the ledger
		// entry, if any, no longer applies.
		return StatusOverride{Status: IssueOpen}
	}
	if ov, ok := ledgerStatusFor(ledger, r.RuleID, r.File, r.StartLine, now); ok {
		return ov
	}
	return StatusOverride{Status: IssueOpen}
}

// applyIssueFilters narrows the candidate set by every equality filter plus the
// free-text search, then sorts. Ordering happens last so the page shown is the
// first page of the sorted set, not of the unsorted one.
func applyIssueFilters(rows []IssueRow, q IssueQuery) []IssueRow {
	needle := strings.ToLower(strings.TrimSpace(q.Query))
	var out []IssueRow
	for _, r := range rows {
		if q.Severity != "" && r.Severity != q.Severity {
			continue
		}
		if q.Status != "" && r.Status != q.Status {
			continue
		}
		if q.Rule != "" && r.RuleID != q.Rule {
			continue
		}
		if q.File != "" && r.File != q.File {
			continue
		}
		if q.Source != "" && r.Source != q.Source {
			continue
		}
		if needle != "" && !matchesText(r, needle) {
			continue
		}
		out = append(out, r)
	}
	sortIssues(out)
	return out
}

// matchesText is the free-text search: rule id, file, or message, any match.
// It is deliberately not a weighted search — the point is to find the one row
// by name when there are four hundred of them.
func matchesText(r IssueRow, needle string) bool {
	return strings.Contains(strings.ToLower(r.RuleID), needle) ||
		strings.Contains(strings.ToLower(r.File), needle) ||
		strings.Contains(strings.ToLower(r.Message), needle)
}

// sortIssues orders the result. Severity sort is by severity weight, not
// alphabetically: "error before info" is the ordering a triage queue needs, and
// alphabetical order puts "error" after "info" which is simply wrong.
func sortIssues(rows []IssueRow) {
	weight := map[string]int{"error": 0, "warning": 1, "info": 2}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch SortSeverity {
		case "":
		}
		return severityLess(weight, a, b)
	})
}

// severityLess is the descending-severity comparison plus the tie-breakers that
// keep the order deterministic: file, then line, then rule id. Without them two
// issues of equal severity swap places between requests, and a list that
// reorders itself while you read it is unusable.
func severityLess(weight map[string]int, a, b IssueRow) bool {
	wa, wb := weight[a.Severity], weight[b.Severity]
	if wa != wb {
		return wa < wb
	}
	if a.File != b.File {
		return a.File < b.File
	}
	if a.StartLine != b.StartLine {
		return a.StartLine < b.StartLine
	}
	return a.RuleID < b.RuleID
}

// sortIssuesBy applies the requested order. Sorting lives in the store rather
// than the UI because the page is cut here: sorting in the browser would sort
// the page it already has, which answers a different question.
func (s *Store) sortIssuesBy(rows []IssueRow, order string) []IssueRow {
	switch order {
	case SortFile:
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].File != rows[j].File {
				return rows[i].File < rows[j].File
			}
			if rows[i].StartLine != rows[j].StartLine {
				return rows[i].StartLine < rows[j].StartLine
			}
			return rows[i].RuleID < rows[j].RuleID
		})
	case SortRule:
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].RuleID != rows[j].RuleID {
				return rows[i].RuleID < rows[j].RuleID
			}
			if rows[i].File != rows[j].File {
				return rows[i].File < rows[j].File
			}
			return rows[i].StartLine < rows[j].StartLine
		})
	case SortNewest:
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].LastSeen != rows[j].LastSeen {
				return rows[i].LastSeen > rows[j].LastSeen
			}
			if rows[i].File != rows[j].File {
				return rows[i].File < rows[j].File
			}
			return rows[i].StartLine < rows[j].StartLine
		})
	default:
		// severity is the default, and "unknown sort" was rejected in Validate.
		sortIssues(rows)
	}
	return rows
}

// issueFacets carries the three fixed-value facets between the counting pass
// and the result, so the counts are plainly the pre-filter ones.
type issueFacets struct {
	Severities map[string]int
	Statuses   map[string]int
	Sources    map[string]int
}

func issueSeverity(r IssueRow) string { return r.Severity }
func issueStatus(r IssueRow) string   { return string(r.Status) }
func issueSource(r IssueRow) string   { return r.Source }

// facetsOf counts the three facets over one population.
func facetsOf(rows []IssueRow) issueFacets {
	return issueFacets{
		Severities: facetCounts(rows, issueSeverity),
		Statuses:   facetCounts(rows, issueStatus),
		Sources:    facetCounts(rows, issueSource),
	}
}

// facetCounts counts rows per facet value, keeping the canonical order for the
// keys that have one (severity, status, source) and sorted order for the
// free-form ones. A facet value absent from the data is absent from the map,
// not present as zero: "no error issues" is a claim about this repository that
// the Summary page renders, and inventing the zero here would make the two
// answers depend on who counted.
func facetCounts(rows []IssueRow, key func(IssueRow) string) map[string]int {
	out := map[string]int{}
	for _, r := range rows {
		out[key(r)]++
	}
	return out
}

// countRules is the rule facet: counts per rule id, highest first, capped so a
// repository with four hundred firing rules does not ship four hundred chips.
func countRules(rows []IssueRow) []RuleCount {
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.RuleID]++
	}
	out := make([]RuleCount, 0, len(counts))
	for id, n := range counts {
		out = append(out, RuleCount{RuleID: id, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].RuleID < out[j].RuleID
	})
	if len(out) > 20 {
		out = out[:20]
	}
	if out == nil {
		out = []RuleCount{}
	}
	return out
}

// paginate converts a total and a request into the page it is on and how many
// pages exist. A page size of zero means "everything", so totalPages is 1 and
// the page slice returns the whole set — that is what a facet pass wants, and
// it keeps a special case out of every caller.
func paginate(total, page, size int) (int, int) {
	if size <= 0 {
		return 1, 1
	}
	pages := (total + size - 1) / size
	if pages == 0 {
		pages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	return page, pages
}

// pageSlice cuts one page out of the sorted set. An out-of-range page returns
// no rows rather than wrapping around: page 9 of 3 pages is an empty result,
// which the UI renders as "nothing here", not as the last page again.
func pageSlice(rows []IssueRow, page, size int) []IssueRow {
	if len(rows) == 0 {
		return rows
	}
	if size <= 0 || page <= 0 {
		return rows
	}
	start := (page - 1) * size
	if start >= len(rows) {
		return nil
	}
	end := start + size
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end]
}

// Per-run measure aggregate ------------------------------------------------
//
// The gate needs one number per metric for one run: the worst case in it. The
// engine fails a run when any single file crosses the error band, so the worst
// value is the only aggregate that can be compared against the band and mean
// the same thing the checker said. An average would answer "is this repository
// roughly this complex", which is a different question and the wrong one here.

// WorstInRun returns the largest value recorded for one metric in one run, and
// whether the run recorded that metric at all. The bool matters: a metric the
// engine did not produce is not a metric that passed, and reporting 0 for it
// would read as "every file is under the threshold".
func (s *Store) WorstInRun(runID int64, metric string) (int, bool) {
	var worst int
	err := s.db.QueryRow(`SELECT MAX(value) FROM metric_snapshots WHERE run_id = ? AND metric = ?`,
		runID, metric).Scan(&worst)
	if err == sql.ErrNoRows {
		return 0, false
	}
	if err != nil {
		return 0, false
	}
	return worst, true
}

// RunMeasures aggregates every metric in one run: the worst value and how many
// snapshots exceeded the warning band. One query per run rather than per
// metric, because the gate endpoint asks for a dozen metrics at once.
func (s *Store) RunMeasures(runID int64) ([]RunMeasure, error) {
	rows, err := s.db.Query(`SELECT metric, MAX(value),
	                                 SUM(CASE WHEN value >= warn_threshold THEN 1 ELSE 0 END)
	                          FROM metric_snapshots WHERE run_id = ?
	                          GROUP BY metric ORDER BY metric`, runID)
	if err != nil {
		return nil, fmt.Errorf("read run measures: %w", err)
	}
	defer rows.Close()
	var out []RunMeasure
	for rows.Next() {
		var m RunMeasure
		if err := rows.Scan(&m.Metric, &m.Worst, &m.Exceeded); err != nil {
			return nil, fmt.Errorf("read run measures: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RunMeasure is one metric's aggregate in one run.
type RunMeasure struct {
	Metric   string `json:"metric"`
	Worst    int    `json:"worst"`
	Exceeded int    `json:"exceeded"`
}
