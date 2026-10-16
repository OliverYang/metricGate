package cockpit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"metricgate/internal/calllog"
	"metricgate/internal/model"
	"metricgate/internal/store"
)

// The fixture-driven render test proves the templates agree with a hand-written
// copy of the payloads. It cannot prove they agree with the server, and that gap
// is where this cockpit has repeatedly broken: a field renamed in Go, a list key
// spelled two ways, a payload the browser reads under another name.
//
// So this test removes the fixture. It asks the frontend what it would request,
// answers from the real handlers over a seeded store, and hands the answers back
// to the frontend's own fetch layer and views. What it asserts is that a
// payload's rows reach the document, so a rename on either side shows up as a
// count of zero.
//
// Node is a development dependency only; this skips when it is absent, the same
// way TestRenderAllViews does, and nothing in the release path runs it.

func TestCockpitRendersThePayloadsTheServerActuallySends(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; live-payload render coverage is skipped (dev-only check)")
	}
	logPath, dbPath := seedLiveCockpit(t)
	runLiveRender(t, node, Handler(logPath, dbPath))
}

// runLiveRender drives the dev renderer until the frontend stops finding new
// endpoints to ask for.
//
// One round is not enough: the load-time requests are known, but a delegated
// click on the sort dropdown asks for a page nobody predicted. Answering only
// the first set would make the interaction assertions fail for the wrong reason
// — an unanswered request rather than a broken page — so the request set is
// iterated to a fixed point against the real handlers.
func runLiveRender(t *testing.T, node string, handler http.Handler) {
	t.Helper()
	payloads := capturePayloads(t, node, handler)
	payloadFile := filepath.Join(t.TempDir(), "payloads.json")

	for round := 0; round < captureRounds; round++ {
		if err := writePayloads(payloadFile, payloads); err != nil {
			t.Fatal(err)
		}
		out, runErr := runSmokeChecked(t, node, payloadFile)
		// Requests are harvested whether or not the run passed: the failures
		// worth retrying are exactly the ones where something new was asked for.
		added := fillMissingRequests(t, handler, payloads, requestedPaths(out))
		if runErr == nil {
			// Only the summary: the REQUEST lines are the capture's channel, not
			// something a passing run should print into the test log.
			t.Logf("%s", smokeSummary(out))
			return
		}
		if added == 0 {
			t.Fatalf("live-payload render failed:\n%s\nendpoints captured:\n%s",
				out, strings.Join(sortedKeys(payloads), "\n"))
		}
	}
	t.Fatalf("the frontend kept requesting new endpoints over %d rounds; the capture never reached a fixed point",
		captureRounds)
}

// smokeSummary pulls the one line the driver prints about what it covered.
func smokeSummary(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "smoke:") {
			return strings.TrimSpace(line)
		}
	}
	return strings.TrimSpace(out)
}

// captureRounds bounds the fixed-point loop. Two suffice today: the load-time
// requests and the one a sort change adds. The bound is here so a frontend that
// requests endlessly fails this test instead of hanging it.
const captureRounds = 4

// fillMissingRequests answers every request the run made that the capture has
// not served yet, and reports how many were new.
func fillMissingRequests(t *testing.T, handler http.Handler, payloads map[string]string, requests []string) int {
	t.Helper()
	added := 0
	for _, path := range requests {
		if _, ok := payloads[path]; ok {
			continue
		}
		payloads[path] = liveGet(t, handler, path)
		added++
	}
	return added
}

func writePayloads(path string, payloads map[string]string) error {
	encoded, err := json.Marshal(payloads)
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o600)
}

// capturePayloads answers the requests the frontend says it will make, straight
// from the handlers the server mounts.
//
// The request list comes from the frontend rather than from this test, because
// an endpoint renamed on one side, or a parameter the other side stopped
// sending, is invisible to a fixture that was written next to the code reading
// it. A path here that no handler answers fails the capture, not the page.
func capturePayloads(t *testing.T, node string, handler http.Handler) map[string]string {
	t.Helper()
	requests := requestedPaths(runSmoke(t, node, "--probe"))
	if len(requests) == 0 {
		t.Fatal("the frontend requested nothing, so the driver is not driving it")
	}
	payloads := map[string]string{}
	for _, path := range requests {
		payloads[path] = liveGet(t, handler, path)
	}
	// A code window is reachable only by the fingerprint the store produced, so
	// it is read out of the issues payload rather than invented here.
	sourcePath := "/api/source?fingerprint=" + firstFingerprint(t, payloads)
	if _, ok := payloads[sourcePath]; !ok {
		payloads[sourcePath] = liveGet(t, handler, sourcePath)
	}
	return payloads
}

