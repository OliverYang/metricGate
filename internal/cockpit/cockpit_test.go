package cockpit

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"metricgate/internal/calllog"
	"metricgate/internal/model"
	"metricgate/internal/store"
)

// waitResult drains the check channel with a deadline so a broken
// implementation fails the test instead of hanging it.
func waitResult(t *testing.T, ch <-chan Status) Status {
	t.Helper()
	select {
	case s, ok := <-ch:
		if !ok {
			t.Fatal("check channel closed without a result")
		}
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("check did not complete in time")
		return Status{}
	}
}

func TestGroupCallsByProjectRequiresVerifiedGitRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "source.go")
	if err := os.WriteFile(file, []byte("package sample"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	records := []calllog.Record{
		{Time: now, Tool: "check_path", Phase: calllog.PhaseVerify, Target: "path=" + file, Verdict: "PASS", Errors: 0, Warnings: 1},
		{Time: now.Add(time.Minute), Tool: "check_path", Target: "path=relative/file.go"},
		{Time: now.Add(2 * time.Minute), Tool: "find_symbols", Target: "name=source.go"},
	}
	calls, projects, unassigned := groupCallsByProject(records)
	if len(calls) != 3 || len(projects) != 1 || unassigned != 2 {
		t.Fatalf("got calls=%d projects=%d unassigned=%d", len(calls), len(projects), unassigned)
	}
	wantRoot := gitRootForPath(targetPath("path=" + file))
	if filepath.Clean(calls[0].ProjectRoot) != filepath.Clean(wantRoot) || calls[1].ProjectRoot != "" || calls[2].ProjectRoot != "" {
		t.Fatalf("unexpected project roots: %q, %q, %q", calls[0].ProjectRoot, calls[1].ProjectRoot, calls[2].ProjectRoot)
	}
	if projects[0].Calls != 1 || projects[0].Warnings != 1 || projects[0].LatestActivity != now.Format(time.RFC3339) {
		t.Fatalf("unexpected project summary: %+v", projects[0])
	}
}

func TestDashboardIncludesProjectGroups(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "source.go")
	if err := os.WriteFile(file, []byte("package sample"), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "calls.jsonl")
	if err := calllog.Append(logPath, calllog.Record{Time: time.Now().UTC(), Tool: "check_path", Target: "path=" + file, Verdict: "PASS"}); err != nil {
		t.Fatal(err)
	}
	dashboard, err := NewDashboard(logPath, "")
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := gitRootForPath(targetPath("path=" + file))
	if len(dashboard.Projects) != 1 || filepath.Clean(dashboard.Projects[0].Root) != filepath.Clean(wantRoot) || dashboard.UnassignedCalls != 0 {
		t.Fatalf("unexpected project payload: %+v (unassigned %d)", dashboard.Projects, dashboard.UnassignedCalls)
	}
	if filepath.Clean(dashboard.Projects[0].ID) != filepath.Clean(wantRoot) {
		t.Fatalf("project ID %q, want canonical root %q", dashboard.Projects[0].ID, wantRoot)
	}
	if len(dashboard.Calls) != 1 || filepath.Clean(dashboard.Calls[0].ProjectRoot) != filepath.Clean(wantRoot) {
		t.Fatalf("unexpected call payload: %+v", dashboard.Calls)
	}
}

