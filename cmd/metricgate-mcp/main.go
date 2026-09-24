// Command metricgate-mcp exposes metricgate as an MCP (Model Context
// Protocol) stdio server, so coding agents can call it as a tool during a
// session: fetch the constraint pack before generating code, check every
// edit deterministically, and list unfinished stubs.
//
// Configure in an MCP client, e.g. Claude Code:
//
//	claude mcp add metricgate -- /path/to/metricgate-mcp -config /path/to/policy.yaml
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"metricgate/internal/bundle"
	"metricgate/internal/config"
	"metricgate/internal/engine"
	"metricgate/internal/facts"
	"metricgate/internal/git"
	"metricgate/internal/model"
	"metricgate/internal/stubs"
	"metricgate/internal/symbols"
)

const toolVersion = "0.1.0"

func main() {
	configPath := flag.String("config", "policy.yaml", "policy file (loaded once at startup)")
	flag.Parse()

	cfg, err := loadPolicy(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metricgate-mcp:", err)
		os.Exit(2)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metricgate-mcp:", err)
		os.Exit(2)
	}
	defer eng.Close()

	svc := &service{eng: eng, cfg: cfg}
	srv := mcp.NewServer(&mcp.Implementation{Name: "metricgate", Version: toolVersion}, nil)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_constraints",
		Description: "Get the dense constraint pack (exact signatures/imports + active policy rules and limits) for a file or directory. Call BEFORE generating code and follow it, so output aligns with the existing API surface.",
	}, svc.getConstraints)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "check_code",
		Description: "Deterministically check in-memory code (0 tokens, milliseconds) without writing to disk. Call after EVERY generated/edited snippet; fix all findings before returning the code.",
	}, svc.checkCode)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "check_path",
		Description: "Check files on disk with rules + metrics. With diff_only=true only new violations are reported (legacy code is not re-flagged).",
	}, svc.checkPath)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_stubs",
		Description: "List unfinished code: empty bodies, panic(\"not implemented\") bodies, Java UnsupportedOperationException throws — each with its exact definition site. Use as the repair worklist.",
	}, svc.listStubs)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "find_symbols",
		Description: "Deterministic cross-file symbol index (0 tokens): exact lookups by name and near-miss suggestions (typos, case twins) so generated code aligns with the existing API surface. Call before inventing a new helper name.",
	}, svc.findSymbols)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_review_bundle",
		Description: "Build a deterministic, budgeted review input for a host agent: the current diff plus path selection, active policy constraints, and compact facts. Read-only; metricgate never calls an LLM. Prefer this over sending whole files; pass include_source only when the diff alone is insufficient.",
	}, svc.getReviewBundle)

	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "metricgate-mcp:", err)
		os.Exit(1)
	}
}

func loadPolicy(path string) (*config.Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return config.Default()
	}
	return config.LoadFile(path)
}

// service implements the MCP tool handlers on top of one engine instance.
type service struct {
	eng *engine.Engine
	cfg *config.Config
}

type constraintsIn struct {
	Path            string `json:"path" jsonschema:"file or directory to describe"`
	MaxChars        int    `json:"max_chars,omitempty" jsonschema:"UTF-8 byte budget (default 6000); mutually exclusive with max_tokens"`
	MaxTokens       int    `json:"max_tokens,omitempty" jsonschema:"approximate model token budget; marked approximate unless a tokenizer is supplied"`
	IncludeExcluded bool   `json:"include_excluded,omitempty" jsonschema:"include paths excluded by policy, for legacy all-files context"`
}

type constraintsOut struct {
	Pack     string           `json:"pack"`
	Files    int              `json:"files"`
	Budget   int              `json:"budget"`
	Used     int              `json:"used"`
	Counter  string           `json:"counter"`
	Exact    bool             `json:"exact"`
	Included []facts.PackRef  `json:"included"`
	Omitted  []facts.Omission `json:"omitted"`
}

func (s *service) getConstraints(ctx context.Context, req *mcp.CallToolRequest, in constraintsIn) (*mcp.CallToolResult, constraintsOut, error) {
	path := in.Path
	if path == "" {
		path = "."
	}
	if in.MaxChars > 0 && in.MaxTokens > 0 {
		return nil, constraintsOut{}, fmt.Errorf("max_chars and max_tokens are mutually exclusive")
	}
	var files []*facts.FileFacts
	var err error
	if in.IncludeExcluded {
		files, err = facts.Collect(path)
	} else {
		files, err = facts.CollectSelected(path, s.cfg)
	}
	if err != nil {
		return nil, constraintsOut{}, err
	}
	if len(files) == 0 {
		return nil, constraintsOut{}, fmt.Errorf("no supported source files selected under %q", path)
	}
	budget, counter := in.MaxChars, facts.TokenCounter(facts.CharCounter{})
	if in.MaxTokens > 0 {
		budget, counter = in.MaxTokens, facts.CharEstimate{}
	}
	if budget <= 0 {
		budget = 6000
	}
	pack, err := facts.RenderBudgeted(files, s.cfg, facts.BudgetOptions{Budget: budget, Counter: counter})
	if err != nil {
		return nil, constraintsOut{}, fmt.Errorf("context budget: %w", err)
	}
	return nil, constraintsOut{Pack: pack.Text, Files: len(files), Budget: pack.Budget, Used: pack.Used, Counter: pack.Counter, Exact: pack.Exact, Included: pack.Included, Omitted: pack.Omitted}, nil
}

