// Package clonedrift detects "clone drift": a diff that edits one copy of a
// Type-2 clone while leaving its twin untouched, so the two copies silently
// diverge. The research on consistent vs inconsistent clone changes shows this
// is a real maintenance hazard — a bug fixed on one copy but not its twin.
//
// It is an adapter: it walks two trees (the base revision and the working
// tree), reuses internal/clones for fingerprinting/grouping, and emits plain
// report records. Nothing here shells out; the base tree is handed in already
// extracted by internal/git.
package clonedrift

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"metricgate/internal/clones"
	"metricgate/internal/diff"
	"metricgate/internal/parser"
	"metricgate/internal/sources"
)

// RuleID is the stable identifier for a drift finding.
const RuleID = "clone-drift"

// Finding is one drifted clone pair. The changed side is reported (the side
// whose edit was not mirrored), pointing at the untouched twin.
type Finding struct {
	File      string `json:"file"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Name      string `json:"name"`
	TwinFile  string `json:"twinFile"`
	TwinLine  int    `json:"twinLine"`
	TwinName  string `json:"twinName"`
	Message   string `json:"message"`
}

// Detect compares base clones against the working tree. changed maps
// repo-relative slash paths to the added-line ranges in the working tree
// (from internal/git). root is the working tree; baseDir is the extracted
// base revision. A pair is reported only when both copies still exist, at
// least one was touched by the diff, and their bodies are no longer identical
// — i.e. the change was not mirrored. Removing one copy is de-duplication, not
// drift, so it is not reported.
func Detect(root, baseDir string, changed map[string][]diff.Range, cc clones.Config) ([]Finding, error) {
	if !cc.Enabled() {
		return nil, nil
	}
	baseFuncs, err := scan(baseDir, cc)
	if err != nil {
		return nil, err
	}
	curFuncs, err := scan(root, cc)
	if err != nil {
		return nil, err
	}
	curByKey := index(curFuncs)

	var out []Finding
	for _, g := range clones.Find(baseFuncs, cc) {
		anchor := g.Funcs[0]
		for _, m := range g.Funcs[1:] {
			if f, ok := drift(anchor, m, curByKey, changed); ok {
				out = append(out, f)
			}
		}
	}
	return out, nil
}

// key identifies a function across revisions: file + name. Line numbers are
// deliberately excluded because an edit above the function shifts them.
type key struct{ file, name string }

func index(funcs []clones.Func) map[key]clones.Func {
	m := make(map[key]clones.Func, len(funcs))
	for _, f := range funcs {
		m[key{f.File, f.Name}] = f
	}
	return m
}

func drift(a, b clones.Func, curByKey map[key]clones.Func, changed map[string][]diff.Range) (Finding, bool) {
	ca, aOK := curByKey[key{a.File, a.Name}]
	cb, bOK := curByKey[key{b.File, b.Name}]
	// One side gone is de-duplication, not drift.
	if !aOK || !bOK {
		return Finding{}, false
	}
	// Bodies must have diverged (both updated the same way stays a clone).
	if ca.Hash == cb.Hash {
		return Finding{}, false
	}
	ta := touched(ca, changed)
	tb := touched(cb, changed)
	if !ta && !tb {
		return Finding{}, false
	}
	return finding(ca, cb, ta), true
}

// touched reports whether the member's current body intersects the diff.
func touched(cur clones.Func, changed map[string][]diff.Range) bool {
	ranges := rangesFor(changed, cur.File)
	return diff.Intersects(ranges, cur.StartLine, cur.EndLine)
}

// finding anchors on the changed side, pointing at the untouched twin.
func finding(ca, cb clones.Func, aTouched bool) Finding {
	if !aTouched {
		ca, cb = cb, ca
	}
	return Finding{
		File:      ca.File,
		StartLine: ca.StartLine,
		EndLine:   ca.EndLine,
		Name:      ca.Name,
		TwinFile:  cb.File,
		TwinLine:  cb.StartLine,
		TwinName:  cb.Name,
		Message: "this clone was edited but its twin at " + cb.File + ":" +
			strconv.Itoa(cb.StartLine) + " (" + cb.Name + ") was not; update both or extract a shared helper",
	}
}

// rangesFor tolerates runs started from a subdirectory (suffix match on the
// repo-rooted git path), matching the -diff filter's behaviour.
func rangesFor(changed map[string][]diff.Range, file string) []diff.Range {
	if r, ok := changed[file]; ok {
		return r
	}
	for path, r := range changed {
		if strings.HasSuffix(path, "/"+file) {
			return r
		}
	}
	return nil
}

// scan walks a tree and extracts clone fingerprints, returning paths relative
// to root so both revisions share one coordinate system.
func scan(root string, cfg clones.Config) ([]clones.Func, error) {
	files, err := sources.Files(root)
	if err != nil {
		return nil, err
	}
	parsers := map[parser.LanguageID]*parser.Parser{}
	defer func() {
		for _, p := range parsers {
			p.Close()
		}
	}()
	var out []clones.Func
	for _, path := range files {
		lang, ok := parser.LanguageForFile(filepath.Base(path))
		if !ok {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		pp := parsers[lang]
		if pp == nil {
			fresh, err := parser.New(lang)
			if err != nil {
				return nil, err
			}
			pp = fresh
			parsers[lang] = pp
		}
		tree, err := pp.Parse(src)
		if err != nil {
			continue
		}
		for _, fn := range clones.Extract(lang, rel(root, path), src, tree, cfg) {
			out = append(out, fn)
		}
		tree.Close()
	}
	return out, nil
}

func rel(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(r)
	}
	return filepath.ToSlash(path)
}
