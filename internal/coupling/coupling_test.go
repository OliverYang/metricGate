package coupling

import (
	"reflect"
	"testing"

	"metricgate/internal/arch"
)

// commit builds a history entry from file paths, ignoring line counts: the
// coupling analysis only cares which files moved together.
func commit(paths ...string) Commit {
	c := Commit{}
	for _, p := range paths {
		c.Files = append(c.Files, File{Path: p})
	}
	return c
}

func TestAnalyzeCountsPairsWithinCommit(t *testing.T) {
	commits := []Commit{
		commit("a.go", "b.go"),
		commit("a.go", "b.go"),
		commit("a.go", "b.go"),
	}
	got := Analyze(commits, Options{MinCount: 3})
	if len(got.Pairs) != 1 {
		t.Fatalf("pairs = %+v, want exactly one", got.Pairs)
	}
	p := got.Pairs[0]
	if p.A != "a.go" || p.B != "b.go" || p.Count != 3 {
		t.Errorf("pair = %+v, want a.go<->b.go count 3", p)
	}
}

// The pair is unordered: a<->b and b<->a in different commits is one pair.
func TestAnalyzePairIsUnordered(t *testing.T) {
	got := Analyze([]Commit{commit("a.go", "b.go"), commit("b.go", "a.go")}, Options{MinCount: 2})
	if len(got.Pairs) != 1 || got.Pairs[0].Count != 2 {
		t.Errorf("pairs = %+v, want one pair counted twice", got.Pairs)
	}
}

// A commit touching more files than the threshold is a sweep (reformat,
// rename, dependency bump) and contributes no pairs; it is counted so the
// caller can say so.
func TestAnalyzeSkipsSweepCommits(t *testing.T) {
	sweep := commit("a.go", "b.go", "c.go", "d.go")
	got := Analyze([]Commit{sweep}, Options{MaxCommitFiles: 3, MinCount: 1})
	if len(got.Pairs) != 0 {
		t.Errorf("pairs = %+v, want none from a sweep", got.Pairs)
	}
	if got.SkippedSweeps != 1 {
		t.Errorf("SkippedSweeps = %d, want 1", got.SkippedSweeps)
	}
}

func TestAnalyzeExcludesGeneratedAndMirroredFiles(t *testing.T) {
	got := Analyze([]Commit{commit("go.sum", "package-lock.json", "app.go", "application-dev.yml")}, Options{MinCount: 1})
	if len(got.Pairs) != 0 {
		t.Errorf("pairs = %+v, want none (all excluded or alone)", got.Pairs)
	}
	if got.SkippedFiles != 3 {
		t.Errorf("SkippedFiles = %d, want 3 (go.sum, lock, mirrored config)", got.SkippedFiles)
	}
}

// Project-specific exclusions add to the defaults rather than replacing them.
func TestAnalyzeProjectExcludeAddsToDefaults(t *testing.T) {
	commits := []Commit{commit("api.pb.go", "custom_gen.go", "real.go")}
	got := Analyze(commits, Options{MinCount: 1, Exclude: []string{"**/*_gen.go"}})
	if len(got.Pairs) != 0 {
		t.Errorf("pairs = %+v, want none", got.Pairs)
	}
	if got.SkippedFiles != 2 {
		t.Errorf("SkippedFiles = %d, want 2", got.SkippedFiles)
	}
}

func TestAnalyzeMinCountFloor(t *testing.T) {
	commits := []Commit{commit("a.go", "b.go"), commit("a.go", "b.go")}
	if got := Analyze(commits, Options{MinCount: 3}); len(got.Pairs) != 0 {
		t.Errorf("pairs = %+v, want none below the floor", got.Pairs)
	}
}

// A path reported twice in one commit must not be paired with itself.
func TestAnalyzeDeduplicatesRepeatedPath(t *testing.T) {
	dup := Commit{Files: []File{{Path: "a.go"}, {Path: "a.go"}}}
	got := Analyze([]Commit{dup}, Options{MinCount: 1})
	if len(got.Pairs) != 0 {
		t.Errorf("pairs = %+v, want none", got.Pairs)
	}
}

func TestAnalyzeOrdersByCountThenPath(t *testing.T) {
	commits := []Commit{
		commit("b.go", "c.go"), commit("b.go", "c.go"),
		commit("a.go", "d.go"), commit("a.go", "d.go"), commit("a.go", "d.go"),
	}
	got := Analyze(commits, Options{MinCount: 1})
	if len(got.Pairs) != 2 || got.Pairs[0].A != "a.go" || got.Pairs[1].A != "b.go" {
		t.Errorf("pairs = %+v, want a.go first (higher count)", got.Pairs)
	}
}

func TestAnalyzeMaxCap(t *testing.T) {
	commits := []Commit{commit("a.go", "b.go"), commit("a.go", "c.go"), commit("b.go", "c.go")}
	if got := Analyze(commits, Options{MinCount: 1, Max: 2}); len(got.Pairs) != 2 {
		t.Errorf("pairs = %d, want capped at 2", len(got.Pairs))
	}
}

