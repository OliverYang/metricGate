package reachability

import (
	"slices"
	"testing"

	"metricgate/internal/facts"
	"metricgate/internal/refs"
	"metricgate/internal/symbols"
)

// fixture builds fact sheets plus a reference index in the shape the package
// consumes: helpers with receivers, one exported class member, and references
// that live in production files and in a test file.
func fixture() ([]*facts.FileFacts, *refs.Index) {
	files := []*facts.FileFacts{
		{
			File:     "core/a.go",
			Language: "go",
			Package:  "core",
			Symbols: []facts.Symbol{
				{Name: "Used", Kind: "function", Visibility: "exported", StartLine: 3},
				{Name: "Unused", Kind: "function", Visibility: "exported", StartLine: 7},
				{Name: "PrivateUnused", Kind: "function", Visibility: "private", StartLine: 11},
				{Name: "Tested", Kind: "function", Visibility: "exported", StartLine: 15},
				{Name: "Operational", Kind: "function", Visibility: "exported", StartLine: 19},
			},
		},
		{
			File:     "core/a_test.go",
			Language: "go",
			Package:  "core",
			Symbols: []facts.Symbol{
				{Name: "TestUsed", Kind: "function", Visibility: "exported", StartLine: 3},
			},
		},
	}
	uses := refs.Build([]refs.Ref{
		{Name: "Used", Kind: refs.KindCall, File: "cmd/x/main.go", Line: 4, Enclosing: "main"},
		{Name: "Tested", Kind: refs.KindCall, File: "cmd/x/main.go", Line: 9, Enclosing: "main"},
		{Name: "Tested", Kind: refs.KindCall, File: "core/a_test.go", Line: 8, Enclosing: "TestUsed"},
		{Name: "Tested", Kind: refs.KindCall, File: "cmd/x/main.go", Line: 12, Enclosing: "helper"},
		{Name: "Operational", Kind: refs.KindCall, File: "cmd/x/main.go", Line: 14, Enclosing: "main"},
	})
	return files, uses
}

func defNames(ds []symbols.Def) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Name)
	}
	return out
}

func names(cs []Candidate) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func TestCollectReportsUnreferencedAndUntested(t *testing.T) {
	files, uses := fixture()
	r := Collect(files, uses, Options{})

	if r.Definitions != 6 {
		t.Errorf("definitions = %d, want 6", r.Definitions)
	}
	// Used is referenced, Tested is referenced (production and test),
	// Operational only by production code, Unused by nothing, PrivateUnused by
	// nothing but is not exported.
	// Operational IS referenced (only from production), so it is untested but
	// not unreferenced; TestUsed is referenced by nothing at all.
	if got, want := names(r.Candidates), []string{"Unused", "PrivateUnused", "TestUsed"}; !slices.Equal(got, want) {
		t.Errorf("candidates = %v, want %v (sorted by file then line)", got, want)
	}
	if r.Unreferenced != 3 {
		t.Errorf("unreferenced = %d, want 3", r.Unreferenced)
	}

	// Tested is named by a test file, so only Used and Operational are untested.
	if got, want := names(r.Untested), []string{"Used", "Operational"}; !slices.Equal(got, want) {
		t.Errorf("untested = %v, want %v (a test reference removes it)", got, want)
	}
	if r.TestReached != r.Definitions-r.UntestedCount {
		t.Errorf("testReached = %d, want %d (definitions minus untested)", r.TestReached, r.Definitions-r.UntestedCount)
	}
	if r.UntestedCount != 2 {
		t.Errorf("untestedCount = %d, want 2", r.UntestedCount)
	}
}

func TestExportedOnlyNarrowsCandidates(t *testing.T) {
	files, uses := fixture()
	all := Collect(files, uses, Options{})
	exported := Collect(files, uses, Options{ExportedOnly: true})

	if got := names(all.Candidates); !slices.Contains(got, "PrivateUnused") {
		t.Errorf("the default report includes privates: %v", got)
	}
	if got := names(exported.Candidates); slices.Contains(got, "PrivateUnused") {
		t.Errorf("exported-only kept a private symbol: %v", got)
	}
	if exported.Unreferenced >= all.Unreferenced {
		t.Error("exported-only must report strictly fewer candidates")
	}
}

func TestCollectSkipsAmbiguousNames(t *testing.T) {
	// Two files declare the same name, so neither is provably unused: a
	// reference to that name may belong to the other one.
	files := []*facts.FileFacts{
		{File: "a.go", Language: "go", Symbols: []facts.Symbol{{Name: "Twin", Visibility: "exported", StartLine: 2}}},
		{File: "b.go", Language: "go", Symbols: []facts.Symbol{{Name: "Twin", Visibility: "exported", StartLine: 5}}},
	}
	r := Collect(files, refs.Build(nil), Options{})

	if r.Unreferenced != 0 || len(r.Candidates) != 0 {
		t.Errorf("candidates = %v (unreferenced %d), want none for an ambiguous name", r.Candidates, r.Unreferenced)
	}
}

func TestCollectCapsListsButNotCounts(t *testing.T) {
	files := []*facts.FileFacts{{File: "a.go", Language: "go"}}
	for _, n := range []string{"One", "Two", "Three", "Four"} {
		files[0].Symbols = append(files[0].Symbols, facts.Symbol{Name: n, Visibility: "exported", StartLine: len(files[0].Symbols) + 1})
	}
	r := Collect(files, refs.Build(nil), Options{Max: 2})

	if r.Unreferenced != 4 {
		t.Errorf("unreferenced = %d, want the full count 4 even though the list is capped", r.Unreferenced)
	}
	if len(r.Candidates) != 2 {
		t.Errorf("candidates listed = %d, want the cap 2", len(r.Candidates))
	}
	if r.Note == "" {
		t.Error("a capped report must still state its boundary")
	}
}

func TestCollectOnEmptyTree(t *testing.T) {
	r := Collect(nil, refs.Build(nil), Options{})
	if r.Definitions != 0 || len(r.Candidates) != 0 || len(r.Untested) != 0 {
		t.Errorf("empty tree: %+v", r)
	}
}

// TestDefsFlatteningIsHonest pins the fact the report depends on: a member
// nested under a class symbol surfaces as its own definition.
func TestDefsFlatteningIsHonest(t *testing.T) {
	files := []*facts.FileFacts{{
		File:     "a.go",
		Language: "go",
		Symbols: []facts.Symbol{{
			Name: "Svc", Kind: "struct", Visibility: "exported", StartLine: 2,
			Children: []facts.Symbol{{Name: "Run", Kind: "method", Visibility: "exported", StartLine: 3}},
		}},
	}}
	got := defNames(facts.Defs(files[0]))
	want := []string{"Svc", "Run"}
	if !slices.Equal(got, want) {
		t.Errorf("Defs = %v, want %v (members surface as their own definitions)", got, want)
	}
}
