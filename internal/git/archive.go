package git

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Base-revision snapshots for the diff-aware metric comparison: `git archive`
// the touched paths into a temp tree, then read the tar with the stdlib so no
// system `tar` binary is needed.
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
