package cockpit

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"metricgate/internal/config"
	"metricgate/internal/parser"
	"metricgate/internal/rules"
	"metricgate/internal/store"
)

// rulesView is the rule catalogue answer: every built-in rule of every
// registered language, each merged with its effective configuration.
//
// It is the inventory of what the tool can detect, crossed with what the policy
// in force actually runs. The page used to answer "which keys exist", which is a
// question about a document; the version after that answered "which rules run",
// but took its language list from the policy — so ten of the thirteen languages
// had no rows at all, and a reader could not tell "this language has no rules"
// from "this policy never mentioned it". This one answers the question the
// operator actually has: what can fire, what is firing, and why not the rest.
type rulesView struct {
	// GeneratedAt follows the dashboard's clock convention so a client reading
	// both payloads compares like with like.
	GeneratedAt string `json:"generatedAt"`
	Scope       string `json:"scope"`
	PolicyPath  string `json:"policyPath"`
	// Languages is the catalogue's language list: every registered language the
	// tool has a rule pack for, which is what the page's subtitle counts. It is
	// deliberately not "the languages the policy configures" — that number (3
	// here) is what made the page read as "the tool only knows three languages"
	// while thirteen packs were compiled into the binary. Which of these the
	// policy actually configures is per-language, in LanguageGroups[].Configured.
	Languages      []string        `json:"languages"`
	LanguageGroups []languageGroup `json:"languageGroups"`
	Rules          []ruleViewEntry `json:"rules"`
}

// languageGroup is one language's slice of the catalogue, resolved far enough
// that the page can build its filter menu without walking the rule rows.
type languageGroup struct {
	Language string `json:"language"`
	// Configured is whether the policy in force names this language. False is
	// not an error: the rules are still built in and still listed.
	Configured bool `json:"configured"`
	// Discovered is whether the most recent run in scope parsed a file of this
	// language. It is the difference between "you could run this" and "you do
	// run this", and it is the only column here that needs the history store.
	Discovered   bool `json:"discovered"`
	RuleCount    int  `json:"ruleCount"`
	EnabledCount int  `json:"enabledCount"`
}

