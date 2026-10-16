package engine

import (
	"path/filepath"

	"metricgate/internal/diff"
	"metricgate/internal/metrics"
	"metricgate/internal/model"
)

// FilterSummary keeps only findings that belong to changed lines and returns
// how many pre-existing findings were hidden.
//
// It is the no-base-values form of FilterSummaryWithBase: without a base
// revision every metric verdict that a changed range intersects is kept. Both
// gate entry points go through baseline.Split, which always supplies base
// values, so this form is for callers that genuinely have no base tree (an
// editor working on a directory that is not a worktree) — not the default an
// entry point may choose when it is convenient.
//
// Semantics (documented in README "Diff-only mode"):
//   - rule findings are kept when their reported range intersects a changed
//     range (they are line-precise, so this is effectively "line is new")
//   - function-scoped metric findings are kept when the reported range
//     intersects a changed range — touching a function resurfaces its
//     metric verdicts ("you touched it, you own it")
//   - file-length findings are kept only when the whole file is new
//   - metric snapshots are never filtered: trends always record full
//     measurements
func FilterSummary(s *model.RunSummary, changed map[string][]diff.Range) (*model.RunSummary, int) {
	split := SplitSummary(s, changed, nil)
	return split.RunSummary, len(split.PreExisting)
}

// MetricKey identifies one measured value across revisions: the file, the
// measured scope (function or class name, empty for file-scoped metrics) and
// the metric id. It is the join key between two revisions' snapshots, chosen
// over line numbers because a scope that moved down the file is still the
// same scope.
type MetricKey struct {
	File   string
	Scope  string
	Metric string
}

// BaseValues maps a measured scope to the value it had at the base revision.
type BaseValues map[MetricKey]int

// IndexBaseValues builds the lookup used to decide whether a metric verdict
// is new. Snapshots from a base run are the only input, so the index is a
// pure function of that run.
func IndexBaseValues(ms []model.MetricResult) BaseValues {
	out := make(BaseValues, len(ms))
	for _, m := range ms {
		out[MetricKey{File: filepath.ToSlash(m.File), Scope: m.Scope, Metric: m.Metric}] = m.Value
	}
	return out
}

// spanKey locates one finding inside the current run's own snapshots.
type spanKey struct {
	file   string
	metric string
	start  int
	end    int
}

// FilterSummaryWithBase is FilterSummary plus the base-revision values.
//
// The line-range test alone cannot tell "you made this worse" from "you
// touched a scope that was already bad": a class-level metric spans the whole
// class body, so editing any method inside it re-reports all five CK values
// even when their numbers are identical. With base values available, a metric
// finding survives only when its scope is new (no base value to compare
// against) or its value actually rose — the same "did the change make it
// worse" question the regression check asks, applied to the gate's own
// verdicts.
//
// It returns the filtered summary, how many findings were hidden as
// pre-existing, and how many metric findings were dropped because their value
// did not move. The two counts are reported separately: "untouched" and
// "touched but unchanged" are different facts about a change, and collapsing
// them would hide exactly the over-reporting this exists to fix.
//
// A nil or empty base disables the value test entirely (every intersecting
// metric finding is kept), so callers without a base tree lose nothing.
func FilterSummaryWithBase(s *model.RunSummary, changed map[string][]diff.Range, base BaseValues) (*model.RunSummary, int, int) {
	split := SplitSummary(s, changed, base)
	return split.RunSummary, len(split.PreExisting), len(split.Unchanged)
}

// DiffSplit is one run sorted by what the diff filter decided about each
// finding: the ones this change owns, the ones that predate it, and the ones
// inside a changed range whose measured value did not move. SplitSummary
// carries all three so a caller can show what the gate is not talking about
// without recomputing the verdicts.
type DiffSplit struct {
	RunSummary  *model.RunSummary // Kept, as FilterSummaryWithBase returns it
	Kept        []model.Finding
	PreExisting []model.Finding
	Unchanged   []model.Finding
}

