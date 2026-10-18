package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"metricgate/internal/config"
	"metricgate/internal/store"
)

// These tests pin the guard, not the delete. Every one of them calls runPrune
// directly rather than through mustPrune: mustPrune answers the confirmation
// prompt on the test's behalf, and the point here is what happens when that
// prompt is refused, unanswered, or never reached.
//
// The round that produced the guard deleted 78 runs from a live development
// database because `store prune -runs 20` was run from a repository root, found
// that repository's .metricgate.yaml, and deleted from the store.path it named —
// with no preview and no confirmation. Each test below is one of the doors that
// now stands in that path.

// TestRealPruneRefusesBuiltinPolicySource is D2 the guard: a bound that no file
// named must not delete, and -yes must not unlock it. The flag answers "don't ask
// me"; the question here is "which file?", and a flag that silences a prompt
// cannot answer that.
func TestRealPruneRefusesBuiltinPolicySource(t *testing.T) {
	g := newGuardStore(t, 0)
	seedRuns(t, g.st, 6)
	cfg := g.withBuiltinSource(t)

	for _, yes := range []bool{false, true} {
		restore := withPruneStdin(strings.NewReader("y\n"), true)
		_, err := runPrune(cfg, pruneFlags{runs: 2, format: "text", yes: yes})
		restore()
		if err == nil {
			t.Fatalf("-yes=%v: a built-in source deleted history; the guard is not enforcing the source", yes)
		}
		if !strings.Contains(err.Error(), "no policy file was named") {
			t.Errorf("-yes=%v error = %v, want the naming refusal", yes, err)
		}
		if !strings.Contains(err.Error(), "-dry-run") || !strings.Contains(err.Error(), "-config") {
			t.Errorf("-yes=%v refusal offers no next step:\n%v", yes, err)
		}
	}
	if got := g.runs(t); got != 6 {
		t.Errorf("runs = %d after two refusals, want 6 (nothing may be removed)", got)
	}
	if _, found := g.intent(t); found {
		t.Error("an intent record was written by a prune that was refused")
	}
}

// TestRealPruneAsksBeforeDeleting covers the non-interactive case: a prune in a
// pipeline or a CI job has nobody at the prompt, so it must refuse and say how to
// proceed deliberately, not delete because no one was there to object.
func TestRealPruneAsksBeforeDeleting(t *testing.T) {
	g := newGuardStore(t, 2)
	seedRuns(t, g.st, 6)

	// interactive=false is the state a CI step or a pipeline produces. The second
	// row is the one that matters: `yes | metricgate store prune` feeds a
	// confirmable answer to a process nobody is watching, so the guard must refuse
	// on the *stdin kind*, not on whether some bytes happen to look like yes.
	for _, stdin := range []string{"", "y\n"} {
		restore := withPruneStdin(strings.NewReader(stdin), false)
		_, err := runPrune(g.cfg, pruneFlags{runs: unsetBound, days: unsetBound, format: "text"})
		restore()
		if err == nil {
			t.Fatalf("stdin %q: a non-interactive real prune with no -yes deleted instead of refusing", stdin)
		}
		if !strings.Contains(err.Error(), "-yes") {
			t.Errorf("stdin %q: refusal does not name the way forward: %v", stdin, err)
		}
		if got := g.runs(t); got != 6 {
			t.Fatalf("stdin %q: runs = %d, want 6 — a refused prune must not have deleted", stdin, got)
		}
	}
}

// TestRealPruneProceedsOnYesAnswer is the confirmation path: a yes answer deletes,
// the store is left holding the bound, and the audit record exists.
func TestRealPruneProceedsOnYesAnswer(t *testing.T) {
	g := newGuardStore(t, 2)
	seedRuns(t, g.st, 6)

	out := confirmWithYes(t, g)
	if out.report.Runs != 4 {
		t.Errorf("removed %d runs, want 4 (6 seeded, keep 2)", out.report.Runs)
	}
	if got := g.runs(t); got != 2 {
		t.Errorf("runs = %d after a confirmed prune keeping 2, want 2", got)
	}
	if _, found := g.intent(t); !found {
		t.Fatal("a real prune left no audit record")
	}
}

