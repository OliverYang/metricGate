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
	"flag"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"metricgate/internal/config"
	"metricgate/internal/diff"
	"metricgate/internal/engine"
	"metricgate/internal/facts"
	"metricgate/internal/model"
	"metricgate/internal/stubs"
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
		Description: "List unfinished code: empty bodies, panic(\"not implemented\") bodies, Java UnsupportedOperationException throws. Use as the repair worklist.",
	}, svc.listStubs)

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
	Path     string `json:"path" jsonschema:"file or directory to describe"`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"character budget for the pack, ~4 chars per token (default 6000)"`
}

type constraintsOut struct {
	Pack  string `json:"pack"`
	Files int    `json:"files"`
}

func (s *service) getConstraints(ctx context.Context, req *mcp.CallToolRequest, in constraintsIn) (*mcp.CallToolResult, constraintsOut, error) {
	path := in.Path
	if path == "" {
		path = "."
	}
	budget := in.MaxChars
	if budget <= 0 {
		budget = 6000
	}
	files, err := facts.Collect(path)
	if err != nil {
		return nil, constraintsOut{}, err
	}
	if len(files) == 0 {
		return nil, constraintsOut{}, fmt.Errorf("no supported source files under %q", path)
	}
	return nil, constraintsOut{Pack: facts.Render(files, s.cfg, budget), Files: len(files)}, nil
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
		changed, err := diff.ChangedLines("", diff.ResolveBase("", "HEAD"))
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
	return nil, stubsOut{Count: len(items), Items: items}, nil
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
