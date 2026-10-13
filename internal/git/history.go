package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FileStat is one file's line change inside a commit. A binary file reports
// no line counts, so Added and Deleted stay 0 and Binary is set.
type FileStat struct {
	Path    string
	Added   int
	Deleted int
	Binary  bool
}

// Commit is one commit's changed files. Order matches `git log`: newest
// commit first, and within a commit the order git reported the stats.
type Commit struct {
	Files []FileStat
}

// History returns the per-commit file changes under dir, optionally limited
// to a `git log --since` expression ("" = all history).
//
// It is the single history source: the churn ranking (hotspots) and the
// co-change analysis (coupling) both read the same facts from one
// `git log --numstat` call, so the two views cannot disagree about what
// changed. Renames are not followed (--no-renames) because a rename line
// would otherwise hide the deleted path from the churn totals.
func History(dir, since string) ([]Commit, error) {
	out, err := historyLog(dir, since)
	if err != nil {
		return nil, err
	}
	return parseHistory(out), nil
}

// historyLog runs the numstat log. The record separator (0x01) is the commit
// delimiter, so one parse pass yields both the per-commit grouping and the
// per-file line counts.
func historyLog(dir, since string) (string, error) {
	args := []string{"log", "--numstat", "--no-renames", "--format=%x01"}
	if since != "" {
		args = append(args, "--since", since)
	}
	out, err := run(dir, args...)
	if err != nil {
		return "", fmt.Errorf("git log: %w (is this a git repository?)", err)
	}
	return out, nil
}

// parseHistory folds numstat output into commits. A commit block starts at
// each 0x01 separator; every following non-blank line is
// "added\tdeleted\tpath", where a binary file reports "-" for both counts.
func parseHistory(out string) []Commit {
	var commits []Commit
	for _, block := range strings.Split(out, "\x01") {
		if c, ok := parseCommit(block); ok {
			commits = append(commits, c)
		}
	}
	return commits
}

// parseCommit reads one commit block. It reports ok=false for a block with no
// usable stat line, which keeps empty commits (and the text before the first
// separator) out of the history.
func parseCommit(block string) (Commit, bool) {
	var c Commit
	for _, line := range strings.Split(block, "\n") {
		if s, ok := parseStatLine(line); ok {
			c.Files = append(c.Files, s)
		}
	}
	return c, len(c.Files) > 0
}

// parseStatLine reads one "added\tdeleted\tpath" line. Merge commits and
// rename lines carry no usable counts and are skipped rather than guessed.
func parseStatLine(line string) (FileStat, bool) {
	if line == "" {
		return FileStat{}, false
	}
	parts := strings.Split(line, "\t")
	if len(parts) < 3 {
		return FileStat{}, false
	}
	path := parts[len(parts)-1]
	if path == "" {
		return FileStat{}, false
	}
	added, errA := strconv.Atoi(parts[0])
	deleted, errD := strconv.Atoi(parts[1])
	if errA != nil || errD != nil {
		return FileStat{Path: path, Binary: true}, true
	}
	return FileStat{Path: path, Added: added, Deleted: deleted}, true
}

// IsShallow reports whether dir is a shallow clone. A shallow clone carries
// truncated history, so any churn or co-change number derived from it is
// silently too small — callers that report history statistics must say so
// instead of presenting a partial count as the truth.
func IsShallow(dir string) bool {
	if out, err := run(dir, "rev-parse", "--is-shallow-repository"); err == nil {
		return strings.TrimSpace(out) == "true"
	}
	// Older git has no --is-shallow-repository; the marker file is the same
	// signal it would have reported.
	root, err := run(dir, "rev-parse", "--git-dir")
	if err != nil {
		return false
	}
	if !filepath.IsAbs(root) && dir != "" {
		root = filepath.Join(dir, root)
	}
	_, err = os.Stat(filepath.Join(root, "shallow"))
	return err == nil
}
