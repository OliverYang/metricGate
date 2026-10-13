package impact

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"metricgate/internal/refs"
	"metricgate/internal/symbols"
)

// computeFixture builds one report over a mixed input: a known definition, two
// calls to it (out of file order), one type use of a different name, and one
// call to a name nobody defines. The assertions read as one clause each, so
// the test's complexity stays flat as cases are added.
func computeFixture() Report {
	defs := symbols.Build([]symbols.Def{
		{Name: "Lookup", Kind: "method", Receiver: "Index", File: "symbols/index.go", Line: 94},
	})
	uses := refs.Build([]refs.Ref{
		{Name: "Lookup", Kind: refs.KindCall, File: "b.go", Line: 3, Receiver: "ix"},
		{Name: "Lookup", Kind: refs.KindCall, File: "a.go", Line: 9, Receiver: "ix"},
		{Name: "Index", Kind: refs.KindType, File: "a.go", Line: 1},
		{Name: "Other", Kind: refs.KindCall, File: "z.go", Line: 1},
	})
	return Compute("Lookup", defs, uses)
}

func TestComputeJoinsDefinitionsAndReferences(t *testing.T) {
	r := computeFixture()

	wantDefs := []symbols.Def{{Name: "Lookup", Kind: "method", Receiver: "Index", File: "symbols/index.go", Line: 94}}
	if !reflect.DeepEqual(r.Definitions, wantDefs) {
		t.Errorf("definitions = %+v", r.Definitions)
	}
	if r.Calls != 2 {
		t.Errorf("calls = %d, want 2", r.Calls)
	}
	if r.TypeUses != 0 {
		t.Errorf("typeUses = %d, want 0 (the type use is of Index, not Lookup)", r.TypeUses)
	}
	if len(r.References) != 2 {
		t.Errorf("references = %+v, want exactly the two Lookup calls", r.References)
	}
}

func TestComputeSortsDedupesAndFlagsConfidence(t *testing.T) {
	r := computeFixture()

	if want := []string{"a.go", "b.go"}; !slices.Equal(r.AffectedFiles, want) {
		t.Errorf("affectedFiles = %v, want %v (sorted, deduped)", r.AffectedFiles, want)
	}
	if r.Confidence != ConfidenceASTOnly {
		t.Errorf("confidence = %q, want %q", r.Confidence, ConfidenceASTOnly)
	}
	if r.Note != "" {
		t.Errorf("note = %q, want empty for a complete answer", r.Note)
	}
}

func TestComputeNotesWhatItCannotSee(t *testing.T) {
	empty := Compute("Ghost", symbols.Build(nil), refs.Build(nil))
	if empty.Note == "" {
		t.Error("a totally unknown name must carry an explanatory note")
	}

	defOnly := Compute("Solo", symbols.Build([]symbols.Def{{Name: "Solo", File: "a.go", Line: 1}}), refs.Build(nil))
	if defOnly.Note == "" || len(defOnly.AffectedFiles) != 0 {
		t.Errorf("definition without references: note=%q files=%v", defOnly.Note, defOnly.AffectedFiles)
	}

	useOnly := Compute("Ext", symbols.Build(nil), refs.Build([]refs.Ref{{Name: "Ext", Kind: refs.KindCall, File: "a.go", Line: 2}}))
	if useOnly.Note == "" || len(useOnly.Definitions) != 0 {
		t.Errorf("external symbol: note=%q defs=%v", useOnly.Note, useOnly.Definitions)
	}
}

// chainFixture models a three-link chain: main calls entry, entry calls leaf.
// Reference sites carry Enclosing, which is what the second hop reads.
func chainFixture() (*symbols.Index, *refs.Index) {
	defs := symbols.Build([]symbols.Def{
		{Name: "entry", Kind: "function", File: "a.go", Line: 5},
	})
	uses := refs.Build([]refs.Ref{
		{Name: "entry", Kind: refs.KindCall, File: "a.go", Line: 12, Enclosing: "main"},
		{Name: "leaf", Kind: refs.KindCall, File: "b.go", Line: 3, Enclosing: "entry"},
		{Name: "main", Kind: refs.KindCall, File: "c.go", Line: 8, Enclosing: "boot"},
	})
	return defs, uses
}

func TestComputeDefaultDepthIsUnchanged(t *testing.T) {
	defs, uses := chainFixture()

	one := Compute("entry", defs, uses)
	defaulted := ComputeWith("entry", defs, uses, Options{})
	zeroed := ComputeWith("entry", defs, uses, Options{Depth: 1})

	if !reflect.DeepEqual(one, defaulted) {
		t.Error("the zero Options must reproduce Compute exactly")
	}
	if !reflect.DeepEqual(one, zeroed) {
		t.Error("depth 1 must reproduce Compute exactly")
	}
	if one.Depth != 0 || len(one.Reach) != 0 {
		t.Errorf("single hop must not carry transitive fields: depth=%d reach=%d", one.Depth, len(one.Reach))
	}
}

func TestComputeWithDepthReachesCallersOfCallers(t *testing.T) {
	defs, uses := chainFixture()
	r := ComputeWith("entry", defs, uses, Options{Depth: 2})

	// depth 2 sees entry's call site (in main) and therefore main's own call
	// site (in boot), which the single hop structurally cannot express.
	if len(r.Reach) != 1 || r.Reach[0].Name != "main" || r.Reach[0].Depth != 2 {
		t.Fatalf("reach = %+v, want just main at depth 2", r.Reach)
	}
	want := []string{"a.go", "c.go"}
	if !slices.Equal(r.AffectedFiles, want) {
		t.Errorf("affectedFiles = %v, want %v (entry's file plus main's caller's file)", r.AffectedFiles, want)
	}
}

