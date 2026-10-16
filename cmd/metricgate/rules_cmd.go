package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"metricgate/internal/config"
	"metricgate/internal/engine"
	"metricgate/internal/metrics"
	"metricgate/internal/policygap"
	"metricgate/internal/rules"
)

// cmdRules exposes deterministic rule/policy explain operations.
func cmdRules(args []string) {
	dispatch("rules", "explain|presets|gaps", args, map[string]func([]string){
		"explain": cmdRulesExplain,
		"presets": cmdRulesPresets,
		"gaps":    cmdRulesGaps,
	})
}

// cmdRulesExplain reports the effective configuration of a rule or metric.
// `rules explain list` lists everything; `rules explain <id>` (or the older
// `rules explain explain <id>`) reports one.
func cmdRulesExplain(args []string) {
	set := flag.NewFlagSet("rules explain", flag.ExitOnError)
	configPath := set.String("config", "", "policy file")
	format := set.String("format", "text", "text|json")
	lang := set.String("lang", "go", "language id to explain")
	parseArgs(set, args)
	cfg, err := loadPolicyFor(*configPath, ".")
	if err != nil {
		fail(err)
	}
	rest := set.Args()
	if len(rest) == 0 || rest[0] == "list" {
		cmdRulesList(cfg, *format)
		return
	}
	ruleID := rest[0]
	if ruleID == "explain" {
		if len(rest) < 2 {
			fail(fmt.Errorf("rules explain requires a rule ID"))
		}
		ruleID = rest[1]
	}
	explainRule(cfg, *lang, ruleID, *format)
}

// cmdRulesList lists every rule the policy arms, with the source of its value,
// so a preset's contributions are visible at a glance.
//
// It reads the run's own rule catalogue rather than the YAML keys: the YAML
// only holds per-language rules and metrics, so reading it directly omitted
// every whole-graph rule — including the architecture ones, which is where most
// of the surviving value sits. A list that calls itself complete and is not is
// worse than no list.
func cmdRulesList(cfg *config.Config, format string) {
	catalog := engine.Catalog(cfg)
	graph := policygap.GraphRules(cfg, ".")
	if format == "json" {
		writeRulesListJSON(catalog, graph, cfg)
		return
	}
	if cfg.Preset != "" {
		fmt.Printf("preset: %s — %s\n\n", cfg.Preset, cfg.PresetNote())
	}
	byLang := map[string][]string{}
	var order []string
	for _, m := range catalog {
		if m.Language == "" {
			continue // a whole-graph rule: printed under [graph]
		}
		if _, ok := byLang[m.Language]; !ok {
			order = append(order, m.Language)
		}
		byLang[m.Language] = append(byLang[m.Language], m.ID)
	}
	sort.Strings(order)
	for _, lang := range order {
		ids := byLang[lang]
		sort.Strings(ids)
		fmt.Printf("[%s]\n", lang)
		for _, id := range ids {
			fmt.Printf("  - %-34s %s\n", id, ruleValueSource(cfg, lang, id))
		}
	}
	fmt.Println("[graph]")
	for _, g := range graph {
		fmt.Printf("  - %-34s %s\n", g.ID, graphSource(g))
	}
}

// ruleListEntry is one row of `rules explain list`.
type ruleListEntry struct {
	ID       string `json:"id"`
	Language string `json:"language,omitempty"`
	Source   string `json:"source"`
}

