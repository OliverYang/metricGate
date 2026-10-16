// Package calllog records local MCP tool-call traffic, so the one question
// that decides whether this tool earns its place in an agent's loop — "is it
// actually called, and does calling it change the outcome?" — can be answered
// with numbers instead of prose.
//
// Three constraints, in order of precedence:
//
//  1. Never record source code. A record carries the tool name, a locator
//     (path / query / name), timing and finding counts — never the `code`
//     argument a caller passed in. That is what makes the file safe to leave
//     on disk next to the repository it describes.
//  2. Local only, off by default. Nothing here opens a socket. The writer
//     appends to one file the operator named; with no file named, nothing is
//     written at all. This is not telemetry, and it is not "telemetry that
//     happens to be local" — there is no remote endpoint to enable later
//     without a deliberate change to this package.
//  3. Never break a tool call. Instrumentation that can fail a check is worse
//     than no instrumentation, so Append's errors are the caller's to ignore
//     and a torn last line is the reader's to skip.
//
// The format is JSON Lines: one JSON object per call, appended. That keeps the
// write path to a single O_APPEND write and lets a reader survive a process
// that died mid-record.
package calllog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// DefaultPath is the log location used when logging is enabled without an
// explicit path. It sits under .metricgate/, the directory that already holds
// history.db and is already gitignored, so the log never becomes a committed
// artifact.
const DefaultPath = ".metricgate/mcp-calls.jsonl"

// Phase is the point in an agent's loop where a tool is normally called.
// Counting calls by phase is what separates "the agent used the gate to verify
// its work" from "the agent paged through the repository" — a distinction the
// raw call count hides.
type Phase string

const (
	// PhaseBefore is grounding for code that has not been written yet.
	PhaseBefore Phase = "before"
	// PhaseVerify is the gate itself: a deterministic verdict on code that
	// exists (in memory or on disk).
	PhaseVerify Phase = "verify"
	// PhaseReview is a delegated review input for a host agent.
	PhaseReview Phase = "review"
	// PhaseExplore is orientation: which files matter, what is unfinished,
	// what would break.
	PhaseExplore Phase = "explore"
	// PhaseOther is the phase of a tool nobody classified. It exists so that
	// adding a tool and forgetting to classify it shows up as "other" in the
	// summary instead of silently vanishing from the phase table.
	PhaseOther Phase = "other"
)

// toolPhases classifies every tool on the MCP surface. The map is deliberately
// total: PhaseOf falls back to PhaseOther rather than guessing, so a new tool
// is visible in the summary on the first run after it is added.
var toolPhases = map[string]Phase{
	"get_constraints":   PhaseBefore,
	"find_symbols":      PhaseBefore,
	"check_code":        PhaseVerify,
	"check_path":        PhaseVerify,
	"get_review_bundle": PhaseReview,
	"get_repo_map":      PhaseExplore,
	"get_impact":        PhaseExplore,
	"get_hotspots":      PhaseExplore,
	"get_trends":        PhaseExplore,
	"get_trace":         PhaseExplore,
	"get_metric_stats":  PhaseExplore,
	"list_stubs":        PhaseExplore,
}

// PhaseOf returns the phase a tool belongs to.
func PhaseOf(tool string) Phase {
	if p, ok := toolPhases[tool]; ok {
		return p
	}
	return PhaseOther
}

// IsVerify reports whether a tool renders a gate verdict. The convergence
// numbers below are computed only over these tools: a `get_repo_map` call
// cannot fail, so counting it as a passed check would inflate the fix rate.
func IsVerify(tool string) bool { return PhaseOf(tool) == PhaseVerify }

