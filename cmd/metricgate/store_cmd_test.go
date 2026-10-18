package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// These tests cover the two things `store prune` can get wrong that a unit test
// of the SQL cannot catch: help that has drifted from the flags actually parsed,
// and a dry run that touches the database. Both are claims this iteration makes
// about the command, so both are pinned here rather than in prose.

// TestPruneUsageMatchesTheParsedFlags asserts the -h text and the FlagSet name
// the same flags, in both directions.
//
// The help is rendered from the FlagSet, which makes this look tautological
// until the day someone hand-writes a flag line into the prose or forgets to
// register one — the failure mode that put a `-port` that never existed into
// docs/capabilities.md. Checking both directions is the point: a real flag
// missing from the help is as invisible to an operator as a help entry that is
// not a real flag.
func TestPruneUsageMatchesTheParsedFlags(t *testing.T) {
	set := newPruneFlagSet()
	usage := pruneUsage(set)

	var real []string
	set.VisitAll(func(f *flag.Flag) { real = append(real, f.Name) })
	slices.Sort(real)

	documented := documentedFlags(t, usage)
	slices.Sort(documented)

	if !slices.Equal(real, documented) {
		t.Errorf("flags and help disagree:\n parsed: %v\n help:   %v", real, documented)
	}
	// Every real flag must also carry its own description, not merely its name:
	// `-runs` without "-1 = use the policy" is a trap, because 0 means unlimited
	// here and an operator who reads 0 as "keep nothing" would delete the history
	// with a preview in mind.
	for _, name := range real {
		f := set.Lookup(name)
		if !strings.Contains(usage, f.Usage) {
			t.Errorf("help omits the description of -%s: %q", name, f.Usage)
		}
	}
}

// TestPruneHelpExitsCleanly checks `-h` is an answer, not an error. `check -h`
// and `cockpit serve -h` print usage and exit 0; a destructive verb whose help
// exits 2 reads as a failed invocation inside a script, which is how operators
// end up running the thing without -dry-run to "see what it says".
func TestPruneHelpExitsCleanly(t *testing.T) {
	set := newPruneFlagSet()
	set.SetOutput(io.Discard)
	if err := set.Parse([]string{"-h"}); !errorsIsFlagHelp(err) {
		t.Fatalf("Parse(-h) = %v, want flag.ErrHelp", err)
	}
	// cmdStorePrune routes flag.ErrHelp to exit 0 through os.Exit, which a test
	// cannot observe without a subprocess, so the path is pinned at the level the
	// parent help is answerable: `store -h` must print and return normally.
	for _, token := range []string{"-h", "-help", "--help"} {
		got := captureStderr(t, func() { cmdStore([]string{token}) })
		if !strings.Contains(got, "usage: metricgate store prune") {
			t.Errorf("store %s did not print prune usage, got %q", token, firstLine(got))
		}
		if strings.Contains(got, "needs a subcommand") {
			t.Errorf("store %s printed the subcommand error instead of help", token)
		}
	}
}

// TestStoreWithoutSubcommandPointsAtDryRun covers the moment the destructive verb
// is most likely to be mistyped: an operator who types `metricgate store` and
// nothing else. They must come away knowing a preview exists.
//
// Asserted against storeBareMessage rather than cmdStore because cmdStore's
// refusal path ends in fail(), which calls os.Exit(2) and would take the test
// binary with it — the exit code is pinned by the subprocess check below, and the
// text is what a pure function can carry.
func TestStoreWithoutSubcommandPointsAtDryRun(t *testing.T) {
	got := storeBareMessage(newPruneFlagSet())
	for _, want := range []string{
		"usage: metricgate store prune",
		"metricgate store prune -dry-run",
		"-dry-run",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bare `store` message omits %q:\n%s", want, firstLine(got))
		}
	}
	// The message must carry the flags as the parser knows them, including -yes:
	// a bare `store` is where an operator learns the safe order of operations, and
	// a usage block missing a real flag is how docs/capabilities.md acquired a
	// `-port` that never existed.
	if !strings.Contains(got, "-yes") {
		t.Errorf("bare `store` message omits -yes:\n%s", got)
	}
}

