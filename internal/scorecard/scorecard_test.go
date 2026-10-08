package scorecard

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"metricgate/internal/diff"
	"metricgate/internal/model"
	"metricgate/internal/regress"
)

// regressID aliases the metric-regression rule id for readability below.
const regressID = regress.RuleID

func changed() map[string][]diff.Range {
	return map[string][]diff.Range{
		"a.go":      {{Start: 1, End: 10}},
		"b_test.go": {{Start: 1, End: 5}},
	}
}

// findings covers every family the card counts, plus one finding and one
// TODO on an untouched file that must not leak into any counter.
func findings() []model.Finding {
	f := func(ruleID string, src model.Source, file string) model.Finding {
		return model.Finding{RuleID: ruleID, Severity: model.SevWarning, Source: src, File: file, StartLine: 3}
	}
	return []model.Finding{
		f("layer-violation", model.SrcRule, "a.go"),
		f("import-cycle", model.SrcRule, "a.go"),
		f("forbid-internal-cmd", model.SrcRule, "a.go"),
		f("unknown-dependency", model.SrcRule, "a.go"),
		f("banned-Executors-new*", model.SrcRule, "a.go"),
		f("cyclomatic-complexity", model.SrcMetric, "a.go"),
		f(regressID, model.SrcMetric, "a.go"),
		f("function-clone", model.SrcRule, "a.go"),
		f("near-clone", model.SrcRule, "a.go"),
		f("todo-comment", model.SrcRule, "a.go"),
		f("print-stack-trace", model.SrcRule, "a.go"), // generic rule finding: tally only
		f("layer-violation", model.SrcRule, "untouched.go"),
		f("todo-comment", model.SrcRule, "untouched.go"),
	}
}

func summary(findings []model.Finding, parseErrors []string) *model.RunSummary {
	return &model.RunSummary{Findings: findings, ParseErrors: parseErrors}
}

func TestBuildCountsEveryFamilyAndStaysInScope(t *testing.T) {
	card := Build(summary(findings(), []string{"a.go:3:1: bad", "untouched.go:9:1: bad"}),
		Options{Changed: changed()})

	if card.ChangedFiles != 2 || card.ChangedLines != 15 {
		t.Errorf("files=%d lines=%d, want 2/15", card.ChangedFiles, card.ChangedLines)
	}
	if !card.TestsTouched {
		t.Error("b_test.go is changed, tests touched must be true")
	}
	if card.StructureViolations != 3 { // layer + cycle + forbid
		t.Errorf("structure = %d, want 3", card.StructureViolations)
	}
	if card.DependencyViolations != 1 {
		t.Errorf("dependency = %d, want 1", card.DependencyViolations)
	}
	if card.BannedAPICalls != 1 {
		t.Errorf("banned-api = %d, want 1", card.BannedAPICalls)
	}
	if card.ThresholdBreaches != 1 { // cyclomatic, not the regression
		t.Errorf("threshold = %d, want 1", card.ThresholdBreaches)
	}
	if card.MetricRegressions != 1 {
		t.Errorf("regression = %d, want 1", card.MetricRegressions)
	}
	if card.ClonedCode != 2 { // function-clone + near-clone
		t.Errorf("clone = %d, want 2", card.ClonedCode)
	}
	if card.IncompleteWork != 1 { // TODO in a.go, not the untouched one
		t.Errorf("incomplete-work = %d, want 1", card.IncompleteWork)
	}
	if card.ParseErrors != 1 { // only the a.go parse error is in scope
		t.Errorf("parse-error = %d, want 1", card.ParseErrors)
	}
}

func TestBuildIsDeterministicAndSortsTopRules(t *testing.T) {
	opt := Options{Changed: changed()}
	first := Build(summary(findings(), nil), opt)
	second := Build(summary(findings(), nil), opt)

	if len(first.TopRules) == 0 || len(first.TopRules) > 5 {
		t.Fatalf("top rules len = %d, want 1..5", len(first.TopRules))
	}
	for i := range first.TopRules {
		if first.TopRules[i] != second.TopRules[i] {
			t.Fatalf("top rules not deterministic: %v vs %v", first.TopRules, second.TopRules)
		}
		if i > 0 {
			prev, cur := first.TopRules[i-1], first.TopRules[i]
			if cur.Count > prev.Count || (cur.Count == prev.Count && cur.Rule < prev.Rule) {
				t.Fatalf("top rules order violated at %d: %v then %v", i, prev, cur)
			}
		}
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("cards differ: %+v vs %+v", first, second)
	}
}

func TestBuildEmptyDiffYieldsZeroCard(t *testing.T) {
	card := Build(summary(findings(), nil), Options{Changed: map[string][]diff.Range{}})
	if !reflect.DeepEqual(card, Card{}) {
		t.Fatalf("empty diff card = %+v, want zero", card)
	}
}

func TestBuildScanStubsFalseSkipsStubScan(t *testing.T) {
	// ScanStubs=false must not touch the filesystem: IncompleteWork comes
	// from findings alone.
	card := Build(summary(findings(), nil), Options{Changed: changed(), ScanStubs: false})
	if card.IncompleteWork != 1 {
		t.Errorf("incomplete-work = %d, want the finding-derived 1", card.IncompleteWork)
	}
}

func TestPrintTextAndJSON(t *testing.T) {
	card := Build(summary(findings(), nil), Options{Changed: changed()})

	var text bytes.Buffer
	Print(&text, card, false)
	out := text.String()
	for _, want := range []string{"scorecard (agent diff):", "files 2", "lines 15", "tests touched: yes", "structure 3", "top rules:"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}

	var js bytes.Buffer
	Print(&js, card, true)
	if !strings.Contains(js.String(), `"changedFiles": 2`) || !strings.Contains(js.String(), `"topRules"`) {
		t.Errorf("json output malformed:\n%s", js.String())
	}
}
