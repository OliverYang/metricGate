package facts

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"metricgate/internal/clones"
	"metricgate/internal/config"
	"metricgate/internal/parser"
)

var ErrBudgetExhausted = errors.New("budget_exhausted")

// TokenCounter counts a complete text. Exact=false explicitly marks a token
// estimate; callers must never present an estimate as a model-token count.
type TokenCounter interface {
	Count(text string) (n int, exact bool, err error)
	ID() string
}

type CharCounter struct{}

func (CharCounter) Count(text string) (int, bool, error) { return len(text), true, nil }
func (CharCounter) ID() string                           { return "utf8-bytes-v1" }

type CharEstimate struct{}

func (CharEstimate) Count(text string) (int, bool, error) { return (len(text) + 3) / 4, false, nil }
func (CharEstimate) ID() string                           { return "bytes-div-4-estimate-v1" }

type BudgetOptions struct {
	Budget      int
	Counter     TokenCounter
	TargetFiles []string // optional: preserve these file fact blocks as required
}

type PackRef struct {
	ID      string `json:"id"`
	Section string `json:"section"`
	Source  string `json:"source,omitempty"`
}
type Omission struct {
	ID       string `json:"id"`
	Section  string `json:"section"`
	Source   string `json:"source,omitempty"`
	Reason   string `json:"reason"`
	Required bool   `json:"required,omitempty"`
}
type PackResult struct {
	Text            string     `json:"text,omitempty"`
	Budget          int        `json:"budget"`
	Used            int        `json:"used"`
	Counter         string     `json:"counter"`
	Exact           bool       `json:"exact"`
	Included        []PackRef  `json:"included"`
	Omitted         []Omission `json:"omitted"`
	BudgetExhausted bool       `json:"budget_exhausted,omitempty"`
}

type PackItem struct {
	ID       string
	Section  string
	Priority int
	Text     string
	Required bool
	Source   string
}

// RenderBudgeted builds facts/policy items and packs them under a complete-text budget.
func RenderBudgeted(files []*FileFacts, cfg *config.Config, opts BudgetOptions) (PackResult, error) {
	return PackItemsBudgeted(BuildPackItems(files, cfg, opts.TargetFiles), opts)
}

// BuildPackItems exposes deterministic facts/policy items so adapters can add
// their own complete diff items before using the shared budget algorithm.
func BuildPackItems(files []*FileFacts, cfg *config.Config, targets []string) []PackItem {
	return buildPackItems(files, cfg, targets)
}

// PackItemsBudgeted admits complete items by required flag, priority, then ID.
// Each candidate is counted as the entire concatenated pack, avoiding false
// exactness from summing independently tokenized fragments.
func PackItemsBudgeted(items []PackItem, opts BudgetOptions) (PackResult, error) {
	if opts.Budget <= 0 || opts.Counter == nil {
		return PackResult{}, fmt.Errorf("a positive budget and explicit counter are required")
	}
	items = append([]PackItem(nil), items...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Required != items[j].Required {
			return items[i].Required
		}
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		return items[i].ID < items[j].ID
	})
	res := PackResult{Budget: opts.Budget, Counter: opts.Counter.ID(), Exact: true}
	var included, optional []PackItem
	for _, item := range items {
		if item.Required {
			included = append(included, item)
		} else {
			optional = append(optional, item)
		}
	}
	text := joinItems(included)
	used, exact, err := opts.Counter.Count(text)
	if err != nil {
		return PackResult{}, fmt.Errorf("count required context: %w", err)
	}
	res.Exact = exact
	if used > opts.Budget {
		res.BudgetExhausted = true
		res.Omitted = refsAsOmissions(included, "required_budget_exceeded", true)
		return res, ErrBudgetExhausted
	}
	for _, item := range optional {
		candidate := append(append([]PackItem(nil), included...), item)
		candidateText := joinItems(candidate)
		n, itemExact, countErr := opts.Counter.Count(candidateText)
		if countErr != nil {
			return PackResult{}, fmt.Errorf("count context item %s: %w", item.ID, countErr)
		}
		res.Exact = res.Exact && itemExact
		if n <= opts.Budget {
			included = candidate
			text, used = candidateText, n
		} else {
			res.Omitted = append(res.Omitted, Omission{ID: item.ID, Section: item.Section, Source: item.Source, Reason: "budget"})
		}
	}
	res.Text, res.Used = text, used
	for _, item := range included {
		res.Included = append(res.Included, PackRef{ID: item.ID, Section: item.Section, Source: item.Source})
	}
	return res, nil
}