// TestPruneIntentEqualsTheDelete is the audit half of the same pass, asserted on
// its own: the record's declared counts must equal the removed counts field for
// field, and both must equal the report the command returned.
//
// On a store this test owns, with no concurrent writer, an exact match is the
// contract — the numbers the operator approved are the numbers that were deleted,
// and the record is the only place that pairing survives the process that did it.
func TestPruneIntentEqualsTheDelete(t *testing.T) {
	g := newGuardStore(t, 2)
	seedRuns(t, g.st, 6)

	out := confirmWithYes(t, g)
	intent, found := g.intent(t)
	if !found {
		t.Fatal("no audit record to compare")
	}
	want := store.ReportJSONOf(out.report)
	if intent.Declared != want {
		t.Errorf("declared %+v, want the removed counts %+v (no concurrent writer, so they must agree)", intent.Declared, want)
	}
	if intent.Removed != want {
		t.Errorf("removed %+v, want %+v", intent.Removed, want)
	}
	if intent.Declared.Runs != 4 {
		t.Errorf("declared %d runs removed, want 4 (6 seeded, keep 2)", intent.Declared.Runs)
	}
	if intent.StorePath != g.path {
		t.Errorf("record path = %q, want %q", intent.StorePath, g.path)
	}
	// The record names the policy that located the store, because that is the
	// thing the guard was built to make visible.
	if intent.Source != policyFileSource {
		t.Errorf("record source = %q, want %q", intent.Source, policyFileSource)
	}
	if intent.At.IsZero() {
		t.Error("the audit record has no timestamp")
	}
}

// TestPruneReplayAfterConfirmFindsNothingLeft is the reconciliation an operator
// runs next: replaying the same command line as a dry run after the confirmed
// delete must find nothing more to remove. A nonzero answer means the prune did
// not do what the declaration said it would.
func TestPruneReplayAfterConfirmFindsNothingLeft(t *testing.T) {
	g := newGuardStore(t, 2)
	seedRuns(t, g.st, 6)
	confirmWithYes(t, g)

	restore := withPruneStdin(strings.NewReader(""), false)
	replay, err := runPrune(g.cfg, pruneFlags{dryRun: true, runs: unsetBound, days: unsetBound, format: "text"})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	if replay.report.Runs != 0 {
		t.Errorf("dry-run replay after the prune wants to remove %d more runs, want 0", replay.report.Runs)
	}
}

// TestRealPruneSkipsPromptWithYesFlag pins what -yes actually buys: it does not
// read stdin at all. A reader that fails the test on the first read is the only
// way to show that, because "the prompt was not shown" is invisible in the
// return value.
func TestRealPruneSkipsPromptWithYesFlag(t *testing.T) {
	g := newGuardStore(t, 2)
	seedRuns(t, g.st, 5)

	out, text := pruneWithYes(t, g)
	if out.report.Runs != 3 {
		t.Errorf("removed %d runs, want 3 (5 seeded, keep 2)", out.report.Runs)
	}
	if _, found := g.intent(t); !found {
		t.Error("-yes skipped the prompt and the audit record too; -yes must skip only the prompt")
	}
	// The declaration survives the flag: what was going to be removed, and from
	// which file. A scripted prune is still a deletion, and the one place an
	// operator can catch a wrong store.path is the line printed before it.
	for _, want := range []string{"about to remove 3 runs", g.path} {
		if !strings.Contains(text, want) {
			t.Errorf("-yes suppressed %q from the pre-delete declaration:\n%s", want, text)
		}
	}
	if strings.Contains(text, "remove this history?") {
		t.Errorf("-yes still asked, which is the case a CI job hangs on:\n%s", text)
	}
}

