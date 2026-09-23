package symbols

import (
	"reflect"
	"testing"
)

func testIndex() *Index {
	return Build([]Def{
		{Name: "Run", File: "internal/engine/engine.go", Line: 90, Kind: "function", Language: "go", Package: "internal/engine", Signature: "func Run(root string) (*RunSummary, error)", Visibility: "exported"},
		{Name: "Runner", File: "internal/rules/rules.go", Line: 20, Kind: "type", Language: "go"},
		{Name: "render", File: "internal/facts/render.go", Line: 48, Kind: "function", Language: "go", Visibility: "private"},
		{Name: "Render", File: "internal/facts/render.go", Line: 38, Kind: "function", Language: "go", Signature: "func Render(files []*FileFacts, cfg *Config, maxChars int) string"},
		{Name: "Parse", File: "internal/parsetest/parsetest.go", Line: 21, Kind: "function", Language: "go"},
		{Name: "pars", File: "legacy/pars.go", Line: 1, Kind: "function", Language: "go"},
	})
}

func TestLookupExact(t *testing.T) {
	ix := testIndex()
	got := ix.Lookup("Render")
	if len(got) != 1 || got[0].File != "internal/facts/render.go" || got[0].Line != 38 {
		t.Errorf("Lookup(Render) = %v", got)
	}
	if ix.Lookup("missing") != nil {
		t.Error("Lookup of a missing name must be empty")
	}
	// Nil-safety: a zero Index answers everything with empty results.
	var nilIx *Index
	if nilIx.Len() != 0 || nilIx.Lookup("x") != nil || nilIx.Similar("x", 5) != nil {
		t.Error("nil index must be safe")
	}
}

func TestSimilarFindsNearMisses(t *testing.T) {
	ix := testIndex()
	// Typo of Render: edit distance 1.
	matches := ix.Similar("Rneder", 5)
	if len(matches) == 0 {
		t.Fatal("a one-key typo must surface Render")
	}
	if matches[0].Def.Name != "Render" {
		t.Errorf("best match = %s, want Render (got %v)", matches[0].Def.Name, matches)
	}
	// Case-insensitive equality also matches, but the exact Parse beats
	// its case-fold twin pars.
	matches = ix.Similar("parse", 5)
	if len(matches) == 0 || matches[0].Def.Name != "Parse" {
		t.Errorf("Similar(parse) best = %+v", matches)
	}
}

func TestTranspositionIsOneEdit(t *testing.T) {
	ix := testIndex()
	// "Rneder" is "Render" with one adjacent key swap: restricted
	// Damerau-Levenshtein scores it 1, so the suggestion is a near-miss,
	// not a loose containment match.
	matches := ix.Similar("Rneder", 5)
	if len(matches) == 0 || matches[0].Def.Name != "Render" || matches[0].Score != scoreEditBase+1 {
		t.Errorf("transposed typo best = %+v, want Render at score %d", matches, scoreEditBase+1)
	}
}

func TestSimilarDeterministicOrder(t *testing.T) {
	ix := testIndex()
	a := ix.Similar("rund", 5)
	b := ix.Similar("rund", 5)
	if !reflect.DeepEqual(a, b) {
		t.Error("Similar must be deterministic between calls")
	}
}

func TestSimilarElsewhereExcludesOwnFile(t *testing.T) {
	ix := testIndex()
	matches := ix.SimilarElsewhere("Render", "internal/facts/render.go", 5)
	for _, m := range matches {
		if m.Def.File == "internal/facts/render.go" {
			t.Errorf("own file leaked into the hints: %+v", m)
		}
	}
	// The same query without the exclusion sees the file's own defs.
	everywhere := ix.Similar("Render", 5)
	ownFound := false
	for _, m := range everywhere {
		if m.Def.File == "internal/facts/render.go" {
			ownFound = true
		}
	}
	if !ownFound {
		t.Error("Similar must include the file's own definitions")
	}
}

func TestSimilarRejectsLooseMatches(t *testing.T) {
	ix := testIndex()
	// One-character query: nothing meaningful can match it.
	if got := ix.Similar("r", 5); len(got) != 0 {
		t.Errorf("one-char query matched %v", got)
	}
	// Distant names stay unmatched.
	if got := ix.Similar("zzzzzzzz", 5); len(got) != 0 {
		t.Errorf("gibberish matched %v", got)
	}
}

func TestBuildSkipsEmptyNames(t *testing.T) {
	ix := Build([]Def{{Name: "", File: "x.go", Line: 1}, {Name: "ok", File: "x.go", Line: 2}})
	if ix.Len() != 1 {
		t.Errorf("Len = %d, want 1 (empty names are skipped)", ix.Len())
	}
}