func TestTargetPathParsesLocatorWithFollowingFields(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.go")
	if err := os.WriteFile(path, []byte("package sample"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		"path=" + path + " store=.metricgate/history.db",
		"query=Handler path=" + path + " name=virtual.go",
	} {
		if got := targetPath(target); got != resolvedPath {
			t.Errorf("targetPath(%q)=%q, want %q", target, got, resolvedPath)
		}
	}
	for _, target := range []string{"path=relative/file.go", "name=" + path} {
		if got := targetPath(target); got != "" {
			t.Errorf("targetPath(%q)=%q, want empty", target, got)
		}
	}
}

func TestDashboardSeparatesProjectAndLegacyRuns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveRun(&model.RunSummary{Files: 1}, "old", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveRunForProject(&model.RunSummary{Files: 2}, "new", "", "/repo-new"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	dashboard, err := NewDashboard("", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(dashboard.Runs) != 1 || dashboard.Runs[0].ProjectRoot != "" {
		t.Fatalf("legacy/unassigned runs = %+v", dashboard.Runs)
	}
	if len(dashboard.Projects) != 1 || dashboard.Projects[0].Root != "/repo-new" || len(dashboard.Projects[0].Runs) != 1 {
		t.Fatalf("project runs = %+v", dashboard.Projects)
	}
	if dashboard.Projects[0].Runs[0].ProjectRoot != "/repo-new" {
		t.Fatalf("project run root = %q", dashboard.Projects[0].Runs[0].ProjectRoot)
	}
}

// The HTTP layer must read the same store the archive route writes to: the
// dashboard and the archive button are two halves of one history view, and a
// handler that passes an empty db path to one of them renders an empty panel
// that looks exactly like a repository with no history.
func TestHandlerDashboardReadsRunHistory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveRun(&model.RunSummary{Files: 1}, "old", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveRunForProject(&model.RunSummary{Files: 2}, "new", "", "/repo-new"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// The handler is exercised directly rather than through httptest.NewServer:
	// the dashboard is served over loopback, and binding a listener for it is
	// an environment dependency a unit test does not need.
	req := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	// The API guard admits loopback hosts only, so the request has to look like
	// it came from the local cockpit: httptest.NewRequest defaults to
	// "example.com", which is exactly the host a remote caller would use.
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	Handler("", dbPath).ServeHTTP(rec, req)
	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	var dashboard Dashboard
	if err := json.NewDecoder(res.Body).Decode(&dashboard); err != nil {
		t.Fatal(err)
	}
	if len(dashboard.Runs) != 1 || dashboard.Runs[0].ProjectRoot != "" {
		t.Errorf("unassigned runs = %+v, want the legacy run", dashboard.Runs)
	}
	if len(dashboard.Projects) != 1 || len(dashboard.Projects[0].Runs) != 1 {
		t.Errorf("projects = %+v, want /repo-new with its run", dashboard.Projects)
	}
}

func TestHandlerServesEmbeddedUIAndAssets(t *testing.T) {
	handler := Handler("", "")
	for _, target := range []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/", contentType: "text/html", contains: "<!doctype html>"},
		{path: "/web/app.js", contentType: "text/javascript", contains: "fetch("},
		{path: "/web/style.css", contentType: "text/css", contains: ":root"},
	} {
		req := httptest.NewRequest(http.MethodGet, target.path, nil)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want %d", target.path, res.Code, http.StatusOK)
			continue
		}
		if got := res.Header().Get("Content-Type"); !strings.Contains(got, target.contentType) {
			t.Errorf("GET %s content type = %q, want substring %q", target.path, got, target.contentType)
		}
		if !strings.Contains(res.Body.String(), target.contains) {
			t.Errorf("GET %s body does not contain %q", target.path, target.contains)
		}
	}
}

func TestCheckAvailable(t *testing.T) {
	// The feed is served through an injected RoundTripper rather than a live
	// listener: the update check's contract is about parsing the feed and
	// running the state machine, and binding a socket to prove it makes the
	// test depend on the environment having a network stack available.
	c := &Checker{
		Current: "0.1.0",
		FeedURL: "http://feed.test/latest.json",
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return jsonResponse(`{"version":"9.9.9","url":"https://example.com/dl"}`), nil
		})},
	}
	res := waitResult(t, c.Check(context.Background()))
	if res.State != StateAvailable {
		t.Fatalf("state = %q, want %q", res.State, StateAvailable)
	}
	if res.Latest != "9.9.9" || res.URL != "https://example.com/dl" {
		t.Fatalf("payload wrong: %+v", res)
	}
	if got := c.Status(); got.State != StateAvailable || got.Latest != "9.9.9" {
		t.Fatalf("Status() = %+v, want available", got)
	}
}

func TestCheckNotAvailable(t *testing.T) {
	c := &Checker{Current: "0.1.0", FeedURL: feedURL, HTTPClient: feedClient(`{"version":"0.1.0"}`)}
	res := waitResult(t, c.Check(context.Background()))
	if res.State != StateNotAvail {
		t.Fatalf("state = %q, want %q", res.State, StateNotAvail)
	}
}

func TestCheckNotEnabled(t *testing.T) {
	// No feed configured is a shipping decision, not a network failure. The
	// old behaviour probed a placeholder domain and reported its DNS error as
	// StateError, which advertised a host that does not exist. The contract
	// here is the opposite: an empty FeedURL must not touch the network at all,
	// so an HTTPClient that fails on any request still yields not_enabled.
	c := &Checker{Current: "0.1.0", HTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("no request may be made when no feed is configured")
	})}}
	res := waitResult(t, c.Check(context.Background()))
	if res.State != StateNotEnabled {
		t.Fatalf("state = %q, want %q", res.State, StateNotEnabled)
	}
	if res.Error == "" {
		t.Fatal("not_enabled must say how to enable a feed")
	}
}

