package cockpit

// The gate's condition list is the one place the cockpit turns policy numbers
// into rows an operator judges. It was shipped with no Go-side test at all:
// the shape was only asserted by render_all.mjs, which checks that whatever the
// backend sends gets drawn — so a backend that sent the wrong grouping, or
// judged a value against the wrong language's band, would have rendered
// perfectly. These tests pin the projection itself.

import (
	"path/filepath"
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/metrics"
	"metricgate/internal/model"
	"metricgate/internal/store"
)

// gateTestPolicy configures two languages whose bands deliberately differ, plus
// one metric that exists in only one of them.
const gateTestPolicy = `
version: 1
languages:
  java:
    metrics:
      cyclomatic-complexity: { warn: 10, error: 20 }
      file-length: { warn: 400, error: 800 }
  go:
    metrics:
      cyclomatic-complexity: { warn: 8, error: 12 }
      file-length: { warn: 300, error: 500 }
      nesting-depth: { warn: 4, error: 6 }
`

func gateTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.LoadWithSource([]byte(gateTestPolicy), "policy_file", ".metricgate.yaml")
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	return cfg
}

// gateTestRun stores one run whose worst measured value sits between the two
// languages' bands for cyclomatic complexity.
func gateTestRun(t *testing.T, values map[string]int) (*store.Store, store.RunRecord) {
	return gateTestRunFiles(t, values, []string{"a.go", "A.java"})
}

// gateTestRunFiles seeds a run measuring every given value once per file.
// One file per language: a policy listing two languages only means both were
// measured if the run actually carries a file of each, and a test that needs
// to prove what an unmeasured language looks like has to be able to seed a
// single one.
func gateTestRunFiles(t *testing.T, values map[string]int, files []string) (*store.Store, store.RunRecord) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	summary := &model.RunSummary{Files: len(files)}
	for metric, value := range values {
		for _, file := range files {
			summary.Metrics = append(summary.Metrics, model.MetricResult{
				Metric: metric, File: file, Value: value,
			})
		}
	}
	id, err := st.SaveRun(summary, "test", "cafe1234")
	if err != nil {
		t.Fatalf("save run: %v", err)
	}
	runs, err := st.LatestRuns(1)
	if err != nil {
		t.Fatalf("read run: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != id {
		t.Fatalf("stored run = %+v, want one run with id %d", runs, id)
	}
	return st, runs[0]
}

func conditionAt(t *testing.T, conditions []GateCondition, language, measure string) GateCondition {
	t.Helper()
	for _, c := range conditions {
		if c.Language == language && c.Measure == measure {
			return c
		}
	}
	t.Fatalf("no condition for %s/%s in %+v", language, measure, conditions)
	return GateCondition{}
}

// The policy declares go before java nowhere: configLangs walks the engine's
// own language list. The table has to present one contiguous group per language
// with the metric order intact inside it, because the frontend inserts a group
// header whenever the language changes and would draw three headers for an
// interleaved list.
func TestGateConditionsGroupByLanguageInRankOrder(t *testing.T) {
	cfg := gateTestConfig(t)
	st, run := gateTestRun(t, map[string]int{metrics.MetricCyclomatic: 15})
	defer st.Close()

	conditions := gateConditions(cfg, st, run)
	got := make([]string, 0, len(conditions))
	for _, c := range conditions {
		got = append(got, c.Language+"/"+c.Measure)
	}
	want := []string{
		"go/" + metrics.MetricCyclomatic, "go/" + metrics.MetricFileLength,
		"go/" + metrics.MetricNesting,
		"java/" + metrics.MetricCyclomatic, "java/" + metrics.MetricFileLength,
	}
	if len(got) != len(want) {
		t.Fatalf("conditions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("conditions = %v, want %v", got, want)
		}
	}
}

