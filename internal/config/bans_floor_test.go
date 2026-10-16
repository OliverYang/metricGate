package config

import "testing"

// The ban floor exists because LoadFile does not merge with DefaultYAML: before
// it, writing any policy file silently deleted the built-in bans, so a
// repository with a policy was *less* constrained than one without. These tests
// pin the floor, the override rule and the provenance.
func TestBansFloorSurvivesPolicyWithoutBannedAPI(t *testing.T) {
	cfg, err := Load([]byte("version: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	bans := cfg.Bans("go")
	if len(bans) != 2 {
		t.Fatalf("go bans = %d, want the 2 builtin floor entries: %+v", len(bans), bans)
	}
	if bans[0].Match != "fmt.Print*" || bans[1].Match != "panic" {
		t.Errorf("floor order/content = %+v", bans)
	}
	if len(cfg.Bans("java")) != 2 {
		t.Errorf("java floor = %+v, want System.out.print* and System.err.print*", cfg.Bans("java"))
	}
}

// A policy entry with the same kind+match replaces the floor entry in place
// rather than being appended next to it: retuning a built-in ban must not
// duplicate it.
func TestBanEntryPolicyOverridesFloorInPlace(t *testing.T) {
	cfg, err := Load([]byte(`version: 1
languages:
  go:
    banned-api:
      - match: fmt.Print*
        reason: our house rule
`))
	if err != nil {
		t.Fatal(err)
	}
	entries := cfg.BanEntries("go")
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (override + untouched panic): %+v", len(entries), entries)
	}
	if entries[0].Ban.Reason != "our house rule" || entries[0].Source == "builtin" {
		t.Errorf("entry[0] = %+v, want the policy's reason and a policy source", entries[0])
	}
	if entries[1].Ban.Match != "panic" || entries[1].Source != "builtin" {
		t.Errorf("entry[1] = %+v, want the untouched builtin panic ban", entries[1])
	}
}

// An explicit kind participates in the key: a call ban and an import ban with
// the same match are different constraints and both survive.
func TestBanEntryKeyIncludesKind(t *testing.T) {
	cfg, err := Load([]byte(`version: 1
languages:
  go:
    banned-api:
      - match: fmt
        kind: import
`))
	if err != nil {
		t.Fatal(err)
	}
	entries := cfg.BanEntries("go")
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3 (2 floor + 1 import): %+v", len(entries), entries)
	}
	if last := entries[2]; last.Ban.Kind != "import" || last.Ban.Match != "fmt" {
		t.Errorf("entry[2] = %+v, want the import ban", last)
	}
}

// A language with no builtin floor and no policy entries has no bans — the
// floor is not a blanket "every language has bans".
func TestBansFloorIsLanguageScoped(t *testing.T) {
	cfg, err := Load([]byte("version: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if bans := cfg.Bans("python"); len(bans) != 0 {
		t.Errorf("python bans = %+v, want none (no builtin floor for python)", bans)
	}
}

// The floor entries are labelled "builtin", not "policy_file": a digest must be
// able to tell an inherited default from something the file actually wrote.
func TestBanEntriesReportBuiltinProvenance(t *testing.T) {
	cfg, err := Load([]byte(`version: 1
languages:
  go:
    banned-api:
      - match: strings.ToLower
`))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, e := range cfg.BanEntries("go") {
		seen[e.Ban.Match] = e.Source
	}
	if seen["fmt.Print*"] != "builtin" || seen["panic"] != "builtin" {
		t.Errorf("floor provenance = %v, want builtin for both", seen)
	}
	if seen["strings.ToLower"] == "" || seen["strings.ToLower"] == "builtin" {
		t.Errorf("policy entry provenance = %q, want the policy source", seen["strings.ToLower"])
	}
}
