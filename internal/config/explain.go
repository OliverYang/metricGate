// Package config provides policy loading and configuration utilities,
// including path/file explanation for preview/debug modes.
package config

import (
	"path/filepath"
	"strings"
)

// ExplainDecision describes why a file is included or excluded from scanning.
type ExplainDecision struct {
	Path            string            // relative path to the file
	Status          string            // "included", "excluded", "unsupported"
	Reason          string            // machine-readable reason
	Language        string            // "go", "java", "unknown"
	MatchedPattern  string            // matching exclude pattern (if excluded)
	EffectivePolicy map[string]string // rule_id -> source ("builtin", "policy_file")
	RuleIDs         []string          // applicable rule IDs
}

// ExclusionReason identifies why a file was filtered out.
type ExclusionReason int

const (
	ReasonIncluded ExclusionReason = iota
	ReasonExtensionMismatch
	ReasonExcludedPattern
	ReasonUnsupportedLanguage
	ReasonParseError
	ReasonPolicyDisabled
	ReasonAllowedFilesMismatch
)

func (r ExclusionReason) String() string {
	switch r {
	case ReasonIncluded:
		return "included"
	case ReasonExtensionMismatch:
		return "extension_mismatch"
	case ReasonExcludedPattern:
		return "excluded_pattern"
	case ReasonUnsupportedLanguage:
		return "unsupported_language"
	case ReasonParseError:
		return "parse_error"
	case ReasonPolicyDisabled:
		return "policy_disabled"
	case ReasonAllowedFilesMismatch:
		return "allowed_files_mismatch"
	default:
		return "unknown"
	}
}

// ExplainPath returns a decision for a single file path.
// It does not parse the file or run rules; it only evaluates filters and config.
func (c *Config) ExplainPath(relPath string) *ExplainDecision {
	decision := &ExplainDecision{
		Path:            relPath,
		EffectivePolicy: make(map[string]string),
		RuleIDs:         []string{},
	}

	// Check exclude patterns - extract to reduce cyclomatic complexity
	if reason, pattern := checkExcludePattern(c.Exclude, relPath); reason != ReasonIncluded {
		decision.Status = "excluded"
		decision.Reason = reason.String()
		decision.MatchedPattern = pattern
		return decision
	}

	// Determine language support
	lang := detectLanguage(relPath)
	if lang == "" {
		decision.Status = "unsupported"
		decision.Reason = ReasonUnsupportedLanguage.String()
		decision.Language = "unknown"
		return decision
	}
	decision.Language = lang

	// Check if language is enabled (all built-in langs are enabled by default)
	// For now, assume all detected langs are supported
	decision.Status = "included"
	decision.Reason = ReasonIncluded.String()

	// Build effective policy for this language
	sec, ok := c.Languages[lang]
	if ok {
		// Collect rule IDs and their sources
		for ruleID := range sec.Rules {
			decision.RuleIDs = append(decision.RuleIDs, ruleID)
			source, _, _ := c.ValueSource("languages."+lang+".rules."+ruleID, "builtin")
			decision.EffectivePolicy[ruleID] = source
		}
		for metricID := range sec.Metrics {
			// Metrics are also rules in metricgate
			decision.RuleIDs = append(decision.RuleIDs, "metric:"+metricID)
			source, _, _ := c.ValueSource("languages."+lang+".metrics."+metricID, "builtin")
			decision.EffectivePolicy["metric:"+metricID] = source
		}
	}

	// Add banned-api rules
	if len(sec.Banned) > 0 {
		for _, ban := range sec.Banned {
			ruleID := "banned-" + strings.ReplaceAll(ban.Match, ".", "-")
			decision.RuleIDs = append(decision.RuleIDs, ruleID)
			decision.EffectivePolicy[ruleID] = "policy_file"
		}
	}

	return decision
}

// detectLanguage returns the supported language ID for a file path, or "" if unsupported.
func detectLanguage(relPath string) string {
	ext := strings.ToLower(filepath.Ext(relPath))
	switch ext {
	case ".go":
		return "go"
	case ".java":
		return "java"
	default:
		return ""
	}
}

// checkExcludePattern checks if a path matches any exclude pattern.
// Returns the exclusion reason and the matching pattern (if excluded).
func checkExcludePattern(exclude []string, relPath string) (ExclusionReason, string) {
	for _, pattern := range exclude {
		// Strategy 1: Match basename (e.g., "*.go" matches "main.go")
		if matched, err := filepath.Match(pattern, filepath.Base(relPath)); err == nil && matched {
			return ReasonExcludedPattern, pattern
		}

		// Strategy 2: Match full path prefix for non-wildcard patterns
		if !strings.ContainsRune(pattern, '*') && !strings.ContainsRune(pattern, '?') {
			if relPath == pattern || strings.HasPrefix(relPath, pattern+"/") {
				return ReasonExcludedPattern, pattern
			}
		}

		// Strategy 3: Match against full path for wildcard patterns
		if matched, err := filepath.Match(pattern, relPath); err == nil && matched {
			return ReasonExcludedPattern, pattern
		}
	}
	return ReasonIncluded, ""
}

