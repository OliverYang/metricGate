package facts

import (
	"os"
	"strings"
	"testing"

	"metricgate/internal/config"
)

// The context pack carries the clone reuse hints, so the LLM sees the
// existing implementation before writing its own (testdata has a clone pair
// written by the shared fixture helper below).
func TestRenderCloneHints(t *testing.T) {
	orig := "package c\n\nfunc Alpha(xs []int) int {\n\tt := 0\n\tfor _, x := range xs {\n\t\tt += x\n\t}\n\tt = t * 2\n\tt = t - 3\n\tt = t * 4\n\tt = t - 5\n\tt = t * 6\n\tt = t - 7\n\treturn t\n}\n"
	twin := "package c\n\nfunc Beta(ys []int) int {\n\tu := 0\n\tfor _, y := range ys {\n\t\tu += y\n\t}\n\tu = u * 2\n\tu = u - 3\n\tu = u * 4\n\tu = u - 5\n\tu = u * 6\n\tu = u - 7\n\treturn u\n}\n"
	dir := t.TempDir()
	write := func(name, src string) {
		t.Helper()
		if err := os.WriteFile(dir+"/"+name, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", orig)
	write("b.go", twin)

	cfg, err := config.Load([]byte(`
version: 1
clones:
  min_tokens: 20
  min_lines: 5
`))
	if err != nil {
		t.Fatal(err)
	}
	files, err := Collect(dir)
	if err != nil {
		t.Fatal(err)
	}
	pack := Render(files, cfg, 20000)
	if !strings.Contains(pack, "## reuse before you generate") {
		t.Fatalf("context pack lacks the reuse section:\n%s", pack)
	}
	if !strings.Contains(pack, "a.go") || !strings.Contains(pack, "do not reimplement") {
		t.Errorf("reuse hint does not point at the anchor:\n%s", pack)
	}
}
