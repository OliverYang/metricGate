package engine

import (
	"strings"
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/model"
)

// longTestFunc builds a Go test function with n statements so its function
// length crosses any threshold below n.
func longTestFunc(n int) string {
	var b strings.Builder
	b.WriteString("package pkg\n\nimport \"testing\"\n\nfunc TestLong(t *testing.T) {\n")
	for i := 0; i < n; i++ {
		b.WriteString("\tx := 1\n\t_ = x\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// Test files apply test_metrics baselines: a length that would flag under
// the regular threshold stays quiet under the relaxed test one, while the
// same length in a non-test file still flags. This is the replacement for
// excluding test files from measurement entirely.
func TestMetricFindingsUsesTestBaseline(t *testing.T) {
	cfg, err := config.Load([]byte(`version: 1
languages:
  go:
    metrics:
      function-length: { warn: 10, error: 30 }
    test_metrics:
      function-length: { warn: 100, error: 300 }
`))
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	// ~12 statements → length well above regular warn 10 but below the
	// regular error 30 and far below the test warn 100.
	src := []byte(longTestFunc(12))

	findings, err := e.CheckSource("helper.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if f := metricFinding(findings, "function-length"); f == nil {
		t.Fatal("non-test file must flag under the regular baseline")
	} else if f.Severity != model.SevWarning {
		t.Errorf("non-test severity = %v, want warning", f.Severity)
	}

	findings, err = e.CheckSource("helper_test.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if f := metricFinding(findings, "function-length"); f != nil {
		t.Errorf("test file must stay under the relaxed baseline, got %v", f.Message)
	}
}

// test_metrics can also disable a level entirely for tests (error: off) and
// can disable the metric for tests without touching production files.
func TestMetricFindingsTestBaselineOff(t *testing.T) {
	cfg, err := config.Load([]byte(`version: 1
languages:
  go:
    metrics:
      function-length: { warn: 10, error: 30 }
    test_metrics:
      function-length: { warn: off, error: off }
`))
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	src := []byte(longTestFunc(40)) // above regular error 30
	findings, err := e.CheckSource("big_test.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if f := metricFinding(findings, "function-length"); f != nil {
		t.Errorf("test baseline off must silence the metric, got %v", f.Message)
	}
}

// Without a test_metrics section the regular thresholds apply everywhere —
// no silent behavior change for policies that never mention test baselines.
func TestMetricFindingsTestBaselineAbsent(t *testing.T) {
	cfg, err := config.Load([]byte(`version: 1
languages:
  go:
    metrics:
      function-length: { warn: 10, error: 30 }
`))
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	src := []byte(longTestFunc(20))
	findings, err := e.CheckSource("helper_test.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if metricFinding(findings, "function-length") == nil {
		t.Fatal("no test_metrics section: regular thresholds must apply to test files too")
	}
}

// metricFinding returns the metric finding for one metric id, or nil.
func metricFinding(findings []model.Finding, metric string) *model.Finding {
	for i := range findings {
		if findings[i].RuleID == metric {
			return &findings[i]
		}
	}
	return nil
}
