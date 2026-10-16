package cockpit

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"metricgate/internal/accept"
	"metricgate/internal/model"
	"metricgate/internal/store"
)

// The API layer's job is translation and refusal: turn a URL into a query, and
// refuse the URLs that would produce a confident wrong answer. These tests
// pin both halves, because a filter that silently returns nothing is the bug a
// dashboard cannot show.

func seededHandler(t *testing.T) (http.HandlerFunc, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	summary := &model.RunSummary{
		Files: 2,
		Findings: []model.Finding{
			{RuleID: "cyclomatic-complexity", Severity: model.SevError, Source: model.SrcMetric,
				File: "a.go", StartLine: 12, Message: "complexity 18",
				Evidence: &model.Evidence{Anchor: model.AnchorExact, SnippetHash: "hashA"}},
			{RuleID: "todo-comment", Severity: model.SevWarning, Source: model.SrcRule,
				File: "b.go", StartLine: 4, Message: "TODO: tidy"},
		},
	}
	if _, err := st.SaveRunForProject(summary, "test", "deadbeef", "/repo-a"); err != nil {
		t.Fatalf("SaveRunForProject: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return issuesAPI("", dbPath), dbPath
}

func getIssues(t *testing.T, handler http.HandlerFunc, url string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeIssues(t *testing.T, rec *httptest.ResponseRecorder) store.IssueResult {
	t.Helper()
	var result store.IssueResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return result
}

func TestIssuesAPIReturnsRowsAndFacets(t *testing.T) {
	handler, _ := seededHandler(t)
	rec := getIssues(t, handler, "/api/issues?limit=20")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	result := decodeIssues(t, rec)
	if result.Total != 2 {
		t.Fatalf("total = %d, want 2", result.Total)
	}
	if result.Severities["error"] != 1 || result.Severities["warning"] != 1 {
		t.Fatalf("facets = %v, want one of each", result.Severities)
	}
	if len(result.Issues) == 0 || result.Issues[0].Fingerprint == "" {
		t.Fatal("rows carry no fingerprint, so the UI could not key a triage decision to them")
	}
	if result.Issues[0].Anchor != model.AnchorExact || result.Issues[1].Anchor != model.AnchorLineOnly {
		t.Fatalf("anchors = %q, %q; want the exact span to say so", result.Issues[0].Anchor, result.Issues[1].Anchor)
	}
}

func TestIssuesAPIRejectsUnknownFilterValue(t *testing.T) {
	handler, _ := seededHandler(t)
	rec := getIssues(t, handler, "/api/issues?severity=critical")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown severity", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "severity") {
		t.Errorf("error = %q, want it to name the parameter", body)
	}
}

func TestIssuesAPIRejectsBadPageAndLimit(t *testing.T) {
	handler, _ := seededHandler(t)
	for _, url := range []string{
		"/api/issues?pageSize=500", // above the cap
		"/api/issues?limit=9000",   // above the cap
		"/api/issues?limit=abc",    // not an integer
		"/api/issues?since=nope",   // not a timestamp
		"/api/issues?sort=recency", // not a known order
	} {
		rec := getIssues(t, handler, url)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", url, rec.Code)
		}
	}
}

func TestIssuesAPIWithoutStoreReturnsEmptyResult(t *testing.T) {
	rec := getIssues(t, issuesAPI("", ""), "/api/issues")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	result := decodeIssues(t, rec)
	if result.Total != 0 || len(result.Issues) != 0 {
		t.Fatalf("empty store = %+v, want zero rows with non-nil slices", result)
	}
	if result.Severities == nil {
		t.Fatal("severity facets came back null; the frontend would need a special case")
	}
}

func patchIssue(t *testing.T, handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/state", bytes.NewBufferString(body))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/json")
	// sameOrigin compares the Origin's authority against the request's Host
	// verbatim, so the probe has to agree on the port too.
	req.Header.Set("Origin", "http://127.0.0.1")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestIssueStatePatchRecordsAVerdict(t *testing.T) {
	handler, dbPath := seededHandler(t)
	rec := patchIssue(t, handler, `{"fingerprint":"abc123","status":"false_positive","reason":"test fixture","owner":"me"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.IssueStates([]string{"abc123"})
	if err != nil {
		t.Fatal(err)
	}
	if got["abc123"].Status != store.IssueFalsePositive || got["abc123"].Reason != "test fixture" {
		t.Fatalf("state = %+v, want the recorded verdict", got["abc123"])
	}
}

func TestIssueStatePatchRefusesCrossOrigin(t *testing.T) {
	handler, _ := seededHandler(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/issues/state",
		bytes.NewBufferString(`{"fingerprint":"x","status":"confirmed","reason":"r"}`))
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/json")
	// A cross-site request: a page on another origin POSTing a verdict into
	// this cockpit. The guard exists so it cannot.
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestIssueStatePatchValidatesTheDecision(t *testing.T) {
	handler, _ := seededHandler(t)
	cases := map[string]string{
		`{"fingerprint":""}`:                       "no fingerprint",
		`{"fingerprint":"x","status":"closed"}`:    "unknown status",
		`{"fingerprint":"x","status":"confirmed"}`: "no reason",
		`{"status":"confirmed","reason":"r"}`:      "unknown field",
		``:                                         "empty body",
	}
	for body, want := range cases {
		rec := patchIssue(t, handler, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", body, rec.Code, want)
		}
	}
}

func TestIssueStatePatchRejectsWrongMethod(t *testing.T) {
	handler, _ := seededHandler(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/issues/state", nil)
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DELETE status = %d, want 405", rec.Code)
	}
}

// The gate endpoint must report the run's own verdict, never a recomputed one:
// a warning under fail_on=error is a pass, and reporting otherwise would make
// the UI contradict the exit code the operator just read.
func TestGateViewReportsTheRunVerdictUnderFailOn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveRun(&model.RunSummary{Files: 1, Findings: []model.Finding{
		{RuleID: "todo-comment", Severity: model.SevWarning, Source: model.SrcRule, File: "a.go", StartLine: 1},
	}}, "test", "cafe"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/gate", nil)
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	gateAPI("", dbPath).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var view GateView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if view.Status != "passed" {
		t.Fatalf("status = %q, want passed: fail_on is error by default, so warnings do not block", view.Status)
	}
	if view.RunID == 0 {
		t.Error("the gate did not name the run its verdict came from")
	}
}

func TestMeasuresViewGradesTheRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	summary := &model.RunSummary{
		Files: 1,
		Metrics: []model.MetricResult{
			{File: "a.go", Metric: "cyclomatic-complexity", Value: 12, Warn: 10, Err: 15},
		},
	}
	if _, err := st.SaveRun(summary, "test", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/measures", nil)
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	measuresAPI("", dbPath).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var view MeasuresView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range view.Measures {
		if m.ID == "cyclomatic-complexity" {
			found = true
			if m.Value != 12 || m.Warn != 10 || m.Err != 15 {
				t.Errorf("measure = %+v, want the numbers the run recorded", m)
			}
			if m.Grade != "C" {
				t.Errorf("grade = %q, want C (12 is above warn 10, below err 15)", m.Grade)
			}
		}
	}
	if !found {
		t.Fatalf("measures = %+v, want the cyclomatic row", view.Measures)
	}
}

// The ledger is a read of the project's own concession record; an unreadable
// one must not blank the issue list, because the findings are the data and the
// ledger is an annotation on it.
func TestIssuesSurviveAnUnreadableLedger(t *testing.T) {
	handler, _ := seededHandler(t)
	t.Chdir(t.TempDir())
	if err := writeLedger(t, ".metricgate/accepted.yaml", "entries: [unbalanced"); err != nil {
		t.Skipf("could not write the ledger probe: %v", err)
	}
	rec := getIssues(t, handler, "/api/issues")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even with a malformed ledger: %s", rec.Code, rec.Body.String())
	}
	if got := decodeIssues(t, rec).Total; got != 2 {
		t.Fatalf("total = %d, want the two findings despite the broken ledger", got)
	}
}

func writeLedger(t *testing.T, path, content string) error {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

var _ = accept.DefaultPath
var _ = time.Now

// The source endpoint is the one place the cockpit reads the operator's code,
// so the boundaries are what the tests are for: a finding's file and nothing
// else (docs/design/cockpit-sonarqube-parity.md §4.4).

// seededSourceHandler seeds a finding that points at a real file in a temp tree
// and returns the handler, the store path, and the fingerprint to ask about.
func seededSourceHandler(t *testing.T) (http.HandlerFunc, string, string) {
	t.Helper()
	root := t.TempDir()
	abs := filepath.Join(root, "engine.go")
	body := strings.Repeat("// filler\n", 5) +
		"func loop() {\n\tfor i := 0; i < 10; i++ {\n\t\tprintln(i)\n\t}\n}\n" +
		strings.Repeat("// trailing\n", 5)
	if err := os.WriteFile(abs, []byte(body), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	summary := &model.RunSummary{
		Files: 1,
		Findings: []model.Finding{
			{RuleID: "cyclomatic-complexity", Severity: model.SevError, Source: model.SrcMetric,
				// Line 6 is `func loop() {`: a keyword line, so the window's
				// highlight class is observable rather than incidental.
				File: abs, StartLine: 6, StartCol: 1, EndLine: 6, EndCol: 13,
				Message:  "complexity 3",
				Evidence: &model.Evidence{Anchor: model.AnchorExact, SnippetHash: "srcHash"}},
		},
	}
	if _, err := st.SaveRunForProject(summary, "test", "deadbeef", root); err != nil {
		t.Fatalf("SaveRunForProject: %v", err)
	}
	rows, err := st.IssuesForQuery(store.IssueQuery{Window: store.Window{Limit: 10}}, nil)
	if err != nil {
		t.Fatalf("IssuesForQuery: %v", err)
	}
	if len(rows.Issues) != 1 {
		t.Fatalf("seeded %d rows, want 1", len(rows.Issues))
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return sourceAPI(dbPath), dbPath, rows.Issues[0].Fingerprint
}

func decodeSource(t *testing.T, rec *httptest.ResponseRecorder) SourceView {
	t.Helper()
	var view SourceView
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return view
}

func TestSourceAPIReturnsTheWindowAroundTheFinding(t *testing.T) {
	handler, _, fingerprint := seededSourceHandler(t)
	rec := getIssues(t, handler, "/api/source?fingerprint="+fingerprint)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	view := decodeSource(t, rec)
	if view.AnchorLine != 6 {
		t.Errorf("anchor line = %d, want 6", view.AnchorLine)
	}
	if view.EndLine <= view.StartLine {
		t.Fatalf("window %d..%d is not a window", view.StartLine, view.EndLine)
	}
	if len(view.Lines) != view.EndLine-view.StartLine+1 {
		t.Errorf("returned %d lines for %d..%d", len(view.Lines), view.StartLine, view.EndLine)
	}
	if view.Language != "go" {
		t.Errorf("language = %q, want go", view.Language)
	}
	anchor := 6 - view.StartLine
	if view.Lines[anchor].Number != 6 {
		t.Errorf("lines are not numbered from their own position in the file")
	}
	if view.Lines[anchor].Class != "key" {
		t.Errorf("anchor line class = %q, want the keyword highlight", view.Lines[anchor].Class)
	} else {
		// The window's filler lines must not share the anchor's class: one
		// class for the whole window would make the highlight meaningless.
		// Lines 1-5 are comments, so they are the control case.
		for _, line := range view.Lines {
			if line.Number < 6 && line.Class != "comment" {
				t.Errorf("filler line %d class = %q, want comment", line.Number, line.Class)
			}
		}
	}
	// An exact anchor keeps its byte span; that is what the UI highlights.
	if view.AnchorStart != 1 || view.AnchorEnd != 13 {
		t.Errorf("anchor span = %d..%d, want 1..13", view.AnchorStart, view.AnchorEnd)
	}
}

func TestSourceAPIRequiresAFingerprint(t *testing.T) {
	handler, _, _ := seededSourceHandler(t)
	rec := getIssues(t, handler, "/api/source")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSourceAPIRefusesAnUnknownFingerprint(t *testing.T) {
	handler, _, _ := seededSourceHandler(t)
	rec := getIssues(t, handler, "/api/source?fingerprint=not-a-fingerprint")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a fingerprint no finding carries", rec.Code)
	}
}

func TestSourceAPIRefusesMethodOtherThanGet(t *testing.T) {
	handler, _, _ := seededSourceHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/source", nil)
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// A finding whose file is gone is a state, not a failure: the operator is
// looking at a run that has since moved under them.
func TestSourceAPIReportsAnUnreadableFileAsAReason(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "gone.go")
	if err := os.WriteFile(abs, []byte("package x\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "history.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	summary := &model.RunSummary{
		Files: 1,
		Findings: []model.Finding{
			{RuleID: "r", Severity: model.SevError, Source: model.SrcMetric, File: abs, StartLine: 1, Message: "m"},
		},
	}
	if _, err := st.SaveRunForProject(summary, "test", "c", root); err != nil {
		t.Fatalf("SaveRunForProject: %v", err)
	}
	rows, err := st.IssuesForQuery(store.IssueQuery{Window: store.Window{Limit: 10}}, nil)
	if err != nil {
		t.Fatalf("IssuesForQuery: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(abs); err != nil {
		t.Fatalf("remove source: %v", err)
	}
	rec := getIssues(t, sourceAPI(dbPath), "/api/source?fingerprint="+rows.Issues[0].Fingerprint)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a reason: %s", rec.Code, rec.Body.String())
	}
	view := decodeSource(t, rec)
	if view.Reason == "" {
		t.Error("a missing file must say why, not return an empty window silently")
	}
}
