// Rendering of the constraint context pack: a dense, token-budgeted block
// combining structural facts (signatures, imports) with the policy's active
// constraints. Degrades gracefully — private symbols, imports and type
// details are dropped before anything is truncated.
package facts

import (
	"fmt"
	"sort"
	"strings"

	"metricgate/internal/arch"
	"metricgate/internal/clones"
	"metricgate/internal/config"
	"metricgate/internal/metrics"
	"metricgate/internal/model"
	"metricgate/internal/parser"
	"metricgate/internal/rules"
)

// renderOpts controls one degradation level.
type renderOpts struct {
	includePrivate bool
	sigMax         int
	maxImports     int
	typesInline    bool
}

// levels is the degradation ladder, full detail first.
var levels = []renderOpts{
	{true, 200, 20, true},
	{false, 120, 12, true},
	{false, 90, 8, false},
	{false, 90, 0, false},
}

// Render builds the constraint context pack. len(pack) never exceeds
// maxChars (level degradation + hard tail cut).
func Render(files []*FileFacts, cfg *config.Config, maxChars int) string {
	for _, o := range levels {
		if pack := render(files, cfg, o); len(pack) <= maxChars {
			return pack
		}
	}
	return cutTail(render(files, cfg, levels[len(levels)-1]), maxChars)
}

