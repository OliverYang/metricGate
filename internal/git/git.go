// Package git is the git adapter of metricgate: it turns a working tree and a
// base ref into the changed-line ranges that -diff mode filters findings by.
//
// It is the only package in the project that shells out to git, and it is an
// adapter: the checker core depends on the pure range types in internal/diff,
// never on this package.
package git

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"metricgate/internal/diff"
)

var hunkRe = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// ResolveBase maps a user-facing base ref to the merge-base with HEAD, so a PR
// diff contains only the branch's own changes even when the target branch moved
// ahead. Falls back to the raw ref (e.g. HEAD) when merge-base fails.
//
// dir is the working directory for the git commands ("" = process cwd).
func ResolveBase(dir, base string) string {
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
func RepoRelative(checkRoot, path string) (string, bool) {
	repoRoot := Root(checkRoot)
	if repoRoot == "" {
		return "", false
	}
	checkBase, err := checkBaseDir(checkRoot)
	if err != nil {
		return "", false
	}
	joined, err := filepath.Rel(repoRoot, filepath.Join(checkBase, filepath.FromSlash(path)))
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
	root, err := run(path, "rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	return filepath.Clean(resolved)
}

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

// ArchivePaths exports only the named repo-relative paths at ref into a fresh
// temp directory, preserving their layout.
//
// It exists because the diff-aware filter only needs the base values of the
// files the change touched. Archiving the whole tree to answer a question
// about a handful of files costs a full second analysis of a repository that
// may be thousands of files.
//
// Paths that do not exist at ref (a file added by the change) are skipped by
// asking git which of them it knows, because `git archive` aborts the entire
// command on one unmatched pathspec. An empty result is not an error: it
// means every touched file is new, so there is nothing to compare against.
func ArchivePaths(dir, ref string, paths []string) (string, func(), error) {
	existing, err := treePaths(dir, ref, paths)
	if err != nil {
		return "", nil, err
	}
	if len(existing) == 0 {
		return "", func() {}, nil
	}
	args := append([]string{"archive", "--format=tar", ref, "--"}, existing...)
	data, err := gitBytes(dir, args...)
	if err != nil {
		return "", nil, fmt.Errorf("git archive %s: %w", ref, err)
	}
	tmp, err := os.MkdirTemp("", "metricgate-base-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(tmp) }
	if err := extractTar(data, tmp); err != nil {
		cleanup()
		return "", nil, err
	}
	return tmp, cleanup, nil
}

// treePaths filters paths down to the ones present in ref's tree. ls-tree
// tolerates unmatched pathspecs (it reports them by omission), which is what
// makes it the right pre-filter for ArchivePaths.
func treePaths(dir, ref string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	args := append([]string{"ls-tree", "-r", "--name-only", ref, "--"}, paths...)
	out, err := run(dir, args...)
	if err != nil {
		return nil, fmt.Errorf("git ls-tree %s: %w", ref, err)
	}
	return strings.Fields(out), nil
}

// Archive exports the tree at ref into a fresh temp directory (repo-relative
// layout preserved) so an adapter can parse the base revision. The caller owns
// the returned cleanup. Extraction uses the stdlib tar reader, so it does not
// depend on a system `tar` binary.
func Archive(dir, ref string) (string, func(), error) {
	data, err := gitBytes(dir, "archive", "--format=tar", ref)
	if err != nil {
		return "", nil, fmt.Errorf("git archive %s: %w", ref, err)
	}
	tmp, err := os.MkdirTemp("", "metricgate-base-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(tmp) }
	if err := extractTar(data, tmp); err != nil {
		cleanup()
		return "", nil, err
	}
	return tmp, cleanup, nil
}

// extractTar walks a tar stream and materialises each entry under dest. The
// top-level loop only steers control flow; per-entry materialisation lives
// in helpers so each function stays under the complexity gates.
func extractTar(data []byte, dest string) error {
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if err := materialiseTarEntry(tr, hdr, dest); err != nil {
			return err
		}
	}
}

// materialiseTarEntry skips symlinks / odd entries and writes either a
// directory or a regular file. Keeping the per-entry work in one place lets
// extractTar stay linear and easy to read.
func materialiseTarEntry(tr *tar.Reader, hdr *tar.Header, dest string) error {
	target := filepath.Join(dest, filepath.FromSlash(hdr.Name))
	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o755)
	case tar.TypeReg:
		return writeRegularTarFile(target, tr)
	}
	return nil
}

// writeRegularTarFile creates the parent directory, copies the entry body,
// and closes the file. Errors from any step propagate to the caller; the
// caller has already opened the archive, so a partial file is fine.
func writeRegularTarFile(target string, src io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.Create(target)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, src); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