// Record is one MCP tool call. Field names are short because the file is read
// by eye as often as by this package.
type Record struct {
	Time time.Time `json:"time"`
	// Session is the process that produced this call, so a log assembled from
	// two interleaved servers still yields per-session numbers.
	Session string `json:"session"`
	// TraceID groups calls that belong to the same unit of work — one LLM
	// session, one PR, one task — across however many processes it took. It is
	// read from $METRICGATE_TRACE_ID and defaults to empty, which is a legal
	// state: "no trace" must be indistinguishable from "no data" only in that
	// both are aggregated under everything else, never silently dropped.
	TraceID     string `json:"traceId,omitempty"`
	Tool        string `json:"tool"`
	Phase       Phase  `json:"phase"`
	Target      string `json:"target,omitempty"` // locator only, never source
	ProjectID   string `json:"projectId,omitempty"`
	ProjectPath string `json:"projectPath,omitempty"`
	ProjectName string `json:"projectName,omitempty"`
	MS          int64  `json:"ms"`
	Findings    int    `json:"findings,omitempty"`
	Errors      int    `json:"errors,omitempty"`
	Warnings    int    `json:"warnings,omitempty"`
	Verdict     string `json:"verdict,omitempty"`
	Err         string `json:"error,omitempty"`
}

// Passed reports whether a verify record's verdict was a clean pass. A record
// that errored is never a pass: an infrastructure failure must not be counted
// as evidence that the code was fine.
func (r Record) Passed() bool {
	return r.Err == "" && strings.HasPrefix(r.Verdict, "PASS")
}

// normalized fills in the fields a caller may have left out, so every path
// that reads or reduces a record agrees on what it means. Records written by
// Append are already complete; this exists for records built in memory, e.g.
// by a test or by a future non-MCP producer.
func (r Record) normalized() Record {
	if r.Phase == "" {
		r.Phase = PhaseOf(r.Tool)
	}
	return r
}

// Append writes one record as a JSON line, creating the file if needed. The
// mode is 0600: the log names the paths an agent touched, which is nobody
// else's business on a shared machine.
//
// A zero path is a no-op, so callers do not need to branch on "is logging on".
func Append(path string, r Record) error {
	if path == "" {
		return nil
	}
	r = r.normalized()
	if r.Time.IsZero() {
		r.Time = time.Now().UTC()
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// Read parses the log in file order. It returns the number of lines it could
// not parse instead of failing: the tail of an appended file can be torn when a
// process dies mid-write, and one truncated byte must not hide every earlier
// call. A caller that cares can print the count.
func Read(path string) (recs []Record, skipped int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			skipped++
			continue
		}
		recs = append(recs, r.normalized())
	}
	if err := sc.Err(); err != nil {
		return recs, skipped, err
	}
	return recs, skipped, nil
}

// VerifyStats is the part of the summary that answers "did the gate catch
// anything, and did the agent then fix it?".
//
// The task-level numbers treat one MCP server process as one task. That is an
// approximation with two known failure modes, stated here so nobody reads more
// into the ratio than it can carry: a session that runs several independent
// tasks is counted once, and a session that ends before the agent finishes
// repairing counts as unresolved even though the gate did its job.
type VerifyStats struct {
	Calls         int     `json:"calls"`
	Clean         int     `json:"clean"`
	Flagged       int     `json:"flagged"`
	InterceptRate float64 `json:"intercept_rate"`
	Tasks         int     `json:"tasks"`
	TasksFlagged  int     `json:"tasks_flagged"`
	TasksResolved int     `json:"tasks_resolved"`
	ConvergeRate  float64 `json:"converge_rate"`
	CleanFirstTry int     `json:"clean_first_try"`
}

// Summary is a log reduced to the numbers that matter.
type Summary struct {
	Log       string         `json:"log"`
	Records   int            `json:"records"`
	Skipped   int            `json:"skipped,omitempty"`
	Sessions  int            `json:"sessions"`
	First     time.Time      `json:"first"`
	Last      time.Time      `json:"last"`
	ByTool    map[string]int `json:"by_tool"`
	ByPhase   map[string]int `json:"by_phase"`
	MedianMS  map[string]int `json:"median_ms_by_tool"`
	ToolError int            `json:"tool_errors"`
	Verify    VerifyStats    `json:"verify"`
}

