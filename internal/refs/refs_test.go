package refs

import (
	"testing"

	"metricgate/internal/parser"
	"metricgate/internal/parsetest"
)

const goSample = `package svc

type Store struct{ n int }

func (s *Store) Get(k string) int { return s.n }

func Run() {
	s := &Store{}
	v := s.Get("a")
	_ = NewThing(v)
}
`

const javaSample = `package com.demo;

public class Svc {
    private Store store = new Store();
    public int get(String k) {
        int v = store.lookup(k);
        return v + Helper.scale(v);
    }
}
`

func extract(t *testing.T, lang parser.LanguageID, src string) []Ref {
	t.Helper()
	tree, release := parsetest.Parse(t, lang, src)
	defer release()
	return Extract("sample", []byte(src), tree, lang)
}

func find(rs []Ref, name, kind string) []Ref {
	var out []Ref
	for _, r := range rs {
		if r.Name == name && r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

func TestGoCallsCarryReceiverAndEnclosing(t *testing.T) {
	rs := extract(t, parser.LangGo, goSample)

	sel := find(rs, "Get", KindCall)
	if len(sel) != 1 {
		t.Fatalf("Get calls = %+v, want exactly 1", sel)
	}
	if sel[0].Receiver != "s" {
		t.Errorf("receiver = %q, want s", sel[0].Receiver)
	}
	if sel[0].Enclosing != "Run" {
		t.Errorf("enclosing = %q, want Run", sel[0].Enclosing)
	}

	bare := find(rs, "NewThing", KindCall)
	if len(bare) != 1 || bare[0].Receiver != "" {
		t.Errorf("bare call = %+v, want one with no receiver", bare)
	}
}

func TestGoDefinitionNamesAreNotReferences(t *testing.T) {
	rs := extract(t, parser.LangGo, goSample)
	// "Get" is a method declaration name (field_identifier) and "Store" is a
	// type_spec name — neither may show up as a use of itself.
	for _, r := range rs {
		if r.Kind == KindType && r.Name == "Store" && r.Line == 3 {
			t.Errorf("type declaration name counted as a reference: %+v", r)
		}
	}
	if len(find(rs, "Store", KindType)) == 0 {
		t.Error("expected at least one real type reference to Store (e.g. &Store{})")
	}
}

func TestJavaCallsAndTypeReferences(t *testing.T) {
	rs := extract(t, parser.LangJava, javaSample)

	lookup := find(rs, "lookup", KindCall)
	if len(lookup) != 1 || lookup[0].Receiver != "store" {
		t.Fatalf("lookup = %+v, want one call on receiver store", lookup)
	}
	if lookup[0].Enclosing != "get" {
		t.Errorf("enclosing = %q, want get", lookup[0].Enclosing)
	}
	if len(find(rs, "scale", KindCall)) != 1 {
		t.Errorf("scale calls = %+v, want 1", find(rs, "scale", KindCall))
	}
	if len(find(rs, "Store", KindType)) == 0 {
		t.Error("expected a type reference to Store (field type / new Store())")
	}
	if len(find(rs, "get", KindCall)) != 0 {
		t.Error("the declaration name get must not be indexed as a call")
	}
}

func TestIndexLookupAndDeterminism(t *testing.T) {
	rs := extract(t, parser.LangGo, goSample)
	ix := Build(rs)
	if ix.Len() != len(rs) {
		t.Errorf("Len = %d, want %d", ix.Len(), len(rs))
	}
	got := ix.Lookup("Get")
	if len(got) != 1 {
		t.Fatalf("Lookup(Get) = %+v", got)
	}
	if ix.Lookup("nope") != nil {
		t.Error("unknown name must return nil")
	}
	if (&Index{}).Len() != 0 || (*Index)(nil).Len() != 0 {
		t.Error("empty/nil index must report 0")
	}
	// A rebuild over the same refs yields the same order.
	again := Build(rs).Lookup("Get")
	if len(again) != 1 || again[0].Line != got[0].Line {
		t.Errorf("rebuild changed the result: %+v vs %+v", again, got)
	}
}

func TestExtractIgnoresUnsupportedLanguage(t *testing.T) {
	tree, release := parsetest.Parse(t, parser.LangGo, goSample)
	defer release()
	if rs := Extract("x", []byte(goSample), tree, parser.LanguageID("fortran")); len(rs) != 0 {
		t.Errorf("unsupported language must yield no refs, got %+v", rs)
	}
}

// TestPythonHeritageIsATypeReference pins the edge this package used to miss
// entirely: a subclass names its bases, and a base is a use of the base type.
// Without it `impact` on a Python repo reported the base class and none of the
// subclasses that would break if it changed.
func TestPythonHeritageIsATypeReference(t *testing.T) {
	src := "class Sub(Base, mixins.Other, metaclass=Meta):\n    pass\n"
	rs := extract(t, parser.LangPy, src)

	bases := find(rs, "Base", KindType)
	if len(bases) != 1 || bases[0].Line != 1 {
		t.Fatalf("Base = %+v, want one type reference on line 1", bases)
	}
	if bases[0].Enclosing != "Sub" {
		t.Errorf("enclosing = %q, want Sub", bases[0].Enclosing)
	}

	// A dotted base is keyed by its own name with the qualifier as receiver,
	// so a lookup by "Other" finds it — matching every other ref in this file.
	other := find(rs, "Other", KindType)
	if len(other) != 1 || other[0].Receiver != "mixins" {
		t.Fatalf("Other = %+v, want one with receiver mixins", other)
	}

	// `metaclass=Meta` is a keyword argument, not a base.
	if got := find(rs, "Meta", KindType); len(got) != 0 {
		t.Errorf("keyword argument counted as a base: %+v", got)
	}
	// The class's own name is a definition, not a use.
	if got := find(rs, "Sub", KindType); len(got) != 0 {
		t.Errorf("class name counted as a reference: %+v", got)
	}
}

func TestTSHeritageCoversExtendsAndImplements(t *testing.T) {
	src := "class Sub extends Base implements IFoo, IBar {}\n"
	rs := extract(t, parser.LangTS, src)

	for _, want := range []string{"Base", "IFoo", "IBar"} {
		got := find(rs, want, KindType)
		if len(got) != 1 || got[0].Line != 1 {
			t.Errorf("%s = %+v, want one type reference on line 1", want, got)
		}
	}
	if got := find(rs, "Sub", KindType); len(got) != 0 {
		t.Errorf("class name counted as a reference: %+v", got)
	}
}

func TestJSHeritageIsATypeReference(t *testing.T) {
	src := "class Sub extends Base {}\n"
	rs := extract(t, parser.LangJS, src)

	got := find(rs, "Base", KindType)
	if len(got) != 1 || got[0].Line != 1 {
		t.Fatalf("Base = %+v, want one type reference on line 1", got)
	}
	if got := find(rs, "Sub", KindType); len(got) != 0 {
		t.Errorf("class name counted as a reference: %+v", got)
	}
}

// TestHeritageSkipsComputedBases guards the other half of the contract: a base
// that is not a plain name must be skipped rather than guessed at, because a
// wrong base fabricates impact that does not exist.
func TestHeritageSkipsComputedBases(t *testing.T) {
	rs := extract(t, parser.LangPy, "class Sub(make_base()):\n    pass\n")
	for _, r := range rs {
		if r.Kind == KindType {
			t.Errorf("computed base produced a type reference: %+v", r)
		}
	}
}
