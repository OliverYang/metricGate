package config

import "testing"

// A policy that names no preset must behave exactly as before presets existed:
// no preset keys, and every value reported as coming from where it always did.
func TestPresetAbsentChangesNothing(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Preset != "" || len(cfg.presetKeys) != 0 {
		t.Fatalf("default preset=%q keys=%d, want none", cfg.Preset, len(cfg.presetKeys))
	}
	if source, _, explicit := cfg.ValueSource("languages.go.metrics.function-length", "builtin"); source != "builtin" || explicit {
		t.Errorf("source=%q explicit=%v, want builtin,false", source, explicit)
	}
	if got := cfg.MetricThresholdsFor("go", "function-length"); got != (MetricThresholds{Warn: 50, Error: 100}) {
		t.Errorf("function-length thresholds = %+v, want the built-in 50/100", got)
	}
}

// The agentic preset supplies values the document leaves unset, and every one
// of them reports "preset:agentic" so it can never pass as a built-in default.
func TestPresetAgenticAppliesAndIsAttributed(t *testing.T) {
	cfg, err := LoadWithSource([]byte("version: 1\npreset: agentic\n"), "policy_file", "p.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MetricThresholdsFor("go", "function-length"); got != (MetricThresholds{Warn: 80, Error: 160}) {
		t.Errorf("function-length = %+v, want the preset's 80/160", got)
	}
	if source, _, _ := cfg.ValueSource("languages.go.metrics.function-length", "builtin"); source != "preset:agentic" {
		t.Errorf("function-length source = %q, want preset:agentic", source)
	}
	if got := cfg.ExplainRule("go", "empty-block"); got.Severity != "error" || got.PolicySource != "preset:agentic" {
		t.Errorf("empty-block explain = %+v, want error from preset:agentic", got)
	}
}

// A preset tightens the whole banned-api family through one fallback, without
// knowing any ban rule's id in advance.
func TestPresetTightensBannedSeverity(t *testing.T) {
	cfg, err := LoadWithSource([]byte("version: 1\npreset: agentic\n"), "policy_file", "p.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.RuleSeverity("go", "banned-some-org-rule"); got != "error" {
		t.Errorf("ban severity = %q, want error", got)
	}
	if got := cfg.BanSeverity("go"); got != "error" {
		t.Errorf("BanSeverity = %q, want error", got)
	}
}

// An explicit value always beats the preset; a sibling the document leaves
// alone still comes from the preset. This is the whole precedence contract.
func TestPresetExplicitValueWins(t *testing.T) {
	cfg, err := LoadWithSource([]byte(`version: 1
preset: agentic
languages:
  go:
    metrics:
      function-length: { warn: 30, error: 60 }
`), "policy_file", "p.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.MetricThresholdsFor("go", "function-length"); got != (MetricThresholds{Warn: 30, Error: 60}) {
		t.Errorf("function-length = %+v, want the document's 30/60", got)
	}
	if source, _, _ := cfg.ValueSource("languages.go.metrics.function-length", "builtin"); source != "policy_file" {
		t.Errorf("function-length source = %q, want policy_file", source)
	}
	if got := cfg.MetricThresholdsFor("go", "file-length"); got != (MetricThresholds{Warn: 800, Error: 1600}) {
		t.Errorf("file-length = %+v, want the preset's 800/1600", got)
	}
}

// A typo in the preset name is an error, never a silent fall-back to the
// default gate.
func TestPresetUnknownNameErrors(t *testing.T) {
	if _, err := LoadWithSource([]byte("version: 1\npreset: nope\n"), "policy_file", "p.yaml"); err == nil {
		t.Fatal("unknown preset must be an error")
	}
}

// The policy `init --preset` writes must actually activate the preset: if it
// spelled the defaults out, they would win and the preset would be inert.
func TestPresetYAMLActivatesPreset(t *testing.T) {
	p, ok := PresetByName("agentic")
	if !ok {
		t.Fatal("agentic preset missing")
	}
	cfg, err := LoadWithSource([]byte(PresetYAML(p)), "policy_file", "p.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Preset != "agentic" {
		t.Fatalf("preset = %q, want agentic", cfg.Preset)
	}
	if got := cfg.MetricThresholdsFor("go", "function-length"); got != (MetricThresholds{Warn: 80, Error: 160}) {
		t.Errorf("function-length = %+v, want the preset's 80/160 (the starter must not freeze it)", got)
	}
}
