package engine

import (
	"path/filepath"
	"sort"
	"time"

	"metricgate/internal/deps"
	"metricgate/internal/model"
)

// CandidateResult is one ranked candidate implementation: its display name,
// the per-file run summary (findings + metrics only) and the derived rank
// keys. The whole-graph stages (architecture, clones) are deliberately out of
// scope: candidates are typically single files, and a file-scoped run has no
// package graph to violate. This is documented divergence from `check .` —
// rank answers "which candidate is cleaner", not "does the repo still fit".
type CandidateResult struct {
	Name    string            `json:"name"`
	Summary *model.RunSummary `json:"summary"`
	Errors  int               `json:"errors"`
	Warns   int               `json:"warnings"`
}

// RankCandidates runs the per-file pipeline (metrics + rules, with
// suppressions honored) on each candidate file independently and returns the
// results sorted better-first: fewer errors, then fewer warnings, then
// earlier name as a deterministic tiebreak. Parse errors are reported as
// info findings by checkFile, so a syntactically broken candidate ranks last
// through its error/warning counts naturally.
func (e *Engine) RankCandidates(files []string) []CandidateResult {
	out := make([]CandidateResult, 0, len(files))
	for _, f := range files {
		start := time.Now()
		root := filepath.Dir(f)
		e.depAllow = deps.ForRoot(root, e.cfg).Prefixes()
		summary := &model.RunSummary{Files: 1}
		results := e.runWorkers(root, []string{f})
		for res := range results {
			summary.Findings = append(summary.Findings, res.findings...)
			summary.Metrics = append(summary.Metrics, res.metrics...)
			countSuppressed(summary, res.ignored, res.ignoredByRule)
			if res.parseError != "" {
				summary.ParseErrors = append(summary.ParseErrors, res.path)
			}
		}
		model.SortFindings(summary.Findings)
		model.SortMetrics(summary.Metrics)
		summary.Duration = time.Since(start)
		cr := CandidateResult{Name: f, Summary: summary}
		for _, fd := range summary.Findings {
			switch fd.Severity {
			case model.SevError:
				cr.Errors++
			case model.SevWarning:
				cr.Warns++
			}
		}
		out = append(out, cr)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Errors != out[j].Errors {
			return out[i].Errors < out[j].Errors
		}
		if out[i].Warns != out[j].Warns {
			return out[i].Warns < out[j].Warns
		}
		return out[i].Name < out[j].Name
	})
	return out
}
