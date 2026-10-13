package accept

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileIsEmptyLedger(t *testing.T) {
	l, err := Load(filepath.Join(t.TempDir(), "accepted.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != 0 {
		t.Fatalf("missing file should be an empty ledger, got %d entries", len(l.Entries))
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accepted.yaml")
	l := &Ledger{Entries: []Entry{{
		RuleID: "fan-out", File: "main.go", Line: 35,
		Owner: "alice", Reason: "pre-existing at HEAD", Expires: "2026-12-31T00:00:00Z",
	}}}
	if err := l.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 || got.Entries[0].RuleID != "fan-out" || got.Entries[0].Line != 35 {
		t.Fatalf("round trip lost data: %+v", got.Entries)
	}
}

func TestMatchesExact(t *testing.T) {
	e := Entry{RuleID: "fan-out", File: "main.go", Line: 35}
	if !e.Matches("fan-out", "main.go", 35) {
		t.Fatal("exact match failed")
	}
	// Different line/rule/file must not match: over-accepting by proximity
	// would silently excuse violations one line away.
	if e.Matches("fan-out", "main.go", 36) {
		t.Fatal("matched different line")
	}
	if e.Matches("cyclomatic-complexity", "main.go", 35) {
		t.Fatal("matched different rule")
	}
	if e.Matches("fan-out", "other/main.go", 35) {
		t.Fatal("matched different file")
	}
}

func TestExpired(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	live := Entry{Expires: "2026-12-31T00:00:00Z"}
	if live.Expired(now) {
		t.Fatal("future expiry counted as expired")
	}
	past := Entry{Expires: "2026-01-01T00:00:00Z"}
	if !past.Expired(now) {
		t.Fatal("past expiry not detected")
	}
	none := Entry{}
	if none.Expired(now) {
		t.Fatal("no expiry counted as expired")
	}
	bad := Entry{Expires: "not-a-date"}
	if bad.Expired(now) {
		t.Fatal("malformed expiry counted as expired; it must stay visible")
	}
}

func TestStatusAcceptedStaleOpen(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	l := &Ledger{Entries: []Entry{
		{RuleID: "a", File: "x.go", Line: 1, Owner: "o", Reason: "r"},
		{RuleID: "b", File: "x.go", Line: 2, Owner: "o", Reason: "r", Expires: "2026-01-01T00:00:00Z"},
	}}
	if got := l.statusFor("a", "x.go", 1, now); got != Accepted {
		t.Fatalf("status = %s, want accepted", got)
	}
	if got := l.statusFor("b", "x.go", 2, now); got != Stale {
		t.Fatalf("expired entry status = %s, want stale", got)
	}
	if got := l.statusFor("c", "x.go", 3, now); got != Open {
		t.Fatalf("unlisted status = %s, want open", got)
	}
}

func TestRemoveDropsMatchingEntries(t *testing.T) {
	l := &Ledger{Entries: []Entry{
		{RuleID: "a", File: "x.go", Line: 1, Owner: "o", Reason: "r"},
		{RuleID: "a", File: "x.go", Line: 1, Owner: "o", Reason: "duplicate"},
		{RuleID: "a", File: "x.go", Line: 2, Owner: "o", Reason: "other line"},
	}}
	if n := l.Remove("a", "x.go", 1); n != 2 {
		t.Fatalf("Remove = %d, want 2", n)
	}
	if len(l.Entries) != 1 || l.Entries[0].Line != 2 {
		t.Fatalf("Remove left wrong entries: %+v", l.Entries)
	}
}

func TestValidateRejectsAnonymousAndIncomplete(t *testing.T) {
	cases := map[string]Entry{
		"no rule":   {File: "x.go", Line: 1, Owner: "o", Reason: "r"},
		"no file":   {RuleID: "a", Line: 1, Owner: "o", Reason: "r"},
		"no line":   {RuleID: "a", File: "x.go", Owner: "o", Reason: "r"},
		"no owner":  {RuleID: "a", File: "x.go", Line: 1, Reason: "r"},
		"no reason": {RuleID: "a", File: "x.go", Line: 1, Owner: "o"},
		"bad date":  {RuleID: "a", File: "x.go", Line: 1, Owner: "o", Reason: "r", Expires: "tomorrow"},
	}
	for name, e := range cases {
		if err := e.Validate(); err == nil {
			t.Errorf("%s: Validate passed, want error", name)
		}
	}
	good := Entry{RuleID: "a", File: "x.go", Line: 1, Owner: "o", Reason: "r"}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid entry rejected: %v", err)
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".metricgate", "accepted.yaml")
	l := &Ledger{Entries: []Entry{{RuleID: "a", File: "x.go", Line: 1, Owner: "o", Reason: "r"}}}
	if err := l.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("ledger not written: %v", err)
	}
}
