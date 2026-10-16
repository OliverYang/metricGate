package traceview

import (
	"path/filepath"
	"testing"
	"time"

	"metricgate/internal/calllog"
	"metricgate/internal/model"
	"metricgate/internal/store"
)

// TestCollectJoinsRunsAttrsAndCalls is the whole point of the package: one
// trace id must pull the run, its findings, the trace attributes and the MCP
// calls into a single view.
func TestCollectJoinsRunsAttrsAndCalls(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "history.db")
	seedTraceStore(t, storePath)

	logPath := filepath.Join(dir, "calls.jsonl")
	writeCall(t, logPath, calllog.Record{Time: time.Now(), Session: "s", TraceID: "trace-1", Tool: "check_code", Verdict: "PASS"})
	writeCall(t, logPath, calllog.Record{Time: time.Now(), Session: "s", TraceID: "trace-2", Tool: "check_code", Verdict: "FAILED"})
	writeCall(t, logPath, calllog.Record{Time: time.Now(), Session: "s", Tool: "check_code", Verdict: "PASS"})

	view := Collect(storePath, logPath, "trace-1")
	assertTraceView(t, view)
}

// seedTraceStore writes one run under trace-1 (with a finding and an attribute)
// and one under trace-2, so the view has a decoy to exclude.
func seedTraceStore(t *testing.T, storePath string) {
	t.Helper()
	st, err := store.Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	summary := &model.RunSummary{Files: 2, Findings: []model.Finding{
		{RuleID: "no-fmt-print", Severity: model.SevWarning, Source: model.SrcRule, File: "a.go", StartLine: 3, Message: "m"},
	}}
	meta := store.RunMeta{TraceID: "trace-1", Attrs: []store.Attr{{Key: "model", Value: "gpt-5"}}}
	if _, err := st.SaveRunFull(summary, "test", "abc", "/repo", meta); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveRunFull(&model.RunSummary{Files: 1}, "test", "abc", "/repo", store.RunMeta{TraceID: "trace-2"}); err != nil {
		t.Fatal(err)
	}
}

// assertTraceView checks that the join kept trace-1's run, its finding, its
// attribute and its call, and excluded the trace-2 decoys.
func assertTraceView(t *testing.T, view View) {
	t.Helper()
	if view.TraceID != "trace-1" {
		t.Errorf("TraceID = %q", view.TraceID)
	}
	assertTraceRuns(t, view)
	assertTraceCalls(t, view)
	if view.Totals.Runs != 1 || view.Totals.Calls != 1 || view.Totals.Warnings != 1 {
		t.Errorf("Totals = %+v", view.Totals)
	}
}

// assertTraceRuns checks the attribute and the run/finding half of the view.
func assertTraceRuns(t *testing.T, view View) {
	t.Helper()
	if len(view.Attrs) != 1 || view.Attrs[0].Value != "gpt-5" {
		t.Errorf("Attrs = %+v, want model=gpt-5", view.Attrs)
	}
	if len(view.Runs) != 1 || len(view.Runs[0].Findings) != 1 {
		t.Fatalf("Runs = %+v, want one run with one finding", view.Runs)
	}
	if view.Runs[0].Findings[0].RuleID != "no-fmt-print" {
		t.Errorf("finding = %+v", view.Runs[0].Findings[0])
	}
}

// assertTraceCalls checks that only trace-1's call survived the join.
func assertTraceCalls(t *testing.T, view View) {
	t.Helper()
	if len(view.Calls) != 1 || view.Calls[0].TraceID != "trace-1" {
		t.Fatalf("Calls = %+v, want only trace-1's call", view.Calls)
	}
}

// An unknown trace id is a legal empty answer, with non-nil lists so the JSON
// reads "[]" not "null".
func TestCollectUnknownTraceIsEmptyNotError(t *testing.T) {
	view := Collect("", "", "nope")
	if view.Runs == nil || view.Calls == nil || view.Attrs == nil {
		t.Errorf("lists must be non-nil: %+v", view)
	}
	if view.Totals.Runs != 0 || view.Totals.Calls != 0 {
		t.Errorf("Totals = %+v, want zero", view.Totals)
	}
}

func writeCall(t *testing.T, path string, r calllog.Record) {
	t.Helper()
	if err := calllog.Append(path, r); err != nil {
		t.Fatal(err)
	}
}
