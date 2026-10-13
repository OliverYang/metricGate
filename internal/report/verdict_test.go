package report

import (
	"encoding/json"
	"strings"
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/model"
)

func policy(t *testing.T, yaml string) *config.Config {
	t.Helper()
	cfg, err := config.Load([]byte(yaml))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// TestVerdictCountsWhatTheGateCounts: the verdict must carry the gate's own
// arithmetic, not a re-derivation. Under fail_on=warning, ledger-accepted
// warnings do not count toward a failure — so a verdict that reports them as
// trips makes a passing repo look red to every machine consumer.
func TestVerdictCountsWhatTheGateCounts(t *testing.T) {
	s := &model.RunSummary{Findings: []model.Finding{
		{RuleID: "conceded", Severity: model.SevWarning, Accepted: true},
	}}
	cfg := policy(t, "gate:\n  fail_on: warning\n")

	v := VerdictOf(s, cfg)
	if v.Trips != 0 {
		t.Errorf("trips = %d, want 0 (the conceded warning does not count)", v.Trips)
	}
	if !v.Passed {
		t.Error("passed = false, want true: the only warning is conceded")
	}
	if v.Accepted != 1 {
		t.Errorf("accepted = %d, want 1 (a consumer filtering findings loses it)", v.Accepted)
	}
	// Add one un-conceded warning and the verdict must flip: the concession is
	// per finding, not per run, so "the gate passed" cannot survive a new one.
	s.Findings = append(s.Findings, model.Finding{RuleID: "fresh", Severity: model.SevWarning})
	if open := VerdictOf(s, cfg); open.Passed || open.Trips != 1 {
		t.Errorf("open verdict = %+v, want failed with 1 trip", open)
	}

	// The same findings under the default threshold trip on the error-free
	// warning run, so the verdict cannot be a constant.
	strict := VerdictOf(s, policy(t, "gate:\n  fail_on: error\n"))
	if strict.Trips != 0 || !strict.Passed {
		t.Errorf("strict = %+v, want a clean pass under fail_on=error", strict)
	}
}

// TestEnvelopeCarriesTalliesAndVerdict pins the JSON shape a consumer parses:
// the tallies and the verdict sit beside the findings, and the findings
// themselves are unchanged.
func TestEnvelopeCarriesTalliesAndVerdict(t *testing.T) {
	s := &model.RunSummary{Findings: []model.Finding{
		{RuleID: "bad", Severity: model.SevError, File: "a.go", StartLine: 2},
	}}
	cfg := policy(t, "gate:\n  fail_on: error\n")
	data, err := JSONEnvelope(s, cfg)
	if err != nil {
		t.Fatalf("JSONEnvelope: %v", err)
	}
	var got struct {
		Errors   int     `json:"errors"`
		Warnings int     `json:"warnings"`
		Verdict  Verdict `json:"verdict"`
		Findings []struct {
			RuleID string `json:"ruleId"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Errors != 1 || got.Warnings != 0 {
		t.Errorf("tallies = %d/%d, want 1/0", got.Errors, got.Warnings)
	}
	if got.Verdict.Passed || got.Verdict.Trips != 1 {
		t.Errorf("verdict = %+v, want failed with 1 trip", got.Verdict)
	}
	if len(got.Findings) != 1 || got.Findings[0].RuleID != "bad" {
		t.Errorf("findings = %+v, want the untouched finding list", got.Findings)
	}
}

// TestVerdictReasonIsReadable keeps the log line honest: a verdict object is
// often printed straight into a log, and "true" explains nothing.
func TestVerdictReasonIsReadable(t *testing.T) {
	s := &model.RunSummary{Findings: []model.Finding{{RuleID: "x", Severity: model.SevError}}}
	if reason := VerdictOf(s, policy(t, "gate:\n  fail_on: error\n")).Reason; !strings.Contains(reason, "fail_on=error") {
		t.Errorf("reason = %q; want it to name the level", reason)
	}
}
