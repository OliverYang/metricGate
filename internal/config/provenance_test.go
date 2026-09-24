package config

import "testing"

func TestLoadTracksExplicitPolicyKeysAndSource(t *testing.T) {
	cfg, err := LoadWithSource([]byte(`version: 1
languages:
  go:
    metrics:
      cyclomatic-complexity: {warn: 7}
    rules:
      empty-block:
        enabled: false
exclude: ["testdata"]
`), "policy_file", "team-policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"languages.go.metrics.cyclomatic-complexity", "languages.go.rules.empty-block.enabled", "exclude"} {
		source, path, explicit := cfg.ValueSource(key, "builtin")
		if !explicit || source != "policy_file" || path != "team-policy.yaml" {
			t.Errorf("ValueSource(%q) = (%q,%q,%v)", key, source, path, explicit)
		}
	}
	if source, _, explicit := cfg.ValueSource("languages.go.metrics.params-count", "builtin"); explicit || source != "builtin" {
		t.Errorf("unconfigured key source = %q explicit=%v, want builtin,false", source, explicit)
	}
}

func TestDefaultMarksBuiltinSource(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SourceKind != "builtin" || cfg.SourcePath != "" {
		t.Errorf("default source=(%q,%q), want builtin/empty", cfg.SourceKind, cfg.SourcePath)
	}
	source, _, explicit := cfg.ValueSource("languages.go.metrics.cyclomatic-complexity", "builtin")
	if source != "builtin" || explicit {
		t.Errorf("source=%q explicit=%v, want builtin,false", source, explicit)
	}
}
