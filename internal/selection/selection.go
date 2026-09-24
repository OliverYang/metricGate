// Package selection contains the pure path-selection policy shared by the
// engine, preview, and facts adapters. Traversal and file I/O remain with the
// callers; this package only classifies a path against the existing policy.
package selection

import (
	"path/filepath"
	"sort"
	"strings"

	"metricgate/internal/parser"
)

type Status string

const (
	Included    Status = "included"
	Excluded    Status = "excluded"
	Unsupported Status = "unsupported"
)

type Reason string

const (
	ReasonSupportedLanguage Reason = "supported_language"
	ReasonDirectoryExclude  Reason = "policy_exclude_directory"
	ReasonUnsupportedExt    Reason = "unsupported_extension"
	ReasonExcludedAncestor  Reason = "excluded_ancestor"
)

// Decision describes the reason a candidate path is selected or rejected.
type Decision struct {
	Path           string `json:"path"`
	Status         Status `json:"status"`
	Reason         Reason `json:"reason"`
	Language       string `json:"language,omitempty"`
	MatchedExclude string `json:"matchedExclude,omitempty"`
}

// Candidate is a repo-relative path discovered by a caller's traversal.
type Candidate struct {
	Path  string
	IsDir bool
}

// Decide classifies one path. Exclude preserves metricgate's existing exact
// directory-basename semantics; it is not a glob or file-name filter.
func Decide(path string, isDir bool, exclude []string) Decision {
	path = normalize(path)
	if isDir {
		name := filepath.Base(filepath.FromSlash(path))
		for _, excluded := range exclude {
			if name == excluded {
				return Decision{Path: path, Status: Excluded, Reason: ReasonDirectoryExclude, MatchedExclude: excluded}
			}
		}
		return Decision{Path: path, Status: Included, Reason: ReasonSupportedLanguage}
	}
	parts := strings.Split(path, "/")
	for _, part := range parts[:max(0, len(parts)-1)] {
		for _, excluded := range exclude {
			if part == excluded {
				return Decision{Path: path, Status: Excluded, Reason: ReasonExcludedAncestor, MatchedExclude: excluded}
			}
		}
	}
	lang, ok := parser.LanguageForFile(filepath.Base(filepath.FromSlash(path)))
	if !ok {
		return Decision{Path: path, Status: Unsupported, Reason: ReasonUnsupportedExt}
	}
	return Decision{Path: path, Status: Included, Reason: ReasonSupportedLanguage, Language: string(lang)}
}

// Plan classifies an already-discovered candidate set. It drops descendants
// of excluded directories and sorts every result deterministically.
func Plan(candidates []Candidate, exclude []string) []Decision {
	dirs := make([]string, 0)
	for _, c := range candidates {
		if c.IsDir {
			d := Decide(c.Path, true, exclude)
			if d.Status == Excluded {
				dirs = append(dirs, normalize(c.Path))
			}
		}
	}
	out := make([]Decision, 0, len(candidates))
	for _, c := range candidates {
		if c.IsDir {
			continue
		}
		p := normalize(c.Path)
		if ancestor := excludedAncestor(p, dirs); ancestor != "" {
			out = append(out, Decision{Path: p, Status: Excluded, Reason: ReasonExcludedAncestor, MatchedExclude: filepath.Base(filepath.FromSlash(ancestor))})
			continue
		}
		out = append(out, Decide(p, false, exclude))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// FilterPaths classifies file paths without directory enumeration. It is
// used when a caller (such as facts) already has a sorted file list.
func FilterPaths(paths, exclude []string) []Decision {
	out := make([]Decision, 0, len(paths))
	for _, p := range paths {
		out = append(out, Decide(p, false, exclude))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func excludedAncestor(path string, dirs []string) string {
	best := ""
	for _, d := range dirs {
		if path == d || strings.HasPrefix(path, d+"/") {
			if len(d) > len(best) {
				best = d
			}
		}
	}
	return best
}

func normalize(path string) string {
	path = strings.ReplaceAll(path, `\`, "/")
	path = filepath.ToSlash(filepath.Clean(path))
	path = strings.TrimPrefix(path, "./")
	if path == "." {
		return ""
	}
	return path
}

// SanitizePath makes a path safe to cross the local trust boundary (a prompt,
// an agent, or CI). A local audit may legitimately report machine-absolute
// paths, but anything leaving metricgate must be machine-independent: given a
// walk root, an absolute result under it becomes root-relative, and any other
// absolute path is reduced to its base name so no home-directory or mount
// layout leaks. Relative paths are normalized (slash-separated, cleaned) and
// never rewritten, so behavior is identical whether root is "." or absolute.
func SanitizePath(root, path string) string {
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		return normalize(path)
	}
	if root != "" {
		if rel, err := filepath.Rel(root, path); err == nil && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, "..") {
			if r := normalize(rel); r != "" {
				return r
			}
		}
	}
	return filepath.Base(filepath.FromSlash(path))
}