// The same measure under two languages is two conditions, not one merged row:
// they can fail independently. A value of 15 is over go's error band of 12 and
// under java's of 20, so the two rows must disagree.
func TestGateConditionsKeepOneRowPerLanguageBand(t *testing.T) {
	cfg := gateTestConfig(t)
	st, run := gateTestRun(t, map[string]int{metrics.MetricCyclomatic: 15})
	defer st.Close()

	conditions := gateConditions(cfg, st, run)
	goCond := conditionAt(t, conditions, "go", metrics.MetricCyclomatic)
	javaCond := conditionAt(t, conditions, "java", metrics.MetricCyclomatic)

	if goCond.Value != 12 || javaCond.Value != 20 {
		t.Fatalf("bands = go %d / java %d, want 12 / 20", goCond.Value, javaCond.Value)
	}
	if !goCond.Failing {
		t.Errorf("go condition does not fail: 15 is above its error band of 12")
	}
	if javaCond.Failing {
		t.Errorf("java condition fails: 15 is below its error band of 20")
	}
	// The measured column is this language's own worst value, not the run's:
	// go and java each carry a snapshot of 15, so both rows show data and both
	// rows disagree with the run-wide answer in their own way.
	if !goCond.HasData || !javaCond.HasData {
		t.Fatalf("hasData = go %v / java %v, want both true", goCond.HasData, javaCond.HasData)
	}
	if goCond.Current != 15 || javaCond.Current != 15 {
		t.Fatalf("current = go %d / java %d, want 15 / 15", goCond.Current, javaCond.Current)
	}
}

// The language the run never measured must not display another language's
// number. Before RunMeasuresByLang, worst was keyed by metric alone, so a
// policy gating go and java with only go files in the run rendered java's row
// as "15, passing" — data from a language that was never measured there.
func TestGateConditionsDoNotBorrowAnotherLanguagesMeasurement(t *testing.T) {
	cfg := gateTestConfig(t)
	st, run := gateTestRunFiles(t, map[string]int{metrics.MetricCyclomatic: 15}, []string{"a.go"})
	defer st.Close()

	conditions := gateConditions(cfg, st, run)
	goCond := conditionAt(t, conditions, "go", metrics.MetricCyclomatic)
	javaCond := conditionAt(t, conditions, "java", metrics.MetricCyclomatic)

	if !goCond.HasData || goCond.Current != 15 {
		t.Errorf("go hasData %v / current %d, want true / 15 (go files were measured)",
			goCond.HasData, goCond.Current)
	}
	if javaCond.HasData || javaCond.Current != 0 {
		t.Errorf("java hasData %v / current %d, want false / 0: no java file was measured in this run",
			javaCond.HasData, javaCond.Current)
	}
	if javaCond.Failing {
		t.Errorf("java condition fails on a language with no measurement")
	}
}

// A metric the run produced no snapshot for is "no data", not "passed". The
// frontend renders this as its own badge, so the projection must leave Failing
// false and HasData false together rather than inventing a value.
func TestGateConditionsMarkUnmeasuredMetricsAsNoData(t *testing.T) {
	cfg := gateTestConfig(t)
	st, run := gateTestRun(t, map[string]int{})
	defer st.Close()

	conditions := gateConditions(cfg, st, run)
	if len(conditions) == 0 {
		t.Fatal("no conditions at all: an unmeasured run must still list what it would be judged on")
	}
	for _, c := range conditions {
		if c.HasData {
			t.Errorf("%s/%s reports data on a run with no metric snapshots", c.Language, c.Measure)
		}
		if c.Failing {
			t.Errorf("%s/%s fails without data", c.Language, c.Measure)
		}
		if c.Current != 0 {
			t.Errorf("%s/%s current = %d, want 0 (no measured value to show)", c.Language, c.Measure, c.Current)
		}
		if c.Operator != "≤" {
			t.Errorf("%s/%s operator = %q, want ≤", c.Language, c.Measure, c.Operator)
		}
	}
	cond := conditionAt(t, conditions, "go", metrics.MetricFileLength)
	if cond.Label != "File length" {
		t.Errorf("label = %q, want %q", cond.Label, "File length")
	}
	if cond.Source != "policy_file" {
		t.Errorf("source = %q, want policy_file", cond.Source)
	}
}

