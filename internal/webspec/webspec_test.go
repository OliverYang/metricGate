package webspec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The spec exists to stop the two sides of the contract drifting. A drift is
// silent by nature — a JSON field renamed on one side only — so the test has to
// compare the document the browser will read against the one the Go side
// serves, byte for byte, on every run.

// specPath is where the generator writes, relative to the package directory.
const specPath = "../cockpit/web/spec.json"

func TestBuildIsDeterministic(t *testing.T) {
	first, err := json.Marshal(Build())
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(Build())
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("Build() is not deterministic; the file comparison below would then be meaningless")
	}
}

// TestSpecFileMatchesBuild is the drift guard. It fails when
// internal/cockpit/web/spec.json does not match what the Go side would write
// right now, which happens exactly when someone changes an enumeration without
// regenerating — or renames a field on one side only.
func TestSpecFileMatchesBuild(t *testing.T) {
	want, err := json.MarshalIndent(Build(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	path := filepath.Join("..", "cockpit", "web", "spec.json")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (run `go generate ./internal/webspec`)", path, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s does not match webspec.Build(); run `go generate ./internal/webspec` and commit the result", path)
	}
}

func TestEveryEnumerationIsUnique(t *testing.T) {
	for name, values := range map[string][]string{
		"severities": Severities(),
		"statuses":   Statuses(),
		"sorts":      Sorts(),
		"views":      Views(),
		"events":     EventsList(),
		"phases":     PhaseFilterValues,
	} {
		seen := map[string]bool{}
		for _, v := range values {
			if v == "" {
				t.Errorf("%s contains an empty value; a blank entry is unreachable from a URL", name)
			}
			if seen[v] {
				t.Errorf("%s contains %q twice; a duplicated facet value renders as two chips", name, v)
			}
			seen[v] = true
		}
	}
}

// TestFrontendReadsTheSpec is a static check that the browser actually loads
// the generated file, rather than a copy that happens to look the same. It is
// the weakest possible assertion that the wiring exists, which is exactly the
// failure it is here to catch.
func TestFrontendReadsTheSpec(t *testing.T) {
	data, err := os.ReadFile("../cockpit/web/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	if !contains(string(data), "spec.json") {
		t.Error("app.js does not load spec.json; the contract file would then serve nothing")
	}
	if !contains(string(data), "import") {
		t.Error("app.js is not a module; a generated import needs <script type=\"module\">")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}()
}