// The reached name's Files are the files of ITS OWN use sites, not the walk so
// far: a scope nothing calls (an entrypoint, a test that is run rather than
// called) has no files, and reporting the accumulated set there would claim
// the closure had stopped expanding when it had not.
func TestComputeWithDepthReportsPerHopFiles(t *testing.T) {
	defs := symbols.Build([]symbols.Def{
		{Name: "seed", File: "a.go", Line: 1},
		{Name: "mid", File: "a.go", Line: 2},
		{Name: "top", File: "c.go", Line: 3},
	})
	uses := refs.Build([]refs.Ref{
		// seed is called from mid, mid is called from top, top is called
		// from nowhere: top's own hop has no use sites.
		{Name: "seed", Kind: refs.KindCall, File: "a.go", Line: 5, Enclosing: "mid"},
		{Name: "mid", Kind: refs.KindCall, File: "b.go", Line: 6, Enclosing: "top"},
	})
	r := ComputeWith("seed", defs, uses, Options{Depth: 3})

	byName := map[string]Reach{}
	for _, reach := range r.Reach {
		byName[reach.Name] = reach
	}
	if got := byName["mid"].Files; !slices.Equal(got, []string{"b.go"}) {
		t.Errorf("mid's files = %v, want its own caller's file b.go", got)
	}
	if got := byName["top"].Files; len(got) != 0 {
		t.Errorf("top's files = %v, want empty (nothing calls top)", got)
	}
	// The walk itself did not stop: the accumulated answer still covers both.
	if !slices.Equal(r.AffectedFiles, []string{"a.go", "b.go"}) {
		t.Errorf("affectedFiles = %v, want [a.go b.go]", r.AffectedFiles)
	}
}

func TestComputeWithDepthTiersEveryReachedName(t *testing.T) {
	defs := symbols.Build([]symbols.Def{
		{Name: "one", File: "a.go", Line: 1},
		{Name: "two", File: "a.go", Line: 2},
		{Name: "two", File: "b.go", Line: 2},
	})
	uses := refs.Build([]refs.Ref{
		{Name: "seed", Kind: refs.KindCall, File: "a.go", Line: 2, Enclosing: "one"},
		{Name: "seed", Kind: refs.KindCall, File: "b.go", Line: 4, Enclosing: "two"},
		{Name: "seed", Kind: refs.KindCall, File: "c.go", Line: 6, Enclosing: "gone"},
		{Name: "one", Kind: refs.KindCall, File: "z.go", Line: 1},
	})
	r := ComputeWith("seed", defs, uses, Options{Depth: 2})

	got := map[string]string{}
	for _, reach := range r.Reach {
		got[reach.Name] = reach.Tier
	}
	want := map[string]string{"one": TierResolved, "two": TierAmbiguous, "gone": TierExternal}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tiers = %v, want %v (one def resolves, a name nobody defines is external)", got, want)
	}
}

func TestComputeWithDepthStopsAtTheWidthCap(t *testing.T) {
	defs, uses := chainFixture()
	wide := ComputeWith("entry", defs, uses, Options{Depth: 4, MaxAffectedFiles: 1})
	if len(wide.AffectedFiles) != 1 || wide.HiddenAffectedFiles == 0 {
		t.Fatalf("cap 1: files=%v hidden=%d, want one kept and a stated remainder", wide.AffectedFiles, wide.HiddenAffectedFiles)
	}
	if wide.Note == "" {
		t.Error("a truncated walk must say in the note that files were not expanded")
	}

	uncapped := ComputeWith("entry", defs, uses, Options{Depth: 4, MaxAffectedFiles: -1})
	if uncapped.HiddenAffectedFiles != 0 {
		t.Errorf("a negative cap disables the limit: hidden=%d", uncapped.HiddenAffectedFiles)
	}
	if len(uncapped.AffectedFiles) <= len(wide.AffectedFiles) {
		t.Error("removing the cap must not shrink the answer")
	}

	defaulted := ComputeWith("entry", defs, uses, Options{Depth: 4})
	if defaulted.HiddenAffectedFiles != 0 {
		t.Errorf("the default cap leaves a three-file chain untouched: hidden=%d", defaulted.HiddenAffectedFiles)
	}
}

func TestComputeWithDepthLabelsAnAmbiguousSeed(t *testing.T) {
	defs := symbols.Build([]symbols.Def{
		{Name: "run", File: "a.go", Line: 1},
		{Name: "run", File: "b.go", Line: 1},
	})
	uses := refs.Build([]refs.Ref{
		{Name: "run", Kind: refs.KindCall, File: "c.go", Line: 1, Enclosing: "outer"},
		{Name: "outer", Kind: refs.KindCall, File: "d.go", Line: 1, Enclosing: "boot"},
	})
	r := ComputeWith("run", defs, uses, Options{Depth: 2})

	if r.Note == "" {
		t.Fatal("a seed with two definitions must be labelled unreliable")
	}
	if !strings.Contains(r.Note, "2 definitions") {
		t.Errorf("note = %q, want the definition count named", r.Note)
	}
}
