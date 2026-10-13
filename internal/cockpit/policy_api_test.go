package cockpit

import (
	"encoding/json"
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
	srv := httptest.NewServer(Handler("", ""))

	view := getPolicyView(t, srv.URL)

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
	srv := httptest.NewServer(Handler("", ""))

	view := getPolicyView(t, srv.URL)

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
	srv := httptest.NewServer(Handler("", ""))

	view := getPolicyView(t, srv.URL)

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
	srv := httptest.NewServer(Handler("", ""))

	view := getPolicyView(t, srv.URL)

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
	srv := httptest.NewServer(Handler("", ""))

	updated := "version: 1\ngate:\n  fail_on: warning\n"
	res := policySave(t, srv.URL, http.MethodPut, map[string]string{
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
	view := getPolicyView(t, srv.URL)
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
	srv := httptest.NewServer(Handler("", ""))

	res := policySave(t, srv.URL, http.MethodPut, map[string]string{
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
	srv := httptest.NewServer(Handler("", ""))

	res := policySave(t, srv.URL, http.MethodPut, map[string]string{
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
	srv := httptest.NewServer(Handler("", ""))

	res := policySave(t, srv.URL, http.MethodPut, map[string]string{
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
	srv := httptest.NewServer(Handler("", ""))

	res := policySave(t, srv.URL, http.MethodDelete, map[string]string{})
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
	srv := httptest.NewServer(Handler("", ""))

	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/policy", strings.NewReader(`{"content":"version: 1\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d: a cross-origin write must be refused", res.StatusCode)
	}
}

func TestPolicySaveLeavesNoTemporaryFileBehind(t *testing.T) {
	root := policyRepo(t)
	chdir(t, root)
	srv := httptest.NewServer(Handler("", ""))
	_ = policySave(t, srv.URL, http.MethodPut, map[string]string{
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
func getPolicyView(t *testing.T, base string) policyView {
	t.Helper()
	res, err := http.Get(base + "/api/policy")
	if err != nil {
		t.Fatal(err)
	}
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
func policySave(t *testing.T, base, method string, body map[string]string) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, base+"/api/policy", strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", base)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
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
