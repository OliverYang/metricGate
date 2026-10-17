package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The retention keys must be invisible until an operator writes them. This is
// the direct evidence for D22-5: a policy that predates the key keeps its
// behaviour byte for byte, because the zero value means "keep everything" and
// the field is omitted when zero.
func TestRetainDefaultsToUnlimited(t *testing.T) {
	cfg, err := Load([]byte("version: 1\nstore:\n  enabled: true\n  path: \".metricgate/history.db\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Store.Retain.Runs; got != 0 {
		t.Errorf("Retain.Runs = %d, want 0", got)
	}
	if got := cfg.Store.Retain.Days; got != 0 {
		t.Errorf("Retain.Days = %d, want 0", got)
	}
	if cfg.Store.PruneOnWrite {
		t.Error("PruneOnWrite = true, want false")
	}
	if !cfg.Store.Retain.Unlimited() {
		t.Error("a store with no retain keys must report Unlimited")
	}
	// Marshal must not introduce the keys either: the written document has to
	// look like the one the repository wrote.
	out, err := yaml.Marshal(cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "retain") || strings.Contains(string(out), "prune_on_write") {
		t.Errorf("zero-value store marshals retention keys:\n%s", out)
	}
}

// The built-in template is what a fresh `metricgate init` writes, so it must
// not start pruning either.
func TestBuiltinPolicyKeepsUnlimitedRetention(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Store.Retain.Unlimited() || cfg.Store.PruneOnWrite {
		t.Errorf("built-in defaults prune: retain=%+v prune_on_write=%v", cfg.Store.Retain, cfg.Store.PruneOnWrite)
	}
}

func TestRetainParsesBothBoundsIndependently(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want RetainConfig
	}{
		{"runs only", "version: 1\nstore:\n  retain:\n    runs: 500\n", RetainConfig{Runs: 500}},
		{"days only", "version: 1\nstore:\n  retain:\n    days: 90\n", RetainConfig{Days: 90}},
		{"both", "version: 1\nstore:\n  retain:\n    runs: 500\n    days: 90\n", RetainConfig{Runs: 500, Days: 90}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load([]byte(tc.yaml))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Store.Retain != tc.want {
				t.Errorf("Retain = %+v, want %+v", cfg.Store.Retain, tc.want)
			}
			if cfg.Store.Retain.Unlimited() {
				t.Error("a written bound must not report Unlimited")
			}
		})
	}
}

func TestPruneOnWriteIsExplicitOptIn(t *testing.T) {
	cfg, err := Load([]byte("version: 1\nstore:\n  retain:\n    runs: 10\n  prune_on_write: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Store.PruneOnWrite {
		t.Error("prune_on_write: true was not recorded")
	}
}

// A negative bound is rejected rather than read as unlimited: silently keeping
// everything while the policy appears to bound history is the exact failure
// this iteration exists to remove.
func TestRetainRejectsNegativeBounds(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"negative runs", "version: 1\nstore:\n  retain:\n    runs: -1\n", "runs=-1 days=0"},
		{"negative days", "version: 1\nstore:\n  retain:\n    days: -30\n", "runs=0 days=-30"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("Load accepted a negative bound: %+v", cfg.Store.Retain)
			}
			if !strings.Contains(err.Error(), "store.retain") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %q, want store.retain error mentioning %q", err, tc.want)
			}
		})
	}
}

// Retention is policy, so `rules explain` must be able to attribute it to the
// document that wrote it rather than to the built-in default.
func TestRetainKeysAreExplicit(t *testing.T) {
	cfg, err := Load([]byte("version: 1\nstore:\n  retain:\n    runs: 200\n  prune_on_write: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"store.retain.runs", "store.prune_on_write"} {
		if !cfg.Explicit(key) {
			t.Errorf("%s not recorded as an explicit key", key)
		}
	}
	if cfg.Explicit("store.retain.days") {
		t.Error("store.retain.days was never written but reports explicit")
	}
}
