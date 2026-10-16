package store

import (
	"testing"
	"time"

	"metricgate/internal/accept"
	"metricgate/internal/model"
)

// The fingerprint is the whole load-bearing idea behind triage state: the same
// code smell must produce the same key on every scan, and a different key once
// the code actually changed. If it fails either way, verdicts either never
// attach or attach to the wrong thing, and both are worse than no triage.

func fp(rule, file string, line int, hash string) model.Finding {
	f := model.Finding{RuleID: rule, File: file, StartLine: line, Message: "m"}
	if hash != "" {
		f.Evidence = &model.Evidence{Anchor: model.AnchorExact, SnippetHash: hash}
	}
	return f
}

func TestFingerprintIsStableForTheSameFinding(t *testing.T) {
	a := fp("cyclomatic-complexity", "a.go", 12, "abc123")
	b := fp("cyclomatic-complexity", "a.go", 12, "abc123")
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatalf("same finding produced two fingerprints: %q vs %q", Fingerprint(a), Fingerprint(b))
	}
}

func TestFingerprintSeparatesTwoOccurrencesOfOneRule(t *testing.T) {
	// The failure mode that matters: two call sites of the same banned API in
	// the same file must not collapse into one issue, or fixing one silences
	// the other's verdict.
	first := Fingerprint(fp("banned-fmt-Print*", "a.go", 10, "h1"))
	second := Fingerprint(fp("banned-fmt-Print*", "a.go", 42, "h2"))
	if first == second {
		t.Fatal("two distinct occurrences of one rule share a fingerprint")
	}
}

func TestFingerprintUsesBytesWhenExactAndLineWhenNot(t *testing.T) {
	exact := Fingerprint(fp("r", "a.go", 10, "h1"))
	lineOnly := Fingerprint(fp("r", "a.go", 10, ""))
	if exact == lineOnly {
		t.Fatal("an exact anchor and a line-only anchor of the same finding collide")
	}
	// A line-scoped finding whose line moved is a new issue. That is the stated
	// trade-off of a weaker key, verified here so it cannot change silently.
	moved := Fingerprint(fp("r", "a.go", 11, ""))
	if moved == lineOnly {
		t.Fatal("a line-only finding survived a line shift")
	}
}

func TestFingerprintIgnoresMessageAndSeverity(t *testing.T) {
	// The message and severity are properties of the verdict as run, not of
	// the issue: an edit that changes how a rule words its message must not
	// change the issue's identity, or every state would be lost on upgrade.
	a := fp("r", "a.go", 10, "h1")
	b := a
	b.Message = "different wording"
	b.Severity = model.SevInfo
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("fingerprint followed message or severity, so verdicts cannot survive rephrasings")
	}
}

