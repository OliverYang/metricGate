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
	"encoding/json"
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
// nearest go.mod above root, plus npm dependency names read from the nearest
// package.json/package-lock.json. A missing manifest is not an error — the
// policy entries still apply, and a repository with none simply has no
// manifest-derived entries.
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
	entries = append(entries, npmNames(root)...)
	entries = append(entries, pyNames(root)...)
	entries = append(entries, cargoNames(root)...)
	return New(entries)
}

// npmNames returns the dependency names declared for an npm project: the
// dependencies/devDependencies of the nearest package.json, plus the
// packages of the nearest package-lock.json. Scoped names (@scope/pkg)
// are kept whole; names are manifest truth, so no version parsing beyond
// the JSON keys themselves.
func npmNames(root string) []string {
	if pkg, ok := findManifest(root, "package-lock.json"); ok {
		if names := npmLockNames(pkg); len(names) > 0 {
			return names
		}
	}
	if pkg, ok := findManifest(root, "package.json"); ok {
		return npmPkgJSONNames(pkg)
	}
	return nil
}

// findManifest walks up from root looking for the named file, stopping at the
// filesystem root. A file root is searched from its directory.
func findManifest(root, name string) (string, bool) {
	dir := root
	if fi, err := os.Stat(root); err == nil && !fi.IsDir() {
		dir = filepath.Dir(root)
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	for {
		candidate := filepath.Join(dir, name)
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

// npmPkgJSONNames reads dependencies and devDependencies keys from a
// package.json. A malformed file yields nothing rather than an error.
func npmPkgJSONNames(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	var out []string
	for name := range pkg.Dependencies {
		out = append(out, name)
	}
	for name := range pkg.DevDependencies {
		out = append(out, name)
	}
	return out
}

// npmLockNames reads the package keys ("node_modules/NAME", possibly scoped)
// from a package-lock.json v2/v3. A malformed file yields nothing.
func npmLockNames(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var lock struct {
		Packages map[string]struct {
			Name string `json:"name"`
		} `json:"packages"`
		Dependencies map[string]struct{} `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil
	}
	var out []string
	for key := range lock.Packages {
		name := strings.TrimPrefix(key, "node_modules/")
		if name == "" || name == key && !strings.HasPrefix(key, "node_modules/") {
			continue // the root package entry ""
		}
		out = append(out, name)
	}
	for name := range lock.Dependencies {
		out = append(out, name)
	}
	return out
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

// pyNames returns the dependency names declared for a Python project:
// requirement lines of the nearest requirements*.txt plus the project/dependencies
// lists of the nearest pyproject.toml. A missing manifest is not an error.
func pyNames(root string) []string {
	var out []string
	for _, name := range []string{"requirements.txt", "requirements-dev.txt", "requirements_dev.txt", "dev-requirements.txt"} {
		if req, ok := findManifest(root, name); ok {
			out = append(out, pyReqNames(req)...)
		}
	}
	if py, ok := findManifest(root, "pyproject.toml"); ok {
		out = append(out, pyProjectNames(py)...)
	}
	return out
}

// pyReqNames reads requirement specifiers from a requirements file: one name
// per non-comment, non-empty line, stripped of version/extra markers. A
// malformed line contributes nothing.
func pyReqNames(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue // comments, options (-r, -e, --index-url)
		}
		out = append(out, pyReqName(line))
	}
	return out
}

// pyReqName strips one requirement line down to the package name: cut at the
// first version/extras/environment marker (space, =, <, >, ~, !, [, ;, @)
// and normalize separators to underscores, matching import-name conventions
// loosely (the allowlist also matches entry prefixes, so both spellings work).
func pyReqName(line string) string {
	if i := strings.IndexAny(line, " =<>~![@;"); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	line = strings.ReplaceAll(line, "-", "_")
	line = strings.ReplaceAll(line, ".", "_")
	return strings.ToLower(line)
}

// pyProjectNames reads the project.dependencies names from a pyproject.toml.
// PEP 621 dependency arrays are line-scanned (no TOML dependency): entries
// between `dependencies = [` and the closing `]` are stripped to package
// names with the same rule as requirements files. A malformed file yields
// nothing rather than an error.
func pyProjectNames(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	inDeps := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !inDeps {
			inDeps = pyDepsArrayOpens(trimmed, &out)
			continue
		}
		if strings.HasPrefix(trimmed, "]") {
			inDeps = false
			continue
		}
		out = append(out, pyReqName(strings.Trim(trimmed, ",\"")))
	}
	return out
}

// pyDepsArrayOpens reports whether a line opens a project.dependencies array,
// capturing any requirement stated on the same line.
func pyDepsArrayOpens(trimmed string, out *[]string) bool {
	if !strings.HasPrefix(trimmed, "dependencies") || !strings.Contains(trimmed, "[") {
		return false
	}
	rest := trimmed[strings.Index(trimmed, "[")+1:]
	rest = strings.TrimSuffix(rest, "]")
	if name := pyReqName(strings.Trim(rest, ",\"")); name != "" {
		*out = append(*out, name)
	}
	return !strings.Contains(trimmed, "]") || strings.Index(trimmed, "[") > strings.Index(trimmed, "]")
}

// cargoNames reads crate names from a Cargo.toml: [dependencies] section
// keys, both inline-table entries (serde = { version = "1" }) and simple
// ones (serde = "1"). A malformed file yields nothing rather than an error.
func cargoNames(root string) []string {
	path, ok := findManifest(root, "Cargo.toml")
	if !ok {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var names []string
	inDeps := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
			continue
		case strings.HasPrefix(trimmed, "["):
			inDeps = trimmed == "[dependencies]"
		case inDeps:
			if key, _, ok := strings.Cut(trimmed, "="); ok {
				if name := strings.TrimSpace(key); name != "" && !strings.HasPrefix(name, "#") {
					names = append(names, name)
				}
			}
		}
	}
	return names
}

// AnyCovers reports whether any entry covers path — the scan's coverage
// test, mirroring allowedImport's boundary semantics so a draft never
// suggests an entry the checker would treat as already covered.
func AnyCovers(path string, entries []string) bool {
	for _, e := range entries {
		if Matches(path, e) {
			return true
		}
	}
	return false
}