// ruleViewEntry is one rule in the catalogue. ID, Title and the pack defaults
// come from the rule pack (the static inventory of what the tool can detect);
// enabled, severity, warn, error, reason and policySource come from ExplainRule
// (what actually applies). Merging them means there is no third copy of rule
// metadata to drift.
//
// The Chinese title and description are the exception to that rule and they are
// deliberately kept out of internal/rules: the rule pack is also consumed by
// the CLI, the SARIF report and the MCP tools, and none of those should receive
// UI prose. They are merged here, in the layer that exists to serve the page.
type ruleViewEntry struct {
	Language string `json:"language"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	// TitleZh and DescriptionZh are the page's own copy. An id the tables do
	// not cover falls back to the English title and an empty description rather
	// than a machine translation: inventing a sentence nobody wrote would be a
	// claim about the engine that no one can check.
	TitleZh       string `json:"titleZh"`
	DescriptionZh string `json:"descriptionZh"`
	Enabled       bool   `json:"enabled"`
	// DefaultEnabled is the pack's own default, which is what the engine runs
	// when the policy is silent. Shown beside Enabled because the two together
	// are the only way to tell "the policy turned this off" from "this was
	// never on".
	DefaultEnabled bool   `json:"defaultEnabled"`
	Severity       string `json:"severity"`
	Warn           int    `json:"warn"`
	Error          int    `json:"error"`
	Reason         string `json:"reason"`
	PolicySource   string `json:"policySource"`
	Note           string `json:"note"`
	Origin         string `json:"origin"`
	// Query is the tree-sitter pattern the rule matches. Every built-in rule in
	// the pack is query-backed today (checked 2026-10-16: 61 of 61), so an empty
	// value here means something unusual rather than the normal case — the page
	// renders it as "引擎内判定" instead of a blank cell.
	Query string `json:"query"`
	// AnchorCapture names the capture whose node span becomes the finding's
	// evidence. Empty means the rule has no single AST span and records a
	// line-only anchor.
	AnchorCapture string `json:"anchorCapture"`
	// AlternateQuery is the second pattern for a ban a language can spell two
	// ways (a C++ call is either "foo(...)" or "std::foo(...)"). No built-in
	// rule has one today; the field is carried because the pack can grow one and
	// because a page that silently drops a rule's second shape would under-report
	// what the tool detects.
	AlternateQuery string `json:"alternateQuery"`
}

// rulesAPI serves GET /api/rules: the effective rule catalogue for one scope.
// The scope decides whose policy is read, exactly as on the policy page, so
// switching projects switches the thresholds without the client knowing how
// discovery works.
func rulesAPI(logPath, dbPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		window, err := policyScopeWindow(r, logPath, dbPath)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		view, err := rulesViewOf(window, dbPath)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(view)
	}
}

// rulesViewOf assembles the catalogue for one resolved scope. The policy path is
// reported so the page can say which file the numbers came from — the same
// provenance the policy page itself shows.
func rulesViewOf(window store.Window, dbPath string) (rulesView, error) {
	path := scopePolicyPath(window)
	cfg, err := loadPolicyQuiet(path)
	if err != nil {
		return rulesView{}, err
	}
	view := rulesView{
		GeneratedAt:    nowUTC(),
		PolicyPath:     path,
		Languages:      []string{},
		LanguageGroups: []languageGroup{},
		Rules:          []ruleViewEntry{},
	}
	view.Scope = scopeOrigin(window)
	configured := configuredLanguages(cfg)
	seen := map[string]bool{}
	for _, lang := range configured {
		seen[lang] = true
	}
	catalogue := catalogueLanguages(configured)
	// Languages mirrors the groups so a client can count languages without
	// walking the rows, and so "13 languages" is the number the page shows.
	view.Languages = catalogue
	for _, lang := range catalogue {
		entries := ruleEntriesFor(cfg, lang)
		group := languageGroup{
			Language:   lang,
			Configured: seen[lang],
			RuleCount:  len(entries),
		}
		for _, entry := range entries {
			if entry.Enabled {
				group.EnabledCount++
			}
			view.Rules = append(view.Rules, entry)
		}
		view.LanguageGroups = append(view.LanguageGroups, group)
	}
	markDiscoveredLanguages(view.LanguageGroups, window, dbPath)
	return view, nil
}

// configuredLanguages lists the languages the loaded policy covers, sorted.
//
// A policy may name a language the tool has no rule pack for (a typo, or one
// only the metrics engine knows about); those are skipped rather than reported
// as an empty language, because an empty language section reads as a defect in
// the page instead of "this language has no built-in rules".
func configuredLanguages(cfg *config.Config) []string {
	out := make([]string, 0, len(cfg.Languages))
	for lang := range cfg.Languages {
		if _, ok := languageIDOf(lang); !ok {
			continue
		}
		out = append(out, lang)
	}
	sort.Strings(out)
	return out
}

// catalogueLanguages is the language list the catalogue iterates: every
// registered language, in the parser's stable order, plus any policy spelling
// the parser knows under a different key.
//
// The union is what turns this endpoint from a view of the policy into a view
// of the tool. A language the policy never mentions still has rules — they are
// compiled into the binary — and the page has to be able to say so, or "the
// rules page lists three languages" reads as "the tool only knows three".
func catalogueLanguages(configured []string) []string {
	out := make([]string, 0, len(configured)+len(parser.Supported()))
	seen := map[string]bool{}
	add := func(lang string) {
		if seen[lang] {
			return
		}
		seen[lang] = true
		out = append(out, lang)
	}
	for _, id := range parser.Supported() {
		add(string(id))
	}
	for _, lang := range configured {
		add(lang)
	}
	return out
}

// markDiscoveredLanguages fills in the one group field that needs the history
// store: whether the newest run in scope parsed a file of that language.
//
// The lookup is deliberately the same call the gate's per-language table makes
// (Store.RunMeasuresByLang), scoped to a single run id. Reading the store is a
// real cost — the comment this replaced refused to read it at all — so the
// scope is one run, not a GROUP BY over the findings table, and a store that
// cannot be opened leaves every flag false. False there means "not observed",
// which is why the page must not render the column at all when no run is in
// scope: "I have no data" and "no" are different answers.
func markDiscoveredLanguages(groups []languageGroup, window store.Window, dbPath string) {
	langs := discoveredLanguages(window, dbPath)
	for i := range groups {
		groups[i].Discovered = langs[groups[i].Language]
	}
}

// discoveredLanguages returns the languages seen in the newest run the window
// admits, keyed by language id. A window with no store, no run, or an
// unreadable store yields an empty map rather than an error: this is an
// annotation on the catalogue, and failing the whole page for it would trade a
// missing column for a missing page.
func discoveredLanguages(window store.Window, dbPath string) map[string]bool {
	out := map[string]bool{}
	if strings.TrimSpace(dbPath) == "" {
		return out
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return out
	}
	defer func() { _ = st.Close() }()
	runWindow := scopeRunWindow(window)
	runs, err := st.LatestRunsIn(runWindow)
	if err != nil || len(runs) == 0 {
		return out
	}
	byLang, err := st.RunMeasuresByLang(runs[0].ID)
	if err != nil {
		return out
	}
	for lang := range byLang {
		out[lang] = true
	}
	return out
}

// ruleEntriesFor is the built-in catalogue of one language with each rule's
// effective values folded in.
//
// A language the policy never configured still produces a full, honest answer:
// ExplainRule reports Reason "language_not_configured", and every rule under it
// falls back to the pack's own DefaultEnabled. That fallback is the same one
// the engine applies (internal/rules/rules.go), so the page cannot say a rule
// is off while the checker is emitting findings from it.
func ruleEntriesFor(cfg *config.Config, lang string) []ruleViewEntry {
	pack := rules.ForLanguage(parser.LanguageID(lang))
	out := make([]ruleViewEntry, 0, len(pack))
	for _, rule := range pack {
		if rule == nil {
			continue
		}
		out = append(out, ruleEntryFor(cfg, lang, rule))
	}
	return out
}

// ruleEntryFor merges one rule's pack metadata with its effective configuration.
func ruleEntryFor(cfg *config.Config, lang string, rule *rules.Rule) ruleViewEntry {
	explain := cfg.ExplainRule(lang, rule.ID)
	enabled, reason := effectiveEnabled(explain, rule)
	titleZh, descriptionZh := ruleChineseCopy(rule.ID)
	if titleZh == "" {
		titleZh = rule.Title
	}
	return ruleViewEntry{
		Language:       lang,
		ID:             rule.ID,
		Title:          rule.Title,
		TitleZh:        titleZh,
		DescriptionZh:  descriptionZh,
		Enabled:        enabled,
		DefaultEnabled: rule.DefaultEnabled,
		Severity:       effectiveSeverity(explain, rule),
		Warn:           explain.Thresholds.Warn,
		Error:          explain.Thresholds.Error,
		Reason:         reason,
		PolicySource:   explain.PolicySource,
		Note:           explain.Note,
		Origin:         ruleOrigin(explain),
		Query:          rule.Query,
		AnchorCapture:  rule.AnchorCapture,
		AlternateQuery: alternateQueryOf(rule),
	}
}

// effectiveEnabled merges the policy's answer with the pack's default, which is
// the same fallback the engine applies (internal/rules/rules.go:162
// Runner.Enabled: a missing key means def, not false).
//
// A rule the policy never mentions is not an unknown rule, and ExplainRule
// cannot say so — the config package does not know the rule pack — so the
// catalogue resolves it here. Without this, every rule the policy happens not to
// list renders as 关闭 + "需排查" while the checker is actively emitting findings
// from it (call-chain-depth, test-no-assert and unknown-dependency all do), which
// is the page telling the operator to investigate a rule that is working.
//
// The same fallback covers an unconfigured language. ExplainRule reports
// language_not_configured for every rule under a language the policy never names,
// and that is a true statement about the policy but not about the engine: the
// engine compiles all thirteen packs regardless, so empty-block is running in a
// repository whose policy only mentions go. Reporting enabled=false there would
// repeat the old defect at a larger scale, so the pack default decides and the
// reason stays as the explanation of *why* nobody chose a value.
func effectiveEnabled(explain config.RuleExplain, rule *rules.Rule) (bool, string) {
	switch explain.Reason {
	case "rule_not_found":
		return rule.DefaultEnabled, "builtin"
	case "language_not_configured":
		return rule.DefaultEnabled, explain.Reason
	}
	return explain.Enabled, explain.Reason
}

// effectiveSeverity prefers the policy's severity and falls back to the pack's.
// A rule with no severity at all would leave the page unable to colour its row.
func effectiveSeverity(explain config.RuleExplain, rule *rules.Rule) string {
	if explain.Severity != "" {
		return explain.Severity
	}
	return string(rule.DefaultSeverity)
}

// alternateQueryOf returns the second pattern of a rule that has one. Only the
// first alternate is reported: the chain exists so a rule can be spelled two
// ways, and a third shape has never been needed. Reporting the length of the
// chain instead would make the field a number the page cannot explain.
func alternateQueryOf(rule *rules.Rule) string {
	if rule.Alternate == nil {
		return ""
	}
	return rule.Alternate.Query
}

// ruleOrigin answers where a rule comes from in the vocabulary the page renders:
// a rule the policy wrote or a custom DSL rule is "policy_file", everything else
// is "builtin". Reason alone would not say that (a language that is not
// configured at all is still a builtin rule).
func ruleOrigin(explain config.RuleExplain) string {
	switch explain.Reason {
	case "custom_rule":
		return "policy_file"
	default:
		if explain.PolicySource == "builtin" || explain.PolicySource == "" {
			return "builtin"
		}
		return "policy_file"
	}
}

// languageIDOf maps a policy language key onto the parser's language id. The
// parser's ids are the canonical spelling, so a policy key that spells a
// language differently is translated here rather than duplicated per endpoint.
//
// The bool result distinguishes "known language" from "unknown language"; an
// unknown key is skipped by the caller instead of becoming an empty group.
func languageIDOf(lang string) (parser.LanguageID, bool) {
	id := parser.LanguageID(strings.ToLower(strings.TrimSpace(lang)))
	if !parser.IsSupported(id) {
		return "", false
	}
	return id, true
}