// TestRealPruneAnswersNoDeletesNothing is the other half of the prompt: any
// answer that is not yes cancels, including a bare Enter and including "no".
func TestRealPruneAnswersNoDeletesNothing(t *testing.T) {
	g := newGuardStore(t, 2)
	seedRuns(t, g.st, 6)

	for _, answer := range []string{"\n", "n\n", "no\n", "maybe\n", ""} {
		restore := withPruneStdin(strings.NewReader(answer), true)
		_, err := runPrune(g.cfg, pruneFlags{runs: unsetBound, days: unsetBound, format: "text"})
		restore()
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Errorf("answer %q: err = %v, want a cancellation", answer, err)
		}
		if got := g.runs(t); got != 6 {
			t.Fatalf("answer %q left %d runs, want 6", answer, got)
		}
	}
}

// TestPruneIntentRecordsAMismatch is the reconciliation warning: when the delete
// touches a different number of runs than was declared, both numbers survive in
// the record and the text report says so. A concurrent `check` inserting a run
// mid-prune is a legitimate cause, which is exactly why the pair has to be kept
// instead of one of them being overwritten.
func TestPruneIntentRecordsAMismatch(t *testing.T) {
	g := newGuardStore(t, 0)
	seedRuns(t, g.st, 6)

	// The store-level pair is what the CLI writes, so the mismatch is injected
	// the way a concurrent writer would produce it: declared and removed differ
	// by one run.
	declared := store.PruneReport{Runs: 4, Findings: 4, Snapshots: 4, Kept: 2}
	removed := store.PruneReport{Runs: 5, Findings: 5, Snapshots: 5, Kept: 1}
	if err := g.st.RecordPruneIntent(store.PruneIntent{
		At:       time.Now().UTC(),
		Source:   "policy_file",
		Declared: store.ReportJSONOf(declared),
		Removed:  store.ReportJSONOf(removed),
	}); err != nil {
		t.Fatal(err)
	}
	intent, found := g.intent(t)
	if !found {
		t.Fatal("the mismatching pair was not recorded")
	}
	if intent.Declared.Runs != 4 || intent.Removed.Runs != 5 {
		t.Errorf("recorded declared=%d removed=%d, want 4 and 5 (overwriting one side loses the evidence)",
			intent.Declared.Runs, intent.Removed.Runs)
	}
	if intent.Declared == intent.Removed {
		t.Error("the two sides collapsed into one value")
	}
	// And the reader sees it: the mismatch is printed, not just stored.
	out := pruneOutput{plan: resolvePrunePlan(g.cfg, pruneFlags{runs: 2}), report: removed, declared: declared}
	text := captureStdout(t, func() { printPruneText(os.Stdout, out) })
	if !strings.Contains(text, "warning: declared") || !strings.Contains(text, "but removed") {
		t.Errorf("text output hides the mismatch:\n%s", text)
	}
	// An agreeing pair must not print the warning: a line that fires on every
	// prune teaches the operator to skim past it.
	quiet := out
	quiet.declared = removed
	ok := captureStdout(t, func() { printPruneText(os.Stdout, quiet) })
	if strings.Contains(ok, "warning: declared") {
		t.Errorf("text output warns about counts that agree:\n%s", ok)
	}
}

