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

// Order is the pack-ordering contract: items with a smaller Order appear
// earlier in the emitted text. It exists because long-context models attend
// unevenly (the "Lost in the Middle" effect), so the hard constraints and the
// current diff must land near the top rather than wherever the tokenizer
// happens to place them. Priority controls *whether* an optional item is
// admitted; Order controls *where* an admitted item goes.
type PackItem struct {
	ID       string
	Section  string
	Order    int
	Priority int
	Text     string
	Required bool
	Source   string
}

// Order constants fix the pack layout. Lower sorts first; ties fall back to
// Priority then ID so the output stays deterministic.
const (
	OrderHeader      = 0  // the one-line preamble
	OrderConstraints = 1  // hard policy constraints — never below the fold
	OrderDiff        = 2  // the current change set — what the agent is editing
	OrderFacts       = 10 // per-file signatures/imports (grounding)
	OrderReuse       = 20 // clone reuse hints
	OrderSymbols     = 30 // similar-symbol hints
	OrderSource      = 40 // opt-in full source (lowest value, largest)
)

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

	// Emitted order is the contract: (Order, Priority, ID). Required items
	// carry the lowest Orders (header/constraints/diff), so they land at the
	// top of the pack, ahead of any optional facts.
	sortByOrder(items)
	res := PackResult{Budget: opts.Budget, Counter: opts.Counter.ID(), Exact: true}
	var included, optional []PackItem
	for _, item := range items {
		if item.Required {
			included = append(included, item)
		} else {
			optional = append(optional, item)
		}
	}
	// Admission order is priority-first; placement order stays Order-first.
	sortByPriority(optional)

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
		sortByOrder(candidate)
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

// sortByOrder orders items for emission: Order, then Priority, then ID.
func sortByOrder(items []PackItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Order != items[j].Order {
			return items[i].Order < items[j].Order
		}
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		return items[i].ID < items[j].ID
	})
}

// sortByPriority orders optional items for admission: Priority, then Order, then ID.
// metricgate: ignore-begin function-clone
func sortByPriority(items []PackItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		if items[i].Order != items[j].Order {
			return items[i].Order < items[j].Order
		}
		return items[i].ID < items[j].ID
	})
}
// metricgate: ignore-end

func buildPackItems(files []*FileFacts, cfg *config.Config, targets []string) []PackItem {
	items := []PackItem{{ID: "header", Section: "header", Order: OrderHeader, Priority: 0, Required: true, Text: "# metricgate context — deterministic facts & constraints\n# Follow the required constraints; only propose changes grounded in these facts.\n\n"}}
	langs := map[parser.LanguageID]bool{}
	for _, f := range files {
		langs[parser.LanguageID(f.Language)] = true
	}
	items = appendConstraints(items, cfg, langs)
	items = appendFactItems(items, files, targets)
	items = appendReuseAndSymbols(items, files, cfg)
	return items
}

func appendConstraints(items []PackItem, cfg *config.Config, langs map[parser.LanguageID]bool) []PackItem {
	constraintLines := constraints(cfg, langs)
	if len(constraintLines) == 0 {
		return items
	}
	var b strings.Builder
	b.WriteString("## constraints (policy)\n")
	for _, line := range constraintLines {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return append(items, PackItem{ID: "constraints", Section: "constraints", Order: OrderConstraints, Priority: 0, Required: true, Source: "policy", Text: b.String()})
}

func appendFactItems(items []PackItem, files []*FileFacts, targets []string) []PackItem {
	targetSet := map[string]bool{}
	for _, t := range targets {
		targetSet[normalizePackPath(t)] = true
	}
	compact := renderOpts{includePrivate: false, sigMax: 90, maxImports: 8, typesInline: false}
	for _, f := range files {
		items = append(items, PackItem{ID: "file:" + normalizePackPath(f.File), Section: "facts", Order: OrderFacts, Priority: 1, Required: targetSet[normalizePackPath(f.File)], Source: normalizePackPath(f.File), Text: renderFactFile(f, compact)})
	}
	return items
}

func appendReuseAndSymbols(items []PackItem, files []*FileFacts, cfg *config.Config) []PackItem {
	writeItems := func(section string, order, priority int, lines []string) []PackItem {
		for i, line := range lines {
			items = append(items, PackItem{ID: fmt.Sprintf("%s:%04d", section, i), Section: section, Order: order, Priority: priority, Source: section, Text: "## " + section + "\n- " + line + "\n\n"})
		}
		return items
	}
	var cloneFns []clones.Func
	for _, f := range files {
		cloneFns = append(cloneFns, f.CloneFns...)
	}
	items = writeItems("reuse before you generate", OrderReuse, 3, clones.Summary(clones.Find(cloneFns, cfg.ClonePolicy())))
	items = writeItems("existing symbols (align, do not reinvent)", OrderSymbols, 4, SimilarSymbols(files, Index(files), 3))
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
