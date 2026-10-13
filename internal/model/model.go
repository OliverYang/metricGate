// Package model defines the shared data types that flow through the
// metricgate pipeline: findings, metric snapshots and run summaries.
package model

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
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
	// Evidence is optional, machine-checkable proof of the finding's exact
	// source span. It is nil for file-level findings that have no AST node
	// (metrics anchored only by line). SARIF locations stay authoritative;
	// Evidence rides in result.properties and is revalidated after an edit
	// by hashing the same byte range.
	Evidence *Evidence `json:"evidence,omitempty"`
	// Accepted marks a warning covered by the acceptance ledger
	// (.metricgate/accepted.yaml): a named owner conceded it with a reason.
	// Accepted warnings stay visible and stored but do not fail the gate.
	// Only the CLI check path sets it; errors are never acceptable.
	Accepted bool `json:"accepted,omitempty"`
}

// AnchorStatus records how precisely a finding's location was captured.
const (
	AnchorExact      = "exact"      // byte span + snippet hash from an AST node
	AnchorLineOnly   = "line_only"  // line/column only, no byte span
	AnchorUnanchored = "unanchored" // no reliable location
)

// Evidence is the deterministic anchor for a finding: the byte span it was
// extracted from and the SHA-256 of those exact bytes. Hashing the raw bytes
// (no trim/normalize) lets a consumer detect that an edit invalidated the
// anchor, so a host agent can trust the location it is asked to fix.
type Evidence struct {
	StartByte   uint   `json:"startByte,omitempty"`
	EndByte     uint   `json:"endByte,omitempty"`
	SnippetHash string `json:"snippetHash,omitempty"`
	Anchor      string `json:"anchorStatus"` // exact|line_only|unanchored
}

// NewEvidence builds exact evidence from a source byte span: the raw bytes are
// hashed as-is (no trim/normalize), so a later edit that shifts or rewrites the
// span changes the hash and invalidates the anchor. Out-of-range spans degrade
// to line_only rather than lying about an exact location.
func NewEvidence(src []byte, start, end uint) *Evidence {
	if int(end) > len(src) || start > end {
		return &Evidence{Anchor: AnchorLineOnly}
	}
	sum := sha256.Sum256(src[start:end])
	return &Evidence{
		StartByte:   start,
		EndByte:     end,
		SnippetHash: "sha256:" + hex.EncodeToString(sum[:]),
		Anchor:      AnchorExact,
	}
}

// MetricResult is one measured value for one scope (a function, a class, or
// the file). Warn/Err record the thresholds that applied when the value was
// measured. StartLine/EndLine anchor the scope so a consumer (regression
// detection, reports) can point at the measured declaration instead of
// re-parsing; a file-scoped metric reports line 1.
type MetricResult struct {
	Metric    string `json:"metric"`
	File      string `json:"file"`
	Scope     string `json:"scope"`
	Value     int    `json:"value"`
	Warn      int    `json:"warn"`
	Err       int    `json:"error"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
}

// RunSummary is the outcome of one engine run over a codebase.
type RunSummary struct {
	Files       int            `json:"files"`
	ParseErrors []string       `json:"parseErrors,omitempty"`
	Findings    []Finding      `json:"findings"`
	Metrics     []MetricResult `json:"metrics"`
	Ignored     int            `json:"ignored,omitempty"`
	// IgnoredByRule splits Ignored by rule id, for the policy audit. A rule
	// silenced many times is a rule the team has voted against, and only the
	// per-rule view shows which one it is.
	IgnoredByRule map[string]int `json:"ignoredByRule,omitempty"`
	Duration      time.Duration  `json:"-"`
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

// SortByKeys orders items by a list of three-way key comparators: the first
// comparator that distinguishes two items decides. It is the shared building
// block for every deterministic ordering in metricgate — findings, metric
// snapshots, references and regression deltas all sort through it, so two
// runs over the same inputs emit byte-identical output.
func SortByKeys[T any](items []T, keys ...func(a, b T) int) {
	sort.SliceStable(items, func(i, j int) bool {
		for _, key := range keys {
			if c := key(items[i], items[j]); c != 0 {
				return c < 0
			}
		}
		return false
	})
}

// SortFindings orders findings by file, then line, then rule id, so output
// is stable between runs.
func SortFindings(fs []Finding) {
	SortByKeys(fs,
		func(a, b Finding) int { return strings.Compare(a.File, b.File) },
		func(a, b Finding) int { return cmp.Compare(a.StartLine, b.StartLine) },
		func(a, b Finding) int { return strings.Compare(a.RuleID, b.RuleID) },
	)
}

// SortMetrics orders metric snapshots by file, then scope, then metric name.
func SortMetrics(ms []MetricResult) {
	SortByKeys(ms,
		func(a, b MetricResult) int { return strings.Compare(a.File, b.File) },
		func(a, b MetricResult) int { return strings.Compare(a.Scope, b.Scope) },
		func(a, b MetricResult) int { return strings.Compare(a.Metric, b.Metric) },
	)
}
