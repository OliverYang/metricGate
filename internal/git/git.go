// Package git is the git adapter of metricgate: it turns a working tree and a
// base ref into the changed-line ranges that -diff mode filters findings by.
//
// It is the only package in the project that shells out to git, and it is an
// adapter: the checker core depends on the pure range types in internal/diff,
// never on this package.
package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"metricgate/internal/diff"
)

var hunkRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// ResolveBase maps a user-facing base ref to the merge-base with HEAD, so a PR
// diff contains only the branch's own changes even when the target branch moved
// ahead. Falls back to the raw ref (e.g. HEAD) when merge-base fails.
//
// A base that already *is* HEAD needs no merge-base at all: the merge-base of
// HEAD with HEAD is HEAD, so the subprocess would only re-derive a value we
// hold in hand. That call used to fire twice per `-diff` run (here and in the
// baseline snapshot), and every git round trip on this machine costs 0.21-0.4 s
// of pure process spawning, so `-diff` paid ~0.5 s to prove a tautology.
//
// dir is the working directory for the git commands ("" = process cwd).
func ResolveBase(dir, base string) string {
	if base == "" || base == "HEAD" {
		return base
	}
	out, err := run(dir, "merge-base", base, "HEAD")
	if err != nil || strings.TrimSpace(out) == "" {
		return base
	}
	return strings.TrimSpace(out)
}

// ChangedLines returns the added-line ranges per file (repo-relative slash
// paths) between the base ref and the current working tree. Untracked but
// non-ignored files count as fully new. Reported paths are relative to the
// repository root whether or not dir was given.
func ChangedLines(dir, base string) (map[string][]diff.Range, error) {
	changes, err := LoadChanges(dir, ChangeRequest{Mode: ModeWorkspace, Base: base})
	if err != nil {
		return nil, err
	}
	return ChangedLinesFromChanges(changes), nil
}

// ChangedLinesFor returns added-line ranges keyed by paths relative to
// checkRoot, for any checkRoot inside a worktree (a directory, a subdirectory,
// or a single file). It exists because two coordinate systems meet here: git
// reports worktree-root-relative paths, while a report scopes its file paths to
// the check root. ChangedLines' repo-relative contract is unchanged for callers
// that want it; this wrapper rebases each path onto checkRoot so a
// `check <subdir> -diff` gate compares like with like instead of silently
// hiding every finding as pre-existing.
func ChangedLinesFor(checkRoot, base string) (map[string][]diff.Range, error) {
	repoRoot := Root(checkRoot)
	if repoRoot == "" {
		return nil, fmt.Errorf("%s is not inside a git worktree (a repository is required for -diff)", checkRoot)
	}
	changes, err := LoadChanges(repoRoot, ChangeRequest{Mode: ModeWorkspace, Base: ResolveBase(repoRoot, base)})
	if err != nil {
		return nil, err
	}
	checkBase, err := checkBaseDir(checkRoot)
	if err != nil {
		return nil, err
	}
	return rebaseChanges(changes, repoRoot, checkBase), nil
}

// checkBaseDir resolves the directory a check root reports paths against,
// in the same canonical space as Root: a file root reports from its parent,
// and symlinks are resolved so the two sides of a filepath.Rel agree.
//
// macOS is the reason the second step is not optional: Root returns
// /private/tmp/... while a plain filepath.Abs gives /tmp/..., and a Rel
// between those two has no common ancestor beyond /.
func checkBaseDir(checkRoot string) (string, error) {
	checkBase := checkRoot
	if info, err := os.Stat(checkRoot); err == nil && !info.IsDir() {
		checkBase = filepath.Dir(checkRoot)
	}
	abs, err := filepath.Abs(checkBase)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs, nil
}

// RepoRelative maps a check-root-relative path onto the worktree root, so a
// caller that has both coordinate systems (the diff keys and a base archive)
// can join them. It reports false when the path is not inside the worktree.
//
// A caller with many paths should build a RepoRelativizer once instead: this
// resolves the two coordinate systems per path, which on a 181-path dirty tree
// meant one `rev-parse --show-toplevel` process per changed file.
func RepoRelative(checkRoot, path string) (string, bool) {
	rel, ok := NewRepoRelativizer(checkRoot)
	if !ok {
		return "", false
	}
	return rel.Rel(path)
}

// RepoRelativizer holds the two coordinate systems a -diff report needs: the
// worktree root git speaks, and the check root the report is scoped to.
// Constructing it resolves both once; Rel itself is pure path math.
type RepoRelativizer struct {
	repoRoot  string
	checkBase string
}