func TestIssueStateRoundTrip(t *testing.T) {
	st := openTestStore(t)
	want := IssueState{Fingerprint: "abc", Status: IssueFalsePositive, Owner: "me", Reason: "not a defect", Expires: ""}
	if err := st.SetIssueState(want); err != nil {
		t.Fatalf("SetIssueState: %v", err)
	}
	got, err := st.IssueStates([]string{"abc", "missing"})
	if err != nil {
		t.Fatalf("IssueStates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("states = %d, want 1 (an unknown key must not be reported as a synthetic open)", len(got))
	}
	if got["abc"].Status != IssueFalsePositive || got["abc"].Reason != "not a defect" {
		t.Fatalf("state = %+v, want the recorded verdict", got["abc"])
	}
	if got["abc"].UpdatedAt == "" {
		t.Error("updated_at was not recorded")
	}
}

func TestIssueStateRejectsUnknownStatus(t *testing.T) {
	st := openTestStore(t)
	if err := st.SetIssueState(IssueState{Fingerprint: "x", Status: IssueStatus("bogus")}); err == nil {
		t.Fatal("an unknown status was accepted; a typo would create a finding no UI can show")
	}
}

func TestIssueStateUpsertOverwrites(t *testing.T) {
	st := openTestStore(t)
	if err := st.SetIssueState(IssueState{Fingerprint: "k", Status: IssueConfirmed}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIssueState(IssueState{Fingerprint: "k", Status: IssueAccepted}); err != nil {
		t.Fatal(err)
	}
	got, err := st.IssueStates([]string{"k"})
	if err != nil {
		t.Fatalf("IssueStates: %v", err)
	}
	if got["k"].Status != IssueAccepted {
		t.Fatalf("status = %q, want accepted (a second decision must replace the first)", got["k"].Status)
	}
}

func TestSetIssueStatesIsAtomic(t *testing.T) {
	st := openTestStore(t)
	err := st.SetIssueStates([]IssueState{
		{Fingerprint: "a", Status: IssueConfirmed},
		{Fingerprint: "b", Status: IssueStatus("bogus")},
	})
	if err == nil {
		t.Fatal("a batch with one invalid row was accepted")
	}
	got, err := st.IssueStates([]string{"a"})
	if err != nil {
		t.Fatalf("IssueStates: %v", err)
	}
	if len(got) != 0 {
		t.Fatal("a failed bulk write left a row behind; the batch was not atomic")
	}
}

func TestStatusPrecedenceStoredBeatsLedger(t *testing.T) {
	st := openTestStore(t)
	ledger := &accept.Ledger{Entries: []accept.Entry{{
		RuleID: "r", File: "a.go", Line: 10, Owner: "owner", Reason: "ledger reason",
	}}}
	row := IssueRow{Fingerprint: "f", RuleID: "r", File: "a.go", StartLine: 10}
	now := time.Now().UTC()

	got := st.EffectiveStatus(row.Fingerprint, ledger, row.RuleID, row.File, row.StartLine, now)
	if got.Status != IssueAccepted || !got.FromLedger {
		t.Fatalf("ledger verdict = %+v, want accepted from the ledger", got)
	}
	if err := st.SetIssueState(IssueState{Fingerprint: "f", Status: IssueFalsePositive, Reason: "reviewed in UI"}); err != nil {
		t.Fatal(err)
	}
	got = st.EffectiveStatus(row.Fingerprint, ledger, row.RuleID, row.File, row.StartLine, now)
	if got.Status != IssueFalsePositive || got.FromLedger {
		t.Fatalf("after a UI decision: %+v, want false_positive and not from the ledger", got)
	}
}

func TestEffectiveStatusReopensAnExpiredLedgerEntry(t *testing.T) {
	st := openTestStore(t)
	ledger := &accept.Ledger{Entries: []accept.Entry{{
		RuleID: "r", File: "a.go", Line: 10, Owner: "o", Reason: "temporary",
		Expires: time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339),
	}}}
	got := st.EffectiveStatus("none", ledger, "r", "a.go", 10, time.Now().UTC())
	if got.Status != IssueOpen {
		t.Fatalf("expired concession = %+v, want open (an expired ledger entry is not a verdict)", got)
	}
}

// End-to-end through Store.IssuesForQuery: the de-duplication, the ledger
// join and the filters all have to hold at once for the API contract to mean
// anything. runSeed writes three runs of the same two findings.

func seedIssues(t *testing.T, st *Store) {
	t.Helper()
	for range 3 {
		sum := &model.RunSummary{
			Files: 3,
			Findings: []model.Finding{
				{RuleID: "cyclomatic-complexity", Severity: model.SevError, Source: model.SrcMetric,
					File: "a.go", StartLine: 12, EndLine: 30, Message: "complexity 18",
					Evidence: &model.Evidence{Anchor: model.AnchorExact, SnippetHash: "hashA"}},
				{RuleID: "todo-comment", Severity: model.SevWarning, Source: model.SrcRule,
					File: "b.go", StartLine: 4, EndLine: 4, Message: "TODO: tidy",
					Evidence: &model.Evidence{Anchor: model.AnchorLineOnly, SnippetHash: ""}},
			},
		}
		if _, err := st.SaveRunForProject(sum, "test", "commit", "/repo"); err != nil {
			t.Fatalf("SaveRunForProject: %v", err)
		}
	}
}

func TestIssuesDeduplicateAcrossRuns(t *testing.T) {
	st := openTestStore(t)
	seedIssues(t, st)
	got, err := st.IssuesForQuery(IssueQuery{PageSize: 0}, nil)
	if err != nil {
		t.Fatalf("IssuesForQuery: %v", err)
	}
	if got.Total != 2 {
		t.Fatalf("total = %d, want 2 (three runs reporting two findings is two issues)", got.Total)
	}
	if len(got.Issues) != 2 || got.Issues[0].Occurrences != 3 {
		t.Fatalf("issues = %+v, want the top issue recorded in all 3 runs", got.Issues)
	}
	if got.Severities["error"] != 1 || got.Severities["warning"] != 1 {
		t.Fatalf("severity facets = %v, want one of each", got.Severities)
	}
	if got.Window == "" {
		t.Error("window label missing: the result must say which runs it covered")
	}
}

func TestIssuesSeverityFilterNarrowsAndReportsFacet(t *testing.T) {
	st := openTestStore(t)
	seedIssues(t, st)
	got, err := st.IssuesForQuery(IssueQuery{Severity: "warning"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 || got.Issues[0].RuleID != "todo-comment" {
		t.Fatalf("issues = %+v, want only the warning", got.Issues)
	}
	// The severity facet is computed over the unfiltered population, so the
	// error chip stays countable while only warnings are listed.
	if got.Severities["error"] != 1 {
		t.Fatalf("severity facets = %v, want the error count to survive the warning filter", got.Severities)
	}
}

func TestIssuesFreeTextSearch(t *testing.T) {
	st := openTestStore(t)
	seedIssues(t, st)
	got, err := st.IssuesForQuery(IssueQuery{Query: "b.go"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 || got.Issues[0].File != "b.go" {
		t.Fatalf("issues = %+v, want the b.go finding", got.Issues)
	}
}

func TestIssuesRejectUnknownFilterValues(t *testing.T) {
	st := openTestStore(t)
	seedIssues(t, st)
	if _, err := st.IssuesForQuery(IssueQuery{Severity: "critical"}, nil); err == nil {
		t.Fatal("severity \"critical\" was accepted; a URL should name the bad parameter, not return an empty list")
	}
	if _, err := st.IssuesForQuery(IssueQuery{Status: "closed"}, nil); err == nil {
		t.Fatal("status \"closed\" was accepted; it is not a state in this machine")
	}
}

func TestIssuesPagingKeepsTotalHonest(t *testing.T) {
	st := openTestStore(t)
	seedIssues(t, st)
	got, err := st.IssuesForQuery(IssueQuery{Page: 1, PageSize: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 || len(got.Issues) != 1 || got.TotalPages != 2 {
		t.Fatalf("page 1 = %+v, want total 2 with one row and two pages", got)
	}
	second, err := st.IssuesForQuery(IssueQuery{Page: 2, PageSize: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Issues) != 1 || second.Issues[0].Fingerprint == got.Issues[0].Fingerprint {
		t.Fatalf("page 2 = %+v, want the other issue", second.Issues)
	}
}

func TestIssuesJoinLedgerVerdict(t *testing.T) {
	st := openTestStore(t)
	seedIssues(t, st)
	ledger := &accept.Ledger{Entries: []accept.Entry{{
		RuleID: "todo-comment", File: "b.go", Line: 4, Owner: "team", Reason: "deferred by decision",
	}}}
	got, err := st.IssuesForQuery(IssueQuery{}, ledger)
	if err != nil {
		t.Fatal(err)
	}
	var row IssueRow
	for _, r := range got.Issues {
		if r.RuleID == "todo-comment" {
			row = r
		}
	}
	if row.Status != string(IssueAccepted) || row.StatusReason != "deferred by decision" {
		t.Fatalf("ledger-covered row = %+v, want accepted with the ledger's reason", row)
	}
	if !row.FromLedger {
		t.Error("FromLedger false: the UI must be able to say this verdict came from git, not from a click")
	}
}

func TestIssuesFilterByStatus(t *testing.T) {
	st := openTestStore(t)
	seedIssues(t, st)
	if err := st.SetIssueState(IssueState{Fingerprint: "nope", Status: IssueConfirmed}); err != nil {
		t.Fatal(err)
	}
	rows := st.IssuesForQueryMust(t, IssueQuery{})
	fpKey := ""
	for _, r := range rows {
		if r.RuleID == "todo-comment" {
			fpKey = r.Fingerprint
		}
	}
	if fpKey == "" {
		t.Fatal("todo-comment row not found")
	}
	if err := st.SetIssueState(IssueState{Fingerprint: fpKey, Status: IssueConfirmed}); err != nil {
		t.Fatal(err)
	}
	got, err := st.IssuesForQuery(IssueQuery{Status: string(IssueConfirmed)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 || got.Issues[0].Fingerprint != fpKey {
		t.Fatalf("status filter = %+v, want the one confirmed issue", got.Issues)
	}
}

// IssuesForQueryMust is the query with the error folded in, for tests that
// already failed the terminal case above.
func (s *Store) IssuesForQueryMust(t *testing.T, q IssueQuery) []IssueRow {
	t.Helper()
	got, err := s.IssuesForQuery(q, nil)
	if err != nil {
		t.Fatalf("IssuesForQuery: %v", err)
	}
	return got.Issues
}

// The window limit is part of the question, not a display detail: "the last 60
// runs" must reach SQL, or every request groups the whole findings table to
// answer it. These tests pin the population the GROUP BY sees, so the limit
// cannot silently stop scoping the candidates (or start dropping runs that
// belong to the window).

func TestIssuesWindowLimitBoundsTheCandidates(t *testing.T) {
	st := openTestStore(t)
	// Five runs, each reporting the same finding. With no limit all five runs
	// are one issue with five occurrences; a limit of 2 admits only the two
	// newest runs, so the same issue is reported twice.
	for range 5 {
		sum := &model.RunSummary{Files: 1, Findings: []model.Finding{
			fp("todo-comment", "b.go", 4, "hashA"),
		}}
		if _, err := st.SaveRunForProject(sum, "test", "commit", "/repo"); err != nil {
			t.Fatal(err)
		}
	}
	unbounded, err := st.IssuesForQuery(IssueQuery{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(unbounded.Issues) != 1 || unbounded.Issues[0].Occurrences != 5 {
		t.Fatalf("unbounded = %+v, want one issue across all 5 runs", unbounded.Issues)
	}
	limited, err := st.IssuesForQuery(IssueQuery{Window: Window{Limit: 2}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Issues) != 1 || limited.Issues[0].Occurrences != 2 {
		t.Fatalf("limit 2 = %+v, want the same issue reported by the 2 newest runs", limited.Issues)
	}
	if limited.Window != "last 2 runs" {
		t.Errorf("window label = %q, want it to state the limit the query actually used", limited.Window)
	}
}

// A window of one run is the default the issues page now ships: it reads "last
// 1 run", never the "last 1 run(s)" no other sentence in the UI uses.
func TestWindowLabelIsSingularForOneRun(t *testing.T) {
	if got := (Window{Limit: 1}).String(); got != "last 1 run" {
		t.Errorf("single-run label = %q, want \"last 1 run\"", got)
	}
}

func TestIssuesWindowLimitZeroMeansEveryRun(t *testing.T) {
	st := openTestStore(t)
	seedIssues(t, st)
	got, err := st.IssuesForQuery(IssueQuery{Window: Window{Limit: 0}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 || got.Issues[0].Occurrences != 3 {
		t.Fatalf("limit 0 = %+v, want every run in scope (a zero limit is 'all')", got.Issues)
	}
}

func TestIssuesWindowLimitKeepsProjectAndUnassignedScope(t *testing.T) {
	st := openTestStore(t)
	for _, root := range []string{"/repo", ""} {
		sum := &model.RunSummary{Files: 1, Findings: []model.Finding{
			fp("todo-comment", "b.go", 4, "hashA"),
		}}
		_, err := st.SaveRunForProject(sum, "test", "commit", root)
		if err != nil {
			t.Fatal(err)
		}
	}
	scoped, err := st.IssuesForQuery(IssueQuery{Window: Window{Limit: 1, ProjectRoot: "/repo"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.Issues) != 1 || scoped.Issues[0].Occurrences != 1 {
		t.Fatalf("project scope = %+v, want only that project's run", scoped.Issues)
	}
	unassigned, err := st.IssuesForQuery(IssueQuery{Window: Window{Limit: 1, Unassigned: true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(unassigned.Issues) != 1 || unassigned.Issues[0].Occurrences != 1 {
		t.Fatalf("unassigned scope = %+v, want only the run with no project root", unassigned.Issues)
	}
}

// Archive mode 2 ("both archived and live") is what this query has always used,
// and the issues list must keep answering for archived runs: archiving is a
// dashboard-level filing decision, and a triage list that quietly hides every
// archived run is a list that disagrees with the gate.
func TestIssuesCandidatesIncludeArchivedRuns(t *testing.T) {
	st := openTestStore(t)
	sum := &model.RunSummary{Files: 1, Findings: []model.Finding{fp("todo-comment", "b.go", 4, "hashA")}}
	runID, err := st.SaveRunForProject(sum, "test", "commit", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetArchived(runID, "/repo", true); err != nil {
		t.Fatalf("SetArchived: %v", err)
	}
	got, err := st.IssuesForQuery(IssueQuery{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 || got.Issues[0].Occurrences != 1 {
		t.Fatalf("archived run = %+v, want the archived run still in scope", got.Issues)
	}
}
