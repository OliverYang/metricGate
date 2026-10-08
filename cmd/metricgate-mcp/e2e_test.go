package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"metricgate/internal/calllog"
	"metricgate/internal/config"
	"metricgate/internal/engine"
)

// TestToolCallsAreRecordedEndToEnd drives the real server over an in-memory
// transport and then reads the log back. It is the test that makes "the MCP
// surface is instrumented" a checked property rather than a claim: it goes
// through registerTools, so a tool added without going through addTool would
// show up as a missing record here.
func TestToolCallsAreRecordedEndToEnd(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.jsonl")
	session := startSession(t, dir, logPath)

	driveSession(t, session, dir)

	recs := readLog(t, logPath)
	assertCallSequence(t, recs)
	assertVerifySummary(t, calllog.Summarize(recs))
}

// startSession boots the real server over an in-memory transport, wired the
// same way main wires it, and returns a connected client session.
func startSession(t *testing.T, dir, logPath string) *mcp.ClientSession {
	t.Helper()
	cfg := writeTestPolicy(t, dir)
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	t.Cleanup(func() { eng.Close() })

	svc := &service{eng: eng, cfg: cfg, log: newCallLogger(logPath)}
	if svc.log == nil {
		t.Fatal("call logging was not enabled for a path that was given")
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "metricgate", Version: "test"}, nil)
	registerTools(srv, svc)

	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	go func() { _ = srv.Run(ctx, serverTransport) }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).
		Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// writeTestPolicy drops a policy that bans os.Exit into dir, so a snippet
// calling it is a deterministic FAIL and the repaired snippet is a PASS.
func writeTestPolicy(t *testing.T, dir string) *config.Config {
	t.Helper()
	policyPath := filepath.Join(dir, "policy.yaml")
	policy := `version: 1
gate:
  fail_on: error
languages:
  go:
    banned-api:
      - match: os.Exit
        reason: return the error instead of exiting
`
	if err := os.WriteFile(policyPath, []byte(policy), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sample.go"), []byte("package sample\n\nfunc F() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(policyPath)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	return cfg
}

// driveSession walks one task through the loop the workflow doc describes:
// ground, generate something wrong, repair it, then gate the tree.
func driveSession(t *testing.T, session *mcp.ClientSession, dir string) {
	t.Helper()
	calls := []struct {
		tool string
		args map[string]any
	}{
		{"get_constraints", map[string]any{"path": dir}},
		{"check_code", map[string]any{
			"name": "x.go",
			"code": "package x\n\nimport \"os\"\n\nfunc F() { os.Exit(1) }\n",
		}},
		{"check_code", map[string]any{
			"name": "x.go",
			"code": "package x\n\nimport \"errors\"\n\nfunc F() error { return errors.New(\"boom\") }\n",
		}},
		{"check_path", map[string]any{"path": dir}},
	}
	for _, c := range calls {
		if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: c.tool, Arguments: c.args}); err != nil {
			t.Fatalf("call %s: %v", c.tool, err)
		}
	}
}

func readLog(t *testing.T, path string) []calllog.Record {
	t.Helper()
	recs, skipped, err := calllog.Read(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("skipped %d lines, want 0", skipped)
	}
	return recs
}

func assertCallSequence(t *testing.T, recs []calllog.Record) {
	t.Helper()
	want := []string{"get_constraints", "check_code", "check_code", "check_path"}
	if len(recs) != len(want) {
		t.Fatalf("recorded %d calls, want %d:\n%+v", len(recs), len(want), recs)
	}
	for i, tool := range want {
		if recs[i].Tool != tool {
			t.Errorf("record %d tool = %q, want %q", i, recs[i].Tool, tool)
		}
		if recs[i].Session == "" {
			t.Errorf("record %d has no session id", i)
		}
	}
}

func assertVerifySummary(t *testing.T, s calllog.Summary) {
	t.Helper()
	if s.ByPhase[string(calllog.PhaseBefore)] != 1 || s.ByPhase[string(calllog.PhaseVerify)] != 3 {
		t.Errorf("by_phase = %v, want 1 before / 3 verify", s.ByPhase)
	}
	v := s.Verify
	if v.Calls != 3 || v.Flagged != 1 || v.Clean != 2 {
		t.Fatalf("verify = %+v, want 3 calls / 1 flagged / 2 clean", v)
	}
	if v.Tasks != 1 || v.TasksFlagged != 1 || v.TasksResolved != 1 || v.ConvergeRate != 1 {
		t.Errorf("tasks = %+v, want one flagged task that converged", v)
	}
}
