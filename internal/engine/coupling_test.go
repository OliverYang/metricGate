package engine

import (
	"strings"
	"testing"

	"metricgate/internal/arch"
	"metricgate/internal/config"
	"metricgate/internal/coupling"
	"metricgate/internal/diff"
)

// fakeHistory is the test-side implementation of the engine's history port. The
// CLI supplies the production one at the composition root
// (cmd/metricgate/coupling_cmd.go).
//
// The port exists so the checker core never links the git adapter, and the
// tests honor that by not linking it either: a fake also makes the history
// explicit and the test instant, instead of building repositories on disk.
type fakeHistory struct {
	shallow bool
	commits []coupling.Commit
}

func (h fakeHistory) IsShallow() bool { return h.shallow }

func (h fakeHistory) Commits() ([]coupling.Commit, error) { return h.commits, nil }

// pairHistory builds a history where the two files moved together n times.
func pairHistory(n int, a, b string) fakeHistory {
	h := fakeHistory{}
	for i := 0; i < n; i++ {
		h.commits = append(h.commits, coupling.Commit{
			Files: []coupling.File{{Path: a}, {Path: b}},
		})
	}
	return h
}

// couplingConfig is the built-in policy plus the coupling gate enabled, with
// the layers the fixtures use.
func couplingConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Architecture.Layers = []arch.Layer{
		{Name: "cli", Packages: []string{"cmd/**"}},
		{Name: "core", Packages: []string{"internal/**"}},
	}
	cfg.Architecture.Coupling = config.CouplingGate{Enabled: true, MinCount: 5}
	return cfg
}

// couplingEngine builds an engine with the coupling gate on.
func couplingEngine(t *testing.T) *Engine {
	t.Helper()
	eng, err := New(couplingConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return eng
}

// crossLayerChange is the change set that contains both halves of the pair.
func crossLayerChange() map[string][]diff.Range {
	return map[string][]diff.Range{
		"cmd/cli.go":            {{Start: 2, End: 2}},
		"internal/core/core.go": {{Start: 2, End: 2}},
	}
}

func TestCouplingFindingsReportsCrossLayerPairInChangeSet(t *testing.T) {
	eng := couplingEngine(t)
	history := pairHistory(7, "cmd/cli.go", "internal/core/core.go")

	got := eng.CouplingFindings(".", crossLayerChange(), history)
	if len(got) != 1 {
		t.Fatalf("findings = %d, want 1 (%+v)", len(got), got)
	}
	f := got[0]
	if f.RuleID != RuleIDCoupling {
		t.Errorf("rule = %q, want %q", f.RuleID, RuleIDCoupling)
	}
	if f.File != "cmd/cli.go" {
		t.Errorf("file = %q, want the first path of the pair", f.File)
	}
	// The finding must name the boundary and the evidence behind it, because
	// that is what the repair channel has to act on.
	if !strings.Contains(f.Message, "cli -> core") || !strings.Contains(f.Message, "7 commits") {
		t.Errorf("message = %q, want the layer boundary and the count", f.Message)
	}
	if f.Severity != "warning" {
		t.Errorf("severity = %q, want warning by default", f.Severity)
	}
}

// Only pairs whose BOTH files are in the change set are this change's problem:
// a pre-existing coupling elsewhere is not what the current change broke.
func TestCouplingFindingsIgnoresPairOutsideChangeSet(t *testing.T) {
	eng := couplingEngine(t)
	history := pairHistory(7, "cmd/cli.go", "internal/core/core.go")

	changed := map[string][]diff.Range{"cmd/cli.go": {{Start: 2, End: 2}}}
	if got := eng.CouplingFindings(".", changed, history); len(got) != 0 {
		t.Errorf("findings = %+v, want none (the other half did not change)", got)
	}
}

func TestCouplingFindingsDisabledByDefault(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	history := pairHistory(7, "cmd/cli.go", "internal/core/core.go")
	if got := eng.CouplingFindings(".", crossLayerChange(), history); len(got) != 0 {
		t.Errorf("findings = %+v, want none without the policy opt-in", got)
	}
}

// A caller that never wires the history port gets no findings: the gate must
// not silently fall back to reading history itself (that is the layering rule).
func TestCouplingFindingsWithoutHistoryPortStaysQuiet(t *testing.T) {
	eng := couplingEngine(t)
	if got := eng.CouplingFindings(".", crossLayerChange(), nil); len(got) != 0 {
		t.Errorf("findings = %+v, want none without a history port", got)
	}
}

// Same-layer coupling is not a boundary crossing and must stay quiet.
func TestCouplingFindingsIgnoresSameLayerPair(t *testing.T) {
	eng := couplingEngine(t)
	history := pairHistory(7, "internal/a.go", "internal/b.go")

	changed := map[string][]diff.Range{
		"internal/a.go": {{Start: 2, End: 2}},
		"internal/b.go": {{Start: 2, End: 2}},
	}
	if got := eng.CouplingFindings(".", changed, history); len(got) != 0 {
		t.Errorf("findings = %+v, want none inside one layer", got)
	}
}

// A shallow clone must produce no coupling findings: its truncated history
// would undercount every pair, and an undercounted gate is a lying gate.
func TestCouplingFindingsRefusesShallowClone(t *testing.T) {
	eng := couplingEngine(t)
	history := pairHistory(7, "cmd/cli.go", "internal/core/core.go")
	history.shallow = true

	if got := eng.CouplingFindings(".", crossLayerChange(), history); len(got) != 0 {
		t.Errorf("findings = %+v, want none from a shallow clone", got)
	}
}

// A pair below the configured floor is not habitual yet.
func TestCouplingFindingsRespectsMinCount(t *testing.T) {
	eng := couplingEngine(t)
	history := pairHistory(4, "cmd/cli.go", "internal/core/core.go")

	if got := eng.CouplingFindings(".", crossLayerChange(), history); len(got) != 0 {
		t.Errorf("findings = %+v, want none below min_count 5", got)
	}
}

func TestCouplingRuleMetaOnlyWhenEnabled(t *testing.T) {
	off, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	if metas := couplingRuleMeta(off); len(metas) != 0 {
		t.Errorf("metas = %+v, want none when the gate is off", metas)
	}
	on := couplingConfig(t)
	metas := couplingRuleMeta(on)
	if len(metas) != 1 || metas[0].ID != RuleIDCoupling {
		t.Errorf("metas = %+v, want the change-coupling entry", metas)
	}
}
