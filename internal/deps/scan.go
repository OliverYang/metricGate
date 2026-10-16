package deps

// deps scan: walk the import face of the repository and draft the
// dependencies.allow entries a policy would need. The draft comes from what
// the code actually imports and is not already covered — declared manifests
// (go.mod, package-lock.json, requirements.txt, Cargo.toml) stay the source
// of truth for the checker; the scan answers "what would we have to declare
// if we started from zero", which onboards a repository onto the gate without
// a manual whitelist hunt.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"metricgate/internal/parser"
	"metricgate/internal/rules"
	"metricgate/internal/sources"
)

// impHit is one import occurrence on the import face.
type impHit struct {
	path string // import text, quotes/brackets trimmed
	raw  string // text as written (brackets kept for C-style includes)
	line int    // 1-based line of the import declaration
}

// Import is one distinct external import path found in the scan.
type Import struct {
	Path  string `json:"path"`  // the import text as written (trimmed)
	File  string `json:"file"`  // first file that declares it
	Lang  string `json:"lang"`  // parser language id
	Line  int    `json:"line"`  // first line in that file
	Count int    `json:"count"` // how many files import it
}

// Draft is the result of one scan.
type Draft struct {
	Module  string   // this repository's own module path ("" if none)
	Imports []Import // external imports, sorted by path
	Covered []string // names already declared by manifests or policy
	// Files counts the parsed files per language, including the languages
	// with no import extractor. Without it a language absent from
	// importQueries is indistinguishable from a language with no imports.
	Files map[string]int
}

// hiddenSkip reports whether a path should be skipped by the scan: any
// dot-directory in it means tooling state (.git, .data, .metricgate), not
// the project's import face. sources.Files already skips .git; this covers
// the rest without teaching sources about policy. Files themselves never
// start with a dot here (LanguageForFile would not match extensionless
// dotfiles anyway).
func hiddenSkip(p string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(filepath.Dir(p)), "/") {
		if strings.HasPrefix(seg, ".") && len(seg) > 1 {
			return true
		}
	}
	return false
}

// ScanRoot walks root and returns the import face. Languages without an
// import extractor are skipped silently; the draft is advisory, and the
// per-language file counts in the output make a missing extractor visible
// instead of silently thinning the whitelist.
func ScanRoot(root string, cfgAllow []string) (*Draft, error) {
	d := &Draft{Files: map[string]int{}}
	if modPath, ok := findGoMod(root); ok {
		d.Module, _ = ParseGoMod(modPath)
	}
	d.Covered = declaredNames(root, cfgAllow)

	files, err := sources.Files(root)
	if err != nil {
		return nil, err
	}
	type key struct {
		lang parser.LanguageID
		path string
	}
	idx := map[key]int{} // import path → Imports position
	counts := map[parser.LanguageID]int{}
	for _, f := range files {
		if hiddenSkip(f) {
			continue
		}
		lang, ok := parser.LanguageForFile(f)
		if !ok {
			continue
		}
		counts[lang]++
		d.Files[string(lang)] = counts[lang]
		q, ok := importQueries[lang]
		if !ok {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			continue // unreadable file: skip, the scan is a draft not a gate
		}
		for _, h := range q(src) {
			if h.path == "" || !d.external(h.path) || rules.IsStdlib(lang, h.rawOrPath()) {
				continue
			}
			k := key{lang, h.path}
			if i, seen := idx[k]; seen {
				d.Imports[i].Count++
				continue
			}
			idx[k] = len(d.Imports)
			d.Imports = append(d.Imports, Import{
				Path: h.path, File: relPath(root, f), Lang: string(lang), Line: h.line, Count: 1,
			})
		}
	}
	sort.Slice(d.Imports, func(i, j int) bool { return d.Imports[i].Path < d.Imports[j].Path })
	return d, nil
}

// relPath renders a file as it appears in reports: relative to the scan root.
func relPath(root, f string) string {
	rel, err := filepath.Rel(root, f)
	if err != nil {
		return f
	}
	return filepath.ToSlash(rel)
}

// external reports whether the import names a dependency rather than this
// repository's own code: relative and root-absolute imports are internal
// structure, and so is anything under the repository's own module path.
func (d *Draft) external(imp string) bool {
	if strings.HasPrefix(imp, ".") || strings.HasPrefix(imp, "/") {
		return false
	}
	if d.Module != "" && (imp == d.Module || strings.HasPrefix(imp, d.Module+"/")) {
		return false
	}
	return true
}

// importQueryFunc extracts the import face of one source file.
type importQueryFunc func(src []byte) []impHit

// declaredNames collects everything the repository already declares: policy
// entries plus every manifest the allowlist machinery reads. The scan's
// value is the *uncovered* remainder, so coverage must use the same sources
// the checker will.
func declaredNames(root string, cfgAllow []string) []string {
	out := append([]string{}, cfgAllow...)
	if modPath, ok := findGoMod(root); ok {
		_, requires := ParseGoMod(modPath)
		out = append(out, requires...)
	}
	if p, ok := findManifest(root, "package.json"); ok {
		out = append(out, npmPkgJSONNames(p)...)
	}
	if p, ok := findManifest(root, "package-lock.json"); ok {
		out = append(out, npmLockNames(p)...)
	}
	out = append(out, pyNames(root)...)
	out = append(out, cargoNames(root)...)
	return out
}

// Uncovered returns the imports no declared name covers, using the same
// boundary rule as the checker (Matches semantics: exact or prefix on a
// separator). This is the part the policy must gain for the gate to stay
// quiet.
func Uncovered(d *Draft) []Import {
	var out []Import
	for _, imp := range d.Imports {
		if !AnyCovers(imp.Path, d.Covered) {
			out = append(out, imp)
		}
	}
	return out
}

// LanguagesWithoutImports returns the parsed languages that have no import
// extractor, in stable order. The scan skips them, and a caller that prints
// the draft has to say so: an empty draft otherwise reads as "no external
// dependencies" rather than "not scanned".
func (d *Draft) LanguagesWithoutImports() []string {
	var out []string
	for lang, n := range d.Files {
		if n == 0 {
			continue
		}
		if _, ok := importQueries[parser.LanguageID(lang)]; !ok {
			out = append(out, lang)
		}
	}
	sort.Strings(out)
	return out
}
