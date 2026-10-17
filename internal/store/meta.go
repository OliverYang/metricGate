package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// metaLastPrune is the store_meta key holding the last retention pass that
// removed something.
const metaLastPrune = "last_prune"

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

func recordPrune(db execer, r PruneRecord) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode prune record: %w", err)
	}
	_, err = db.Exec(`INSERT INTO store_meta(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, metaLastPrune, string(data))
	if err != nil {
		return fmt.Errorf("record prune: %w", err)
	}
	return nil
}

// LastPrune reads the most recent retention pass. found is false when this
// store has never pruned, which is the normal state of a database nobody has
// configured retention for — the caller must not read that as "pruned
// nothing", because those are different facts.
func (s *Store) LastPrune() (PruneRecord, bool, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM store_meta WHERE key = ?`, metaLastPrune).Scan(&raw)
	if err == sql.ErrNoRows {
		return PruneRecord{}, false, nil
	}
	if err != nil {
		return PruneRecord{}, false, fmt.Errorf("read prune record: %w", err)
	}
	var r PruneRecord
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return PruneRecord{}, false, fmt.Errorf("decode prune record %q: %w", raw, err)
	}
	return r, true, nil
}
