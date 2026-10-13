package engine

import (
	"testing"

	"metricgate/internal/diff"
	"metricgate/internal/metrics"
	"metricgate/internal/model"
)

func TestFilterSummary(t *testing.T) {
	changed := map[string][]diff.Range{
		"a.go":     {{Start: 5, End: 7}},
		"new/b.go": {{Start: 1, End: 20}},
	}
	s := &model.RunSummary{
		Findings: []model.Finding{
			{RuleID: "r-old", File: "a.go", StartLine: 2, EndLine: 2},
			{RuleID: "r-new", File: "a.go", StartLine: 6, EndLine: 6},
			{RuleID: metrics.MetricCyclomatic, Source: model.SrcMetric, File: "a.go", StartLine: 1, EndLine: 10},
			{RuleID: metrics.MetricFileLength, Source: model.SrcMetric, File: "a.go", StartLine: 1, EndLine: 1},
			{RuleID: metrics.MetricFileLength, Source: model.SrcMetric, File: "new/b.go", StartLine: 1, EndLine: 1},
			{RuleID: "r-untouched", File: "other/c.go", StartLine: 1, EndLine: 1},
		},
		Metrics: []model.MetricResult{
			{Metric: metrics.MetricFileLength, File: "a.go", Value: 100},
			{Metric: metrics.MetricFileLength, File: "new/b.go", Value: 20},
		},
	}

	out, hidden := FilterSummary(s, changed)
	if hidden != 3 {
		t.Errorf("hidden = %d, want 3 (r-old, a.go file-length, r-untouched)", hidden)
	}

	got := map[string]bool{}
	for _, f := range out.Findings {
		got[f.RuleID+"/"+f.File] = true
	}
	for _, want := range []string{
		"r-new/a.go",                           // rule on a changed line
		metrics.MetricCyclomatic + "/a.go",     // touched function keeps its metric verdict
		metrics.MetricFileLength + "/new/b.go", // brand-new file reports file-length
	} {
		if !got[want] {
			t.Errorf("finding %q missing; kept = %v", want, got)
		}
	}
	if len(out.Findings) != 3 {
		t.Errorf("kept = %d findings, want 3", len(out.Findings))
	}
	// Metric snapshots must never be filtered.
	if len(out.Metrics) != 2 {
		t.Errorf("metrics = %d, want 2 (unfiltered)", len(out.Metrics))
	}
}

// TestFilterSummaryDropsUnchangedMetricValues pins the reason the base values
// exist: a class-level metric spans the whole class body, so editing any
// method inside it re-reports every CK value. Only a value that actually rose
// belongs to the change.
func TestFilterSummaryDropsUnchangedMetricValues(t *testing.T) {
	changed := map[string][]diff.Range{"a.go": {{Start: 40, End: 42}}}
	s := &model.RunSummary{
		Findings: []model.Finding{
			{RuleID: metrics.MetricCBO, Source: model.SrcMetric, File: "a.go", StartLine: 10, EndLine: 90, Message: `cbo of "Big" is 189 (threshold error 25)`},
			{RuleID: metrics.MetricWMC, Source: model.SrcMetric, File: "a.go", StartLine: 10, EndLine: 90, Message: `wmc of "Big" is 482 (threshold error 60)`},
			{RuleID: metrics.MetricCognitive, Source: model.SrcMetric, File: "a.go", StartLine: 38, EndLine: 60, Message: `cognitive-complexity of "hot" is 33 (threshold error 25)`},
			{RuleID: "todo-comment", File: "a.go", StartLine: 41, EndLine: 41},
		},
		Metrics: []model.MetricResult{
			{Metric: metrics.MetricCBO, File: "a.go", Scope: "Big", Value: 189, StartLine: 10, EndLine: 90},
			{Metric: metrics.MetricWMC, File: "a.go", Scope: "Big", Value: 482, StartLine: 10, EndLine: 90},
			{Metric: metrics.MetricCognitive, File: "a.go", Scope: "hot", Value: 33, StartLine: 38, EndLine: 60},
		},
	}
	base := IndexBaseValues([]model.MetricResult{
		{Metric: metrics.MetricCBO, File: "a.go", Scope: "Big", Value: 189},      // identical
		{Metric: metrics.MetricWMC, File: "a.go", Scope: "Big", Value: 478},      // rose 478 -> 482
		{Metric: metrics.MetricCognitive, File: "a.go", Scope: "hot", Value: 30}, // rose 30 -> 33
	})

	out, hidden, unchanged := FilterSummaryWithBase(s, changed, base)
	if hidden != 0 {
		t.Errorf("hidden = %d, want 0 (every finding intersects the change)", hidden)
	}
	if unchanged != 1 {
		t.Errorf("unchanged = %d, want 1 (cbo 189 did not move)", unchanged)
	}
	got := map[string]bool{}
	for _, f := range out.Findings {
		got[f.RuleID] = true
	}
	if got[metrics.MetricCBO] {
		t.Error("cbo was kept even though its value is identical to the base revision")
	}
	for _, want := range []string{metrics.MetricWMC, metrics.MetricCognitive, "todo-comment"} {
		if !got[want] {
			t.Errorf("finding %q missing; kept = %v", want, got)
		}
	}
	// Snapshots stay unfiltered so trends still record full measurements.
	if len(out.Metrics) != 3 {
		t.Errorf("metrics = %d, want 3 (unfiltered)", len(out.Metrics))
	}
}

