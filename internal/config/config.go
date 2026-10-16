// Package config loads and normalises the policy file (.metricgate.yaml) that
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

// builtinBans is the ban floor: the organisation bans every policy gets even
// when its file never mentions banned-api. It mirrors the banned-api blocks of
// DefaultYAML, and exists separately because LoadFile does not merge with
// DefaultYAML — without a floor, merely adding a policy file would silently
// delete the built-in bans, leaving a repository with a policy less constrained
// than one without. An explicit policy entry with the same kind and match
// replaces the floor entry, so a project can retune a built-in ban instead of
// losing it.
var builtinBans = map[string][]BanConfig{
	"go": {
		{Match: "fmt.Print*", Reason: "direct fmt.Print* writes to stdout; use the project logger"},
		{Match: "panic", Reason: "direct panic(); return an error so callers can decide", ExceptFiles: []string{"_test.go"}},
	},
	"java": {
		{Match: "System.out.print*", Reason: "direct System.out.print* writes to the console; use the project logger"},
		{Match: "System.err.print*", Reason: "direct System.err.print* writes to the console; use the project logger"},
	},
}

// BanEntry is one effective ban plus where it came from: "builtin" for the
// floor every policy inherits, otherwise the policy source (policy_file,
// extends, preset) that declared the entry. Index is the entry's position in
// the declaring banned-api block (-1 for the floor), which is what a reader
// needs to find the line in the policy file.
type BanEntry struct {
	Ban    BanConfig
	Source string
	Index  int
}

// BanEntries returns the effective ban list of one language: the built-in
// floor first, then the policy's own entries. A policy entry with the same
// kind and match replaces the floor entry in place, so retuning a built-in ban
// does not duplicate it. This is the ban analogue of EffectiveThresholds — the
// one place that answers "which bans can fire here".
func (c *Config) BanEntries(lang string) []BanEntry {
	sec, _ := langSection(c, lang)
	entries := make([]BanEntry, 0, len(builtinBans[lang])+len(sec.Banned))
	for _, b := range builtinBans[lang] {
		entries = append(entries, BanEntry{Ban: b, Source: "builtin", Index: -1})
	}
	// One source for the whole block: provenance is recorded per YAML key, and
	// the sequence items share the banned-api key path.
	src, _, _ := c.ValueSource("languages."+lang+".banned-api", "builtin")
	for i, b := range sec.Banned {
		if j := banFloorIndex(entries, b); j >= 0 {
			entries[j] = BanEntry{Ban: b, Source: src, Index: i}
			continue
		}
		entries = append(entries, BanEntry{Ban: b, Source: src, Index: i})
	}
	return entries
}

// banFloorIndex finds the built-in entry a policy entry overrides.
func banFloorIndex(entries []BanEntry, b BanConfig) int {
	key := banKey(b)
	for i, e := range entries {
		if e.Source == "builtin" && banKey(e.Ban) == key {
			return i
		}
	}
	return -1
}

// banKey identifies a ban by kind and match. An empty kind means "call",
// which is also how the rules layer reads it.
func banKey(b BanConfig) string {
	kind := b.Kind
	if kind == "" {
		kind = "call"
	}
	return kind + "\x00" + b.Match
}

// Bans returns the effective ban list of one language: the built-in floor plus
// the policy entries. Use BanEntries when the provenance of each entry matters
// (digests, explain).
func (c *Config) Bans(lang string) []BanConfig {
	entries := c.BanEntries(lang)
	out := make([]BanConfig, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Ban)
	}
	return out
}

