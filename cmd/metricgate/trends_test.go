package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"metricgate/internal/model"
	"metricgate/internal/store"
)

// TestResolveTraceScope covers the three flags that turn a trace attribute into
// a window scope: nil when nothing was asked (every run), and a concrete set of
// ids otherwise.
func TestResolveTraceScope(t *testing.T) {
	st := openTrendStore(t)
	saveTraceRun(t, st, "t1", "gpt-5", "sha256:aa")
	saveTraceRun(t, st, "t2", "gpt-5", "sha256:bb")
	saveTraceRun(t, st, "t3", "other", "sha256:aa")

	cases := []struct {
		name string
		f    trendsFlags
		want []string
	}{
		{"none set means every trace", trendsFlags{}, nil},
		{"explicit trace", trendsFlags{trace: "t2"}, []string{"t2"}},
		{"model resolves to its traces", trendsFlags{model: "gpt-5"}, []string{"t1", "t2"}},
		{"prompt digest resolves to its traces", trendsFlags{promptDigest: "sha256:aa"}, []string{"t1", "t3"}},
		{"union of model and digest", trendsFlags{model: "other", promptDigest: "sha256:aa"}, []string{"t1", "t3"}},
		{"no match is an empty non-nil set", trendsFlags{model: "nope"}, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveTraceScope(tc.f, st)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("resolveTraceScope(%+v) = %v, want %v", tc.f, got, tc.want)
			}
		})
	}
}

// TestTrendsTraceScopeFiltersTheReport: a -model filter must narrow the report
// to the matching runs, and a non-matching one must answer "no runs" rather
// than silently widening back to everything.
func TestTrendsTraceScopeFiltersTheReport(t *testing.T) {
	st := openTrendStore(t)
	saveTraceRun(t, st, "t1", "gpt-5", "")
	saveTraceRun(t, st, "t2", "other", "")

	ids := resolveTraceScope(trendsFlags{model: "gpt-5"}, st)
	report, err := st.ReportIn(store.Window{TraceIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	if report.Runs != 1 {
		t.Fatalf("Runs = %d, want 1 (only t1 matches)", report.Runs)
	}

	none := resolveTraceScope(trendsFlags{model: "nope"}, st)
	empty, err := st.ReportIn(store.Window{TraceIDs: none})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Runs != 0 {
		t.Fatalf("Runs = %d, want 0 for a non-matching filter", empty.Runs)
	}
}

func openTrendStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// saveTraceRun stores one run under a trace, with optional model/prompt attrs.
func saveTraceRun(t *testing.T, st *store.Store, trace, modelName, prompt string) {
	t.Helper()
	meta := store.RunMeta{TraceID: trace}
	if modelName != "" {
		meta.Attrs = append(meta.Attrs, store.Attr{Key: "model", Value: modelName})
	}
	if prompt != "" {
		meta.Attrs = append(meta.Attrs, store.Attr{Key: "prompt_digest", Value: prompt})
	}
	if _, err := st.SaveRunFull(&model.RunSummary{Files: 1}, "test", "abc", "/repo", meta); err != nil {
		t.Fatal(err)
	}
}