type reviewBundleIn struct {
	Path          string `json:"path,omitempty" jsonschema:"repository root (default .)"`
	Base          string `json:"base,omitempty" jsonschema:"git base ref compared with merge-base against HEAD (default: working tree vs HEAD)"`
	From          string `json:"from,omitempty" jsonschema:"range mode start ref (requires to)"`
	To            string `json:"to,omitempty" jsonschema:"range mode end ref (requires from)"`
	Commit        string `json:"commit,omitempty" jsonschema:"single-commit mode"`
	MaxChars      int    `json:"max_chars,omitempty" jsonschema:"UTF-8 byte budget (default 6000); mutually exclusive with max_tokens"`
	MaxTokens     int    `json:"max_tokens,omitempty" jsonschema:"approximate model token budget"`
	IncludeSource bool   `json:"include_source,omitempty" jsonschema:"include full source of selected files (default: diff only)"`
}

// getReviewBundle reuses bundle.Build, the same builder the CLI uses, so the
// MCP and CLI schemas cannot drift. The output is a decoded map because the
// SDK cannot infer a schema for the self-referential facts.Symbol type; the
// map is still returned as structuredContent carrying the full JSON bundle.
func (s *service) getReviewBundle(ctx context.Context, req *mcp.CallToolRequest, in reviewBundleIn) (*mcp.CallToolResult, map[string]any, error) {
	path := in.Path
	if path == "" {
		path = "."
	}
	if in.MaxChars > 0 && in.MaxTokens > 0 {
		return nil, nil, fmt.Errorf("max_chars and max_tokens are mutually exclusive")
	}
	budget, counter := in.MaxChars, facts.TokenCounter(facts.CharCounter{})
	if in.MaxTokens > 0 {
		budget, counter = in.MaxTokens, facts.CharEstimate{}
	}
	if budget <= 0 {
		budget, counter = 6000, facts.CharCounter{}
	}
	mode := git.ModeWorkspace
	switch {
	case in.Commit != "":
		mode = git.ModeCommit
	case in.From != "" || in.To != "":
		if in.From == "" || in.To == "" {
			return nil, nil, fmt.Errorf("from and to must be provided together")
		}
		mode = git.ModeRange
	default:
		if in.Base != "" {
			in.Base = git.ResolveBase(path, in.Base)
		}
	}
	b, err := bundle.Build(bundle.BuildOptions{
		Request: bundle.Request{
			Root: path, Mode: mode, From: in.From, To: in.To, Commit: in.Commit,
			Budget: budget, Counter: counter, IncludeSource: in.IncludeSource,
		},
		Config: s.cfg,
	})
	if err != nil {
		return nil, nil, err
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, nil, err
	}
	return nil, out, nil
}

type checkCodeIn struct {
	Name string `json:"name" jsonschema:"virtual file name for the code, e.g. service.go or UserService.java; the extension picks the language"`
	Code string `json:"code" jsonschema:"the source code to check"`
}

type checkCodeOut struct {
	Verdict  string          `json:"verdict"`
	Errors   int             `json:"errors"`
	Warnings int             `json:"warnings"`
	Findings []model.Finding `json:"findings"`
}

func (s *service) checkCode(ctx context.Context, req *mcp.CallToolRequest, in checkCodeIn) (*mcp.CallToolResult, checkCodeOut, error) {
	if in.Code == "" {
		return nil, checkCodeOut{}, fmt.Errorf("code is empty")
	}
	findings, err := s.eng.CheckSource(in.Name, []byte(in.Code))
	if err != nil {
		return nil, checkCodeOut{}, err
	}
	return nil, summarizeFindings(findings), nil
}

type checkPathIn struct {
	Path     string `json:"path,omitempty" jsonschema:"file or directory to check (default .)"`
	DiffOnly bool   `json:"diff_only,omitempty" jsonschema:"only report findings on lines changed vs HEAD"`
}

type checkPathOut struct {
	Verdict  string          `json:"verdict"`
	Files    int             `json:"files"`
	Errors   int             `json:"errors"`
	Warnings int             `json:"warnings"`
	Hidden   int             `json:"hidden,omitempty"`
	Findings []model.Finding `json:"findings"`
}

