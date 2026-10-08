package deps

import (
	"os"
	"path/filepath"
	"testing"

	"metricgate/internal/config"
)

func TestParseGoModReadsModuleAndRequires(t *testing.T) {
	text := `module example.com/app

go 1.26

require (
	github.com/spf13/cobra v1.8.0
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

require github.com/gorilla/mux v1.8.1
`
	module, requires := parseGoModText(text)
	if module != "example.com/app" {
		t.Errorf("module = %q", module)
	}
	want := []string{"github.com/spf13/cobra", "gopkg.in/yaml.v3", "github.com/gorilla/mux"}
	if len(requires) != len(want) {
		t.Fatalf("requires = %v, want %v", requires, want)
	}
	for i := range want {
		if requires[i] != want[i] {
			t.Errorf("requires[%d] = %q, want %q", i, requires[i], want[i])
		}
	}
}

func TestMatchesRequiresSeparatorBoundary(t *testing.T) {
	cases := []struct {
		path, entry string
		want        bool
	}{
		{"example.com/foo", "example.com/foo", true},
		{"example.com/foo/bar", "example.com/foo", true},
		{"example.com/foo.bar", "example.com/foo", true},
		{"example.com/foobar", "example.com/foo", false},
		{"org.slf4j.Logger", "org.slf4j", true},
		{"org.slf4jX.Logger", "org.slf4j", false},
		{"anything", "", false},
	}
	for _, c := range cases {
		if got := Matches(c.path, c.entry); got != c.want {
			t.Errorf("Matches(%q, %q) = %v, want %v", c.path, c.entry, got, c.want)
		}
	}
}

func TestForRootReadsGoModAboveRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n\nrequire github.com/known/dep v1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	allow := ForRoot(sub, nil)
	for _, path := range []string{"example.com/app/internal/deep", "github.com/known/dep"} {
		if !allow.Allows(path) {
			t.Errorf("Allows(%q) = false, want true (from the go.mod above root)", path)
		}
	}
	if allow.Allows("github.com/other/thing") {
		t.Error("Allows should reject an undeclared module")
	}
}

func TestForRootMergesPolicyEntries(t *testing.T) {
	cfg := &config.Config{Dependencies: config.DepsConfig{
		Allow: []string{"org.slf4j"},
		Self:  []string{"com.example.app"},
	}}
	allow := ForRoot(t.TempDir(), cfg)
	for _, path := range []string{"org.slf4j.Logger", "com.example.app.Service"} {
		if !allow.Allows(path) {
			t.Errorf("Allows(%q) = false, want true", path)
		}
	}
}

func TestNewDedupesAndSorts(t *testing.T) {
	allow := New([]string{" b ", "a", "", "a", "b"})
	got := allow.Prefixes()
	want := []string{"a", "b"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Prefixes() = %v, want %v", got, want)
	}
}
