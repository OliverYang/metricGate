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
	if rs := Extract("x", []byte(goSample), tree, parser.LanguageID("rust")); len(rs) != 0 {
		t.Errorf("unsupported language must yield no refs, got %+v", rs)
	}
}
