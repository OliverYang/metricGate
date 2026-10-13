package report

import (
	"strings"
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/model"
	"metricgate/internal/rules"
)

// testPolicy is the minimal policy the SARIF run properties are stamped from.
func testPolicy(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load([]byte("gate:\n  fail_on: error\n"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

func TestSARIFShape(t *testing.T) {
	s := &model.RunSummary{
		Files: 1,
		Findings: []model.Finding{{
			RuleID: "no-fmt-print", Severity: model.SevWarning, Source: model.SrcRule,
			File: "a.go", StartLine: 2, EndLine: 2, Message: "msg",
		}},
	}
	data, err := SARIF(s, "test", []rules.Meta{{ID: "no-fmt-print", Title: "Direct fmt.Print* call"}}, testPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{
		`"version": "2.1.0"`,
		`"ruleId": "no-fmt-print"`,
		`"level": "warning"`,
		`"uri": "a.go"`,
		`"startLine": 2`,
		`"source": "rule"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("SARIF output missing %s", want)
		}
	}
}

// TestSARIFCarriesEvidenceInProperties proves the anchor rides in
// result.properties.evidence and does not disturb SARIF locations.
func TestSARIFCarriesEvidenceInProperties(t *testing.T) {
	s := &model.RunSummary{
		Files: 1,
		Findings: []model.Finding{{
			RuleID: "empty-block", Severity: model.SevWarning, Source: model.SrcRule,
			File: "a.go", StartLine: 3, EndLine: 4, Message: "empty",
			Evidence: &model.Evidence{
				StartByte: 20, EndByte: 24,
				SnippetHash: "sha256:deadbeef", Anchor: model.AnchorExact,
			},
		}},
	}
	data, err := SARIF(s, "test", []rules.Meta{{ID: "empty-block", Title: "Empty block"}}, testPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	// Evidence lives in properties; the authoritative location is untouched.
	for _, want := range []string{
		`"evidence"`,
		`"startByte": 20`,
		`"endByte": 24`,
		`"snippetHash": "sha256:deadbeef"`,
		`"anchorStatus": "exact"`,
		`"startLine": 3`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("SARIF output missing %s", want)
		}
	}
}
