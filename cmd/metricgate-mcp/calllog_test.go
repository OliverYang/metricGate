package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"metricgate/internal/calllog"
	"metricgate/internal/model"
)

// TestNewCallLoggerIsOffUnlessAsked pins the default. A tool that starts
// recording what an agent touched without being asked has changed its own data
// posture, so every spelling of "no" must produce a nil logger.
func TestNewCallLoggerIsOffUnlessAsked(t *testing.T) {
	for _, setting := range []string{"", "  ", "0", "off", "OFF", "false", "no"} {
		if l := newCallLogger(setting); l != nil {
			t.Errorf("newCallLogger(%q) = %+v, want nil", setting, l)
		}
	}
	if l := newCallLogger("1"); l == nil || l.path != calllog.DefaultPath {
		t.Errorf("newCallLogger(\"1\") = %+v, want the default path %q", l, calllog.DefaultPath)
	}
	// An explicit path is honoured and its directory is created once, up front.
	path := filepath.Join(t.TempDir(), "nested", "calls.jsonl")
	l := newCallLogger(path)
	if l == nil || l.path != path {
		t.Fatalf("newCallLogger(%q) = %+v, want that path", path, l)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("directory was not created: %v", err)
	}
	if l.session == "" {
		t.Error("session id is empty; every record would group under one task")
	}
}

func TestRecordProjectUsesCanonicalGitRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package main"), 0o600); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	id, path, name := (&callLogger{}).recordProject(checkPathIn{Path: file})
	if id == "" || filepath.Clean(id) != filepath.Clean(path) || filepath.Clean(path) != filepath.Clean(canonicalRoot) || name != filepath.Base(canonicalRoot) {
		t.Fatalf("recordProject = (%q, %q, %q), want canonical repository identity", id, path, name)
	}
}

