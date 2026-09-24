package selection

import (
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
