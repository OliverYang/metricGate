package cockpit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metricgate/internal/model"
	"metricgate/internal/store"
)

// The code window has three outcomes, and only two of them used to be tested:
// a window, and a file that is gone. The third is a file that is there but
// yields no lines — the window is empty, and the reader has to be told that
// rather than shown a frame with "第 0-0 行 / 共 0 行" over an empty list.
//
// The state is reachable from a real run: a file that shrank to nothing since
// the finding was recorded, or an empty file the engine still measured.

// seededEmptySource seeds a finding against a file that exists and is empty.
func seededEmptySource(t *testing.T) (SourceView, string) {
	t.Helper()
	root := t.TempDir()
	abs := filepath.Join(root, "emptied.go")
	if err := os.WriteFile(abs, nil, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return seedAndOpen(t, root, abs, 1)
}

// seedAndOpen stores one run whose single finding points at file, then resolves
// that finding through the source view. Both seeding tests differ only in the
// file they point at, so the setup lives here once: npath counts the product of
// a function's branches, and a second copy of this setup pushed the counter past
// its warning threshold without adding a second case.
func seedAndOpen(t *testing.T, root, file string, startLine int) (SourceView, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	summary := &model.RunSummary{
		Files: 1,
		Findings: []model.Finding{
			{RuleID: "r", Severity: model.SevError, Source: model.SrcMetric,
				File: file, StartLine: startLine, Message: "m"},
		},
	}
	if _, err := st.SaveRunForProject(summary, "test", "c", root); err != nil {
		t.Fatalf("SaveRunForProject: %v", err)
	}
	rows, err := st.IssuesForQuery(store.IssueQuery{Window: store.Window{Limit: 10}}, nil)
	if err != nil {
		t.Fatalf("IssuesForQuery: %v", err)
	}
	if len(rows.Issues) != 1 {
		t.Fatalf("seeded %d rows, want 1", len(rows.Issues))
	}
	fingerprint := rows.Issues[0].Fingerprint
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	view, err := sourceView(dbPath, fingerprint)
	if err != nil {
		t.Fatalf("sourceView: %v", err)
	}
	return view, fingerprint
}

// An empty file is an empty window, not a failure. Reporting a reason here
// would send the reader looking for a moved file; reporting zero lines with no
// notice would draw a plausible-looking blank viewer.
func TestSourceAPIEmptyFileIsAnEmptyWindowNotAReason(t *testing.T) {
	view, _ := seededEmptySource(t)
	if view.Reason != "" {
		t.Errorf("reason = %q, want none: the file was read, it is simply empty", view.Reason)
	}
	if len(view.Lines) != 0 {
		t.Errorf("%d lines from an empty file, want 0", len(view.Lines))
	}
	if view.TotalLines != 0 {
		t.Errorf("totalLines = %d, want 0", view.TotalLines)
	}
	if view.StartLine != 0 || view.EndLine != 0 {
		t.Errorf("window %d..%d, want 0..0 so the header cannot claim a range", view.StartLine, view.EndLine)
	}
	if view.Language != "go" {
		t.Errorf("language = %q, want go: an empty file still has a name", view.Language)
	}
}

// The engine records paths relative to the project root it scanned, so a window
// that only accepts absolute paths opens for almost no real finding. The
// regression is invisible to a fixture that seeds an absolute path (which is
// what the empty-file test above does), so this one seeds the relative shape a
// real run produces and asserts the file is actually read.
func seededRelativeSource(t *testing.T) (SourceView, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "alpha.go"),
		[]byte("package pkg\n\nfunc Alpha() {}\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return seedAndOpen(t, root, "pkg/alpha.go", 3)
}

// A relative finding resolves against the project root of the run that recorded
// it. Without that join the code window is empty for every real run and the
// operator sees "finding has no readable file path" on a file sitting right
// there.
func TestSourceAPIResolvesRelativePathAgainstRunProjectRoot(t *testing.T) {
	view, root := seededRelativeSource(t)
	if view.Reason != "" {
		t.Fatalf("reason = %q, want none: %s/pkg/alpha.go exists", view.Reason, root)
	}
	if len(view.Lines) == 0 {
		t.Fatalf("0 lines, want the file's 3 lines")
	}
	if view.TotalLines != 3 {
		t.Errorf("totalLines = %d, want 3", view.TotalLines)
	}
	if view.Language != "go" {
		t.Errorf("language = %q, want go", view.Language)
	}
}

// A relative path with no project root to anchor it stays a reason, not a
// silent read of whatever file happens to sit at that path relative to the
// server's working directory. Guessing here would show the operator a
// different repository's file under the finding's name.
func TestSourceAPIUnanchoredRelativePathIsAReason(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	// Run 0 does not exist, so there is no root to join against.
	if _, err := resolveSourcePath(st, store.IssueRow{File: "pkg/alpha.go"}); err == nil {
		t.Errorf("resolveSourcePath = nil error for a run-less relative path; want a reason")
	}
}

// The empty window has to survive the wire as an empty list, because the view
// tests `lines` to choose the empty state. `null` would work by accident —
// every falsy value does — but a payload that omits the key entirely would be
// indistinguishable from an older server that never sent lines at all.
func TestSourceAPIEmptyWindowSendsAnEmptyList(t *testing.T) {
	view, _ := seededEmptySource(t)
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"lines":[]`) {
		t.Errorf("encoded window = %s, want \"lines\":[] so the client's emptiness test is on data", encoded)
	}
}
