// Package diff resolves which lines of which files changed relative to a
// git base ref. It powers -diff mode: gates that only fail on new
// violations, so legacy codebases can adopt the tool without a big-bang
// cleanup.
package diff

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Range is an inclusive 1-based line range of ADDED lines in the current
// working tree.
type Range struct {
	Start, End int
}

// Intersects reports whether [start,end] overlaps any of the ranges.
func Intersects(ranges []Range, start, end int) bool {
	for _, r := range ranges {
		if start <= r.End && r.Start <= end {
			return true
		}
	}
	return false
}

// Len sums the number of lines covered by the ranges.
func Len(ranges []Range) int {
	n := 0
	for _, r := range ranges {
		n += r.End - r.Start + 1
	}
	return n
}

var hunkRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// ResolveBase maps a user-facing base ref to the merge-base with HEAD, so a
// PR diff contains only the branch's own changes even when the target branch
// moved ahead. Falls back to the raw ref (e.g. HEAD) when merge-base fails.
func ResolveBase(dir, base string) string {
	out, err := git(dir, "merge-base", base, "HEAD")
	if err != nil || strings.TrimSpace(out) == "" {
		return base
	}
	return strings.TrimSpace(out)
}

// ChangedLines returns the added-line ranges per file (repo-relative slash
// paths) between the base ref and the current working tree. Untracked but
// non-ignored files count as fully new. dir is the working directory for the
// git commands ("" = process working directory); reported paths are relative
// to the repository root either way.
func ChangedLines(dir, base string) (map[string][]Range, error) {
	out, err := git(dir, "diff", "--unified=0", "--no-color", base)
	if err != nil {
		// git diff exits 1 when differences exist — that is not a failure.
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			return nil, fmt.Errorf("git diff %s: %w (diff mode requires a git repository)", base, err)
		}
	}
	changed := map[string][]Range{}
	parseDiff(out, changed)

	if listing, err := git(dir, "ls-files", "--others", "--exclude-standard"); err == nil {
		for _, f := range strings.Split(listing, "\n") {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			if n := countLines(joinPath(dir, f)); n > 0 {
				changed[f] = append(changed[f], Range{Start: 1, End: n})
			}
		}
	}
	return changed, nil
}

func parseDiff(d string, changed map[string][]Range) {
	cur := ""
	for _, line := range strings.Split(d, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			cur = strings.TrimPrefix(line, "+++ b/")
		case strings.HasPrefix(line, "@@"):
			if cur == "" {
				continue
			}
			if m := hunkRe.FindStringSubmatch(line); m != nil {
				start, _ := strconv.Atoi(m[1])
				count := 1
				if m[2] != "" {
					count, _ = strconv.Atoi(m[2])
				}
				if count > 0 {
					changed[cur] = append(changed[cur], Range{Start: start, End: start + count - 1})
				}
			}
		}
	}
}

// git runs a git command and returns trimmed stdout. dir "" means the
// process working directory.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

func countLines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := bytes.Count(data, []byte{'\n'})
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}
