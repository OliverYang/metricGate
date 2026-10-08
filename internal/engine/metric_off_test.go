package engine

import (
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/metrics"
	"metricgate/internal/model"
)

// error: off must keep the warn level working and never produce an error
// verdict, even for values far above the builtin default error line.
func TestMetricVerdictHonorsOff(t *testing.T) {
	cfg, err := config.Load([]byte(`version: 1
languages:
  go:
    metrics:
      npath-complexity: { warn: 200, error: off }
`))
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	sev, threshold, ok := e.metricVerdict("go", metrics.MetricNPath, 3000)
	if !ok || sev != model.SevWarning || threshold != 200 {
		t.Errorf("value 3000 verdict = (%v,%d,%v), want (warning,200,true)", sev, threshold, ok)
	}
	// Below warn: nothing fires.
	if _, _, ok := e.metricVerdict("go", metrics.MetricNPath, 100); ok {
		t.Error("value 100 should not fire")
	}
}

// warn: off keeps only the error level; values between the default warn line
// and the error line must stay silent.
func TestMetricVerdictWarnOff(t *testing.T) {
	cfg, err := config.Load([]byte(`version: 1
languages:
  go:
    metrics:
      cyclomatic-complexity: { warn: off, error: 15 }
`))
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	if _, _, ok := e.metricVerdict("go", metrics.MetricCyclomatic, 12); ok {
		t.Error("value 12 (above default warn 10, below error 15) must stay silent with warn: off")
	}
	sev, threshold, ok := e.metricVerdict("go", metrics.MetricCyclomatic, 20)
	if !ok || sev != model.SevError || threshold != 15 {
		t.Errorf("value 20 verdict = (%v,%d,%v), want (error,15,true)", sev, threshold, ok)
	}
}

// EffectiveThresholds drives digest/policygap; verify the off resolution there.
func TestEffectiveThresholdsHonorsOff(t *testing.T) {
	cfg, err := config.Load([]byte(`version: 1
languages:
  go:
    metrics:
      npath-complexity: { warn: 200, error: off }
      fan-out: { warn: off, error: 25 }
`))
	if err != nil {
		t.Fatal(err)
	}
	warn, errAt := EffectiveThresholds(cfg, "go", "npath-complexity")
	if warn != 200 || errAt != 0 {
		t.Errorf("npath = (%d,%d), want (200,0)", warn, errAt)
	}
	warn, errAt = EffectiveThresholds(cfg, "go", "fan-out")
	if warn != 0 || errAt != 25 {
		t.Errorf("fan-out = (%d,%d), want (0,25)", warn, errAt)
	}
}
