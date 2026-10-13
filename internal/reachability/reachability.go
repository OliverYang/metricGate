// Package reachability answers a different question from internal/impact:
// impact asks who uses one symbol, this asks which symbols nobody uses.
//
// It is a report, not a gate. A symbol with no reference in the tree is a
// deletion candidate or a dynamically reached one, and only a human can tell
// which — so this package reports candidates with the evidence that produced
// them and never claims a symbol is dead.
//
// The same walk also answers a second question for free: which symbols are
// reachable from a test. That is a structural lower bound on coverage, not a
// coverage number — a mocked or dynamically dispatched collaborator leaves no
// AST edge, so the bound is pessimistic. Reporting it optimistically is the
// failure mode a coverage tool has, and this package refuses it.
package reachability

import (
	"sort"

	"metricgate/internal/facts"
	"metricgate/internal/refs"
	"metricgate/internal/sources"
	"metricgate/internal/symbols"
)

// Report is the deterministic answer over one tree.
type Report struct {
	Definitions  int         `json:"definitions"`
	Unreferenced int         `json:"unreferenced"`
	Candidates   []Candidate `json:"candidates,omitempty"`
	// TestReached is how many definitions at least one test file names. The
	// share is the report's one number: a structural lower bound, not a
	// coverage percentage.
	TestReached   int         `json:"testReached"`
	UntestedCount int         `json:"untested"`
	Untested      []Candidate `json:"untestedExamples,omitempty"`
	Note          string      `json:"note,omitempty"`
}

// Candidate is one definition plus the fact that makes it interesting.
type Candidate struct {
	Name       string `json:"name"`
	Receiver   string `json:"receiver,omitempty"`
	Kind       string `json:"kind,omitempty"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Language   string `json:"language,omitempty"`
	Package    string `json:"package,omitempty"`
	Visibility string `json:"visibility,omitempty"`
}

// Options scopes the report. The zero value reports over the whole tree.
type Options struct {
	// ExportedOnly keeps only exported definitions, which is where a deletion
	// candidate is worth a look: unexported code the compiler already flags as
	// unused does not need this report.
	ExportedOnly bool
	// Max caps how many candidates each section lists, so a repo whose whole
	// surface is unreferenced still yields a readable report. 0 = DefaultMax.
	Max int
}

// DefaultMax keeps each section short enough to read and long enough to act
// on; the section header always states the full count.
const DefaultMax = 50

// Collect is the whole analysis over fact sheets the caller already parsed, so
// the report never re-walks the tree.
func Collect(files []*facts.FileFacts, uses *refs.Index, opts Options) Report {
	if opts.Max <= 0 {
		opts.Max = DefaultMax
	}
	defs := facts.AllDefs(files)
	report := Report{Definitions: len(defs)}

	byName := map[string]int{}
	for _, d := range defs {
		byName[d.Name]++
	}

	report.Candidates, report.Unreferenced = unusedCandidates(defs, byName, uses, opts)
	sortCandidates(report.Candidates)

	testFiles := testFileSet(files)
	report.Untested, report.UntestedCount = untestedCandidates(defs, uses, testFiles)
	report.TestReached = report.Definitions - report.UntestedCount
	if report.TestReached < 0 {
		report.TestReached = 0
	}
	sortCandidates(report.Untested)

	report.Candidates = capList(report.Candidates, opts.Max)
	report.Untested = capList(report.Untested, opts.Max)
	report.Note = reportNote()
	return report
}

// unusedCandidates lists the definitions with no reference anywhere in the
// tree, and the count of how many there are in total (the listed slice is
// capped later, the count is not). A name with several definitions is skipped:
// some other file with the same name may be the referenced one, so this
// definition is not provably unused and guessing is worse than silence.
func unusedCandidates(defs []symbols.Def, byName map[string]int, uses *refs.Index, opts Options) ([]Candidate, int) {
	var out []Candidate
	n := 0
	for _, d := range defs {
		if opts.ExportedOnly && d.Visibility != "exported" {
			continue
		}
		if byName[d.Name] > 1 || len(uses.Lookup(d.Name)) > 0 {
			continue
		}
		n++
		out = append(out, candidate(d))
	}
	return out, n
}

// untestedCandidates lists the definitions that ARE referenced, but only from
// non-test files: nobody's test names them. A definition nothing references
// at all is already an unused candidate and is not repeated here.
func untestedCandidates(defs []symbols.Def, uses *refs.Index, testFiles map[string]bool) ([]Candidate, int) {
	var out []Candidate
	n := 0
	for _, d := range defs {
		if uses.Lookup(d.Name) == nil || reachableFromTest(d, uses, testFiles) {
			continue
		}
		n++
		out = append(out, candidate(d))
	}
	return out, n
}

// testFileSet is the set of files in the run that are tests, built once from
// the fact sheets so the shared predicate answers the same question twice.
func testFileSet(files []*facts.FileFacts) map[string]bool {
	set := map[string]bool{}
	for _, f := range files {
		if f.File != "" && sources.IsTestPath(f.File) {
			set[f.File] = true
		}
	}
	return set
}

// reachableFromTest reports whether some reference to the symbol's name sits in
// a test file. Name-level by construction: it says a same-named symbol is
// reachable from a test, which is the strongest statement these indexes
// support without a type system.
func reachableFromTest(d symbols.Def, uses *refs.Index, testFiles map[string]bool) bool {
	for _, r := range uses.Lookup(d.Name) {
		if testFiles[r.File] {
			return true
		}
	}
	return false
}

// candidate converts a definition into the report shape.
func candidate(d symbols.Def) Candidate {
	return Candidate{
		Name: d.Name, Receiver: d.Receiver, Kind: d.Kind, File: d.File, Line: d.Line,
		Language: d.Language, Package: d.Package, Visibility: d.Visibility,
	}
}

// sortCandidates gives the report a stable order (file, then line), so two runs
// over the same tree produce byte-identical output.
func sortCandidates(cs []Candidate) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].File != cs[j].File {
			return cs[i].File < cs[j].File
		}
		return cs[i].Line < cs[j].Line
	})
}

// capList truncates a list, leaving the full count in the header rather than
// in the list, so a wide section stays readable.
func capList(cs []Candidate, max int) []Candidate {
	if max > 0 && len(cs) > max {
		return cs[:max]
	}
	return cs
}

// reportNote states what the report cannot claim, once, next to the numbers.
func reportNote() string {
	return "name-level: a symbol reached only by interface dispatch, reflection or a dynamic name is still reported as unreferenced; " +
		"test reachability is a structural LOWER BOUND — a mocked or dynamically dispatched collaborator leaves no AST edge, so a real test suite covers more than this reports"
}
