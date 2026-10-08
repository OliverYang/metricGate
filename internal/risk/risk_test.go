package risk

import (
	"encoding/json"
	"math"
	"testing"

	"metricgate/internal/metrics"
	"metricgate/internal/model"
)

func closeTo(got, want float64) bool { return math.Abs(got-want) < 1e-6 }

// one runs Compute over a single file's findings/debt and returns its score.
func one(t *testing.T, findings []model.Finding, mets []model.MetricResult) FileRisk {
	t.Helper()
	got := Compute(findings, mets, DefaultOptions())
	if len(got) != 1 {
		t.Fatalf("Compute returned %d files, want 1: %+v", len(got), got)
	}
	return got[0]
}

func TestSeverityWeightsShapeTheScore(t *testing.T) {
	// load=10 -> loadNorm=0.5 -> 100*0.7*0.5 = 35
	err := one(t, []model.Finding{{File: "a.go", Severity: model.SevError}}, nil)
	if !closeTo(err.Score, 35) {
		t.Errorf("1 error score = %.4f, want 35", err.Score)
	}
	// load=3 -> 3/13 -> 100*0.7*3/13
	warn := one(t, []model.Finding{{File: "a.go", Severity: model.SevWarning}}, nil)
	if !closeTo(warn.Score, 100*0.7*3.0/13.0) {
		t.Errorf("1 warning score = %.4f, want %.4f", warn.Score, 100*0.7*3.0/13.0)
	}
	// load=1 -> 1/11
	info := one(t, []model.Finding{{File: "a.go", Severity: model.SevInfo}}, nil)
	if !closeTo(info.Score, 100*0.7/11.0) {
		t.Errorf("1 info score = %.4f, want %.4f", info.Score, 100*0.7/11.0)
	}
	if !(err.Score > warn.Score && warn.Score > info.Score) {
		t.Errorf("severity must order the score: err=%.2f warn=%.2f info=%.2f", err.Score, warn.Score, info.Score)
	}
}

func TestDebtContributesItsShare(t *testing.T) {
	mets := []model.MetricResult{{Metric: metrics.MetricMaintainability, File: "a.go", Value: 100}}
	got := one(t, nil, mets)
	if !closeTo(got.Score, 30) || !closeTo(got.Debt, 100) {
		t.Errorf("debt-only = %+v, want score 30 debt 100", got)
	}
	// findings + debt combine: 0.7*0.5 + 0.3*1 = 0.65
	both := one(t, []model.Finding{{File: "a.go", Severity: model.SevError}}, mets)
	if !closeTo(both.Score, 65) {
		t.Errorf("error+debt score = %.4f, want 65", both.Score)
	}
}

func TestWorstDebtAndNonMIMetricsIgnored(t *testing.T) {
	mets := []model.MetricResult{
		{Metric: metrics.MetricCyclomatic, File: "a.go", Value: 999}, // not MI: ignored
		{Metric: metrics.MetricMaintainability, File: "a.go", Value: 40},
		{Metric: metrics.MetricMaintainability, File: "a.go", Value: 70}, // worst wins
	}
	got := one(t, []model.Finding{{File: "a.go", Severity: model.SevInfo}}, mets)
	if !closeTo(got.Debt, 70) {
		t.Errorf("debt = %.1f, want 70 (worst function)", got.Debt)
	}
}

func TestCleanFileIsOmitted(t *testing.T) {
	got := Compute([]model.Finding{{File: "a.go", Severity: model.SevInfo}}, nil, DefaultOptions())
	if len(got) != 1 || got[0].File != "a.go" {
		t.Fatalf("want only a.go, got %+v", got)
	}
	if n := len(Compute(nil, nil, DefaultOptions())); n != 0 {
		t.Errorf("no findings and no debt must yield nothing, got %d", n)
	}
}

// Nearly every file carries a little MI debt; a sub-1 score is noise, not a
// risk, so it is dropped rather than reported.
func TestSubFloorDebtIsNotARisk(t *testing.T) {
	// debt 2 -> 100*0.3*0.02 = 0.6, below the floor.
	mets := []model.MetricResult{{Metric: metrics.MetricMaintainability, File: "a.go", Value: 2}}
	if got := Compute(nil, mets, DefaultOptions()); len(got) != 0 {
		t.Errorf("debt 2 should be below MinScore, got %+v", got)
	}
	// debt 5 -> 1.5, at or above the floor.
	mets[0].Value = 5
	if got := Compute(nil, mets, DefaultOptions()); len(got) != 1 {
		t.Errorf("debt 5 should be reported, got %+v", got)
	}
}

func TestOrderIsScoreThenPath(t *testing.T) {
	findings := []model.Finding{
		{File: "b.go", Severity: model.SevWarning},
		{File: "a.go", Severity: model.SevError},
		{File: "c.go", Severity: model.SevError}, // ties with a.go on score
	}
	got := Compute(findings, nil, DefaultOptions())
	if len(got) != 3 {
		t.Fatalf("got %d files", len(got))
	}
	if got[0].File != "a.go" || got[1].File != "c.go" || got[2].File != "b.go" {
		t.Errorf("order = %s, %s, %s; want a.go, c.go, b.go", got[0].File, got[1].File, got[2].File)
	}
}

func TestPathsNormalizeToRunRoot(t *testing.T) {
	opt := DefaultOptions()
	opt.Root = "/repo"
	got := Compute([]model.Finding{{File: "/repo/internal/a.go", Severity: model.SevError}}, nil, opt)
	if len(got) != 1 || got[0].File != "internal/a.go" {
		t.Errorf("absolute path not normalised: %+v", got)
	}
}

func TestComputeIsDeterministic(t *testing.T) {
	findings := []model.Finding{
		{File: "b.go", Severity: model.SevWarning},
		{File: "a.go", Severity: model.SevError},
		{File: "a.go", Severity: model.SevInfo},
	}
	first, err := json.Marshal(Compute(findings, nil, DefaultOptions()))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := json.Marshal(Compute(findings, nil, DefaultOptions()))
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("run %d differs:\n%s\n%s", i, first, again)
		}
	}
}

func TestScoresFlattensRanking(t *testing.T) {
	risks := Compute([]model.Finding{{File: "a.go", Severity: model.SevError}}, nil, DefaultOptions())
	byFile := Scores(risks)
	if !closeTo(byFile["a.go"], 35) {
		t.Errorf("Scores = %v, want a.go=35", byFile)
	}
}
