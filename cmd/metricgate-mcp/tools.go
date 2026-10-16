package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"metricgate/internal/calllog"
	"metricgate/internal/dist"
	"metricgate/internal/facts"
	"metricgate/internal/hotspots"
	"metricgate/internal/model"
	"metricgate/internal/store"
	"metricgate/internal/traceview"
)

// addTool registers one tool on the server, routing its handler through the
// call log.
//
// Every tool goes through here rather than calling mcp.AddTool directly, so
// "the surface is instrumented" is a property of this file rather than of a
// reviewer remembering to add a line. With logging off the wrapper returns the
// handler unchanged, so the cost of the indirection is one nil check per call.
func addTool[In, Out any](srv *mcp.Server, svc *service, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	mcp.AddTool(srv, t, wrapTool(svc.gate, wrapLogged(svc.log, t.Name, h)))
}

// registerTools declares the MCP tool surface. It lives apart from main so the
// whole surface reads as one list and main stays a startup sequence. gate is
// the inline-verdict watcher (see gate.go); it may be nil, which disables the
// preamble.
func registerTools(srv *mcp.Server, svc *service) {
	registerLoopTools(srv, svc)
	registerInsightTools(srv, svc)
}

// registerLoopTools declares the tools an agent calls while it is producing and
// checking code: the grounding pack, the two gates, the unfinished-code
// worklist and the symbol/impact lookups that keep generated code aligned with
// the existing surface.
func registerLoopTools(srv *mcp.Server, svc *service) {
	addTool(srv, svc, &mcp.Tool{
		Name:        "get_constraints",
		Description: "Get the dense constraint pack (exact signatures/imports + active policy rules and limits) for a file or directory. Call BEFORE generating code and follow it, so output aligns with the existing API surface.",
	}, svc.getConstraints)
	_ = svc.log // log wiring lives in addTool via wrapLogged below
	addTool(srv, svc, &mcp.Tool{
		Name:        "check_code",
		Description: "Deterministically check in-memory code (0 tokens, milliseconds) without writing to disk. Call after EVERY generated/edited snippet; fix all findings before returning the code.",
	}, svc.checkCode)
	addTool(srv, svc, &mcp.Tool{
		Name:        "check_path",
		Description: "Check files on disk with rules + metrics. With diff_only=true the gate applies the same ratchet as `metricgate check -diff` against base=HEAD: findings outside the changed lines are hidden (reported as `hidden`), and metric findings whose measured value did not rise since the base revision are dropped (reported as `unchanged`). Both counters say what the gate did NOT report — check them instead of trusting a clean verdict; new files have no base values, so their metric findings are always kept. Guarantee holds for path at the worktree root: under a subdirectory the base values are keyed worktree-relatively and the value test cannot join, so nothing is reported as unchanged.",
	}, svc.checkPath)
	addTool(srv, svc, &mcp.Tool{
		Name:        "list_stubs",
		Description: "List unfinished code: empty bodies, panic(\"not implemented\") bodies, Java UnsupportedOperationException throws — each with its exact definition site. Use as the repair worklist.",
	}, svc.listStubs)
	addTool(srv, svc, &mcp.Tool{
		Name:        "find_symbols",
		Description: "Deterministic cross-file symbol index (0 tokens): exact lookups by name and near-miss suggestions (typos, case twins) so generated code aligns with the existing API surface. Call before inventing a new helper name.",
	}, svc.findSymbols)
	addTool(srv, svc, &mcp.Tool{
		Name:        "get_impact",
		Description: "What uses this symbol: every call site and type reference, plus the files affected if it changes. AST-only and name-based (interface dispatch, reflection and dynamic names can be missed), 0 tokens. Call BEFORE renaming a symbol or changing its signature.",
	}, svc.getImpact)
}

