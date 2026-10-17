package main

import (
	"bytes"
	"strings"
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/store"
)

// prune_on_write is the only place in this codebase where running a check can
// delete history, so every branch of it is pinned here: the off switch, the
// unlimited policy, the retirement itself, and the failure path that must not
// turn a written run into a failed command.

// writeRetainPolicy builds a policy with the store enabled and a caller-chosen
// retention bound.
func writeRetainPolicy(t *testing.T, path string, keepRuns int, onWrite bool) *config.Config {
	t.Helper()
	cfg := defaultPolicyForPruneTest(t)
	cfg.Store.Enabled = true
	cfg.Store.Path = path
	cfg.Store.Retain.Runs = keepRuns
	cfg.Store.PruneOnWrite = onWrite
	return cfg
}

func TestPruneOnWriteOffKeepsEveryRun(t *testing.T) {
	// D22-5: with the switch off — the shipped default — writing a run must not
	// retire anything, however tight the retention bounds in the policy look.
	// A check that silently trimmed history on the operator's first run after
	// upgrading would be the exact failure mode this iteration is here to avoid.
	cfg, st := newPruneTestStore(t, 0)
	cfg.Store.PruneOnWrite = false
	cfg.Store.Retain.Runs = 2
	seedRuns(t, st, 5)

	var out bytes.Buffer
	pruneAfterWrite(cfg, st, &out)

	if got := mustStats(t, st).RunCount; got != 5 {
		t.Errorf("runs after write = %d, want 5 (prune_on_write is off)", got)
	}
	if out.Len() != 0 {
		t.Errorf("prune_on_write off printed %q, want silence", out.String())
	}
}

func TestPruneOnWriteUnlimitedPolicyIsSilent(t *testing.T) {
	// Opting in without setting a bound must not read as "delete everything".
	// Unlimited means unlimited, and the empty output is what keeps the console
	// line reserved for runs that actually retired something.
	cfg, st := newPruneTestStore(t, 0)
	cfg.Store.PruneOnWrite = true
	cfg.Store.Retain.Runs = 0
	cfg.Store.Retain.Days = 0
	seedRuns(t, st, 3)

	var out bytes.Buffer
	pruneAfterWrite(cfg, st, &out)

	if got := mustStats(t, st).RunCount; got != 3 {
		t.Errorf("runs after write = %d, want 3 (no bound set)", got)
	}
	if out.Len() != 0 {
		t.Errorf("unlimited policy printed %q, want silence", out.String())
	}
}

func TestPruneOnWriteRetiresOldRuns(t *testing.T) {
	cfg, st := newPruneTestStore(t, 0)
	cfg.Store.PruneOnWrite = true
	cfg.Store.Retain.Runs = 2
	seedRuns(t, st, 5)

	var out bytes.Buffer
	pruneAfterWrite(cfg, st, &out)

	stats := mustStats(t, st)
	if stats.RunCount != 2 {
		t.Errorf("runs after write = %d, want 2", stats.RunCount)
	}
	if stats.Findings != 2 || stats.Snapshots != 2 {
		t.Errorf("children after write = %d findings / %d snapshots, want 2 / 2 (orphan rows are the failure this guards)",
			stats.Findings, stats.Snapshots)
	}
	line := firstLine(out.String())
	if !strings.Contains(line, "retired 3 runs") || !strings.Contains(line, "kept 2") {
		t.Errorf("reported %q, want the retired and kept counts on one line", line)
	}
}

func TestPruneOnWriteFailureLeavesTheRunAlone(t *testing.T) {
	// The check's verdict is already computed and committed by the time this
	// runs, so a store that refuses to shrink must report and carry on, not
	// crash the command. os.Exit inside a prune failure would turn a passing
	// gate into a broken tool call for every agent on the host.
	cfg, st := newPruneTestStore(t, 0)
	cfg.Store.PruneOnWrite = true
	cfg.Store.Retain.Runs = 1
	seedRuns(t, st, 3)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	pruneAfterWrite(cfg, st, &out) // must not panic, must not exit

	if msg := out.String(); !strings.Contains(msg, "failed") {
		t.Errorf("closed store printed %q, want a failure line", msg)
	}
}

func TestStoreRetentionPolicyReadsBothBounds(t *testing.T) {
	// The write path and `store prune` share this one reader; if it dropped a
	// bound, the two entry points would disagree about what the repo keeps.
	cfg, err := config.Load([]byte("version: 1\nstore:\n  retain:\n    runs: 200\n    days: 90\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := store.RetainPolicy{Runs: 200, Days: 90}
	if got := storeRetentionPolicy(cfg); got.Runs != want.Runs || got.Days != want.Days {
		t.Errorf("storeRetentionPolicy = %+v, want %+v", got, want)
	}
	if storeRetentionPolicy(defaultPolicyForPruneTest(t)).Unlimited() == false {
		t.Error("built-in defaults must be unlimited")
	}
}
