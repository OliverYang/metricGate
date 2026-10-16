package cockpit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"metricgate/internal/model"
	"metricgate/internal/store"
)

// The scope contract is the one thing every workspace endpoint has to agree
// on, so it is tested as a contract: the same token must produce the same
// window, and a token nobody granted must produce the same refusal, on every
// endpoint that reads it.

func TestResolveScopeTokens(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	summary := &model.RunSummary{Files: 2, Findings: []model.Finding{
		{RuleID: "go/cyclomatic", Severity: model.SevError, Source: model.SrcMetric,
			File: "a.go", StartLine: 12, Message: "complexity 18"},
	}}
	if _, err := st.SaveRunForProject(summary, "go", "aaaa", "/repo-a"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		url    string
		window store.Window
		status int
	}{
		// No token is the pre-scope request and must stay global.
		{"/api/gate", store.Window{}, http.StatusOK},
		{"/api/gate?scope=all", store.Window{}, http.StatusOK},
		{"/api/gate?scope=unassigned", store.Window{Unassigned: true}, http.StatusOK},
		{"/api/gate?scope=/repo-a", store.Window{ProjectRoot: "/repo-a"}, http.StatusOK},
		// A directory that exists but was never checked is refused: the
		// whitelist is about what this cockpit has seen, not what the disk
		// can resolve.
		{"/api/gate?scope=/etc", store.Window{}, http.StatusBadRequest},
		{"/api/gate?scope=/no/such/repo-xyz", store.Window{}, http.StatusBadRequest},
	}
	handler := gateAPI("", dbPath)
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.url, nil)
		req.Host = "127.0.0.1"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Errorf("%s: status = %d, want %d (%s)", tc.url, rec.Code, tc.status, rec.Body.String())
			continue
		}
		if tc.status != http.StatusOK {
			if body := rec.Body.String(); !strings.Contains(body, "unknown scope") {
				t.Errorf("%s: refusal = %q, want it to name the condition", tc.url, body)
			}
		}
	}
}