// TestDryRunNeedsNoAuthorization keeps AC1 and D22-1 intact: a preview needs
// neither a named source nor a willing human. If this regressed, the guard would
// block the very command it tells people to run first.
func TestDryRunNeedsNoAuthorization(t *testing.T) {
	g := newGuardStore(t, 2)
	seedRuns(t, g.st, 6)
	builtin := g.withBuiltinSource(t)

	// interactive=false, empty stdin, no -yes, and the built-in source: every
	// ingredient of a refusal except the one that matters, which is -dry-run.
	for _, cfg := range []*config.Config{g.cfg, builtin} {
		restore := withPruneStdin(strings.NewReader(""), false)
		out, err := runPrune(cfg, pruneFlags{dryRun: true, runs: 2, format: "text"})
		restore()
		if err != nil {
			t.Errorf("source %q: dry run needed authorization: %v", cfg.SourceKind, err)
			continue
		}
		if out.report.Runs != 4 {
			t.Errorf("dry run would remove %d runs, want 4", out.report.Runs)
		}
	}
	if got := g.runs(t); got != 6 {
		t.Errorf("runs = %d after two dry runs, want 6", got)
	}
	// And an unlimited policy on the default path needs nothing either: it
	// deletes nothing, so the guard has no business being in the way.
	cfg := g.cfg
	cfg.Store.Retain.Runs = 0
	restore := withPruneStdin(strings.NewReader(""), false)
	if _, err := runPrune(cfg, pruneFlags{runs: unsetBound, days: unsetBound, format: "text"}); err != nil {
		t.Errorf("unlimited real prune was refused: %v", err)
	}
	restore()
	if got := g.runs(t); got != 6 {
		t.Errorf("runs = %d after an unlimited prune, want 6", got)
	}
	if _, found := g.intent(t); found {
		t.Error("a prune that removed nothing wrote an intent record")
	}
}

// TestAuthorizePruneNamesTheAbsoluteStore is the prompt's content: the operator
// has to see which file is about to lose rows, and how many. A policy normally
// holds a relative store.path, so the guard must print the resolved file rather
// than a fragment nobody can verify.
func TestAuthorizePruneNamesTheAbsoluteStore(t *testing.T) {
	g := newGuardStore(t, 2)
	seedRuns(t, g.st, 6)
	abs := makeStorePathRelative(t, g)

	text, err := cancelPruneAtPrompt(t, g.cfg)
	if !errors.Is(err, errPruneCancelled) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
	for _, want := range []string{abs, "4 runs"} {
		if !strings.Contains(text, want) {
			t.Errorf("prompt omits %q (want the absolute store and the declared counts):\n%s", want, text)
		}
	}
}

// TestCancelledPromptDeletesNothing is the other half of the same exchange: an
// answer of "no" is a refusal the guard must honour, not a formality on the way
// to the delete.
func TestCancelledPromptDeletesNothing(t *testing.T) {
	g := newGuardStore(t, 2)
	seedRuns(t, g.st, 6)
	makeStorePathRelative(t, g)

	if _, err := cancelPruneAtPrompt(t, g.cfg); err == nil {
		t.Fatal("the guard reported no error after a declined prompt")
	}
	if got := g.runs(t); got != 6 {
		t.Errorf("runs = %d, want 6 — a cancelled prompt must leave the store alone", got)
	}
}

// TestGuardRequiredCoversOnlyRealBoundedPrunes pins the entry condition itself.
// Wrong in the permissive direction and the silent delete is back; wrong in the
// strict direction and `-dry-run` — the command both refusal messages tell the
// operator to run first — is itself blocked.
func TestGuardRequiredCoversOnlyRealBoundedPrunes(t *testing.T) {
	bounded := prunePlan{policy: store.RetainPolicy{Runs: 2}}
	unlimited := prunePlan{policy: store.RetainPolicy{}}
	for _, tc := range []struct {
		name string
		plan prunePlan
		f    pruneFlags
		want bool
	}{
		{"real bounded prune", bounded, pruneFlags{}, true},
		{"real unlimited prune", unlimited, pruneFlags{}, false},
		{"dry run bounded", bounded, pruneFlags{dryRun: true}, false},
		{"dry run unlimited", unlimited, pruneFlags{dryRun: true}, false},
	} {
		if got := guardRequired(tc.plan, tc.f); got != tc.want {
			t.Errorf("%s: guardRequired = %v, want %v", tc.name, got, tc.want)
		}
	}
}
