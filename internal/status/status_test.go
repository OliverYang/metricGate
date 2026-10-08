package status

import (
	"strings"
	"testing"
	"time"

	"metricgate/internal/model"
	"metricgate/internal/rules"
)

func run() *model.RunSummary {
	return &model.RunSummary{
		Files:       10,
		Duration:    120 * time.Millisecond,
		ParseErrors: []string{"bad.java"},
		Findings: []model.Finding{
			{RuleID: "banned-fmt-Print*", Severity: model.SevError, File: "a.go"},
			{RuleID: "banned-fmt-Print*", Severity: model.SevWarning, File: "b.go"},
			{RuleID: "cyclomatic-complexity", Severity: model.SevError, Source: model.SrcMetric, File: "c.go"},
			{RuleID: "silent-catch", Severity: model.SevWarning, File: "d.java"},
			{RuleID: "engine-internal-only", Severity: model.SevInfo, File: "e.go"},
		},
		IgnoredByRule: map[string]int{"banned-os-exec": 2},
	}
}

func catalog() []rules.Meta {
	return []rules.Meta{
		{ID: "banned-fmt-Print*", Title: "print ban", Language: "go"},
		{ID: "cyclomatic-complexity", Title: "cyclomatic", Language: "go"},
		{ID: "silent-catch", Title: "silent catch", Language: "java"},
		{ID: "todo-comment", Title: "todo", Language: "go"},
		{ID: "import-cycle", Title: "cycle"},
	}
}

func TestBuildStates(t *testing.T) {
	s := run()
	// A java-rule finding would imply java WAS scanned; drop it so the na
	// branch is exercised consistently.
	var fs []model.Finding
	for _, f := range s.Findings {
		if f.RuleID != "silent-catch" {
			fs = append(fs, f)
		}
	}
	s.Findings = fs
	rep := Build(catalog(), []string{"go"}, s, model.SevError)
	st := map[string]RuleStatus{}
	for _, r := range rep.Rules {
		st[r.ID] = r
	}
	if got := st["banned-fmt-Print*"]; got.State != StateHit || got.Hits != 2 || got.Errors != 1 || got.Warnings != 1 {
		t.Errorf("hit rule = %+v", got)
	}
	if got := st["cyclomatic-complexity"]; got.State != StateHit || got.Hits != 1 {
		t.Errorf("metric hit = %+v", got)
	}
	// java was NOT scanned: silent-catch must be na, not silent.
	if got := st["silent-catch"]; got.State != StateNA {
		t.Errorf("java rule state = %s, want na", got.State)
	}
	// go WAS scanned: todo-comment zero hits is the actionable ambiguity.
	if got := st["todo-comment"]; got.State != StateSilent {
		t.Errorf("todo state = %s, want silent", got.State)
	}
	if got := st["import-cycle"]; got.State != StateSilent {
		t.Errorf("whole-graph rule state = %s, want silent", got.State)
	}
}

func TestBuildGateAndSuppression(t *testing.T) {
	rep := Build(catalog(), []string{"go", "java"}, run(), model.SevError)
	if rep.Gate != "fail" || rep.GateTrips != 2 || rep.Errors != 2 || rep.ParseErrors != 1 {
		t.Errorf("gate fail-on-error = %+v", rep)
	}
	warn := Build(catalog(), []string{"go", "java"}, run(), model.SevWarning)
	if warn.Gate != "fail" || warn.GateTrips != 4 {
		t.Errorf("gate fail-on-warning = %+v", warn)
	}
	clean := run()
	clean.Findings = nil
	pass := Build(catalog(), []string{"go"}, clean, model.SevError)
	if pass.Gate != "pass" {
		t.Errorf("gate pass = %+v", pass)
	}
}

func TestBuildUnmatchedNoteAndSuppressedCount(t *testing.T) {
	rep := Build(catalog(), []string{"go"}, run(), model.SevError)
	found := false
	for _, n := range rep.Notes {
		if strings.Contains(n, "engine-internal-only") {
			found = true
		}
	}
	if !found {
		t.Errorf("unmatched note missing: %v", rep.Notes)
	}
	// IgnoredByRule for an id not in the catalogue should NOT fabricate a hit
	// state on a catalogue rule; it is only attached when ids match.
	for _, r := range rep.Rules {
		if r.ID == "banned-os-exec" {
			t.Errorf("unknown rule materialized: %+v", r)
		}
	}
}

func TestBuildSuppressionAttachesToCatalogRule(t *testing.T) {
	s := run()
	s.Findings = nil
	s.IgnoredByRule = map[string]int{"todo-comment": 3}
	rep := Build(catalog(), []string{"go"}, s, model.SevError)
	var todo RuleStatus
	for _, r := range rep.Rules {
		if r.ID == "todo-comment" {
			todo = r
		}
	}
	if todo.Ignored != 3 || todo.State != StateSilent || todo.Hits != 0 {
		t.Errorf("suppressed rule = %+v", todo)
	}
}

func TestBuildSortOrder(t *testing.T) {
	rep := Build(catalog(), []string{"go", "java"}, run(), model.SevError)
	// Hits first, then silent, then na.
	sawNA := false
	for _, r := range rep.Rules {
		if r.State == StateNA {
			sawNA = true
		} else if sawNA {
			t.Errorf("state order broken at %s", r.ID)
		}
	}
}