// seedLiveCockpit builds the state a first real installation has: a Git worktree
// with a finding-producing run in the store and calls in the log that point at
// it. It is seeded rather than mocked because the dashboard's project grouping
// reads the filesystem, and a mock would test the frontend against the mock's
// shape instead of the server's.
func seedLiveCockpit(t *testing.T) (logPath, dbPath string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "alpha")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(repo, "engine.go")
	if err := os.WriteFile(source, []byte(liveSourceFile()), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath = filepath.Join(filepath.Dir(repo), "history.db")
	seedLiveRun(t, dbPath, repo, source)
	logPath = filepath.Join(filepath.Dir(repo), "calls.jsonl")
	seedLiveCalls(t, logPath, repo, source)
	return logPath, dbPath
}

// liveSourceFile is a file whose middle lines are keyword lines, so the code
// window's highlight classes are observable rather than incidental.
func liveSourceFile() string {
	return strings.Repeat("// filler\n", 5) +
		"func loop() {\n\tfor i := 0; i < 12; i++ {\n\t\tprintln(i)\n\t}\n}\n" +
		strings.Repeat("// trailing\n", 5)
}

// seedLiveRun stores one run carrying both an exactly-anchored metric finding
// and a line-only rule finding: the two anchor shapes the triage list and the
// code window treat differently.
func seedLiveRun(t *testing.T, dbPath, repo, source string) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	summary := &model.RunSummary{
		Files: 2,
		Findings: []model.Finding{
			{RuleID: "cyclomatic-complexity", Severity: model.SevError, Source: model.SrcMetric,
				File: source, StartLine: 7, StartCol: 1, EndLine: 7, EndCol: 12,
				Message:  "complexity 18 exceeds 15",
				Evidence: &model.Evidence{Anchor: model.AnchorExact, SnippetHash: "liveHash"}},
			{RuleID: "todo-comment", Severity: model.SevWarning, Source: model.SrcRule,
				File: source, StartLine: 12, Message: "TODO: tighten the loop"},
		},
	}
	if _, err := st.SaveRunForProject(summary, "v-test", "cafe1234", repo); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

// seedLiveCalls writes the trace the loop leaves behind: one grounding call and
// one verify call that did not pass, which is the pair the cockpit exists to put
// next to each other.
func seedLiveCalls(t *testing.T, logPath, repo, source string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	for _, rec := range []calllog.Record{
		{Time: now, Session: "s1", TraceID: "t1", Tool: "check_code",
			Phase: calllog.PhaseVerify, Target: "path=" + source + " store=check",
			ProjectPath: repo, MS: 42, Findings: 2, Errors: 1, Warnings: 1, Verdict: "FAIL"},
		{Time: now.Add(-time.Minute), Session: "s2", TraceID: "t2", Tool: "get_constraints",
			Phase: calllog.PhaseBefore, Target: "path=" + repo + " query=go",
			ProjectPath: repo, MS: 7, Verdict: "PASS"},
	} {
		if err := calllog.Append(logPath, rec); err != nil {
			t.Fatal(err)
		}
	}
}

// liveGet serves one request through the real handler and returns its body,
// failing the test on anything other than a 200: an endpoint the frontend asks
// for must exist, and a redirect or a 404 is that contract breaking.
func liveGet(t *testing.T, handler http.Handler, path string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	// The loopback guard reads Host, and the cockpit is only ever served on a
	// loopback address, so the request has to look like one.
	req.Host = "127.0.0.1:7788"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// runSmoke drives the dev renderer inside the embedded web directory. The
// script name is prepended here rather than at each call site because node
// reads a leading "--probe" as its own flag and refuses to start.
func runSmoke(t *testing.T, node string, args ...string) string {
	t.Helper()
	out, _ := runSmokeChecked(t, node, args...)
	return out
}

func runSmokeChecked(t *testing.T, node string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(node, append([]string{"smoke.mjs"}, args...)...)
	cmd.Dir = "web"
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// requestedPaths reads the REQUEST lines the probe printed.
func requestedPaths(probe string) []string {
	var out []string
	for _, line := range strings.Split(probe, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "REQUEST "); ok {
			out = append(out, rest)
		}
	}
	return out
}

// firstFingerprint digs one fingerprint out of the issues payload.
func firstFingerprint(t *testing.T, payloads map[string]string) string {
	t.Helper()
	for path, body := range payloads {
		if !strings.HasPrefix(path, "/api/issues") {
			continue
		}
		var doc struct {
			Issues []struct {
				Fingerprint string `json:"fingerprint"`
			} `json:"issues"`
		}
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if len(doc.Issues) == 0 {
			t.Fatal("the seeded store returned no issues, so there is nothing to check")
		}
		return doc.Issues[0].Fingerprint
	}
	t.Fatal("the frontend never asked for /api/issues, so the triage list is not wired")
	return ""
}

func sortedKeys(payloads map[string]string) []string {
	keys := make([]string, 0, len(payloads))
	for k := range payloads {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
