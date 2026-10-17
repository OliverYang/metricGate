package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestLastPruneIsAbsentOnAFreshStore pins the difference between "never pruned"
// and "pruned nothing": found must be false, because a caller that reads a zero
// record as an event would report a cleanup that never happened.
func TestLastPruneIsAbsentOnAFreshStore(t *testing.T) {
	st := newRetainStore(t)
	rec, found, err := st.LastPrune()
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Errorf("LastPrune found %+v on a store that never pruned", rec)
	}
}

// TestPruneRecordsWhatItRemoved is the visibility half of the iteration: after
// a prune, a different process opening the same file must be able to say how
// much history went away.
func TestPruneRecordsWhatItRemoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedRetentionFixture(t, st)
	policy := RetainPolicy{Runs: 2, Now: retainNow(t)}
	report, err := st.Prune(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// A second handle, as the cockpit has: the record has to be in the file,
	// not in the process that did the deleting.
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rec, found, err := reopened.LastPrune()
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("LastPrune found nothing after a prune that removed rows")
	}
	if rec.Runs != report.Runs || rec.Findings != report.Findings ||
		rec.Snapshots != report.Snapshots || rec.Kept != report.Kept {
		t.Errorf("recorded %+v, want the report %+v", rec, report)
	}
	if rec.At.IsZero() {
		t.Error("recorded prune has no timestamp")
	}
}

// TestPruneThatRemovesNothingRecordsNothing keeps prunedLastWrite meaning "the
// last time history was actually trimmed". A no-op that overwrote the record
// would erase the only evidence that trimming ever happened.
func TestPruneThatRemovesNothingRecordsNothing(t *testing.T) {
	st := newRetainStore(t)
	seedRetentionFixture(t, st)
	if _, err := st.Prune(RetainPolicy{Runs: 100, Now: retainNow(t)}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := st.LastPrune(); err != nil || found {
		t.Errorf("LastPrune found=%v err=%v after a prune that removed nothing", found, err)
	}
}

// TestPruneCountsRecordsNothing is the dry-run guarantee at the store level:
// counting a prune must leave no trace at all, including in the record.
func TestPruneCountsRecordsNothing(t *testing.T) {
	st := newRetainStore(t)
	seedRetentionFixture(t, st)
	if _, err := st.PruneCounts(RetainPolicy{Runs: 2, Now: retainNow(t)}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := st.LastPrune(); err != nil || found {
		t.Errorf("LastPrune found=%v err=%v after PruneCounts", found, err)
	}
}

// TestRecordPruneSurvivesLegacyStores covers the migration: a database written
// before store_meta existed gets the table on open, and the first record lands
// in it. The fixture drops the table to reproduce that store.
func TestRecordPruneSurvivesLegacyStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`DROP TABLE store_meta`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	legacy, err := Open(path)
	if err != nil {
		t.Fatalf("open store without store_meta: %v", err)
	}
	defer legacy.Close()
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	if err := legacy.RecordPrune(PruneRecord{At: at, Runs: 3, Findings: 9, Snapshots: 12, Kept: 7}); err != nil {
		t.Fatal(err)
	}
	rec, found, err := legacy.LastPrune()
	if err != nil || !found {
		t.Fatalf("LastPrune found=%v err=%v", found, err)
	}
	if rec.Runs != 3 || rec.Findings != 9 || rec.Snapshots != 12 || rec.Kept != 7 || !rec.At.Equal(at) {
		t.Errorf("round-tripped %+v, want the record that was written", rec)
	}
}
