// Presets are named, built-in overlays of policy values that a repository can
// opt into with a single `preset:` line. They exist so a class of work (here:
// code written by a language model) can be gated the way that work actually
// fails, without every repository re-deriving the same deltas.
//
// A preset is a *default layer*, not a second policy: explicit YAML always
// wins, and every value a preset supplies is recorded so `rules explain` can
// report where it came from. A preset that is never named changes nothing —
// the built-in default policy has no preset, so its behaviour is byte-for-byte
// what it was before presets existed.
package config

import (
	"fmt"
	"sort"
	"strings"
)

// Preset is one named overlay. Every map is keyed by language id; the zero
// value of any section means "this preset does not touch it".
type Preset struct {
	Name             string
	Note             string
	RuleSeverity     map[string]map[string]string
	RuleEnabled      map[string]map[string]bool
	MetricThresholds map[string]map[string]MetricConfig
	BannedSeverity   map[string]string
}

// presets holds the built-in presets by name.
var presets = map[string]Preset{
	"agentic": agenticPreset,
}

// agenticRelaxedMetrics are the pure size/style ceilings the agentic preset
// lifts. Complexity metrics are deliberately absent: they are the signal the
// gate exists for, and a generated function that is genuinely too complex
// should still fail.
var agenticRelaxedMetrics = map[string]MetricConfig{
	"function-length": {Warn: 80, Error: 160},
	"file-length":     {Warn: 800, Error: 1600},
	"params-count":    {Warn: 6, Error: 9},
}

// agenticPreset tunes the gate for code written by a language model. The
// rationale is the one SonarQube gave for its "Sonar way for Agentic AI" gate
// (2026.4): generated code fails differently — it swallows errors and reaches
// for banned APIs more often, while legitimately running long in file and
// function size. The preset therefore moves severity up on failure modes and
// down on pure size/style ceilings, and leaves the complexity metrics exactly
// where the default policy put them.
var agenticPreset = Preset{
	Name: "agentic",
	Note: "stricter on failure modes (swallowed errors, banned APIs, pseudo-completion markers), looser on pure size/style ceilings",
	RuleSeverity: map[string]map[string]string{
		"go":         {"empty-block": "error", "unknown-dependency": "error", "todo-comment": "warning"},
		"java":       {"empty-block": "error", "print-stack-trace": "error", "unknown-dependency": "error", "todo-comment": "warning"},
		"python":     {"empty-block": "error", "unknown-dependency": "error", "todo-comment": "warning"},
		"typescript": {"empty-block": "error", "unknown-dependency": "error", "todo-comment": "warning"},
		"tsx":        {"empty-block": "error", "unknown-dependency": "error", "todo-comment": "warning"},
		"javascript": {"empty-block": "error", "unknown-dependency": "error", "todo-comment": "warning"},
		"c":          {"empty-block": "error", "unknown-dependency": "error", "todo-comment": "warning"},
		"cpp":        {"empty-block": "error", "unknown-dependency": "error", "todo-comment": "warning"},
		"csharp":     {"empty-block": "error", "unknown-dependency": "error", "test-no-assert": "warning", "todo-comment": "warning"},
		"rust":       {"empty-block": "error", "unknown-dependency": "error", "test-no-assert": "warning", "todo-comment": "warning"},
	},
	// todo-comment is off in the Go rule pack by default (real repos are full
	// of legitimate TODOs), but a TODO marker left in agent-written code is a
	// completion claim the work cannot back — the exact pseudo-completion
	// signal the agentic preset exists for. Warning severity: visible in the
	// scorecard's incomplete-work counter, never a gate blocker.
	RuleEnabled: map[string]map[string]bool{
		"go":         {"todo-comment": true},
		"java":       {"todo-comment": true},
		"python":     {"todo-comment": true},
		"typescript": {"todo-comment": true},
		"tsx":        {"todo-comment": true},
		"javascript": {"todo-comment": true},
		"c":          {"todo-comment": true},
		"cpp":        {"todo-comment": true},
		"csharp":     {"todo-comment": true},
		"rust":       {"todo-comment": true},
	},
	BannedSeverity: map[string]string{
		"go":         "error",
		"java":       "error",
		"python":     "error",
		"typescript": "error",
		"tsx":        "error",
		"javascript": "error",
		"c":          "error",
		"cpp":        "error",
		"csharp":     "error",
		"rust":       "error",
	},
	MetricThresholds: map[string]map[string]MetricConfig{
		"go":         agenticRelaxedMetrics,
		"java":       agenticRelaxedMetrics,
		"python":     agenticRelaxedMetrics,
		"typescript": agenticRelaxedMetrics,
		"tsx":        agenticRelaxedMetrics,
		"javascript": agenticRelaxedMetrics,
		"c":          agenticRelaxedMetrics,
		"cpp":        agenticRelaxedMetrics,
		"csharp":     agenticRelaxedMetrics,
		"rust":       agenticRelaxedMetrics,
	},
}