// TestStoreBareSubcommandStillFails is the exit-code half: the usage block is an
// addition to the refusal, not a replacement for it. Run in a subprocess because
// the refusal exits the process, and the claim ("a bare `store` still fails")
// cannot be observed any other way.
func TestStoreBareSubcommandStillFails(t *testing.T) {
	t.Setenv("CGO_ENABLED", "0")
	if os.Getenv("TESTSTORESUBCOMMAND") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=TestStoreBareSubcommandStillFails")
		cmd.Env = append(os.Environ(), "TESTSTORESUBCOMMAND=1")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("`store` with no subcommand exited 0, want 2:\n%s", out)
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 {
			t.Errorf("exit code = %v, want 2:\n%s", err, out)
		}
		for _, want := range []string{"needs a subcommand", "metricgate store prune -dry-run"} {
			if !strings.Contains(string(out), want) {
				t.Errorf("output omits %q:\n%s", want, out)
			}
		}
		return
	}
	cmdStore(nil)
	t.Fatal("cmdStore did not exit")
}

// documentedFlags pulls the flag names out of a usage block, i.e. out of the
// rendered text rather than out of the FlagSet, so the comparison is against
// what a human actually reads.
func documentedFlags(t *testing.T, usage string) []string {
	t.Helper()
	lines := strings.Split(usage, "\n")
	start := slices.Index(lines, "flags:")
	if start < 0 {
		t.Fatalf("usage has no `flags:` section:\n%s", usage)
	}
	re := regexp.MustCompile(`^ {2}-(?P<name>[a-z0-9-]+)\s*$`)
	var out []string
	for _, line := range lines[start+1:] {
		if m := re.FindStringSubmatch(line); m != nil {
			out = append(out, m[1])
		}
	}
	if len(out) == 0 {
		t.Fatalf("no flag lines parsed from:\n%s", usage)
	}
	return out
}

func errorsIsFlagHelp(err error) bool { return errors.Is(err, flag.ErrHelp) }

// TestDryRunLeavesTheStoreUntouched is D22-1 in miniature: page_count and the
// run count must be identical across a dry run that would otherwise delete most
// of the rows.
func TestDryRunLeavesTheStoreUntouched(t *testing.T) {
	cfg, st := newPruneTestStore(t, 6)
	seedRuns(t, st, 6)

	before := mustStats(t, st)
	out := mustPrune(t, cfg, pruneFlags{dryRun: true, runs: 2, format: "text"})
	if out.report.Runs != 4 {
		t.Errorf("dry run would remove %d runs, want 4", out.report.Runs)
	}
	if out.before.PageCount != out.after.PageCount || out.before.RunCount != out.after.RunCount {
		t.Errorf("dry run moved the database: page_count %d -> %d, runs %d -> %d",
			out.before.PageCount, out.after.PageCount, out.before.RunCount, out.after.RunCount)
	}
	final := mustStats(t, st)
	if final.RunCount != 6 || final.Findings != 6 || final.Snapshots != 6 {
		t.Errorf("rows after dry run: runs=%d findings=%d snapshots=%d, want 6/6/6",
			final.RunCount, final.Findings, final.Snapshots)
	}
	if final.PageCount != before.PageCount {
		t.Errorf("page_count changed: %d -> %d", before.PageCount, final.PageCount)
	}
	text := captureStdout(t, func() { printPruneText(os.Stdout, out) })
	if !strings.Contains(text, "dry run: database unchanged — page_count") {
		t.Errorf("dry run output omits the unchanged proof:\n%s", text)
	}
}

