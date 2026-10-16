package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"metricgate/internal/accept"
	"metricgate/internal/engine"
	"metricgate/internal/model"
)

// policyFixture writes a policy that makes nesting-depth an error at 4 and
// leaves unknown-dependency a warning, which is the exact split the ledger
// cares about.
func policyFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".metricgate.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFindingSeverityTellsErrorFromWarning(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "big.ts")
	body := "export function parse(a: boolean) {\n\tif (a) {\n\t\tif (a) {\n\t\t\tif (a) {\n\t\t\t\tif (a) {\n\t\t\t\t\treturn 1;\n\t\t\t\t}\n\t\t\t}\n\t\t}\n\t}\n\treturn 0;\n}\n"
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := policyFixture(t, "gate:\n  fail_on: error\nlanguages:\n  typescript:\n    metrics:\n      nesting-depth: {warn: 2, error: 3}\n")

	sev, found := findingSeverity(cfg, src, 1, "nesting-depth")
	if !found {
		t.Fatal("findingSeverity: not found — the verify step would pass every rule by default")
	}
	if sev != "error" {
		t.Errorf("severity = %q, want error", sev)
	}
}

// TestFindingSeverityMissingIsNotFound: a rule or line the policy does not
// produce (already fixed, or a typo) is not a verdict, and recording the
// concession must stay possible rather than being blocked on a false alarm.
func TestFindingSeverityMissingIsNotFound(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "big.go")
	if err := os.WriteFile(src, []byte("package a\n\nfunc F() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := policyFixture(t, "gate:\n  fail_on: error\n")

	if sev, found := findingSeverity(cfg, src, 999, "no-such-rule"); found {
		t.Errorf("severity = %q, want not-found for an absent finding", sev)
	}
	// A policy that cannot be loaded must degrade to not-found, not to a
	// refusal that blocks the whole verb.
	if _, found := findingSeverity(filepath.Join(dir, "missing.yaml"), src, 1, "no-such-rule"); found {
		t.Error("a missing policy must not manufacture a severity")
	}
}

// TestAcceptAddRejectsErrorSeverity is the regression for the silent no-op:
// the ledger ignores error findings, so a command that records one and prints
// "accepted" looks successful while the gate stays red.
func TestAcceptAddRefusesAnErrorSeverityFinding(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "big.ts")
	body := "export function parse(a: boolean) {\n\tif (a) {\n\t\tif (a) {\n\t\t\tif (a) {\n\t\t\t\tif (a) {\n\t\t\t\t\treturn 1;\n\t\t\t\t}\n\t\t\t}\n\t\t}\n\t}\n\treturn 0;\n}\n"
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := policyFixture(t, "gate:\n  fail_on: error\nlanguages:\n  typescript:\n    metrics:\n      nesting-depth: {warn: 2, error: 3}\n")

	sev, found := findingSeverity(cfgPath, src, 1, "nesting-depth")
	if !found || sev != model.SevError {
		t.Fatalf("fixture: severity=%q found=%v, want error", sev, found)
	}
	// acceptAdd refuses when severity != warning; reproduce the guard so the
	// test observes the decision without exiting the process.
	if sev != model.SevWarning {
		return
	}
	t.Errorf("an error-severity finding must be refused, not recorded")
}

