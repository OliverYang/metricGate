package owners

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pattern vocabulary is the whole product here: a CODEOWNERS line that
// names the wrong files routes findings to people who cannot act on them, which
// is worse than showing no owner at all.

func TestParseSkipsCommentsAndBlankLines(t *testing.T) {
	m := ParseFile("# a comment\n\n  \n*.go @gophers\n", "CODEOWNERS")
	if len(m.Rules) != 1 {
		t.Fatalf("rules = %d, want 1: %+v", len(m.Rules), m.Rules)
	}
	if m.Rules[0].Pattern != "*.go" || m.Rules[0].Line != 4 {
		t.Errorf("rule = %+v, want *.go on line 4", m.Rules[0])
	}
	if got := m.OwnerOf("main.go"); got != "@gophers" {
		t.Errorf("OwnerOf(main.go) = %q, want @gophers", got)
	}
}

func TestLastMatchingRuleWins(t *testing.T) {
	m := ParseFile("* @everyone\ninternal/store/* @storage\n", "CODEOWNERS")
	// git semantics: the later declaration overrides the earlier one.
	if got := m.OwnerOf("internal/store/store.go"); got != "@storage" {
		t.Errorf("OwnerOf(store.go) = %q, want @storage (last match wins)", got)
	}
	if got := m.OwnerOf("cmd/metricgate/main.go"); got != "@everyone" {
		t.Errorf("OwnerOf(main.go) = %q, want @everyone", got)
	}
}

func TestClearingLineFallsBackToTheEarlierMatch(t *testing.T) {
	m := ParseFile("* @everyone\ninternal/old/*\n", "CODEOWNERS")
	// A pattern with no owner clears the match rather than failing the lookup.
	if got := m.OwnerOf("internal/old/a.go"); got != "@everyone" {
		t.Errorf("OwnerOf = %q, want @everyone after the clearing line", got)
	}
}

func TestLeadingSlashIsAnchored(t *testing.T) {
	m := ParseFile("/apps/web @web\n", "CODEOWNERS")
	if got := m.OwnerOf("apps/web/index.tsx"); got != "@web" {
		t.Errorf("OwnerOf = %q, want @web", got)
	}
}

func TestPatternVocabulary(t *testing.T) {
	m := ParseFile(strings.Join([]string{
		"* @all",
		"*.go @golang",
		"**/*_test.go @tests",
		"internal/ @core",
		"cmd/metricgate/ @cli",
		"docs/** @docs",
		"apps/web @webteam",
	}, "\n"), "CODEOWNERS")
	// git's own convention: the broad pattern first and the specific override
	// later, because the LAST matching rule wins.
	cases := []struct{ file, want string }{
		{"README.md", "@all"},                  // only "*" claims it
		{"docs/a.md", "@docs"},                 // docs/** subtree
		{"docs/deep/nested/x.md", "@docs"},     // at any depth
		{"internal/owners/owners.go", "@core"}, // internal/ beats *.go
		{"cmd/metricgate/main.go", "@cli"},     // the deepest path wins
		{"cmd/x/other_test.go", "@tests"},      // *_test.go beats the dir
		{"apps/web/index.tsx", "@webteam"},     // an exact path, at any depth
	}
	for _, tc := range cases {
		if got := m.OwnerOf(tc.file); got != tc.want {
			t.Errorf("OwnerOf(%q) = %q, want %q", tc.file, got, tc.want)
		}
	}
}

func TestExactFilePattern(t *testing.T) {
	m := ParseFile("go.mod @build\n", "CODEOWNERS")
	if got := m.OwnerOf("go.mod"); got != "@build" {
		t.Errorf("OwnerOf(go.mod) = %q, want @build", got)
	}
	if got := m.OwnerOf("go.sum"); got != "" {
		t.Errorf("OwnerOf(go.sum) = %q, want empty", got)
	}
}

