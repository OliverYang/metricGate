package store

import (
	"strings"
	"testing"
)

// TestStatsOnAnEmptyStore pins the empty case, which is the one that used to
// crash: `mkdir -p x/.metricgate && cd x && metricgate store prune -dry-run`
// exited 2 with "sql: Scan error on column index 8, name
// (SELECT MIN(started_at) FROM runs): converting NULL to string is unsupported"
// because MIN/MAX over zero rows is NULL and FirstRunAt/LastRunAt are strings.
//
// The command calls Stats before it looks at any bound, so the failure landed
// on the default unlimited path too — a store nobody had configured yet was the
// exact store that could not be described.
func TestStatsOnAnEmptyStore(t *testing.T) {
	st := newRetainStore(t)

	stats, err := st.Stats()
	if err != nil {
		t.Fatalf("Stats on an empty store: %v", err)
	}
	if stats.RunCount != 0 {
		t.Errorf("RunCount = %d, want 0", stats.RunCount)
	}
	if stats.Findings != 0 || stats.Snapshots != 0 {
		t.Errorf("rows = %d findings / %d snapshots, want 0/0", stats.Findings, stats.Snapshots)
	}
	if stats.FirstRunAt != "" || stats.LastRunAt != "" {
		t.Errorf("run times = %q/%q, want empty strings (NULL, not an error)", stats.FirstRunAt, stats.LastRunAt)
	}
}

// TestStatsTimesFollowTheRunsThatExist is the non-empty half of the same
// contract: the empty-string sentinel must not leak into a store that has runs,
// or a dry run would report "first run at ”" and an operator would have no way
// to tell a broken field from an empty one.
func TestStatsTimesFollowTheRunsThatExist(t *testing.T) {
	st := newRetainStore(t)
	seedRetentionFixture(t, st)

	stats, err := st.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.RunCount == 0 {
		t.Fatal("fixture wrote no runs, so the time assertions below would pass for the wrong reason")
	}
	if stats.FirstRunAt == "" || stats.LastRunAt == "" {
		t.Errorf("run times = %q/%q, want the fixture's first and last run", stats.FirstRunAt, stats.LastRunAt)
	}
	if stats.FirstRunAt > stats.LastRunAt {
		t.Errorf("first run %q is after last run %q", stats.FirstRunAt, stats.LastRunAt)
	}
}

// TestStatsQueryCoalescesTheRunTimeColumns is the reverse verification for the
// COALESCE above: with the guards removed the query hands Scan a NULL string,
// which is the original crash. Reading the constant rather than running the
// query keeps this a claim about the SQL, not about the schema.
func TestStatsQueryCoalescesTheRunTimeColumns(t *testing.T) {
	for _, column := range []string{"MIN(started_at)", "MAX(started_at)"} {
		uncoalesced := "(SELECT " + column + " FROM runs)"
		if !strings.Contains(statsQuery, "(SELECT COALESCE("+column+", '') FROM runs)") {
			t.Errorf("statsQuery lost the COALESCE around %q — an empty store would Scan NULL into a string:\n%s",
				uncoalesced, statsQuery)
		}
	}
}
