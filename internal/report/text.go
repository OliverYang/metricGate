package report

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"metricgate/internal/bundle"
	"metricgate/internal/config"
	"metricgate/internal/model"
)

// Text renders the human-readable report.
func Text(s *model.RunSummary, cfg *config.Config, toolVersion string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "metricgate %s — %d files, %s\n\n", toolVersion, s.Files, s.Duration.Round(time.Millisecond))
	for _, f := range s.Findings {
		fmt.Fprintf(&b, "  %s:%d: [%s] %s (%s)", f.File, f.StartLine, f.Severity, f.RuleID, f.Message)
		if f.Accepted {
			fmt.Fprintf(&b, "  [accepted by ledger]")
		}
		b.WriteByte('\n')
	}
	if len(s.ParseErrors) > 0 {
		fmt.Fprintf(&b, "\n  parse errors: %s\n", strings.Join(s.ParseErrors, ", "))
	}
	fmt.Fprintf(&b, "\nsummary: %d errors, %d warnings, %d notes, %d metric snapshots\n",
		s.Count(model.SevError), s.Count(model.SevWarning), s.Count(model.SevInfo), len(s.Metrics))
	if s.Ignored > 0 {
		fmt.Fprintf(&b, "note: %d findings suppressed by metricgate: ignore directives\n", s.Ignored)
	}
	return b.String()
}

// JSON renders the raw summary for tool-to-tool consumption (this is the
// payload the LLM feedback loop will read in v2).
func JSON(s *model.RunSummary) ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// Verdict is the gate's answer to one run, in machine-readable form.
//
// It travels with the JSON because the counts a consumer can see (severity
// tallies in the findings list) are not the number the gate acted on: the
// level is chosen by policy (fail_on), and ledger-accepted warnings do not
// count. A script that reads the findings has to re-implement that rule to
// know whether the run passed; a verdict field simply tells it.
type Verdict struct {
	Passed bool   `json:"passed"`
	FailOn string `json:"failOn"`
	// Trips is how many findings the gate counted against the threshold —
	// the number the pass/fail decision is made on, not the severity totals.
	Trips int `json:"trips"`
	// Reason is the one-line justification, so a log line shows why.
	Reason string `json:"reason,omitempty"`
	// Accepted and Suppressed carry the two concession systems forward: a JSON
	// consumer that filters findings loses them otherwise, and "the gate
	// passed" must remain distinguishable from "nothing was ever questioned".
	Accepted   int `json:"accepted,omitempty"`
	Suppressed int `json:"suppressed,omitempty"`
}

// VerdictOf computes the verdict for one run. The CLI enforces the same
// function, so the printed gate line and the JSON field cannot disagree.
func VerdictOf(s *model.RunSummary, cfg *config.Config) Verdict {
	failOn := cfg.FailOn()
	trips := s.Count(model.SevError)
	if failOn == model.SevWarning {
		trips += s.Count(model.SevWarning)
		for _, f := range s.Findings {
			if f.Severity == model.SevWarning && f.Accepted {
				trips--
			}
		}
	}
	v := Verdict{Passed: trips == 0, FailOn: string(failOn), Trips: trips, Suppressed: s.Ignored}
	for _, f := range s.Findings {
		if f.Accepted {
			v.Accepted++
		}
	}
	if trips > 0 {
		v.Reason = fmt.Sprintf("%d finding(s) at or above fail_on=%s", trips, failOn)
	} else {
		v.Reason = fmt.Sprintf("no finding at or above fail_on=%s", failOn)
	}
	return v
}

// Envelope is the JSON form of one run: the summary's own fields plus the
// severity tallies and the verdict the gate reached.
//
// The tallies exist because counting them out of the findings list is exactly
// what a consumer should not have to do — the run already knows the answer.
type Envelope struct {
	*model.RunSummary
	Errors   int     `json:"errors"`
	Warnings int     `json:"warnings"`
	Verdict  Verdict `json:"verdict"`
	// Policy is the fingerprint of the policy document that produced these
	// verdicts. It travels with the run for the same reason the store keeps
	// it: a finding means nothing once the rules can change, so a consumer
	// must be able to ask "under which policy" without re-running.
	Policy *Policy `json:"policy,omitempty"`
	// Run is the provenance of the run itself — the trace id and the run id.
	// It is omitted entirely when there is nothing to attribute, so a consumer
	// that has neither never sees a new key appear. See Run.
	Run *Run `json:"run,omitempty"`
}

