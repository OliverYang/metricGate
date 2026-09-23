// Package config loads and normalises the policy file (policy.yaml) that
// drives the whole tool: which rules run, their severities and options, the
// metric thresholds, the gate behaviour and the trend store.
//
// The policy file is the "constraint model" of the project: the same file is
// consumed by the checker today and by the context/skeleton generators later.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"metricgate/internal/model"
)

// BanConfig is one organisation-level banned API or import. Match is a call
// pattern ("fmt.Print", "System.out.print", "panic") or an import path. A
// trailing "*" on a call name is a prefix match. Kind defaults to "call".
type BanConfig struct {
	Kind        string   `yaml:"kind,omitempty"`
	Match       string   `yaml:"match"`
	Reason      string   `yaml:"reason,omitempty"`
	ExceptFiles []string `yaml:"except_files,omitempty"` // file-name suffixes
}

// Bans returns the policy ban list of one language (nil when unset).
func (c *Config) Bans(lang string) []BanConfig {
	sec, ok := langSection(c, lang)
	if !ok {
		return nil
	}
	return sec.Banned
}

// RuleConfig overrides one rule. A nil Enabled means "use the rule default".
type RuleConfig struct {
	Enabled  *bool          `yaml:"enabled,omitempty"`
	Severity string         `yaml:"severity,omitempty"`
	Options  map[string]any `yaml:"options,omitempty"`
}

// MetricConfig overrides one metric's thresholds. A zero value means
// "use the metric default".
type MetricConfig struct {
	Enabled *bool `yaml:"enabled,omitempty"`
	Warn    int   `yaml:"warn,omitempty"`
	Error   int   `yaml:"error,omitempty"`
}

// LanguageConfig groups per-language overrides.
type LanguageConfig struct {
	Rules   map[string]RuleConfig   `yaml:"rules,omitempty"`
	Metrics map[string]MetricConfig `yaml:"metrics,omitempty"`
	Banned  []BanConfig             `yaml:"banned-api,omitempty"`
}

// GateConfig controls when the check command exits non-zero.
type GateConfig struct {
	FailOn string `yaml:"fail_on"` // "error" (default) or "warning"
}

// StoreConfig controls the local trend store (SQLite).
type StoreConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
}

// Config is the parsed policy file.
type Config struct {
	Version   int                       `yaml:"version"`
	Gate      GateConfig                `yaml:"gate"`
	Exclude   []string                  `yaml:"exclude,omitempty"`
	Languages map[string]LanguageConfig `yaml:"languages,omitempty"`
	Store     StoreConfig               `yaml:"store"`
}

// DefaultYAML is the policy emitted by `metricgate init` and used when no
// policy file is present.
const DefaultYAML = `# metricgate policy — the constraint model for this repository.
# All values below are the defaults; delete lines you do not need to override.
version: 1

gate:
  # Exit code 1 when any finding at or above this severity is present.
  fail_on: error

# Directory names to skip while walking the tree.
exclude: [".git", "vendor", "node_modules", "testdata"]

languages:
  go:
    rules:
      empty-block:
        severity: warning
      todo-comment:
        enabled: false
        severity: info
    metrics:
      cyclomatic-complexity: { warn: 10, error: 15 }
      cognitive-complexity:  { warn: 15, error: 25 }
      function-length:       { warn: 50, error: 100 }
      file-length:           { warn: 400, error: 800 }
      nesting-depth:         { warn: 4,  error: 6 }
      params-count:          { warn: 5,  error: 8 }
    # Organisation bans, evaluated by the same deterministic engine. A trailing
    # "*" is a name prefix; except_files lists file-name suffixes to skip;
    # kind: import bans a package import instead of a call. Override severity
    # or add allowed_files under rules.<id>, where <id> is "banned-" plus the
    # match with dots replaced by dashes (e.g. banned-fmt-Print*).
    banned-api:
      - match: fmt.Print*
        reason: direct fmt.Print* writes to stdout; use the project logger
      - match: panic
        reason: direct panic(); return an error so callers can decide
        except_files: ["_test.go"]

  java:
    rules:
      print-stack-trace:
        severity: warning
      empty-block:
        severity: warning
      todo-comment:
        enabled: false
        severity: info
    metrics:
      cyclomatic-complexity: { warn: 10, error: 15 }
      cognitive-complexity:  { warn: 15, error: 25 }
      function-length:       { warn: 50, error: 100 }
      file-length:           { warn: 400, error: 800 }
      nesting-depth:         { warn: 4,  error: 6 }
      params-count:          { warn: 5,  error: 8 }
    banned-api:
      - match: System.out.print*
        reason: direct System.out.print* writes to the console; use the project logger
      - match: System.err.print*
        reason: direct System.err.print* writes to the console; use the project logger


store:
  enabled: false
  path: ".metricgate/history.db"
`

// Default returns the built-in policy. It always parses; the error return
// exists for symmetry with Load.
func Default() (*Config, error) { return Load([]byte(DefaultYAML)) }

// Load parses policy data and normalises it.
func Load(data []byte) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse policy: %w", err)
	}
	if c.Version == 0 {
		c.Version = 1
	}
	if c.Gate.FailOn == "" {
		c.Gate.FailOn = "error"
	}
	if c.Store.Path == "" {
		c.Store.Path = ".metricgate/history.db"
	}
	if c.Languages == nil {
		c.Languages = map[string]LanguageConfig{}
	}
	return &c, nil
}

// LoadFile reads and parses a policy file from disk.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Load(data)
}

func langSection(c *Config, lang string) (LanguageConfig, bool) {
	sec, ok := c.Languages[lang]
	return sec, ok
}

// RuleSeverity returns the configured severity for a rule, or "" if unset.
func (c *Config) RuleSeverity(lang, ruleID string) string {
	sec, ok := langSection(c, lang)
	if !ok {
		return ""
	}
	rc, ok := sec.Rules[ruleID]
	if !ok {
		return ""
	}
	return rc.Severity
}

// RuleEnabled returns the configured enabled flag for a rule, or nil if unset.
func (c *Config) RuleEnabled(lang, ruleID string) *bool {
	sec, ok := langSection(c, lang)
	if !ok {
		return nil
	}
	rc, ok := sec.Rules[ruleID]
	if !ok {
		return nil
	}
	return rc.Enabled
}

// RuleOptions returns the configured options for a rule (may be nil).
func (c *Config) RuleOptions(lang, ruleID string) map[string]any {
	sec, ok := langSection(c, lang)
	if !ok {
		return nil
	}
	rc, ok := sec.Rules[ruleID]
	if !ok {
		return nil
	}
	return rc.Options
}

// MetricThresholds returns the configured thresholds for a metric. Zero
// values mean "not overridden; use the metric default".
func (c *Config) MetricThresholds(lang, metricID string) (warn, errAt int, enabled *bool) {
	sec, ok := langSection(c, lang)
	if !ok {
		return 0, 0, nil
	}
	mc, ok := sec.Metrics[metricID]
	if !ok {
		return 0, 0, nil
	}
	return mc.Warn, mc.Error, mc.Enabled
}

// FailOn converts the gate setting into a Severity, defaulting to error.
func (c *Config) FailOn() model.Severity {
	if c.Gate.FailOn == "warning" {
		return model.SevWarning
	}
	return model.SevError
}