func TestResolveScopeDirectly(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	summary := &model.RunSummary{Files: 2, Findings: []model.Finding{
		{RuleID: "go/cyclomatic", Severity: model.SevError, Source: model.SrcMetric,
			File: "a.go", StartLine: 12, Message: "complexity 18"},
	}}
	if _, err := st.SaveRunForProject(summary, "go", "aaaa", "/repo-a"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	window, err := resolveScope(scopeRequest("/api/x"), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if window.Limit != 0 || !window.Since.IsZero() || window.ProjectRoot != "" || window.Unassigned {
		t.Fatalf("no-scope window = %+v, want the zero window", window)
	}
	if window, err = resolveScope(scopeRequest("/api/x?scope=unassigned"), dbPath); err != nil {
		t.Fatal(err)
	}
	if !window.Unassigned || window.ProjectRoot != "" {
		t.Fatalf("unassigned window = %+v", window)
	}
	// A call-log project the run history has never seen must still resolve:
	// the project picker renders it, and a 400 on a card the operator can see
	// is a dead end.
	if window, err = resolveScope(scopeRequest("/api/x?scope=/repo-b"), dbPath, "/repo-b"); err != nil {
		t.Fatalf("call-log-only root refused: %v", err)
	}
	if window.ProjectRoot != "/repo-b" {
		t.Fatalf("window = %+v", window)
	}
	if _, err := resolveScope(scopeRequest("/api/x?scope=/repo-c"), dbPath); err == nil {
		t.Fatal("unknown root accepted")
	}
}

func TestScopeWindowOnlyNarrows(t *testing.T) {
	// The issue list arrives with a 60-run window. A project scope keeps it;
	// the non-project scopes clear it, because they describe which runs
	// qualify rather than how many.
	base := store.Window{Limit: defaultWindowLimit}
	dst := base
	scopeWindow(&dst, store.Window{ProjectRoot: "/repo-a"})
	if dst.Limit != defaultWindowLimit || dst.ProjectRoot != "/repo-a" {
		t.Fatalf("project scope widened or lost the window: %+v", dst)
	}
	dst = base
	scopeWindow(&dst, store.Window{})
	if dst.Limit != defaultWindowLimit || dst.ProjectRoot != "" {
		t.Fatalf("global scope = %+v, want the window kept and every project included", dst)
	}
	dst = base
	scopeWindow(&dst, store.Window{Unassigned: true})
	if dst.Limit != defaultWindowLimit || !dst.Unassigned {
		t.Fatalf("unassigned scope = %+v", dst)
	}
}

func TestPolicyReadReflectsScope(t *testing.T) {
	// Two repositories, one database, one server. Reading the policy while
	// looking at project A must never hand back project B's file — that is
	// the failure the whole iteration exists to remove.
	root := t.TempDir()
	scopeDir := filepath.Join(root, "project-a")
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "version: 1\nlanguages:\n  go:\n    enabled: true\n    rules:\n      go/cyclomatic:\n        max: 7\n"
	policyPath := filepath.Join(scopeDir, ".metricgate.yaml")
	if err := os.WriteFile(policyPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	summary := &model.RunSummary{Files: 2, Findings: []model.Finding{
		{RuleID: "go/cyclomatic", Severity: model.SevError, Source: model.SrcMetric,
			File: "a.go", StartLine: 12, Message: "complexity 18"},
	}}
	if _, err := st.SaveRunForProject(summary, "go", "aaaa", scopeDir); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	handler := policyAPI("", dbPath)
	serve := func(url string) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		req.Host = "127.0.0.1"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var out map[string]any
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode %s: %v", url, err)
			}
		}
		return rec.Code, out
	}

	code, view := serve("/api/policy?scope=" + scopeDir)
	if code != http.StatusOK {
		t.Fatalf("scoped policy status = %d", code)
	}
	if got := view["root"]; canonicalScopePath(fmt.Sprint(got)) != canonicalScopePath(scopeDir) {
		t.Errorf("scoped root = %v, want %v", got, scopeDir)
	}
	if got := view["path"]; canonicalScopePath(fmt.Sprint(got)) != canonicalScopePath(policyPath) {
		t.Errorf("scoped path = %v, want %v", got, policyPath)
	}
	// The refusal must not leak a policy from the server's own directory.
	if code, _ := serve("/api/policy?scope=/no/such/repo-xyz"); code != http.StatusBadRequest {
		t.Errorf("unknown scope status = %d, want 400", code)
	}
	// No scope keeps the historical behaviour: the process working directory.
	if code, _ := serve("/api/policy"); code != http.StatusOK {
		t.Errorf("unscoped policy status = %d, want 200", code)
	}
}

// scopeRequest builds a same-origin GET for the resolver.
func scopeRequest(url string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Host = "127.0.0.1"
	return req
}

func TestPolicyWriteFollowsScope(t *testing.T) {
	// The save path has to resolve the same file the read path showed. If it
	// fell back to the process directory, an operator editing project A's
	// policy would rewrite the policy of the repository the server started in
	// — the exact failure the read-side scope exists to prevent, only now it
	// destroys work instead of misreporting it.
	root := t.TempDir()
	scopeDir := filepath.Join(root, "project-a")
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(scopeDir, ".metricgate.yaml")
	if err := os.WriteFile(policyPath, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	summary := &model.RunSummary{Files: 1}
	if _, err := st.SaveRunForProject(summary, "go", "aaaa", scopeDir); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	handler := policyAPI("", dbPath)
	body := fmt.Sprintf(`{"path":%q,"content":"version: 1\n"}`, policyPath)

	// A write aimed at a project the cockpit has not seen must be refused, not
	// redirected into whatever file the server can find.
	req := httptest.NewRequest(http.MethodPut, "/api/policy?scope=/no/such/repo-xyz", strings.NewReader(body))
	req.Host = "127.0.0.1"
	req.Header.Set("Origin", "http://127.0.0.1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown-scope write status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}

	// The scoped write lands in the scoped file.
	scoped := fmt.Sprintf(`{"path":%q,"content":"version: 1\nlanguages:\n  go:\n    enabled: true\n"}`, policyPath)
	req = httptest.NewRequest(http.MethodPut, "/api/policy?scope="+scopeDir, strings.NewReader(scoped))
	req.Host = "127.0.0.1"
	req.Header.Set("Origin", "http://127.0.0.1")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("scoped write status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	written, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "enabled: true") {
		t.Fatalf("scoped write did not reach the scoped file: %s", written)
	}
}
