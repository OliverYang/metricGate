package clonedrift

import (
	"os"
	"path/filepath"
	"testing"

	"metricgate/internal/clones"
	"metricgate/internal/diff"
)

// bodyA/bodyB are Type-2 clones (same shape, renamed identifiers) large enough
// to clear the default 10-line / 60-token gates. editedB breaks the clone.
const bodyA = `package c

func Alpha(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	total = total * 2
	total = total - 3
	total = total * 4
	total = total - 5
	total = total * 6
	total = total - 7
	total = total * 8
	total = total - 9
	total = total * 10
	total = total - 11
	return total
}
`

const bodyB = `package c

func Beta(ys []int) int {
	sum := 0
	for _, y := range ys {
		sum += y
	}
	sum = sum * 2
	sum = sum - 3
	sum = sum * 4
	sum = sum - 5
	sum = sum * 6
	sum = sum - 7
	sum = sum * 8
	sum = sum - 9
	sum = sum * 10
	sum = sum - 11
	return sum
}
`

const editedB = `package c

func Beta(ys []int) int {
	sum := 0
	for _, y := range ys {
		sum += y
	}
	sum = sum * 2
	sum = sum - 3
	sum = sum * 4
	sum = sum - 5
	sum = sum * 6
	sum = sum - 7
	sum = sum * 8
	sum = sum - 9
	sum = sum * 10
	sum = sum - 11
	sum = sum + 99
	return sum
}
`

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// files lists the tree's own written files in sorted order, replacing the
// sources.Files call the production code moved to the composition root: a
// core-layer test must not depend on the adapter layer either.
func files(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		panic(err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".go" {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

func TestDetectFlagsDriftWhenOneSideEdited(t *testing.T) {
	base := writeTree(t, map[string]string{"a.go": bodyA, "b.go": bodyB})
	work := writeTree(t, map[string]string{"a.go": bodyA, "b.go": editedB})
	got, err := Detect(work, base, files(base), files(work), map[string][]diff.Range{"b.go": {{Start: 1, End: 40}}}, clones.Default)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("findings = %+v, want exactly 1 drift", got)
	}
	f := got[0]
	if f.File != "b.go" || f.TwinFile != "a.go" || f.Name != "Beta" || f.TwinName != "Alpha" {
		t.Errorf("drift anchor = %+v, want b.go/Beta -> a.go/Alpha", f)
	}
}

func TestDetectIgnoresUntouchedPairs(t *testing.T) {
	base := writeTree(t, map[string]string{"a.go": bodyA, "b.go": bodyB})
	work := writeTree(t, map[string]string{"a.go": bodyA, "b.go": bodyB})
	got, err := Detect(work, base, files(base), files(work), map[string][]diff.Range{"b.go": {{Start: 5, End: 6}}}, clones.Default)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("identical copies reported as drift: %+v", got)
	}
}

func TestDetectIgnoresSymmetricEdit(t *testing.T) {
	base := writeTree(t, map[string]string{"a.go": bodyA, "b.go": bodyB})
	work := writeTree(t, map[string]string{"a.go": bodyA + "\nvar keepA = 1\n", "b.go": bodyB + "\nvar keepB = 1\n"})
	changed := map[string][]diff.Range{"a.go": {{Start: 1, End: 40}}, "b.go": {{Start: 1, End: 40}}}
	got, err := Detect(work, base, files(base), files(work), changed, clones.Default)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("symmetric edit reported as drift: %+v", got)
	}
}

func TestDetectIgnoresDeletion(t *testing.T) {
	base := writeTree(t, map[string]string{"a.go": bodyA, "b.go": bodyB})
	work := writeTree(t, map[string]string{"a.go": bodyA})
	got, err := Detect(work, base, files(base), files(work), map[string][]diff.Range{"b.go": {{Start: 1, End: 20}}}, clones.Default)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("deletion reported as drift: %+v", got)
	}
}

func TestDetectDisabledConfig(t *testing.T) {
	base := writeTree(t, map[string]string{"a.go": bodyA, "b.go": bodyB})
	work := writeTree(t, map[string]string{"a.go": bodyA, "b.go": editedB})
	// Enabled() is MinLines>0 && MinTokens>0; zeroing them disables the pass.
	got, err := Detect(work, base, files(base), files(work), map[string][]diff.Range{"b.go": {{Start: 1, End: 40}}}, clones.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("disabled config produced findings: %+v", got)
	}
}
