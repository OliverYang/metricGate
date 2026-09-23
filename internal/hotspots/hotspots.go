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
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// FileHotspot is one ranked file.
type FileHotspot struct {
	File  string  `json:"file"`
	Churn int     `json:"churn"` // added+deleted lines since --since
	MI    float64 `json:"mi"`    // worst-function maintainability (0 = not measured)
	Score float64 `json:"score"` // churnNorm * (100-MI)
}

// Options configures one hotspot run.
type Options struct {
	Since string // git --since expression ("3.months ago"); empty = all history
	Root  string // working directory for git ("" = cwd)
	MIs   map[string]float64
	Max   int // report top N (0 = 10)
}

// gitLog runs `git log --numstat --since` and returns its output.
func gitLog(opts Options) (string, error) {
	args := []string{"log", "--numstat", "--no-renames", "--format=%x01"}
	if opts.Since != "" {
		args = append(args, "--since", opts.Since)
	}
	cmd := exec.Command("git", args...)
	if opts.Root != "" {
		cmd.Dir = opts.Root
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git log: %w (is this a git repository?)", err)
	}
	return string(out), nil
}

// parseNumstat folds --numstat output into per-file churn. Commits are
// separated by the %x01 record separator; each stat line is
// "added\tdeleted\tpath". Binary files report "-" and are skipped.
func parseNumstat(out string) map[string]int {
	churn := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		if line == "" || line == "\x01" || strings.HasPrefix(line, "\x01") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		added, errA := strconv.Atoi(parts[0])
		deleted, errD := strconv.Atoi(parts[1])
		if errA != nil || errD != nil {
			continue // binary ("-") or rename line
		}
		path := parts[len(parts)-1]
		if path == "" {
			continue
		}
		churn[path] += added + deleted
	}
	return churn
}

// Compute ranks the files. MI lookups are path-exact (both sides use the
// repo-relative path); files without an MI entry still rank by churn with
// debt capped at the worst case (100), which keeps new, unmeasured files
// visible without inventing a measurement.
func Compute(opts Options) ([]FileHotspot, error) {
	out, err := gitLog(opts)
	if err != nil {
		return nil, err
	}
	churn := parseNumstat(out)

	maxChurn := 0
	for _, c := range churn {
		if c > maxChurn {
			maxChurn = c
		}
	}
	if maxChurn == 0 {
		return nil, nil
	}

	hotspots := make([]FileHotspot, 0, len(churn))
	for file, c := range churn {
		debt := 100.0
		if mi, ok := opts.MIs[file]; ok && mi > 0 {
			debt = 100 - mi
		}
		score := float64(c) / float64(maxChurn) * debt
		hotspots = append(hotspots, FileHotspot{File: file, Churn: c, MI: opts.MIs[file], Score: score})
	}
	return sortAndLimit(hotspots, opts.Max), nil
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
