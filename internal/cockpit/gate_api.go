package cockpit

import (
	"fmt"
	"net/http"
	"sort"

	"metricgate/internal/config"
	"metricgate/internal/metrics"
	"metricgate/internal/model"
	"metricgate/internal/store"
	"metricgate/internal/webspec"
)

// Gate, measures and rules API ---------------------------------------------
//
// GET /api/gate projects the policy into a gate the UI can render: the
// conditions in force, the numbers they currently see, and the verdict. The
// verdict is always the one the checker produced — it is read off the last
// stored run — never recomputed here. A gate that disagreed with the checker
// would teach operators to trust neither.
//
// GET /api/measures grades the repository over a window from snapshots the
// store already holds. Every row carries its raw numbers and its thresholds so
// a grade can be recomputed by whoever is reading it; that is the only reason
// a grade is worth showing (docs/design/cockpit-single-file-tech.md §1).
//
// GET /api/rules lists the rules in force for a language, with the provenance
// the policy view already shows. It exists so the Rules view does not have to
// reimplement selection to answer "what applies to this file".

// gateAPI serves GET /api/gate.
func gateAPI(logPath, dbPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		scope, err := policyScopeWindow(r, logPath, dbPath)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		view, err := gateView(dbPath, scope)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "gate unavailable: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, view)
	}
}

// GateCondition is one condition of the gate, in the shape the policy states
// it: a measure, a comparison, and the value that crosses the line.
//
// fail_on is expressed as one condition per metric with an error band, because
// that is what the checker does: for rules it counts findings at the fail_on
// level, and for metrics it fails when any file crosses the error band. The
// condition list is therefore a description of "what would make this run
// fail", derived from the same thresholds the engine uses.
type GateCondition struct {
	// Language is the language section the band was read from. A policy that
	// configures three languages gates on three sets of bands, and the table
	// then repeats the same measure once per language — so without this field
	// three identical-looking "Cyclomatic complexity ≤ 15" rows appear with
	// nothing saying which one covers which file.
	Language string `json:"language"`
	Measure  string `json:"measure"`
	Label    string `json:"label"`
	// Operator is "≤": the condition passes when the worst measured value is
	// at or below Value.
	Operator string `json:"operator"`
	Value    int    `json:"value"`
	// Current is the run's worst-case value for this measure. Worst, not
	// average: the engine fails on any single file above the band, so an
	// average would answer a question nobody asked.
	Current int  `json:"current"`
	HasData bool `json:"hasData"`
	Failing bool `json:"failing"`
	// Warn is the warning band the policy set for this language and measure,
	// before gateBand's fallback. Without it the projection only knows where a
	// condition fails, so the status column had two choices for a measured row
	// — failing or passing — and drew a value sitting inside the warning zone
	// the same green as a comfortable one. That is the band an operator is
	// about to cross, and the table exists to show it.
	Warn int `json:"warn"`
	// Warning is measured, above Warn and not yet above Value. It is false when
	// the policy sets no warning band (Warn == 0) and when Err == Warn, because
	// then "warning" and "failing" are the same line and saying both would
	// report one measurement as two states.
	Warning bool `json:"warning"`
	// Source is where the threshold came from, in the same vocabulary the
	// policy view uses: "policy_file", "preset:<name>" or "builtin".
	Source string `json:"source,omitempty"`
}

// GateView is the gate summary: what blocks, the conditions, the verdict, and
// the run the verdict came from.
type GateView struct {
	FailOn     string          `json:"failOn"`
	Status     string          `json:"status"` // passed | failed | unknown
	Reason     string          `json:"reason,omitempty"`
	Conditions []GateCondition `json:"conditions"`
	RunID      int64           `json:"runId,omitempty"`
	Commit     string          `json:"commit,omitempty"`
	StartedAt  string          `json:"startedAt,omitempty"`
	Error      string          `json:"error,omitempty"`
	// Foreign records that the verdict came from another project root, so the
	// UI can say "this is not your repository's latest run" instead of showing a
	// neighbour's PASSED as the operator's own. MeasuresView has carried this
	// flag since iteration 1, but nothing rendered it and the gate never set it
	// at all — so a scope with no run of its own displayed a green badge for
	// somebody else's run with no label. The empty-project entry this iteration
	// adds made that state reachable, which is what turned a latent gap into a
	// visible one.
	Foreign bool `json:"foreign,omitempty"`
}

