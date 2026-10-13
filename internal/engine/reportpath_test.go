package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metricgate/internal/config"
)

// TestRunReportPathsAreRootRelative pins the path invariant the -diff gate
// depends on: whatever spelling of the root the caller used, every reported
// path names its file relative to that root. A relative root other than "."
// (`metricgate check keep`) used to produce cwd-relative paths ("keep/a.go"),
// which never matched the root-relative keys git reports for -diff — so every
// finding was silently classified as pre-existing and the gate reported a clean
// pass while comparing nothing at all.
func TestRunReportPathsAreRootRelative(t *testing.T) {
	root := writeSelectionFixture(t)
	sub := filepath.Join(root, "keep")
	cfg, err := config.Load([]byte("version: 1\nexclude: [\".git\", \"testdata\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	for _, tc := range []struct {
		name   string
		dir    string // working directory the command runs from
		target string // the root argument, spelled the way a caller would
		want   string // expected reported path of keep/a.go
	}{
		{"root relative dot", root, ".", "keep/a.go"},
		{"root absolute", root, root, "keep/a.go"},
		{"root relative name", root, "keep", "a.go"},
		{"sub absolute", root, sub, "a.go"},
		{"inside sub dot", sub, ".", "a.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chdir(t, tc.dir)
			assertReportPaths(t, eng, tc.target, tc.want)
		})
	}
}

// assertReportPaths runs the engine from a given root spelling and checks that
// every reported path is root-relative and that the fixture file is named the
// way that spelling implies.
func assertReportPaths(t *testing.T, eng *Engine, target, want string) {
	t.Helper()
	summary, err := eng.Run(target)
	if err != nil {
		t.Fatal(err)
	}
	seen := make([]string, 0, len(summary.Metrics)+len(summary.Findings))
	for _, m := range summary.Metrics {
		seen = append(seen, m.File)
	}
	for _, f := range summary.Findings {
		seen = append(seen, f.File)
	}
	// Metrics are reported for every parsed file, so the fixture exercises the
	// invariant even with no findings at all.
	if len(seen) == 0 {
		t.Fatal("fixture produced no report paths; the test would pass vacuously")
	}
	if !containsPath(seen, want) {
		t.Errorf("reported paths %v do not contain %q", seen, want)
	}
	for _, p := range seen {
		if filepath.IsAbs(p) || strings.HasPrefix(p, "..") {
			t.Errorf("reported path %q is not root-relative", p)
		}
	}
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

// chdir switches the working directory for one subtest and restores it, so a
// relative-root case is exercised the way a shell invocation would.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatal(err)
		}
	})
}
