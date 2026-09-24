package selection

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestDecidePreservesDirectoryBasenameExclude(t *testing.T) {
	exclude := []string{"testdata", "vendor"}
	cases := []struct {
		path   string
		dir    bool
		want   Status
		reason Reason
	}{
		{"testdata", true, Excluded, ReasonDirectoryExclude},
		{"pkg/testdata", true, Excluded, ReasonDirectoryExclude},
		{"pkg/testdata/example.go", false, Excluded, ReasonExcludedAncestor},
		{"testdata.go", false, Included, ReasonSupportedLanguage},
		{"pkg/code.go", false, Included, ReasonSupportedLanguage},
		{"pkg/README.md", false, Unsupported, ReasonUnsupportedExt},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := Decide(tc.path, tc.dir, exclude)
			if got.Status != tc.want || got.Reason != tc.reason {
				t.Fatalf("Decide(%q,%v) = %+v, want status=%s reason=%s", tc.path, tc.dir, got, tc.want, tc.reason)
			}
			if got.Path != tc.path {
				t.Errorf("path = %q, want normalized repo-relative %q", got.Path, tc.path)
			}
		})
	}
}

func TestPlanDropsExcludedDirectoryDescendantsAndSorts(t *testing.T) {
	candidates := []Candidate{
		{Path: "b/z.go"},
		{Path: "vendor/pkg/v.go"},
		{Path: "vendor", IsDir: true},
		{Path: "a/x.java"},
		{Path: "README.md"},
		{Path: "a", IsDir: true},
	}
	got := Plan(candidates, []string{"vendor"})
	wantPaths := []string{"README.md", "a/x.java", "b/z.go", "vendor/pkg/v.go"}
	if len(got) != len(wantPaths) {
		t.Fatalf("decisions = %d, want %d: %+v", len(got), len(wantPaths), got)
	}
	paths := make([]string, len(got))
	for i, d := range got {
		paths[i] = d.Path
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Errorf("paths = %v, want %v", paths, wantPaths)
	}
	if got[0].Status != Unsupported || got[1].Language != "java" || got[3].Reason != ReasonExcludedAncestor {
		t.Errorf("classification = %+v", got)
	}
}

func TestNormalizeSlashAndDot(t *testing.T) {
	got := Decide(`./pkg\x.go`, false, nil)
	if got.Path != "pkg/x.go" {
		t.Errorf("path = %q, want pkg/x.go", got.Path)
	}
}

func TestSanitizePathMakesEgressMachineIndependent(t *testing.T) {
	cases := []struct{ root, path, want string }{
		// absolute under the walk root -> root-relative
		{"/home/u/repo", "/home/u/repo/internal/a.go", "internal/a.go"},
		{"/home/u/repo", "/home/u/repo/a.go", "a.go"},
		// single-file root: path equals root -> base name, never "."
		{"/home/u/repo/a.go", "/home/u/repo/a.go", "a.go"},
		// absolute outside the root -> base name only (no home/mount layout)
		{"/home/u/repo", "/etc/secret/x.go", "x.go"},
		{"/home/u/repo", "/home/u/other/y.go", "y.go"},
		// relative paths are normalized, never rewritten
		{"", "internal\\a.go", "internal/a.go"},
		{".", "./pkg/z.go", "pkg/z.go"},
		// empty stays empty
		{"", "", ""},
	}
	for _, tc := range cases {
		if got := SanitizePath(tc.root, tc.path); got != tc.want {
			t.Errorf("SanitizePath(%q,%q) = %q, want %q", tc.root, tc.path, got, tc.want)
		}
	}
}

func TestSanitizePathIsIdempotentAndNeverAbsolute(t *testing.T) {
	once := SanitizePath("/home/u/repo", "/home/u/repo/pkg/a.go")
	twice := SanitizePath("/home/u/repo", once)
	if once != twice {
		t.Errorf("not idempotent: %q -> %q", once, twice)
	}
	if filepath.IsAbs(once) {
		t.Errorf("sanitized path is still absolute: %q", once)
	}
}
