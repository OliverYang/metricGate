package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// metaLastPruneIntent is the store_meta key holding the last destructive prune
// together with the counts it declared before deleting.
const metaLastPruneIntent = "last_prune_intent"

// metaLastPrune is the store_meta key holding the last retention pass that
// removed something.
const metaLastPrune = "last_prune"

// PruneReportJSON is PruneReport's wire form. A store_meta value is JSON, so the
// counts have to marshal; the fields carry the JSON names rather than aliasing
// PruneReport so the two sides of an intent record can be compared field by
// field in a test without a schema to consult.
type PruneReportJSON struct {
	Runs      int `json:"runs"`
	Findings  int `json:"findings"`
	Snapshots int `json:"snapshots"`
	Kept      int `json:"kept"`
}

// ReportJSONOf maps a PruneReport into the record's JSON counts. It is exported
// because the CLI that ran the deletion is the caller that writes the record,
// and it must report the numbers Prune returned rather than a hand-built pair.
func ReportJSONOf(r PruneReport) PruneReportJSON {
	return PruneReportJSON{Runs: r.Runs, Findings: r.Findings, Snapshots: r.Snapshots, Kept: r.Kept}
}

// PruneIntent is what a destructive prune said it was going to do, next to what
// it actually did. Declared is counted before anything is deleted; Removed is
// what the delete reports. When the two disagree, history changed underneath the
// prune.
//
// last_prune answers "what did the last prune remove". This key answers the
// question that makes that one auditable: "is what it removed the set it claimed
// it would remove". A pass whose declared and removed counts differ silently
// would leave the operator holding a number that describes different rows than
// the ones they approved — and approving a deletion is what this record exists for.
//
// Like last_prune, the row lives in store_meta rather than on runs: it has to
// outlive the runs it describes.
type PruneIntent struct {
	At        time.Time       `json:"at"`
	Source    string          `json:"source"`
	Declared  PruneReportJSON `json:"declared"`
	Removed   PruneReportJSON `json:"removed"`
	StorePath string          `json:"store_path"`
}

// RecordPruneIntent stores the declared/removed pair of the most recent
// destructive prune, so a later reader can replay the same command line and
// compare.
//
// It is exported for the same reason RecordPrune is: a caller that has to
// remember to write the evidence is a caller that can forget. The CLI writes it
// after Prune returns; a future scheduled job gets the same shape for free.
//
// A mismatch is recorded, not rejected. Concurrent checks legitimately insert
// runs between the declared count and the delete, so a difference is a fact about
// the store at that moment — the operator needs both numbers to see it.
func (s *Store) RecordPruneIntent(intent PruneIntent) error {
	return writeMeta(s.db, metaLastPruneIntent, intent)
}

// LastPruneIntent reads the most recent destructive prune's declared and removed
// counts. found is false when this store has never been pruned destructively,
// which is the normal state of a fresh store.
func (s *Store) LastPruneIntent() (PruneIntent, bool, error) {
	var intent PruneIntent
	found, err := s.readMeta(metaLastPruneIntent, &intent)
	if err != nil || !found {
		return PruneIntent{}, found, err
	}
	return intent, true, nil
}

// PruneRecord is what the most recent retention pass retired, kept in the
// database so the answer survives the process that did the deleting.
//
// It exists because `prune_on_write` is otherwise invisible. The check that
// triggers a prune reports one line about its own run; the hundreds of rows it
// retired leave no trace anywhere — not in the CLI output of the next command,
// not in the cockpit, not in the file. "The database shrank and nothing says
// why" is the failure this record prevents, and a number nobody can see is how
// the database reached 2.9 GB in the first place.
//
// The row lives in store_meta rather than as a column on runs because it has
// to outlive the rows it describes: the runs it removed are exactly the ones
// that are gone.
type PruneRecord struct {
	At        time.Time `json:"at"`
	Runs      int       `json:"runs"`
	Findings  int       `json:"findings"`
	Snapshots int       `json:"snapshots"`
	Kept      int       `json:"kept"`
}

// RecordPrune stores r as the store's most recent retention pass.
//
// Prune calls recordPrune inside its own transaction, so every path that
// removes history — the CLI verb, a future scheduled job, the write-time pass —
// leaves the same record without the caller having to remember. This exported
// form exists for callers outside the package that hold no transaction of their
// own; a caller that has to remember to write it is a caller that can forget.
func (s *Store) RecordPrune(r PruneRecord) error {
	return recordPrune(s.db, r)
}

// execer is the subset of *sql.DB and *sql.Tx that the record needs, so the
// same statement can run inside Prune's transaction or on its own.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// writeMeta stores one JSON value under one store_meta key.
//
// Both retention records go through this instead of repeating the statement:
// INSERT ... ON CONFLICT is the whole contract of store_meta (one row per key,
// last writer wins), and a second copy of it is a second place to get the
// conflict clause subtly wrong.
func writeMeta(db execer, key string, val any) error {
	data, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("encode %s: %w", key, err)
	}
	_, err = db.Exec(`INSERT INTO store_meta(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(data))
	if err != nil {
		return fmt.Errorf("record %s: %w", key, err)
	}
	return nil
}

// readMeta decodes one store_meta key into val. found is false when the key was
// never written, which is a different fact than a zero value: a caller that reads
// "no record" as "the record says nothing happened" would report an event that
// never occurred.
func (s *Store) readMeta(key string, val any) (bool, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM store_meta WHERE key = ?`, key).Scan(&raw)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", key, err)
	}
	if err := json.Unmarshal([]byte(raw), val); err != nil {
		return false, fmt.Errorf("decode %s %q: %w", key, raw, err)
	}
	return true, nil
}

func recordPrune(db execer, r PruneRecord) error {
	return writeMeta(db, metaLastPrune, r)
}

// LastPrune reads the most recent retention pass. found is false when this
// store has never pruned, which is the normal state of a database nobody has
// configured retention for — the caller must not read that as "pruned
// nothing", because those are different facts.
func (s *Store) LastPrune() (PruneRecord, bool, error) {
	var r PruneRecord
	found, err := s.readMeta(metaLastPrune, &r)
	if err != nil || !found {
		return PruneRecord{}, found, err
	}
	return r, true, nil
}
