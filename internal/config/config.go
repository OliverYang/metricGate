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
	"strings"

	"gopkg.in/yaml.v3"

	"metricgate/internal/arch"
	"metricgate/internal/clones"
	"metricgate/internal/model"
)

// BanConfig is one organisation-level banned API or import. Match is a call
// pattern ("fmt.Print", "System.out.print", "panic"), an import path, or a
// Java class name for kind "constructor" (matches new X()). A
// trailing "*" on a call/constructor name is a prefix match. Kind defaults
// to "call".
type BanConfig struct {
	Kind        string   `yaml:"kind,omitempty"`
	Match       string   `yaml:"match"`
	Reason      string   `yaml:"reason,omitempty"`
	Use         string   `yaml:"use,omitempty"`          // compliant alternative; rendered in findings and digests
	ArgsBan     []string `yaml:"args_ban,omitempty"`     // parameter-aware: fire only when a string-literal argument equals one of these
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
	// BannedSeverity is the severity every banned-api rule of this language
	// falls back to when the rule has no severity of its own. Empty means
	// "use each ban rule's built-in default".
	BannedSeverity string `yaml:"banned-api-severity,omitempty"`
}

// CloneConfig tunes the duplicate-code pass (function-level Type-2 clone
// detection). The zero value disables the pass entirely.
type CloneConfig struct {
	Enabled   *bool `yaml:"enabled,omitempty"`
	MinLines  int   `yaml:"min_lines,omitempty"`
	MinTokens int   `yaml:"min_tokens,omitempty"`
}

// ArchConfig is the architecture section: layer boundaries and forbidden
// dependency directions, enforced over the package graph built by internal/arch.
type ArchConfig struct {
	Edges  string `yaml:"edges,omitempty"`  // severity of layer/edge violations
	Cycles string `yaml:"cycles,omitempty"` // severity of import cycles
	// SDP is the severity of stable-dependencies violations, or "off" to keep
	// recording the package metrics without emitting a finding. It defaults to
	// warning, and it should stay at or below gate.fail_on: reusing an existing
	// implementation adds a dependency edge, and a new edge can make a stable
	// package depend on a less stable one — failing on that would punish the
	// very behaviour the reuse guidance asks for.
	SDP string `yaml:"sdp,omitempty"`
	// SDPTolerance is the minimum instability gap that counts as a
	// stable-dependencies violation: an edge is reported only when the target
	// is more unstable by more than this. The zero value keeps Martin's strict
	// reading. Instability is a ratio over integer coupling counts, so its
	// resolution is about 1/(Ca+Ce); a tolerance near that value filters the
	// rounding noise without hiding a real dependency inversion.
	SDPTolerance float64       `yaml:"sdp_tolerance,omitempty"`
	Layers       []arch.Layer  `yaml:"layers,omitempty"`
	Forbid       []arch.Forbid `yaml:"forbid,omitempty"`
}

// DepsConfig declares the dependencies a repository has already accepted, so
// an import that is none of them can be flagged before it is merged. Entries
// are matched as prefixes on a path separator ("/" for Go, "." for Java).
type DepsConfig struct {
	Allow []string `yaml:"allow,omitempty"` // extra permitted import prefixes
	Self  []string `yaml:"self,omitempty"`  // this repository's own module/package prefixes
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
	Clones       CloneConfig               `yaml:"clones,omitempty"`
	Version      int                       `yaml:"version"`
	Preset       string                    `yaml:"preset,omitempty"`
	Gate         GateConfig                `yaml:"gate"`
	Exclude      []string                  `yaml:"exclude,omitempty"`
	Languages    map[string]LanguageConfig `yaml:"languages,omitempty"`
	Dependencies DepsConfig                `yaml:"dependencies,omitempty"`
	Architecture ArchConfig                `yaml:"architecture,omitempty"`
	Store        StoreConfig               `yaml:"store"`
	SourcePath   string                    `yaml:"-"`
	SourceKind   string                    `yaml:"-"`
	ExplicitKeys map[string]bool           `yaml:"-"`
	// presetKeys are the dotted keys the active preset supplied. They are the
	// preset's half of provenance: ValueSource reports them as "preset:<name>".
	presetKeys map[string]bool
}

// Default returns the built-in policy. It always parses; the error return
// exists for symmetry with Load.
func Default() (*Config, error) {
	return LoadWithSource([]byte(DefaultYAML), "builtin", "")
}

// Load parses policy data and normalises it. Values are marked as coming
// from a policy document; callers loading a known path should use LoadFile.
func Load(data []byte) (*Config, error) {
	return LoadWithSource(data, "policy_file", "")
}

