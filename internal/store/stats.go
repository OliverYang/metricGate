package store

import "fmt"

// This file answers "how big is the history, and what is in it". The numbers
// exist because `store prune` has to report what it removed and `check` has to
// report what a write-time prune did, and neither claim is worth anything if it
// is composed from guesses about rows-per-table.
//
// The state and the row counts are taken in ONE query on ONE connection. The
// store is written by every `check` and read by the cockpit and the MCP server,
// so two separate queries can straddle an insert and produce counts that
// disagree with each other — a freed-bytes figure computed from a stale
// page_count is worse than no figure, because it looks measured.

// Stats is the size and contents of the database as of one snapshot.
type Stats struct {
	// PageCount and PageSize multiply out to the file size the database itself
	// reports. FreePages are pages a delete made reusable: still allocated, so
	// the file has NOT shrunk until a VACUUM rewrites it.
	PageCount  int64
	PageSize   int64
	FreePages  int64
	RunCount   int
	Archived   int
	Findings   int
	Snapshots  int
	Projects   int
	FirstRunAt string
	LastRunAt  string
}

// Bytes is every page the file holds.
func (st Stats) Bytes() int64 { return st.PageCount * st.PageSize }

// FreeBytes is what a VACUUM can hand back: the reusable pages, at the page
// size that costs to allocate. Reporting this next to a file size that has not
// moved is the difference between "the delete worked" and "the delete lied".
func (st Stats) FreeBytes() int64 { return st.FreePages * st.PageSize }

// statsQuery reads the whole snapshot in one round trip. pragma_* used as table
// valued functions are what keep this atomic: they see the same database state
// as the COUNT(*) beside them.
//
// The last two columns are wrapped in COALESCE because MIN/MAX over an empty
// runs table is NULL, while FirstRunAt/LastRunAt are plain strings. The NULL
// reached Scan as "converting NULL to string is unsupported", so an empty
// store — a fresh `.metricgate/` directory, or a store whose every run has been
// archived away — failed the command before it could report it. `store prune`
// calls Stats unconditionally, which is how `metricgate store prune -dry-run`
// in an empty directory came to exit 2 instead of describing the empty store.
const statsQuery = `
SELECT
  (SELECT page_count FROM pragma_page_count()),
  (SELECT page_size FROM pragma_page_size()),
  (SELECT freelist_count FROM pragma_freelist_count()),
  (SELECT COUNT(*) FROM runs),
  (SELECT COUNT(*) FROM runs WHERE archived = 1),
  (SELECT COUNT(*) FROM findings),
  (SELECT COUNT(*) FROM metric_snapshots),
  (SELECT COUNT(DISTINCT COALESCE(NULLIF(project_root, ''), '')) FROM runs WHERE archived = 0),
  (SELECT COALESCE(MIN(started_at), '') FROM runs),
  (SELECT COALESCE(MAX(started_at), '') FROM runs)`

// Stats reads the snapshot. It writes nothing.
func (s *Store) Stats() (Stats, error) {
	var st Stats
	err := s.db.QueryRow(statsQuery).Scan(
		&st.PageCount, &st.PageSize, &st.FreePages, &st.RunCount, &st.Archived,
		&st.Findings, &st.Snapshots, &st.Projects, &st.FirstRunAt, &st.LastRunAt)
	if err != nil {
		return Stats{}, fmt.Errorf("read store stats: %w", err)
	}
	return st, nil
}

// AverageRunBytes is what one run costs in this database on average, index
// pages included. Retention estimates are charged against this number instead
// of a hardcoded per-row weight, because the file already contains the indexes
// a delete frees, and an average taken from the real database cannot be wrong
// about that in a way a constant would be.
func (st Stats) AverageRunBytes() int64 {
	if st.RunCount == 0 {
		return 0
	}
	return st.Bytes() / int64(st.RunCount)
}
