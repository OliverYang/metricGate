package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"metricgate/internal/diff"
)

// TestChangedLines builds a real git repo: a base commit, then a working tree
// that replaces one base line with three lines and adds an untracked file. The
// changed-line ranges must reflect exactly that.
func TestChangedLines(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	runGit("init", "-q", "--initial-branch=main")
	runGit("config", "user.email", "t@t")
	runGit("config", "user.name", "t")
	write("a.go", "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\n")
	runGit("add", ".")
	runGit("commit", "-qm", "base")

	// Working tree: replace line 5 with three lines; leave the rest.
	write("a.go", "l1\nl2\nl3\nl4\nl5a\nl5b\nl5c\nl6\nl7\nl8\n")
	// Untracked file: every line counts as new.
	write("b.go", "n1\nn2\n")

	changed, err := ChangedLines(dir, ResolveBase(dir, "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]diff.Range{
		"a.go": {{Start: 5, End: 7}},
		"b.go": {{Start: 1, End: 2}},
	}
	if !reflect.DeepEqual(changed, want) {
		t.Errorf("changed = %v, want %v", changed, want)
	}
}

func TestParseDiffPureDeletion(t *testing.T) {
	// A pure deletion hunk (`+0,0`) must not produce a range.
	changed := map[string][]diff.Range{}
	parseDiff(`diff --git a/a.go b/a.go
+++ b/a.go
@@ -5,3 +5,0 @@
-old one
-old two
-old three
`, changed)
	if len(changed["a.go"]) != 0 {
		t.Errorf("pure deletion produced ranges: %v", changed["a.go"])
	}
}

func TestResolveBaseFallsBackOutsideRepo(t *testing.T) {
	// A directory with no git history must not break -diff: the raw ref is used.
	got := ResolveBase(t.TempDir(), "HEAD")
	if got != "HEAD" {
		t.Errorf("ResolveBase = %q, want the untouched fallback HEAD", got)
	}
}
