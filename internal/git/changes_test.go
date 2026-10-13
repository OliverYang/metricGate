package git

import (
	"os"
	"path/filepath"
	"strings"
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

// TestLoadChangesOnRepoWithoutCommitsNamesTheRealProblem covers the state a
// pre-commit hook hits on a repository's very first commit: the repository
// exists, but HEAD has nothing to diff against. The generic "requires a git
// repository" wording is wrong there and sends the reader looking for a git
// installation that is already present.
func TestLoadChangesOnRepoWithoutCommitsNamesTheRealProblem(t *testing.T) {
	root := initGitRepo(t)
	writeRepoFile(t, root, "a.go", "package a\n")

	_, err := LoadChanges(root, ChangeRequest{Mode: ModeWorkspace})
	if err == nil {
		t.Fatal("LoadChanges on an unborn HEAD succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "no commits yet") {
		t.Errorf("error = %v, want it to name the unborn HEAD", err)
	}
	if strings.Contains(err.Error(), "requires a git repository") {
		t.Errorf("error = %v, want the misleading generic wording replaced", err)
	}
}

// TestLoadChangesOutsideRepoKeepsTheGenericMessage: the specific wording must
// not swallow the case it was not written for.
func TestLoadChangesOutsideRepoKeepsTheGenericMessage(t *testing.T) {
	_, err := LoadChanges(t.TempDir(), ChangeRequest{Mode: ModeWorkspace})
	if err == nil || !strings.Contains(err.Error(), "requires a git repository") {
		t.Fatalf("error = %v, want the generic repository message", err)
	}
}

// TestLoadChangesUnknownBaseRefNamesTheRef: a CI job that forgot to fetch the
// target branch must be told which ref is missing, not that there is no
// repository.
func TestLoadChangesUnknownBaseRefNamesTheRef(t *testing.T) {
	root := initGitRepo(t)
	writeRepoFile(t, root, "a.go", "package a\n")
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-qm", "base")

	_, err := LoadChanges(root, ChangeRequest{Mode: ModeWorkspace, Base: "origin/nope"})
	if err == nil {
		t.Fatal("LoadChanges with an unknown base succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "origin/nope") {
		t.Errorf("error = %v, want it to name the ref that could not be resolved", err)
	}
	if strings.Contains(err.Error(), "requires a git repository") {
		t.Errorf("error = %v, want the misleading generic wording replaced", err)
	}
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

// TestLoadChangesFromSubdirectoryKeepsHunks pins the silent-failure mode a
// subdirectory cwd used to cause: git resolves pathspecs against the worktree
// root, so `git diff -- internal/core/core.go` run inside internal/core matched
// nothing and every change lost its hunks. -diff then hid every finding as
// pre-existing and the gate reported green on a violating change.
func TestLoadChangesFromSubdirectoryKeepsHunks(t *testing.T) {
	root := initGitRepo(t)
	writeRepoFile(t, root, "internal/core/core.go", "package core\n\nfunc Simple() int { return 1 }\n")
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-qm", "base")
	writeRepoFile(t, root, "internal/core/core.go", "package core\n\nfunc Simple() int {\n\tif true {\n\t\treturn 1\n\t}\n\treturn 2\n}\n")

	for _, dir := range []string{root, filepath.Join(root, "internal", "core")} {
		changes, err := LoadChanges(dir, ChangeRequest{Mode: ModeWorkspace, Base: "HEAD"})
		if err != nil {
			t.Fatalf("LoadChanges(%s): %v", dir, err)
		}
		if len(changes) != 1 || len(changes[0].Hunks) == 0 {
			t.Fatalf("LoadChanges(%s) changes=%+v, want one change with hunks", dir, changes)
		}
		if changes[0].Path != "internal/core/core.go" {
			t.Errorf("LoadChanges(%s) path=%q, want the worktree-relative path", dir, changes[0].Path)
		}
	}
}

// TestChangedLinesForRebasesOntoCheckRoot: git speaks worktree-relative paths
// while a report is scoped to the check root, so the gate needs the rebased
// form for every spelling of the same scope.
func TestChangedLinesForRebasesOntoCheckRoot(t *testing.T) {
	root := initGitRepo(t)
	writeRepoFile(t, root, "internal/core/core.go", "package core\n\nfunc Simple() int { return 1 }\n")
	writeRepoFile(t, root, "other/other.go", "package other\n\nfunc Other() int { return 1 }\n")
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-qm", "base")
	writeRepoFile(t, root, "internal/core/core.go", "package core\n\nfunc Simple() int {\n\treturn 2\n}\n")
	writeRepoFile(t, root, "other/other.go", "package other\n\nfunc Other() int {\n\treturn 2\n}\n")

	cases := []struct {
		root string
		want []string
	}{
		{root, []string{"internal/core/core.go", "other/other.go"}},
		{filepath.Join(root, "internal", "core"), []string{"core.go"}},
		{filepath.Join(root, "internal", "core", "core.go"), []string{"core.go"}},
	}
	for _, tc := range cases {
		changed, err := ChangedLinesFor(tc.root, "HEAD")
		if err != nil {
			t.Fatalf("ChangedLinesFor(%s): %v", tc.root, err)
		}
		if len(changed) != len(tc.want) {
			t.Fatalf("ChangedLinesFor(%s) keys=%v, want %v", tc.root, changed, tc.want)
		}
		for _, key := range tc.want {
			if len(changed[key]) == 0 {
				t.Errorf("ChangedLinesFor(%s) missing key %q (got %v)", tc.root, key, changed)
			}
		}
	}
}

// TestChangedLinesForOutsideRepoFailsLoudly: -diff needs a repository, and a
// silent empty map would read as "nothing changed".
func TestChangedLinesForOutsideRepoFailsLoudly(t *testing.T) {
	if _, err := ChangedLinesFor(t.TempDir(), "HEAD"); err == nil {
		t.Fatal("ChangedLinesFor outside a worktree succeeded, want an error")
	}
}

// TestArchivePathsSkipsFilesAbsentAtRef covers the reason ArchivePaths exists:
// `git archive` aborts the whole command on one unmatched pathspec, so a file
// the change adds would otherwise take the entire base snapshot down with it.
func TestArchivePathsSkipsFilesAbsentAtRef(t *testing.T) {
	root := initGitRepo(t)
	writeRepoFile(t, root, "keep/a.go", "package keep\n")
	writeRepoFile(t, root, "added/b.go", "package added\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-q", "-m", "base")
	gitRun(t, root, "rm", "-q", "added/b.go")
	gitRun(t, root, "commit", "-q", "-m", "remove added")

	dir, cleanup, err := ArchivePaths(root, "HEAD", []string{"keep/a.go", "added/b.go"})
	if err != nil {
		t.Fatalf("ArchivePaths: %v", err)
	}
	defer cleanup()
	if _, err := os.Stat(filepath.Join(dir, "keep", "a.go")); err != nil {
		t.Errorf("present path missing from the archive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "added", "b.go")); !os.IsNotExist(err) {
		t.Errorf("removed path should not be in the archive, stat err = %v", err)
	}
}

// TestArchivePathsWithNoSurvivingPathsIsEmptyNotAnError pins the degradation
// the filter depends on: when every touched file is new there is nothing to
// compare against, and that is a fact rather than a failure.
func TestArchivePathsWithNoSurvivingPathsIsEmptyNotAnError(t *testing.T) {
	root := initGitRepo(t)
	writeRepoFile(t, root, "a.go", "package a\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-q", "-m", "base")

	dir, cleanup, err := ArchivePaths(root, "HEAD", []string{"brand/new.go"})
	if err != nil {
		t.Fatalf("ArchivePaths: %v", err)
	}
	defer cleanup()
	if dir != "" {
		t.Errorf("dir = %q, want empty when nothing exists at the ref", dir)
	}
}

// TestRepoRelativeMapsCheckRootOntoWorktree pins the join the base snapshot
// needs: diff keys are check-root-relative while the archive is worktree
// shaped, so a subdirectory check must not look up paths that do not exist.
func TestRepoRelativeMapsCheckRootOntoWorktree(t *testing.T) {
	root := initGitRepo(t)
	writeRepoFile(t, root, "internal/core/core.go", "package core\n")
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-q", "-m", "base")

	sub := filepath.Join(root, "internal", "core")
	if got, ok := RepoRelative(sub, "core.go"); !ok || got != "internal/core/core.go" {
		t.Errorf("RepoRelative(sub, core.go) = %q,%v, want internal/core/core.go,true", got, ok)
	}
	if got, ok := RepoRelative(root, "internal/core/core.go"); !ok || got != "internal/core/core.go" {
		t.Errorf("RepoRelative(root, ...) = %q,%v, want the path unchanged", got, ok)
	}
	if _, ok := RepoRelative(t.TempDir(), "a.go"); ok {
		t.Error("RepoRelative outside a worktree reported success")
	}
}
