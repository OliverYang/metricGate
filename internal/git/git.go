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

// parseDiff collects the "+"-side hunk ranges of unified diff text.
func parseDiff(d string, changed map[string][]diff.Range) {
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
					changed[cur] = append(changed[cur], diff.Range{Start: start, End: start + count - 1})
				}
			}
		}
	}
}

// run runs a git command and returns trimmed stdout.
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
		// Skip links/odd entries; only materialise regular files and dirs.
		target := filepath.Join(dest, filepath.FromSlash(hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
}
