package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestPruneIntentRoundTripsThroughTheFile is the round-trip for the audit
// record. The intent is written by the CLI that ran the deletion and read by
// whichever process comes back later to ask whether the numbers agreed, so it
// has to survive a close and a reopen, not just the process that wrote it.
func TestPruneIntentRoundTripsThroughTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)
	written := PruneIntent{
		At:        at,
		Source:    "policy_file",
		Declared:  ReportJSONOf(PruneReport{Runs: 78, Findings: 29503, Snapshots: 1869629, Kept: 26}),
		Removed:   ReportJSONOf(PruneReport{Runs: 78, Findings: 29503, Snapshots: 1869629, Kept: 26}),
		StorePath: "/tmp/retain32/hazard.db",
	}
	if err := st.RecordPruneIntent(written); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got := mustIntentFound(t, reopened)

	// Table-driven rather than one if per field: the claim is that every field
	// survives the file, and a row of `if got.X != Y` blocks is both the shape
	// that hides a dropped field and the shape this tool reads as complex.
	for _, field := range []struct {
		name      string
		got, want any
	}{
		{"At", got.At, written.At},
		{"Source", got.Source, written.Source},
		{"StorePath", got.StorePath, written.StorePath},
		{"Declared", got.Declared, written.Declared},
		{"Removed", got.Removed, written.Removed},
	} {
		if field.got != field.want {
			t.Errorf("%s = %+v, want %+v", field.name, field.got, field.want)
		}
	}
}

// TestPruneIntentKeepsBothSidesOfAMismatch is the reason the record exists: a
// prune whose actual deletion differs from what was declared must keep both
// figures. Overwriting one with the other — or recording only the winner —
// would leave no evidence that the operator was shown a different number.
func TestPruneIntentKeepsBothSidesOfAMismatch(t *testing.T) {
	st := newRetainStore(t)
	declared := PruneReport{Runs: 20, Findings: 900, Snapshots: 3000, Kept: 4}
	removed := PruneReport{Runs: 21, Findings: 940, Snapshots: 3100, Kept: 3}
	if err := st.RecordPruneIntent(PruneIntent{
		At: time.Now().UTC(), Source: "-runs/-days",
		Declared: ReportJSONOf(declared), Removed: ReportJSONOf(removed),
	}); err != nil {
		t.Fatal(err)
	}
	got := mustIntentFound(t, st)
	if got.Declared == got.Removed {
		t.Fatalf("declared and removed collapsed into %+v, so this is not testing a mismatch", got.Declared)
	}
	if got.Declared.Runs != 20 || got.Removed.Runs != 21 {
		t.Errorf("runs = %d declared / %d removed, want 20 / 21", got.Declared.Runs, got.Removed.Runs)
	}
	if got.Declared.Kept != 4 || got.Removed.Kept != 3 {
		t.Errorf("kept = %d declared / %d removed, want 4 / 3", got.Declared.Kept, got.Removed.Kept)
	}
}

// TestPruneIntentIsAbsentOnAFreshStore distinguishes "never pruned
// destructively" from "pruned destructively and agreed": found must be false,
// because a caller reading a zero record as an event would report a deletion
// that never happened — the same failure TestLastPruneIsAbsentOnAFreshStore
// pins for the other key.
func TestPruneIntentIsAbsentOnAFreshStore(t *testing.T) {
	st := newRetainStore(t)
	intent, found, err := st.LastPruneIntent()
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Errorf("LastPruneIntent found %+v on a store that was never pruned", intent)
	}
}

// TestRetentionKeysStayIndependent keeps the two store_meta keys apart:
// last_prune is what went away, last_prune_intent is the declared/removed pair.
// Writing one must not erase the other, and reading one must not decode the
// other's bytes into a zero value that looks like an event.
func TestRetentionKeysStayIndependent(t *testing.T) {
	st := newRetainStore(t)
	if err := st.RecordPrune(PruneRecord{At: time.Now().UTC(), Runs: 5, Findings: 10, Snapshots: 15, Kept: 2}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordPruneIntent(PruneIntent{At: time.Now().UTC(), Source: "policy_file"}); err != nil {
		t.Fatal(err)
	}
	rec := mustPruneRecordFound(t, st)
	// The prune record still reports its own numbers, not the intent's zeros.
	if rec.Runs != 5 || rec.Findings != 10 || rec.Snapshots != 15 || rec.Kept != 2 {
		t.Errorf("last_prune = %+v, want the record written before the intent", rec)
	}
	intent := mustIntentFound(t, st)
	if intent.Declared != (PruneReportJSON{}) || intent.Removed != (PruneReportJSON{}) {
		t.Errorf("intent counts = %+v/%+v, want both sides empty (last_prune is not an intent)",
			intent.Declared, intent.Removed)
	}
}

// TestPruneCountsWritesNoRecord is the dry-run guarantee at the record level,
// asserted over both keys at once: counting a prune must leave no trace, and a
// store that already holds a record must keep the one it had.
func TestPruneCountsWritesNoRecord(t *testing.T) {
	st := newRetainStore(t)
	seedRetentionFixture(t, st)
	if _, err := st.PruneCounts(RetainPolicy{Runs: 2, Now: retainNow(t)}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := st.LastPruneIntent(); err != nil || found {
		t.Errorf("LastPruneIntent found=%v err=%v after PruneCounts", found, err)
	}
	if _, found, err := st.LastPrune(); err != nil || found {
		t.Errorf("LastPrune found=%v err=%v after PruneCounts", found, err)
	}
}

// mustIntentFound reads the intent record and fails unless it is there. The
// three round-trip tests above all need "the record exists, now compare it", and
// repeating the found/err pair in each is where a test starts asserting on a
// record it never confirmed was written.
func mustIntentFound(t *testing.T, st *Store) PruneIntent {
	t.Helper()
	intent, found, err := st.LastPruneIntent()
	if err != nil || !found {
		t.Fatalf("LastPruneIntent found=%v err=%v", found, err)
	}
	return intent
}

// mustPruneRecordFound reads last_prune and fails unless the record is there,
// the same contract mustIntentFound enforces for last_prune_intent. Two tests
// that both need "it was written, now compare it" should not each re-derive what
// found=false means.
func mustPruneRecordFound(t *testing.T, st *Store) PruneRecord {
	t.Helper()
	rec, found, err := st.LastPrune()
	if err != nil || !found {
		t.Fatalf("LastPrune found=%v err=%v", found, err)
	}
	return rec
}
