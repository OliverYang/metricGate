// Package configpath resolves which policy file a command should read.
//
// A fixed ".metricgate.yaml" relative to the process working directory is a trap:
// `check /other/repo` would either read the wrong file or silently fall back to
// the built-in defaults. This package turns the policy location into an
// explicit, testable rule — a search that starts at the root being analysed and
// stops at the repository boundary:
//
//	.config flag (explicit, always wins)
//	<root>/.metricgate.yaml
//	<root>/.metricgate/config.yaml
//	<root>/policy.yaml (legacy spelling)
//	…the same names, one directory up, until the git root
//
// The first file that exists wins and the walk stops there: two documents are
// never merged by accident, so "which values are in force" always has one
// answer. Composition stays explicit through `extends:`, which the caller sees
// in cfg.Extends().
package configpath

import (
	"os"
	"path/filepath"
	"strings"

	"metricgate/internal/git"
)

// CandidateNames are the policy file names searched at each directory level,
// most specific first. The dot-prefixed forms are the recommended home; the
// bare legacy policy.yaml keeps every existing repository working unchanged —
// it is deliberately last, so a repository that has both reads the dot form.
var CandidateNames = []string{".metricgate.yaml", filepath.Join(".metricgate", "config.yaml"), "policy.yaml"}

// MaxSearchDepth caps the walk so a pathological tree cannot spin forever.
const MaxSearchDepth = 32

// Found reports where the policy for root came from. Empty Path means nothing
// was found and the caller should use the built-in defaults.
type Found struct {
	// Path is the policy file to read, relative to the process working
	// directory or absolute — exactly the form that os.ReadFile accepts.
	Path string `json:"path"`
	// Name is the candidate that matched, for display (".metricgate.yaml").
	Name string `json:"name"`
	// Root is the directory the search started from, so a UI can explain
	// the decision back to the user.
	Root string `json:"root"`
	// StoppedAt is the directory the search stopped at: the git root when
	// one exists, otherwise the start directory.
	StoppedAt string `json:"stoppedAt"`
	// Tried lists every candidate that did not exist, oldest first, so a
	// trace explains why nothing was found.
	Tried []string `json:"tried,omitempty"`
}

// FoundAny reports whether a policy file was located.
func (f Found) FoundAny() bool { return f.Path != "" }

// Display renders one line describing the decision.
func (f Found) Display() string {
	if !f.FoundAny() {
		return "no policy file found; using built-in defaults"
	}
	return f.Name + " at " + f.Path
}

// Discover searches for a policy file starting at root.
//
// Explicit paths (the -config flag) are returned as Found with only Path set,
// without an existence check: a caller that names a file wants the read error,
// not a silent downgrade to defaults.
func Discover(explicit, root string) Found {
	if explicit != "" {
		return Found{Path: explicit}
	}
	start := startDir(root)
	stop := stopDir(root, start)
	found := Found{Root: start, StoppedAt: stop}
	// The walk compares directory strings, so both ends must be absolute: a
	// relative "." has no parent (filepath.Dir(".") == "."), which would stop
	// the search after one level and hide a repository's own policy from every
	// command started inside a subdirectory.
	start = abs(start)
	stop = abs(stop)
	found.Root, found.StoppedAt = start, stop
	dir := start
	for depth := 0; depth < MaxSearchDepth; depth++ {
		for _, name := range CandidateNames {
			candidate := filepath.Join(dir, name)
			if isFile(candidate) {
				found.Path = candidate
				found.Name = name
				return found
			}
			found.Tried = append(found.Tried, candidate)
		}
		if dir == stop {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return found
}

// startDir normalizes the search origin: an empty root means the current
// directory, and a file root means the file's directory. A path that does not
// exist (a typo, a file the user is about to create) is treated as a file so
// the search still lands in the repository it was typed inside.
func startDir(root string) string {
	if root == "" {
		root = "."
	}
	info, err := os.Stat(root)
	if err != nil {
		return filepath.Dir(root)
	}
	if !info.IsDir() {
		return filepath.Dir(root)
	}
	return root
}

// stopDir is the highest directory the search may reach: the git root when the
// start is inside a worktree, otherwise the start itself. Stopping at the
// repository boundary keeps a monorepo's parent directory from supplying
// policy to a nested repository.
func stopDir(root, start string) string {
	if repo := git.Root(root); repo != "" {
		if resolved, err := filepath.EvalSymlinks(repo); err == nil {
			repo = resolved
		}
		if sameDir(repo, start) || isUnder(repo, start) || isUnder(start, repo) {
			return repo
		}
		// The start and the worktree disagree (a symlinked or deleted
		// path): trust the worktree root so the boundary stays honest.
		return repo
	}
	return start
}

// sameDir reports whether two paths denote the same directory, tolerating
// spelling differences that do not change the target.
func sameDir(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// isUnder reports whether child is inside parent (or equals it).
func isUnder(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// abs makes a path absolute without letting a failure change the answer: on
// error the original spelling is kept so the reported paths stay recognisable.
func abs(path string) string {
	if out, err := filepath.Abs(path); err == nil {
		return out
	}
	return path
}

// isFile reports whether path names a regular file.
func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
