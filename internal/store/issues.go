package store

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"metricgate/internal/accept"
	"metricgate/internal/model"
)

// Issue state persistence ---------------------------------------------------
//
// The findings table is a record of what one run saw. It has no identity of
// its own: a finding is a row in a report, and the same code smell reappears
// as a new row on every scan. That is correct for a run history and useless
// for a triage queue, where the unit of work is *the finding*, not *the run*
// — a false-positive verdict recorded on Monday must still hold on Friday
// even though the run that carried it is long gone.
//
// issue_states gives a finding a durable key. The key is a fingerprint
// computed from what the finding means (rule + file + anchored span), so it is
// stable across runs without needing an id from a server nobody has.

// IssueStatus is the triage verdict on one finding. The values mirror the
// ledger's vocabulary (accept.Status) so the two never drift into synonyms a
// human would read as different decisions.
type IssueStatus string

const (
	// IssueOpen is the untouched state: the finding is on the list because
	// the last run saw it, not because anyone decided anything about it.
	IssueOpen IssueStatus = "open"
	// IssueConfirmed marks the finding as true and scheduled.
	IssueConfirmed IssueStatus = "confirmed"
	// IssueFalsePositive marks it never going to be fixed and not a defect.
	IssueFalsePositive IssueStatus = "false_positive"
	// IssueAccepted marks it conceded: covered by the ledger with an owner
	// and a reason. It stays visible and does not fail the gate.
	IssueAccepted IssueStatus = "accepted"
)

// Valid reports whether the status is one this store will persist. Anything
// else is a caller bug, and refusing it here keeps a typo from creating a
// finding no UI can ever show again.
func (s IssueStatus) Valid() bool {
	switch s {
	case IssueOpen, IssueConfirmed, IssueFalsePositive, IssueAccepted:
		return true
	}
	return false
}

// Fingerprint is the durable identity of a finding.
//
// The scope is chosen per anchor status because the precision of the two
// kinds of finding is genuinely different, and pretending otherwise is what
// makes triage queues lie:
//
//   - exact anchors carry a byte span and a SHA-256 of those bytes. The bytes
//     are the identity: an edit that rewrites them changes the fingerprint,
//     so the finding is a new one and the old verdict stops applying. That is
//     the honest answer — the code the owner conceded is not the code now in
//     the file.
//   - line-only anchors (file-scoped metrics, findings with no AST node) carry
//     no span, so bytes cannot be used. Their identity is rule + file + line.
//     It is weaker, and the weakness is stated in Results.ModelName rather
//     than papered over.
//
// Span bytes are deliberately not hashed raw: the whole point is that two
// findings of the same rule in the same file, found in the same file version,
// produce different fingerprints, which the line and column achieve.
func Fingerprint(f model.Finding) string {
	if f.Evidence != nil && f.Evidence.Anchor == "exact" && f.Evidence.SnippetHash != "" {
		raw := strings.Join([]string{f.RuleID, f.File, "exact", f.Evidence.SnippetHash}, "\x00")
		return hashParts(raw)
	}
	raw := strings.Join([]string{f.RuleID, f.File, "line", fmt.Sprintf("%d", f.StartLine)}, "\x00")
	return hashParts(raw)
}

// hashParts renders a stable 16-hex-digit digest. Full 64 hex digits would be
// a column carried through every URL and every row of the UI for no gain:
// hexadecimally, 16 digits are past the point where a collision between two
// findings in one repository is possible.
func hashParts(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum)[:16]
}

// IssueState is one stored verdict.
type IssueState struct {
	Fingerprint string      `json:"fingerprint"`
	Status      IssueStatus `json:"status"`
	Owner       string      `json:"owner,omitempty"`
	Reason      string      `json:"reason,omitempty"`
	Expires     string      `json:"expires,omitempty"`
	UpdatedAt   string      `json:"updatedAt"`
}

// SetIssueState upserts one verdict. The expires field follows the ledger's
// convention: an empty string is "no expiry", not "expired".
func (s *Store) SetIssueState(st IssueState) error {
	if !st.Status.Valid() {
		return fmt.Errorf("issue state: unknown status %q", st.Status)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`INSERT INTO issue_states (fingerprint, status, owner, reason, expires, updated_at)
	                     VALUES (?, ?, ?, ?, ?, ?)
	                     ON CONFLICT(fingerprint) DO UPDATE SET
	                       status = excluded.status, owner = excluded.owner,
	                       reason = excluded.reason, expires = excluded.expires,
	                       updated_at = excluded.updated_at`,
		st.Fingerprint, string(st.Status), nullableText(st.Owner),
		nullableText(st.Reason), nullableText(st.Expires), now)
	if err != nil {
		return fmt.Errorf("record issue state: %w", err)
	}
	return nil
}

