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
