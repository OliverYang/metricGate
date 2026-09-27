// Package config provides policy loading and configuration utilities,
// including path/file explanation for preview/debug modes.
package config

import (
	"path/filepath"
	"strings"
)

// ExplainDecision describes why a file is included or excluded from scanning.
type ExplainDecision struct {
	Path             string            // relative path to the file
	Status           string            // "included", "excluded", "unsupported"
	Reason           string            // machine-readable reason
	Language         string            // "go", "java", "unknown"
	MatchedPattern   string            // matching exclude pattern (if excluded)
	EffectivePolicy  map[string]string // rule_id -> source ("builtin", "policy_file")
	RuleIDs          []string          // applicable rule IDs
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
			_, _, explicit := c.ValueSource(lang+".rules."+ruleID, "builtin")
			source := "builtin"
			if explicit {
				source = "policy_file"
			}
			decision.EffectivePolicy[ruleID] = source
		}
		for metricID := range sec.Metrics {
			// Metrics are also rules in metricgate
			decision.RuleIDs = append(decision.RuleIDs, "metric:"+metricID)
			_, _, explicit := c.ValueSource(lang+".metrics."+metricID, "builtin")
			source := "builtin"
			if explicit {
				source = "policy_file"
			}
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

// ExplainRule returns the effective configuration for a specific rule.
func (c *Config) ExplainRule(lang, ruleID string) RuleExplain {
	sec, ok := c.Languages[lang]
	if !ok {
		return RuleExplain{
			Enabled:      false,
			Reason:       "language_not_configured",
			Severity:     "",
			Thresholds:   MetricThresholds{},
			PolicySource: "builtin",
		}
	}

	rc, hasRule := sec.Rules[ruleID]
	_, hasMetric := sec.Metrics[ruleID]

	var enabled *bool
	var severity string
	var thresholds MetricThresholds
	var policySource string

	if hasRule {
		if rc.Enabled != nil {
			enabled = rc.Enabled
		} else {
			enabled = nil // use default
		}
		severity = rc.Severity
		source, _, explicit := c.ValueSource(lang+".rules."+ruleID, "builtin")
		policySource = source
		if !explicit {
			policySource = "builtin"
		}
	} else if hasMetric {
		// Metrics are treated as rules with enabled=true by default
		enabled = &[]bool{true}[0]
		source, _, explicit := c.ValueSource(lang+".metrics."+ruleID, "builtin")
		policySource = source
		if !explicit {
			policySource = "builtin"
		}
	} else {
		return RuleExplain{
			Enabled:    false,
			Reason:     "rule_not_found",
			Severity:   "",
			Thresholds: MetricThresholds{},
		}
	}

	warn, errAt, _ := c.MetricThresholds(lang, ruleID)
	thresholds.Warn = warn
	thresholds.Error = errAt

	return RuleExplain{
		Enabled:      enabled != nil && *enabled,
		Reason:       "configured",
		Severity:     severity,
		Thresholds:   thresholds,
		PolicySource: policySource,
	}
}

// RuleExplain contains the effective configuration for a single rule.
type RuleExplain struct {
	Enabled      bool
	Reason       string
	Severity     string
	Thresholds   MetricThresholds
	PolicySource string
}

// MetricThresholds holds the warn/error thresholds for a metric.
type MetricThresholds struct {
	Warn  int
	Error int
}

// ExplainAllPaths returns decisions for all files under root that would be scanned.
// This is used by --preview to show what would be included/excluded without running rules.
func (c *Config) ExplainAllPaths(root string) ([]*ExplainDecision, error) {
	// This will be implemented in engine layer to avoid importing filepath.Walk here
	// For now, return empty slice
	return []*ExplainDecision{}, nil
}