// NewRepoRelativizer resolves the worktree root and the check base for
// checkRoot. It reports false when checkRoot is not inside a readable Git
// worktree, the same condition RepoRelative reports as false per path.
func NewRepoRelativizer(checkRoot string) (RepoRelativizer, bool) {
	repoRoot := Root(checkRoot)
	if repoRoot == "" {
		return RepoRelativizer{}, false
	}
	checkBase, err := checkBaseDir(checkRoot)
	if err != nil {
		return RepoRelativizer{}, false
	}
	return RepoRelativizer{repoRoot: repoRoot, checkBase: checkBase}, true
}

// Rel maps one check-root-relative path onto the worktree root, reporting
// false when the path escapes the worktree.
func (r RepoRelativizer) Rel(path string) (string, bool) {
	joined, err := filepath.Rel(r.repoRoot, filepath.Join(r.checkBase, filepath.FromSlash(path)))
	if err != nil || filepath.IsAbs(joined) || joined == ".." || strings.HasPrefix(joined, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(joined), true
}

// rebaseChanges rewrites worktree-root-relative change paths into paths
// relative to checkBase, dropping anything outside the check scope. Paths use
// forward slashes so they match the engine's report paths on every platform.
func rebaseChanges(changes []FileChange, repoRoot, checkBase string) map[string][]diff.Range {
	out := map[string][]diff.Range{}
	for _, c := range changes {
		rel, ok := rebaseOne(c.NewPath, repoRoot, checkBase)
		if !ok {
			continue
		}
		for _, h := range c.Hunks {
			if h.NewCount > 0 {
				out[rel] = append(out[rel], diff.Range{Start: h.NewStart, End: h.NewStart + h.NewCount - 1})
			}
		}
	}
	return out
}

// rebaseOne maps one worktree-relative path onto checkBase, reporting false
// when the path escapes the check scope (a sibling subtree, or a root that is
// not the worktree itself).
func rebaseOne(rel, repoRoot, checkBase string) (string, bool) {
	if rel == "" {
		return "", false
	}
	joined, err := filepath.Rel(checkBase, filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil || filepath.IsAbs(joined) || joined == ".." || strings.HasPrefix(joined, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(joined), true
}

// Root returns the canonical Git worktree root containing path, or "" when
// path is not inside a readable Git worktree.
//
// Successful answers are memoised for the life of the process: this is the one
// git call in the package that cannot be batched, and -diff used to reach it
// once per changed file (172 `rev-parse --show-toplevel` processes, ~21 s of
// pure process spawning on a 173-path tree). Only non-empty answers are cached,
// so a directory that is not a worktree yet does not stay a miss — that keeps
// `git init` inside a long-lived process working, and keeps the failure mode
// loud rather than a silently stale root. The accepted trade-off is that a
// worktree whose root *moves* under a running process keeps being reported at
// its old path; every git command downstream would fail there anyway.
func Root(path string) string {
	if path == "" {
		path = "."
	}
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if !info.IsDir() {
		path = filepath.Dir(path)
	}
	key := filepath.Clean(path)
	rootMu.Lock()
	cached, hit := rootCache[key]
	rootMu.Unlock()
	if hit {
		return cached
	}
	root, err := run(path, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	clean := filepath.Clean(resolved)
	rootMu.Lock()
	rootCache[key] = clean
	rootMu.Unlock()
	return clean
}

var (
	rootMu    sync.Mutex
	rootCache = map[string]string{}
)

// parseDiff collects the "+"-side hunk ranges of unified diff text. The
// per-line decisions (filename tracker, hunk header matcher) live in a
// helper so this loop stays flat under the cognitive-complexity gate.
func parseDiff(d string, changed map[string][]diff.Range) {
	cur := ""
	for _, line := range strings.Split(d, "\n") {
		if strings.HasPrefix(line, "+++ b/") {
			cur = strings.TrimPrefix(line, "+++ b/")
			continue
		}
		if !strings.HasPrefix(line, "@@") || cur == "" {
			continue
		}
		recordDiffHunk(cur, line, changed)
	}
}

// run runs a git command and returns trimmed stdout.

// recordDiffHunk appends one hunk's "+" range to the changed map when the
// header parses and the new-side count is non-zero. A malformed header is
// dropped silently: the next hunk in the same file still gets recorded.
func recordDiffHunk(file, header string, changed map[string][]diff.Range) {
	m := hunkRe.FindStringSubmatch(header)
	if m == nil {
		return
	}
	start, _ := strconv.Atoi(m[1])
	count := 1
	if m[2] != "" {
		count, _ = strconv.Atoi(m[2])
	}
	if count > 0 {
		changed[file] = append(changed[file], diff.Range{Start: start, End: start + count - 1})
	}
}
func run(dir string, args ...string) (string, error) {
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
