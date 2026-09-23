package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/symbols"
)

// writeTree lays out a tiny repo inside one temp dir: Run exists in
// engine.go, the same file invents Rneder (a one-key typo of Render, which
// lives in facts/render.go), and a Java class carries a method.
func writeTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, src string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(dir+"/"+rel), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir+"/"+rel, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/engine/engine.go", `package engine

func Run(root string) (*Summary, error) {
	if root == "" {
		root = "."
	}
	return &Summary{}, nil
}

func Rneder(files []*File) string {
	total := 0
	for range files {
		total++
		total++
		total++
		total++
	}
	return ""
}
`)
	write("internal/facts/render.go", `package facts

func Render(files []*File, cfg *Config, maxChars int) string {
	return ""
}
`)
	write("Store.java", `package a;

public class Store {
    public int size() { return 0; }
}
`)
	return dir
}

func collectTree(t *testing.T, dir string) ([]*FileFacts, *symbols.Index) {
	t.Helper()
	files, ix, err := CollectWithIndex(dir)
	if err != nil {
		t.Fatal(err)
	}
	return files, ix
}

func TestDefsFlatten(t *testing.T) {
	files, ix := collectTree(t, writeTree(t))
	if ix.Len() == 0 {
		t.Fatal("empty index")
	}
	// Java class members surface as standalone defs with the receiver set.
	var sizeDef *symbols.Def
	for _, f := range files {
		for i, d := range Defs(f) {
			if d.Name == "size" {
				sizeDef = &Defs(f)[i]
			}
		}
	}
	if sizeDef == nil || sizeDef.Receiver != "Store" {
		t.Errorf("java method def missing or receiver != Store")
	}
}

func TestLookupAcrossFiles(t *testing.T) {
	_, ix := collectTree(t, writeTree(t))
	defs := ix.Lookup("Render")
	if len(defs) != 1 || !strings.HasSuffix(defs[0].File, "render.go") {
		t.Errorf("Lookup(Render) = %v", defs)
	}
	// The Go method and the Java method with the same name are distinct
	// defs, ordered by file.
	if defs := ix.Lookup("Run"); len(defs) != 1 {
		t.Errorf("Lookup(Run) = %d defs, want 1", len(defs))
	}
}

func TestRenderHintsInPack(t *testing.T) {
	dir := writeTree(t)
	files, _ := collectTree(t, dir)
	cfg, err := config.Load([]byte("version: 1"))
	if err != nil {
		t.Fatal(err)
	}
	pack := Render(files, cfg, 20000)
	if !strings.Contains(pack, "## existing symbols (align, do not reinvent)") {
		t.Fatalf("context pack lacks the symbol hints section:\n%s", pack)
	}
	// The one-key typo twin (Rneder -> Render) surfaces, pointing at the
	// file to align with.
	if !strings.Contains(pack, "similar:") {
		t.Errorf("pack lacks the near-miss hint:\n%s", pack)
	}
}

func TestHintPointsAtTheOtherFile(t *testing.T) {
	dir := writeTree(t)
	files, ix := collectTree(t, dir)
	hints := SimilarSymbols(files, ix, 3)
	if len(hints) == 0 {
		t.Fatal("no hints produced")
	}
	for _, h := range hints {
		// Every hint names a definition site and an action.
		if !strings.Contains(h, ".go:") {
			t.Errorf("hint lacks a definition site: %q", h)
		}
		if !strings.Contains(h, "reuse") && !strings.Contains(h, "align") {
			t.Errorf("hint lacks the action: %q", h)
		}
	}
}
