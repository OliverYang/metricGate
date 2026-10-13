package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseHistoryGroupsByCommit(t *testing.T) {
	out := "\x01\n10\t2\ta.go\n3\t4\tb.go\n\x01\n0\t8\ta.go\n\x01\n-\t-\timage.png\n"
	got := parseHistory(out)
	if len(got) != 3 {
		t.Fatalf("commits = %d, want 3 (the binary-only commit still counts)", len(got))
	}
	if !reflect.DeepEqual(got[0].Files, []FileStat{
		{Path: "a.go", Added: 10, Deleted: 2},
		{Path: "b.go", Added: 3, Deleted: 4},
	}) {
		t.Errorf("commit 0 = %+v", got[0].Files)
	}
	if len(got[1].Files) != 1 || got[1].Files[0].Deleted != 8 {
		t.Errorf("commit 1 = %+v", got[1].Files)
	}
	if !got[2].Files[0].Binary {
		t.Errorf("binary file must be marked: %+v", got[2].Files[0])
	}
}

// The text before the first separator (and an empty commit) is not history.
func TestParseHistorySkipsEmptyBlocks(t *testing.T) {
	if got := parseHistory("garbage with no stats\n\x01\n\n\x01\n"); len(got) != 0 {
		t.Errorf("commits = %+v, want none", got)
	}
}

func TestParseStatLineRejectsMalformed(t *testing.T) {
	cases := map[string]bool{
		"":                  false,
		"10\t2":             false, // no path
		"10\t2\t":           false, // empty path
		"10\t2\ta.go":       true,
		"-\t-\timage.png":   true, // binary, still a real change
		"x\ty\ta.go":        true, // unparseable counts, kept as binary
		"1\t2\ta.go\textra": true, // path is the last field
	}
	for line, want := range cases {
		if _, ok := parseStatLine(line); ok != want {
			t.Errorf("parseStatLine(%q) ok = %v, want %v", line, ok, want)
		}
	}
}

func TestHistoryFromTinyRepo(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@example.com")
	runGit(t, dir, "config", "user.name", "T")
	writeFile(t, filepath.Join(dir, "a.go"), "one\n")
	runGit(t, dir, "add", "a.go")
	runGit(t, dir, "commit", "-qm", "base")
	writeFile(t, filepath.Join(dir, "b.go"), "two\nthree\n")
	runGit(t, dir, "add", "b.go")
	runGit(t, dir, "commit", "-qm", "add b")

	commits, err := History(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(commits))
	}
	// Newest first: the b.go commit.
	if len(commits[0].Files) != 1 || commits[0].Files[0].Path != "b.go" || commits[0].Files[0].Added != 2 {
		t.Errorf("newest commit = %+v, want b.go +2", commits[0].Files)
	}
	if len(commits[1].Files) != 1 || commits[1].Files[0].Path != "a.go" {
		t.Errorf("oldest commit = %+v, want a.go", commits[1].Files)
	}
}

func TestHistoryRejectsNonRepo(t *testing.T) {
	if _, err := History(t.TempDir(), ""); err == nil {
		t.Fatal("History outside a git repository must fail")
	}
}

func TestIsShallowOnFullClone(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	if IsShallow(dir) {
		t.Error("a fresh repository is not shallow")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
