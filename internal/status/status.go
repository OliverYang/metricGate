// Package status reports the detection situation AFTER constraints land:
// for every rule a policy compiles to, did it fire on this run, stay silent,
// or have no files to look at. A rule with zero hits is ambiguous between
// "the code is clean" and "the rule never matches" — the report makes that
// ambiguity visible per rule instead of hiding it in an aggregate count.
package status

import (
	"sort"
	"strings"

	"metricgate/internal/model"
	"metricgate/internal/parser"
	"metricgate/internal/rules"
)

// State classes one rule's detection situation on a run.
const (
	StateHit    = "hit"    // ≥1 finding
	StateSilent = "silent" // 0 findings, its language WAS scanned
	StateNA     = "na"     // 0 findings, its language is not in the tree
)

// RuleStatus is one catalogue rule's outcome on a run.
type RuleStatus struct {
	ID       string `json:"id"`
	Language string `json:"language,omitempty"` // empty = whole-graph rule
	Title    string `json:"title"`
	State    string `json:"state"`
	Hits     int    `json:"hits"`
	Errors   int    `json:"errors"`
	Warnings int    `json:"warnings"`
	Infos    int    `json:"infos"`
	Ignored  int    `json:"ignored,omitempty"` // suppression directives honored
}

// Report aggregates a finished run against the compiled rule catalogue.
type Report struct {
	Files       int          `json:"files"`
	Languages   []string     `json:"languages"`
	DurationMS  int64        `json:"durationMs"`
	Errors      int          `json:"errors"`
	Warnings    int          `json:"warnings"`
	Infos       int          `json:"infos"`
	Gate        string       `json:"gate"` // pass | fail
	GateFailOn  string       `json:"gateFailOn"`
	GateTrips   int          `json:"gateTrips,omitempty"`
	ParseErrors int          `json:"parseErrors,omitempty"`
	Rules       []RuleStatus `json:"rules"`
	Notes       []string     `json:"notes,omitempty"`
}

// tally counts one rule's findings in one bucket (one file language, or
// unknown-language files).
type tally struct{ hits, errs, warns, infos int }

func (t *tally) add(sev model.Severity) {
	t.hits++
	switch sev {
	case model.SevError:
		t.errs++
	case model.SevWarning:
		t.warns++
	default:
		t.infos++
	}
}

func (t *tally) merge(o *tally) {
	t.hits += o.hits
	t.errs += o.errs
	t.warns += o.warns
	t.infos += o.infos
}

// tallies buckets findings by rule ID and file language: most rule IDs exist
// once per language (go + java metric entries), so an unscoped RuleID map
// would double-count every hit onto both entries.
type tallies struct {
	byRule     map[string]*tally                       // findings on unknown-language files
	byRuleLang map[string]map[parser.LanguageID]*tally // ruleID -> file language
}

func collectTallies(s *model.RunSummary) *tallies {
	tl := &tallies{
		byRule:     map[string]*tally{},
		byRuleLang: map[string]map[parser.LanguageID]*tally{},
	}
	for _, f := range s.Findings {
		tl.tallyFor(f.RuleID, f.File).add(f.Severity)
	}
	return tl
}

func (tl *tallies) tallyFor(ruleID, file string) *tally {
	lang, ok := parser.LanguageForFile(file)
	if !ok {
		t := tl.byRule[ruleID]
		if t == nil {
			t = &tally{}
			tl.byRule[ruleID] = t
		}
		return t
	}
	m := tl.byRuleLang[ruleID]
	if m == nil {
		m = map[parser.LanguageID]*tally{}
		tl.byRuleLang[ruleID] = m
	}
	t := m[lang]
	if t == nil {
		t = &tally{}
		m[lang] = t
	}
	return t
}