func buildPackItems(files []*FileFacts, cfg *config.Config, targets []string) []PackItem {
	items := []PackItem{{ID: "header", Section: "header", Priority: 0, Required: true, Text: "# metricgate context — deterministic facts & constraints\n# Follow the required constraints; only propose changes grounded in these facts.\n\n"}}
	langs := map[parser.LanguageID]bool{}
	for _, f := range files {
		langs[parser.LanguageID(f.Language)] = true
	}
	constraintLines := constraints(cfg, langs)
	if len(constraintLines) > 0 {
		var b strings.Builder
		b.WriteString("## constraints (policy)\n")
		for _, line := range constraintLines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
		items = append(items, PackItem{ID: "constraints", Section: "constraints", Priority: 0, Required: true, Source: "policy", Text: b.String()})
	}
	targetSet := map[string]bool{}
	for _, t := range targets {
		targetSet[normalizePackPath(t)] = true
	}
	compact := renderOpts{includePrivate: false, sigMax: 90, maxImports: 8, typesInline: false}
	for _, f := range files {
		items = append(items, PackItem{ID: "file:" + normalizePackPath(f.File), Section: "facts", Priority: 1, Required: targetSet[normalizePackPath(f.File)], Source: normalizePackPath(f.File), Text: renderFactFile(f, compact)})
	}
	writeItems := func(section string, priority int, lines []string) {
		for i, line := range lines {
			items = append(items, PackItem{ID: fmt.Sprintf("%s:%04d", section, i), Section: section, Priority: priority, Source: section, Text: "## " + section + "\n- " + line + "\n\n"})
		}
	}
	var cloneFns []clones.Func
	for _, f := range files {
		cloneFns = append(cloneFns, f.CloneFns...)
	}
	writeItems("reuse before you generate", 3, clones.Summary(clones.Find(cloneFns, cfg.ClonePolicy())))
	writeItems("existing symbols (align, do not reinvent)", 4, SimilarSymbols(files, Index(files), 3))
	return items
}

func renderFactFile(f *FileFacts, o renderOpts) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s (%s)\n", normalizePackPath(f.File), f.Language)
	if f.Package != "" {
		fmt.Fprintf(&b, "package: %s\n", f.Package)
	}
	if o.maxImports > 0 && len(f.Imports) > 0 {
		imports := f.Imports
		if len(imports) > o.maxImports {
			imports = append(append([]string(nil), imports[:o.maxImports]...), fmt.Sprintf("… +%d more", len(imports)-o.maxImports))
		}
		fmt.Fprintf(&b, "imports: [%s]\n", strings.Join(imports, ", "))
	}
	for _, sym := range f.Symbols {
		if line := symbolLine(sym, o); line != "" {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')
	return b.String()
}

func joinItems(items []PackItem) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteString(item.Text)
	}
	return b.String()
}

func refsAsOmissions(items []PackItem, reason string, required bool) []Omission {
	out := make([]Omission, 0, len(items))
	for _, item := range items {
		out = append(out, Omission{ID: item.ID, Section: item.Section, Source: item.Source, Reason: reason, Required: required})
	}
	return out
}

func normalizePackPath(path string) string {
	path = strings.ReplaceAll(path, "\\\\", "/")
	path = strings.TrimPrefix(path, "./")
	return path
}
