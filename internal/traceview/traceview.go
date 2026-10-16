// Package traceview builds the one-trace panorama shared by the `metricgate
// trace` verb and the MCP `get_trace` tool.
//
// The value of a trace is that it is the key joining five tables the tool
// already writes but never joined: the run history, its findings, its metric
// snapshots, the trace attributes and the MCP call log. Building that join in
// one package is what keeps the CLI answer and the MCP answer byte-comparable
// for the same inputs — the same reason store.TrendReport is shared.
package traceview

import (
	"sort"

	"metricgate/internal/calllog"
	"metricgate/internal/store"
)

// View is the machine-readable shape of one trace. Every list is non-nil so
// the JSON reads "[]" rather than "null": an empty list and an absent one are
// different statements, and a reader should never have to guess which it got.
type View struct {
	TraceID string           `json:"traceId"`
	Attrs   []store.Attr     `json:"attrs"`
	Runs    []Run            `json:"runs"`
	Calls   []calllog.Record `json:"calls"`
	Totals  Totals           `json:"totals"`
}

// Run is one stored run plus its findings, folded together because a reader
// asking "what did this trace do" wants the verdict next to its causes.
//
// The fields are spelled out rather than embedding store.RunRecord: RunRecord
// carries no JSON tags (the Cockpit reads it through a case-tolerant helper),
// and a brand-new machine-readable surface should not inherit PascalCase keys
// that a consumer would have to guess at.
type Run struct {
	ID           int64     `json:"id"`
	StartedAt    string    `json:"startedAt"`
	DurationMs   int64     `json:"durationMs"`
	ToolVersion  string    `json:"toolVersion"`
	GitCommit    string    `json:"gitCommit,omitempty"`
	Files        int       `json:"files"`
	ProjectRoot  string    `json:"projectRoot,omitempty"`
	PolicyDigest string    `json:"policyDigest,omitempty"`
	TraceID      string    `json:"traceId,omitempty"`
	ErrorCount   int       `json:"errorCount"`
	WarningCount int       `json:"warningCount"`
	InfoCount    int       `json:"infoCount"`
	Archived     bool      `json:"archived"`
	Findings     []Finding `json:"findings"`
}

// Finding is a finding reduced to the fields a trace view needs. It drops the
// byte-span evidence on purpose: this is a routing view, and the evidence is
// already available from the run that produced it.
type Finding struct {
	RuleID   string `json:"ruleId"`
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"startLine"`
	Message  string `json:"message"`
	Accepted bool   `json:"accepted,omitempty"`
}

// Totals is the one-line answer a reader scans first.
type Totals struct {
	Runs     int                 `json:"runs"`
	Errors   int                 `json:"errors"`
	Warnings int                 `json:"warnings"`
	Infos    int                 `json:"infos"`
	Calls    int                 `json:"calls"`
	ByPhase  map[string]int      `json:"callsByPhase"`
	ByTool   map[string]int      `json:"callsByTool"`
	Verify   calllog.VerifyStats `json:"verify"`
}

// Collect gathers the sources for one trace id. A missing store, a missing
// call log or an unknown id are all legal: the view says what it found and the
// empty lists carry the answer.
func Collect(storePath, logPath, traceID string) View {
	view := View{TraceID: traceID, Attrs: []store.Attr{}, Runs: []Run{}, Calls: []calllog.Record{}}
	view.Attrs, view.Runs = collectRuns(storePath, traceID)
	view.Calls = collectCalls(logPath, traceID)
	view.Totals = totals(view)
	return view
}

// collectRuns reads the stored runs (and their findings) attributed to the
// trace, plus the attributes attached to it. It returns both because they come
// from one store open: closing and reopening to read a single trace's attrs
// would double the file-handle churn for no gain.
func collectRuns(storePath, traceID string) ([]store.Attr, []Run) {
	attrs := []store.Attr{}
	runs := []Run{}
	if storePath == "" {
		return attrs, runs
	}
	st, err := store.Open(storePath)
	if err != nil {
		return attrs, runs
	}
	defer st.Close()
	if got, err := st.TraceAttrs(traceID); err == nil {
		attrs = got
	}
	records, err := st.LatestRunsIn(store.Window{TraceIDs: []string{traceID}})
	if err != nil {
		return attrs, runs
	}
	for _, rec := range records {
		runs = append(runs, buildRun(st, rec))
	}
	return attrs, runs
}

// buildRun attaches one run's findings.
func buildRun(st *store.Store, rec store.RunRecord) Run {
	run := Run{
		ID: rec.ID, StartedAt: rec.StartedAt, DurationMs: rec.DurationMs,
		ToolVersion: rec.ToolVersion, GitCommit: rec.GitCommit, Files: rec.Files,
		ProjectRoot: rec.ProjectRoot, PolicyDigest: rec.PolicyDigest, TraceID: rec.TraceID,
		ErrorCount: rec.ErrorCount, WarningCount: rec.WarningCount, InfoCount: rec.InfoCount,
		Archived: rec.Archived, Findings: []Finding{},
	}
	findings, err := st.RunFindings(rec.ID)
	if err != nil {
		return run
	}
	for _, f := range findings {
		run.Findings = append(run.Findings, Finding{
			RuleID: f.RuleID, Severity: string(f.Severity), File: f.File,
			Line: f.StartLine, Message: f.Message, Accepted: f.Accepted,
		})
	}
	return run
}

// collectCalls reads the MCP call log and keeps the calls that share this trace
// id. A missing log is the default state, not a failure.
func collectCalls(logPath, traceID string) []calllog.Record {
	out := []calllog.Record{}
	recs, _, err := calllog.Read(logPath)
	if err != nil {
		return out
	}
	for _, r := range recs {
		if r.TraceID == traceID {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

// totals folds a view into the summary line.
func totals(view View) Totals {
	t := Totals{Runs: len(view.Runs), Calls: len(view.Calls), ByPhase: map[string]int{}, ByTool: map[string]int{}}
	for _, run := range view.Runs {
		t.Errors += run.ErrorCount
		t.Warnings += run.WarningCount
		t.Infos += run.InfoCount
	}
	for _, c := range view.Calls {
		t.ByTool[c.Tool]++
		t.ByPhase[string(c.Phase)]++
	}
	t.Verify = calllog.Summarize(view.Calls).Verify
	return t
}
