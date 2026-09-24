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