func TestInvertedPatternIsRefused(t *testing.T) {
	// "!" inverts a pattern, which a one-line static read cannot honour. The
	// line is skipped and the earlier match survives — the honest answer is the
	// earlier match, not a silent ownership claim from the wrong rule.
	m := ParseFile("* @everyone\n!internal/legacy/* @nobody\ninternal/other/a.go @other\n", "CODEOWNERS")
	if got := m.OwnerOf("internal/legacy/a.go"); got != "@everyone" {
		t.Errorf("OwnerOf = %q, want @everyone (inverted line skipped)", got)
	}
	if got := m.OwnerOf("internal/other/a.go"); got != "@other" {
		t.Errorf("OwnerOf = %q, want @other", got)
	}
}

func TestUnknownIsExplicit(t *testing.T) {
	m := ParseFile("*.go @go\n", "CODEOWNERS")
	if got := m.Label("README.md"); got != Unknown {
		t.Errorf("Label(README.md) = %q, want %q", got, Unknown)
	}
	if got := m.OwnerOf("README.md"); got != "" {
		t.Errorf("OwnerOf = %q, want empty (Label is the one that says unknown)", got)
	}
}

func TestUnknownShareCountsTheRoutingGap(t *testing.T) {
	m := ParseFile("internal/** @core\n", "CODEOWNERS")
	unknown, total := m.UnknownShare([]string{"internal/a.go", "README.md", "docs/x.md"})
	if unknown != 2 || total != 3 {
		t.Errorf("UnknownShare = %d/%d, want 2/3", unknown, total)
	}
}

func TestOwnersListsDistinctOwnersSorted(t *testing.T) {
	m := ParseFile("internal/** @core\ncmd/** @cli\n*.md @docs\n", "CODEOWNERS")
	got := m.Owners([]string{"internal/a.go", "cmd/x/main.go", "README.md", "internal/b.go"})
	want := []string{"@cli", "@core", "@docs"}
	if len(got) != len(want) {
		t.Fatalf("Owners = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Owners = %v, want %v", got, want)
		}
	}
}

func TestOwnersOfReturnsTheFullList(t *testing.T) {
	m := ParseFile("* @a @b @c\n", "CODEOWNERS")
	got := m.OwnersOf("x.go")
	if len(got) != 3 || got[0] != "@a" || got[2] != "@c" {
		t.Errorf("OwnersOf = %v, want the full declared list", got)
	}
}

func TestFindReadsTheGithubLocation(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "*.go @github-location\n"
	if err := os.WriteFile(filepath.Join(dir, ".github", "CODEOWNERS"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Find(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Source != ".github/CODEOWNERS" {
		t.Errorf("Source = %q, want .github/CODEOWNERS", m.Source)
	}
	if got := m.OwnerOf("main.go"); got != "@github-location" {
		t.Errorf("OwnerOf = %q", got)
	}
}

func TestFindPrefersTheGithubLocation(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{".github/CODEOWNERS", "CODEOWNERS"} {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("* @"+filepath.Base(rel)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m, err := Find(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Source != ".github/CODEOWNERS" {
		t.Errorf("Source = %q, want .github/CODEOWNERS to win", m.Source)
	}
}

func TestFindWithoutAnyFileIsAnEmptyMap(t *testing.T) {
	m, err := Find(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !m.Empty() {
		t.Errorf("Empty() = false, want an empty map with no CODEOWNERS file")
	}
	if got := m.Describe(); got == "" {
		t.Error("Describe() must say something even for an empty map")
	}
}

func TestDescribeCarriesTheSourceAndRuleCount(t *testing.T) {
	m := ParseFile("*.go @go\n*.md @docs\n", "CODEOWNERS")
	want := "CODEOWNERS: 2 rule(s)"
	if got := m.Describe(); got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}

func TestPatternIsNormalisedForStorage(t *testing.T) {
	m := ParseFile("/leading/* @slash\n", "CODEOWNERS")
	if m.Rules[0].Pattern != "leading/*" {
		t.Errorf("Pattern = %q, want the leading slash stripped", m.Rules[0].Pattern)
	}
}
