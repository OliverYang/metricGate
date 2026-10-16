package cockpit

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"metricgate/internal/accept"
	"metricgate/internal/store"
)

// Issues API ----------------------------------------------------------------
//
// Per-page filtering, faceting and triage. This endpoint is what the Issues
// view is built on; it is separate from /api/dashboard because the two answer
// different questions. The dashboard answers "what is the state of everything",
// once, on page load. This endpoint answers "what are the errors, in this
// window, filtered these ways, on page 3" — a question a fixed payload cannot
// answer without shipping a repository's whole finding history to the browser.
//
// Read and write are separate verbs on purpose. GET never mutates, so it sits
// behind the loopback guard alone. PATCH records a decision, so it takes the
// full write posture this package uses everywhere: same-origin, a strict
// content type, a size cap, unknown-field rejection, and a store round trip
// that closes on every path.

// issuesAPI serves GET /api/issues and PATCH /api/issues/state.
func issuesAPI(logPath, dbPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/state") {
			issuesStateAPI(w, r, dbPath)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		servingIssuesForScope(w, r, logPath, dbPath)
	}
}

// servingIssuesForScope parses the query, resolves the scope, opens the store, and
// writes one page.
func servingIssuesForScope(w http.ResponseWriter, r *http.Request, logPath, dbPath string) {
	query, err := parseIssueQuery(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	scope, err := policyScopeWindow(r, logPath, dbPath)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The scope narrows the window the parser built; it never widens it, so
	// "the last 60 runs of this project" stays a bounded question.
	scopeWindow(&query.Window, scope)
	if dbPath == "" {
		writeJSON(w, http.StatusOK, emptyIssueResult(query.Window.String()))
		return
	}
	st, err := store.Open(dbPath)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "history store unavailable")
		return
	}
	defer st.Close()
	ledger, ledgerErr := accept.Load(accept.DefaultPath)
	if ledgerErr != nil {
		// A ledger that cannot be read must not blank the issue list: the
		// findings are the primary data and the ledger is an annotation on
		// them. The error is dropped, so the UI shows every verdict as "open"
		// rather than hiding the row it applies to.
		ledger = nil
	}
	result, err := st.IssuesForQuery(query, ledger)
	if err != nil {
		// A refused query is a caller bug — an unknown value, a bad page — so
		// it is a 400. Anything else is genuinely internal, and calling it
		// that is the honest answer.
		if isQueryRejected(err) {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "issue query failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// parseIssueQuery reads the filters from the URL. Every unknown value is a 400
// naming the parameter, not an empty list: "severity=critical" is a caller bug,
// and answering it with zero issues reads as "this repository is clean" — the
// most expensive kind of wrong answer a gate can give.
func parseIssueQuery(r *http.Request) (store.IssueQuery, error) {
	q := store.IssueQuery{Window: store.Window{Limit: defaultWindowLimit}}
	window, err := windowFromParams(r.URL.Query())
	if err != nil {
		return q, err
	}
	q.Window = window
	q.Severity = strings.TrimSpace(r.URL.Query().Get("severity"))
	q.Status = strings.TrimSpace(r.URL.Query().Get("status"))
	q.Rule = strings.TrimSpace(r.URL.Query().Get("rule"))
	q.File = strings.TrimSpace(r.URL.Query().Get("file"))
	q.Source = strings.TrimSpace(r.URL.Query().Get("source"))
	q.Query = strings.TrimSpace(r.URL.Query().Get("q"))
	q.Sort = strings.TrimSpace(r.URL.Query().Get("sort"))

	page, err := intParam(r.URL.Query().Get("page"), 1)
	if err != nil {
		return q, err
	}
	size, err := intParam(r.URL.Query().Get("pageSize"), issuesPageSizeDefault)
	if err != nil {
		return q, err
	}
	if size > issuesPageSizeMax {
		return q, errPageSizeTooLarge
	}
	q.Page, q.PageSize = page, size
	return q, nil
}

// The parse errors are values rather than ad-hoc Errorf strings so the message
// a 400 carries names the parameter the same way on every path.
var (
	errIntValue              = errors.New("must be an integer")
	errWindowLimitOutOfRange = errors.New("limit must be between 1 and 500")
	errPageSizeTooLarge      = errors.New("pageSize must be at most 200")
	errSinceUnparsable       = errors.New("since must be an RFC3339 timestamp")
)

const (
	// defaultWindowLimit bounds one response. The store's own semantics are
	// "every run when limit is 0"; this endpoint never asks for that, because
	// a repository's entire history of issues is not a UI page.
	defaultWindowLimit    = 60
	issuesPageSizeDefault = 50
	issuesPageSizeMax     = 200
)

// windowFromParams reads the window: limit and since.
//
// The project scope is not read here. It arrives through resolveScope, which
// refuses any directory the server has not already seen: a browser-reachable
// endpoint that accepted arbitrary paths would be a way to read projects that
// were never granted to this cockpit, and a shared database answering with
// another project's issues is the one failure here that looks like a working
// feature.
func windowFromParams(params map[string][]string) (store.Window, error) {
	w := store.Window{Limit: defaultWindowLimit}
	limit, err := intParam(firstValue(params, "limit"), defaultWindowLimit)
	if err != nil {
		return w, err
	}
	if limit < 1 || limit > 500 {
		return w, errWindowLimitOutOfRange
	}
	w.Limit = limit
	since := strings.TrimSpace(firstValue(params, "since"))
	if since != "" {
		parsed, err := time.Parse(time.RFC3339, since)
		if err != nil {
			return w, errSinceUnparsable
		}
		w.Since = parsed
	}
	return w, nil
}

// firstValue reads one parameter from a map, so the parsers above are unit
// testable with a plain map and no *http.Request to build.
func firstValue(params map[string][]string, key string) string {
	if values, ok := params[key]; ok && len(values) > 0 {
		return values[0]
	}
	return ""
}

// intParam parses an optional integer, falling back when the value is absent.
// An empty value is a legal omission; a non-numeric one is not.
func intParam(raw string, fallback int) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return fallback, errIntValue
	}
	return n, nil
}