// TestVacuumReturnsTheFreedPages covers the half of the story a delete cannot
// tell: DELETE marks pages free and leaves the file the same size, so the verb
// that reports "371 MiB is now free pages" needs a way to actually get them
// back. Store.Vacuum existed with no caller and no test before this.
func TestVacuumReturnsTheFreedPages(t *testing.T) {
	cfg, st := newPruneTestStore(t, 2)
	seedRuns(t, st, 12)

	out := mustPrune(t, cfg, pruneFlags{runs: 2, format: "text", vacuum: true})
	if !out.vacuumed {
		t.Fatal("vacuum flag did not reach the store")
	}
	if out.report.Runs == 0 {
		t.Fatal("prune removed nothing, so the vacuum assertion would be vacuous")
	}
	if out.after.FreePages != 0 {
		t.Errorf("after vacuum freelist_count = %d, want 0 (that is what VACUUM means)", out.after.FreePages)
	}
	// The file size is deliberately not asserted: VACUUM compacts pages, it does
	// not promise a shorter file, and on a store this small the rewrite lands on
	// the same page count. freelist_count is the fact VACUUM is defined by.
	text := captureStdout(t, func() { printPruneText(os.Stdout, out) })
	if !strings.Contains(text, "vacuumed, so the freed pages are back on disk") {
		t.Errorf("vacuumed output still claims the pages are free:\n%s", text)
	}
}

// TestVacuumIsRefusedWithDryRun pins the contradiction rather than resolving it
// silently in either direction: -dry-run promises to change nothing, and a
// VACUUM rewrites the whole file.
func TestVacuumIsRefusedWithDryRun(t *testing.T) {
	err := rejectedParse(t, []string{"-dry-run", "-vacuum", "-runs", "5"})
	if err == nil {
		t.Fatal("accepted -dry-run with -vacuum")
	}
	if !strings.Contains(err.Error(), "contradict") {
		t.Errorf("error does not name the contradiction: %v", err)
	}
}

// TestVacuumSkipsAnEmptyPrune keeps the expensive path off a no-op: rewriting a
// 500 MB file to reclaim zero pages is pure cost.
func TestVacuumSkipsAnEmptyPrune(t *testing.T) {
	cfg, st := newPruneTestStore(t, 100)
	seedRuns(t, st, 3)

	out := mustPrune(t, cfg, pruneFlags{runs: 100, format: "text", vacuum: true})
	if out.report.Runs != 0 {
		t.Fatalf("prune removed %d runs under a bound that keeps all 3", out.report.Runs)
	}
	if out.vacuumed {
		t.Error("vacuumed a prune that removed nothing")
	}
}

// TestUnlimitedPolicyWritesNothing covers D22-5: the shipped default has no
// bound, and running the verb anyway must delete nothing and say so.
func TestUnlimitedPolicyWritesNothing(t *testing.T) {
	cfg, st := newPruneTestStore(t, 3)
	cfg.Store.Retain.Runs = 0
	seedRuns(t, st, 3)

	// The policy is the source, so the flags must be left untouched (unsetBound,
	// not 0: a zero-valued pruneFlags would read as an explicit "-runs 0", which is
	// a different statement about intent even though both mean unlimited).
	out := mustPrune(t, cfg, pruneFlags{runs: unsetBound, days: unsetBound, format: "text"})
	if out.report.Runs != 0 || out.report.Kept != 3 {
		t.Errorf("unlimited policy reported %d removed / %d kept, want 0/3", out.report.Runs, out.report.Kept)
	}
	if out.plan.source != "store.retain" {
		t.Errorf("source = %q, want store.retain", out.plan.source)
	}
	text := captureStdout(t, func() { printPruneText(os.Stdout, out) })
	if !strings.Contains(text, "unlimited (no bound set): nothing is ever removed") {
		t.Errorf("text output omits the refusal:\n%s", text)
	}
	// "0 runs removed" is the sentence that must never appear for the default
	// policy: it reads as a deletion set that was evaluated and found empty.
	if strings.Contains(text, "removed 0 runs") || strings.Contains(text, "would remove") {
		t.Errorf("unlimited policy reported a removal:\n%s", text)
	}
	final := mustStats(t, st)
	if final.RunCount != 3 {
		t.Errorf("runs = %d after an unlimited prune, want 3", final.RunCount)
	}
}

