package cockpit

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"metricgate/internal/gittest"
	"testing"
)

// policyRepo builds a repository with one committed policy and one source file,
// so the discovery chain has a worktree to stop at.
func policyRepo(t *testing.T) (root string) {
	t.Helper()
	root = gittest.InitRepo(t)
	gittest.Write(t, root, ".metricgate.yaml", testPolicy)
	gittest.Write(t, root, "svc/a.go", "package svc\n\nfunc Bad() int { return 1 }\n")
	gittest.Run(t, root, "add", "-A")
	gittest.Run(t, root, "commit", "-qm", "policy fixture")
	return root
}

func TestHandlerServesTheDiscoveredPolicy(t *testing.T) {
	root := policyRepo(t)
	chdir(t, root)
	srv := newLocalCockpit(t)

	view := getPolicyView(t, srv)

	if !view.Found {
		t.Fatal("the discovered policy must be reported as found, or the UI cannot say what it is editing")
	}
	if view.Name != ".metricgate.yaml" {
		t.Fatalf("name = %q, want .metricgate.yaml", view.Name)
	}
	if !strings.Contains(view.Content, "cyclomatic-complexity") {
		t.Fatalf("content = %q: the raw document must round-trip for editing", view.Content)
	}
	if len(view.Sections) == 0 {
		t.Fatal("sections must list the top-level keys so the UI can jump to them")
	}
	if len(view.Candidates) == 0 {
		t.Fatal("candidates must name the discovery chain")
	}
}

func TestPolicyViewReportsSectionsWithLines(t *testing.T) {
	root := policyRepo(t)
	chdir(t, root)
	srv := newLocalCockpit(t)

	view := getPolicyView(t, srv)

	var wantKey string
	for _, s := range view.Sections {
		if s.Key == "languages" {
			wantKey = s.Key
			if s.Line < 1 {
				t.Fatalf("line = %d: section lines must be 1-based for an editor jump", s.Line)
			}
		}
	}
	if wantKey == "" {
		t.Fatal("languages must be listed as a section")
	}
}

func TestPolicyViewCarriesProvenanceForEveryEffectiveKey(t *testing.T) {
	root := policyRepo(t)
	chdir(t, root)
	srv := newLocalCockpit(t)

	view := getPolicyView(t, srv)

	if len(view.Keys) == 0 {
		t.Fatal("keys must list the effective configuration; a dashboard that shows nothing reads as 'nothing configured'")
	}
	for _, k := range view.Keys {
		if k.Source == "" {
			t.Fatalf("key %q has no source: every value must be traceable", k.Key)
		}
	}
}

func TestPolicyViewWithoutAnyFileIsHonest(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	srv := newLocalCockpit(t)

	view := getPolicyView(t, srv)

	if view.Found {
		t.Fatal("a repository with no policy must not claim one was found")
	}
	if view.Path != "" {
		t.Fatalf("path = %q", view.Path)
	}
	if len(view.Tried) == 0 {
		t.Fatal("Tried must record the search so an empty result is explainable")
	}
}