// emptyIssueResult is what a database-less installation returns. It is shaped
// exactly like a real result so the frontend needs no special case for "no
// store configured yet".
func emptyIssueResult(window string) store.IssueResult {
	return store.IssueResult{
		Issues:     []store.IssueRow{},
		Severities: map[string]int{},
		Statuses:   map[string]int{},
		Sources:    map[string]int{},
		Rules:      []store.RuleCount{},
		Window:     window,
	}
}

// issueStateRequest is one triage decision. Both fields are required: a state
// without the fingerprint it applies to is not a decision, it is a guess about
// which finding was meant.
type issueStateRequest struct {
	Fingerprint string `json:"fingerprint"`
	Status      string `json:"status"`
	Owner       string `json:"owner"`
	Reason      string `json:"reason"`
	Expires     string `json:"expires"`
}

// issueStateMaxBytes caps a decision. A verdict is a sentence and a name; a
// request past a few kilobytes is a paste, not a decision.
const issueStateMaxBytes = 16 * 1024

// issuesStateAPI serves PATCH /api/issues/state.
func issuesStateAPI(w http.ResponseWriter, r *http.Request, dbPath string) {
	if r.Method != http.MethodPatch {
		w.Header().Set("Allow", "PATCH")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !sameOrigin(r) {
		writeJSONError(w, http.StatusForbidden, "same-origin request required")
		return
	}
	request, ok := decodeIssueStateRequest(w, r)
	if !ok {
		return
	}
	if dbPath == "" {
		writeJSONError(w, http.StatusServiceUnavailable, "history store is disabled")
		return
	}
	st, err := store.Open(dbPath)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, "history store unavailable")
		return
	}
	defer st.Close()
	recordIssueState(w, st, request)
}

// recordIssueState validates and stores one verdict. A verdict without a
// reason is refused, mirroring the acceptance ledger: "someone decided
// something" without saying what or why is exactly the anonymous concession
// internal/accept exists to prevent.
func recordIssueState(w http.ResponseWriter, st *store.Store, request issueStateRequest) {
	state := store.IssueState{
		Fingerprint: request.Fingerprint,
		Status:      store.IssueStatus(request.Status),
		Owner:       strings.TrimSpace(request.Owner),
		Reason:      strings.TrimSpace(request.Reason),
		Expires:     strings.TrimSpace(request.Expires),
	}
	if state.Fingerprint == "" {
		writeJSONError(w, http.StatusBadRequest, "fingerprint is required")
		return
	}
	if !state.Status.Valid() {
		writeJSONError(w, http.StatusBadRequest, "status must be one of open, confirmed, false_positive, accepted")
		return
	}
	if state.Status != store.IssueOpen && state.Reason == "" {
		writeJSONError(w, http.StatusBadRequest, "reason is required — a verdict without a reason cannot be reviewed later")
		return
	}
	if err := st.SetIssueState(state); err != nil {
		writeJSONError(w, http.StatusBadRequest, "triage decision rejected: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// decodeIssueStateRequest reads the body with the package's usual posture:
// strict content type, size cap, unknown-field rejection, and a trailing-data
// check.
func decodeIssueStateRequest(w http.ResponseWriter, r *http.Request) (issueStateRequest, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeJSONError(w, http.StatusUnsupportedMediaType, "application/json required")
		return issueStateRequest{}, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, issueStateMaxBytes+1))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "read body: "+err.Error())
		return issueStateRequest{}, false
	}
	if len(body) > issueStateMaxBytes {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "decision too large")
		return issueStateRequest{}, false
	}
	var request issueStateRequest
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return issueStateRequest{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeJSONError(w, http.StatusBadRequest, "invalid request: unexpected trailing data")
		return issueStateRequest{}, false
	}
	return request, true
}

// writeJSON encodes a payload. Encoder failures are not recoverable for the
// caller's purposes, so they are logged to the response and dropped.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// isQueryRejected reports whether an error is a refused query rather than an
// internal failure. parseIssueQuery catches the URL-level problems; this one
// catches the store-level refusals (unknown sort, out-of-range page) so the two
// halves of validation cannot disagree about what a bad request looks like.
func isQueryRejected(err error) bool {
	message := err.Error()
	for _, prefix := range []string{"unknown ", "page ", "limit ", "since ", "must be an integer"} {
		if strings.HasPrefix(message, prefix) {
			return true
		}
	}
	return false
}
