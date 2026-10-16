package baseline

import (
	"sort"
	"strings"
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/diff"
	"metricgate/internal/engine"
	"metricgate/internal/gittest"
	"metricgate/internal/model"
)

// baselinePolicy disables the history store -- these tests run in temp dirs and
// a multi-gigabyte database written there is a far worse surprise than a
// failing test -- and turns off the todo rule so the only measurements in play
// are metrics, which is the half of the ratchet under test.
const baselinePolicy = `version: 1
store:
  enabled: false
gate:
  fail_on: error
languages:
  go:
    rules:
      todo-comment:
        enabled: false
`

const baselineSource = `package basepkg

import "fmt"

func A(p1, p2, p3, p4, p5, p6 int) int {
	if p1 > 0 {
		if p2 > 0 {
			if p3 > 0 {
				if p4 > 0 {
					if p5 > 0 {
						if p6 > 0 {
							fmt.Println("deep")
						}
					}
				}
			}
		}
	}
	return p1
}
`

// newBaselineTree builds its own git worktree at one commit with one dirtied
// file, so the test does not depend on this repository's working-tree state --
// other sessions dirty it continuously and a measurement taken against it would
// drift under the assertions below.
//
// It also chdirs into the fixture. That is not incidental: the base archive is
// built by `git archive` with an empty working directory, which means git
// resolves it against the process cwd, while the engine is pointed at the check
// root by argument. Mirroring the real invocation (`cd <tree> && metricgate
// check <root> -diff`) is what makes these tests measure the same thing the CLI
// does; run from elsewhere, the archive comes back empty and every assertion
// below would fail for a reason that has nothing to do with key spaces.
func newBaselineTree(t *testing.T) string {
	t.Helper()
	dir := gittest.InitRepo(t)
	t.Chdir(dir)
	gittest.Write(t, dir, ".metricgate.yaml", baselinePolicy)
	gittest.Write(t, dir, "basepkg/a.go", baselineSource)
	gittest.Run(t, dir, "add", "-A")
	gittest.Run(t, dir, "commit", "-qm", "init")
	gittest.Write(t, dir, "basepkg/a.go", strings.Replace(baselineSource, `"deep"`, `"deeper"`, 1))
	return dir
}

// TestSnapshotKeysTheBaseRunInTheCheckRootsOwnSpace is the direct test of the
// key space, and the one that was missing when the subdirectory degradation
// shipped.
//
// Snapshot's caller joins its result against the run being gated, and that run
// is keyed relative to the check root. The base run is not: it happens inside a
// `git archive` extraction of the whole worktree, so the engine reports paths
// relative to the worktree root no matter which directory was passed on the
// command line. At the worktree root the two coincide and nothing looks wrong;
// under a subdirectory they share no key at all, so every value comparison
// missed and the metric half of the ratchet quietly degraded to keep-everything.
//
// Asserting the exact key per root shape is what keeps that from coming back: a
// test that only checked "the map is non-empty" would pass under both key
// spaces, which is precisely how the defect survived.
func TestSnapshotKeysTheBaseRunInTheCheckRootsOwnSpace(t *testing.T) {
	if _, err := engine.New(mustConfig(t)); err != nil {
		t.Skipf("engine unavailable: %v", err)
	}
	dir := newBaselineTree(t)

	for _, tc := range []struct {
		name     string
		root     string
		changed  map[string][]diff.Range
		wantKey  string
		notWants []string
	}{
		{
			name:     "worktree root keeps worktree-relative keys",
			root:     dir,
			changed:  map[string][]diff.Range{"basepkg/a.go": {{Start: 12, End: 12}}},
			wantKey:  "basepkg/a.go",
			notWants: []string{"a.go"},
		},
		{
			name:     "subdirectory rebases onto the check root",
			root:     dir + "/basepkg",
			changed:  map[string][]diff.Range{"a.go": {{Start: 12, End: 12}}},
			wantKey:  "a.go",
			notWants: []string{"basepkg/a.go"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values, err := Snapshot(mustConfig(t), tc.root, "HEAD", tc.changed)
			if err != nil {
				t.Fatalf("Snapshot(root=%s): %v", tc.root, err)
			}
			if len(values) == 0 {
				t.Fatalf("Snapshot(root=%s) returned no base values; the fixture measured nothing", tc.root)
			}
			assertKeySpace(t, tc.root, values, tc.wantKey, tc.notWants)
		})
	}
}