// gateView reads the discovered policy for the conditions and the store for the
// verdict. No store yields "unknown" rather than a fake "passed": there is
// nothing to pass yet, and saying so is more useful than a green banner that
// actually means "never ran".
func gateView(dbPath string, scope store.Window) (GateView, error) {
	cfg, err := loadPolicyQuiet(scopePolicyPath(scope))
	if err != nil {
		return GateView{}, err
	}
	view := GateView{
		FailOn:     string(cfg.FailOn()),
		Conditions: []GateCondition{},
		Status:     "unknown",
		Reason:     "history store is disabled; no gate verdict is available",
	}
	if dbPath == "" {
		return view, nil
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return GateView{}, err
	}
	defer st.Close()
	runs, err := st.LatestRunsIn(scopeRunWindow(scope))
	if err != nil {
		return GateView{}, err
	}
	if len(runs) == 0 {
		// A shared database with no run under this project's root is a real
		// state (the operator ran the gate somewhere else). Rather than show a
		// green "passed" that means "never ran here", fall back to the newest
		// run anywhere and label it as such — the verdict is still the one the
		// checker produced, just from another directory.
		runs, _ = st.LatestRunsIn(store.Window{Limit: 1})
		if len(runs) == 0 {
			view.Reason = "no stored run yet"
			return view, nil
		}
		view.Foreign = true
	}
	run := runs[0]
	view.RunID, view.Commit, view.StartedAt = run.ID, run.GitCommit, run.StartedAt
	view.Status, view.Reason = gateVerdict(cfg, run)
	view.Conditions = gateConditions(cfg, st, run)
	return view, nil
}

// configLangs lists the languages the policy configures. The gate shows one
// condition per configured metric per language, because the same metric can
// have a different band in Go than in Java and a single row would have to
// choose one of them.
func configLangs(cfg *config.Config) []string {
	out := []string{}
	for _, lang := range knownLanguages {
		if _, ok := langSectionOf(cfg, lang); ok {
			out = append(out, lang)
		}
	}
	return out
}

// langSectionOf reads one language's section. A policy that configures no
// languages gates on the defaults, which is a different question from "this
// language has no thresholds" — the defaults are the metric bands the engine
// falls back to.
func langSectionOf(cfg *config.Config, lang string) (config.LanguageConfig, bool) {
	if cfg == nil {
		return config.LanguageConfig{}, false
	}
	sec, ok := cfg.Languages[lang]
	return sec, ok
}

// knownLanguages are the language ids the policy gates on.
var knownLanguages = []string{"go", "java", "javascript", "typescript", "python", "c", "cpp", "csharp", "rust"}

// metricSource reports where a metric's band came from, in the same vocabulary
// the policy view uses: this policy, a preset, an extended parent, or the
// built-in default.
func metricSource(cfg *config.Config, lang, metric string) string {
	key := fmt.Sprintf("languages.%s.metrics.%s", lang, metric)
	source, path, _ := cfg.ValueSource(key, "builtin")
	if source == "extends" && path != "" {
		return path
	}
	return source
}

// gateVerdict reports the run's own outcome under fail_on. A warning under
// fail_on: error stays a warning: reporting it as a failure would make the UI
// disagree with the exit code the operator just saw.
func gateVerdict(cfg *config.Config, run store.RunRecord) (string, string) {
	blocking := run.WarningCount + run.ErrorCount
	what := "warnings"
	if cfg.FailOn() == model.SevError {
		blocking = run.ErrorCount
	}
	if blocking == 0 {
		return "passed", "no findings at or above the fail_on level"
	}
	if run.ErrorCount > 0 {
		what = "errors"
	}
	return "failed", fmt.Sprintf("%d %s at or above fail_on=%s", blocking, what, cfg.FailOn())
}

// gateConditions builds the condition list from the policy's metric bands. A
// metric with no band configured is skipped rather than shown as "always
// passes": a condition with no threshold is not a condition.
func gateConditions(cfg *config.Config, st *store.Store, run store.RunRecord) []GateCondition {
	out := []GateCondition{}
	// Per-language, not run-wide: metric_snapshots groups by metric only, so
	// RunMeasures returns one MAX for the whole run and the go and java blocks
	// then render that same number as if each language measured it. A language
	// with no snapshots in this run must stay hasData=false — borrowing the
	// run-wide worst would report some other language's file as this one's.
	byLang, err := st.RunMeasuresByLang(run.ID)
	if err != nil {
		byLang = nil
	}
	for _, lang := range configLangs(cfg) {
		worst := make(map[string]int, len(byLang[lang]))
		for _, m := range byLang[lang] {
			worst[m.Metric] = m.Worst
		}
		for _, metric := range gateMetrics {
			warn, errAt, ok := gateBand(cfg, lang, metric)
			if !ok {
				continue
			}
			condition := GateCondition{
				Language: lang,
				Measure:  metric,
				Label:    metricLabels[metric],
				Operator: "≤",
				Value:    errAt,
				Warn:     warn,
				Source:   metricSource(cfg, lang, metric),
			}
			if value, ok := worst[metric]; ok {
				condition.HasData, condition.Current = true, value
				condition.Failing = value > errAt
				condition.Warning = warn > 0 && errAt > warn && value > warn && !condition.Failing
			}
			out = append(out, condition)
		}
	}
	sortConditionsByLanguage(out)
	return out
}

