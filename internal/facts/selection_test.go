package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metricgate/internal/config"
)

func TestCollectSelectedUsesPolicyExcludesAndCollectStaysCompatible(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package sample\nfunc Keep() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/keep.go")
	write("testdata/fixture.go")
	cfg, err := config.Load([]byte("version: 1\nexclude: [testdata]\n"))
	if err != nil {
		t.Fatal(err)
	}
	selected, err := CollectSelected(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	all, err := Collect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || !strings.HasSuffix(selected[0].File, "src/keep.go") {
		t.Errorf("selected facts = %+v, want only src/keep.go", selected)
	}
	if len(all) != 2 {
		t.Errorf("legacy Collect changed behavior: files=%d, want 2", len(all))
	}
}

// TestCollectPathsAreRootRelative proves the LLM-facing fact sheets never carry
// an absolute machine path: File is root-relative even when the root is absolute.
func TestCollectPathsAreRootRelative(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "pkg", "a.go")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("package pkg\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := Collect(root) // absolute root
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("files = %d, want 1", len(files))
	}
	if filepath.IsAbs(files[0].File) {
		t.Errorf("File is absolute: %q", files[0].File)
	}
	if files[0].File != "pkg/a.go" {
		t.Errorf("File = %q, want pkg/a.go", files[0].File)
	}
	if strings.Contains(files[0].File, root) {
		t.Errorf("fact sheet leaked the root path: %q", files[0].File)
	}
}