func TestRecordProjectLeavesVirtualAndRelativePathsUnassigned(t *testing.T) {
	logger := &callLogger{}
	for _, input := range []any{checkCodeIn{Name: "service.go", Code: "package service"}, checkPathIn{Path: "internal/service"}} {
		id, path, name := logger.recordProject(input)
		if id != "" || path != "" || name != "" {
			t.Errorf("recordProject(%+v) = (%q, %q, %q), want unassigned", input, id, path, name)
		}
	}
}
func TestLocatorReadsOnlyAllowListedFields(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"path only", constraintsIn{Path: "internal/engine"}, "path=internal/engine"},
		{"path and query", impactIn{Path: "internal/facts", Query: "Collect"}, "path=internal/facts query=Collect"},
		{"name, never code", checkCodeIn{Name: "service.go", Code: "func main() {}"}, "name=service.go"},
		{"empty", constraintsIn{}, ""},
		{"non-struct", "internal/engine", ""},
	}
	for _, tc := range cases {
		if got := locator(tc.in); got != tc.want {
			t.Errorf("%s: locator = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestWrapToolRecordsVerdictAndNeverSource is the load-bearing privacy test:
// the log must carry the verdict of a check and none of the code that produced
// it. If this ever fails, the log has become a store of agent-authored source.
func TestWrapToolRecordsVerdictAndNeverSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.jsonl")
	logger := &callLogger{path: path, session: "test-session"}
	const secret = "func Sneaky() { os.Exit(1) } // api_key=abc123"

	var h mcp.ToolHandlerFor[checkCodeIn, checkCodeOut] = func(context.Context, *mcp.CallToolRequest, checkCodeIn) (*mcp.CallToolResult, checkCodeOut, error) {
		return nil, checkCodeOut{
			Verdict:  "FAIL: fix the errors",
			Errors:   1,
			Findings: []model.Finding{{RuleID: "banned-api"}},
		}, nil
	}
	wrapped := wrapLogged(logger, "check_code", h)
	if _, _, err := wrapped(context.Background(), nil, checkCodeIn{Name: "service.go", Code: secret}); err != nil {
		t.Fatalf("wrapped handler: %v", err)
	}

	assertNoSourceInLog(t, path)
	assertCheckCallRecorded(t, readOneRecord(t, path))
}

// assertNoSourceInLog fails if any part of the code argument reached the file.
func assertNoSourceInLog(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(raw), "abc123") || strings.Contains(string(raw), "Sneaky") {
		t.Fatalf("the call log captured source code:\n%s", raw)
	}
}

func readOneRecord(t *testing.T, path string) calllog.Record {
	t.Helper()
	recs, skipped, err := calllog.Read(path)
	if err != nil || skipped != 0 || len(recs) != 1 {
		t.Fatalf("Read = %d records / %d skipped / %v, want exactly 1 record", len(recs), skipped, err)
	}
	return recs[0]
}

func assertCheckCallRecorded(t *testing.T, r calllog.Record) {
	t.Helper()
	if r.Tool != "check_code" || r.Phase != calllog.PhaseVerify || r.Session != "test-session" {
		t.Errorf("record = %+v, want tool/phase/session filled in", r)
	}
	if r.Target != "name=service.go" {
		t.Errorf("target = %q, want %q", r.Target, "name=service.go")
	}
	if r.Verdict != "FAIL: fix the errors" || r.Findings != 1 || r.Errors != 1 {
		t.Errorf("outcome = %+v, want the handler's verdict and counts", r)
	}
	if r.Passed() {
		t.Error("a FAIL verdict must not be recorded as a pass")
	}
}

// TestWrapToolDisabledWritesNothing: with logging off there is no file, no
// directory and no error — the wrapper is the identity function.
func TestWrapToolDisabledWritesNothing(t *testing.T) {
	dir := t.TempDir()
	var h mcp.ToolHandlerFor[checkPathIn, checkPathOut] = func(context.Context, *mcp.CallToolRequest, checkPathIn) (*mcp.CallToolResult, checkPathOut, error) {
		return nil, checkPathOut{Verdict: "PASS"}, nil
	}
	wrapped := wrapLogged(nil, "check_path", h)
	if _, out, err := wrapped(context.Background(), nil, checkPathIn{Path: "."}); err != nil || out.Verdict != "PASS" {
		t.Fatalf("wrapped = %+v, %v; want the handler's result", out, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("logging is off but %d files appeared: %v", len(entries), entries)
	}
}

// TestWrapToolRecordsErrorsAsErrors: an infrastructure failure must be visible
// in the summary, not silently counted as a passed check.
func TestWrapToolRecordsErrorsAsErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.jsonl")
	logger := &callLogger{path: path, session: "s"}
	boom := errors.New("engine: path does not exist")

	var h mcp.ToolHandlerFor[checkPathIn, checkPathOut] = func(context.Context, *mcp.CallToolRequest, checkPathIn) (*mcp.CallToolResult, checkPathOut, error) {
		return nil, checkPathOut{}, boom
	}
	if _, _, err := wrapLogged(logger, "check_path", h)(context.Background(), nil, checkPathIn{Path: "nope"}); !errors.Is(err, boom) {
		t.Fatalf("wrapped handler error = %v, want it passed through unchanged", err)
	}
	recs, _, err := calllog.Read(path)
	if err != nil || len(recs) != 1 {
		t.Fatalf("Read: %v, %d records", err, len(recs))
	}
	if recs[0].Err == "" || recs[0].Passed() {
		t.Errorf("record = %+v, want the error recorded and not scored as a pass", recs[0])
	}
}

// TestWrapToolSurvivesUnwritableLog: instrumentation must never be the reason
// a check fails, so a write error is swallowed.
func TestWrapToolSurvivesUnwritableLog(t *testing.T) {
	// A directory path can never be opened for append as a file.
	dir := t.TempDir()
	logger := &callLogger{path: dir, session: "s"}

	var h mcp.ToolHandlerFor[checkCodeIn, checkCodeOut] = func(context.Context, *mcp.CallToolRequest, checkCodeIn) (*mcp.CallToolResult, checkCodeOut, error) {
		return nil, checkCodeOut{Verdict: "PASS"}, nil
	}
	_, out, err := wrapLogged(logger, "check_code", h)(context.Background(), nil, checkCodeIn{Name: "a.go"})
	if err != nil {
		t.Fatalf("a log write failure must not surface as a tool error, got %v", err)
	}
	if out.Verdict != "PASS" {
		t.Errorf("verdict = %q, want the handler's own result", out.Verdict)
	}
}
