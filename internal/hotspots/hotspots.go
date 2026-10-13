// Package hotspots ranks files by change frequency weighted by
// maintainability debt — the "hotspot analysis" of Feathers (2004) and
// Tornhill's "Your Code as a Crime Scene" (2015): code that changes often
// AND scores badly on a maintainability metric is where defects cluster,
// and it is what an LLM should read (and be most careful in) first.
//
// Score per file, 0..100:
//
//	churnNorm = churn / maxChurn           (churn = added+deleted lines)
//	debt      = 100 - MI(worst function)   (0..100, see metrics.MI)
//	score     = churnNorm * debt
//
// A file must have nonzero churn to appear at all: never-changed code is
// not a maintenance risk regardless of its MI.
package hotspots

import (
	"fmt"
	"sort"
	"strconv"

	"metricgate/internal/git"
)

// FileHotspot is one ranked file.
type FileHotspot struct {
	File  string  `json:"file"`
	Churn int     `json:"churn"` // added+deleted lines since --since
	MI    float64 `json:"mi"`    // worst-function maintainability (0 = not measured)
	Risk  float64 `json:"risk"`  // static risk score 0..100 (0 = not supplied)
	Score float64 `json:"score"` // churnNorm * (100-MI)
}

// Options configures one hotspot run.
type Options struct {
	Since string // git --since expression ("3.months ago"); empty = all history
	Root  string // working directory for git ("" = cwd)
	MIs   map[string]float64
	// Risk is the optional static risk score per repo-relative path (see
	// internal/risk). It annotates a hotspot with how much is already wrong
	// with the file; it does not change the churn-based ordering.
	Risk map[string]float64
	Max  int // report top N (0 = 10)
	// AllowShallow opts in to reporting churn from a shallow clone. Without
	// it a shallow clone is refused: its truncated history makes every churn
	// number silently too small, and a quietly undercounted ranking is worse
	// than no ranking.
	AllowShallow bool
}

// churnOf folds per-commit file stats into per-file churn. Binary files carry
// no line counts and are skipped, exactly as the raw numstat line would have
// been.
func churnOf(commits []git.Commit) map[string]int {
	churn := map[string]int{}
	for _, c := range commits {
		for _, f := range c.Files {
			if f.Binary || f.Path == "" {
				continue
			}
			churn[f.Path] += f.Added + f.Deleted
		}
	}
	return churn
}

// Compute ranks the files. MI lookups are path-exact (both sides use the
// repo-relative path); files without an MI entry still rank by churn with
// debt capped at the worst case (100), which keeps new, unmeasured files
// visible without inventing a measurement.
func Compute(opts Options) ([]FileHotspot, error) {
	if !opts.AllowShallow && git.IsShallow(opts.Root) {
		return nil, fmt.Errorf("shallow clone: git history is truncated, so churn would be undercounted (clone with full history, or pass -allow-shallow to report the partial numbers)")
	}
	commits, err := git.History(opts.Root, opts.Since)
	if err != nil {
		return nil, err
	}
	return scoreFiles(churnOf(commits), opts.MIs, opts.Risk, opts.Max), nil
}

// scoreFiles is the pure scoring half of Compute, factored so the exact
// formula can be checked without a git subprocess.
func scoreFiles(churn map[string]int, mis, risk map[string]float64, max int) []FileHotspot {
	maxChurn := 0
	for _, c := range churn {
		if c > maxChurn {
			maxChurn = c
		}
	}
	if maxChurn == 0 {
		return nil
	}

	hotspots := make([]FileHotspot, 0, len(churn))
	for file, c := range churn {
		debt := 100.0
		if mi, ok := mis[file]; ok && mi > 0 {
			debt = 100 - mi
		}
		score := float64(c) / float64(maxChurn) * debt
		hotspots = append(hotspots, FileHotspot{File: file, Churn: c, MI: mis[file], Risk: risk[file], Score: score})
	}
	return sortAndLimit(hotspots, max)
}

// SinceDays is a helper: "now minus n days" as a git --since expression.
func SinceDays(n int) string {
	return strconv.Itoa(n) + ".days ago"
}

// sortAndLimit stabilises the ranking and truncates it to the requested N.
func sortAndLimit(hotspots []FileHotspot, max int) []FileHotspot {
	sort.Slice(hotspots, func(i, j int) bool {
		if hotspots[i].Score != hotspots[j].Score {
			return hotspots[i].Score > hotspots[j].Score
		}
		return hotspots[i].File < hotspots[j].File
	})
	if max <= 0 {
		max = 10
	}
	if len(hotspots) > max {
		hotspots = hotspots[:max]
	}
	return hotspots
}
