package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"metricgate/internal/accept"
	"metricgate/internal/model"
)

// gateStub checks the one file it is given and reports a fixed error, so the
// preamble assertions do not depend on the real rule packs.
type gateStub struct{ errs int }

func (s gateStub) CheckSource(name string, src []byte) ([]model.Finding, error) {
	if s.errs == 0 {
		return nil, nil
	}
	return []model.Finding{{
		RuleID: "gate-test", Severity: model.SevError,
		StartLine: 3, Message: "stub finding",
	}}, nil
}

// newGateRepo creates a temp Git worktree containing one tracked Go file.
func newGateRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitCmd := gateGit(t, root)
	gitCmd("init", "-q", ".")
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd("add", ".")
	gitCmd("commit", "-qm", "init")
	return root
}

// gateGit returns a runner for git commands in one repo with a fixed test
// identity, so commits work in any environment.
func gateGit(t *testing.T, dir string) func(args ...string) {
	t.Helper()
	return func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// TestGatePreambleReportsChangedFiles: an edit after the last check produces
// an error preamble naming the file; a second call with no further edits
// produces none.
func TestGatePreambleReportsChangedFiles(t *testing.T) {
	root := newGateRepo(t)
	g := &gateWatcher{eng: gateStub{errs: 1}, root: root}

	path := filepath.Join(root, "a.go")
	if err := os.WriteFile(path, []byte("package a\n\nfunc bad() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// LoadChanges compares against HEAD, so the mtime filter is what makes the
	// edit visible; make sure the window start is strictly before the write.
	time.Sleep(10 * time.Millisecond)

	first := g.preamble()
	if first == "" {
		t.Fatal("first preamble: want a verdict for the edited file, got none")
	}
	if !strings.Contains(first, "a.go") || !strings.Contains(first, "1 error") {
		t.Fatalf("first preamble = %q; want a.go + 1 error", first)
	}

	if second := g.preamble(); second != "" {
		t.Fatalf("second preamble = %q; want empty (no edits since last call)", second)
	}
}

// TestGatePreambleCleanEditSilent: an edit that introduces no findings must
// not spam every response.
func TestGatePreambleCleanEditSilent(t *testing.T) {
	root := newGateRepo(t)
	g := &gateWatcher{eng: gateStub{}, root: root}

	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n\nfunc ok() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := g.preamble(); got != "" {
		t.Fatalf("preamble = %q; want empty for a clean edit", got)
	}
}

// TestGateUntrackedFileCovered: a brand-new file (untracked) must be checked
// like an edit — new files are exactly what an agent produces.
func TestGateUntrackedFileCovered(t *testing.T) {
	root := newGateRepo(t)
	g := &gateWatcher{eng: gateStub{errs: 1}, root: root}

	if err := os.WriteFile(filepath.Join(root, "new.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := g.preamble()
	if !strings.Contains(got, "new.go") {
		t.Fatalf("preamble = %q; want the untracked file covered", got)
	}
}

// TestGateNoRootStandsDown: outside a Git worktree the watcher must be inert,
// not erroring on every call.
func TestGateNoRootStandsDown(t *testing.T) {
	g := newGateWatcher(gateStub{})
	if g.root != "" {
		t.Skip("test run directory is inside a Git worktree; root resolution cannot fail here")
	}
	if got := g.preamble(); got != "" {
		t.Fatalf("preamble = %q; want empty outside a worktree", got)
	}
}

// TestGatePreambleNilWatcher: a nil watcher is the disabled state and must be
// safe to call.
func TestGatePreambleNilWatcher(t *testing.T) {
	var g *gateWatcher
	if got := g.preamble(); got != "" {
		t.Fatalf("preamble = %q; want empty for nil watcher", got)
	}
}

func TestCountOpenWarningsRespectsLedger(t *testing.T) {
	l := &accept.Ledger{Entries: []accept.Entry{{
		RuleID: "fan-out", File: "a.go", Line: 3, Owner: "o", Reason: "r",
	}}}
	findings := []model.Finding{
		{RuleID: "fan-out", File: "a.go", StartLine: 3, Severity: model.SevWarning}, // accepted
		{RuleID: "fan-out", File: "a.go", StartLine: 9, Severity: model.SevWarning}, // open
		{RuleID: "fan-out", File: "b.go", StartLine: 3, Severity: model.SevWarning}, // open (different file)
		{RuleID: "other", File: "a.go", StartLine: 3, Severity: model.SevWarning},   // open (different rule)
		{RuleID: "err", File: "a.go", StartLine: 3, Severity: model.SevError},       // error: never acceptable
	}
	if got := countOpenWarnings(findings, l); got != 3 {
		t.Fatalf("countOpenWarnings = %d, want 3", got)
	}
	if !findings[0].Accepted {
		t.Fatal("accepted finding not annotated")
	}
	// countOpenWarnings annotates in place, so rebuild a fresh slice for
	// the nil-ledger case (the annotation from the first call persists).
	fresh := []model.Finding{
		{RuleID: "fan-out", File: "a.go", StartLine: 3, Severity: model.SevWarning},
		{RuleID: "fan-out", File: "a.go", StartLine: 9, Severity: model.SevWarning},
		{RuleID: "fan-out", File: "b.go", StartLine: 3, Severity: model.SevWarning},
		{RuleID: "other", File: "a.go", StartLine: 3, Severity: model.SevWarning},
		{RuleID: "err", File: "a.go", StartLine: 3, Severity: model.SevError},
	}
	if got := countOpenWarnings(fresh, nil); got != 4 {
		t.Fatalf("nil ledger count = %d, want 4", got)
	}
}

// TestWrapGateKeepsTheHandlerPayload: a handler that returns a nil result
// delegates its text channel to the SDK, which serialises the Out value. The
// preamble must precede that payload, never replace it — otherwise the same
// query answers with a verdict on one call and with the answer on the next,
// depending only on whether a file changed in between.
func TestWrapGateKeepsTheHandlerPayload(t *testing.T) {
	root := newGateRepo(t)
	g := &gateWatcher{eng: gateStub{errs: 1}, root: root}

	path := filepath.Join(root, "a.go")
	if err := os.WriteFile(path, []byte("package a\n\nfunc bad() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// LoadChanges compares against HEAD, so the mtime filter is what makes the
	// edit visible; make sure the window start is strictly before the write.
	time.Sleep(10 * time.Millisecond)

	var h mcp.ToolHandlerFor[checkCodeIn, checkCodeOut] = func(context.Context, *mcp.CallToolRequest, checkCodeIn) (*mcp.CallToolResult, checkCodeOut, error) {
		return nil, checkCodeOut{Verdict: "PASS", Findings: []model.Finding{{RuleID: "banned-api"}}}, nil
	}
	res, _, err := wrapGate(g, h)(context.Background(), nil, checkCodeIn{Name: "a.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) != 2 {
		t.Fatalf("content blocks = %d, want 2 (preamble + serialised Out)", len(res.Content))
	}
	first, ok := res.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(first.Text, "1 error") {
		t.Fatalf("first block = %#v, want the gate preamble", res.Content[0])
	}
	second, ok := res.Content[1].(*mcp.TextContent)
	if !ok || !strings.Contains(second.Text, `"ruleId":"banned-api"`) {
		t.Fatalf("second block = %#v, want the handler's own payload", res.Content[1])
	}
}
