package impact

import (
	"reflect"
	"slices"
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