// LoadWithSource loads YAML while retaining explicit-key provenance for
// explain/report consumers. Existing Config values and YAML semantics are
// unchanged; provenance is metadata, not a second policy layer.
func LoadWithSource(data []byte, source, sourcePath string) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse policy: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse policy keys: %w", err)
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
	c.SourceKind = source
	c.SourcePath = sourcePath
	c.ExplicitKeys = map[string]bool{}
	if len(doc.Content) > 0 {
		collectYAMLKeys(doc.Content[0], "", c.ExplicitKeys)
	}
	// The preset is overlaid after the explicit keys are known, so it can tell
	// "the repository wrote this" from "nobody wrote this yet".
	if err := c.applyPresetByName(); err != nil {
		return nil, err
	}
	return &c, nil
}

func collectYAMLKeys(n *yaml.Node, prefix string, keys map[string]bool) {
	if n == nil {
		return
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			path := k.Value
			if prefix != "" {
				path = prefix + "." + path
			}
			keys[path] = true
			collectYAMLKeys(v, path, keys)
		}
		return
	}
	for _, child := range n.Content {
		collectYAMLKeys(child, prefix, keys)
	}
}

// Explicit reports whether the YAML document contained this dotted key.
func (c *Config) Explicit(key string) bool { return c != nil && c.ExplicitKeys[key] }

// ValueSource returns the configured source if the key was explicitly
// present, otherwise the supplied built-in source label. A key supplied by the
// active preset reports "preset:<name>", so a preset can never masquerade as a
// built-in default.
func (c *Config) ValueSource(key, builtin string) (source, path string, explicit bool) {
	if c == nil {
		return builtin, "", false
	}
	if c.SourceKind != "builtin" && c.Explicit(key) {
		return c.SourceKind, c.SourcePath, true
	}
	if c.presetKeys[key] {
		return "preset:" + c.Preset, "", true
	}
	return builtin, "", false
}

// LoadFile reads and parses a policy file from disk, preserving its path for
// explain output.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadWithSource(data, "policy_file", path)
}

func langSection(c *Config, lang string) (LanguageConfig, bool) {
	sec, ok := c.Languages[lang]
	return sec, ok
}

// RuleSeverity returns the configured severity for a rule, or "" if unset.
// A banned-api rule with no severity of its own falls back to the language's
// banned-api-severity, which is how a preset can tighten a whole ban family
// whose rule IDs it cannot know in advance.
func (c *Config) RuleSeverity(lang, ruleID string) string {
	sec, ok := langSection(c, lang)
	if !ok {
		return ""
	}
	if rc, ok := sec.Rules[ruleID]; ok && rc.Severity != "" {
		return rc.Severity
	}
	if strings.HasPrefix(ruleID, "banned-") {
		return sec.BannedSeverity
	}
	return ""
}

// BanSeverity returns the language's banned-api fallback severity, or "".
func (c *Config) BanSeverity(lang string) string {
	sec, ok := langSection(c, lang)
	if !ok {
		return ""
	}
	return sec.BannedSeverity
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

// ArchPolicy returns the architecture constraint model of this policy.
func (c *Config) ArchPolicy() arch.Policy {
	return arch.Policy{Layers: c.Architecture.Layers, Forbids: c.Architecture.Forbid}
}

// ArchSeverity is the severity of architecture findings. kind is "edges" for
// ClonePolicy returns the duplicate-code pass configuration: clones.Default
// when the policy leaves it unset, or the policy values with an explicit
// off switch. Thresholds of 0 mean "use the default" for that knob.
func (c *Config) ClonePolicy() clones.Config {
	cfg := clones.Default
	if c.Clones.Enabled != nil && !*c.Clones.Enabled {
		return clones.Config{} // the zero value is the documented off switch
	}
	if c.Clones.MinLines > 0 {
		cfg.MinLines = c.Clones.MinLines
	}
	if c.Clones.MinTokens > 0 {
		cfg.MinTokens = c.Clones.MinTokens
	}
	return cfg
}

// layer and forbidden-edge findings, "cycles" for import cycles; both default
// to warning so that switching architecture checking on never breaks a build
// by surprise.
//
// "sdp" additionally accepts "off", which is not a severity: the caller must
// skip emitting those findings rather than look them up here (see
// Engine.appendSdpFindings).
func (c *Config) ArchSeverity(kind string) model.Severity {
	value := c.Architecture.Edges
	if kind == "cycles" {
		value = c.Architecture.Cycles
	}
	if kind == "sdp" {
		value = c.Architecture.SDP
	}
	if value == "" {
		return model.SevWarning
	}
	return model.Severity(value)
}
