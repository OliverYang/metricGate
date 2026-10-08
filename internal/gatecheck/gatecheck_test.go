package gatecheck

import (
	"reflect"
	"strings"
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/diff"
	"metricgate/internal/model"
	"metricgate/internal/refs"
	"metricgate/internal/symbols"
)

func fixtureIndexes() (*symbols.Index, *refs.Index) {
	defs := []symbols.Def{
		{Name: "WidelyUsed", File: "internal/core/a.go", Line: 10},
		{Name: "Tiny", File: "internal/core/tiny.go", Line: 3},
	}
	uses := []refs.Ref{
		{Name: "WidelyUsed", Kind: refs.KindCall, File: "service/x.go", Line: 5},
		{Name: "WidelyUsed", Kind: refs.KindCall, File: "service/y.go", Line: 7},
		{Name: "WidelyUsed", Kind: refs.KindType, File: "handler/z.go", Line: 9},
		{Name: "Tiny", Kind: refs.KindCall, File: "internal/core/tiny.go", Line: 8},
	}
	return symbols.Build(defs), refs.Build(uses)
}

func TestChangedSymbols(t *testing.T) {
	defs, _ := fixtureIndexes()
	got := ChangedSymbols(defs, map[string][]diff.Range{
		"internal/core/a.go":    {{Start: 1, End: 2}},
		"internal/core/tiny.go": {{Start: 1, End: 1}},
	})
	want := []string{"Tiny", "WidelyUsed"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ChangedSymbols = %v, want %v", got, want)
	}
	if got := ChangedSymbols(defs, nil); got != nil {
		t.Errorf("ChangedSymbols(nil changed) = %v, want nil", got)
	}
}

func TestBlastRadiusOverCap(t *testing.T) {
	defs, uses := fixtureIndexes()
	opts := Options{
		MaxAffectedFiles: 2,
		WantImpact:       true,
		Changed: map[string][]diff.Range{
			"internal/core/a.go": {{Start: 10, End: 12}},
		},
	}
	findings := BlastRadiusCheck(defs, uses, opts)
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1 (%v)", len(findings), findings)
	}
	f := findings[0]
	if f.RuleID != RuleIDImpactScope || f.Severity != model.SevError {
		t.Errorf("rule/sev = %s/%s, want impact-scope/error", f.RuleID, f.Severity)
	}
	if f.File != "internal/core/a.go" || f.StartLine != 10 {
		t.Errorf("anchor = %s:%d, want internal/core/a.go:10", f.File, f.StartLine)
	}
	for _, want := range []string{"WidelyUsed", "3 affected files > 2", "service/x.go", "LC-403"} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message %q missing %q", f.Message, want)
		}
	}
}

func TestBlastRadiusCleanCases(t *testing.T) {
	defs, uses := fixtureIndexes()
	base := Options{
		WantImpact: true,
		Changed:    map[string][]diff.Range{"internal/core/a.go": {{Start: 1, End: 1}}},
	}
	// Under the default cap: 3 affected files ≤ 25.
	if got := BlastRadiusCheck(defs, uses, base); got != nil {
		t.Errorf("default cap fired: %v", got)
	}
	// Disabled via negative cap.
	off := base
	off.MaxAffectedFiles = -1
	if got := BlastRadiusCheck(defs, uses, off); got != nil {
		t.Errorf("disabled cap fired: %v", got)
	}
	// Rule not wanted.
	noWant := base
	noWant.WantImpact = false
	noWant.MaxAffectedFiles = 1
	if got := BlastRadiusCheck(defs, uses, noWant); got != nil {
		t.Errorf("unwanted check fired: %v", got)
	}
	// Unchanged symbols never checked.
	other := base
	other.MaxAffectedFiles = 1
	other.Changed = map[string][]diff.Range{"unrelated/main.go": {{Start: 1, End: 1}}}
	if got := BlastRadiusCheck(defs, uses, other); got != nil {
		t.Errorf("unchanged file fired: %v", got)
	}
}

func TestTestsTouched(t *testing.T) {
	logicOnly := Options{
		WantTests: true,
		Changed:   map[string][]diff.Range{"internal/core/a.go": {{Start: 1, End: 2}}},
	}
	findings := TestsTouchedCheck(logicOnly)
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	f := findings[0]
	if f.RuleID != RuleIDTestsTouched || f.Severity != model.SevWarning {
		t.Errorf("rule/sev = %s/%s, want tests-touched/warning", f.RuleID, f.Severity)
	}
	if !strings.Contains(f.Message, "internal/core/a.go") || !strings.Contains(f.Message, "LC-404") {
		t.Errorf("message = %q, want file + LC-404", f.Message)
	}
	// A touched test file satisfies the rule.
	withTest := logicOnly
	withTest.Changed["internal/core/a_test.go"] = []diff.Range{{Start: 1, End: 1}}
	if got := TestsTouchedCheck(withTest); got != nil {
		t.Errorf("test-touched change fired: %v", got)
	}
	// Test-only change: nothing to say.
	testOnly := Options{WantTests: true, Changed: map[string][]diff.Range{"src/test/java/A.java": {{Start: 1, End: 1}}}}
	if got := TestsTouchedCheck(testOnly); got != nil {
		t.Errorf("test-only change fired: %v", got)
	}
	// Disabled.
	off := logicOnly
	off.WantTests = false
	if got := TestsTouchedCheck(off); got != nil {
		t.Errorf("disabled check fired: %v", got)
	}
}

func TestResolve(t *testing.T) {
	cfg := &config.Config{Languages: map[string]config.LanguageConfig{
		"go": {Rules: map[string]config.RuleConfig{
			RuleIDImpactScope:  {Severity: "error", Options: map[string]any{"max_affected_files": 7}},
			RuleIDTestsTouched: {Severity: "warning"},
		}},
	}}
	opts := Resolve(cfg)
	if !opts.WantImpact || opts.MaxAffectedFiles != 7 || opts.ImpactSeverity != model.SevError {
		t.Errorf("impact resolve = %+v", opts)
	}
	if !opts.WantTests || opts.TestsSeverity != model.SevWarning {
		t.Errorf("tests resolve = %+v", opts)
	}
	// Explicit enabled:false vetoes the check even with options present.
	no := false
	cfgOff := &config.Config{Languages: map[string]config.LanguageConfig{
		"go": {Rules: map[string]config.RuleConfig{
			RuleIDImpactScope: {Enabled: &no, Options: map[string]any{"max_affected_files": 7}},
		}},
	}}
	if opts := Resolve(cfgOff); opts.WantImpact {
		t.Errorf("disabled impact resolved on: %+v", opts)
	}
	// No declaration → not vetoed: the flag decides, policy refines.
	if opts := Resolve(&config.Config{}); !opts.WantImpact || !opts.WantTests {
		t.Errorf("silent config vetoed: %+v", opts)
	}
}
