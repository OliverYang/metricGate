package main

import (
	"context"
	"path/filepath"
	"testing"

	"metricgate/internal/calllog"
	"metricgate/internal/config"
	"metricgate/internal/model"
	"metricgate/internal/store"
)

// TestGetTraceReadsStoreAndLog exercises the MCP get_trace handler against a
// real store and call log, so the wiring — not just traceview — is covered.
func TestGetTraceReadsStoreAndLog(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "history.db")
	logPath := filepath.Join(dir, "calls.jsonl")
	seedMCPTrace(t, storePath, logPath)

	svc := &service{cfg: &config.Config{Store: config.StoreConfig{Enabled: true, Path: storePath}}}
	_, view, err := svc.getTrace(context.Background(), nil, traceIn{TraceID: "t1", LogPath: logPath})
	if err != nil {
		t.Fatal(err)
	}
	if view.TraceID != "t1" || len(view.Runs) != 1 || len(view.Calls) != 1 {
		t.Fatalf("view = %+v, want one run and one call", view)
	}
	if len(view.Attrs) != 1 || view.Attrs[0].Key != "model" {
		t.Errorf("Attrs = %+v, want model", view.Attrs)
	}
}

// TestGetTraceRequiresID: an empty trace id is a caller error, not an empty
// view, because "which trace?" has no default.
func TestGetTraceRequiresID(t *testing.T) {
	svc := &service{cfg: &config.Config{}}
	if _, _, err := svc.getTrace(context.Background(), nil, traceIn{}); err == nil {
		t.Fatal("empty trace_id must be an error")
	}
}

// TestGetTrendsScopesByModel: the MCP trends tool must honour the same -model
// scoping the CLI does, so the two surfaces agree.
func TestGetTrendsScopesByModel(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "history.db")
	seedMCPTrace(t, storePath, filepath.Join(dir, "calls.jsonl"))

	svc := &service{cfg: &config.Config{Store: config.StoreConfig{Enabled: true, Path: storePath}}}
	_, report, err := svc.getTrends(context.Background(), nil, trendsIn{StorePath: storePath, Model: "gpt-5"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Runs != 1 {
		t.Fatalf("Runs = %d, want 1 (only the gpt-5 trace)", report.Runs)
	}
}

// seedMCPTrace writes one run under trace t1 (model=gpt-5) and a decoy under
// t2, plus one call logged under t1.
func seedMCPTrace(t *testing.T, storePath, logPath string) {
	t.Helper()
	st, err := store.Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.SaveRunFull(&model.RunSummary{Files: 1}, "test", "abc", "/repo",
		store.RunMeta{TraceID: "t1", Attrs: []store.Attr{{Key: "model", Value: "gpt-5"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveRunFull(&model.RunSummary{Files: 1}, "test", "abc", "/repo",
		store.RunMeta{TraceID: "t2", Attrs: []store.Attr{{Key: "model", Value: "other"}}}); err != nil {
		t.Fatal(err)
	}
	if err := calllog.Append(logPath, calllog.Record{TraceID: "t1", Tool: "check_code", Verdict: "PASS"}); err != nil {
		t.Fatal(err)
	}
}
