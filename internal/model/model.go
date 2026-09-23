// Package model defines the shared data types that flow through the
// metricgate pipeline: findings, metric snapshots and run summaries.
package model

import (
	"sort"
	"time"
)

// Severity is the seriousness of a finding. It maps onto SARIF levels.
type Severity string

const (
	SevError   Severity = "error"
	SevWarning Severity = "warning"
	SevInfo    Severity = "info"
)

// SARIFLevel converts the severity to a SARIF 2.1.0 result level.
func (s Severity) SARIFLevel() string {
	switch s {
	case SevError:
		return "error"
	case SevWarning:
		return "warning"
	default:
		return "note"
	}
}

// Source marks where a finding came from. Deterministic rules and metric
// thresholds are "rule"/"metric"; LLM stages (v2+) will use "llm".
type Source string

const (
	SrcRule   Source = "rule"
	SrcMetric Source = "metric"
)

// Finding is a single violation reported at a precise location.
// Lines and columns are 1-based so they match editor and SARIF conventions.
type Finding struct {
	RuleID    string   `json:"ruleId"`
	Severity  Severity `json:"severity"`
	Source    Source   `json:"source"`
	File      string   `json:"file"`
	StartLine int      `json:"startLine"`
	StartCol  int      `json:"startColumn"`
	EndLine   int      `json:"endLine"`
	EndCol    int      `json:"endColumn"`
	Message   string   `json:"message"`
}

// MetricResult is one measured value for one scope (a function, or the file).
// Warn/Err record the thresholds that applied when the value was measured.
type MetricResult struct {
	Metric string `json:"metric"`
	File   string `json:"file"`
	Scope  string `json:"scope"`
	Value  int    `json:"value"`
	Warn   int    `json:"warn"`
	Err    int    `json:"error"`
}

// RunSummary is the outcome of one engine run over a codebase.
type RunSummary struct {
	Files       int            `json:"files"`
	ParseErrors []string       `json:"parseErrors,omitempty"`
	Findings    []Finding      `json:"findings"`
	Metrics     []MetricResult `json:"metrics"`
	Duration    time.Duration  `json:"-"`
}

// Count returns how many findings carry the given severity.
func (r *RunSummary) Count(sev Severity) int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == sev {
			n++
		}
	}
	return n
}

// SortFindings orders findings by file, then line, then rule id, so output is
// stable between runs.
func SortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		return a.RuleID < b.RuleID
	})
}

// SortMetrics orders metric snapshots by file, then scope, then metric name.
func SortMetrics(ms []MetricResult) {
	sort.SliceStable(ms, func(i, j int) bool {
		a, b := ms[i], ms[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		return a.Metric < b.Metric
	})
}
