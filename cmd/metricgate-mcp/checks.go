package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"metricgate/internal/config"
	"metricgate/internal/engine"
	"metricgate/internal/git"
	"metricgate/internal/model"
)

// The two gate entry points live apart from main for the same reason
// registerTools does: main stays a startup sequence, and the code that turns
// findings into a verdict — the part whose wording an agent actually reads —
// reads as one unit.

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

// callOutcome exposes the verdict to the call log without the wrapper having
// to know this type. See outcomeCarrier.
func (o checkCodeOut) callOutcome() (findings, errors, warnings int, verdict string) {
	return len(o.Findings), o.Errors, o.Warnings, o.Verdict
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

// callOutcome exposes the verdict to the call log without the wrapper having
// to know this type. See outcomeCarrier.
func (o checkPathOut) callOutcome() (findings, errors, warnings int, verdict string) {
	return len(o.Findings), o.Errors, o.Warnings, o.Verdict
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
