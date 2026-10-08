package calllog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func rec(session, tool, verdict string, ms int64) Record {
	return Record{Session: session, Tool: tool, Verdict: verdict, MS: ms}
}

func TestAppendReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.jsonl")
	in := []Record{
		rec("s1", "get_constraints", "", 12),
		rec("s1", "check_code", "FAIL: fix the errors", 3),
		rec("s1", "check_code", "PASS", 2),
	}
	appendAll(t, path, in)

	got, skipped, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if skipped != 0 || len(got) != len(in) {
		t.Fatalf("read %d records / %d skipped, want %d / 0", len(got), skipped, len(in))
	}
	for i := range in {
		assertSameCall(t, i, got[i], in[i])
	}
}

func appendAll(t *testing.T, path string, recs []Record) {
	t.Helper()
	for _, r := range recs {
		if err := Append(path, r); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
}

// assertSameCall checks that a round-tripped record kept its payload, picked up
// the phase its tool implies, and was stamped by Append.
func assertSameCall(t *testing.T, i int, got, want Record) {
	t.Helper()
	if got.Tool != want.Tool || got.Verdict != want.Verdict || got.MS != want.MS {
		t.Errorf("record %d = %+v, want %+v", i, got, want)
	}
	if got.Phase != PhaseOf(want.Tool) {
		t.Errorf("record %d phase = %q, want %q", i, got.Phase, PhaseOf(want.Tool))
	}
	if got.Time.IsZero() {
		t.Errorf("record %d has no timestamp: Append must stamp it", i)
	}
}

// TestAppendEmptyPathIsNoop pins the "off by default" property: an unset log
// path must not create a file anywhere.
func TestAppendEmptyPathIsNoop(t *testing.T) {
	if err := Append("", rec("s1", "check_code", "PASS", 1)); err != nil {
		t.Fatalf("Append(\"\"): %v", err)
	}
}

// TestReadSurvivesTornTail covers the case that motivated the JSONL format: a
// process killed mid-write leaves a partial line, and losing the whole log
// because of it would be worse than losing one call.
func TestReadSurvivesTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.jsonl")
	if err := Append(path, rec("s1", "check_code", "PASS", 1)); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"tool":"check_path","ver`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	got, skipped, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 1 || skipped != 1 {
		t.Fatalf("got %d records / %d skipped, want 1 / 1", len(got), skipped)
	}
}

// TestSummarizeConvergence is the measurement that decides whether the gate
// earns its place: session s1 is flagged and then repairs to a pass, s2 is
// flagged and never does, s3 is clean throughout.
func TestSummarizeConvergence(t *testing.T) {
	s := Summarize(convergenceLog())

	if s.Records != 5 || s.Sessions != 3 {
		t.Fatalf("records/sessions = %d/%d, want 5/3", s.Records, s.Sessions)
	}
	assertPhaseCounts(t, s)
	assertVerifyCounts(t, s)
	if s.MedianMS["check_code"] != 3 {
		t.Errorf("median ms for check_code = %d, want 3", s.MedianMS["check_code"])
	}
}

// convergenceLog is one flagged-and-repaired task, one flagged task, and one
// clean task.
func convergenceLog() []Record {
	base := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	return []Record{
		{Session: "s1", Tool: "get_constraints", Time: base, MS: 10},
		{Session: "s1", Tool: "check_code", Verdict: "FAIL: fix the errors", Time: base.Add(time.Second), MS: 3},
		{Session: "s1", Tool: "check_code", Verdict: "PASS", Time: base.Add(2 * time.Second), MS: 2},
		{Session: "s2", Tool: "check_code", Verdict: "WARN: fix the warnings before submitting", Time: base.Add(3 * time.Second), MS: 4},
		{Session: "s3", Tool: "check_path", Verdict: "PASS", Time: base.Add(4 * time.Second), MS: 40},
	}
}

func assertPhaseCounts(t *testing.T, s Summary) {
	t.Helper()
	if s.ByPhase[string(PhaseVerify)] != 4 || s.ByPhase[string(PhaseBefore)] != 1 {
		t.Errorf("by_phase = %v, want 4 verify / 1 before", s.ByPhase)
	}
}