// TestFilterSummaryKeepsNewScopes guards the other direction: a scope with no
// base value cannot have regressed, but it is also the only evidence that
// new code crossed a threshold, so it must survive.
func TestFilterSummaryKeepsNewScopes(t *testing.T) {
	changed := map[string][]diff.Range{"a.go": {{Start: 1, End: 5}}}
	s := &model.RunSummary{
		Findings: []model.Finding{
			{RuleID: metrics.MetricCyclomatic, Source: model.SrcMetric, File: "a.go", StartLine: 2, EndLine: 4, Message: `cyclomatic-complexity of "fresh" is 20 (threshold error 15)`},
		},
		Metrics: []model.MetricResult{
			{Metric: metrics.MetricCyclomatic, File: "a.go", Scope: "fresh", Value: 20, StartLine: 2, EndLine: 4},
		},
	}
	base := IndexBaseValues([]model.MetricResult{
		{Metric: metrics.MetricCyclomatic, File: "a.go", Scope: "other", Value: 3},
	})
	out, hidden, unchanged := FilterSummaryWithBase(s, changed, base)
	if hidden != 0 || unchanged != 0 || len(out.Findings) != 1 {
		t.Fatalf("hidden=%d unchanged=%d kept=%d, want 0/0/1", hidden, unchanged, len(out.Findings))
	}
}

// TestFilterSummaryNilBaseKeepsEverything pins the degradation: a caller with
// no base tree (the MCP server) must keep the old line-range behaviour rather
// than silently drop metric verdicts it cannot judge.
func TestFilterSummaryNilBaseKeepsEverything(t *testing.T) {
	changed := map[string][]diff.Range{"a.go": {{Start: 40, End: 42}}}
	s := &model.RunSummary{
		Findings: []model.Finding{
			{RuleID: metrics.MetricCBO, Source: model.SrcMetric, File: "a.go", StartLine: 10, EndLine: 90},
		},
		Metrics: []model.MetricResult{
			{Metric: metrics.MetricCBO, File: "a.go", Scope: "Big", Value: 189, StartLine: 10, EndLine: 90},
		},
	}
	out, _, unchanged := FilterSummaryWithBase(s, changed, nil)
	if unchanged != 0 || len(out.Findings) != 1 {
		t.Fatalf("unchanged=%d kept=%d, want 0/1 with no base values", unchanged, len(out.Findings))
	}
}

// TestSplitSummaryReturnsTheGroupsNotJustTheSizes pins the contract the CLI
// -hidden flag depends on: the same verdicts FilterSummaryWithBase counts must
// be handed back as lists, so "0 new findings" can be expanded instead of
// trusted. Counting and listing must never disagree.
func TestSplitSummaryReturnsTheGroupsNotJustTheSizes(t *testing.T) {
	changed := map[string][]diff.Range{"a.go": {{Start: 40, End: 42}}}
	s := &model.RunSummary{
		Findings: []model.Finding{
			{RuleID: metrics.MetricCBO, Source: model.SrcMetric, File: "a.go", StartLine: 10, EndLine: 90},
			{RuleID: metrics.MetricWMC, Source: model.SrcMetric, File: "a.go", StartLine: 10, EndLine: 90},
			{RuleID: "todo-comment", File: "b.go", StartLine: 3, EndLine: 3},
		},
		Metrics: []model.MetricResult{
			{Metric: metrics.MetricCBO, File: "a.go", Scope: "Big", Value: 189, StartLine: 10, EndLine: 90},
			{Metric: metrics.MetricWMC, File: "a.go", Scope: "Big", Value: 482, StartLine: 10, EndLine: 90},
		},
	}
	base := IndexBaseValues([]model.MetricResult{
		{Metric: metrics.MetricCBO, File: "a.go", Scope: "Big", Value: 189},
		{Metric: metrics.MetricWMC, File: "a.go", Scope: "Big", Value: 478},
	})

	split := SplitSummary(s, changed, base)
	if len(split.Kept) != 1 || split.Kept[0].RuleID != metrics.MetricWMC {
		t.Errorf("kept = %v, want [wmc]", split.Kept)
	}
	if len(split.PreExisting) != 1 || split.PreExisting[0].File != "b.go" {
		t.Errorf("pre-existing = %v, want [b.go todo-comment]", split.PreExisting)
	}
	if len(split.Unchanged) != 1 || split.Unchanged[0].RuleID != metrics.MetricCBO {
		t.Errorf("unchanged = %v, want [cbo]", split.Unchanged)
	}
	// The wrapper's counts are derived from the same groups, so they must
	// agree by construction — this is the assertion that would fail if one
	// path were recomputed independently.
	out, hidden, unchanged := FilterSummaryWithBase(s, changed, base)
	if hidden != len(split.PreExisting) || unchanged != len(split.Unchanged) {
		t.Errorf("wrapper counts hidden=%d unchanged=%d, want %d/%d (the split's sizes)", hidden, unchanged, len(split.PreExisting), len(split.Unchanged))
	}
	if len(out.Findings) != len(split.Kept) {
		t.Errorf("wrapper kept %d findings, want %d", len(out.Findings), len(split.Kept))
	}
	if len(out.Metrics) != 2 {
		t.Errorf("metrics = %d, want 2 (unfiltered)", len(out.Metrics))
	}
}
