// Package deps builds the allowlist of imports a repository has already
// declared, so a new one can be flagged before it is merged.
//
// The failure it guards against is specific to generated code: a model that
// invents a plausible-looking import — a package that does not exist, or a
// near-miss spelling of one that does (typosquatting) — compiles locally
// against nothing and fails much later. Catching it needs no type system and
// no network: a Go module already lists every dependency it may import in
// go.mod, and anything else is, by definition, new.
//
// The allowlist is a set of path prefixes, matched on a separator boundary so
// that "example.com/foo" never quietly covers "example.com/foobar".
package deps

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"metricgate/internal/config"
)

// Allowlist is the set of import prefixes a repository may use.
type Allowlist struct {
	prefixes []string // sorted, deduped
}

// Prefixes returns the allowed prefixes, for handing to a rule as options.
func (a Allowlist) Prefixes() []string { return a.prefixes }

// Allows reports whether an import path is covered by the allowlist. A prefix
// matches the path exactly or on a separator boundary, so a prefix never
// silently swallows a longer, different package.
func (a Allowlist) Allows(path string) bool {
	for _, p := range a.prefixes {
		if Matches(path, p) {
			return true
		}
	}
	return false
}

// Matches reports whether path falls under the prefix entry.
func Matches(path, entry string) bool {
	if entry == "" {
		return false
	}
	return path == entry ||
		strings.HasPrefix(path, entry+"/") ||
		strings.HasPrefix(path, entry+".")
}

// New builds an allowlist from arbitrary entries, dropping blanks.
func New(entries []string) Allowlist {
	seen := map[string]bool{}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	sort.Strings(out)
	return Allowlist{prefixes: out}
}

// ForRoot builds the allowlist for a repository: the policy's declared allow
// and self entries, plus the module path and requirements read from the
// nearest go.mod above root. A missing go.mod is not an error — the policy
// entries still apply, and a non-Go repository simply has none.
func ForRoot(root string, cfg *config.Config) Allowlist {
	var entries []string
	if cfg != nil {
		entries = append(entries, cfg.Dependencies.Self...)
		entries = append(entries, cfg.Dependencies.Allow...)
	}
	if modPath, ok := findGoMod(root); ok {
		module, requires := ParseGoMod(modPath)
		entries = append(entries, module)
		entries = append(entries, requires...)
	}
	return New(entries)
}

// findGoMod walks up from root looking for a go.mod, stopping at the filesystem
// root. A file root is searched from its directory.
func findGoMod(root string) (string, bool) {
	dir := root
	if fi, err := os.Stat(root); err == nil && !fi.IsDir() {
		dir = filepath.Dir(root)
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	for {
		candidate := filepath.Join(dir, "go.mod")
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// ParseGoMod extracts the module path and the required module paths from a
// go.mod file. It reads the file and returns whatever it finds; a malformed
// file yields the entries parsed so far rather than an error, because a
// best-effort allowlist is still useful.
func ParseGoMod(path string) (module string, requires []string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil
	}
	return parseGoModText(string(data))
}

// parseGoModText is the line-oriented go.mod reader: it understands the
// single-line `module X` / `require X` forms and the `require ( ... )` block,
// ignores comments, and treats every other directive as irrelevant.
func parseGoModText(text string) (module string, requires []string) {
	inRequireBlock := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(stripGoModComment(raw))
		if line == "" {
			continue
		}
		if inRequireBlock {
			if line == ")" {
				inRequireBlock = false
				continue
			}
			requires = append(requires, firstField(line))
			continue
		}
		switch {
		case strings.HasPrefix(line, "module "):
			module = unquote(strings.TrimSpace(strings.TrimPrefix(line, "module")))
		case line == "require (":
			inRequireBlock = true
		case strings.HasPrefix(line, "require "):
			requires = append(requires, firstField(strings.TrimPrefix(line, "require")))
		}
	}
	return module, requires
}

// stripGoModComment removes a trailing "// ..." comment.
func stripGoModComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	return line
}

// firstField returns the first whitespace-separated field, "" when none.
func firstField(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// unquote strips surrounding double quotes, if present.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