func assertVerifyCounts(t *testing.T, s Summary) {
	t.Helper()
	v := s.Verify
	if v.Calls != 4 || v.Flagged != 2 || v.Clean != 2 {
		t.Fatalf("verify = %+v, want 4 calls / 2 flagged / 2 clean", v)
	}
	if v.InterceptRate != 0.5 {
		t.Errorf("intercept_rate = %v, want 0.5", v.InterceptRate)
	}
	if v.Tasks != 3 || v.TasksFlagged != 2 || v.TasksResolved != 1 || v.CleanFirstTry != 1 {
		t.Errorf("tasks = %+v, want 3 tasks / 2 flagged / 1 resolved / 1 clean-first-try", v)
	}
	if v.ConvergeRate != 0.5 {
		t.Errorf("converge_rate = %v, want 0.5", v.ConvergeRate)
	}
}

// TestSummarizeCountsErrorsAsNotPassed: a tool call that returned an error is
// not evidence the code was fine, so it must not be scored as a pass.
func TestSummarizeCountsErrorsAsNotPassed(t *testing.T) {
	recs := []Record{
		{Session: "s1", Tool: "check_code", Err: "engine: no such path", Verdict: "PASS"},
	}
	s := Summarize(recs)
	if s.ToolError != 1 {
		t.Errorf("tool_errors = %d, want 1", s.ToolError)
	}
	if s.Verify.Clean != 0 || s.Verify.Flagged != 1 {
		t.Errorf("verify = %+v, want the errored call counted as flagged", s.Verify)
	}
}

// TestSummarizeEmptyDenominatorsAreZero: with no verify calls at all, both
// rates are 0 rather than NaN, and the caller can tell by reading the counts.
func TestSummarizeEmptyDenominatorsAreZero(t *testing.T) {
	s := Summarize([]Record{{Session: "s1", Tool: "get_repo_map"}})
	if s.Verify.Calls != 0 || s.Verify.ConvergeRate != 0 || s.Verify.InterceptRate != 0 {
		t.Fatalf("verify = %+v, want zeroed rates with zero calls", s.Verify)
	}
	if s.ByPhase[string(PhaseExplore)] != 1 {
		t.Errorf("by_phase = %v, want 1 explore", s.ByPhase)
	}
}

// TestPhaseOfUnknownToolIsOther keeps an unclassified tool visible instead of
// letting it fall through into a phase it does not belong to.
func TestPhaseOfUnknownToolIsOther(t *testing.T) {
	if got := PhaseOf("brand_new_tool"); got != PhaseOther {
		t.Fatalf("PhaseOf(unknown) = %q, want %q", got, PhaseOther)
	}
	if IsVerify("brand_new_tool") {
		t.Error("an unclassified tool must not count as a verify call")
	}
}

func TestFilterSince(t *testing.T) {
	base := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	recs := []Record{
		{Session: "s1", Tool: "check_code", Time: base},
		{Session: "s2", Tool: "check_code", Time: base.Add(48 * time.Hour)},
	}
	if got := FilterSince(recs, time.Time{}); len(got) != 2 {
		t.Errorf("zero since kept %d records, want 2", len(got))
	}
	if got := FilterSince(recs, base.Add(24*time.Hour)); len(got) != 1 || got[0].Session != "s2" {
		t.Errorf("filtered = %+v, want only s2", got)
	}
}

func TestParseSince(t *testing.T) {
	if _, err := ParseSince(""); err != nil {
		t.Errorf("empty since must mean no filter, got %v", err)
	}
	if _, err := ParseSince("2026-09-28"); err != nil {
		t.Errorf("date-only since rejected: %v", err)
	}
	if _, err := ParseSince("2026-09-28T10:00:00Z"); err != nil {
		t.Errorf("RFC 3339 since rejected: %v", err)
	}
	_, err := ParseSince("last tuesday")
	if err == nil || !strings.Contains(err.Error(), "unrecognised") {
		t.Errorf("garbage since = %v, want an explicit rejection", err)
	}
}
