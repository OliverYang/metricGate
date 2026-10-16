package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"metricgate/internal/diff"
)

type ChangeMode string

const (
	ModeWorkspace ChangeMode = "workspace"
	ModeRange     ChangeMode = "range"
	ModeCommit    ChangeMode = "commit"
)

type ChangeRequest struct {
	Mode   ChangeMode
	From   string
	To     string
	Commit string
	Base   string // workspace comparison base; empty means HEAD
}

type Hunk struct {
	OldStart int      `json:"oldStart"`
	OldCount int      `json:"oldCount"`
	NewStart int      `json:"newStart"`
	NewCount int      `json:"newCount"`
	Added    []string `json:"added,omitempty"`
	Deleted  []string `json:"deleted,omitempty"`
}

type FileChange struct {
	Path      string `json:"path"` // current path, or old path for deletion
	OldPath   string `json:"oldPath,omitempty"`
	NewPath   string `json:"newPath,omitempty"`
	Status    string `json:"status"` // A/M/D/R/C/T
	Untracked bool   `json:"untracked,omitempty"`
	Hunks     []Hunk `json:"hunks,omitempty"`
}

// LoadChanges returns a deterministic change set for a workspace, merge-base
// range, or single commit. Git process execution stays in this adapter.
//
// dir may be any path inside the worktree (including a subdirectory or a
// single file): the query runs from the worktree root, because git reports
// root-relative paths and resolves pathspecs against the root, so a subdirectory
// cwd would silently match nothing.
func LoadChanges(dir string, req ChangeRequest) ([]FileChange, error) {
	root := gitDir(dir)
	normalized, err := resolveRequest(root, req)
	if err != nil {
		return nil, err
	}
	out, err := gitBytes(root, nameStatusArgs(normalized)...)
	if err != nil {
		return nil, changedPathsError(root, normalized, err)
	}
	changes, err := parseNameStatusZ(out)
	if err != nil {
		return nil, err
	}
	if normalized.Mode == ModeWorkspace {
		changes, err = addUntracked(root, changes)
		if err != nil {
			return nil, err
		}
	}
	if err := attachHunks(root, normalized, changes); err != nil {
		return nil, err
	}
	sortChanges(changes)
	return changes, nil
}

// attachHunks fills the hunks of a whole change set with ONE git process. The
// previous shape spawned `git diff -- <path>` per file, which on a 173-path
// dirty tree cost ~14 s of pure process startup — the bulk of the overhead that
// made `-diff` feel too expensive to run per edit. Untracked files are read
// from disk (their whole content is new) and need no git at all.
func attachHunks(root string, req ChangeRequest, changes []FileChange) error {
	if err := readUntrackedHunks(root, changes); err != nil {
		return err
	}
	if !hasTrackedChange(changes) {
		return nil
	}
	patch, err := gitBytes(root, patchArgs(req, nil)...)
	if err != nil {
		return fmt.Errorf("git diff %s: %w", patchTarget(req), err)
	}
	assignPatchHunks(changes, string(patch))
	return nil
}

// patchTarget names what the batched diff compares against, so a failure says
// which side of the diff broke rather than blaming one arbitrary file.
func patchTarget(req ChangeRequest) string {
	switch req.Mode {
	case ModeRange:
		return req.From + ".." + req.To
	case ModeCommit:
		return req.Commit
	default:
		if req.Base == "" {
			return "HEAD"
		}
		return req.Base
	}
}

func hasTrackedChange(changes []FileChange) bool {
	for i := range changes {
		if !changes[i].Untracked {
			return true
		}
	}
	return false
}

func readUntrackedHunks(root string, changes []FileChange) error {
	for i := range changes {
		if !changes[i].Untracked {
			continue
		}
		data, err := os.ReadFile(joinPath(root, changes[i].Path))
		if err != nil {
			return fmt.Errorf("read untracked %s: %w", changes[i].Path, err)
		}
		changes[i].Hunks = wholeFileHunk(string(data))
	}
	return nil
}