// assertKeySpace states the whole claim in one place: the base value exists
// under the check-root-relative file name, and no value is left behind in the
// other coordinate system.
//
// file-length is a file-scoped measurement, so its key carries the empty scope
// while function-scoped metrics carry the function name. The key is asserted
// exactly rather than by prefix because the file component is the whole
// question here.
func assertKeySpace(t *testing.T, root string, values engine.BaseValues, wantKey string, notWants []string) {
	t.Helper()
	if _, ok := values[engine.MetricKey{File: wantKey, Scope: "", Metric: "file-length"}]; !ok {
		t.Errorf("Snapshot(root=%s) has no %q/file-length key; keys present: %s", root, wantKey, formatKeys(values))
	}
	for _, k := range keysOf(values) {
		if containsString(notWants, k.File) {
			t.Errorf("Snapshot(root=%s) keyed a base value %q, which is the other coordinate system; "+
				"the join against the gated run would miss and the value ratchet would silently keep everything",
				root, k.File)
		}
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestSnapshotSubdirectoryKeysJoinTheRunBeingGated states the consequence
// rather than the mechanism: the keys Snapshot produces under a subdirectory
// must be the same keys a run of that subdirectory produces.
//
// This is the assertion the old behaviour failed. It compares against a real
// engine run rather than a hard-coded string so a future change to how the
// engine names paths cannot make the two sides agree with each other by both
// being wrong.
func TestSnapshotSubdirectoryKeysJoinTheRunBeingGated(t *testing.T) {
	cfg := mustConfig(t)
	dir := newBaselineTree(t)
	sub := dir + "/basepkg"

	eng, err := engine.New(cfg)
	if err != nil {
		t.Skipf("engine unavailable: %v", err)
	}
	defer eng.Close()
	summary, err := eng.Run(sub)
	if err != nil {
		t.Fatalf("engine.Run(%s): %v", sub, err)
	}
	runFiles := map[string]bool{}
	for _, m := range summary.Metrics {
		runFiles[m.File] = true
	}
	if !runFiles["a.go"] {
		t.Fatalf("the gated run is not keyed %q; measured files: %v", "a.go", runFiles)
	}

	values, err := Snapshot(cfg, sub, "HEAD", map[string][]diff.Range{"a.go": {{Start: 12, End: 12}}})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	joined := 0
	for _, k := range keysOf(values) {
		if runFiles[k.File] {
			joined++
		}
	}
	if joined == 0 {
		t.Errorf("no base value keyed under the subdirectory joined the gated run's own keys "+
			"(base keys: %s; run keys: %v): the value ratchet compares nothing",
			formatKeys(values), sortedKeys(runFiles))
	}
}

// TestFullSnapshotKeysJoinTheGatedRunUnderASubdirectory covers the second
// caller of this package, and the defect that had no assertion at all.
//
// applyRegressions compares FullSnapshot's base values against the gated run's
// own metrics with regress.Compare, which pairs them by exact (file, scope,
// metric) string equality. Under a subdirectory the two sides used to sit in
// different key spaces, so nothing paired, regress.FilterToChanged had nothing
// to filter, and `check <subdir> -diff -regress` reported "0 measurement(s) got
// worse than HEAD" for a tree that had genuinely regressed -- a false negative
// on a CI gate, and a strictly worse failure than the unchanged counter being
// 0, because it is a clean-looking number rather than a missing clause.
//
// Snapshot's key space was already asserted above; this asserts FullSnapshot's,
// because they are separate functions with separate call sites and fixing one
// says nothing about the other. The assertion is the join itself rather than a
// regression count: a count would also depend on the fixture actually
// regressing, while an empty join is unconditionally a defect.
func TestFullSnapshotKeysJoinTheGatedRunUnderASubdirectory(t *testing.T) {
	cfg := mustConfig(t)
	dir := newBaselineTree(t)

	for _, tc := range []struct{ name, root string }{
		{"worktree root", dir},
		{"subdirectory", dir + "/basepkg"},
	} {
		root := tc.root
		t.Run(tc.name, func(t *testing.T) {
			base, current := fullSnapshotAndRun(t, cfg, root)
			baseKeys, curKeys := metricKeySet(base), metricKeySet(current)
			if joined := countJoined(baseKeys, curKeys); joined == 0 {
				t.Errorf("no base measurement joined the gated run under root=%s, so regress.Compare pairs nothing "+
					"and `-regress` reports 0 regressions for any tree: base keys=%v run keys=%v",
					root, sortedStringSet(baseKeys), sortedStringSet(curKeys))
			}
		})
	}
}

// fullSnapshotAndRun returns both sides of the join exactly as applyRegressions
// builds them: the base archive through FullSnapshot, and the gated run through
// RelativizeMetrics, which is what applyRegressions does to it before comparing.
func fullSnapshotAndRun(t *testing.T, cfg *config.Config, root string) (base, current []model.MetricResult) {
	t.Helper()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Skipf("engine unavailable: %v", err)
	}
	defer eng.Close()
	summary, err := eng.Run(root)
	if err != nil {
		t.Fatalf("engine.Run(%s): %v", root, err)
	}
	if current = RelativizeMetrics(summary.Metrics, root); len(current) == 0 {
		t.Fatal("the fixture measured nothing; there is no join to test")
	}
	if base, err = FullSnapshot(cfg, root, "HEAD"); err != nil {
		t.Fatalf("FullSnapshot(root=%s): %v", root, err)
	}
	if len(base) == 0 {
		t.Fatalf("FullSnapshot(root=%s) returned no base values", root)
	}
	return base, current
}

// metricKeySet reproduces regress.Compare's pairing rule, so the test states
// the contract instead of borrowing it: identical file+scope+metric.
func metricKeySet(ms []model.MetricResult) map[string]bool {
	out := map[string]bool{}
	for _, m := range ms {
		out[m.File+"|"+m.Scope+"|"+m.Metric] = true
	}
	return out
}

func countJoined(a, b map[string]bool) int {
	joined := 0
	for k := range a {
		if b[k] {
			joined++
		}
	}
	return joined
}

func sortedStringSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func mustConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load([]byte(baselinePolicy))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

func keysOf(values engine.BaseValues) []engine.MetricKey {
	out := make([]engine.MetricKey, 0, len(values))
	for k := range values {
		out = append(out, k)
	}
	return out
}

func formatKeys(values engine.BaseValues) string {
	seen := map[string]bool{}
	var parts []string
	for _, k := range keysOf(values) {
		if seen[k.File] {
			continue
		}
		seen[k.File] = true
		parts = append(parts, k.File)
	}
	return strings.Join(parts, ", ")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
