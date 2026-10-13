// Package accept implements the审美让步账本 (acceptance ledger): a durable,
// auditable record of warnings a team has explicitly decided to live with.
//
// A suppression via inline `metricgate: ignore` is anonymous — it silences a
// finding forever and leaves no trace of who decided or why. The ledger is
// the opposite: every entry names an owner, a reason, and (optionally) an
// expiry. Accepted warnings remain visible in reports, tagged as accepted, so
// the concession is a documented decision rather than an disappearance.
package accept

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultPath is where the ledger lives relative to the repository root —
// the same .metricgate/ directory that already holds history.db and the
// MCP call log.
const DefaultPath = ".metricgate/accepted.yaml"

// Entry is one accepted warning. Matching is exact (rule + file + line):
// deterministic, greppable, and impossible to over-accept by regex accident.
type Entry struct {
	RuleID  string `yaml:"rule" json:"rule"`
	File    string `yaml:"file" json:"file"`
	Line    int    `yaml:"line" json:"line"`
	Owner   string `yaml:"owner" json:"owner"`
	Reason  string `yaml:"reason" json:"reason"`
	Expires string `yaml:"expires,omitempty" json:"expires,omitempty"` // RFC3339 date; empty = no expiry
}

// Ledger is the parsed accepted.yaml.
type Ledger struct {
	Entries []Entry `yaml:"entries"`
}

// Load reads the ledger at path. A missing file is an empty ledger — never
// an error, so repositories without any accepted warnings need no setup.
func Load(path string) (*Ledger, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Ledger{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read accept ledger: %w", err)
	}
	var l Ledger
	if err := yaml.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("parse accept ledger %s: %w", path, err)
	}
	return &l, nil
}

// Save writes the ledger back atomically (temp file + rename), keeping the
// file readable while a concurrent check runs.
func (l *Ledger) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create ledger directory: %w", err)
	}
	data, err := yaml.Marshal(l)
	if err != nil {
		return fmt.Errorf("render accept ledger: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write accept ledger: %w", err)
	}
	return os.Rename(tmp, path)
}

// Matches reports whether this entry covers a finding at rule/file/line.
func (e Entry) Matches(ruleID, file string, line int) bool {
	return e.RuleID == ruleID && e.File == file && e.Line == line
}

// Expired reports whether the entry's expiry date (if any) has passed.
// A parse error in the date counts as not expired — a malformed entry
// should be visible, not silently ignored; the audit command reports it.
func (e Entry) Expired(now time.Time) bool {
	if e.Expires == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, e.Expires)
	if err != nil {
		return false
	}
	return now.After(t)
}

// Status classifies a finding against the ledger.
type Status string

const (
	// Open means the finding is not covered by the ledger.
	Open Status = "open"
	// Accepted means an unexpired entry covers the finding.
	Accepted Status = "accepted"
	// Stale means an entry covers the finding but has expired — the team
	// must re-accept or fix; the finding counts as open for the gate.
	Stale Status = "stale"
)

// Ledger lookup helpers -----------------------------------------------------

// statusFor returns the ledger's verdict for one finding.
func (l *Ledger) statusFor(ruleID, file string, line int, now time.Time) Status {
	for _, e := range l.Entries {
		if e.Matches(ruleID, file, line) {
			if e.Expired(now) {
				return Stale
			}
			return Accepted
		}
	}
	return Open
}

// AcceptedCount is how many of the given findings the (unexpired) ledger
// covers. Stale entries do not count: an expired concession is an open one.
func (l *Ledger) AcceptedCount(ruleID, file string, lines []int, now time.Time) int {
	n := 0
	for _, line := range lines {
		if l.statusFor(ruleID, file, line, now) == Accepted {
			n++
		}
	}
	return n
}

// IsAccepted is the single-finding convenience form.
func (l *Ledger) IsAccepted(ruleID, file string, line int, now time.Time) bool {
	return l.statusFor(ruleID, file, line, now) == Accepted
}

// IsStale reports whether a matching entry exists but has expired.
func (l *Ledger) IsStale(ruleID, file string, line int, now time.Time) bool {
	return l.statusFor(ruleID, file, line, now) == Stale
}

// Remove drops every entry matching rule/file/line (a normal accept records
// one finding, but duplicates may accumulate); it reports how many went away.
func (l *Ledger) Remove(ruleID, file string, line int) int {
	kept := l.Entries[:0]
	n := 0
	for _, e := range l.Entries {
		if e.Matches(ruleID, file, line) {
			n++
			continue
		}
		kept = append(kept, e)
	}
	l.Entries = kept
	return n
}

// Validate rejects entries that would silently never match: a missing rule
// or file, a non-positive line. Called on add; also available to auditors.
func (e Entry) Validate() error {
	if e.RuleID == "" {
		return fmt.Errorf("accept entry: rule is required")
	}
	if e.File == "" {
		return fmt.Errorf("accept entry: file is required")
	}
	if e.Line <= 0 {
		return fmt.Errorf("accept entry: line must be positive (got %d)", e.Line)
	}
	if e.Owner == "" {
		return fmt.Errorf("accept entry: owner is required — an anonymous concession defeats the ledger")
	}
	if e.Reason == "" {
		return fmt.Errorf("accept entry: reason is required — future readers must know why")
	}
	if e.Expires != "" {
		if _, err := time.Parse(time.RFC3339, e.Expires); err != nil {
			return fmt.Errorf("accept entry: expires must be RFC3339 date (e.g. 2026-12-31T00:00:00Z): %w", err)
		}
	}
	return nil
}