// resolve returns the tally a catalogue entry owns: language-scoped entries
// read their language's bucket, unscoped entries (arch/clone/regress)
// aggregate all languages plus unknown-language files.
func (tl *tallies) resolve(id, language string) *tally {
	if language != "" {
		return tl.byRuleLang[id][parser.LanguageID(language)]
	}
	var agg *tally
	for _, lt := range tl.byRuleLang[id] {
		if agg == nil {
			agg = &tally{}
		}
		agg.merge(lt)
	}
	if t := tl.byRule[id]; t != nil {
		if agg == nil {
			return t
		}
		agg.merge(t)
	}
	return agg
}

func (tl *tallies) unmatched(known map[string]bool) []string {
	var out []string
	for id := range tl.byRule {
		if !known[id] {
			out = append(out, id)
		}
	}
	for id := range tl.byRuleLang {
		if !known[id] {
			out = append(out, id)
		}
	}
	return out
}

// statusOf resolves one catalogue entry's state: na when its language is not
// in the scan tree, hit when its bucket has findings, silent otherwise — the
// actionable ambiguity between clean and never-matches.
func statusOf(m rules.Meta, langSet map[string]bool, ign map[string]int, t *tally) RuleStatus {
	st := RuleStatus{ID: m.ID, Language: m.Language, Title: m.Title, Ignored: ign[m.ID]}
	if m.Language != "" && !langSet[m.Language] {
		// Whatever the same-ID tally holds belongs to another language's entry.
		st.State = StateNA
		return st
	}
	if t != nil && t.hits > 0 {
		st.State = StateHit
		st.Hits, st.Errors, st.Warnings, st.Infos = t.hits, t.errs, t.warns, t.infos
		return st
	}
	st.State = StateSilent
	return st
}

// gatePreview predicts the enforceGate verdict from the severity counts.
func gatePreview(rep *Report, failOn model.Severity) {
	trips := rep.Errors
	if failOn == model.SevWarning {
		trips += rep.Warnings
	}
	rep.GateFailOn = string(failOn)
	if trips > 0 {
		rep.Gate, rep.GateTrips = "fail", trips
	} else {
		rep.Gate = "pass"
	}
}

// Build assembles the report from a finished engine run. Pure: the caller
// owns engine lifecycle; tests feed synthetic findings.
func Build(cat []rules.Meta, langs []string, s *model.RunSummary, failOn model.Severity) Report {
	langSet := map[string]bool{}
	for _, l := range langs {
		langSet[l] = true
	}
	tl := collectTallies(s)
	ign := map[string]int{}
	for id, n := range s.IgnoredByRule {
		ign[id] = n
	}

	rep := Report{
		Files:       s.Files,
		Languages:   langs,
		DurationMS:  s.Duration.Milliseconds(),
		Errors:      s.Count(model.SevError),
		Warnings:    s.Count(model.SevWarning),
		Infos:       s.Count(model.SevInfo),
		ParseErrors: len(s.ParseErrors),
	}

	known := map[string]bool{}
	for _, m := range cat {
		known[m.ID] = true
		rep.Rules = append(rep.Rules, statusOf(m, langSet, ign, tl.resolve(m.ID, m.Language)))
	}
	if ids := tl.unmatched(known); len(ids) > 0 {
		sort.Strings(ids)
		rep.Notes = append(rep.Notes,
			"findings outside the compiled catalogue (engine-internal or drifted ids): "+strings.Join(ids, ", "))
	}
	gatePreview(&rep, failOn)
	sortRules(rep.Rules)
	return rep
}

// stateRank orders the report: hits first (most informative), then silent
// rules (the actionable ambiguity), then not-applicable.
var stateRank = map[string]int{StateHit: 0, StateSilent: 1, StateNA: 2}

func sortRules(rs []RuleStatus) {
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if stateRank[a.State] != stateRank[b.State] {
			return stateRank[a.State] < stateRank[b.State]
		}
		if a.Hits != b.Hits {
			return a.Hits > b.Hits
		}
		return a.ID < b.ID
	})
}