func resolveRequest(dir string, req ChangeRequest) (ChangeRequest, error) {
	if req.Mode == "" {
		req.Mode = ModeWorkspace
	}
	switch req.Mode {
	case ModeWorkspace:
	case ModeRange:
		if req.From == "" || req.To == "" {
			return req, fmt.Errorf("range mode requires from and to refs")
		}
		if base, err := gitBytes(dir, "merge-base", req.From, req.To); err == nil && len(bytes.TrimSpace(base)) > 0 {
			req.From = strings.TrimSpace(string(base))
		}
	case ModeCommit:
		if req.Commit == "" {
			return req, fmt.Errorf("commit mode requires commit ref")
		}
	default:
		return req, fmt.Errorf("unsupported change mode %q", req.Mode)
	}
	return req, nil
}

// changedPathsError explains a failed change-set query. "requires a git
// repository" is right for a missing or unreadable repository, but it is
// actively misleading for the two states that actually show up while wiring a
// pre-commit hook or CI: a repository whose HEAD has no commits yet, and a base
// ref this clone has never fetched. Both send the reader looking for a git
// installation that is already there, so both get their own wording.
func changedPathsError(dir string, req ChangeRequest, err error) error {
	if hint := baseRefHint(dir, req); hint != "" {
		return fmt.Errorf("git changed paths: %w — %s", err, hint)
	}
	return fmt.Errorf("git changed paths: %w (requires a git repository)", err)
}

// baseRefHint names the most likely reason `git diff <base>` failed, or "" when
// the repository itself is the problem. Each probe runs only on the error path,
// so a successful check pays nothing for the diagnosis.
func baseRefHint(dir string, req ChangeRequest) string {
	if req.Mode != ModeWorkspace {
		return ""
	}
	if !gitSucceeds(dir, "rev-parse", "--git-dir") {
		return "" // not a repository at all: the generic message is the right one
	}
	if !gitSucceeds(dir, "rev-parse", "--verify", "--quiet", "HEAD") {
		return "HEAD has no commits yet, so there is nothing to diff against; make an initial commit, or run without -diff"
	}
	base := req.Base
	if base == "" {
		base = "HEAD"
	}
	if !gitSucceeds(dir, "rev-parse", "--verify", "--quiet", base+"^{commit}") {
		return fmt.Sprintf("%q is not a commit this repository knows about; run `git fetch` (CI needs fetch-depth 0), or pass -base explicitly", base)
	}
	return ""
}

func gitSucceeds(dir string, args ...string) bool {
	_, err := gitBytes(dir, args...)
	return err == nil
}

func nameStatusArgs(req ChangeRequest) []string {
	prefix := []string{"--literal-pathspecs"}
	switch req.Mode {
	case ModeRange:
		return append(prefix, "diff", "--name-status", "-z", "--find-renames", req.From, req.To)
	case ModeCommit:
		return []string{"diff-tree", "--root", "--no-commit-id", "-r", "--name-status", "-z", "--find-renames", req.Commit}
	default:
		base := req.Base
		if base == "" {
			base = "HEAD"
		}
		return append(prefix, "diff", "--name-status", "-z", "--find-renames", base)
	}
}

// patchArgs builds the diff query for a change set. --src-prefix/--dst-prefix
// are pinned because `diff.mnemonicPrefix` (c/ + w/) or `diff.noprefix` in a
// user's config would otherwise hand section paths back in a shape that
// splitPatchSections/patchSectionPaths cannot strip.
func patchArgs(req ChangeRequest, paths []string) []string {
	var args []string
	switch req.Mode {
	case ModeRange:
		args = []string{"--literal-pathspecs", "diff", "--no-ext-diff", "--no-color", "--unified=0", "--src-prefix=a/", "--dst-prefix=b/", "--find-renames", req.From, req.To, "--"}
	case ModeCommit:
		// show handles root commits as well as ordinary parented commits.
		args = []string{"--literal-pathspecs", "show", "--format=", "--no-ext-diff", "--no-color", "--unified=0", "--src-prefix=a/", "--dst-prefix=b/", "--find-renames", req.Commit, "--"}
	default:
		base := req.Base
		if base == "" {
			base = "HEAD"
		}
		args = []string{"--literal-pathspecs", "diff", "--no-ext-diff", "--no-color", "--unified=0", "--src-prefix=a/", "--dst-prefix=b/", "--find-renames", base, "--"}
	}
	return append(args, paths...)
}

