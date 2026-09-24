package git

import (
	"os"
	"path/filepath"
	"testing"

	"metricgate/internal/gittest"
)

var initGitRepo = gittest.InitRepo
var writeRepoFile = gittest.Write
var gitRun = gittest.Run

func byPath(changes []FileChange) map[string]FileChange {
	m := map[string]FileChange{}
	for _, c := range changes {
		m[c.Path] = c
	}
	return m
}

func TestLoadChangesWorkspaceIncludesStagedUnstagedUntrackedRenameDeleteAndLiteralPaths(t *testing.T) {
	root, tabPath := setupWorkspaceFixture(t)
	changes, err := LoadChanges(root, ChangeRequest{Mode: ModeWorkspace})
	if err != nil {
		t.Fatal(err)
	}
	got := byPath(changes)
	assertPresent(t, got, []string{"staged.go", "unstaged.go", "untracked.go", "new-name.go", "delete.go", "space name.go", tabPath})
	assertModified(t, got, "staged.go")
	assertModified(t, got, "unstaged.go")
	assertUntrackedWholeFile(t, got["untracked.go"])
	assertRename(t, got["new-name.go"], "old-name.go")
	assertDelete(t, got["delete.go"])
	assertHasHunks(t, got, []string{"space name.go", tabPath})
	assertLegacyProjection(t, changes)
}

func setupWorkspaceFixture(t *testing.T) (root, tabPath string) {
	t.Helper()
	root = initGitRepo(t)
	tabPath = "tab\tname.go"
	for name, body := range map[string]string{
		"staged.go":     "package x\nfunc A() int { return 1 }\n",
		"unstaged.go":   "package x\nfunc B() int { return 1 }\n",
		"old-name.go":   "package x\nfunc RenamedName() int {\n\treturn 7\n}\n",
		"delete.go":     "package x\nfunc DeleteMe() int { return 9 }\n",
		"space name.go": "package x\nfunc Space() int { return 1 }\n",
		tabPath:         "package x\nfunc Tab() int { return 1 }\n",
	} {
		writeRepoFile(t, root, name, body)
	}
	gitRun(t, root, "add", "--all")
	gitRun(t, root, "commit", "-qm", "base")

	// staged and unstaged changes coexist; the change set is final worktree vs HEAD.
	writeRepoFile(t, root, "staged.go", "package x\nfunc A() int { return 2 }\n")
	gitRun(t, root, "add", "staged.go")
	writeRepoFile(t, root, "unstaged.go", "package x\nfunc B() int { return 3 }\n")
	if err := os.Rename(filepath.Join(root, "old-name.go"), filepath.Join(root, "new-name.go")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", "-A")
	if err := os.Remove(filepath.Join(root, "delete.go")); err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, root, "untracked.go", "package x\nfunc New() int { return 4 }\n")
	writeRepoFile(t, root, "space name.go", "package x\nfunc Space() int { return 2 }\n")
	writeRepoFile(t, root, tabPath, "package x\nfunc Tab() int { return 2 }\n")
	return root, tabPath
}

func assertPresent(t *testing.T, got map[string]FileChange, paths []string) {
	t.Helper()
	for _, path := range paths {
		if _, ok := got[path]; !ok {
			t.Errorf("missing change %q; paths=%v", path, keys(got))
		}
	}
}

func assertModified(t *testing.T, got map[string]FileChange, path string) {
	t.Helper()
	if !got[path].StatusIs("M") {
		t.Errorf("%s status = %q, want M", path, got[path].Status)
	}
}

func assertUntrackedWholeFile(t *testing.T, c FileChange) {
	t.Helper()
	if !c.Untracked || len(c.Hunks) != 1 || c.Hunks[0].NewCount != 2 {
		t.Errorf("untracked hunk = %+v", c)
	}
}

func assertRename(t *testing.T, c FileChange, oldPath string) {
	t.Helper()
	if c.Status != "R" || c.OldPath != oldPath {
		t.Errorf("rename = %+v", c)
	}
}

func assertDelete(t *testing.T, c FileChange) {
	t.Helper()
	if c.Status != "D" || c.NewPath != "" {
		t.Errorf("delete = %+v", c)
	}
}

func assertHasHunks(t *testing.T, got map[string]FileChange, paths []string) {
	t.Helper()
	for _, name := range paths {
		if len(got[name].Hunks) == 0 {
			t.Errorf("special path %q has no parsed hunk", name)
		}
	}
}

func assertLegacyProjection(t *testing.T, changes []FileChange) {
	t.Helper()
	changed := ChangedLinesFromChanges(changes)
	if len(changed["delete.go"]) != 0 || len(changed["staged.go"]) == 0 {
		t.Errorf("legacy range projection = %v", changed)
	}
}

func (c FileChange) StatusIs(s string) bool { return c.Status == s }
func keys(m map[string]FileChange) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestLoadChangesRangeAndCommit(t *testing.T) {
	root := initGitRepo(t)
	writeRepoFile(t, root, "a.go", "package x\nfunc A() int { return 1 }\n")
	gitRun(t, root, "add", "a.go")
	gitRun(t, root, "commit", "-qm", "base")
	base := gitRun(t, root, "rev-parse", "HEAD")
	writeRepoFile(t, root, "a.go", "package x\nfunc A() int { return 2 }\n")
	gitRun(t, root, "add", "a.go")
	gitRun(t, root, "commit", "-qm", "change")
	commit := gitRun(t, root, "rev-parse", "HEAD")
	for _, req := range []ChangeRequest{{Mode: ModeRange, From: base, To: commit}, {Mode: ModeCommit, Commit: commit}} {
		changes, err := LoadChanges(root, req)
		if err != nil {
			t.Fatal(err)
		}
		if len(changes) != 1 || changes[0].Path != "a.go" || len(changes[0].Hunks) == 0 {
			t.Errorf("mode %s changes=%+v", req.Mode, changes)
		}
	}
}

func TestParseNameStatusRejectsMalformedRecords(t *testing.T) {
	if _, err := parseNameStatusZ([]byte("R100\x00old.go\x00")); err == nil {
		t.Fatal("expected malformed rename error")
	}
	if _, err := parseNameStatusZ([]byte("M\x00")); err == nil {
		t.Fatal("expected missing path error")
	}
}
