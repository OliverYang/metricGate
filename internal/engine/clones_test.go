package engine

import (
	"strings"
	"testing"

	"metricgate/internal/model"
)

// The fixture tree under testdata/clones holds a cross-file Type-2 clone
// pair: Original (the anchor, testdata/clones/orig.go) and Twin (the copy,
// testdata/clones/twin.go), plus a structurally unrelated function.

func TestClonesEndToEnd(t *testing.T) {
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	summary, err := eng.Run("testdata/clones")
	if err != nil {
		t.Fatal(err)
	}
	cloneFindings := 0
	var twin model.Finding
	for _, f := range summary.Findings {
		if f.RuleID != "function-clone" {
			continue
		}
		cloneFindings++
		if f.Source != model.SrcRule {
			t.Errorf("%s source = %s, want rule", f.RuleID, f.Source)
		}
		if f.Severity != model.SevWarning {
			t.Errorf("clone severity = %s, want the warning default", f.Severity)
		}
		if strings.HasSuffix(f.File, "twin.go") {
			twin = f
		}
		if strings.HasSuffix(f.File, "orig.go") {
			t.Error("the anchor (orig.go Original) must not be reported as a clone")
		}
	}
	if cloneFindings != 1 {
		t.Fatalf("function-clone findings = %d, want 1", cloneFindings)
	}
	if twin.File == "" {
		t.Fatal("no clone finding anchored on twin.go")
	}
	if !strings.Contains(twin.Message, "orig.go") || !strings.Contains(twin.Message, "Original") {
		t.Errorf("clone message = %q, want it to point at the anchor", twin.Message)
	}
	if twin.StartLine < 5 || twin.EndLine < twin.StartLine {
		t.Errorf("clone anchor = %d-%d, want the Twin body range", twin.StartLine, twin.EndLine)
	}
}

// An inline `metricgate: ignore` directive on the duplicate silences the
// finding, and the report says so.
func TestClonesSuppression(t *testing.T) {
	dir := "testdata/clonesup"
	// One file, the duplicate carrying its own suppression directive.
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	summary, err := eng.Run(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range summary.Findings {
		if f.RuleID == "function-clone" {
			t.Errorf("function-clone in %s must be suppressed, got %q", f.File, f.Message)
		}
	}
	if summary.Ignored == 0 {
		t.Error("the suppressed clone finding should be counted as ignored")
	}
}

// The duplicate-code pass ships on, and its rule is in the catalogue SARIF
// and editors read.
func TestClonesInCatalogue(t *testing.T) {
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	seen := map[string]bool{}
	for _, m := range eng.Rules() {
		seen[m.ID] = true
	}
	if !seen["function-clone"] {
		t.Error("function-clone missing from the rule catalogue")
	}
}

// Policy can switch the pass off entirely.
func TestClonesDisabledByPolicy(t *testing.T) {
	eng := newPolicyEngine(t, `
version: 1
clones:
  enabled: false
`)
	summary, err := eng.Run("testdata/clones")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range summary.Findings {
		if f.RuleID == "function-clone" {
			t.Errorf("function-clone reported although the pass is disabled: %s:%d", f.File, f.StartLine)
		}
	}
}

// Policy can tune the sensitivity knobs.
func TestClonesPolicyThresholds(t *testing.T) {
	eng := newPolicyEngine(t, `
version: 1
clones:
  min_lines: 999
`)
	summary, err := eng.Run("testdata/clones")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range summary.Findings {
		if f.RuleID == "function-clone" {
			t.Errorf("function-clone reported although min_lines is above every fixture function: %s", f.File)
		}
	}
}
