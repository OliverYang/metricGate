// Package webspec holds the contract between the cockpit server and its
// single-file frontend, in exactly one place.
//
// The cockpit frontend is deliberately one JavaScript file with no build
// step (see docs/design/cockpit-single-file-tech.md). That buys zero network
// at install time and one artifact to ship, but it leaves the two sides of
// the contract nothing to share: Go compiles its types, the browser gets
// whatever the JSON says. A field renamed on one side and not the other is
// then a silent mismatch — a table that renders empty rather than an error.
//
// So this package exists to make the mismatch loud. It writes the spec to
// internal/cockpit/web/spec.json, the frontend imports it at load time, and
// internal/cockpit/cockpit_test.go asserts that the spec the browser reads is
// the spec the server served. A drifting field fails `go test`, not the
// user's screen.
//
// The file is generated, never hand-edited: `go generate ./internal/webspec`
// is the only writer.
package webspec

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
)

// APIVersion is the single number both sides can refuse to reconcile with.
// The frontend compares it to the copy it was shipped with and shows a
// "binary and assets are out of date, rebuild" notice instead of rendering
// half a page.
const APIVersion = 1

// Severity values double as the facet values the URL carries, so they are
// part of the external contract, not just labels.
const (
	SevError   = "error"
	SevWarning = "warning"
	SevInfo    = "info"
)

// Status values are the issue state machine (docs/design/cockpit-sonarqube-
// parity.md §4.1). They persist in issue_states and travel in the URL.
const (
	StatusOpen          = "open"
	StatusAccepted      = "accepted"
	StatusFalsePositive = "false_positive"
	StatusConfirmed     = "confirmed"
)

// Sort orders the Issues list understands. Stable ids, not display labels:
// the label is a frontend concern, the id is what the URL persists.
const (
	SortSeverity = "severity"
	SortFile     = "file"
	SortNewest   = "newest"
	SortRule     = "rule"
)

// View ids map one to one onto the entries of VIEWS in the frontend.
const (
	ViewProjects = "projects"
	ViewOverview = "overview"
	ViewIssues   = "issues"
	ViewRules    = "rules"
	ViewGate     = "gate"
	ViewActivity = "activity"
	ViewCalls    = "calls"
	ViewRuns     = "runs"
	ViewPolicy   = "policy"
)

// Metric scale direction, used by the Summary measures: a rating of "A" means
// nothing until the reader knows whether the metric counts something good or
// something bad.
const (
	ScaleLowerIsBetter  = "lower"
	ScaleHigherIsBetter = "higher"
)

// Grade is the A-E rating shown on Summary measures. The bands are a product
// decision and live in policy, so this package only enumerates the letters.
const (
	GradeA = "A"
	GradeB = "B"
	GradeC = "C"
	GradeD = "D"
	GradeE = "E"
)

// Events are the Activity timeline's sources. Scan runs are the primary
// event; the rest exist because the deterministic loop leaves traces of its
// own (tool calls) and because a gate change is a decision worth dating.
const (
	EventScan   = "scan"
	EventCall   = "call"
	EventPolicy = "policy"
	EventAccept = "accept"
)

// PhaseFilterValues are the MCP-call phases the calls view filters on. They
// mirror the phases the server already emits.
var PhaseFilterValues = []string{"before", "explore", "verify", "review", "other"}

// Severities returns every severity, worst first — the order both the facet
// list and the sort use, so the two cannot disagree about what "first" means.
func Severities() []string {
	return []string{SevError, SevWarning, SevInfo}
}

// Statuses returns the state machine in the order a finding moves through
// it. The first entry is the initial state of anything untouched.
func Statuses() []string {
	return []string{StatusOpen, StatusConfirmed, StatusFalsePositive, StatusAccepted}
}

// Sorts returns every order the Issues view accepts.
func Sorts() []string {
	return []string{SortSeverity, SortFile, SortNewest, SortRule}
}

// Views returns every route the frontend can render, in sidebar order.
//
// This is the enumeration the sidebar, the workspace tab bar and the URL all
// read, so it has to be the whole set: a view that exists in VIEWS but not here
// is reachable only by a pasted URL, and a view named here with no VIEWS entry
// is a nav button that paints nothing. The cockpit test compares this list with
// the frontend's table in both directions.
func Views() []string {
	return []string{
		ViewProjects, ViewOverview, ViewIssues, ViewGate, ViewRules, ViewActivity,
		ViewCalls, ViewRuns, ViewPolicy,
	}
}

// EventsList returns the Activity timeline's event kinds in display order.
func EventsList() []string {
	return []string{EventScan, EventCall, EventPolicy, EventAccept}
}

// MetricQuota is the ceiling on saved metric snapshots per API response, low
// enough that a worst-case response stays a few hundred kilobytes.
const MetricQuota = 5000

// Spec is the whole document: the version, the enumerations, and the shared
// constants a frontend would otherwise have to re-derive.
type Spec struct {
	APIVersion  int      `json:"apiVersion"`
	Severities  []string `json:"severities"`
	Statuses    []string `json:"statuses"`
	Sorts       []string `json:"sorts"`
	Views       []string `json:"views"`
	Events      []string `json:"events"`
	Phases      []string `json:"phases"`
	MetricQuota int      `json:"metricQuota"`
}

// Build renders the spec both sides read. Deterministic by construction:
// every slice comes from the accessors above, so two runs produce identical
// bytes and a test can compare them.
func Build() Spec {
	return Spec{
		APIVersion:  APIVersion,
		Severities:  Severities(),
		Statuses:    Statuses(),
		Sorts:       Sorts(),
		Views:       Views(),
		Events:      EventsList(),
		Phases:      PhaseFilterValues,
		MetricQuota: MetricQuota,
	}
}

// Write renders the spec as pretty JSON at path. The file is generated, so
// the caller owns nothing about the bytes except that they are valid.
func Write(path string) error {
	data, err := json.MarshalIndent(Build(), "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}

// Handler serves the spec document the frontend imports, so the browser and
// the Go side read one file rather than two copies that drift.
func Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(Build())
	}
}