// gateBand returns both bands of a condition: the warning line the policy set
// and the line it fails on. The error band still falls back to the warning one
// when no error band is configured — a policy that only warns still bounds the
// metric — but the warning band is returned on its own so the caller can tell
// "inside the warning zone" apart from "comfortably under the threshold".
func gateBand(cfg *config.Config, lang, metric string) (warn, errAt int, ok bool) {
	warn, errAt = 0, 0
	if w, e, enabled := cfg.MetricThresholds(lang, metric); enabled == nil || *enabled {
		warn, errAt = w, e
	}
	if errAt == 0 {
		errAt = warn
	}
	return warn, errAt, errAt != 0
}

// sortConditionsByLanguage groups the condition list by language without
// disturbing the metric order inside a group.
//
// configLangs walks the engine's own language list, which is neither
// alphabetical nor the policy's declaration order, so an unsorted table groups
// in an order that follows an unrelated constant. The rows are not deduped:
// the same measure with two different bands is two conditions that can each
// fail, and collapsing them would hide one of them.
func sortConditionsByLanguage(conditions []GateCondition) {
	sort.SliceStable(conditions, func(i, j int) bool {
		ri, rj := languageRank(conditions[i].Language), languageRank(conditions[j].Language)
		if ri != rj {
			return ri < rj
		}
		return conditions[i].Language < conditions[j].Language
	})
}

// languageRank orders the gate's language groups: the ones the tool itself is
// written in first, then the rest alphabetically.
var languageRankOrder = []string{"go", "java", "javascript", "typescript", "python"}

func languageRank(lang string) int {
	for i, known := range languageRankOrder {
		if known == lang {
			return i
		}
	}
	return len(languageRankOrder)
}

// gateMetrics are the metrics a condition can be written for, in the order the
// gate shows them: complexity first, because that is what operators gate on.
var gateMetrics = []string{
	metrics.MetricCyclomatic, metrics.MetricCognitive, metrics.MetricFuncLength,
	metrics.MetricFileLength, metrics.MetricNesting, metrics.MetricParams,
	metrics.MetricNPath, metrics.MetricFanOut, metrics.MetricBoolParams,
}

// metricLabels keeps the gate readable without the caller mapping ids to
// words. The labels are for the reader; the ids stay the wire values.
var metricLabels = map[string]string{
	metrics.MetricCyclomatic: "Cyclomatic complexity",
	metrics.MetricCognitive:  "Cognitive complexity",
	metrics.MetricFuncLength: "Function length",
	metrics.MetricFileLength: "File length",
	metrics.MetricNesting:    "Nesting depth",
	metrics.MetricParams:     "Parameter count",
	metrics.MetricNPath:      "NPATH complexity",
	metrics.MetricFanOut:     "Fan-out",
	metrics.MetricBoolParams: "Boolean parameters",
}

// measuresAPI serves GET /api/measures: the graded view of one run's metrics.
//
// Grading is deterministic and shown next to the numbers it came from. The
// bands are the policy's own: a metric at or below its warning line is A,
// above the warning line is B/C, and above the error line is D/E. That mapping
// is legible to a reader ("what is B? one file past the warning line") and it
// does not invent a scale the engine does not have.
func measuresAPI(logPath, dbPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		scope, err := policyScopeWindow(r, logPath, dbPath)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		view, err := measuresView(dbPath, scope)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "measures unavailable: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, view)
	}
}

// MeasureRow is one graded measure.
type MeasureRow struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Scale string `json:"scale"` // "lower" is better for every measure here
	Value int    `json:"value"`
	Grade string `json:"grade"`
	// Exceeded is how many snapshots in the run sit at or above the warning
	// line, so a good-looking worst case cannot hide many mediocre ones.
	Exceeded int `json:"exceeded"`
	Warn     int `json:"warnThreshold"`
	Err      int `json:"errThreshold"`
}

// MeasuresView is the Summary payload: the run it was graded from and the rows.
//
// Foreign records that the graded run came from another project root, so the
// UI can say "this is not your repository's latest run" instead of presenting
// a neighbour's numbers as the operator's own.
type MeasuresView struct {
	Foreign   bool         `json:"foreign,omitempty"`
	RunID     int64        `json:"runId,omitempty"`
	StartedAt string       `json:"startedAt,omitempty"`
	Window    string       `json:"window"`
	Grade     string       `json:"grade"`
	Measures  []MeasureRow `json:"measures"`
	Files     int          `json:"files"`
	// Reason explains an empty view: "no run yet" is a state the UI shows, not
	// a failure to hide.
	Reason string `json:"reason,omitempty"`
}