func (s *service) checkPath(ctx context.Context, req *mcp.CallToolRequest, in checkPathIn) (*mcp.CallToolResult, checkPathOut, error) {
	path := in.Path
	if path == "" {
		path = "."
	}
	summary, err := s.eng.Run(path)
	if err != nil {
		return nil, checkPathOut{}, err
	}
	out := checkPathOut{Files: summary.Files}
	if in.DiffOnly {
		changed, err := git.ChangedLines("", git.ResolveBase("", "HEAD"))
		if err != nil {
			return nil, checkPathOut{}, err
		}
		var hidden int
		summary, hidden = engine.FilterSummary(summary, changed)
		out.Hidden = hidden
	}
	out.Findings = summary.Findings
	out.Errors = summary.Count(model.SevError)
	out.Warnings = summary.Count(model.SevWarning)
	out.Verdict = verdict(s.cfg, out.Errors, out.Warnings)
	return nil, out, nil
}

type stubsIn struct {
	Path string `json:"path,omitempty" jsonschema:"file or directory to scan (default .)"`
}

type stubsOut struct {
	Count int          `json:"count"`
	Items []stubs.Item `json:"items"`
}

func (s *service) listStubs(ctx context.Context, req *mcp.CallToolRequest, in stubsIn) (*mcp.CallToolResult, stubsOut, error) {
	path := in.Path
	if path == "" {
		path = "."
	}
	items, err := stubs.Scan(path)
	if err != nil {
		return nil, stubsOut{}, err
	}
	annotateStubs(path, items)
	return nil, stubsOut{Count: len(items), Items: items}, nil
}

// annotateStubs fills each item's At with the symbol's definition site so
// the worklist points at the exact body to finish. Failures are silent:
// the worklist is still correct without the annotations.
func annotateStubs(path string, items []stubs.Item) {
	files, ix, err := facts.CollectWithIndex(path)
	if err != nil || ix.Len() == 0 {
		return
	}
	for i := range items {
		if items[i].Symbol == "" {
			continue
		}
		for _, d := range ix.Lookup(items[i].Symbol) {
			if d.Signature != "" {
				items[i].At = d.File + ":" + strconv.Itoa(d.Line) + " (" + oneLine(d.Signature, 90) + ")"
				break
			}
		}
	}
	_ = files
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > max {
		r = append(r[:max], []rune("…")...)
	}
	return string(r)
}

type findSymbolsIn struct {
	Path  string `json:"path,omitempty" jsonschema:"file or directory to index (default .)"`
	Query string `json:"query" jsonschema:"symbol name to look up; near-miss suggestions are returned when the exact name does not exist"`
	Max   int    `json:"max,omitempty" jsonschema:"maximum suggestions to return (default 5)"`
}

type findSymbolsOut struct {
	Exact       []symbols.Def   `json:"exact,omitempty"`
	Suggestions []symbols.Match `json:"suggestions,omitempty"`
	Symbols     int             `json:"symbols"`
}

func (s *service) findSymbols(ctx context.Context, req *mcp.CallToolRequest, in findSymbolsIn) (*mcp.CallToolResult, findSymbolsOut, error) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, findSymbolsOut{}, fmt.Errorf("query is empty")
	}
	path := in.Path
	if path == "" {
		path = "."
	}
	_, ix, err := facts.CollectWithIndex(path)
	if err != nil {
		return nil, findSymbolsOut{}, err
	}
	max := in.Max
	if max <= 0 {
		max = 5
	}
	out := findSymbolsOut{
		Exact:   ix.Lookup(in.Query),
		Symbols: ix.Len(),
	}
	if len(out.Exact) == 0 {
		out.Suggestions = ix.Similar(in.Query, max)
	}
	return nil, out, nil
}

// summarizeFindings renders a check_code verdict.
func summarizeFindings(findings []model.Finding) checkCodeOut {
	out := checkCodeOut{Findings: findings}
	for _, f := range findings {
		switch f.Severity {
		case model.SevError:
			out.Errors++
		case model.SevWarning:
			out.Warnings++
		}
	}
	out.Verdict = "PASS"
	if out.Errors > 0 {
		out.Verdict = "FAIL: fix the errors"
	} else if out.Warnings > 0 {
		out.Verdict = "WARN: fix the warnings before submitting"
	}
	return out
}

// verdict renders the gate verdict for a path check.
func verdict(cfg *config.Config, errors, warnings int) string {
	failOn := cfg.FailOn()
	trips := errors
	if failOn == model.SevWarning {
		trips += warnings
	}
	if trips > 0 {
		return fmt.Sprintf("FAIL (%d findings at fail_on=%s)", trips, failOn)
	}
	return "PASS"
}
