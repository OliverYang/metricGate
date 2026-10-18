package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"metricgate/internal/config"
	"metricgate/internal/model"
	"metricgate/internal/store"
)

// These are the fixtures the prune tests share, kept out of the test files that
// use them for two reasons. The first is length: the assertions belong at the
// top of a file a reviewer can read in one pass, and a helper block below them
// pushes the interesting half off the screen. The second is that the guard tests
// and the command tests now need the same store and the same stdout capture, and
// a fixture defined inside one of them would be copied into the other — which is
// how a fixture stops being the thing both tests agree on.

// newPruneTestStore points a policy at a temporary database, so the command runs
// end to end against a store this test owns.
//
// The policy is written to a real file and loaded through config.LoadFile, which
// is what gives it SourceKind "policy_file" — the same SourceKind an operator's
// .metricgate.yaml produces. Starting from config.Default() instead would make
// every real prune in these tests "builtin", and the guard refuses built-in
// sources for exactly the reason this round is about: nothing named the file.
// -yes would not rescue them; it skips the prompt, not the source requirement.
func newPruneTestStore(t *testing.T, runs int) (*config.Config, *store.Store) {
	t.Helper()
	path := t.TempDir() + "/history.db"
	policyPath := filepath.Join(t.TempDir(), ".metricgate.yaml")
	policy := "version: 1\nstore:\n  enabled: true\n  path: " + path + "\n"
	if runs > 0 {
		policy += "  retain:\n    runs: " + strconv.Itoa(runs) + "\n"
	}
	if err := os.WriteFile(policyPath, []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Store.Retain.Runs = runs
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return cfg, st
}

// seedRuns writes N real runs through SaveRunFull. It goes through the write path
// rather than raw INSERTs because the counts asserted below are the ones `check`
// produces, and a hand-made row can differ in exactly the columns that matter
// (project_root nullability, the archived default, the finding columns).
func seedRuns(t *testing.T, st *store.Store, n int) {
	t.Helper()
	for i := range n {
		summary := &model.RunSummary{
			Files: 1,
			Findings: []model.Finding{{
				RuleID: "sample", Severity: model.SevWarning, Source: model.SrcRule,
				File: "a.go", StartLine: i + 1, StartCol: 1, EndLine: i + 1, EndCol: 1,
				Message: "seeded",
			}},
			Metrics: []model.MetricResult{{
				Metric: "file-length", File: "a.go", Value: 10, Warn: 400, Err: 800,
			}},
		}
		if _, err := st.SaveRunFull(summary, "test", "abc", "/repo/prune", store.RunMeta{}); err != nil {
			t.Fatal(err)
		}
		// Space the runs out so "newest N" is unambiguous: SaveRunFull stamps the
		// wall clock, and two runs in the same second fall back to id order.
		time.Sleep(2 * time.Millisecond)
	}
}

// defaultPolicyForPruneTest is the shipped built-in policy, the one an operator
// gets with no .metricgate.yaml — which is the case D22-5 is about, so the test
// starts from it and only changes the store coordinates.
func defaultPolicyForPruneTest(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// mustPrune and mustStats fold the error checks into the call. Without them,
// every assertion in these tests carries its own `if err != nil { t.Fatal }`,
// and a row of those is exactly what pushes a test's npath past the threshold
// the tool applies to it — the test then reads as a complexity problem when the
// only intent was to fail loudly.
//
// -yes is forced here, not in the call sites, for one reason: these are the
// tests that assert a real deletion happened, and a real deletion asks for
// confirmation before it happens. Forcing the answer in one place keeps that
// concern out of eleven assert bodies while leaving the source requirement
// enforced in every one of them: authorizePrune checks the policy's origin
// before it looks at -yes, so a fixture that fell back to the built-in defaults
// would still be refused here. A test that wants to prove the refusal path calls
// runPrune directly (see store_guard_test.go).
func mustPrune(t *testing.T, cfg *config.Config, f pruneFlags) pruneOutput {
	t.Helper()
	f.yes = true
	out, err := runPrune(cfg, f)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mustStats(t *testing.T, st *store.Store) store.Stats {
	t.Helper()
	stats, err := st.Stats()
	if err != nil {
		t.Fatal(err)
	}
	return stats
}

// captureStdout and captureStderr redirect the process-level writers for one
// call. They are here because cmdStore and the printers write to os.Stdout: the
// claim under test is what the operator sees, and an out parameter returning a
// string would test something other than what runs.
// The redirect rebinds the package variable itself (hence **os.File): the code
// under test and the flag package both read os.Stdout / os.Stderr at call time,
// so copying the pointer would leave the writers untouched and the capture would
// silently return an empty string — a test that passes by reading nothing.
func captureStdout(t *testing.T, fn func()) string { return captureTo(t, &os.Stdout, fn) }

func captureStderr(t *testing.T, fn func()) string { return captureTo(t, &os.Stderr, fn) }

func captureTo(t *testing.T, dest **os.File, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := *dest
	var buf bytes.Buffer
	readDone := make(chan string, 1)
	*dest = w
	go func() {
		_, _ = io.Copy(&buf, r)
		readDone <- buf.String()
	}()
	defer func() {
		*dest = old
		_ = r.Close()
	}()
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-readDone:
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("capture reader never finished")
		return ""
	}
}

// guardTestStore is a store this test owns plus the policy that points at it.
// SourceKind is what distinguishes the two refusal paths, so both are built
// here: a file-backed policy (what an operator's .metricgate.yaml produces) and
// the built-in defaults (what a directory with no policy file produces).
type guardTestStore struct {
	cfg  *config.Config
	st   *store.Store
	path string
}

// newGuardStore writes a policy naming a temporary database and loads it
// through config.LoadFile, so the config carries SourceKind "policy_file" — the
// only source the guard lets delete.
func newGuardStore(t *testing.T, keepRuns int) guardTestStore {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "history.db")
	policy := "version: 1\nstore:\n  enabled: true\n  path: " + path + "\n"
	if keepRuns > 0 {
		policy += "  retain:\n    runs: " + strconv.Itoa(keepRuns) + "\n"
	}
	policyPath := filepath.Join(dir, ".metricgate.yaml")
	if err := os.WriteFile(policyPath, []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SourceKind != policyFileSource {
		t.Fatalf("fixture source = %q, want %q — the guard would refuse every case below",
			cfg.SourceKind, policyFileSource)
	}
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return guardTestStore{cfg: cfg, st: st, path: path}
}

// withBuiltinSource replaces the policy's origin with the built-in defaults,
// keeping the store coordinates. This is the state of a directory that has no
// .metricgate.yaml: the bounds still exist, but no document names them.
func (g guardTestStore) withBuiltinSource(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Store.Enabled = true
	cfg.Store.Path = g.path
	cfg.Store.Retain.Runs = g.cfg.Store.Retain.Runs
	return cfg
}

func (g guardTestStore) runs(t *testing.T) int {
	t.Helper()
	stats, err := g.st.Stats()
	if err != nil {
		t.Fatal(err)
	}
	return stats.RunCount
}

func (g guardTestStore) intent(t *testing.T) (store.PruneIntent, bool) {
	t.Helper()
	intent, found, err := g.st.LastPruneIntent()
	if err != nil {
		t.Fatal(err)
	}
	return intent, found
}

// confirmWithYes runs a real prune as an operator who answered the prompt "y",
// and returns the report the delete produced. It is the one entry point the
// confirmation tests share: the pruning itself is not what they assert, and
// repeating the seam swap in each of them is a place to forget the restore.
func confirmWithYes(t *testing.T, g guardTestStore) pruneOutput {
	t.Helper()
	restore := withPruneStdin(strings.NewReader("y\n"), true)
	defer restore()
	out, err := runPrune(g.cfg, pruneFlags{runs: unsetBound, days: unsetBound, format: "text"})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// pruneWithYes runs a real prune confirmed by the flag rather than a human, and
// returns the report together with everything the guard printed on the way.
//
// The printed text is returned because -yes is defined as skipping the question,
// not the evidence: a test that only checks the store cannot tell "the operator
// was never told" from "the operator was told and did not have to answer".
func pruneWithYes(t *testing.T, g guardTestStore) (pruneOutput, string) {
	t.Helper()
	restore := withPruneStdin(failOnRead{t: t}, false)
	defer restore()
	var out pruneOutput
	var err error
	text := captureStderr(t, func() {
		out, err = runPrune(g.cfg, pruneFlags{runs: unsetBound, days: unsetBound, format: "text", yes: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	return out, text
}

// cancelPruneAtPrompt runs a real bounded prune, answers the prompt "no", and
// returns what the guard printed before asking together with the error it
// returned. Reading the declaration requires the prompt to have been reached, so
// the cancellation is the cheapest way to observe it without deleting anything.
func cancelPruneAtPrompt(t *testing.T, cfg *config.Config) (string, error) {
	t.Helper()
	restore := withPruneStdin(strings.NewReader("n\n"), true)
	defer restore()
	var err error
	text := captureStderr(t, func() {
		_, err = runPrune(cfg, pruneFlags{runs: unsetBound, days: unsetBound, format: "text"})
	})
	return text, err
}

// makeStorePathRelative rewrites the policy's store.path into the relative form a
// real .metricgate.yaml carries, standing in the directory it is relative to.
// configpath.Discover does not promise an absolute path, so the guard has to
// resolve one itself — and the test needs the absolute form to compare against.
func makeStorePathRelative(t *testing.T, g guardTestStore) string {
	t.Helper()
	abs, err := filepath.Abs(g.path)
	if err != nil {
		t.Fatal(err)
	}
	// t.Chdir restores the working directory at cleanup, which matters: this
	// package's other tests resolve store paths against it.
	t.Chdir(filepath.Dir(abs))
	g.cfg.Store.Path = filepath.Base(abs)
	if abs == g.cfg.Store.Path {
		t.Skipf("store.path %q is already absolute, so filepath.Abs is not exercised", abs)
	}
	return abs
}

// failOnRead fails the test the moment the guard reads stdin. It is how "did not
// ask" is shown: the absence of a prompt has no return value.
type failOnRead struct{ t *testing.T }

func (r failOnRead) Read([]byte) (int, error) {
	r.t.Error("the guard read stdin although -yes was given")
	return 0, errors.New("stdin must not be read")
}