// measuresView grades the last run's metrics.
func measuresView(dbPath string, scope store.Window) (MeasuresView, error) {
	view := MeasuresView{Measures: []MeasureRow{}, Grade: webspec.GradeA}
	if dbPath == "" {
		view.Reason = "history store is disabled; enable store in policy to grade this repository"
		return view, nil
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return MeasuresView{}, err
	}
	defer st.Close()
	runs, err := st.LatestRunsIn(scopeRunWindow(scope))
	if err != nil {
		return MeasuresView{}, err
	}
	if len(runs) == 0 {
		// Same fallback as the gate: grade the newest run anywhere rather than
		// show nothing, but say which one it was so the reader is not misled
		// about whose code they are looking at.
		runs, err = st.LatestRunsIn(store.Window{Limit: 1})
		if err != nil {
			return MeasuresView{}, err
		}
		if len(runs) == 0 {
			view.Reason = "no stored run yet; run `metricgate check` with store.enabled"
			return view, nil
		}
		view.Foreign = true
	}
	run := runs[0]
	view.RunID, view.StartedAt, view.Window = run.ID, run.StartedAt, fmt.Sprintf("last run %s", run.StartedAt)
	cfg, err := loadPolicyQuiet(scopePolicyPath(scope))
	if err != nil {
		return MeasuresView{}, err
	}
	measures, err := st.RunMeasures(run.ID)
	if err != nil {
		return MeasuresView{}, err
	}
	worst := make(map[string]int, len(measures))
	exceeded := make(map[string]int, len(measures))
	for _, m := range measures {
		worst[m.Metric], exceeded[m.Metric] = m.Worst, m.Exceeded
	}
	view.Files = run.Files
	var grades []string
	for _, metric := range gateMetrics {
		row, ok := gradeMetric(cfg, metric, worst, exceeded)
		if !ok {
			continue
		}
		view.Measures = append(view.Measures, row)
		grades = append(grades, row.Grade)
	}
	view.Grade = worstGrade(grades)
	return view, nil
}

// gradeMetric grades one measure against its bands. A metric with no band and
// no measurement is not a row: "graded nothing" is not a grade.
func gradeMetric(cfg *config.Config, metric string, worst, exceeded map[string]int) (MeasureRow, bool) {
	var warn, errAt int
	for _, lang := range configLangs(cfg) {
		w, e, enabled := cfg.MetricThresholds(lang, metric)
		if enabled != nil && !*enabled {
			continue
		}
		if w > warn {
			warn = w
		}
		if e > errAt {
			errAt = e
		}
	}
	value, measured := worst[metric]
	if warn == 0 && !measured {
		return MeasureRow{}, false
	}
	row := MeasureRow{
		ID:       metric,
		Label:    metricLabels[metric],
		Scale:    webspec.ScaleLowerIsBetter,
		Value:    value,
		Exceeded: exceeded[metric],
		Warn:     warn,
		Err:      errAt,
		Grade:    gradeFor(value, warn, errAt),
	}
	return row, true
}

// gradeFor maps a value onto A-E against the two bands. A and B are the two
// sides of the warning line; D and E the two sides of the error line. The
// letters exist for scanning, the thresholds exist for recomputing, and the
// note in the UI shows both.
func gradeFor(value, warn, errAt int) string {
	switch {
	case errAt > 0 && value > errAt:
		return webspec.GradeD
	case warn > 0 && value > warn:
		return webspec.GradeC
	case warn > 0 && value == 0:
		return webspec.GradeA
	case warn > 0:
		return webspec.GradeB
	default:
		// No band configured: the value is reported, not graded.
		return webspec.GradeA
	}
}

// worstGrade is the grade a repository is known by: the worst of its measures'
// grades. The alternative — the average — would let a single catastrophic file
// hide behind ten good ones, which is the failure a gate exists to prevent.
func worstGrade(grades []string) string {
	order := []string{webspec.GradeA, webspec.GradeB, webspec.GradeC, webspec.GradeD, webspec.GradeE}
	worst := ""
	for _, grade := range grades {
		if indexOf(grade, order) > indexOf(worst, order) {
			worst = grade
		}
	}
	if worst == "" {
		return webspec.GradeA
	}
	return worst
}

func indexOf(want string, values []string) int {
	for i, v := range values {
		if v == want {
			return i
		}
	}
	return -1
}
