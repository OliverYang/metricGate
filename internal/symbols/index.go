// Package symbols builds a cross-file definition index over extracted
// facts and answers two questions deterministically, in milliseconds and
// for zero tokens:
//
//   - where is this symbol defined? (Lookup)
//   - which similar names already exist? (Similar / SimilarElsewhere) —
//     so generated code aligns with the existing API surface instead of
//     reinventing a near-miss, and duplicate concepts get a name.
//
// Everything is name-based and rebuilt per call; a persistent or
// incremental cache is future work once call sites justify it.
package symbols

import (
	"sort"
	"strings"
)

// Def is one indexed definition.
type Def struct {
	Name       string `json:"name"`
	Receiver   string `json:"receiver,omitempty"`
	Kind       string `json:"kind,omitempty"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Signature  string `json:"signature,omitempty"`
	Language   string `json:"language,omitempty"`
	Package    string `json:"package,omitempty"`
	Visibility string `json:"visibility,omitempty"`
}

// Match pairs a definition with the similarity score that surfaced it
// (lower is closer).
type Match struct {
	Def   Def `json:"def"`
	Score int `json:"score"`
}

// Similarity scores. Anything above scoreContains is "not similar".
const (
	scoreExact    = 0 // same name, same case
	scoreFold     = 1 // same name, case differs
	scoreEditBase = 2 // edit distance, scored as base + distance
	scoreContains = 6 // one name contains the other (>= 5 runes)
	scoreNo       = 127
)

// Index maps symbol names to their definitions.
type Index struct {
	byName map[string][]Def
	all    []Def
}

// Build indexes the given definitions. The index is immutable once built.
func Build(defs []Def) *Index {
	ix := &Index{
		byName: make(map[string][]Def, len(defs)),
		all:    make([]Def, 0, len(defs)),
	}
	for _, d := range defs {
		if d.Name == "" {
			continue
		}
		ix.byName[d.Name] = append(ix.byName[d.Name], d)
		ix.all = append(ix.all, d)
	}
	for name := range ix.byName {
		ds := ix.byName[name]
		sort.Slice(ds, func(i, j int) bool { return lessDef(ds[i], ds[j]) })
	}
	return ix
}

// Len is the number of indexed definitions.
func (ix *Index) Len() int {
	if ix == nil {
		return 0
	}
	return len(ix.all)
}

// All returns every indexed definition in file order (the index's own
// deterministic order). Callers that need to iterate the whole index —
// package-scoped listings, the constraint digest — use this instead of
// duplicating the index build.
func (ix *Index) All() []Def {
	if ix == nil {
		return nil
	}
	return ix.all
}

// lessDef orders definitions by file, then line, then receiver.
func lessDef(a, b Def) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Receiver < b.Receiver
}

// Lookup returns every definition of exactly that name, in file order.
func (ix *Index) Lookup(name string) []Def {
	if ix == nil || name == "" {
		return nil
	}
	return ix.byName[name]
}

// Similar returns up to max definitions whose names resemble query, best
// first, with a deterministic tie-break.
func (ix *Index) Similar(query string, max int) []Match {
	return ix.similar(query, "", max, scoreContains)
}

// SimilarElsewhere is Similar with two tightenings for the context pack:
// definitions from ownFile are excluded (a file needs no hint about
// itself) and only tight matches count — same name, case-insensitive
// equal or small edit distance. Loose substring matches would be noise.
func (ix *Index) SimilarElsewhere(query, ownFile string, max int) []Match {
	return ix.similar(query, ownFile, max, scoreContains-1)
}

func (ix *Index) similar(query, excludeFile string, max, maxScore int) []Match {
	if ix == nil || max <= 0 || strings.TrimSpace(query) == "" {
		return nil
	}
	var out []Match
	for _, d := range ix.all {
		if excludeFile != "" && d.File == excludeFile {
			continue
		}
		s := matchScore(query, d.Name)
		if s > maxScore {
			continue
		}
		out = append(out, Match{Def: d, Score: s})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Score != b.Score {
			return a.Score < b.Score
		}
		if a.Def.Name != b.Def.Name {
			return a.Def.Name < b.Def.Name
		}
		return lessDef(a.Def, b.Def)
	})
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// matchScore grades how well cand resembles query. Precedence: exact >
// case-fold > small edit distance > containment; a one-key typo (swap,
// drop or double key) must rank as the near-miss it is, not drown in
// substring matches.
func matchScore(query, cand string) int {
	if query == cand {
		return scoreExact
	}
	if strings.EqualFold(query, cand) {
		return scoreFold
	}
	q, c := []rune(query), []rune(cand)
	if abs(len(q)-len(c)) <= maxEditDistance(len(q)) {
		if d := editDistance(q, c); d <= maxEditDistance(len(q)) {
			return scoreEditBase + d
		}
	}
	if containsName(q, c) {
		return scoreContains
	}
	return scoreNo
}

// maxEditDistance is the tolerated typo budget for a name of n runes:
// short names tolerate less, or every two-letter helper would match every
// other one.
func maxEditDistance(n int) int {
	switch {
	case n <= 4:
		return 1
	case n <= 9:
		return 2
	default:
		return 3
	}
}

// containsName reports whether one name contains the other, when the
// shorter part is meaningful (>= 5 runes).
func containsName(a, b []rune) bool {
	short, long := a, b
	if len(short) > len(long) {
		short, long = b, a
	}
	if len(short) < 5 || len(long) <= len(short) {
		return false
	}
	return strings.Contains(string(long), string(short))
}

// editDistance is the restricted Damerau-Levenshtein distance over runes
// (optimal string alignment): insertions, deletions, substitutions and
// adjacent transpositions all cost 1 — "Rneder" is one key-swap away from
// "Render", which plain Levenshtein would score as two.
func editDistance(a, b []rune) int {
	la, lb := len(a), len(b)
	d := make([][]int, la+1)
	for i := range d {
		d[i] = make([]int, lb+1)
		d[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		d[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			best := min3(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				if t := d[i-2][j-2] + 1; t < best {
					best = t
				}
			}
			d[i][j] = best
		}
	}
	return d[la][lb]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