// ExplainRule returns the effective configuration for a specific rule. The
// PolicySource it reports is where the value came from: "policy_file" for a
// key the document wrote, "preset:<name>" for one the active preset supplied,
// or "builtin" for a value nobody overrode. That is what makes a preset's
// deviations auditable instead of silent.
func (c *Config) ExplainRule(lang, ruleID string) RuleExplain {
	sec, ok := c.Languages[lang]
	if !ok {
		return RuleExplain{Reason: "language_not_configured", PolicySource: "builtin"}
	}
	if rc, isRule := sec.Rules[ruleID]; isRule {
		return c.explainRuleEntry(lang, ruleID, rc)
	}
	if _, isMetric := sec.Metrics[ruleID]; isMetric {
		return c.explainMetricEntry(lang, ruleID)
	}
	if strings.HasPrefix(ruleID, "banned-") {
		return c.explainBanEntry(lang)
	}
	return RuleExplain{Reason: "rule_not_found"}
}

// explainRuleEntry reports a rule configured under rules.<id>. A listed rule is
// enabled unless it explicitly says otherwise; the only built-in rule that
// defaults off (todo-comment) is always listed with enabled: false.
func (c *Config) explainRuleEntry(lang, ruleID string, rc RuleConfig) RuleExplain {
	severity, srcKey := rc.Severity, "languages."+lang+".rules."+ruleID
	if severity == "" && strings.HasPrefix(ruleID, "banned-") {
		severity, srcKey = c.BanSeverity(lang), "languages."+lang+".banned-api-severity"
	}
	source, _, _ := c.ValueSource(srcKey, "builtin")
	return RuleExplain{
		Enabled:      rc.Enabled == nil || *rc.Enabled,
		Reason:       "configured",
		Severity:     severity,
		Thresholds:   c.explainThresholds(lang, ruleID),
		PolicySource: source,
	}
}

// explainMetricEntry reports a metric configured under metrics.<id>.
func (c *Config) explainMetricEntry(lang, ruleID string) RuleExplain {
	source, _, _ := c.ValueSource("languages."+lang+".metrics."+ruleID, "builtin")
	return RuleExplain{
		Enabled:      true,
		Reason:       "configured",
		Thresholds:   c.explainThresholds(lang, ruleID),
		PolicySource: source,
	}
}

// explainBanEntry reports a banned-api rule that has no per-rule entry: its
// severity comes from the language's banned-api-severity fallback.
func (c *Config) explainBanEntry(lang string) RuleExplain {
	source, _, _ := c.ValueSource("languages."+lang+".banned-api-severity", "builtin")
	return RuleExplain{
		Enabled:      true,
		Reason:       "configured",
		Severity:     c.BanSeverity(lang),
		PolicySource: source,
	}
}

// MetricThresholdsFor returns a metric's configured warn/error pair.
func (c *Config) MetricThresholdsFor(lang, metricID string) MetricThresholds {
	warn, errAt, _ := c.MetricThresholds(lang, metricID)
	return MetricThresholds{
		Warn:     warn,
		Error:    errAt,
		WarnOff:  c.MetricWarnOff(lang, metricID),
		ErrorOff: c.MetricErrorOff(lang, metricID),
	}
}

// explainThresholds reads the warn/error pair of a metric.
func (c *Config) explainThresholds(lang, metricID string) MetricThresholds {
	return c.MetricThresholdsFor(lang, metricID)
}

// RuleExplain contains the effective configuration for a single rule.
type RuleExplain struct {
	Enabled      bool
	Reason       string
	Severity     string
	Thresholds   MetricThresholds
	PolicySource string
}

// MetricThresholds holds the warn/error thresholds for a metric. A WarnOff
// / ErrorOff flag records an explicit `warn: off` / `error: off` in the
// policy — the corresponding int is 0 but means "disabled", not "default".
type MetricThresholds struct {
	Warn     int
	Error    int
	WarnOff  bool
	ErrorOff bool
}

// ExplainAllPaths returns decisions for all files under root that would be scanned.
// This is used by --preview to show what would be included/excluded without running rules.
func (c *Config) ExplainAllPaths(root string) ([]*ExplainDecision, error) {
	// This will be implemented in engine layer to avoid importing filepath.Walk here
	// For now, return empty slice
	return []*ExplainDecision{}, nil
}