// CustomRules returns the project's DSL rule assertions for one language:
// rules whose languages list is empty apply to every language. Entries that
// do not name this language are filtered out here, so the rules layer only
// sees assertions that mean to apply.
func (c *Config) CustomRules(lang string) []DslRuleConfig {
	sec, ok := langSection(c, lang)
	if !ok {
		return nil
	}
	var out []DslRuleConfig
	for _, r := range sec.CustomRules {
		if len(r.Languages) == 0 {
			out = append(out, r)
			continue
		}
		for _, l := range r.Languages {
			if l == lang {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// RuleConfig overrides one rule. A nil Enabled means "use the rule default".
type RuleConfig struct {
	Enabled  *bool          `yaml:"enabled,omitempty"`
	Severity string         `yaml:"severity,omitempty"`
	Options  map[string]any `yaml:"options,omitempty"`
}

// MetricConfig overrides one metric's thresholds. A zero value means
// "use the metric default" — which is why "off" has its own flag: warn: 0
// cannot express "disabled" (it means "default").
//
// In YAML, `warn: off` / `error: off` disable that severity level entirely:
// the metric can only ever fire the other level. This is the honest form of
// the observation-period entry (`error: 5000` above the repo max was the
// workaround). Either flag can combine with an explicit value for the other
// level, e.g. `{ warn: 10, error: off }`.
type MetricConfig struct {
	Enabled *bool `yaml:"enabled,omitempty"`
	Warn    int   `yaml:"warn,omitempty"`
	Error   int   `yaml:"error,omitempty"`
	// WarnOff/ErrorOff record an explicit `warn: off` / `error: off` in the
	// policy file. Programmatic constructors (presets, CLI flags) leave them
	// false and keep the plain-int semantics.
	WarnOff  bool `yaml:"-"`
	ErrorOff bool `yaml:"-"`
}

// UnmarshalYAML accepts ints or the scalar "off" (case-insensitive) for the
// warn/error fields; any other string is a policy error, not a silent zero.
func (m *MetricConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Enabled *bool     `yaml:"enabled"`
		Warn    yaml.Node `yaml:"warn"`
		Error   yaml.Node `yaml:"error"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	m.Enabled = raw.Enabled
	for _, pair := range []struct {
		node yaml.Node
		slot *int
		off  *bool
		name string
	}{
		{raw.Warn, &m.Warn, &m.WarnOff, "warn"},
		{raw.Error, &m.Error, &m.ErrorOff, "error"},
	} {
		n := pair.node
		if n.Kind == 0 {
			continue // field absent
		}
		if n.Kind == yaml.ScalarNode {
			if strings.EqualFold(strings.TrimSpace(n.Value), "off") {
				*pair.off = true
				continue
			}
			var iv int
			if err := n.Decode(&iv); err != nil {
				return fmt.Errorf("metric %s: expected an integer or \"off\", got %q", pair.name, n.Value)
			}
			*pair.slot = iv
			continue
		}
		return fmt.Errorf("metric %s: expected an integer or \"off\", got a %s", pair.name, n.Tag)
	}
	return nil
}

// LanguageConfig groups per-language overrides.
type LanguageConfig struct {
	Rules   map[string]RuleConfig   `yaml:"rules,omitempty"`
	Metrics map[string]MetricConfig `yaml:"metrics,omitempty"`
	// TestMetrics overrides thresholds for test files only (_test.go,
	// Test.java, test*/ dirs — the same heuristic as the gate's test
	// detection). Tests legitimately run long and take many parameters, so
	// a project can relax baselines here instead of excluding the files
	// from measurement entirely.
	TestMetrics map[string]MetricConfig `yaml:"test_metrics,omitempty"`
	Banned      []BanConfig             `yaml:"banned-api,omitempty"`
	// BannedSeverity is the severity every banned-api rule of this language
	// falls back to when the rule has no severity of its own. Empty means
	// "use each ban rule's built-in default".
	BannedSeverity string `yaml:"banned-api-severity,omitempty"`
	// CustomRules are the project's own pattern assertions (the rule DSL):
	// name a tree-sitter node kind, optionally narrow it to a child field,
	// and assert a regex over the captured text. See internal/rules/dsl.go.
	CustomRules []DslRuleConfig `yaml:"custom_rules,omitempty"`
}

// DslRuleConfig is one user-authored rule assertion in .metricgate.yaml.
type DslRuleConfig struct {
	ID           string   `yaml:"id"`
	Title        string   `yaml:"title,omitempty"`
	Severity     string   `yaml:"severity,omitempty"`      // error|warning|note; default warning
	Node         string   `yaml:"node"`                    // tree-sitter node kind, e.g. call_expression
	Field        string   `yaml:"field,omitempty"`         // child field to capture, e.g. "function"; empty = whole node
	MatchRegex   string   `yaml:"match_regex,omitempty"`   // fire when the captured text matches (default: always)
	ExcludeRegex string   `yaml:"exclude_regex,omitempty"` // suppress when the captured text matches this
	Languages    []string `yaml:"languages,omitempty"`     // restrict to these languages; empty = every parsed language
	// Files restricts the rule to matching paths. The vocabulary is the same
	// one banned rules use for except_files/allowed_files: an exact path, a
	// prefix ("cmd/app/"), a suffix ("_test.go"), a glob matched against
	// the whole path or the base name, or a leading "**/" for "at any
	// depth" ("**/*_test.go"). Empty = every file, so an existing policy
	// behaves exactly as before.
	Files  []string `yaml:"files,omitempty"`
	Reason string   `yaml:"reason,omitempty"`
	Use    string   `yaml:"use,omitempty"` // suggested replacement, shown in the finding
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
	// CouplingExclude are extra path patterns the change-coupling analysis
	// ignores on top of its built-in lock/generated/mirrored defaults. It is
	// the project's own list of files whose co-change means nothing.
	CouplingExclude []string `yaml:"coupling_exclude,omitempty"`
	// Coupling gates on change coupling in -diff mode: a pair of changed files
	// that habitually change together across a declared layer boundary. It is
	// the erosion signal the dependency graph cannot see — the layering rule
	// still passes while the two files already move as one unit.
	Coupling CouplingGate `yaml:"coupling,omitempty"`
}

// CouplingGate configures the change-coupling gate. The zero value is the
// documented off switch: no coupling gate runs unless a policy asks for one.
type CouplingGate struct {
	// Enabled turns the check on. It is off by default because coupling is a
	// process signal, not a code defect, and a team should opt in after
	// looking at its own ranking once.
	Enabled bool `yaml:"enabled"`
	// MinCount is how many shared commits make a pair habitual (0 = 5).
	MinCount int `yaml:"min_count,omitempty"`
	// Since limits the history window ("3.months ago"; empty = all history).
	Since string `yaml:"since,omitempty"`
	// MaxCommitFiles drops sweep commits (0 = 12).
	MaxCommitFiles int `yaml:"max_commit_files,omitempty"`
	// Severity of a cross-layer coupling finding (default warning).
	Severity string `yaml:"severity,omitempty"`
}

// CouplingSeverity resolves the configured severity for a coupling finding.
func (c *Config) CouplingSeverity() model.Severity {
	if c.Architecture.Coupling.Severity == "" {
		return model.SevWarning
	}
	return model.Severity(c.Architecture.Coupling.Severity)
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
	// inherited is the dotted-key attribution of the documents this policy
	// extends, keyed by key and pointing at the nearest parent that wrote it.
	// It exists so provenance stays honest once inheritance lands: a threshold
	// the repository never wrote must not be reported as the repository's.
	inherited map[string]string
	// extendsChain is the chain of parent documents, nearest first.
	extendsChain []ExtendsSource
	// effective is the document actually in force after inheritance is folded
	// in. It is nil unless this policy extends another, and it is the bytes
	// the policy digest is taken over — see PolicyDocument.
	effective []byte
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
	// Inheritance is resolved before anything looks at the document, so every
	// later step (parse, key collection, provenance, the preset overlay) sees
	// one document: the effective one. A policy with no `extends:` key takes an
	// untouched path through here and produces byte-identical behaviour.
	ext, err := resolveExtends(data, sourcePath)
	if err != nil {
		return nil, err
	}
	if ext.effective != nil {
		data = ext.effective
	}
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
	c.inherited = map[string]string{}
	c.effective = ext.effective
	for _, parent := range ext.parents {
		c.extendsChain = append(c.extendsChain, ExtendsSource{Path: parent.path})
		for key := range parent.keys {
			// Nearest parent wins: that is the document a reader will open to
			// find out why this value is what it is.
			if _, seen := c.inherited[key]; !seen {
				c.inherited[key] = parent.path
			}
		}
	}
	if len(doc.Content) > 0 {
		collectYAMLKeys(doc.Content[0], "", c.ExplicitKeys)
	}
	// A key the repository wrote itself always beats any parent, even one the
	// effective document also carries through a merge. ownKeys is the leaf's
	// own contribution to the effective document, so an overridden parent key
	// is attributed to the child that overrode it and not to the parent.
	for key := range ext.ownKeys {
		delete(c.inherited, key)
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
// active preset reports "preset:<name>", and a key carried in from a parent
// document reports that parent's path, so a preset or an inheritred policy can
// never masquerade as something it is not.
func (c *Config) ValueSource(key, builtin string) (source, path string, explicit bool) {
	if c == nil {
		return builtin, "", false
	}
	// Inheritance is checked before the document's own keys because an
	// inherited value is the more specific provenance: the effective document
	// carries a parent's key verbatim, so an "explicit" test alone would credit
	// the child for a threshold it never wrote.
	if parent, ok := c.inherited[key]; ok {
		return "extends", parent, true
	}
	if c.SourceKind != "builtin" && c.Explicit(key) {
		return c.SourceKind, c.SourcePath, true
	}
	if c.presetKeys[key] {
		return "preset:" + c.Preset, "", true
	}
	return builtin, "", false
}

// Extends returns the chain of documents this policy extends, nearest parent
// first, with the keys each one contributed. A policy that extends nothing
// returns nil, so callers never have to distinguish "no inheritance" from
// "inheritance with an empty parent".
func (c *Config) Extends() []ExtendsSource {
	if c == nil {
		return nil
	}
	return c.extendsChain
}

// ExtendsSource is one parent document in a chain, for `rules explain` to
// report where an inherited value came from.
type ExtendsSource struct {
	Path string `json:"path"`
}

// PolicyDocument returns the effective policy document — the bytes actually in
// force after `extends:` inheritance is folded in — or nil when no inheritance
// happened (in which case the loaded file itself is the effective document).
//
// Callers that fingerprint a policy should prefer this over re-reading the
// source path, because a child policy and its parent enforce different rules
// and must therefore fingerprint differently. See bundle.PolicyDigest, which
// already does this.
func (c *Config) PolicyDocument() []byte {
	if c == nil {
		return nil
	}
	return c.effective
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
// values mean "not overridden; use the metric default". Use MetricWarnOff /
// MetricErrorOff to detect an explicit `off`.
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

// metricOff reports an explicit `warn: off` / `error: off` on the metric.
func (c *Config) metricOff(lang, metricID string) (warnOff, errOff bool) {
	sec, ok := langSection(c, lang)
	if !ok {
		return false, false
	}
	mc, ok := sec.Metrics[metricID]
	if !ok {
		return false, false
	}
	return mc.WarnOff, mc.ErrorOff
}

// MetricWarnOff reports whether the policy disables the metric's warning
// level via `warn: off`.
func (c *Config) MetricWarnOff(lang, metricID string) bool {
	w, _ := c.metricOff(lang, metricID)
	return w
}

// MetricErrorOff reports whether the policy disables the metric's error
// level via `error: off`.
func (c *Config) MetricErrorOff(lang, metricID string) bool {
	_, e := c.metricOff(lang, metricID)
	return e
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

// TestMetricThresholds returns the configured thresholds for a metric on
// test files. Zero values mean "no test override; the regular resolution
// (default, then metrics override) applies". A test_metrics entry never
// *tightens* semantics away from the regular path — it is resolved first by
// the engine, which falls back to EffectiveThresholds when no override
// exists for a level.
func (c *Config) TestMetricThresholds(lang, metricID string) (warn, errAt int, enabled *bool) {
	sec, ok := langSection(c, lang)
	if !ok {
		return 0, 0, nil
	}
	mc, ok := sec.TestMetrics[metricID]
	if !ok {
		return 0, 0, nil
	}
	return mc.Warn, mc.Error, mc.Enabled
}

// TestMetricOff reports an explicit `warn: off` / `error: off` under
// test_metrics for the metric.
func (c *Config) TestMetricOff(lang, metricID string) (warnOff, errOff bool) {
	sec, ok := langSection(c, lang)
	if !ok {
		return false, false
	}
	mc, ok := sec.TestMetrics[metricID]
	if !ok {
		return false, false
	}
	return mc.WarnOff, mc.ErrorOff
}
