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
	fileLen := map[string]int{}
	for _, m := range s.Metrics {
		if m.Metric == metrics.MetricFileLength {
			fileLen[filepath.ToSlash(m.File)] = m.Value
		}
	}

	kept := make([]model.Finding, 0, len(s.Findings))
	hidden := 0
	for _, f := range s.Findings {
		file := filepath.ToSlash(f.File)
		ranges := rangesFor(changed, file)
		switch {
		case len(ranges) == 0:
			hidden++
		case f.RuleID == metrics.MetricFileLength:
			// The file's own file-length snapshot carries its total size;
			// a brand-new file has every line in the diff.
			if diff.Len(ranges) >= fileLen[file] {
				kept = append(kept, f)
			} else {
				hidden++
			}
		case diff.Intersects(ranges, f.StartLine, f.EndLine):
			kept = append(kept, f)
		default:
			hidden++
		}
	}

	out := *s
	out.Findings = kept
	return &out, hidden
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