// TestPruneTextReportsBothCounters checks the numbers an operator reads are the
// ones the report carries, including the estimate and the free-page line, and
// that the output says where the bound came from: "keep newest 2 runs
// [store.retain]" and "keep newest 2 runs [-runs/-days]" are different statements
// about the repository, and an operator comparing a dry run to the real thing has
// to be able to see which one they tested.
func TestPruneTextReportsBothCounters(t *testing.T) {
	cfg, st := newPruneTestStore(t, 5)
	cfg.Store.Retain.Runs = 2
	seedRuns(t, st, 5)

	out := mustPrune(t, cfg, pruneFlags{runs: unsetBound, days: unsetBound, format: "text"})
	text := captureStdout(t, func() { printPruneText(os.Stdout, out) })
	for _, want := range []string{"retention: keep newest 2 runs [store.retain]", "removed 3 runs", "kept: 2 runs", "is now free pages"} {
		if !strings.Contains(text, want) {
			t.Errorf("text output missing %q:\n%s", want, text)
		}
	}
	if out.report.Findings != 3 || out.report.Snapshots != 3 {
		t.Errorf("child counts = %d findings / %d snapshots, want 3/3", out.report.Findings, out.report.Snapshots)
	}
	if out.freedBytes() <= 0 {
		t.Errorf("freed estimate = %d, want > 0", out.freedBytes())
	}

	// Same bound, different origin: the flag override must be visible.
	flagged := mustPrune(t, cfg, pruneFlags{runs: 2, days: unsetBound, format: "text"})
	if got := captureStdout(t, func() { printPruneText(os.Stdout, flagged) }); !strings.Contains(got, "[-runs/-days]") {
		t.Errorf("a -runs override did not name itself as the source:\n%s", got)
	}
}

// TestPruneJSONCarriesBothSides pins the machine-readable contract: a caller must
// be able to tell a dry run from a real one, and see the counts on both sides,
// without parsing prose.
func TestPruneJSONCarriesBothSides(t *testing.T) {
	cfg, st := newPruneTestStore(t, 4)
	seedRuns(t, st, 4)

	out := mustPrune(t, cfg, pruneFlags{dryRun: true, runs: 1, format: "json"})
	var got pruneJSON
	body := captureStdout(t, func() { printPruneJSON(os.Stdout, out) })
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", firstLine(body), err)
	}
	if !got.DryRun || got.RunsRemoved != 3 || got.Kept != 1 {
		t.Errorf("dry_run=%v removed=%d kept=%d, want true/3/1", got.DryRun, got.RunsRemoved, got.Kept)
	}
	if got.Path != cfg.Store.Path {
		t.Errorf("store_path = %q, want %q", got.Path, cfg.Store.Path)
	}
	if got.Before.PageCount != got.After.PageCount || got.Before.Runs != got.After.Runs {
		t.Errorf("before/after differ on a dry run: %+v vs %+v", got.Before, got.After)
	}
	if got.Retention.Runs != 1 || got.Retention.Source != "-runs/-days" {
		t.Errorf("retention = %+v, want runs=1 sourced from the flags", got.Retention)
	}
}

// TestParsePruneFlagsRejectsNonsense checks the validation a destructive command
// cannot afford to skip: an unknown flag must not be swallowed, and -2 must not
// silently mean something.
func TestParsePruneFlagsRejectsNonsense(t *testing.T) {
	// Each rejection prints the usage block, so the call goes through
	// rejectedParse: flag writes to stderr on its own and the log would otherwise
	// fill with usage text that reads as a failure.
	for _, bad := range [][]string{{"-dry-run", "-bogus"}, {"-runs", "-2"}, {"-format", "yaml"}, {"-runs", "x"}} {
		if err := rejectedParse(t, bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	f, err := parsePruneFlags([]string{"-dry-run", "-runs", "0"})
	if err != nil {
		t.Fatal(err)
	}
	if !f.dryRun || f.runs != 0 {
		t.Errorf("parsed %+v, want dryRun=true runs=0", f)
	}
	if override(f.days) {
		t.Errorf("-days default %d must not count as an override", f.days)
	}
	if override(f.runs) != true {
		t.Error("-runs 0 must count as an override: 0 is the policy's own unlimited value")
	}
}

// rejectedParse parses args and returns only the error, with the usage block the
// flag package prints on failure captured out of the log.
func rejectedParse(t *testing.T, args []string) error {
	t.Helper()
	var err error
	captureStderr(t, func() { _, err = parsePruneFlags(args) })
	return err
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