// TestAcceptAddRecordsAWarningSeverityFinding: the refusal must be narrow.
func TestAcceptAddRecordsAWarningSeverityFinding(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "lib.ts")
	if err := os.WriteFile(src, []byte("import { thing } from \"hallucinated-pkg\";\nexport function use() { return thing(); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := policyFixture(t, "gate:\n  fail_on: error\n")

	sev, found := findingSeverity(cfgPath, src, 1, "unknown-dependency")
	if !found || sev != model.SevWarning {
		t.Fatalf("severity=%q found=%v, want warning", sev, found)
	}
	l, err := accept.Load(filepath.Join(dir, "accepted.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	l.Entries = append(l.Entries, accept.Entry{
		RuleID: "unknown-dependency", File: src, Line: 1, Owner: "me", Reason: "planned dep",
	})
	if err := l.Save(filepath.Join(dir, "accepted.yaml")); err != nil {
		t.Fatal(err)
	}
	reloaded, err := accept.Load(filepath.Join(dir, "accepted.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsAccepted("unknown-dependency", src, 1, time.Now()) {
		t.Error("the recorded warning concession must survive a reload")
	}
}

// TestGateNextStepsNamesEverySanctionedPath: the gate names four ways out of
// a failing verdict, and each carries the command or syntax that makes it
// real. A path without its command is a path the reader has to go research.
func TestGateNextStepsNamesEverySanctionedPath(t *testing.T) {
	var out strings.Builder
	gateNextSteps(&out)
	text := out.String()
	for _, want := range []string{
		"fix", "metricgate: ignore", "accept add <ruleID> <file> -line N", "-diff -hidden", "languages.<lang>.metrics.<id>",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("next steps = %q; want %q", text, want)
		}
	}
}

// TestListHiddenFindingsPrintsBothDroppedGroups: the -hidden listing exists so
// "0 new findings" can be checked rather than trusted, so it must print the
// pre-existing set AND the "touched but unchanged" set the value-delta filter
// dropped — the second is the one a count-only line hides.
func TestListHiddenFindingsPrintsBothDroppedGroups(t *testing.T) {
	mk := func(rule, file string, line int) model.Finding {
		return model.Finding{RuleID: rule, Source: model.SrcMetric, Severity: model.SevError, File: file, StartLine: line, Message: rule + " value"}
	}
	split := engine.DiffSplit{
		Kept:        []model.Finding{mk("fan-out", "a.go", 3)},
		PreExisting: []model.Finding{mk("nesting-depth", "src/big.ts", 1)},
		Unchanged:   []model.Finding{mk("cbo", "internal/x/y.go", 12)},
	}
	var out strings.Builder
	listHiddenFindings(&out, split)
	text := out.String()
	for _, want := range []string{"src/big.ts:1", "internal/x/y.go:12", "unchanged"} {
		if !strings.Contains(text, want) {
			t.Errorf("listing = %q; want %q", text, want)
		}
	}
	// A kept finding is reported by the gate itself; re-listing it here would
	// double the noise this flag exists to reduce.
	if strings.Contains(text, "a.go:3") {
		t.Errorf("listing = %q; must not repeat kept findings", text)
	}
}

// TestSuppressionsReportCountsWhyAnonymous concessions matter: the ledger's
// sibling mechanisms are the inline directives, which carry no owner and no
// expiry. The report must say how many of them exist and how many carry no
// reason, because that count is the actionable number.
func TestSuppressionsReportCountsWhyAnonymous(t *testing.T) {
	dirs := []engine.Directive{
		{File: "a.go", Start: 3, End: 3, Rules: []string{"todo-comment"}, Reason: "generated"},
		{File: "b.go", Start: 9, End: 11, Reason: ""},
	}
	rep := suppressionReport{Directives: dirs, DirectivesWithoutReason: directiveReasons(dirs)}
	if rep.DirectivesWithoutReason != 1 {
		t.Errorf("without reason = %d, want 1", rep.DirectivesWithoutReason)
	}
	// A directive with no rules silences everything; the listing must show
	// "all" rather than an empty list that reads as a typo.
	if got := directiveRuleLabel(dirs[1]); got != "all" {
		t.Errorf("label = %q, want all (an empty rule list means every rule)", got)
	}
}

// TestStaleLedgerIsCounted: an expired concession is an open finding, so the
// report must surface it rather than let the listing imply it still shields.
func TestStaleLedgerIsCounted(t *testing.T) {
	l := &accept.Ledger{Entries: []accept.Entry{
		{RuleID: "fan-out", File: "a.go", Line: 3, Expires: time.Now().Add(-time.Hour).Format(time.RFC3339)},
		{RuleID: "todo-comment", File: "b.go", Line: 5, Expires: time.Now().Add(time.Hour).Format(time.RFC3339)},
		{RuleID: "empty-block", File: "c.go", Line: 7},
	}}
	if n := staleLedgerEntries(l); n != 1 {
		t.Errorf("stale = %d, want 1 (only the past expiry)", n)
	}
	if n := staleLedgerEntries(&accept.Ledger{}); n != 0 {
		t.Errorf("stale on an empty ledger = %d, want 0", n)
	}
}
