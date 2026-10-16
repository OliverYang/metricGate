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

// TestVBStubs covers the grammar with no body node: "empty" is the absence of
// `statement` children, and the placeholder is a Throw, not a panic.
func TestVBStubs(t *testing.T) {
	src := `Module M
    Function Empty() As Integer
    End Function

    Sub Todo()
        Throw New NotImplementedException()
    End Sub
End Module
`
	items := detect(t, parser.LangVB, src)
	if len(items) != 2 {
		t.Fatalf("detected %d vb stubs, want 2: %+v", len(items), items)
	}
	if items[0].Kind != KindEmpty || items[0].Symbol != "Empty" {
		t.Errorf("first = %+v, want an empty body named Empty", items[0])
	}
	if items[1].Kind != KindUnsupported || items[1].Detail != "NotImplementedException" {
		t.Errorf("second = %+v, want a NotImplementedException placeholder", items[1])
	}
}

// TestVerilogStubs covers the grammar whose declarations have no fields at
// all, so the symbol name has to come from a nested identifier node.
func TestVerilogStubs(t *testing.T) {
	src := `module m;
  function automatic int nothing();
  endfunction

  function automatic int later();
    $fatal("not implemented");
  endfunction
endmodule
`
	items := detect(t, parser.LangVerilog, src)
	if len(items) != 2 {
		t.Fatalf("detected %d verilog stubs, want 2: %+v", len(items), items)
	}
	if items[0].Kind != KindEmpty || items[0].Symbol != "nothing" {
		t.Errorf("first = %+v, want an empty body named nothing", items[0])
	}
	if items[1].Kind != KindUnsupported || items[1].Detail != "$fatal" {
		t.Errorf("second = %+v, want a $fatal placeholder", items[1])
	}
}

// TestSQLHasNoStubSurface pins the deliberate gap: the SQL grammar has no
// throw equivalent and its PL/pgSQL RAISE form does not parse, so the pass
// reports nothing rather than inventing a signal.
func TestSQLHasNoStubSurface(t *testing.T) {
	src := `CREATE FUNCTION later() RETURNS int AS $$
  -- TODO: implement
  SELECT NULL;
$$ LANGUAGE sql;
`
	if items := detect(t, parser.LangSQL, src); len(items) != 0 {
		t.Errorf("sql reported %d stubs, want 0: %+v", len(items), items)
	}
}
