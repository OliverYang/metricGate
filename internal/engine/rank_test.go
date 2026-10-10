package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// rankFixture writes one candidate file and returns its path.
func rankFixture(t *testing.T, dir, name, src string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const rankClean = `package a

// Add sums two ints. Small, no violations.
func Add(x, y int) int { return x + y }
`

const rankDirty = `package a

import "fmt"

// Dirty has a violation: direct print (banned-api in policy fixtures).
func Dirty(xs []int) {
	for _, x := range xs {
		if x > 0 {
			if x%2 == 0 {
				if x > 10 {
					fmt.Println("big even", x)
				}
			}
		}
	}
	fmt.Println("done")
}
`

func TestRankCandidatesOrdersBetterFirst(t *testing.T) {
	dir := t.TempDir()
	clean := rankFixture(t, dir, "a_clean.go", rankClean)
	dirty := rankFixture(t, dir, "b_dirty.go", rankDirty)

	e, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	got := e.RankCandidates([]string{dirty, clean})
	if len(got) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(got))
	}
	if got[0].Name != clean {
		t.Errorf("want clean candidate first, got %s (errs=%d warns=%d) then %s (errs=%d warns=%d)",
			got[0].Name, got[0].Errors, got[0].Warns, got[1].Name, got[1].Errors, got[1].Warns)
	}
	if got[0].Errors != 0 && got[1].Errors <= got[0].Errors {
		t.Errorf("rank keys: first errs=%d, second errs=%d — first must not be worse", got[0].Errors, got[1].Errors)
	}
	if got[0].Summary.Files != 1 {
		t.Errorf("per-candidate summary must cover exactly its file, got Files=%d", got[0].Summary.Files)
	}
}

func TestRankCandidatesDeterministicTiebreak(t *testing.T) {
	dir := t.TempDir()
	f1 := rankFixture(t, dir, "z_first.go", rankClean)
	f2 := rankFixture(t, dir, "a_second.go", rankClean)
	e, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for i := 0; i < 3; i++ {
		got := e.RankCandidates([]string{f1, f2})
		if got[0].Name != f2 || got[1].Name != f1 {
			t.Fatalf("run %d: want alphabetical tiebreak %s first, got %s", i, f2, got[0].Name)
		}
	}
}

func TestRankCandidatesDeterministicJSON(t *testing.T) {
	dir := t.TempDir()
	clean := rankFixture(t, dir, "a_clean.go", rankClean)
	dirty := rankFixture(t, dir, "b_dirty.go", rankDirty)
	e, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	first := e.RankCandidates([]string{clean, dirty})
	// Fresh engine instance to mimic a second CLI invocation, not a reused one.
	e2, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	second := e2.RankCandidates([]string{clean, dirty})
	for i := range first {
		if first[i].Name != second[i].Name || first[i].Errors != second[i].Errors || first[i].Warns != second[i].Warns {
			t.Fatalf("run-to-run rank drift at %d: %+v vs %+v", i, first[i], second[i])
		}
		if len(first[i].Summary.Findings) != len(second[i].Summary.Findings) ||
			len(first[i].Summary.Metrics) != len(second[i].Summary.Metrics) {
			t.Fatalf("run-to-run summary drift at %d", i)
		}
	}
}
