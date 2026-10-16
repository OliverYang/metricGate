package store

import (
	"path/filepath"
	"testing"

	"metricgate/internal/model"
)

// TestRunMeasuresByLangKeepsLanguagesApart pins the fix for the go/java blocks
// that rendered byte-identical values: metric_snapshots has no language column,
// so RunMeasures folds every file of a metric into one MAX and both language
// rows then display that one number. The per-language variant must derive the
// language from the filename and keep the two apart.
func TestRunMeasuresByLangKeepsLanguagesApart(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	runID, err := st.SaveRun(&model.RunSummary{Files: 3}, "test", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	// Same metric, three files, deliberately different values: the go block
	// must see 9, the java block 4, and the run-wide MAX must be 9 alone.
	seeds := []struct {
		file, metric string
		value        int
	}{
		{"pkg/alpha.go", "cyclomatic-complexity", 9},
		{"pkg/beta.go", "cyclomatic-complexity", 5},
		{"src/Alpha.java", "cyclomatic-complexity", 4},
	}
	for _, s := range seeds {
		if err := st.insertSnapshot(runID, s.file, "function", s.metric, s.value, 6, 12); err != nil {
			t.Fatalf("seed %s: %v", s.file, err)
		}
	}

	merged, err := st.RunMeasures(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 1 || merged[0].Worst != 9 {
		t.Fatalf("RunMeasures() = %+v, want one run-wide worst of 9", merged)
	}

	byLang, err := st.RunMeasuresByLang(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(byLang) != 2 {
		t.Fatalf("RunMeasuresByLang() = %d languages, want 2: %v", len(byLang), byLang)
	}
	wantWorst := map[string]int{"go": 9, "java": 4}
	for lang, want := range wantWorst {
		got, ok := worstMeasure(byLang[lang], "cyclomatic-complexity")
		if !ok || got != want {
			t.Errorf("%s worst(cyclomatic-complexity) = %d/%v, want %d (buckets: %v)", lang, got, ok, want, byLang)
		}
	}
	if byLang["go"][0].Worst == byLang["java"][0].Worst {
		t.Errorf("go and java collapse to one value (%d) — language derivation lost",
			byLang["go"][0].Worst)
	}
}

// worstMeasure picks one metric out of a language's measures. Extracted because
// three tests need the same search and an inline loop each time is duplication
// the clone detector would flag.
func worstMeasure(ms []RunMeasure, metric string) (int, bool) {
	for _, m := range ms {
		if m.Metric == metric {
			return m.Worst, true
		}
	}
	return 0, false
}

// TestRunMeasuresByLangDropsUnregisteredFiles: a file the parser has no
// language for must not be folded into some language's bucket. Nothing but the
// engine's own snapshots reaches this table, but the invariant is worth a
// failing test rather than a comment.
func TestRunMeasuresByLangDropsUnregisteredFiles(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	runID, err := st.SaveRun(&model.RunSummary{Files: 2}, "test", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.insertSnapshot(runID, "notes.txt", "file", "file-length", 4000, 400, 800); err != nil {
		t.Fatal(err)
	}
	if err := st.insertSnapshot(runID, "main.go", "file", "file-length", 12, 400, 800); err != nil {
		t.Fatal(err)
	}
	byLang, err := st.RunMeasuresByLang(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(byLang) != 1 || byLang["go"][0].Worst != 12 {
		t.Fatalf("RunMeasuresByLang() = %v, want only go with worst 12", byLang)
	}
	if _, leaked := byLang["txt"]; leaked {
		t.Errorf("unregistered extension produced a %q bucket: %v", "txt", byLang)
	}
}