func render(files []*FileFacts, cfg *config.Config, o renderOpts) string {
	langs := map[parser.LanguageID]bool{}
	for _, f := range files {
		langs[parser.LanguageID(f.Language)] = true
	}

	var b strings.Builder
	b.WriteString("# metricgate context — facts & constraints for the code below\n")
	b.WriteString("# Align new code with these symbols exactly; obey every rule/limit.\n\n")

	for _, f := range files {
		fmt.Fprintf(&b, "## %s (%s)\n", f.File, f.Language)
		if f.Package != "" {
			fmt.Fprintf(&b, "package: %s\n", f.Package)
		}
		if o.maxImports > 0 && len(f.Imports) > 0 {
			imports := f.Imports
			if len(imports) > o.maxImports {
				imports = append(append([]string{}, imports[:o.maxImports]...),
					fmt.Sprintf("… +%d more", len(f.Imports)-o.maxImports))
			}
			fmt.Fprintf(&b, "imports: [%s]\n", strings.Join(imports, ", "))
		}
		for _, s := range f.Symbols {
			if line := symbolLine(s, o); line != "" {
				b.WriteString(line)
				b.WriteByte('\n')
			}
		}
		b.WriteByte('\n')
	}

	// The "reuse before you generate" sections, then the policy.
	writeCloneHints(&b, files, cfg)
	writeSymbolHints(&b, files)

	if lines := constraints(cfg, langs); len(lines) > 0 {
		b.WriteString("## constraints (policy)\n")
		for _, l := range lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// symbolLine renders one symbol (and its members, when inlining types) or
// "" when the symbol is filtered out at this degradation level.
func symbolLine(s Symbol, o renderOpts) string {
	if !o.includePrivate && s.Visibility != "exported" {
		return ""
	}
	sig := oneline(s.Signature, o.sigMax)
	if sig == "" {
		return ""
	}
	line := "- " + sig
	if o.typesInline && len(s.Children) > 0 {
		parts := make([]string, 0, len(s.Children))
		for _, c := range s.Children {
			parts = append(parts, oneline(c.Signature, 60))
		}
		line += " { " + strings.Join(parts, "; ") + " }"
	}
	return oneline(line, o.sigMax+220)
}

// constraints renders the active rules and metric limits for the languages
// present in the fact set. Duplicate lines across languages are collapsed.
func constraints(cfg *config.Config, langs map[parser.LanguageID]bool) []string {
	var out []string
	seen := map[string]bool{}
	add := func(lines []string) {
		for _, l := range lines {
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
	}

	for _, lang := range []parser.LanguageID{parser.LangGo, parser.LangJava} {
		if langs[lang] {
			add(langConstraints(cfg, lang))
		}
	}
	// Architecture constraints are global (a dependency direction is never
	// single-language), so they are added once, however many languages ran.
	add(bullets(arch.Summary(cfg.ArchPolicy())))

	sort.Strings(out)
	return out
}

// bullets turns bare policy lines into context-pack bullets.
func bullets(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, "- "+l)
	}
	return out
}

// langConstraints renders the rule and limit lines of one language.
func langConstraints(cfg *config.Config, lang parser.LanguageID) []string {
	var lines []string
	for _, r := range rules.RulesFor(lang, cfg) {
		if l := ruleLine(cfg, lang, r); l != "" {
			lines = append(lines, l)
		}
	}
	ids := metricIDs
	if lang == parser.LangJava {
		ids = javaMetricIDs
	}
	for _, id := range ids {
		if l := limitLine(cfg, lang, id); l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// ruleLine renders one rule constraint, or "" when the rule is disabled.
func ruleLine(cfg *config.Config, lang parser.LanguageID, r *rules.Rule) string {
	enabled := r.DefaultEnabled
	if b := cfg.RuleEnabled(string(lang), r.ID); b != nil {
		enabled = *b
	}
	if !enabled {
		return ""
	}
	sev := r.DefaultSeverity
	if s := cfg.RuleSeverity(string(lang), r.ID); s != "" {
		sev = model.Severity(s)
	}
	return fmt.Sprintf("- rule %s (%s): %s", r.ID, sev, r.Title)
}

// limitLine renders one metric limit, or "" when the metric is disabled.
// Policy thresholds apply whenever set, enabled flag or not.
func limitLine(cfg *config.Config, lang parser.LanguageID, id string) string {
	def := metrics.DefaultThresholds[id]
	warn, errAt := def.Warn, def.Error
	w, e, en := cfg.MetricThresholds(string(lang), id)
	if en != nil && !*en {
		return ""
	}
	if w > 0 {
		warn = w
	}
	if e > 0 {
		errAt = e
	}
	return fmt.Sprintf("- limit %s: warn >%d / error >%d", id, warn, errAt)
}

// metricIDs is the stable order for the limits section.
var metricIDs = []string{
	metrics.MetricCyclomatic,
	metrics.MetricCognitive,
	metrics.MetricFuncLength,
	metrics.MetricFileLength,
	metrics.MetricNesting,
	metrics.MetricParams,
}

// javaMetricIDs extends metricIDs with the CK class metrics, which only
// exist for Java.
var javaMetricIDs = append(append([]string{}, metricIDs...),
	metrics.MetricWMC, metrics.MetricNOM, metrics.MetricLCOM,
	metrics.MetricCBO, metrics.MetricDIT)

const tail = "\n… (truncated — raise -max-chars or narrow the path)"

// cutTail hard-truncates the pack to maxChars on a line boundary, reserving
// room for the truncation notice so the result never exceeds the budget.
func cutTail(s string, maxChars int) string {
	limit := maxChars - len(tail)
	if limit <= 0 {
		if len(s) > maxChars {
			return s[:maxChars]
		}
		return s
	}
	if len(s) <= limit {
		return s
	}
	cut := s[:limit]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i]
	}
	return cut + tail
}

// writeCloneHints renders the clone classes as the deterministic
// "reuse before you generate" rule: never reimplement what already exists.
func writeCloneHints(b *strings.Builder, files []*FileFacts, cfg *config.Config) {
	var cloneFns []clones.Func
	for _, f := range files {
		cloneFns = append(cloneFns, f.CloneFns...)
	}
	groups := clones.Find(cloneFns, cfg.ClonePolicy())
	if len(groups) == 0 {
		return
	}
	b.WriteString("## reuse before you generate\n")
	for _, l := range clones.Summary(groups) {
		b.WriteString("- ")
		b.WriteString(l)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}

// writeSymbolHints renders the existing symbols a file may have duplicated
// by a near-miss name, so generated code aligns with the live API surface.
func writeSymbolHints(b *strings.Builder, files []*FileFacts) {
	hints := SimilarSymbols(files, Index(files), 3)
	if len(hints) == 0 {
		return
	}
	b.WriteString("## existing symbols (align, do not reinvent)\n")
	for _, l := range hints {
		b.WriteString("- ")
		b.WriteString(l)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
}
