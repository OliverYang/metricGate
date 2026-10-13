package configpath

import (
	"os"
	"path/filepath"
	"testing"

	"metricgate/internal/gittest"
)

// writeFile creates path with body, failing the test on any error.
func writeFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// discoverRepo builds a real git repository so tests exercise the boundary
// rule: a temp dir with no worktree shrinks the search to its own directory.
func discoverRepo(t *testing.T) string {
	t.Helper()
	return gittest.InitRepo(t)
}

func TestDiscoverFindsHiddenPolicyAtRoot(t *testing.T) {
	root := discoverRepo(t)
	want := writeFile(t, filepath.Join(root, ".metricgate.yaml"), "version: 1\n")

	got := Discover("", root)

	if got.Path != want {
		t.Fatalf("path = %q, want %q (tried %v)", got.Path, want, got.Tried)
	}
	if got.Name != ".metricgate.yaml" {
		t.Fatalf("name = %q", got.Name)
	}
}

func TestDiscoverPrefersHiddenOverLegacyName(t *testing.T) {
	root := discoverRepo(t)
	hidden := writeFile(t, filepath.Join(root, ".metricgate.yaml"), "version: 1\n")
	writeFile(t, filepath.Join(root, "policy.yaml"), "version: 1\n")
	writeFile(t, filepath.Join(root, ".metricgate", "config.yaml"), "version: 1\n")

	got := Discover("", root)

	if got.Path != hidden {
		t.Fatalf("path = %q, want the hidden form %q", got.Path, hidden)
	}
}

func TestDiscoverFallsBackToLegacyPolicyName(t *testing.T) {
	root := discoverRepo(t)
	want := writeFile(t, filepath.Join(root, "policy.yaml"), "version: 1\n")

	got := Discover("", root)

	if got.Path != want {
		t.Fatalf("path = %q, want %q", got.Path, want)
	}
	if got.Name != "policy.yaml" {
		t.Fatalf("name = %q, want policy.yaml", got.Name)
	}
}

func TestDiscoverWalksUpToThePolicy(t *testing.T) {
	root := discoverRepo(t)
	want := writeFile(t, filepath.Join(root, ".metricgate.yaml"), "version: 1\n")
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got := Discover("", nested)

	if got.Path != want {
		t.Fatalf("path = %q, want %q (a nested check must see the repo policy)", got.Path, want)
	}
	if got.Name != ".metricgate.yaml" {
		t.Fatalf("name = %q, want .metricgate.yaml", got.Name)
	}
}

func TestDiscoverNearestAncestorWins(t *testing.T) {
	root := discoverRepo(t)
	writeFile(t, filepath.Join(root, "policy.yaml"), "version: 1\n")
	nested := filepath.Join(root, "a")
	want := writeFile(t, filepath.Join(nested, ".metricgate.yaml"), "version: 1\n")

	got := Discover("", nested)

	if got.Path != want {
		t.Fatalf("path = %q, want the nearer %q", got.Path, want)
	}
}

func TestDiscoverExplicitPathWinsWithoutExistenceCheck(t *testing.T) {
	root := discoverRepo(t)
	writeFile(t, filepath.Join(root, ".metricgate.yaml"), "version: 1\n")

	got := Discover(filepath.Join(root, "somewhere-else.yaml"), root)

	if got.Path != filepath.Join(root, "somewhere-else.yaml") {
		t.Fatalf("path = %q: an explicit path must be returned untouched", got.Path)
	}
	if got.Name != "" {
		t.Fatalf("name = %q: an explicit path has no candidate name", got.Name)
	}
}

func TestDiscoverInvalidPathWorks(t *testing.T) {
	root := discoverRepo(t)
	want := writeFile(t, filepath.Join(root, ".metricgate.yaml"), "version: 1\n")

	got := Discover("", filepath.Join(root, "gone"))

	if got.Path != want {
		t.Fatalf("path = %q, want %q: a missing root must not hide the policy", got.Path, want)
	}
}

func TestDiscoverFileRootUsesItsDirectory(t *testing.T) {
	root := discoverRepo(t)
	writeFile(t, filepath.Join(root, "policy.yaml"), "version: 1\n")
	target := filepath.Join(root, "sub")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	policyFile := writeFile(t, filepath.Join(target, ".metricgate.yaml"), "version: 1\n")
	source := filepath.Join(target, "main.go")
	writeFile(t, source, "package main\n")

	got := Discover("", source)

	if got.Path != policyFile {
		t.Fatalf("path = %q, want the sibling %q", got.Path, policyFile)
	}
}

func TestDiscoverStopsAtTheGitRoot(t *testing.T) {
	root := t.TempDir()
	// A policy ABOVE the repository must not be adopted.
	above := writeFile(t, filepath.Join(root, ".metricgate.yaml"), "version: 1\n")
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	got := Discover("", repo)

	if got.Path != "" {
		t.Fatalf("path = %q: the search escaped the repository to %q", got.Path, above)
	}
	if got.FoundAny() {
		t.Fatal("FoundAny must be false outside a repository")
	}
	if got.Display() == "" {
		t.Fatal("Display must explain the empty decision")
	}
}

func TestDiscoverReportsTriedCandidatesWhenNothingFound(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "x"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := Discover("", filepath.Join(root, "x"))

	if len(got.Tried) == 0 {
		t.Fatal("Tried must record what was looked at, or the empty result is unexplainable")
	}
	for _, p := range got.Tried {
		if p == filepath.Join(root, ".metricgate.yaml") {
			t.Fatal("the walk must not look outside the repository")
		}
	}
}
