package engine

import (
	"path/filepath"
	"sort"
	"strings"

	"metricgate/internal/metrics"
	"metricgate/internal/model"
	"metricgate/internal/parser"
	"metricgate/internal/rules"
	"metricgate/internal/selection"
)

// ExplainItem describes one effective rule, metric, or global constraint.
type ExplainItem struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"` // rule | metric | constraint
	Title      string         `json:"title,omitempty"`
	Enabled    bool           `json:"enabled"`
	Severity   model.Severity `json:"severity,omitempty"`
	Warn       int            `json:"warn,omitempty"`
	Error      int            `json:"error,omitempty"`
	Source     string         `json:"source"`
	SourcePath string         `json:"sourcePath,omitempty"`
	Explicit   bool           `json:"explicit"`
	Options    map[string]any `json:"options,omitempty"`
}

// PathExplanation reports selection and effective constraints for one
// source path without reading/parsing the file or running checks.
type PathExplanation struct {
	SchemaVersion int                `json:"schemaVersion"`
	Path          string             `json:"path"`
	Language      string             `json:"language,omitempty"`
	Selection     selection.Decision `json:"selection"`
	Items         []ExplainItem      `json:"items"`
}

// ExplainPath is a read-only view of the same rule assembly and metric
// defaults used by this engine. It deliberately does not read source bytes.
func (e *Engine) ExplainPath(path string) PathExplanation {
	lang, supported := parser.LanguageForFile(filepath.Base(path))
	decision := selection.Decide(path, false, e.cfg.Exclude)
	out := PathExplanation{SchemaVersion: 1, Path: decision.Path, Selection: decision}
	if !supported || decision.Status != selection.Included {
		return out
	}
	out.Language = string(lang)
	for _, r := range e.packsFor(lang) {
		enabled := r.DefaultEnabled
		if v := e.cfg.RuleEnabled(string(lang), r.ID); v != nil {
			enabled = *v
		}
		severity := r.DefaultSeverity
		if s := e.cfg.RuleSeverity(string(lang), r.ID); s != "" {
			severity = model.Severity(s)
		}
		source, sourcePath, explicit := e.cfg.ValueSource("languages."+string(lang)+".rules."+r.ID, "rule_pack")
		if strings.HasPrefix(r.ID, "banned-") {
			source, sourcePath, explicit = e.cfg.ValueSource("languages."+string(lang)+".banned-api", "policy")
		}
		out.Items = append(out.Items, ExplainItem{
			ID: r.ID, Kind: "rule", Title: r.Title, Enabled: enabled,
			Severity: severity, Source: source, SourcePath: sourcePath,
			Explicit: explicit, Options: e.cfg.RuleOptions(string(lang), r.ID),
		})
	}
	for id, def := range metrics.DefaultThresholds {
		warn, errAt := def.Warn, def.Error
		w, er, enabled := e.cfg.MetricThresholds(string(lang), id)
		if w > 0 {
			warn = w
		}
		if er > 0 {
			errAt = er
		}
		isEnabled := enabled == nil || *enabled
		source, sourcePath, explicit := e.cfg.ValueSource("languages."+string(lang)+".metrics."+id, "builtin")
		out.Items = append(out.Items, ExplainItem{
			ID: id, Kind: "metric", Enabled: isEnabled, Warn: warn, Error: errAt,
			Source: source, SourcePath: sourcePath, Explicit: explicit,
		})
	}
	out.Items = append(out.Items, e.globalExplainItems()...)
	sortExplainItems(out.Items)
	return out
}

func (e *Engine) packsFor(lang parser.LanguageID) []*rules.Rule {
	return rules.RulesFor(lang, e.cfg)
}

func (e *Engine) globalExplainItems() []ExplainItem {
	var out []ExplainItem
	if !e.cfg.ArchPolicy().Empty() {
		source, path, explicit := e.cfg.ValueSource("architecture", "builtin")
		out = append(out, ExplainItem{ID: "architecture", Kind: "constraint", Enabled: true, Severity: e.cfg.ArchSeverity("edges"), Source: source, SourcePath: path, Explicit: explicit,
			Options: map[string]any{"layers": len(e.cfg.Architecture.Layers), "forbid": len(e.cfg.Architecture.Forbid)}})
	}
	clone := e.cfg.ClonePolicy()
	source, path, explicit := e.cfg.ValueSource("clones", "builtin")
	out = append(out, ExplainItem{ID: "clones", Kind: "constraint", Enabled: clone.Enabled(), Source: source, SourcePath: path, Explicit: explicit,
		Options: map[string]any{"min_lines": clone.MinLines, "min_tokens": clone.MinTokens}})
	return out
}

func sortExplainItems(items []ExplainItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].ID < items[j].ID
	})
}