// SetIssueStates upserts many verdicts in one transaction, so a bulk change
// is all-or-nothing: a partial bulk operation leaves the triage queue in a
// state no one asked for.
func (s *Store) SetIssueStates(states []IssueState) error {
	if len(states) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, st := range states {
		if !st.Status.Valid() {
			return fmt.Errorf("issue state: unknown status %q", st.Status)
		}
		if _, err := tx.Exec(`INSERT INTO issue_states (fingerprint, status, owner, reason, expires, updated_at)
		                     VALUES (?, ?, ?, ?, ?, ?)
		                     ON CONFLICT(fingerprint) DO UPDATE SET
		                       status = excluded.status, owner = excluded.owner,
		                       reason = excluded.reason, expires = excluded.expires,
		                       updated_at = excluded.updated_at`,
			st.Fingerprint, string(st.Status), nullableText(st.Owner),
			nullableText(st.Reason), nullableText(st.Expires), now); err != nil {
			return fmt.Errorf("record issue state: %w", err)
		}
	}
	return tx.Commit()
}

// IssueStates returns the verdicts for the given fingerprints. Unknown
// fingerprints are absent from the result rather than present with a zero
// status: the caller joins against the finding list, and a synthetic "open"
// would make "nothing has been decided" indistinguishable from "decided open".
func (s *Store) IssueStates(fingerprints []string) (map[string]IssueState, error) {
	out := map[string]IssueState{}
	if len(fingerprints) == 0 {
		return out, nil
	}
	// SQLite's default variable limit is generous but not infinite; the
	// chunk keeps a bulk read of a large selection from tripping it.
	const chunk = 400
	for start := 0; start < len(fingerprints); start += chunk {
		end := start + chunk
		if end > len(fingerprints) {
			end = len(fingerprints)
		}
		batch := fingerprints[start:end]
		placeholders := strings.Repeat(",?", len(batch)-1)
		args := make([]any, len(batch))
		for i, fp := range batch {
			args[i] = fp
		}
		rows, err := s.db.Query(`SELECT fingerprint, status, owner, reason, expires, updated_at
		                         FROM issue_states WHERE fingerprint IN (?`+placeholders+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("read issue states: %w", err)
		}
		for rows.Next() {
			var st IssueState
			var owner, reason, expires sql.NullString
			if err := rows.Scan(&st.Fingerprint, &st.Status, &owner, &reason, &expires, &st.UpdatedAt); err != nil {
				rows.Close()
				return nil, fmt.Errorf("read issue states: %w", err)
			}
			st.Owner, st.Reason, st.Expires = owner.String, reason.String, expires.String
			out[st.Fingerprint] = st
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read issue states: %w", err)
		}
		rows.Close()
	}
	return out, nil
}

// StatusOverride is the triage knowledge applied to a finding at read time,
// computed in one place so the API, the gate and the trend all read the same
// verdict as the same thing.
type StatusOverride struct {
	Status IssueStatus `json:"status"`
	Reason string      `json:"reason,omitempty"`
	Owner  string      `json:"owner,omitempty"`
	// FromLedger records that the verdict came from the acceptance ledger
	// rather than a decision made in the UI. The distinction matters: a
	// ledger entry is a reviewed artifact in git, a UI decision is not.
	FromLedger bool `json:"fromLedger,omitempty"`
}

// EffectiveStatus resolves the verdict in force for a finding: a stored
// issue state first, the ledger second, open otherwise. A ledger entry that
// has expired is not a verdict at all — it is an open finding with a note.
func (s *Store) EffectiveStatus(fp string, ledger *accept.Ledger, ruleID, file string, line int, now time.Time) StatusOverride {
	if fp != "" {
		states, err := s.IssueStates([]string{fp})
		if err == nil {
			if st, ok := states[fp]; ok && st.Status != IssueOpen {
				return StatusOverride{Status: st.Status, Reason: st.Reason, Owner: st.Owner}
			}
		}
	}
	if ov, ok := ledgerStatusFor(ledger, ruleID, file, line, now); ok {
		return ov
	}
	return StatusOverride{Status: IssueOpen}
}

// ledgerStatusFor is the ledger's verdict for one finding, kept as a small
// helper so the read path asks the question in exactly one place.
func ledgerStatusFor(l *accept.Ledger, ruleID, file string, line int, now time.Time) (StatusOverride, bool) {
	if l == nil {
		return StatusOverride{}, false
	}
	for _, e := range l.Entries {
		if !e.Matches(ruleID, file, line) {
			continue
		}
		if e.Expired(now) {
			// An expired concession is an open finding; the note says why.
			return StatusOverride{}, false
		}
		return StatusOverride{Status: IssueAccepted, Reason: e.Reason, Owner: e.Owner, FromLedger: true}, true
	}
	return StatusOverride{}, false
}
