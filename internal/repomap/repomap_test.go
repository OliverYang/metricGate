package repomap

import (
	"encoding/json"
	"testing"

	"metricgate/internal/refs"
	"metricgate/internal/symbols"
)

func fixture() ([]symbols.Def, []refs.Ref) {
	defs := []symbols.Def{
		{Name: "Core", Kind: "function", File: "a.go", Line: 1, Signature: "func Core()"},
		{Name: "Leaf", Kind: "function", File: "b.go", Line: 1, Signature: "func Leaf()"},
		{Name: "Main", Kind: "function", File: "c.go", Line: 1, Signature: "func Main()"},
	}
	uses := []refs.Ref{
		{Name: "Core", Kind: refs.KindCall, File: "c.go", Line: 5},
		{Name: "Core", Kind: refs.KindCall, File: "c.go", Line: 6},
		{Name: "Core", Kind: refs.KindCall, File: "b.go", Line: 5},
	}
	return defs, uses
}

func TestBuildRanksMostReferencedFileFirst(t *testing.T) {
	defs, uses := fixture()
	got := Build(defs, uses, DefaultOptions())
	if len(got) != 3 {
		t.Fatalf("files = %d, want 3", len(got))
	}
	if got[0].File != "a.go" {
		t.Errorf("top file = %s, want a.go (the one everything references)", got[0].File)
	}
	if got[0].Callers != 2 {
		t.Errorf("a.go callers = %d, want 2 (b.go and c.go)", got[0].Callers)
	}
	if got[0].Top[0].Def.Name != "Core" || got[0].Top[0].Uses != 3 {
		t.Errorf("a.go top symbol = %+v, want Core with 3 uses", got[0].Top)
	}
}

// A name defined in two files splits its weight, so a shared name does not
// falsely promote either definition over a genuinely central one.
func TestBuildSplitsWeightAcrossDefinitions(t *testing.T) {
	defs := []symbols.Def{
		{Name: "Dup", File: "a.go", Line: 1},
		{Name: "Dup", File: "b.go", Line: 1},
	}
	uses := []refs.Ref{
		{Name: "Dup", File: "c.go", Line: 1},
		{Name: "Dup", File: "c.go", Line: 2},
	}
	got := Build(defs, uses, DefaultOptions())
	byFile := map[string]FileScore{}
	for _, fs := range got {
		byFile[fs.File] = fs
	}
	if byFile["a.go"].Score != byFile["b.go"].Score {
		t.Errorf("split should be symmetric: a=%.4f b=%.4f", byFile["a.go"].Score, byFile["b.go"].Score)
	}
	if byFile["a.go"].Callers != 1 || byFile["b.go"].Callers != 1 {
		t.Errorf("each definition should have one caller, got a=%d b=%d", byFile["a.go"].Callers, byFile["b.go"].Callers)
	}
}

// The ranking must be byte-stable: map iteration order must never leak into
// the result.
func TestBuildIsDeterministic(t *testing.T) {
	defs, uses := fixture()
	first, err := json.Marshal(Build(defs, uses, DefaultOptions()))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := json.Marshal(Build(defs, uses, DefaultOptions()))
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("run %d differs:\n%s\n%s", i, first, again)
		}
	}
}

func TestRenderRespectsBudget(t *testing.T) {
	defs, uses := fixture()
	scores := Build(defs, uses, DefaultOptions())
	header := "## repo map (ranked by reference importance; read these first)\n" +
		"# name-based and AST-only: same-named symbols in different files count together\n"
	for _, budget := range []int{len(header), 200, 300, 0} {
		out := Render(scores, budget)
		if budget > 0 && len(out) > budget {
			t.Errorf("budget %d: output is %d bytes", budget, len(out))
		}
	}
	// A budget below the header still yields the header, never an empty or
	// truncated line.
	if out := Render(scores, 10); out != header {
		t.Errorf("tiny budget output = %q, want just the header", out)
	}
}

func TestBriefCapsFiles(t *testing.T) {
	defs, uses := fixture()
	out := Brief(Build(defs, uses, DefaultOptions()), 2)
	if n := countLines(out) - 1; n != 2 {
		t.Errorf("brief listed %d files, want 2:\n%s", n, out)
	}
}

func countLines(s string) int {
	n := 0
	for _, r := range s {
		if r == '\n' {
			n++
		}
	}
	return n
}
