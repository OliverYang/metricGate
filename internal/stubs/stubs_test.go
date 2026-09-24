package stubs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metricgate/internal/parser"
	"metricgate/internal/parsetest"
)

func detect(t *testing.T, lang parser.LanguageID, src string) []Item {
	t.Helper()
	tree, release := parsetest.Parse(t, lang, src)
	defer release()
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

// TestScanReportsRootRelativePaths guards the egress boundary: the repair
// worklist is copied into agent prompts, so its file paths must be
// machine-independent even when the scanned root is absolute.
func TestScanReportsRootRelativePaths(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "pkg", "s.go")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("package pkg\n\nfunc Todo() {\n\tpanic(\"not implemented\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	items, err := Scan(root) // absolute root
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("no stubs found")
	}
	for _, it := range items {
		if filepath.IsAbs(it.File) {
			t.Errorf("stub file is absolute: %q", it.File)
		}
		if strings.Contains(it.File, root) {
			t.Errorf("stub file leaked the root path: %q", it.File)
		}
	}
}
