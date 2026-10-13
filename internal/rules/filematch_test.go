package rules

import "testing"

// TestFileMatchesVocabulary pins the pattern vocabulary that banned rules
// (except_files/allowed_files) and DSL rules (files) now share. Every form
// here is a promise to a policy author, so a change in behavior has to be a
// deliberate change to this table.
func TestFileMatchesVocabulary(t *testing.T) {
	cases := []struct {
		name     string
		patterns []string
		file     string
		want     bool
	}{
		// Empty means "no restriction", not "match nothing": a policy that
		// never sets files must keep applying everywhere.
		{"no patterns", nil, "internal/x/a.go", false},
		{"empty pattern skipped", []string{""}, "internal/x/a.go", false},

		// Suffix form — the documented idiom for test files.
		{"suffix", []string{"_test.go"}, "internal/x/a_test.go", true},
		{"suffix does not match prod", []string{"_test.go"}, "internal/x/a.go", false},
		{"suffix at root", []string{"_test.go"}, "a_test.go", true},

		// Prefix form — for scoping to a subtree.
		{"prefix", []string{"cmd/app/"}, "cmd/app/main.go", true},
		{"prefix other subtree", []string{"cmd/app/"}, "cmd/cli/main.go", false},

		// Exact path.
		{"exact", []string{"cmd/app/main.go"}, "cmd/app/main.go", true},

		// Glob, matched against the whole path or the base name.
		{"glob base name", []string{"*.go"}, "internal/x/a.go", true},
		{"glob whole path", []string{"internal/*/a.go"}, "internal/x/a.go", true},

		// A leading "**/" means "at any depth". filepath.Match alone would
		// read "**/*_test.go" as exactly one directory deep, which silently
		// skipped root-level files — the bug this form exists to prevent.
		{"any depth, deep", []string{"**/*_test.go"}, "internal/x/a_test.go", true},
		{"any depth, root", []string{"**/*_test.go"}, "a_test.go", true},
		{"any depth, prod", []string{"**/*_test.go"}, "internal/x/a.go", false},
		{"any depth with dir", []string{"**/internal/**/*_test.go"}, "internal/x/a_test.go", true},

		// Bare "**" is "everything".
		{"bare doublestar", []string{"**"}, "anything/at/all.go", true},

		// Any one hit among several patterns is enough.
		{"second pattern hits", []string{"cmd/", "_test.go"}, "internal/a_test.go", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fileMatches(tc.patterns, tc.file); got != tc.want {
				t.Errorf("fileMatches(%q, %q) = %v, want %v", tc.patterns, tc.file, got, tc.want)
			}
		})
	}
}
