package facts

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/model"
)

func writeGo(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// twoFileRepo writes a.go (defines Core) and b.go (calls it), so the ranking
// has a direction: a.go must come first.
func twoFileRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeGo(t, dir, "a.go", "package p\n\nfunc Core() {}\n")
	writeGo(t, dir, "b.go", "package p\n\nfunc Use() { Core() }\n")
	return dir
}

func defaultConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestRankedFilesRanksReferencedFileFirst(t *testing.T) {
	dir := twoFileRepo(t)
	scores, err := RankedFiles(dir, defaultConfig(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 2 || scores[0].File != "a.go" {
		t.Fatalf("ranking = %+v, want a.go first", scores)
	}
	for _, s := range scores {
		if s.Risk != 0 {
			t.Errorf("nil findings must leave risk at zero, got %s risk=%.1f", s.File, s.Risk)
		}
	}
}

// The finding path is normalised against the same root as the map, so an
// absolute run root still joins to the repo-relative ranking key.
func TestRankedFilesJoinsRiskAcrossAbsoluteRoot(t *testing.T) {
	dir := twoFileRepo(t)
	findings := []model.Finding{{File: filepath.Join(dir, "a.go"), Severity: model.SevError}}
	scores, err := RankedFiles(dir, defaultConfig(t), findings, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 2 {
		t.Fatalf("got %d files", len(scores))
	}
	byFile := map[string]float64{}
	for _, s := range scores {
		byFile[s.File] = s.Risk
	}
	// 1 error -> 100*0.7*(10/20) = 35
	if math.Abs(byFile["a.go"]-35) > 1e-6 {
		t.Errorf("a.go risk = %.2f, want 35", byFile["a.go"])
	}
	if byFile["b.go"] != 0 {
		t.Errorf("b.go risk = %.2f, want 0", byFile["b.go"])
	}
}

func TestRankedFilesErrorsWhenNothingSelected(t *testing.T) {
	_, err := RankedFiles(t.TempDir(), defaultConfig(t), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no supported source files") {
		t.Fatalf("err = %v, want a no-files error", err)
	}
}
