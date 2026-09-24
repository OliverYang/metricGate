package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
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
func LoadChanges(dir string, req ChangeRequest) ([]FileChange, error) {
	normalized, err := resolveRequest(dir, req)
	if err != nil {
		return nil, err
	}
	out, err := gitBytes(dir, nameStatusArgs(normalized)...)
	if err != nil {
		return nil, fmt.Errorf("git changed paths: %w (requires a git repository)", err)
	}
	changes, err := parseNameStatusZ(out)
	if err != nil {
		return nil, err
	}
	if normalized.Mode == ModeWorkspace {
		changes, err = addUntracked(dir, changes)
		if err != nil {
			return nil, err
		}
	}
	for i := range changes {
		if changes[i].Untracked {
			data, readErr := os.ReadFile(joinPath(dir, changes[i].Path))
			if readErr != nil {
				return nil, fmt.Errorf("read untracked %s: %w", changes[i].Path, readErr)
			}
			changes[i].Hunks = wholeFileHunk(string(data))
			continue
		}
		paths := changePaths(changes[i])
		args := patchArgs(normalized, paths)
		patch, patchErr := gitBytes(dir, args...)
		if patchErr != nil {
			return nil, fmt.Errorf("git diff %s: %w", changes[i].Path, patchErr)
		}
		changes[i].Hunks = parseUnifiedHunks(string(patch))
	}
	sortChanges(changes)
	return changes, nil
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

func patchArgs(req ChangeRequest, paths []string) []string {
	var args []string
	switch req.Mode {
	case ModeRange:
		args = []string{"--literal-pathspecs", "diff", "--no-ext-diff", "--no-color", "--unified=0", "--find-renames", req.From, req.To, "--"}
	case ModeCommit:
		// show handles root commits as well as ordinary parented commits.
		args = []string{"--literal-pathspecs", "show", "--format=", "--no-ext-diff", "--no-color", "--unified=0", "--find-renames", req.Commit, "--"}
	default:
		base := req.Base
		if base == "" {
			base = "HEAD"
		}
		args = []string{"--literal-pathspecs", "diff", "--no-ext-diff", "--no-color", "--unified=0", "--find-renames", base, "--"}
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

func changePaths(c FileChange) []string {
	var paths []string
	if c.OldPath != "" {
		paths = append(paths, c.OldPath)
	}
	if c.NewPath != "" && c.NewPath != c.OldPath {
		paths = append(paths, c.NewPath)
	}
	if len(paths) == 0 {
		paths = append(paths, c.Path)
	}
	return paths
}

func parseNameStatusZ(data []byte) ([]FileChange, error) {
	fields := nulFields(data)
	var out []FileChange
	for i := 0; i < len(fields); {
		status := fields[i]
		i++
		if status == "" {
			continue
		}
		code := status[:1]
		if code == "R" || code == "C" {
			if i+1 >= len(fields) {
				return nil, fmt.Errorf("malformed NUL name-status record %q", status)
			}
			oldPath, newPath := fields[i], fields[i+1]
			i += 2
			if oldPath == "" || newPath == "" {
				return nil, fmt.Errorf("empty path in NUL name-status record %q", status)
			}
			out = append(out, FileChange{Path: newPath, OldPath: oldPath, NewPath: newPath, Status: code})
			continue
		}
		if i >= len(fields) {
			return nil, fmt.Errorf("missing path after status %q", status)
		}
		path := fields[i]
		i++
		if path == "" {
			return nil, fmt.Errorf("empty path after status %q", status)
		}
		oldPath, newPath := path, path
		if code == "A" {
			oldPath = ""
		}
		if code == "D" {
			newPath = ""
		}
		out = append(out, FileChange{Path: path, OldPath: oldPath, NewPath: newPath, Status: code})
	}
	return out, nil
}

func nulFields(data []byte) []string {
	parts := bytes.Split(data, []byte{0})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, string(p))
	}
	return out
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func parseUnifiedHunks(patch string) []Hunk {
	var out []Hunk
	var current *Hunk
	for _, line := range strings.Split(patch, "\n") {
		if m := hunkHeader.FindStringSubmatch(line); m != nil {
			oldStart, _ := strconv.Atoi(m[1])
			newStart, _ := strconv.Atoi(m[3])
			oldCount, newCount := 1, 1
			if m[2] != "" {
				oldCount, _ = strconv.Atoi(m[2])
			}
			if m[4] != "" {
				newCount, _ = strconv.Atoi(m[4])
			}
			out = append(out, Hunk{OldStart: oldStart, OldCount: oldCount, NewStart: newStart, NewCount: newCount})
			current = &out[len(out)-1]
			continue
		}
		if current == nil || line == "" || strings.HasPrefix(line, "\\") {
			continue
		}
		switch line[0] {
		case '+':
			current.Added = append(current.Added, strings.TrimPrefix(line, "+"))
		case '-':
			current.Deleted = append(current.Deleted, strings.TrimPrefix(line, "-"))
		}
	}
	return out
}

func wholeFileHunk(src string) []Hunk {
	lines := strings.Split(src, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}
	return []Hunk{{NewStart: 1, NewCount: len(lines), Added: lines}}
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
