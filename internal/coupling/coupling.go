// Package coupling finds files that change together — the change coupling
// (temporal coupling) of Tornhill's "Your Code as a Crime Scene" (2015).
//
// It answers a question no import graph can: two files with no dependency
// between them that keep being edited in the same commit are coupled in
// practice, whatever the AST says. A cross-layer pair like that is
// architectural erosion in progress — the layering rule is still satisfied,
// and the code is already moving as one unit.
//
// The input is internal/git's History — the same `git log --numstat` facts the
// churn ranking (internal/hotspots) reads, so the two views can never
// disagree about what changed. The adapter converts it into the Commit shape
// declared below, which keeps this package free of the git adapter itself.
package coupling

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"metricgate/internal/arch"
)

// Defaults chosen so the ranking shows real coupling rather than the noise
// that any two frequently edited files accumulate.
const (
	// DefaultMaxCommitFiles is the sweep threshold: a commit touching more
	// files than this is a reformat, a rename or a dependency bump, and
	// pairing every file in it would manufacture coupling between unrelated
	// code. Measured on a real 5636-commit repository, the median commit
	// touches 1 file, so the default sits far above normal work and far
	// below a sweep.
	DefaultMaxCommitFiles = 12
	// DefaultMinCount is how many shared commits make a pair worth reporting.
	DefaultMinCount = 5
	// DefaultMax caps the reported ranking.
	DefaultMax = 20
)

// DefaultExclude are path patterns whose co-change carries no design
// information: lock files move with every dependency bump, generated code
// moves with its generator, and per-environment config mirrors move with each
// other. They are patterns, not globs of the filesystem kind, and are matched
// by MatchFile (the same "**"-aware vocabulary the architecture patterns use,
// but against file paths rather than package names).
var DefaultExclude = []string{
	"**/*.sum",
	"**/package-lock.json",
	"**/yarn.lock",
	"**/pnpm-lock.yaml",
	"**/Cargo.lock",
	"**/poetry.lock",
	"**/Pipfile.lock",
	"**/composer.lock",
	"**/Gemfile.lock",
	"**/*.min.js",
	"**/*.min.css",
	"**/*.pb.go",
	"**/zz_generated*",
	"**/application-*.yml",
	"**/application-*.yaml",
	"**/application-*.properties",
}

// Commit is one commit's file changes: the shape this analysis consumes.
//
// It is declared here rather than reused from the git adapter on purpose. The
// analysis only ever needs paths, and a package that takes an adapter type as
// its input forces every caller to link that adapter — which is exactly how a
// "pure library" core starts shelling out to processes.
type Commit struct {
	Files []File
}

// File is one file touched by a commit. Additions and deletions are not
// modeled: co-change is about which files moved together, not how much.
type File struct {
	Path string
}

// Pair is two files that changed in the same commits.
type Pair struct {
	A     string `json:"a"`
	B     string `json:"b"`
	Count int    `json:"count"`
	// Layers are the architecture layer names of A and B ("" when the file's
	// package is in no declared layer). CrossLayer is true only when both
	// sides have a layer and the layers differ, so an unassigned file never
	// looks like a boundary crossing.
	Layers     [2]string `json:"layers"`
	CrossLayer bool      `json:"crossLayer"`
}

// Options configures one coupling run.
type Options struct {
	// MaxCommitFiles is the sweep threshold (0 = DefaultMaxCommitFiles).
	MaxCommitFiles int
	// Exclude are extra path patterns to ignore, added to DefaultExclude.
	Exclude []string
	// MinCount is the shared-commit floor (0 = DefaultMinCount).
	MinCount int
	// Max caps the ranking (0 = DefaultMax).
	Max int
	// CrossLayerOnly keeps only pairs whose two files sit in different declared
	// layers (the gate's view). When false every pair is returned, which is
	// what the `coupling` CLI verb shows: the ranking is useful as a map of
	// the codebase, and the boundary crossing is the annotation that makes a
	// pair actionable.
	CrossLayerOnly bool
	// LayerOf maps a package path to its architecture layer name ("" = no
	// layer). It is injected so this package stays free of policy loading.
	LayerOf func(pkg string) string
}

// normalized fills the defaults so the zero Options is the documented policy.
func (o Options) normalized() Options {
	if o.MaxCommitFiles <= 0 {
		o.MaxCommitFiles = DefaultMaxCommitFiles
	}
	if o.MinCount <= 0 {
		o.MinCount = DefaultMinCount
	}
	if o.Max <= 0 {
		o.Max = DefaultMax
	}
	return o
}

// Report is the outcome of one coupling run, including what was left out, so
// a caller can explain the number instead of just showing it.
type Report struct {
	Pairs []Pair `json:"pairs"`
	// SkippedSweeps counts commits dropped by the sweep threshold and
	// SkippedFiles the individual files dropped by the exclusion patterns.
	// Both are reported: a ranking computed after silently dropping half the
	// history would be misleading.
	SkippedSweeps int `json:"skippedSweeps"`
	SkippedFiles  int `json:"skippedFiles"`
	// Shallow marks a run over a shallow clone, whose truncated history
	// undercounts every pair. The caller must surface this.
	Shallow bool `json:"shallow"`
}