func writeRulesListJSON(catalog []rules.Meta, graph []policygap.GraphRule, cfg *config.Config) {
	out := struct {
		Rules []ruleListEntry `json:"rules"`
		Graph []ruleListEntry `json:"graph_rules"`
	}{Rules: []ruleListEntry{}, Graph: []ruleListEntry{}}
	for _, m := range catalog {
		if m.Language == "" {
			continue
		}
		out.Rules = append(out.Rules, ruleListEntry{ID: m.ID, Language: m.Language, Source: ruleValueSource(cfg, m.Language, m.ID)})
	}
	for _, g := range graph {
		out.Graph = append(out.Graph, ruleListEntry{ID: g.ID, Source: graphSource(g)})
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	os.Stdout.Write(append(data, '\n'))
}

// ruleValueSource reports where one rule's effective value came from.
func ruleValueSource(cfg *config.Config, lang, id string) string {
	// A ban exists only because the policy declared it, so its source is the
	// policy even though no per-rule key holds an override.
	if strings.HasPrefix(id, "banned-") {
		return cfg.SourceKind + " (banned-api)"
	}
	key := "languages." + lang + ".rules." + id
	for _, m := range metrics.IDsFor(lang) {
		if m == id {
			key = "languages." + lang + ".metrics." + id
			break
		}
	}
	return valueSource(cfg, key)
}

// graphSource says what makes a whole-graph rule able to fire, so a rule the
// policy never armed is distinguishable from one it did.
func graphSource(g policygap.GraphRule) string {
	switch {
	case g.Armed:
		return "armed by " + g.ArmedBy
	case !g.InCheck:
		return "needs a flag: " + g.ArmedBy
	default:
		return "not armed — declare " + g.ArmedBy
	}
}

// cmdRulesPresets prints the built-in presets and every value each one moves —
// the "what would `preset: X` actually change?" answer.
func cmdRulesPresets(args []string) {
	set := flag.NewFlagSet("rules presets", flag.ExitOnError)
	format := set.String("format", "text", "text|json")
	parseArgs(set, args)
	if *format == "json" {
		data, _ := json.MarshalIndent(config.PresetList(), "", "  ")
		os.Stdout.Write(append(data, '\n'))
		return
	}
	for _, p := range config.PresetList() {
		printPreset(p)
	}
}

func printPreset(p config.Preset) {
	fmt.Printf("%s — %s\n", p.Name, p.Note)
	fmt.Printf("  use: `preset: %s` in .metricgate.yaml, or `metricgate init --preset %s`\n", p.Name, p.Name)
	for _, lang := range sortedKeys(p.RuleSeverity) {
		for _, ruleID := range sortedKeys(p.RuleSeverity[lang]) {
			fmt.Printf("  [%s] rule %s severity -> %s\n", lang, ruleID, p.RuleSeverity[lang][ruleID])
		}
	}
	for _, lang := range sortedKeys(p.RuleEnabled) {
		for _, ruleID := range sortedKeys(p.RuleEnabled[lang]) {
			fmt.Printf("  [%s] rule %s enabled -> %v\n", lang, ruleID, p.RuleEnabled[lang][ruleID])
		}
	}
	for _, lang := range sortedKeys(p.BannedSeverity) {
		fmt.Printf("  [%s] banned-api severity -> %s\n", lang, p.BannedSeverity[lang])
	}
	for _, lang := range sortedKeys(p.MetricThresholds) {
		for _, metricID := range sortedKeys(p.MetricThresholds[lang]) {
			fmt.Printf("  [%s] metric %s -> %s\n", lang, metricID, formatMetricThreshold(p.MetricThresholds[lang][metricID]))
		}
	}
	fmt.Println()
}

// formatMetricThreshold renders one configured threshold pair, honoring an
// explicit `warn: off` / `error: off`.
func formatMetricThreshold(mc config.MetricConfig) string {
	w, e := "off", "off"
	if !mc.WarnOff {
		w = strconv.Itoa(mc.Warn)
	}
	if !mc.ErrorOff {
		e = strconv.Itoa(mc.Error)
	}
	return fmt.Sprintf("warn=%s, error=%s", w, e)
}

// formatLevel renders one threshold level for the explain output; off=true
// means the policy disabled this level entirely (`warn: off` / `error: off`).
func formatLevel(value int, off bool) string {
	if off {
		return "off"
	}
	return strconv.Itoa(value)
}

func explainRule(cfg *config.Config, lang, ruleID, format string) {
	explain := cfg.ExplainRule(lang, ruleID)

	if format == "json" {
		data, _ := json.MarshalIndent(explain, "", "  ")
		os.Stdout.Write(append(data, '\n'))
		return
	}

	fmt.Printf("Rule: %s (language: %s)\n", ruleID, lang)
	fmt.Printf("  Enabled:     %v\n", explain.Enabled)
	fmt.Printf("  Reason:      %s\n", explain.Reason)
	fmt.Printf("  Severity:    %s\n", explain.Severity)
	fmt.Printf("  Source:      %s\n", explain.PolicySource)
	if explain.Note != "" {
		fmt.Printf("  Note:        %s\n", explain.Note)
	}
	switch {
	case explain.Thresholds.WarnOff || explain.Thresholds.ErrorOff:
		fmt.Printf("  Thresholds:  warn=%s, error=%s\n",
			formatLevel(explain.Thresholds.Warn, explain.Thresholds.WarnOff),
			formatLevel(explain.Thresholds.Error, explain.Thresholds.ErrorOff))
	default:
		fmt.Printf("  Thresholds:  warn=%d, error=%d\n", explain.Thresholds.Warn, explain.Thresholds.Error)
	}
}

// valueSource reports where a dotted policy key's value came from.
func valueSource(cfg *config.Config, key string) string {
	src, _, _ := cfg.ValueSource(key, "builtin")
	return src
}

// sortedKeys returns a map's keys in sorted order, so explain output is stable
// across runs (Go map iteration is not).
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