// registerInsightTools declares the read-only views over data the gate already
// wrote: the repo map, hotspots, trends, one trace in full, metric stats and
// the review bundle. None of them mutate the store or the code.
func registerInsightTools(srv *mcp.Server, svc *service) {
	addTool(srv, svc, &mcp.Tool{
		Name:        "get_repo_map",
		Description: "Rank every file by how much the rest of the code leans on it (reference graph + PageRank, 0 tokens, AST-only). Call this FIRST to decide what to read, then get_constraints on the chosen files. By default each file also carries its static risk score; pass skip_risk=true to skip that extra pass.",
	}, svc.getRepoMap)
	addTool(srv, svc, &mcp.Tool{
		Name:        "get_hotspots",
		Description: "Rank files by change churn × maintainability debt (Tornhill hotspot analysis) over a git history window, each annotated with its static risk score. Use it to find where defects cluster before a refactor. Requires a git repository.",
	}, svc.getHotspots)
	addTool(srv, svc, &mcp.Tool{
		Name:        "get_trends",
		Description: "Read the local trend database: the per-run severity series, its time range, and the rules that fired most often. Use it to see whether the codebase is improving across runs. Read-only — only `check` writes the store.",
	}, svc.getTrends)
	addTool(srv, svc, &mcp.Tool{
		Name:        "get_trace",
		Description: "One trace in full: the stored runs and findings attributed to a trace id, the trace's attributes (model/prompt_digest/harness/…), and the MCP calls made under it. Use it to attribute a verdict to the unit of work that produced it. Read-only.",
	}, svc.getTrace)
	addTool(srv, svc, &mcp.Tool{
		Name:        "get_metric_stats",
		Description: "Distribution of every measured metric across its scopes (count/min/p50/p90/p95/max/mean) plus the worst scopes per metric, e.g. the most complex functions. Use it to decide where a refactor pays off; `check_path` only reports threshold crossings.",
	}, svc.getMetricStats)
	addTool(srv, svc, &mcp.Tool{
		Name:        "get_review_bundle",
		Description: "Build a deterministic, budgeted review input for a host agent: the current diff plus path selection, active policy constraints, and compact facts. Read-only; metricgate never calls an LLM. Prefer this over sending whole files; pass include_source only when the diff alone is insufficient.",
	}, svc.getReviewBundle)
}

// orDot defaults an omitted path argument to the current directory.
func orDot(path string) string {
	if path == "" {
		return "."
	}
	return path
}

type repoMapIn struct {
	Path     string `json:"path,omitempty" jsonschema:"repository root (default .)"`
	Max      int    `json:"max,omitempty" jsonschema:"maximum files to return (default 25)"`
	SkipRisk bool   `json:"skip_risk,omitempty" jsonschema:"skip joining each file's static risk score (default false: risk is included, which runs the checker once)"`
}

type repoMapOut struct {
	Files   int                `json:"files"`
	Ranking []facts.RankedFile `json:"ranking"`
}

// getRepoMap answers "which files should I read first?" from the reference
// graph. It reuses facts.RankedFiles, the same builder the CLI `map` verb
// uses, so the two cannot drift.
func (s *service) getRepoMap(ctx context.Context, req *mcp.CallToolRequest, in repoMapIn) (*mcp.CallToolResult, repoMapOut, error) {
	path := orDot(in.Path)
	var findings []model.Finding
	var mets []model.MetricResult
	if !in.SkipRisk {
		summary, err := s.eng.Run(path)
		if err != nil {
			return nil, repoMapOut{}, err
		}
		findings, mets = summary.Findings, summary.Metrics
	}
	scores, err := facts.RankedFiles(path, s.cfg, findings, mets)
	if err != nil {
		return nil, repoMapOut{}, err
	}
	max := in.Max
	if max <= 0 {
		max = 25
	}
	if len(scores) > max {
		scores = scores[:max]
	}
	return nil, repoMapOut{Files: len(scores), Ranking: scores}, nil
}

type hotspotsIn struct {
	Path  string `json:"path,omitempty" jsonschema:"repository root (default .)"`
	Since string `json:"since,omitempty" jsonschema:"git history window, e.g. 90.days ago (default 3.months ago)"`
	Max   int    `json:"max,omitempty" jsonschema:"top N files (default 10)"`
}

type hotspotsOut struct {
	Since string                 `json:"since"`
	Count int                    `json:"count"`
	Files []hotspots.FileHotspot `json:"files"`
}

// getHotspots reuses hotspots.FromSummary, the same wiring the CLI `hotspots`
// verb uses, so the MCP and CLI results are identical for the same inputs.
func (s *service) getHotspots(ctx context.Context, req *mcp.CallToolRequest, in hotspotsIn) (*mcp.CallToolResult, hotspotsOut, error) {
	path := orDot(in.Path)
	since := in.Since
	if since == "" {
		since = "3.months ago"
	}
	max := in.Max
	if max <= 0 {
		max = 10
	}
	summary, err := s.eng.Run(path)
	if err != nil {
		return nil, hotspotsOut{}, err
	}
	h, err := hotspots.Compute(hotspots.FromSummary(path, since, max, summary))
	if err != nil {
		return nil, hotspotsOut{}, err
	}
	return nil, hotspotsOut{Since: since, Count: len(h), Files: h}, nil
}