// PresetByName returns the built-in preset with this name.
func PresetByName(name string) (Preset, bool) {
	p, ok := presets[name]
	return p, ok
}

// PresetNames returns the built-in preset names, sorted.
func PresetNames() []string {
	names := make([]string, 0, len(presets))
	for n := range presets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// PresetList returns every built-in preset, sorted by name, for explain output.
func PresetList() []Preset {
	out := make([]Preset, 0, len(presets))
	for _, n := range PresetNames() {
		out = append(out, presets[n])
	}
	return out
}

// presetTemplate is the lean starter policy `init --preset` writes: only the
// keys a preset cannot supply. Spelling the defaults out here would freeze them
// and make the preset inert, because an explicit value always beats a preset.
const presetTemplate = `# metricgate policy — starting from the %[1]s preset.
#
# A preset is a default layer: it supplies the values this document does not
# set. Add any key below to take it over — an explicit value always beats the
# preset. "metricgate rules presets" lists every value %[1]s moves, and
# "metricgate rules explain list" shows, key by key, where each value came from.
version: 1
preset: %[1]s

gate:
  fail_on: error

exclude: [".git", "vendor", "node_modules", "testdata"]

store:
  enabled: true
  path: ".metricgate/history.db"
`

// PresetYAML returns a starter policy that activates p.
func PresetYAML(p Preset) string {
	return fmt.Sprintf(presetTemplate, p.Name)
}

// applyPresetByName validates the configured preset name and overlays it. An
// unknown name is an error rather than a silent no-op: a typo must not quietly
// leave a repository on the default gate.
func (c *Config) applyPresetByName() error {
	if c.Preset == "" {
		return nil
	}
	p, ok := PresetByName(c.Preset)
	if !ok {
		return fmt.Errorf("unknown preset %q (available: %s)", c.Preset, strings.Join(PresetNames(), ", "))
	}
	c.applyPreset(p)
	return nil
}

// applyPreset overlays p under the explicit YAML values. It is the only entry
// point, so provenance recording cannot be forgotten on one branch.
func (c *Config) applyPreset(p Preset) {
	if p.Name == "" {
		return
	}
	if c.presetKeys == nil {
		c.presetKeys = map[string]bool{}
	}
	c.applyPresetRules(p)
	c.applyPresetEnabled(p)
	c.applyPresetMetrics(p)
	c.applyPresetBanned(p)
}

// applyPresetRules sets rule severities the policy left unset.
func (c *Config) applyPresetRules(p Preset) {
	for lang, rules := range p.RuleSeverity {
		for ruleID, sev := range rules {
			leaf := "languages." + lang + ".rules." + ruleID + ".severity"
			if !c.presetWins(leaf) {
				continue
			}
			c.setRuleSeverity(lang, ruleID, sev)
			c.markPreset(leaf, "languages."+lang+".rules."+ruleID)
		}
	}
}

// setRuleSeverity writes one rule's severity, creating the maps it needs.
func (c *Config) setRuleSeverity(lang, ruleID, sev string) {
	sec := c.Languages[lang]
	if sec.Rules == nil {
		sec.Rules = map[string]RuleConfig{}
	}
	rc := sec.Rules[ruleID]
	rc.Severity = sev
	sec.Rules[ruleID] = rc
	c.Languages[lang] = sec
}

// applyPresetEnabled turns rules on that the policy did not explicitly enable
// or disable. The nil-Enabled semantics of RuleConfig mean an absent entry is
// "use the rule default" — which for todo-comment is off, so a preset that
// only set the severity would be tuning a muted instrument.
func (c *Config) applyPresetEnabled(p Preset) {
	for lang, rules := range p.RuleEnabled {
		for ruleID, on := range rules {
			leaf := "languages." + lang + ".rules." + ruleID + ".enabled"
			if !c.presetWins(leaf) {
				continue
			}
			c.setRuleEnabled(lang, ruleID, on)
			c.markPreset(leaf, "languages."+lang+".rules."+ruleID)
		}
	}
}

// setRuleEnabled writes one rule's enabled flag, creating the maps it needs.
func (c *Config) setRuleEnabled(lang, ruleID string, on bool) {
	sec := c.Languages[lang]
	if sec.Rules == nil {
		sec.Rules = map[string]RuleConfig{}
	}
	rc := sec.Rules[ruleID]
	rc.Enabled = &on
	sec.Rules[ruleID] = rc
	c.Languages[lang] = sec
}

// applyPresetMetrics moves metric thresholds the policy left unset. Warn and
// error are independent: a repository that pinned only warn still receives the
// preset's error ceiling.
func (c *Config) applyPresetMetrics(p Preset) {
	for lang, metrics := range p.MetricThresholds {
		for metricID, mc := range metrics {
			c.applyPresetMetric(lang, metricID, mc)
		}
	}
}

// applyPresetMetric writes one metric's thresholds, per field.
func (c *Config) applyPresetMetric(lang, metricID string, want MetricConfig) {
	if want.Warn == 0 && want.Error == 0 {
		return
	}
	base := "languages." + lang + ".metrics." + metricID
	warnKey, errKey := base+".warn", base+".error"
	sec := c.Languages[lang]
	if sec.Metrics == nil {
		sec.Metrics = map[string]MetricConfig{}
	}
	cur, touched := sec.Metrics[metricID], false
	if want.Warn != 0 && c.presetWins(warnKey) {
		cur.Warn, touched = want.Warn, true
		c.markPreset(warnKey)
	}
	if want.Error != 0 && c.presetWins(errKey) {
		cur.Error, touched = want.Error, true
		c.markPreset(errKey)
	}
	if !touched {
		return
	}
	sec.Metrics[metricID] = cur
	c.Languages[lang] = sec
	c.markPreset(base)
}

// applyPresetBanned sets the severity every banned-api rule falls back to,
// unless the policy pinned one itself.
func (c *Config) applyPresetBanned(p Preset) {
	for lang, sev := range p.BannedSeverity {
		key := "languages." + lang + ".banned-api-severity"
		if !c.presetWins(key) {
			continue
		}
		sec := c.Languages[lang]
		sec.BannedSeverity = sev
		c.Languages[lang] = sec
		c.markPreset(key)
	}
}

// presetWins reports whether a preset value may apply to key: it may only when
// the policy document did not write that key itself.
func (c *Config) presetWins(key string) bool { return !c.Explicit(key) }

// markPreset records that the preset supplied these dotted keys.
func (c *Config) markPreset(keys ...string) {
	for _, k := range keys {
		c.presetKeys[k] = true
	}
}

// PresetNote returns the one-line description of the active preset, or "".
func (c *Config) PresetNote() string {
	if c == nil || c.Preset == "" {
		return ""
	}
	return presets[c.Preset].Note
}