func TestCheckNetworkError(t *testing.T) {
	// A transport that refuses every request stands in for a closed server.
	c := &Checker{Current: "0.1.0", FeedURL: feedURL, HTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: connection refused")
	})}}
	res := waitResult(t, c.Check(context.Background()))
	if res.State != StateError {
		t.Fatalf("state = %q, want %q", res.State, StateError)
	}
	if res.Error == "" {
		t.Fatal("error state carries no message")
	}
}

func TestCheckTimeout(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })

	c := &Checker{Current: "0.1.0", FeedURL: feedURL, Timeout: 50 * time.Millisecond,
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done() // hang until the client gives up
			return nil, r.Context().Err()
		})}}
	res := waitResult(t, c.Check(context.Background()))
	if res.State != StateError {
		t.Fatalf("state = %q, want %q (timeout must surface as error, not hang)", res.State, StateError)
	}
}

func TestCheckNonBlocking(t *testing.T) {
	// A slow feed must not block the caller: Check returns while the probe
	// is still in flight, and status is checking at that point.
	c := &Checker{Current: "0.1.0", FeedURL: feedURL, HTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		time.Sleep(200 * time.Millisecond)
		return jsonResponse(`{"version":"0.1.0"}`), nil
	})}}
	ch := c.Check(context.Background())
	if got := c.Status(); got.State != StateChecking {
		t.Fatalf("state right after Check = %q, want %q", got.State, StateChecking)
	}
	if res := waitResult(t, ch); res.State != StateNotAvail {
		t.Fatalf("state = %q, want %q", res.State, StateNotAvail)
	}
}

func TestCheckBadFeed(t *testing.T) {
	c := &Checker{Current: "0.1.0", FeedURL: feedURL, HTTPClient: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}}
	if res := waitResult(t, c.Check(context.Background())); res.State != StateError {
		t.Fatalf("state = %q, want %q for HTTP 500", res.State, StateError)
	}
}

func TestSupersededCheckKeepsLatestStatus(t *testing.T) {
	// One transport answers both feeds: the slow URL blocks until released,
	// the fast URL responds immediately. The test is about the generation
	// counter, not about sockets.
	slow := make(chan struct{})
	var slowOnce sync.Once
	releaseSlow := func() { slowOnce.Do(func() { close(slow) }) }
	defer releaseSlow()
	transport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.String(), "fast") {
			return jsonResponse(`{"version":"2.0.0"}`), nil
		}
		<-slow
		return jsonResponse(`{"version":"1.0.0"}`), nil
	})

	c := &Checker{Current: "0.1.0", FeedURL: "http://feed.test/slow.json", Timeout: 2 * time.Second, HTTPClient: &http.Client{Transport: transport}}
	first := c.Check(context.Background())

	// Second check against a fast feed supersedes the first.
	c.FeedURL = "http://feed.test/fast.json"
	second := c.Check(context.Background())
	if res := waitResult(t, second); res.Latest != "2.0.0" {
		t.Fatalf("second check latest = %q, want 2.0.0", res.Latest)
	}

	releaseSlow()
	select {
	case _, ok := <-first:
		if ok {
			t.Fatal("superseded check unexpectedly published a result")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("superseded check did not finish")
	}
}

func TestCheckContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Checker{Current: "0.1.0", FeedURL: feedURL, Timeout: 5 * time.Second,
		HTTPClient: &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			select {
			case <-r.Context().Done():
				return nil, r.Context().Err()
			case <-time.After(500 * time.Millisecond):
				return jsonResponse(`{"version":"9.9.9"}`), nil
			}
		})}}
	ch := c.Check(ctx)
	cancel()
	res := waitResult(t, ch)
	if res.State != StateError {
		t.Fatalf("state = %q, want %q after cancel", res.State, StateError)
	}
}

// feedURL is the stand-in release feed every test below addresses. No socket
// is bound: the Checker accepts an injected client precisely so the state
// machine can be tested without a network stack.
const feedURL = "http://feed.test/latest.json"

// feedClient serves a canned document from memory.
func feedClient(body string) *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(body), nil
	})}
}

// roundTripperFunc adapts a function to http.RoundTripper, so a test can serve
// a canned response without binding a port.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// jsonResponse builds a 200 JSON response in memory.
func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