// CrossLayer needs two assigned, different layers: an unassigned file is
// unknown, not a boundary crossing.
func TestCrossLayerRequiresTwoAssignedLayers(t *testing.T) {
	layers := []arch.Layer{
		{Name: "cli", Packages: []string{"cmd/**"}},
		{Name: "core", Packages: []string{"internal/core/**"}},
	}
	cases := []struct {
		name       string
		commit     Commit
		wantCross  bool
		wantLayers [2]string
	}{
		{"different layers", commit("cmd/a.go", "internal/core/b.go"), true, [2]string{"cli", "core"}},
		{"same layer", commit("internal/core/a.go", "internal/core/b.go"), false, [2]string{"core", "core"}},
		{"one unassigned", commit("cmd/a.go", "docs/b.md"), false, [2]string{"cli", ""}},
		{"both unassigned", commit("docs/a.md", "docs/b.md"), false, [2]string{"", ""}},
	}
	for _, tc := range cases {
		got := Analyze([]Commit{tc.commit}, Options{MinCount: 1, LayerOf: LayerResolver(layers)})
		if len(got.Pairs) != 1 {
			t.Fatalf("%s: pairs = %+v, want one", tc.name, got.Pairs)
		}
		if got.Pairs[0].CrossLayer != tc.wantCross || got.Pairs[0].Layers != tc.wantLayers {
			t.Errorf("%s: cross=%v layers=%v, want cross=%v layers=%v",
				tc.name, got.Pairs[0].CrossLayer, got.Pairs[0].Layers, tc.wantCross, tc.wantLayers)
		}
	}
}

// Without a resolver the analysis still ranks pairs, just without layer tags.
func TestAnalyzeWithoutLayerResolver(t *testing.T) {
	got := Analyze([]Commit{commit("cmd/a.go", "internal/core/b.go")}, Options{MinCount: 1})
	if len(got.Pairs) != 1 || got.Pairs[0].CrossLayer {
		t.Errorf("pairs = %+v, want one untagged pair", got.Pairs)
	}
}

// CrossLayerOnly is the gate's view: the ranking is filtered down to the pairs
// that actually cross a boundary, so the caller never has to re-filter.
func TestCrossLayerOnlyKeepsBoundaryCrossings(t *testing.T) {
	layers := []arch.Layer{
		{Name: "cli", Packages: []string{"cmd/**"}},
		{Name: "core", Packages: []string{"internal/core/**"}},
	}
	commits := []Commit{
		commit("cmd/a.go", "internal/core/b.go"),           // crosses cli -> core
		commit("internal/core/c.go", "internal/core/d.go"), // same layer
		commit("docs/a.md", "docs/b.md"),                   // unassigned: unknown, not a crossing
	}
	opts := Options{MinCount: 1, LayerOf: LayerResolver(layers)}
	if all := Analyze(commits, opts); len(all.Pairs) != 3 {
		t.Fatalf("unfiltered pairs = %d, want 3", len(all.Pairs))
	}
	opts.CrossLayerOnly = true
	got := Analyze(commits, opts)
	if len(got.Pairs) != 1 {
		t.Fatalf("cross-layer pairs = %+v, want only the cli -> core pair", got.Pairs)
	}
	if got.Pairs[0].A != "cmd/a.go" || !got.Pairs[0].CrossLayer {
		t.Errorf("pair = %+v, want the cli -> core crossing", got.Pairs[0])
	}
}

func TestPackageOf(t *testing.T) {
	cases := map[string]string{
		"internal/coupling/coupling.go": "internal/coupling",
		"cmd/metricgate/main.go":        "cmd/metricgate",
		"main.go":                       "",
		"":                              "",
	}
	for in, want := range cases {
		if got := PackageOf(in); got != want {
			t.Errorf("PackageOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLayerResolverFirstMatchWins(t *testing.T) {
	layers := []arch.Layer{
		{Name: "first", Packages: []string{"internal/**"}},
		{Name: "second", Packages: []string{"internal/coupling"}},
	}
	r := LayerResolver(layers)
	if got := r("internal/coupling"); got != "first" {
		t.Errorf("resolver = %q, want the first matching layer", got)
	}
	if got := r(""); got != "" {
		t.Errorf("resolver(empty) = %q, want empty", got)
	}
}

func TestSummarizeMarksCrossLayer(t *testing.T) {
	lines := Summarize([]Pair{
		{A: "cmd/a.go", B: "internal/b.go", Count: 7, Layers: [2]string{"cli", "core"}, CrossLayer: true},
		{A: "x.go", B: "y.go", Count: 3},
	})
	want := []string{
		"   7  cmd/a.go  <->  internal/b.go  [cross-layer: cli -> core]",
		"   3  x.go  <->  y.go",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("Summarize = %#v, want %#v", lines, want)
	}
}