// SplitSummary is FilterSummaryWithBase with the verdict groups returned
// instead of only their sizes.
func SplitSummary(s *model.RunSummary, changed map[string][]diff.Range, base BaseValues) DiffSplit {
	fileLen := map[string]int{}
	for _, m := range s.Metrics {
		if m.Metric == metrics.MetricFileLength {
			fileLen[filepath.ToSlash(m.File)] = m.Value
		}
	}
	spans := indexSpans(s.Metrics)

	out := DiffSplit{
		Kept:        make([]model.Finding, 0, len(s.Findings)),
		PreExisting: make([]model.Finding, 0, len(s.Findings)),
		Unchanged:   make([]model.Finding, 0, len(s.Findings)),
	}
	for _, f := range s.Findings {
		switch verdictFor(f, changed, fileLen, base, spans) {
		case verdictKeep:
			out.Kept = append(out.Kept, f)
		case verdictUnchanged:
			out.Unchanged = append(out.Unchanged, f)
		default:
			out.PreExisting = append(out.PreExisting, f)
		}
	}

	summary := *s
	summary.Findings = out.Kept
	out.RunSummary = &summary
	return out
}

// verdict is the disposition of one finding under the diff filter.
type verdict int

const (
	verdictKeep      verdict = iota // belongs to this change
	verdictHidden                   // pre-existing, not in the changed ranges
	verdictUnchanged                // in the changed ranges but the value did not move
)

// verdictFor decides one finding. It is split out so the loop above stays a
// switch and each rule keeps its own explanation.
func verdictFor(f model.Finding, changed map[string][]diff.Range, fileLen map[string]int, base BaseValues, spans map[spanKey]model.MetricResult) verdict {
	file := filepath.ToSlash(f.File)
	ranges := rangesFor(changed, file)
	if len(ranges) == 0 {
		return verdictHidden
	}
	if f.RuleID == metrics.MetricFileLength {
		// The file's own file-length snapshot carries its total size;
		// a brand-new file has every line in the diff.
		if diff.Len(ranges) >= fileLen[file] {
			return verdictKeep
		}
		return verdictHidden
	}
	if !diff.Intersects(ranges, f.StartLine, f.EndLine) {
		return verdictHidden
	}
	if valueMoved(f, base, spans) {
		return verdictKeep
	}
	return verdictUnchanged
}

// valueMoved reports whether a metric finding's scope is new or its value rose
// since the base revision. Non-metric findings always count as moved: a rule
// hit is line-precise, so intersecting a changed range already means the line
// is new.
func valueMoved(f model.Finding, base BaseValues, spans map[spanKey]model.MetricResult) bool {
	if base == nil || f.Source != model.SrcMetric {
		return true
	}
	m, ok := spans[spanKey{file: filepath.ToSlash(f.File), metric: f.RuleID, start: f.StartLine, end: f.EndLine}]
	if !ok {
		// No snapshot for this finding means we cannot judge it; keeping it
		// is the safe direction because dropping a real regression is worse
		// than reporting an unchanged one.
		return true
	}
	prev, ok := base[MetricKey{File: filepath.ToSlash(m.File), Scope: m.Scope, Metric: m.Metric}]
	if !ok {
		return true // scope is new: no base to regress from
	}
	return m.Value > prev
}

// indexSpans maps each snapshot onto the finding span it produces, so a
// finding can be traced back to its measured value. Findings and snapshots
// come from the same run and both use scopeRange, so the spans agree by
// construction.
func indexSpans(ms []model.MetricResult) map[spanKey]model.MetricResult {
	out := make(map[spanKey]model.MetricResult, len(ms))
	for _, m := range ms {
		out[spanKey{file: filepath.ToSlash(m.File), metric: m.Metric, start: m.StartLine, end: m.EndLine}] = m
	}
	return out
}

// rangesFor looks up changed ranges for an engine-reported file path.
// It only returns non-nil if the file is explicitly in the changed map,
// with no suffix matching to avoid false positives from similar paths.
func rangesFor(changed map[string][]diff.Range, file string) []diff.Range {
	if r, ok := changed[file]; ok {
		return r
	}
	return nil
}
