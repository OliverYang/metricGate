package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"metricgate/internal/diff"
)

func TestRootReturnsCanonicalGitRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	file := filepath.Join(dir, "source.go")
	if err := os.WriteFile(file, []byte("package sample"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Root(file)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != resolved {
		t.Fatalf("Root(%q) = %q, want %q", file, got, resolved)
	}
}

func TestRootUnassignedOutsideGit(t *testing.T) {
	if got := Root(t.TempDir()); got != "" {
		t.Fatalf("Root outside git = %q", got)
	}
}

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
	// "main" (not "HEAD") so the fallback path is the one under test — a HEAD
	// base never reaches git at all (TestResolveBaseShortCircuitsForHead).
	got := ResolveBase(t.TempDir(), "main")
	if got != "main" {
		t.Errorf("ResolveBase = %q, want the untouched fallback main", got)
	}
}

func TestResolveBaseShortCircuitsForHead(t *testing.T) {
	// The merge-base of HEAD with HEAD is HEAD, so the subprocess only re-derives
	// a value already in hand. -diff used to pay for it twice per run (the change
	// set and the baseline snapshot), ~0.21-0.4 s of spawn cost each on a machine
	// where that is what a single git command costs. A directory outside any
	// repository proves the short circuit: a real merge-base would fail there.
	dir := t.TempDir()
	if got := ResolveBase(dir, "HEAD"); got != "HEAD" {
		t.Errorf("ResolveBase(dir, HEAD) = %q, want HEAD without a git round trip", got)
	}
	if got := ResolveBase(dir, ""); got != "" {
		t.Errorf("ResolveBase(dir, \"\") = %q, want the empty base left untouched", got)
	}
}

func TestRootDoesNotCacheAMiss(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	if got := Root(dir); got != "" {
		t.Fatalf("Root before git init = %q, want empty", got)
	}
	// The first call was a miss. If a miss were cached, a long-lived process
	// (the MCP server, the cockpit gate watcher) would keep reporting "no
	// repository" after the user runs `git init` in the directory it watches.
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if got := Root(dir); got == "" {
		t.Fatal("Root after git init = empty, want the new worktree root")
	}
}

func TestRepoRelativizerAgreesWithRepoRelative(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := initGitRepo(t)
	if err := os.MkdirAll(filepath.Join(root, "internal", "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "internal", "core")
	for _, checkRoot := range []string{root, sub} {
		rel, ok := NewRepoRelativizer(checkRoot)
		if !ok {
			t.Fatalf("NewRepoRelativizer(%s) reported failure", checkRoot)
		}
		for _, path := range []string{"core.go", "internal/git/git.go", "../outside.go", "/abs/outside.go"} {
			got, gotOK := rel.Rel(path)
			want, wantOK := RepoRelative(checkRoot, path)
			if got != want || gotOK != wantOK {
				t.Errorf("Rel(%q) = %q,%v; RepoRelative = %q,%v", path, got, gotOK, want, wantOK)
			}
		}
	}
	if _, ok := NewRepoRelativizer(t.TempDir()); ok {
		t.Error("NewRepoRelativizer outside a worktree reported success")
	}
}

// TestRepoRelativizerUnrelInvertsRel pins both halves of the contract Unrel has
// to keep: it must undo Rel, and at the worktree root it must be the identity.
//
// The identity half is not a special case of the first. Every report produced at
// the worktree root is guaranteed to be byte-for-byte what it was before the
// subdirectory fix, and that guarantee rests on Unrel returning the same string
// there -- a "rebase" that normalised separators, cleaned a path or resolved a
// symlink would pass a round-trip test and still change the output.
func TestRepoRelativizerUnrelInvertsRel(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := initGitRepo(t)
	sub := filepath.Join(root, "internal", "core")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	assertUnrelIsIdentityAtRoot(t, root)
	assertRelUnrelRoundTrips(t, sub)
}

// assertUnrelIsIdentityAtRoot checks the half that protects the byte-for-byte
// guarantee: at the worktree root Unrel must hand back exactly what it was
// given, including for a path that does not exist on disk.
func assertUnrelIsIdentityAtRoot(t *testing.T, root string) {
	t.Helper()
	atRoot, ok := NewRepoRelativizer(root)
	if !ok {
		t.Fatal("NewRepoRelativizer(root) reported failure")
	}
	for _, path := range []string{"core.go", "internal/git/git.go", "not/created/yet.go"} {
		got, gotOK := atRoot.Unrel(path)
		if !gotOK || got != path {
			t.Errorf("Unrel(%q) at the worktree root = %q,%v; want the path unchanged", path, got, gotOK)
		}
	}
}

// assertRelUnrelRoundTrips checks the half the -diff base snapshot relies on:
// under a subdirectory the archive is keyed worktree-relatively and the report
// is keyed check-root-relatively, so Rel and Unrel must be inverses across that
// boundary. A path outside the check root has no check-root-relative form, and
// returning one would put a base value into a key space the gated run never
// uses -- silently, which is the failure mode this whole change removes.
func assertRelUnrelRoundTrips(t *testing.T, sub string) {
	t.Helper()
	rel, ok := NewRepoRelativizer(sub)
	if !ok {
		t.Fatal("NewRepoRelativizer(sub) reported failure")
	}
	for _, checkRelative := range []string{"core.go", "nested/deep.go"} {
		repoRelative, ok := rel.Rel(checkRelative)
		if !ok {
			t.Fatalf("Rel(%q) failed", checkRelative)
		}
		back, ok := rel.Unrel(repoRelative)
		if !ok || back != checkRelative {
			t.Errorf("Unrel(Rel(%q)) = %q,%v; want %q", checkRelative, back, ok, checkRelative)
		}
	}
	if got, ok := rel.Unrel("elsewhere/other.go"); ok {
		t.Errorf("Unrel(%q) = %q, true; want false for a path outside the check root", "elsewhere/other.go", got)
	}
}