// Summarize reduces records to a Summary. Records are ordered by time first,
// so a log assembled from two interleaved processes still yields per-session
// sequences in the order they happened.
func Summarize(recs []Record) Summary {
	ordered := append([]Record(nil), recs...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Time.Before(ordered[j].Time) })

	s := Summary{
		Records:  len(ordered),
		ByTool:   map[string]int{},
		ByPhase:  map[string]int{},
		MedianMS: map[string]int{},
	}
	durations := map[string][]int64{}
	sessions := map[string]bool{}
	for _, raw := range ordered {
		r := raw.normalized()
		if s.First.IsZero() || r.Time.Before(s.First) {
			s.First = r.Time
		}
		if r.Time.After(s.Last) {
			s.Last = r.Time
		}
		s.ByTool[r.Tool]++
		s.ByPhase[string(r.Phase)]++
		durations[r.Tool] = append(durations[r.Tool], r.MS)
		sessions[r.Session] = true
		if r.Err != "" {
			s.ToolError++
		}
	}
	s.Sessions = len(sessions)
	for tool, ds := range durations {
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		s.MedianMS[tool] = percentile(ds, 0.5)
	}
	s.Verify = summarizeVerify(ordered)
	return s
}

// summarizeVerify computes the intercept and convergence numbers. Both are
// defined over the *verdict the gate already returns*, not over a finding
// count: a PASS carrying three info-level notes is a pass, and re-deriving
// that judgement here would let the summary drift from the gate.
func summarizeVerify(ordered []Record) VerifyStats {
	var s VerifyStats
	// One entry per session: whether it ever saw a non-pass, and whether its
	// last verify call was a pass.
	type task struct {
		flagged  bool
		lastPass bool
		cleanAll bool
	}
	tasks := map[string]*task{}
	var order []string
	for _, r := range ordered {
		if !IsVerify(r.Tool) {
			continue
		}
		s.Calls++
		t := tasks[r.Session]
		if t == nil {
			t = &task{cleanAll: true}
			tasks[r.Session] = t
			order = append(order, r.Session)
		}
		if r.Passed() {
			s.Clean++
		} else {
			s.Flagged++
			t.flagged = true
			t.cleanAll = false
		}
		t.lastPass = r.Passed()
	}
	s.InterceptRate = ratio(s.Flagged, s.Calls)
	for _, id := range order {
		t := tasks[id]
		s.Tasks++
		if t.cleanAll {
			s.CleanFirstTry++
			continue
		}
		s.TasksFlagged++
		if t.lastPass {
			s.TasksResolved++
		}
	}
	s.ConvergeRate = ratio(s.TasksResolved, s.TasksFlagged)
	return s
}

// ratio returns num/den as a fraction in [0,1], or 0 when den is 0. Reporting
// 0% for "no data" is a lie the caller has to correct by checking the
// denominator, so both rates are always printed next to their counts.
func ratio(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// percentile returns the nearest-rank percentile of an already-sorted slice,
// matching the convention the metric distributions use elsewhere in this
// repository. An empty slice is 0.
func percentile(sorted []int64, p float64) int {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(p*float64(len(sorted))+0.5) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return int(sorted[rank])
}

// FilterSince keeps records at or after t. A zero t keeps everything, which is
// how the CLI expresses "no -since flag".
func FilterSince(recs []Record, t time.Time) []Record {
	if t.IsZero() {
		return recs
	}
	out := recs[:0:0]
	for _, r := range recs {
		if !r.Time.Before(t) {
			out = append(out, r)
		}
	}
	return out
}

// ParseSince accepts the two forms a person actually types for a date
// cutoff — "2006-01-02" and a full RFC 3339 timestamp — and rejects anything
// else rather than silently treating it as "no filter".
func ParseSince(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised -since %q: want 2006-01-02 or an RFC 3339 timestamp", s)
}
