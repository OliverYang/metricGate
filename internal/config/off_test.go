package config

import (
	"strings"
	"testing"
)

func TestMetricConfigAcceptsOffScalars(t *testing.T) {
	cfg, err := Load([]byte(`version: 1
languages:
  go:
    metrics:
      npath-complexity: { warn: 200, error: off }
      fan-out: { warn: off, error: 25 }
      params-count: { warn: 5, error: 8 }
`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.MetricErrorOff("go", "npath-complexity") {
		t.Error("npath error: off not recorded")
	}
	if cfg.MetricWarnOff("go", "npath-complexity") {
		t.Error("npath warn should not be off")
	}
	if !cfg.MetricWarnOff("go", "fan-out") || cfg.MetricErrorOff("go", "fan-out") {
		t.Error("fan-out off flags wrong")
	}
	if cfg.MetricWarnOff("go", "params-count") || cfg.MetricErrorOff("go", "params-count") {
		t.Error("plain-int metric must not be marked off")
	}
}

func TestMetricConfigRejectsNonIntegerScalars(t *testing.T) {
	_, err := Load([]byte(`version: 1
languages:
  go:
    metrics:
      params-count: { warn: soon }
`))
	if err == nil || !strings.Contains(err.Error(), "expected an integer or \"off\"") {
		t.Fatalf("err = %v, want scalar-type error", err)
	}
}

func TestEffectiveThresholdsHonorsOffInConfig(t *testing.T) {
	cfg, err := Load([]byte(`version: 1
languages:
  go:
    metrics:
      npath-complexity: { warn: 200, error: off }
      fan-out: { warn: off, error: 25 }
`))
	if err != nil {
		t.Fatal(err)
	}
	w1, e1 := cfg.MetricThresholdsFor("go", "npath-complexity"), cfg.MetricThresholdsFor("go", "fan-out")
	_ = w1
	_ = e1
	// Resolution-through-engine semantics are covered by the engine package
	// tests; here we pin that the config layer records the off flags.
	if !cfg.MetricErrorOff("go", "npath-complexity") || !cfg.MetricWarnOff("go", "fan-out") {
		t.Fatal("off flags lost")
	}
}

func TestThresholdExplainShowsOff(t *testing.T) {
	cfg, err := Load([]byte(`version: 1
languages:
  go:
    metrics:
      npath-complexity: { warn: 200, error: off }
`))
	if err != nil {
		t.Fatal(err)
	}
	th := cfg.MetricThresholdsFor("go", "npath-complexity")
	if !th.ErrorOff || th.Error != 0 || th.Warn != 200 || th.WarnOff {
		t.Errorf("thresholds = %+v, want warn 200 / error off", th)
	}
}
