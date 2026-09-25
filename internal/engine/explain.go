// Package engine provides the core checking logic. This file implements
// ExplainPath for preview/debug modes without running actual rules.
package engine

import (
	"io/fs"
	"os"
	"path/filepath"

	"metricgate/internal/config"
	"metricgate/internal/parser"
)

// ExplainDecision wraps config.ExplainDecision with engine-specific context.
type ExplainDecision struct {
	Path         string            // relative path
	Status       string            // "included", "excluded", "unsupported"
	Reason       string            // machine-readable reason
	Language     string            // "go", "java", "unknown"
	MatchedPattern string          // matching exclude pattern
	PolicySource map[string]string // rule_id -> source
	RuleIDs      []string          // applicable rules
	Err          error             // parse error if any
}

// ExplainPath returns why a single file is included or excluded.
// It does not parse the file or run rules; only evaluates filters and config.
func (e *Engine) ExplainPath(relPath string) *ExplainDecision {
	decision := e.cfg.ExplainPath(relPath)

	out := &ExplainDecision{
		Path:         decision.Path,
		Status:       decision.Status,
		Reason:       decision.Reason,
		Language:     decision.Language,
		MatchedPattern: decision.MatchedPattern,
		PolicySource: decision.EffectivePolicy,
		RuleIDs:      decision.RuleIDs,
	}

	// Try to parse the file to check for parse errors
	if decision.Status == "included" {
		langID, ok := parser.LanguageForFile(filepath.Base(relPath))
		if !ok {
			out.Status = "unsupported"
			out.Reason = "unsupported_language"
			return out
		}

		_, hasParser := e.langs[langID]
		if !hasParser {
			out.Status = "unsupported"
			out.Reason = "parser_not_available"
			return out
		}

		// Try opening the file to detect read errors
		fullPath := filepath.Join(".", relPath)
		f, err := os.Open(fullPath)
		if err != nil {
			out.Err = err
			out.Status = "error"
			out.Reason = "read_error"
			return out
		}
		f.Close()
	}

	return out
}

// ExplainAllPaths returns decisions for all files under root that would be scanned.
// This is used by --preview to show what would be included/excluded.
func (e *Engine) ExplainAllPaths(root string) ([]*ExplainDecision, error) {
	var decisions []*ExplainDecision

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		decision := e.ExplainPath(relPath)
		decisions = append(decisions, decision)
		return nil
	})

	if err != nil {
		return nil, err
	}

	return decisions, nil
}

// ExplainRule returns the effective configuration for a specific rule.
func (e *Engine) ExplainRule(lang, ruleID string) config.RuleExplain {
	return e.cfg.ExplainRule(lang, ruleID)
}