func addUntracked(dir string, changes []FileChange) ([]FileChange, error) {
	out, err := gitBytes(dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("git list untracked files: %w", err)
	}
	for _, path := range nulFields(out) {
		if path != "" {
			changes = append(changes, FileChange{Path: path, NewPath: path, Status: "A", Untracked: true})
		}
	}
	return changes, nil
}

// parseNameStatusZ walks git's `--name-status -z` output. The loop only
// dispatches on the status code; the rename/copy and plain-status branches
// each live in their own helper so this function stays flat under the
// cognitive-complexity gate.
func parseNameStatusZ(data []byte) ([]FileChange, error) {
	fields := nulFields(data)
	var out []FileChange
	for i := 0; i < len(fields); {
		status := fields[i]
		i++
		if status == "" {
			continue
		}
		change, next, err := parseZRecord(status, fields, i)
		if err != nil {
			return nil, err
		}
		i = next
		out = append(out, change)
	}
	return out, nil
}

// parseZRecord turns one (status, rest-of-fields) pair into a FileChange
// and reports how many fields it consumed. Rename/copy records take two
// path slots; everything else takes one. The helper centralises the two
// error paths so the loop above stays one-directional.
func parseZRecord(status string, fields []string, i int) (FileChange, int, error) {
	code := status[:1]
	switch {
	case code == "R" || code == "C":
		if i+1 >= len(fields) {
			return FileChange{}, i, fmt.Errorf("malformed NUL name-status record %q", status)
		}
		oldPath, newPath := fields[i], fields[i+1]
		if oldPath == "" || newPath == "" {
			return FileChange{}, i, fmt.Errorf("empty path in NUL name-status record %q", status)
		}
		return FileChange{Path: newPath, OldPath: oldPath, NewPath: newPath, Status: code}, i + 2, nil
	default:
		return parseZPlain(status, code, fields, i)
	}
}

// parseZPlain handles non-rename/copy records: a single path slot, with
// the empty old/new path conventions for added and deleted entries.
func parseZPlain(status, code string, fields []string, i int) (FileChange, int, error) {
	if i >= len(fields) {
		return FileChange{}, i, fmt.Errorf("missing path after status %q", status)
	}
	path := fields[i]
	if path == "" {
		return FileChange{}, i, fmt.Errorf("empty path after status %q", status)
	}
	oldPath, newPath := path, path
	if code == "A" {
		oldPath = ""
	}
	if code == "D" {
		newPath = ""
	}
	return FileChange{Path: path, OldPath: oldPath, NewPath: newPath, Status: code}, i + 1, nil
}
func nulFields(data []byte) []string {
	parts := bytes.Split(data, []byte{0})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, string(p))
	}
	return out
}

// ChangedLinesFromChanges projects a change set onto the legacy new-side
// ranges consumed by Engine.FilterSummary. Pure deletions have no new range.
func ChangedLinesFromChanges(changes []FileChange) map[string][]diff.Range {
	out := map[string][]diff.Range{}
	for _, c := range changes {
		if c.NewPath == "" {
			continue
		}
		for _, h := range c.Hunks {
			if h.NewCount > 0 {
				out[c.NewPath] = append(out[c.NewPath], diff.Range{Start: h.NewStart, End: h.NewStart + h.NewCount - 1})
			}
		}
	}
	return out
}

// gitDir resolves dir to the worktree root so git queries always run from the
// repository root. A path outside any worktree is returned unchanged, which
// keeps the "not a git repository" error the caller expects.
func gitDir(dir string) string {
	if root := Root(dir); root != "" {
		return root
	}
	return dir
}

// gitBytes runs git in dir ("" = process cwd).
func gitBytes(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.Output()
}

func sortChanges(changes []FileChange) {
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].Path != changes[j].Path {
			return changes[i].Path < changes[j].Path
		}
		return changes[i].Status < changes[j].Status
	})
}
