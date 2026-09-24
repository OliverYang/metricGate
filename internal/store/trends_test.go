package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"metricgate/internal/model"
)

// TestSeverityTrend exercises SeverityTrend: three runs with distinct
// severity counts, written back-to-back, must come out oldest first with the
// counts preserved verbatim. The test also pins the (started_at, id) ordering
// so a future schema change that drops the id tie-breaker fails loudly.
func TestSeverityTrend(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	plans := []struct {
		errs, warns, infos int
	}{
		{errs: 1, warns: 2, infos: 0},
		{errs: 0, warns: 1, infos: 3},
		{errs: 2, warns: 0, infos: 1},
	}
	for _, p := range plans {
		sum := &model.RunSummary{
			Files:    1,
			Duration: time.Millisecond,
			Findings: buildFindings(p.errs, p.warns, p.infos),
		}
		if _, err := st.SaveRun(sum, "0.1.0", "deadbeef"); err != nil {
			t.Fatalf("SaveRun: %v", err)
		}
		// Sleep is unnecessary: SaveRun is synchronous and modernc/sqlite
		// stamps started_at from Go's wall clock per call. If two runs end
		// up with the same RFC3339 string (sub-second ties), the inner id
		// ordering must still keep them stable.
	}

	trend, err := st.SeverityTrend(0)
	if err != nil {
		t.Fatalf("SeverityTrend: %v", err)
	}
	if trend.Runs != len(plans) {
		t.Fatalf("Runs = %d, want %d", trend.Runs, len(plans))
	}
	if len(trend.Series) != len(plans) {
		t.Fatalf("Series len = %d, want %d", len(trend.Series), len(plans))
	}

	// Each point should report the same severity counts we wrote in. The
	// series is oldest first, so plans[0] → series[0], etc.
	for i, p := range plans {
		got := trend.Series[i]
		if got.Errors != p.errs || got.Warnings != p.warns || got.Infos != p.infos {
			t.Errorf("Series[%d] = %+v, want E=%d W=%d I=%d",
				i, got, p.errs, p.warns, p.infos)
		}
	}

	// Time ordering: started_at must be non-decreasing.
	for i := 1; i < len(trend.Series); i++ {
		if trend.Series[i-1].StartedAt > trend.Series[i].StartedAt {
			t.Errorf("series not oldest-first at index %d: %q > %q",
				i, trend.Series[i-1].StartedAt, trend.Series[i].StartedAt)
		}
	}
}

// TestRuleFrequency: aggregate finding counts across multiple runs and check
// the descending-count / ascending-rule-id ordering that the `trends` CLI
// depends on for stable output.
func TestRuleFrequency(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Two runs. Run A: 3× cyclomatic, 1× function-length. Run B: 2×
	// cyclomatic, 2× function-length. Total: cyclomatic = 5,
	// function-length = 3.
	save := func(findings []model.Finding) {
		t.Helper()
		if _, err := st.SaveRun(&model.RunSummary{Files: 1, Findings: findings}, "0.1.0", ""); err != nil {
			t.Fatalf("SaveRun: %v", err)
		}
	}
	save([]model.Finding{
		{RuleID: "cyclomatic-complexity", Severity: model.SevWarning, Source: model.SrcMetric, File: "a.go"},
		{RuleID: "cyclomatic-complexity", Severity: model.SevWarning, Source: model.SrcMetric, File: "a.go"},
		{RuleID: "cyclomatic-complexity", Severity: model.SevWarning, Source: model.SrcMetric, File: "a.go"},
		{RuleID: "function-length", Severity: model.SevWarning, Source: model.SrcMetric, File: "a.go"},
	})
	save([]model.Finding{
		{RuleID: "cyclomatic-complexity", Severity: model.SevWarning, Source: model.SrcMetric, File: "b.go"},
		{RuleID: "cyclomatic-complexity", Severity: model.SevWarning, Source: model.SrcMetric, File: "b.go"},
		{RuleID: "function-length", Severity: model.SevWarning, Source: model.SrcMetric, File: "b.go"},
		{RuleID: "function-length", Severity: model.SevWarning, Source: model.SrcMetric, File: "b.go"},
	})

	rules, err := st.RuleFrequency(0)
	if err != nil {
		t.Fatalf("RuleFrequency: %v", err)
	}
	want := []RuleCount{
		{RuleID: "cyclomatic-complexity", Count: 5},
		{RuleID: "function-length", Count: 3},
	}
	if !reflect.DeepEqual(rules, want) {
		t.Errorf("RuleFrequency = %+v, want %+v", rules, want)
	}
}

// TestLatestRunsOrdering: newest-first, ties broken by id descending. Same
// reasoning as TestSeverityTrend: SaveRun's RFC3339 timestamp can collide
// when runs are issued back-to-back, and the ordering invariant must hold
// either way.
func TestLatestRunsOrdering(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	for i := 0; i < 3; i++ {
		if _, err := st.SaveRun(&model.RunSummary{Files: 1}, "0.1.0", ""); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := st.LatestRuns(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("len = %d, want 3", len(runs))
	}
	for i := 1; i < len(runs); i++ {
		if runs[i-1].ID < runs[i].ID {
			t.Errorf("runs not newest-first at index %d: id=%d then id=%d", i, runs[i-1].ID, runs[i].ID)
		}
	}
}

func buildFindings(errs, warns, infos int) []model.Finding {
	out := make([]model.Finding, 0, errs+warns+infos)
	add := func(n int, sev model.Severity) {
		for i := 0; i < n; i++ {
			out = append(out, model.Finding{
				RuleID: string(sev), Severity: sev, Source: model.SrcRule,
				File: "x.go", StartLine: 1, EndLine: 1, Message: "m",
			})
		}
	}
	add(errs, model.SevError)
	add(warns, model.SevWarning)
	add(infos, model.SevInfo)
	return out
}