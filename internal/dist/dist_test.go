package dist

import (
	"encoding/json"
	"strconv"
	"testing"

	"metricgate/internal/model"
)

func TestSummarizeUsesNearestRank(t *testing.T) {
	// 1..10 sorted: p50 -> rank 5 -> 5; p90 -> rank 9 -> 9; p95 -> rank 10 -> 10.
	values := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	got := Summarize("cyclomatic-complexity", values)
	if got.Count != 10 || got.Min != 1 || got.Max != 10 {
		t.Errorf("count/min/max = %d/%d/%d, want 10/1/10", got.Count, got.Min, got.Max)
	}
	if got.P50 != 5 || got.P90 != 9 || got.P95 != 10 {
		t.Errorf("p50/p90/p95 = %d/%d/%d, want 5/9/10", got.P50, got.P90, got.P95)
	}
	if got.Mean != 5.5 {
		t.Errorf("mean = %.2f, want 5.5", got.Mean)
	}
}

// The input slice must not be mutated: callers hand over snapshots they still
// use, and a hidden in-place sort would make the result order-dependent.
func TestSummarizeDoesNotMutateInput(t *testing.T) {
	values := []int{3, 1, 2}
	Summarize("m", values)
	if values[0] != 3 || values[1] != 1 || values[2] != 2 {
		t.Errorf("input was reordered: %v", values)
	}
}

func TestSummarizeEmpty(t *testing.T) {
	got := Summarize("m", nil)
	if got.Count != 0 || got.P50 != 0 || got.Max != 0 || got.Mean != 0 {
		t.Errorf("empty summary = %+v, want all zero", got)
	}
}

func TestTopBreaksTiesByFileThenLine(t *testing.T) {
	scopes := []Scope{
		{File: "c.go", Line: 1, Value: 5},
		{File: "a.go", Line: 9, Value: 5},
		{File: "a.go", Line: 3, Value: 5},
		{File: "z.go", Line: 1, Value: 1},
	}
	got := Top(scopes, 3)
	want := []string{"a.go:3", "a.go:9", "c.go:1"}
	for i, w := range want {
		label := got[i].File + ":" + strconv.Itoa(got[i].Line)
		if label != w {
			t.Errorf("top[%d] = %s, want %s", i, label, w)
		}
	}
}

func TestTopAllWhenNIsZero(t *testing.T) {
	scopes := []Scope{{File: "a.go", Value: 1}, {File: "b.go", Value: 2}}
	if got := Top(scopes, 0); len(got) != 2 {
		t.Errorf("Top(n=0) returned %d scopes, want all 2", len(got))
	}
}

// Reports with topN <= 0 must leave the top lists out entirely, which is how
// `stats -top 0` asks for the distribution table alone.
func TestReportsOmitsTopWhenNIsZero(t *testing.T) {
	mets := []model.MetricResult{{Metric: "m", File: "a.go", Value: 3}}
	got := Reports(mets, 0)
	if len(got) != 1 || got[0].Count != 1 {
		t.Fatalf("reports = %+v, want one report", got)
	}
	if len(got[0].Top) != 0 {
		t.Errorf("top = %+v, want none", got[0].Top)
	}
}

func TestReportsGroupsAndSortsByMetric(t *testing.T) {
	mets := []model.MetricResult{
		{Metric: "b-metric", File: "x.go", Value: 4},
		{Metric: "a-metric", File: "y.go", Scope: "Fn", StartLine: 7, Value: 9},
		{Metric: "a-metric", File: "x.go", Value: 2},
	}
	got := Reports(mets, 1)
	if len(got) != 2 || got[0].Metric != "a-metric" || got[1].Metric != "b-metric" {
		t.Fatalf("reports = %+v, want a-metric then b-metric", got)
	}
	if got[0].Count != 2 || len(got[0].Top) != 1 {
		t.Fatalf("a-metric = %+v, want count 2 and topN 1", got[0])
	}
	top := got[0].Top[0]
	if top.File != "y.go" || top.Scope != "Fn" || top.Line != 7 || top.Value != 9 {
		t.Errorf("a-metric top = %+v, want y.go:7 Fn = 9", top)
	}
}

func TestReportsIsDeterministic(t *testing.T) {
	mets := []model.MetricResult{
		{Metric: "m", File: "b.go", Value: 3},
		{Metric: "m", File: "a.go", Value: 3},
		{Metric: "n", File: "c.go", Value: 1},
	}
	first, err := json.Marshal(Reports(mets, 2))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := json.Marshal(Reports(mets, 2))
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("run %d differs:\n%s\n%s", i, first, again)
		}
	}
}
