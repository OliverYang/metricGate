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

// rulesView is the rule catalogue answer: the built-in rules of every language
// the policy in force covers, each merged with its effective configuration.
//
// It is deliberately policy-plus-catalogue and nothing else. The rule page used
// to answer "which keys exist", which is a question about a document; this one
// answers "which rules run, with what thresholds, and where the numbers came
// from", which is the question a reader actually has when a threshold fires.
// Reading the history store here would turn a static page into a GROUP BY over a
// million findings rows, so no database is consulted at all.
type rulesView struct {
	// GeneratedAt follows the dashboard's clock convention so a client reading
	// both payloads compares like with like.
	GeneratedAt string          `json:"generatedAt"`
	Scope       string          `json:"scope"`
	PolicyPath  string          `json:"policyPath"`
	Languages   []string        `json:"languages"`
	Rules       []ruleViewEntry `json:"rules"`
}

// ruleViewEntry is one rule in the catalogue. ID and Title come from the rule
// pack (the static inventory of what the tool can detect); enabled, severity,
// warn, error, reason and policySource come from ExplainRule (what actually
// applies). Merging them means there is no third copy of rule metadata to drift.
type ruleViewEntry struct {
	Language     string `json:"language"`
	ID           string `json:"id"`
	Title        string `json:"title"`
	Enabled      bool   `json:"enabled"`
	Severity     string `json:"severity"`
	Warn         int    `json:"warn"`
	Error        int    `json:"error"`
	Reason       string `json:"reason"`
	PolicySource string `json:"policySource"`
	Note         string `json:"note"`
	Origin       string `json:"origin"`
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
		view, err := rulesViewOf(window)
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
func rulesViewOf(window store.Window) (rulesView, error) {
	path := scopePolicyPath(window)
	cfg, err := loadPolicyQuiet(path)
	if err != nil {
		return rulesView{}, err
	}
	view := rulesView{
		GeneratedAt: nowUTC(),
		PolicyPath:  path,
		Languages:   []string{},
		Rules:       []ruleViewEntry{},
	}
	view.Scope = scopeOrigin(window)
	languages := configuredLanguages(cfg)
	for _, lang := range languages {
		view.Languages = append(view.Languages, lang)
		for _, entry := range ruleEntriesFor(cfg, lang) {
			view.Rules = append(view.Rules, entry)
		}
	}
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

// ruleEntriesFor is the built-in catalogue of one language with each rule's
// effective values folded in. The language key has already been verified as a
// registered language by configuredLanguages.
func ruleEntriesFor(cfg *config.Config, lang string) []ruleViewEntry {
	pack := rules.ForLanguage(parser.LanguageID(lang))
	out := make([]ruleViewEntry, 0, len(pack))
	for _, rule := range pack {
		if rule == nil {
			continue
		}
		explain := cfg.ExplainRule(lang, rule.ID)
		severity := explain.Severity
		if severity == "" {
			severity = string(rule.DefaultSeverity)
		}
		// A rule the policy never mentions is not an unknown rule: the engine runs
		// it at its pack default, because Runner.Enabled falls back to
		// DefaultEnabled when the policy is silent (internal/rules/rules.go:162).
		// ExplainRule cannot say that — the config package does not know the rule
		// pack — so the catalogue resolves it here. Without this, every rule the
		// policy happens not to list renders as 关闭 + "需排查" while the checker is
		// actively emitting findings from it (call-chain-depth, test-no-assert,
		// unknown-dependency all do), which is the page telling the operator to
		// investigate a rule that is working.
		enabled, reason := explain.Enabled, explain.Reason
		if reason == "rule_not_found" {
			enabled, reason = rule.DefaultEnabled, "builtin"
		}
		out = append(out, ruleViewEntry{
			Language:     lang,
			ID:           rule.ID,
			Title:        rule.Title,
			Enabled:      enabled,
			Severity:     severity,
			Warn:         explain.Thresholds.Warn,
			Error:        explain.Thresholds.Error,
			Reason:       reason,
			PolicySource: explain.PolicySource,
			Note:         explain.Note,
			Origin:       ruleOrigin(explain),
		})
	}
	return out
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