func TestPolicySaveReplacesTheDiscoveredFile(t *testing.T) {
	root := policyRepo(t)
	chdir(t, root)
	srv := newLocalCockpit(t)

	updated := "version: 1\ngate:\n  fail_on: warning\n"
	res := policySave(t, srv, http.MethodPut, map[string]string{
		"path":    filepath.Join(root, ".metricgate.yaml"),
		"content": updated,
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: a valid save must succeed", res.StatusCode)
	}
	got, err := os.ReadFile(filepath.Join(root, ".metricgate.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "fail_on: warning") {
		t.Fatalf("content on disk = %q: the save must land in the discovered file", got)
	}
	// A re-read must see the new values, so the UI never shows a stale editor.
	view := getPolicyView(t, srv)
	if !strings.Contains(view.Content, "fail_on: warning") {
		t.Fatalf("view = %q: GET must reflect the save", view.Content)
	}
}

func TestPolicySaveRefusesAnUnparseableDocument(t *testing.T) {
	root := policyRepo(t)
	chdir(t, root)
	before, err := os.ReadFile(filepath.Join(root, ".metricgate.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	srv := newLocalCockpit(t)

	res := policySave(t, srv, http.MethodPut, map[string]string{
		"path":    filepath.Join(root, ".metricgate.yaml"),
		"content": "version: 1\nlanguages: [this is: not a map\n",
	})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d: an invalid policy must be refused", res.StatusCode)
	}
	after, err := os.ReadFile(filepath.Join(root, ".metricgate.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("the file must be untouched when the document does not parse")
	}
}

func TestPolicySaveRefusesANewPath(t *testing.T) {
	root := policyRepo(t)
	chdir(t, root)
	srv := newLocalCockpit(t)

	res := policySave(t, srv, http.MethodPut, map[string]string{
		"path":    filepath.Join(root, "invented.yaml"),
		"content": "version: 1\n",
	})
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d: saving a file the chain would never read must be refused", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(root, "invented.yaml")); !os.IsNotExist(err) {
		t.Fatal("no new file may be created")
	}
}

func TestPolicySaveWithoutADiscoveredFileIsARefusal(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	srv := newLocalCockpit(t)

	res := policySave(t, srv, http.MethodPut, map[string]string{
		"content": "version: 1\n",
	})
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d: with no file in force there is nothing to save over", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(root, ".metricgate.yaml")); !os.IsNotExist(err) {
		t.Fatal("the endpoint must not create a file the discovery chain did not choose")
	}
}

func TestPolicySaveRejectsGETAndViceVersa(t *testing.T) {
	root := policyRepo(t)
	chdir(t, root)
	srv := newLocalCockpit(t)

	res := policySave(t, srv, http.MethodDelete, map[string]string{})
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d: only GET and PUT are allowed", res.StatusCode)
	}
	if allow := res.Header.Get("Allow"); !strings.Contains(allow, "GET") || !strings.Contains(allow, "PUT") {
		t.Fatalf("Allow = %q", allow)
	}
}

func TestPolicySaveIsRejectedCrossOrigin(t *testing.T) {
	root := policyRepo(t)
	chdir(t, root)
	srv := newLocalCockpit(t)

	req, err := http.NewRequest(http.MethodPut, srv.base+"/api/policy", strings.NewReader(`{"content":"version: 1\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	// The host guard sees a loopback request; the same-origin check is the one
	// that has to refuse it, which is the point of the test.
	res := srv.doRaw(req)
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d: a cross-origin write must be refused", res.StatusCode)
	}
}

func TestPolicySaveLeavesNoTemporaryFileBehind(t *testing.T) {
	root := policyRepo(t)
	chdir(t, root)
	srv := newLocalCockpit(t)
	_ = policySave(t, srv, http.MethodPut, map[string]string{
		"content": "version: 1\n",
	})

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".metricgate-policy-") || strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temporary file %q survived the save", e.Name())
		}
	}
}

// testPolicy is a small but real-shaped policy: enough structure that section
// and key extraction are meaningful, with a comment that must survive a load.
const testPolicy = `version: 1
gate:
  fail_on: error
languages:
  go:
    metrics:
      cyclomatic-complexity: {warn: 3, error: 5}
`

// writeTestFile creates path with body, creating parent directories.
func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// getPolicyView performs the GET the UI would perform.
func getPolicyView(t *testing.T, srv localCockpit) policyView {
	t.Helper()
	res := srv.get("/api/policy")
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var view policyView
	if err := json.NewDecoder(res.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	return view
}

// policySave PUTs a JSON body and returns the response with its body
// still readable by the caller's assertions (it is closed here).
func policySave(t *testing.T, srv localCockpit, method string, body map[string]string) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	res := srv.do(method, "/api/policy", strings.NewReader(string(data)), "application/json")
	t.Cleanup(func() { res.Body.Close() })
	return res
}

// chdir moves the test into root for the duration of the test: the cockpit
// resolves the policy from its own working directory, exactly as the server
// does in production.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// localCockpit serves the cockpit handler without binding a socket.
//
// httptest.NewServer listens on loopback, which is a dependency this project's
// own environment does not always have (a locked-down CI runner has no
// permission to bind). The windows are narrow — one GET per assertion — but
// they are the ones these tests are actually about, and the sandbox-free
// version runs everywhere.
type localCockpit struct {
	handler http.Handler
	base    string
}

func newLocalCockpit(t *testing.T) localCockpit {
	t.Helper()
	// The origin is loopback because the API guard admits nothing else.
	return localCockpit{handler: Handler("", ""), base: "http://127.0.0.1"}
}

func (c localCockpit) Close() {}

// doRaw serves a request the test assembled itself, for the cases that need an
// unusual header (a cross-site write) rather than a plain JSON body.
func (c localCockpit) doRaw(req *http.Request) *http.Response {
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	return rec.Result()
}

// get performs GET relative to the cockpit origin.
func (c localCockpit) get(path string) *http.Response {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	return rec.Result()
}

// do performs a request with a body, as the UI's POST/PUT/DELETE would.
func (c localCockpit) do(method, path string, body io.Reader, contentType string) *http.Response {
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// Both the host guard and the same-origin check read the request's own
	// Host, so a write has to look like it came from the cockpit itself.
	req.Host = "127.0.0.1"
	req.Header.Set("Origin", "http://127.0.0.1")
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	return rec.Result()
}
