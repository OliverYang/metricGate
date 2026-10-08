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
	"metricgate/internal/calllog"
	"metricgate/internal/config"
	"metricgate/internal/engine"
	"metricgate/internal/facts"
	"metricgate/internal/git"
	"metricgate/internal/impact"
	"metricgate/internal/stubs"
	"metricgate/internal/symbols"
)

const toolVersion = "0.1.0"

func main() {
	configPath := flag.String("config", "policy.yaml", "policy file (loaded once at startup)")
	callLogPath := flag.String("calllog", os.Getenv("METRICGATE_CALLLOG"),
		"append one JSON line per tool call to this file; '1' means "+calllog.DefaultPath+
			". Off unless set here or via $METRICGATE_CALLLOG; local file only, never sent anywhere, and never records the code argument")
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

	svc := &service{eng: eng, cfg: cfg, log: newCallLogger(*callLogPath)}
	srv := mcp.NewServer(&mcp.Implementation{Name: "metricgate", Version: toolVersion}, nil)
	registerTools(srv, svc)

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

type impactIn struct {
	Path  string `json:"path,omitempty" jsonschema:"file or directory to index (default .)"`
	Query string `json:"query" jsonschema:"symbol name to analyse (exact match; near-miss names are reported in the note when nothing matches)"`
	Max   int    `json:"max,omitempty" jsonschema:"maximum references to return (default 50)"`
}

// getImpact answers "what breaks if this symbol changes?" from the definition
// and reference indexes built in one walk. It is the same computation the CLI
// impact verb performs, so the two cannot drift.
func (s *service) getImpact(ctx context.Context, req *mcp.CallToolRequest, in impactIn) (*mcp.CallToolResult, impact.Report, error) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, impact.Report{}, fmt.Errorf("query is empty")
	}
	path := in.Path
	if path == "" {
		path = "."
	}
	_, defs, uses, err := facts.CollectWithRefs(path)
	if err != nil {
		return nil, impact.Report{}, err
	}
	report := impact.Compute(in.Query, defs, uses)
	if in.Max > 0 && len(report.References) > in.Max {
		report.References = report.References[:in.Max]
	}
	return nil, report, nil
}
