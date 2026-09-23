package stubs

import (
	"strings"
	"testing"

	"metricgate/internal/parser"
)

func detect(t *testing.T, lang parser.LanguageID, src string) []Item {
	t.Helper()
	p, err := parser.New(lang)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	tree, err := p.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	return Detect("x."+string(lang), []byte(src), tree, lang)
}

func TestGoStubs(t *testing.T) {
	src := `package x

type S struct{}

func (s S) Empty() {}

func (s S) Placeholder() {
	panic("not implemented")
}

func (s S) Real() int { return 1 }

func WithTODO() {
	panic("TODO: implement")
}
`
	items := detect(t, parser.LangGo, src)
	kinds := map[string]Item{}
	for _, it := range items {
		kinds[it.Symbol] = it
	}
	if got := kinds["S.Empty"]; got.Kind != KindEmpty {
		t.Errorf("S.Empty kind = %q, want %q (all: %v)", got.Kind, KindEmpty, items)
	}
	if got := kinds["S.Placeholder"]; got.Kind != KindPanicStub || !strings.Contains(got.Detail, "not implemented") {
		t.Errorf("S.Placeholder = %+v", got)
	}
	if got := kinds["WithTODO"]; got.Kind != KindPanicStub {
		t.Errorf("WithTODO = %+v", got)
	}
	if _, ok := kinds["S.Real"]; ok {
		t.Error("S.Real must not be reported as a stub")
	}
}

func TestJavaStubs(t *testing.T) {
	src := `package x;

class S {
    void empty() {
    }

    int unsupported() {
        throw new UnsupportedOperationException("later");
    }

    int real() {
        return 1;
    }
}
`
	items := detect(t, parser.LangJava, src)
	if len(items) != 2 {
		t.Fatalf("items = %+v, want 2", items)
	}
	byKind := map[string]bool{}
	for _, it := range items {
		byKind[it.Kind] = true
	}
	if !byKind[KindEmpty] || !byKind[KindUnsupported] {
		t.Errorf("kinds = %v, want %q + %q", byKind, KindEmpty, KindUnsupported)
	}
}