// Analyze folds per-commit file changes into ranked co-change pairs.
func Analyze(commits []Commit, opts Options) Report {
	o := opts.normalized()
	counts := map[[2]string]int{}
	rep := Report{}
	for _, c := range commits {
		files := keepFiles(c, o)
		if len(files) > o.MaxCommitFiles {
			rep.SkippedSweeps++
			continue
		}
		rep.SkippedFiles += len(c.Files) - len(files)
		addPairs(counts, files)
	}
	rep.Pairs = rankPairs(counts, o)
	return rep
}

// keepFiles returns the commit's distinct, non-excluded paths in a stable
// order. Deduplicating matters: a file reported twice in one commit (a merge
// or a rename pair) would otherwise be paired with itself.
func keepFiles(c Commit, o Options) []string {
	seen := map[string]bool{}
	var files []string
	for _, f := range c.Files {
		if f.Path == "" || seen[f.Path] || excluded(f.Path, o) {
			continue
		}
		seen[f.Path] = true
		files = append(files, f.Path)
	}
	sort.Strings(files)
	return files
}

// excluded reports whether path matches the default or configured patterns.
func excluded(p string, o Options) bool {
	if MatchFile(DefaultExclude, p) {
		return true
	}
	return MatchFile(o.Exclude, p)
}

// MatchFile reports whether any glob pattern matches a slash-separated file
// path. "**" spans any number of path segments (including none) and every
// other segment is matched by path.Match, so "**/*.sum" covers both "go.sum"
// and "vendor/x/go.sum", and "application-*.yml" covers the mirrored config
// files wherever they live.
func MatchFile(patterns []string, file string) bool {
	name := strings.Split(strings.ReplaceAll(file, "\\", "/"), "/")
	for _, p := range patterns {
		if matchSegments(strings.Split(strings.Trim(p, "/"), "/"), name) {
			return true
		}
	}
	return false
}

// matchSegments is the recursive core of MatchFile. "**" consumes zero or
// more segments; every other pattern segment must match exactly one.
func matchSegments(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		return matchSubtree(pat[1:], name)
	}
	if len(name) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], name[0])
	return err == nil && ok && matchSegments(pat[1:], name[1:])
}

// matchSubtree tries every split point for a leading "**".
func matchSubtree(rest, name []string) bool {
	for i := 0; i <= len(name); i++ {
		if matchSegments(rest, name[i:]) {
			return true
		}
	}
	return false
}

// addPairs increments the counter for every unordered pair of files. Only
// called for commits that passed the sweep filter, so the pair count stays
// bounded by MaxCommitFiles².
func addPairs(counts map[[2]string]int, files []string) {
	for i := 0; i < len(files); i++ {
		for j := i + 1; j < len(files); j++ {
			counts[[2]string{files[i], files[j]}]++
		}
	}
}

// rankPairs applies the count floor and the cross-layer filter, resolves
// layers, and returns the top pairs. Ordering is by count then by path, so the
// same history always yields the same ranking.
func rankPairs(counts map[[2]string]int, o Options) []Pair {
	pairs := make([]Pair, 0, len(counts))
	for key, n := range counts {
		if n < o.MinCount {
			continue
		}
		p := makePair(key, n, o)
		if o.CrossLayerOnly && !p.CrossLayer {
			continue
		}
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Count != pairs[j].Count {
			return pairs[i].Count > pairs[j].Count
		}
		if pairs[i].A != pairs[j].A {
			return pairs[i].A < pairs[j].A
		}
		return pairs[i].B < pairs[j].B
	})
	if len(pairs) > o.Max {
		pairs = pairs[:o.Max]
	}
	return pairs
}

// makePair resolves both files' layers. CrossLayer requires two assigned,
// different layers: an unassigned file is unknown, not a boundary crossing.
func makePair(key [2]string, n int, o Options) Pair {
	p := Pair{A: key[0], B: key[1], Count: n}
	if o.LayerOf == nil {
		return p
	}
	p.Layers[0] = o.LayerOf(PackageOf(key[0]))
	p.Layers[1] = o.LayerOf(PackageOf(key[1]))
	p.CrossLayer = p.Layers[0] != "" && p.Layers[1] != "" && p.Layers[0] != p.Layers[1]
	return p
}

// PackageOf returns the directory that owns a file, in slash form. It is the
// package identity the architecture layers are written against; a
// repository-root file has no package and yields "".
func PackageOf(file string) string {
	dir := path.Dir(strings.ReplaceAll(file, "\\", "/"))
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

// LayerResolver builds the LayerOf function for one layer list. The layers
// come straight from policy.yaml's architecture section, so a project that
// already declared its layering gets cross-layer coupling for free.
func LayerResolver(layers []arch.Layer) func(string) string {
	return func(pkg string) string {
		if pkg == "" {
			return ""
		}
		for _, l := range layers {
			if arch.MatchesAny(l.Packages, pkg) {
				return l.Name
			}
		}
		return ""
	}
}

// Summarize renders one line per pair, for text output.
func Summarize(pairs []Pair) []string {
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		tag := ""
		if p.CrossLayer {
			tag = fmt.Sprintf("  [cross-layer: %s -> %s]", p.Layers[0], p.Layers[1])
		}
		out = append(out, fmt.Sprintf("%4d  %s  <->  %s%s", p.Count, p.A, p.B, tag))
	}
	return out
}
