package engine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"metricgate/internal/config"
)

func TestPreviewMatchesRunSelectionWithoutParsing(t *testing.T) {
	root := writeSelectionFixture(t)
	cfg, err := config.Load([]byte(`version: 1
exclude: [".git", "vendor", "node_modules", "testdata"]
`))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	preview, err := eng.Preview(root)
	if err != nil {
		t.Fatal(err)
	}
	assertPreviewCounts(t, preview)
	assertRelativePaths(t, preview)
	assertIncludedOrder(t, preview)
	assertRunMatchesPreview(t, eng, root, preview)
}

func writeSelectionFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("keep/a.go", "package a\nfunc A() {}\n")
	write("keep/B.java", "class B {}\n")
	write("keep/readme.md", "unsupported\n")
	write("testdata/hidden.go", "package hidden\nfunc ( { syntax error\n")
	return root
}

func assertPreviewCounts(t *testing.T, preview *PreviewReport) {
	t.Helper()
	if preview.Candidate != 3 || len(preview.Included) != 2 || len(preview.Unsupported) != 1 || len(preview.PrunedDirs) != 1 {
		t.Fatalf("preview counts = candidates:%d included:%d unsupported:%d pruned:%d; report=%+v",
			preview.Candidate, len(preview.Included), len(preview.Unsupported), len(preview.PrunedDirs), preview)
	}
}

func assertRelativePaths(t *testing.T, preview *PreviewReport) {
	t.Helper()
	for _, d := range preview.Included {
		if filepath.IsAbs(d.Path) {
			t.Errorf("preview path is absolute: %q", d.Path)
		}
	}
}

func assertIncludedOrder(t *testing.T, preview *PreviewReport) {
	t.Helper()
	included := []string{preview.Included[0].Path, preview.Included[1].Path}
	want := []string{"keep/B.java", "keep/a.go"}
	if !reflect.DeepEqual(included, want) {
		t.Errorf("included paths=%v, want %v", included, want)
	}
}

func assertRunMatchesPreview(t *testing.T, eng *Engine, root string, preview *PreviewReport) {
	t.Helper()
	summary, err := eng.Run(root)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Files != len(preview.Included) {
		t.Errorf("Run files=%d, preview included=%d", summary.Files, len(preview.Included))
	}
	if len(summary.ParseErrors) != 0 {
		t.Errorf("excluded syntax error should not be parsed: %v", summary.ParseErrors)
	}
}

func TestPreviewSingleFileRoot(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "one.go")
	if err := os.WriteFile(file, []byte("package one\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load([]byte("version: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	p, err := eng.Preview(file)
	if err != nil {
		t.Fatal(err)
	}
	if p.Candidate != 1 || len(p.Included) != 1 || p.Included[0].Path != "one.go" {
		t.Errorf("single-file preview = %+v", p)
	}
}