type metricStatsIn struct {
	Path   string `json:"path,omitempty" jsonschema:"file or directory to measure (default .)"`
	Top    int    `json:"top,omitempty" jsonschema:"worst scopes to list per metric (default 5)"`
	Metric string `json:"metric,omitempty" jsonschema:"restrict the report to one metric id, e.g. cyclomatic-complexity (default: every measured metric)"`
}

type metricStatsOut struct {
	Reports []dist.Report `json:"reports"`
}

// getMetricStats reuses dist.Reports, the same aggregation the CLI `stats`
// verb uses, so the MCP and CLI views cannot drift.
func (s *service) getMetricStats(ctx context.Context, req *mcp.CallToolRequest, in metricStatsIn) (*mcp.CallToolResult, metricStatsOut, error) {
	top := in.Top
	if top <= 0 {
		top = 5
	}
	summary, err := s.eng.Run(orDot(in.Path))
	if err != nil {
		return nil, metricStatsOut{}, err
	}
	mets := summary.Metrics
	if in.Metric != "" {
		mets = filterMetric(mets, in.Metric)
	}
	return nil, metricStatsOut{Reports: dist.Reports(mets, top)}, nil
}

// filterMetric keeps only the snapshots of one metric id, so a caller that
// asks about cyclomatic complexity does not pay for the other nine metrics.
func filterMetric(mets []model.MetricResult, id string) []model.MetricResult {
	var out []model.MetricResult
	for _, m := range mets {
		if m.Metric == id {
			out = append(out, m)
		}
	}
	return out
}

type trendsIn struct {
	Runs         int    `json:"runs,omitempty" jsonschema:"how many of the most recent runs to include (default 20)"`
	StorePath    string `json:"store_path,omitempty" jsonschema:"override the trend database path (default: store.path from .metricgate.yaml)"`
	Trace        string `json:"trace,omitempty" jsonschema:"scope the report to one trace id"`
	Model        string `json:"model,omitempty" jsonschema:"scope the report to runs whose trace carries this model attribute"`
	PromptDigest string `json:"prompt_digest,omitempty" jsonschema:"scope the report to runs whose trace carries this prompt_digest attribute"`
}

// getTrends reads the local trend database through the same store.Report the
// CLI `trends` verb uses, so the two payloads cannot drift.
func (s *service) getTrends(ctx context.Context, req *mcp.CallToolRequest, in trendsIn) (*mcp.CallToolResult, store.TrendReport, error) {
	path := in.StorePath
	if path == "" {
		path = s.cfg.Store.Path
	}
	if path == "" {
		return nil, store.TrendReport{}, fmt.Errorf("no trend store configured: set store.path in .metricgate.yaml or pass store_path")
	}
	limit := in.Runs
	if limit <= 0 {
		limit = 20
	}
	st, err := store.Open(path)
	if err != nil {
		return nil, store.TrendReport{}, fmt.Errorf("open store: %w (run `check` first to create %s)", err, path)
	}
	defer st.Close()
	window := store.Window{Limit: limit}
	if window.TraceIDs, err = st.ResolveTraceFilters([]store.TraceFilter{
		{Key: "", Value: in.Trace},
		{Key: "model", Value: in.Model},
		{Key: "prompt_digest", Value: in.PromptDigest},
	}); err != nil {
		return nil, store.TrendReport{}, err
	}
	report, err := st.ReportIn(window)
	if err != nil {
		return nil, store.TrendReport{}, err
	}
	return nil, report, nil
}

type traceIn struct {
	TraceID   string `json:"trace_id" jsonschema:"the trace id to inspect (from $METRICGATE_TRACE_ID on the runs you want)"`
	StorePath string `json:"store_path,omitempty" jsonschema:"override the trend database path (default: store.path from .metricgate.yaml)"`
	LogPath   string `json:"log_path,omitempty" jsonschema:"override the MCP call log path (default: .metricgate/mcp-calls.jsonl)"`
}

// getTrace reuses traceview.Collect, the same builder the CLI `trace` verb
// uses, so the MCP and CLI panoramas cannot drift.
func (s *service) getTrace(ctx context.Context, req *mcp.CallToolRequest, in traceIn) (*mcp.CallToolResult, traceview.View, error) {
	if in.TraceID == "" {
		return nil, traceview.View{}, fmt.Errorf("trace_id is required")
	}
	storePath := in.StorePath
	if storePath == "" {
		storePath = s.cfg.Store.Path
	}
	logPath := in.LogPath
	if logPath == "" {
		logPath = calllog.DefaultPath
	}
	return nil, traceview.Collect(storePath, logPath, in.TraceID), nil
}
