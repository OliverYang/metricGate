package arch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// imp builds File.Imports literals (Import{Path, Line}).
func imp(paths ...string) []Import {
	out := make([]Import, 0, len(paths))
	for _, p := range paths {
		out = append(out, Import{Path: p})
	}
	return out
}

func graphFor(t *testing.T, files []File) *Graph {
	t.Helper()
	g := Build(files)
	if g == nil {
		t.Fatal("Build returned nil graph")
	}
	return g
}

// Three packages: a→b (two files), b→c (one file), c isolated (no edges).
func sampleGraph(t *testing.T) (*Graph, *Snapshot) {
	t.Helper()
	g := graphFor(t, []File{
		{Name: "a/a1.go", Pkg: "a", Imports: imp("b")},
		{Name: "a/a2.go", Pkg: "a", Imports: imp("b")},
		{Name: "b/b.go", Pkg: "b", Imports: imp("c")},
		{Name: "c/c.go", Pkg: "c"},
	})
	return g, Capture(g)
}

func TestCaptureDeterministicAndAggregated(t *testing.T) {
	_, s1 := sampleGraph(t)
	_, s2 := sampleGraph(t)

	b1, err := json.Marshal(s1)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := json.Marshal(s2)
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("Capture not deterministic:\n%s\n%s", b1, b2)
	}

	// a→b declared by two files must aggregate to one edge with count 2.
	want := []SnapshotEdge{
		{From: "a", To: "b", Count: 2},
		{From: "b", To: "c", Count: 1},
	}
	if len(s1.Edges) != len(want) {
		t.Fatalf("edges = %+v, want %+v", s1.Edges, want)
	}
	for i, e := range s1.Edges {
		if e != want[i] {
			t.Errorf("edge[%d] = %+v, want %+v", i, e, want[i])
		}
	}
	// c has no edges but is still a node of the shape.
	if got := s1.Packages; len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("packages = %v, want [a b c]", got)
	}
	if s1.Version != snapshotVersion {
		t.Errorf("version = %d, want %d", s1.Version, snapshotVersion)
	}
}

func TestSnapshotSaveLoadRoundTrip(t *testing.T) {
	_, s := sampleGraph(t)
	path := filepath.Join(t.TempDir(), "arch.json")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := json.Marshal(s)
	b2, _ := json.Marshal(got)
	if string(b1) != string(b2) {
		t.Fatalf("round trip changed the snapshot:\n%s\n%s", b1, b2)
	}
}

func TestLoadSnapshotRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "arch.json")
	old := &Snapshot{Version: snapshotVersion + 999, Packages: []string{"a"}, Edges: nil}
	if err := old.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(path); err == nil {
		t.Fatal("LoadSnapshot accepted an unsupported version")
	}
}

func TestLoadSnapshotMissingFile(t *testing.T) {
	if _, err := LoadSnapshot(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("LoadSnapshot accepted a missing file")
	}
}

func TestCompareNoDrift(t *testing.T) {
	_, s1 := sampleGraph(t)
	_, s2 := sampleGraph(t)
	d := Compare(s1, s2)
	if d.Drifted() {
		t.Fatalf("identical snapshots reported drift: %+v", d)
	}
}

func TestCompareAddedAndRemovedEdges(t *testing.T) {
	_, base := sampleGraph(t)

	// New tree: a now also imports c (edge added); b stops importing c
	// (edge removed); package d appears. b and c stay known (a imports
	// them; both keep their package files).
	g2 := graphFor(t, []File{
		{Name: "a/a1.go", Pkg: "a", Imports: imp("b", "c")},
		{Name: "a/a2.go", Pkg: "a", Imports: imp("b")},
		{Name: "b/b.go", Pkg: "b"},
		{Name: "c/c.go", Pkg: "c"},
		{Name: "d/d.go", Pkg: "d"},
	})
	head := Capture(g2)

	d := Compare(base, head)
	if !d.Drifted() {
		t.Fatal("drift not reported")
	}
	wantAdded := []EdgeDrift{{From: "a", To: "c", Kind: "added", NewN: 1}}
	if len(d.Added) != 1 || d.Added[0] != wantAdded[0] {
		t.Errorf("added = %+v, want %+v", d.Added, wantAdded)
	}
	wantRemoved := []EdgeDrift{{From: "b", To: "c", Kind: "removed", OldN: 1}}
	if len(d.Removed) != 1 || d.Removed[0] != wantRemoved[0] {
		t.Errorf("removed = %+v, want %+v", d.Removed, wantRemoved)
	}
	if len(d.PkgsGone) != 0 {
		t.Errorf("packages_removed = %v, want none", d.PkgsGone)
	}
	if len(d.PkgsNew) != 1 || d.PkgsNew[0] != "d" {
		t.Errorf("packages_added = %v, want [d]", d.PkgsNew)
	}
}

func TestCompareCountOnlyChangeIsNotDrift(t *testing.T) {
	_, base := sampleGraph(t)
	// Same directions, a→b now declared by one more file.
	g2 := graphFor(t, []File{
		{Name: "a/a1.go", Pkg: "a", Imports: imp("b")},
		{Name: "a/a2.go", Pkg: "a", Imports: imp("b")},
		{Name: "a/a3.go", Pkg: "a", Imports: imp("b")},
		{Name: "b/b.go", Pkg: "b", Imports: imp("c")},
		{Name: "c/c.go", Pkg: "c"},
	})
	head := Capture(g2)
	if d := Compare(base, head); d.Drifted() {
		t.Fatalf("count-only change reported as drift: %+v", d)
	}
}

func TestSnapshotFileIsStableJSON(t *testing.T) {
	_, s := sampleGraph(t)
	path := filepath.Join(t.TempDir(), "arch.json")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Deterministic output: save twice, byte-identical.
	path2 := filepath.Join(t.TempDir(), "arch2.json")
	if err := s.Save(path2); err != nil {
		t.Fatal(err)
	}
	data2, _ := os.ReadFile(path2)
	if string(data) != string(data2) {
		t.Fatal("Save is not deterministic")
	}
	if data[len(data)-1] != '\n' {
		t.Error("snapshot file should end with a newline")
	}
}