// Policy is the machine-readable provenance of the rule set in force.
type Policy struct {
	Digest string `json:"digest"`
	Source string `json:"source"`
}

// Run is the machine-readable provenance of the run itself: the keys that tie
// this verdict to a unit of work and to a row in the run history.
//
// The trace is what groups the calls a host made while producing this change,
// and the run id is the row this run occupies in the store. Both are omitted
// when empty — a hand-run check has neither, and an envelope full of empty
// strings tells a reader nothing — but a host that has the id can point back
// at the run without a second invocation.
type Run struct {
	TraceID string `json:"traceId,omitempty"`
	RunID   string `json:"runId,omitempty"`
	Extends string `json:"extends,omitempty"`
}

// EnvelopeOf builds the JSON envelope for one run.
func EnvelopeOf(s *model.RunSummary, cfg *config.Config) Envelope {
	out := Envelope{
		RunSummary: s,
		Errors:     s.Count(model.SevError),
		Warnings:   s.Count(model.SevWarning),
		Verdict:    VerdictOf(s, cfg),
	}
	if cfg != nil {
		out.Policy = &Policy{Digest: bundle.PolicyDigest(cfg), Source: bundle.PolicySourceLabel(cfg)}
	}
	out.Run = runProvenance(cfg, "")
	return out
}

// runProvenance assembles the run's own provenance, or nil when there is
// nothing to attribute: a hand-run check whose run was not stored and which
// carries no trace emits no "run" object at all. An object full of empty
// strings would tell a reader nothing while still being a new key in the
// document, and "no new keys unless the feature is in use" is what keeps an
// unset METRICGATE_TRACE_ID byte-compatible with the previous behaviour.
func runProvenance(cfg *config.Config, runID string) *Run {
	out := Run{RunID: runID, TraceID: traceID()}
	if cfg != nil && len(cfg.Extends()) > 0 {
		out.Extends = strings.Join(extendsPaths(cfg), ",")
	}
	if out == (Run{}) {
		return nil
	}
	return &out
}

// traceID returns the ambient trace id, or "" when the operator set none. An
// empty trace is a legal state: it means the host chose not to attribute this
// run to a unit of work, which is not the same as the run not existing.
func traceID() string {
	return strings.TrimSpace(os.Getenv("METRICGATE_TRACE_ID"))
}

// extendsPaths lists the documents the policy inherited from, nearest first,
// so a consumer can tell "these thresholds came from the org policy" from
// "this repository wrote them itself".
func extendsPaths(cfg *config.Config) []string {
	out := make([]string, 0, len(cfg.Extends()))
	for _, p := range cfg.Extends() {
		out = append(out, p.Path)
	}
	return out
}

// JSONEnvelope renders the run as the machine-readable envelope.
func JSONEnvelope(s *model.RunSummary, cfg *config.Config) ([]byte, error) {
	return json.MarshalIndent(EnvelopeOf(s, cfg), "", "  ")
}

// JSONEnvelopeWithRun renders the envelope with the run id stamped on.
//
// It exists as a separate entry point rather than a changed signature because
// the run id is the one field the report layer cannot know on its own: it is
// assigned by the store, after the analysis that produced the summary. Keeping
// it out of JSONEnvelope means the ordinary path stays byte-identical for a
// run that is not stored, and the caller that stored the run opts in.
func JSONEnvelopeWithRun(s *model.RunSummary, cfg *config.Config, runID string) ([]byte, error) {
	out := EnvelopeOf(s, cfg)
	out.Run = runProvenance(cfg, runID)
	return json.MarshalIndent(out, "", "  ")
}
