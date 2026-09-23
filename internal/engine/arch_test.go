package engine

import (
	"strings"
	"testing"

	"metricgate/internal/arch"
	"metricgate/internal/model"
)

// The fixture tree under testdata/arch declares three packages:
//
//	api   → svc            legal (handler → service)
//	svc   → api, store     svc → api is a layer violation; svc → store is legal
//	store → svc            forbidden edge (and a layer violation, but the
//	                       forbid entry wins)
//
// Together they form one cycle over all three packages.
const archPolicy = `
version: 1
architecture:
  cycles: error
  layers:
    - name: handler
      packages: ["api"]
      may_depend_on: [service]
    - name: service
      packages: ["svc"]
      may_depend_on: [storage]
    - name: storage
      packages: ["store"]
  forbid:
    - from: store
      to: svc
      reason: storage must not call the service layer
`

func ruleCounts(fs []model.Finding) map[string]int {
	counts := map[string]int{}
	for _, f := range fs {
		counts[f.RuleID]++
	}
	return counts
}

func TestArchitectureEndToEnd(t *testing.T) {
	eng := newPolicyEngine(t, archPolicy)
	summary, err := eng.Run("testdata/arch")
	if err != nil {
		t.Fatal(err)
	}
	counts := ruleCounts(summary.Findings)
	for id, want := range map[string]int{
		arch.RuleLayerViolation: 1,
		arch.RuleForbiddenEdge:  1,
		arch.RuleImportCycle:    1,
	} {
		if counts[id] != want {
			t.Errorf("%s = %d, want %d (all: %v)", id, counts[id], want, counts)
		}
	}

	for _, f := range summary.Findings {
		if f.Source != model.SrcRule {
			t.Errorf("%s source = %s, want rule", f.RuleID, f.Source)
		}
		switch f.RuleID {
		case arch.RuleImportCycle:
			if f.Severity != model.SevError {
				t.Errorf("cycle severity = %s, want error from architecture.cycles", f.Severity)
			}
			if !strings.Contains(f.Message, "api") || !strings.Contains(f.Message, "svc") {
				t.Errorf("cycle message = %q, want the package names", f.Message)
			}
		case arch.RuleLayerViolation, arch.RuleForbiddenEdge:
			if f.Severity != model.SevWarning {
				t.Errorf("%s severity = %s, want the warning default", f.RuleID, f.Severity)
			}
		}
	}
}

// Every architecture finding is anchored at the import that created it, so the
// LLM (and the developer) sees the exact line to change.
func TestArchitectureAnchorsAtImports(t *testing.T) {
	eng := newPolicyEngine(t, archPolicy)
	summary, err := eng.Run("testdata/arch")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range summary.Findings {
		if !strings.HasPrefix(f.RuleID, "layer-") && !strings.HasPrefix(f.RuleID, "forbidden-") &&
			!strings.HasPrefix(f.RuleID, "import-") {
			continue
		}
		if f.StartLine < 1 || f.StartLine != f.EndLine {
			t.Errorf("%s anchor = %d-%d, want one line", f.RuleID, f.StartLine, f.EndLine)
		}
		switch f.RuleID {
		case arch.RuleForbiddenEdge:
			if !strings.HasSuffix(f.File, "store/store.go") {
				t.Errorf("forbidden edge in %s, want store/store.go", f.File)
			}
		case arch.RuleLayerViolation:
			if !strings.HasSuffix(f.File, "svc/svc.go") {
				t.Errorf("layer violation in %s, want svc/svc.go", f.File)
			}
		}
	}
}

// An inline directive on the import line silences an architecture finding, and
// the report says so.
func TestArchitectureSuppression(t *testing.T) {
	eng := newPolicyEngine(t, `
version: 1
architecture:
  cycles: error
  forbid:
    - from: p
      to: q
      reason: no new coupling
`)
	summary, err := eng.Run("testdata/archsup")
	if err != nil {
		t.Fatal(err)
	}
	counts := ruleCounts(summary.Findings)
	if counts[arch.RuleImportCycle] != 0 {
		t.Errorf("import-cycle = %d, want 0 (suppressed by the directive)", counts[arch.RuleImportCycle])
	}
	if counts[arch.RuleForbiddenEdge] != 1 {
		t.Errorf("forbidden-edge = %d, want 1 (the directive names only import-cycle)", counts[arch.RuleForbiddenEdge])
	}
	if summary.Ignored != 1 {
		t.Errorf("ignored = %d, want 1", summary.Ignored)
	}
}

// Without an architecture section the graph pass stays out of the way.
func TestArchitectureOffByDefault(t *testing.T) {
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	summary, err := eng.Run("testdata/arch")
	if err != nil {
		t.Fatal(err)
	}
	counts := ruleCounts(summary.Findings)
	for _, id := range []string{arch.RuleLayerViolation, arch.RuleForbiddenEdge, arch.RuleImportCycle} {
		if counts[id] != 0 {
			t.Errorf("%s = %d, want 0 without an architecture policy", id, counts[id])
		}
	}
	for _, m := range eng.Rules() {
		if m.ID == arch.RuleImportCycle {
			t.Error("architecture rules must not appear in the catalogue when unconfigured")
		}
	}
}

// When configured, the architecture checks are part of the rule catalogue that
// SARIF and editors read.
func TestArchitectureInCatalogue(t *testing.T) {
	eng := newPolicyEngine(t, archPolicy)
	seen := map[string]bool{}
	for _, m := range eng.Rules() {
		seen[m.ID] = true
	}
	for _, id := range []string{arch.RuleLayerViolation, arch.RuleForbiddenEdge, arch.RuleImportCycle} {
		if !seen[id] {
			t.Errorf("%s missing from the rule catalogue", id)
		}
	}
}
