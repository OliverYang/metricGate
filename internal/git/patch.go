package git

import (
	"regexp"
	"strconv"
	"strings"
)

// Patch splitting for the batched `git diff` in changes.go. One patch covers every
// changed file, so the work of attributing hunks to files lives here: cut at each
// `diff --git ` header, decode the path fields (git C-quotes some of them), and
// match them against the verbatim `--name-status -z` paths.

// assignPatchHunks splits one patch into per-file sections and gives each
// section's hunks to the change its paths name. Matching uses the verbatim
// `--name-status -z` paths, so nothing depends on git's quoting rules beyond
// decoding the path fields the section itself carries.
func assignPatchHunks(changes []FileChange, patch string) {
	index := changePathIndex(changes)
	if index == nil {
		return
	}
	for _, section := range splitPatchSections(patch) {
		hunks := parseUnifiedHunks(section)
		if len(hunks) == 0 {
			continue
		}
		if i, ok := lookupChange(index, section); ok {
			changes[i].Hunks = hunks
		}
	}
}

func changePathIndex(changes []FileChange) map[string]int {
	if len(changes) == 0 {
		return nil
	}
	index := make(map[string]int, 3*len(changes))
	for i := range changes {
		for _, path := range []string{changes[i].Path, changes[i].OldPath, changes[i].NewPath} {
			if path != "" {
				if _, seen := index[path]; !seen {
					index[path] = i
				}
			}
		}
	}
	return index
}

func lookupChange(index map[string]int, section string) (int, bool) {
	for _, path := range patchSectionPaths(section) {
		if i, ok := index[path]; ok {
			return i, true
		}
	}
	return 0, false
}

// splitPatchSections cuts a patch at each `diff --git ` header line. A content
// line cannot forge that prefix: every hunk body line starts with ' ', '+', '-'
// or '\'. Anything before the first header (a diffstat, if git ever emits one)
// is discarded.
func splitPatchSections(patch string) []string {
	var (
		sections []string
		current  []string
		open     bool
	)
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			if open {
				sections = append(sections, strings.Join(current, "\n"))
			}
			open, current = true, nil
		}
		if open {
			current = append(current, line)
		}
	}
	if open {
		sections = append(sections, strings.Join(current, "\n"))
	}
	return sections
}

// patchSectionPaths returns the paths a section talks about: the --- / +++ pair
// when it carries text hunks, the rename pair when it is a pure rename.
func patchSectionPaths(section string) []string {
	var out []string
	for _, line := range strings.Split(section, "\n") {
		switch {
		case strings.HasPrefix(line, "--- "):
			out = append(out, decodePatchPath(strings.TrimPrefix(line, "--- "), "a/"))
		case strings.HasPrefix(line, "+++ "):
			out = append(out, decodePatchPath(strings.TrimPrefix(line, "+++ "), "b/"))
		case strings.HasPrefix(line, "rename from "):
			out = append(out, decodeQuotedPath(strings.TrimPrefix(line, "rename from ")))
		case strings.HasPrefix(line, "rename to "):
			out = append(out, decodeQuotedPath(strings.TrimPrefix(line, "rename to ")))
		}
	}
	return out
}

func decodePatchPath(field, sidePrefix string) string {
	path := decodeQuotedPath(field)
	if path == "/dev/null" {
		return ""
	}
	path = strings.TrimPrefix(path, sidePrefix)
	// A bare path that contains a space is separated from the rest of the line
	// by a trailing tab; a quoted path never carries one.
	if !strings.HasPrefix(field, "\"") {
		path = strings.TrimSuffix(path, "\t")
	}
	return path
}

func decodeQuotedPath(field string) string {
	if strings.HasPrefix(field, "\"") && strings.HasSuffix(field, "\"") {
		if unquoted, err := strconv.Unquote(field); err == nil {
			return unquoted
		}
	}
	return field
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
