package report

import (
	"strings"
	"testing"

	"metricgate/internal/model"
	"metricgate/internal/rules"
)

func TestSARIFShape(t *testing.T) {
	s := &model.RunSummary{
		Files: 1,
		Findings: []model.Finding{{
			RuleID: "no-fmt-print", Severity: model.SevWarning, Source: model.SrcRule,
			File: "a.go", StartLine: 2, EndLine: 2, Message: "msg",
		}},
	}
	data, err := SARIF(s, "test", []rules.Meta{{ID: "no-fmt-print", Title: "Direct fmt.Print* call"}})
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
