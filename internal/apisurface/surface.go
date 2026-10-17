// Package apisurface snapshots and diffs the public API of a codebase
// (Phase B-3 of docs/phase-b-design.md).
//
// The model follows SemVer's intent: what the package exports is the
// contract; removing it or changing its signature is a breaking change,
// adding to it is safe. The snapshot is a sorted JSON file — stable bytes,
// so it can live in the repo and be diffed by tooling.
package apisurface

import (
	"path/filepath"
	"sort"
	"strings"

	"metricgate/internal/facts"
)

// Entry is one exported symbol of the snapshot.
type Entry struct {
	Name      string `json:"name"`
	Receiver  string `json:"receiver,omitempty"`
	Signature string `json:"signature,omitempty"`
	File      string `json:"file"`
	Line      int    `json:"line"`
}

// Snapshot is the exported API of one run, keyed for diffing.
type Snapshot struct {
	Version  int                `json:"version"`
	Packages map[string][]Entry `json:"packages"`
}

// Current captures the exported surface of the fact sheets. Package is the
// fact sheet's declared package (Go: the package clause; the graph identity
// of a Go package is its directory, which the file path already carries).
func Current(files []*facts.FileFacts) *Snapshot {
	snap := &Snapshot{Version: 1, Packages: map[string][]Entry{}}
	for _, f := range files {
		if !productionFile(f.File) {
			continue // tests/fixtures are not the shipped API contract
		}
		for _, s := range f.Symbols {
			if s.Visibility == "exported" && s.Name != "" {
				appendEntry(snap, f, Entry{
					Name: s.Name, Receiver: s.Receiver, Signature: s.Signature,
					File: f.File, Line: s.StartLine,
				})
			}
			// Java facts nest methods/fields under their class symbol; they
			// are still part of that class's public API and must be snapshotted.
			for _, c := range s.Children {
				if c.Visibility != "exported" || c.Name == "" {
					continue
				}
				appendEntry(snap, f, Entry{
					Name: c.Name, Receiver: s.Name, Signature: c.Signature,
					File: f.File, Line: c.StartLine,
				})
			}
		}
	}
	for pkg := range snap.Packages {
		entries := snap.Packages[pkg]
		sort.Slice(entries, func(i, j int) bool {
			a, b := entries[i], entries[j]
			if a.Name != b.Name {
				return a.Name < b.Name
			}
			return a.Receiver < b.Receiver
		})
		snap.Packages[pkg] = entries
	}
	return snap
}

// rootPackage is the bucket for symbols whose file declares no package. It is
// a bucket, not a namespace — see key.
const rootPackage = "(root)"

// appendEntry adds a definition to its package bucket, normalising the
// package name only when the fact sheet does not provide one.
func appendEntry(snap *Snapshot, f *facts.FileFacts, e Entry) {
	pkg := f.Package
	if pkg == "" {
		pkg = rootPackage
	}
	snap.Packages[pkg] = append(snap.Packages[pkg], e)
}

// productionFile excludes tests and fixtures: they are useful to the
// checker, but are not the library's shipped API contract.
func productionFile(path string) bool {
	if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "Test.java") {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == "testdata" || strings.HasPrefix(part, "Test") {
			return false
		}
	}
	return true
}

// key identifies one exported symbol across snapshots: package, name and
// receiver (methods on different types are different symbols).
//
// Symbols with no declared package land in the "(root)" bucket, and that
// bucket is not a namespace: it collects every package-less file in the tree
// (scripts, one-off programs), where the same name is routinely defined by
// unrelated files. Keying those by name alone made Compare pair two different
// functions and report a signature change for a tree compared against itself.
// The file joins the key only for that bucket, so real packages keep the
// move-detection behaviour they had.
func key(pkg string, e Entry) string {
	if pkg == rootPackage {
		return pkg + "|" + e.File + "|" + e.Receiver + "|" + e.Name
	}
	return pkg + "|" + e.Receiver + "|" + e.Name
}

// sigOf normalises a signature for comparison: all whitespace dropped, so
// formatting-only changes (a space before a paren, a wrapped line) are not
// breaking. Signatures are single-line oneline() output from the facts
// extractor, so this only ever strips presentation spacing.
func sigOf(e Entry) string {
	out := make([]byte, 0, len(e.Signature))
	for i := 0; i < len(e.Signature); i++ {
		c := e.Signature[i]
		switch c {
		case ' ', '\t', '\n', '\r':
		default:
			out = append(out, c)
		}
	}
	return string(out)
}

// Diff is the change set between two snapshots.
type Diff struct {
	Breaking []Change `json:"breaking,omitempty"` // removed or signature-changed
	Added    []Change `json:"added,omitempty"`    // new symbols (safe)
	Moved    []Change `json:"moved,omitempty"`    // same symbol, new location (informational)
}

// Changed reports whether the surface changed at all.
func (d Diff) Changed() bool {
	return len(d.Breaking) > 0 || len(d.Added) > 0 || len(d.Moved) > 0
}

// Change is one difference, anchored where the symbol lives now.
type Change struct {
	Package string `json:"package"`
	Kind    string `json:"kind"` // removed | signature-changed | visibility-lowered | added | moved
	Name    string `json:"name"`
	Old     string `json:"old,omitempty"`
	New     string `json:"new,omitempty"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
}

// Compare diffs old against new: anything in old but not in new, or with a
// changed normalised signature, is breaking; new-only symbols are safe.
func Compare(oldSnap, newSnap *Snapshot) Diff {
	var d Diff
	oldKeys := map[string]Entry{}
	for pkg, entries := range oldSnap.Packages {
		for _, e := range entries {
			oldKeys[key(pkg, e)] = e
		}
	}
	seen := map[string]bool{}
	for pkg, entries := range newSnap.Packages {
		for _, e := range entries {
			k := key(pkg, e)
			seen[k] = true
			o, ok := oldKeys[k]
			if !ok {
				d.Added = append(d.Added, Change{Package: pkg, Kind: "added", Name: e.Name, New: e.Receiver, File: e.File, Line: e.Line})
				continue
			}
			if sigOf(o) != sigOf(e) {
				d.Breaking = append(d.Breaking, Change{Package: pkg, Kind: "signature-changed", Name: e.Name, Old: sigOf(o), New: sigOf(e), File: e.File, Line: e.Line})
				continue
			}
			if o.File != e.File {
				d.Moved = append(d.Moved, Change{Package: pkg, Kind: "moved", Name: e.Name, Old: o.File, New: e.File, File: e.File, Line: e.Line})
			}
		}
	}
	for pkg, entries := range oldSnap.Packages {
		for _, e := range entries {
			k := key(pkg, e)
			if !seen[k] {
				d.Breaking = append(d.Breaking, Change{Package: pkg, Kind: "removed", Name: e.Name, Old: sigOf(e), File: e.File, Line: e.Line})
			}
		}
	}
	sortChanges(&d)
	return d
}

func sortChanges(d *Diff) {
	sorter := func(cs []Change) {
		sort.Slice(cs, func(i, j int) bool {
			if cs[i].Package != cs[j].Package {
				return cs[i].Package < cs[j].Package
			}
			if cs[i].Kind != cs[j].Kind {
				return cs[i].Kind < cs[j].Kind
			}
			return cs[i].Name < cs[j].Name
		})
	}
	sorter(d.Breaking)
	sorter(d.Added)
	sorter(d.Moved)
}
