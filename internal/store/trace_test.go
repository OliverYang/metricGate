package store

import (
	"testing"

	"metricgate/internal/model"
)

// TestTraceAttrsRoundTrip stores a run with attributes and reads them back.
func TestTraceAttrsRoundTrip(t *testing.T) {
	st := openTestStore(t)
	meta := RunMeta{TraceID: "trace-a", Attrs: []Attr{{Key: "model", Value: "gpt-5"}, {Key: "harness", Value: "codex"}}}
	if _, err := st.SaveRunFull(&model.RunSummary{Files: 1}, "test", "abc", "/repo", meta); err != nil {
		t.Fatal(err)
	}
	got, err := st.TraceAttrs("trace-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "harness" || got[1].Key != "model" {
		t.Fatalf("TraceAttrs = %+v, want harness then model", got)
	}
}

// A second run under the same trace updates an attribute rather than failing
// on the primary key: one task spans many runs, and the latest value wins.
func TestTraceAttrsUpsertAcrossRuns(t *testing.T) {
	st := openTestStore(t)
	save := func(modelName string) {
		t.Helper()
		meta := RunMeta{TraceID: "trace-a", Attrs: []Attr{{Key: "model", Value: modelName}}}
		if _, err := st.SaveRunFull(&model.RunSummary{Files: 1}, "test", "abc", "/repo", meta); err != nil {
			t.Fatal(err)
		}
	}
	save("gpt-5")
	save("gpt-5.1")
	got, err := st.TraceAttrs("trace-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Value != "gpt-5.1" {
		t.Fatalf("TraceAttrs = %+v, want model=gpt-5.1", got)
	}
}

// Attributes without a trace id are not stored: they describe a unit of work,
// and a row no report can join back to a run is noise.
func TestTraceAttrsNeedATrace(t *testing.T) {
	st := openTestStore(t)
	meta := RunMeta{Attrs: []Attr{{Key: "model", Value: "gpt-5"}}}
	if _, err := st.SaveRunFull(&model.RunSummary{Files: 1}, "test", "abc", "/repo", meta); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM trace_attrs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("trace_attrs rows = %d, want 0", n)
	}
}

// TestResolveTraceAttr maps an attribute value back to its trace ids.
func TestResolveTraceAttr(t *testing.T) {
	st := openTestStore(t)
	save := func(trace, modelName string) {
		t.Helper()
		meta := RunMeta{TraceID: trace, Attrs: []Attr{{Key: "model", Value: modelName}}}
		if _, err := st.SaveRunFull(&model.RunSummary{Files: 1}, "test", "abc", "/repo", meta); err != nil {
			t.Fatal(err)
		}
	}
	save("t1", "gpt-5")
	save("t2", "gpt-5")
	save("t3", "other")
	got, err := st.ResolveTraceAttr("model", "gpt-5")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "t1" || got[1] != "t2" {
		t.Fatalf("ResolveTraceAttr = %v, want [t1 t2]", got)
	}
}

// TestWindowTraceIDsScopesReports: a non-nil TraceIDs keeps only those runs,
// and an empty non-nil slice selects nothing (the "no trace matched" case that
// must not widen back to every run).
func TestWindowTraceIDsScopesReports(t *testing.T) {
	st := openTestStore(t)
	save := func(trace string) {
		t.Helper()
		meta := RunMeta{TraceID: trace}
		if _, err := st.SaveRunFull(&model.RunSummary{Files: 1}, "test", "abc", "/repo", meta); err != nil {
			t.Fatal(err)
		}
	}
	save("t1")
	save("t2")
	save("")

	scoped, err := st.LatestRunsIn(Window{TraceIDs: []string{"t1"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || scoped[0].TraceID != "t1" {
		t.Fatalf("scoped runs = %+v, want only t1", scoped)
	}

	none, err := st.LatestRunsIn(Window{TraceIDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("empty TraceIDs selected %d runs, want 0", len(none))
	}

	all, err := st.LatestRunsIn(Window{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("nil TraceIDs selected %d runs, want all 3", len(all))
	}
}