// gateBand judges against the error band and falls back to the warning band,
// because a policy that only warns still bounds the metric; a metric with
// neither is not a condition at all, so the row is skipped rather than showing
// a threshold of zero that nothing can satisfy.
func TestGateBandPrefersErrorAndFallsBackToWarning(t *testing.T) {
	cfg := gateTestConfig(t)
	if warn, got, ok := gateBand(cfg, "go", metrics.MetricCyclomatic); !ok || got != 12 || warn != 8 {
		t.Errorf("gateBand(go, cyclomatic) = warn %d / err %d / %v, want 8 / 12 / true", warn, got, ok)
	}
	if _, _, ok := gateBand(cfg, "java", metrics.MetricNesting); ok {
		t.Errorf("gateBand(java, nesting) reported a condition for a metric the policy does not configure")
	}
	warnOnly, err := config.LoadWithSource([]byte(`
version: 1
languages:
  go:
    metrics:
      cyclomatic-complexity: { warn: 10 }
`), "policy_file", ".metricgate.yaml")
	if err != nil {
		t.Fatalf("load warn-only policy: %v", err)
	}
	if warn, got, ok := gateBand(warnOnly, "go", metrics.MetricCyclomatic); !ok || got != 10 || warn != 10 {
		t.Errorf("gateBand with only a warning band = warn %d / err %d / %v, want 10 / 10 / true", warn, got, ok)
	}
}

// An unknown language sorts after every known one: the group order must be
// stable, and a policy that adds a language the rank table has never heard of
// must not jump into the middle of the list.
func TestLanguageRankOrdersUnknownLanguagesLast(t *testing.T) {
	conditions := []GateCondition{
		{Language: "cobol"}, {Language: "python"}, {Language: "java"}, {Language: "go"},
	}
	sortConditionsByLanguage(conditions)
	got := make([]string, 0, len(conditions))
	for _, c := range conditions {
		got = append(got, c.Language)
	}
	want := []string{"go", "java", "python", "cobol"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted languages = %v, want %v", got, want)
		}
	}
}

// A scope with no run of its own falls back to the newest run anywhere, so the
// operator sees the checker's real verdict instead of a fake green. That
// fallback must be labelled: the empty-project entry added in iteration 3 made
// this state reachable, and an unlabelled PASSED belonging to a neighbour reads
// as "your repository passed". MeasuresView carried `foreign` for exactly this
// since iteration 1, but the gate never set it and no frontend code read
// either flag.
func TestGateMarksAnotherProjectsRunAsForeign(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	summary := &model.RunSummary{Files: 1, Metrics: []model.MetricResult{{Metric: "cyclomatic-complexity", File: "a.go", Value: 3}}}
	if _, err := st.SaveRunForProject(summary, "test", "cafe1234", "/repo-other"); err != nil {
		t.Fatalf("save run: %v", err)
	}

	// A scope that is a known project but has no run of its own: the window
	// filters to /repo-mine, finds nothing, and gateView falls back.
	mine := store.Window{Limit: 1, ProjectRoot: "/repo-mine"}
	view, err := gateView(dbPath, mine)
	if err != nil {
		t.Fatalf("gateView: %v", err)
	}
	if !view.Foreign {
		t.Fatalf("gateView foreign = false, want true for a run from another root (runID=%d)", view.RunID)
	}

	// The same run read under its own root is not foreign.
	own := store.Window{Limit: 1, ProjectRoot: "/repo-other"}
	view, err = gateView(dbPath, own)
	if err != nil {
		t.Fatalf("gateView: %v", err)
	}
	if view.Foreign {
		t.Fatalf("gateView foreign = true, want false when the run belongs to the scope")
	}
}

// MeasuresView already flagged the borrowed run; this pins that it stays set so
// the overview's caption cannot regress to showing a neighbour's grades as the
// project's own.
func TestMeasuresMarksAnotherProjectsRunAsForeign(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	summary := &model.RunSummary{Files: 1, Metrics: []model.MetricResult{{Metric: "cyclomatic-complexity", File: "a.go", Value: 3}}}
	if _, err := st.SaveRunForProject(summary, "test", "cafe1234", "/repo-other"); err != nil {
		t.Fatalf("save run: %v", err)
	}
	view, err := measuresView(dbPath, store.Window{Limit: 1, ProjectRoot: "/repo-mine"})
	if err != nil {
		t.Fatalf("measuresView: %v", err)
	}
	if !view.Foreign {
		t.Fatalf("measuresView foreign = false, want true (runID=%d)", view.RunID)
	}
}
