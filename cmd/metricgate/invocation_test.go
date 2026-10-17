package main

import (
	"strings"
	"testing"
)

// The stored command line is provenance the dashboard shows to anyone who can
// open it, so these tests hold two promises at once: the configuration must
// survive intact enough to paste back, and a credential must never survive at
// all. A test that only checks the first would pass while leaking.

func TestInvocationKeepsOrdinaryFlagsWhole(t *testing.T) {
	got := formatInvocation([]string{"metricgate", "check", ".",
		"-arch-base", "arch.json", "-config", "./metricgate.yaml", "-diff"})
	want := "metricgate check . -arch-base arch.json -config ./metricgate.yaml -diff"
	if got != want {
		t.Fatalf("invocation rewrote the command:\n got %q\nwant %q", got, want)
	}
}

func TestInvocationRedactsSecretFlagValues(t *testing.T) {
	for _, argv := range [][]string{
		{"metricgate", "check", "-token", "ghp_abc123def456"},
		{"metricgate", "check", "-token=ghp_abc123def456"},
		{"metricgate", "check", "--password", "hunter2"},
		{"metricgate", "check", "--api-key=sk-live-9f3a"},
	} {
		got := formatInvocation(argv)
		if strings.Contains(got, "ghp_abc123def456") || strings.Contains(got, "hunter2") ||
			strings.Contains(got, "sk-live-9f3a") {
			t.Errorf("formatInvocation(%q) leaked a secret: %q", argv, got)
		}
		if !strings.Contains(got, redactedValue) {
			t.Errorf("formatInvocation(%q) did not redact: %q", argv, got)
		}
	}
}

// A secret shape can arrive with no flag name to match on — as a positional or
// an env assignment — so the value itself is judged. This is the case a
// name-only list misses.
func TestInvocationRedactsCredentialShapedValue(t *testing.T) {
	got := formatInvocation([]string{"metricgate", "check", "GITHUB_TOKEN=ghp_abc123def456"})
	if strings.Contains(got, "ghp_abc123def456") {
		t.Fatalf("credential-shaped positional leaked: %q", got)
	}
	if !strings.Contains(got, "GITHUB_TOKEN="+redactedValue) {
		t.Fatalf("env assignment should keep its name and mask its value: %q", got)
	}
}

// Redacting too much is its own failure: the column exists to show which flags
// produced a verdict, so a path that happens to contain a letter sequence must
// stay readable.
func TestInvocationDoesNotRedactOrdinaryPaths(t *testing.T) {
	for _, path := range []string{"/home/amy/sk-notes/report.md", "./tokens.csv", "-out", "secrets-report.sarif"} {
		got := formatInvocation([]string{"metricgate", "check", path})
		if strings.Contains(got, redactedValue) {
			t.Errorf("formatInvocation redacted a non-secret token %q: %q", path, got)
		}
	}
}

// A positional named like a flag must not swallow the argument after it.
func TestInvocationOnlyLetsFlagTokensConsumeAValue(t *testing.T) {
	got := formatInvocation([]string{"metricgate", "check", "src/token", "./secret-dir/main.go"})
	want := "metricgate check src/token ./secret-dir/main.go"
	if got != want {
		t.Fatalf("positional argument consumed its neighbour:\n got %q\nwant %q", got, want)
	}
}

func TestInvocationQuotesTokensThatNeedIt(t *testing.T) {
	got := formatInvocation([]string{"metricgate", "check", ".", "-config", "my policy.yaml"})
	if !strings.Contains(got, `'my policy.yaml'`) {
		t.Fatalf("a path with a space must be quoted to stay pasteable: %q", got)
	}
	// A token with no shell metacharacters stays bare, so the common line reads
	// like the command actually typed.
	if strings.Contains(got, `'metricgate'`) || strings.Contains(got, `'check'`) {
		t.Fatalf("ordinary tokens must not be quoted: %q", got)
	}
}

func TestInvocationQuotesEmbeddedSingleQuote(t *testing.T) {
	got := formatInvocation([]string{"metricgate", "check", "it's here"})
	if !strings.Contains(got, `'it'\''s here'`) {
		t.Fatalf("single quote not spliced the way a shell expects: %q", got)
	}
}

func TestInvocationTruncatesABlowupButMarksIt(t *testing.T) {
	argv := []string{"metricgate", "check"}
	for i := 0; i < 600; i++ {
		argv = append(argv, "src/pkg/file_000123.go")
	}
	got := formatInvocation(argv)
	if n := len([]rune(got)); n > maxInvocationRunes {
		t.Fatalf("stored invocation is %d runes, over the %d bound", n, maxInvocationRunes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("a truncated line must say so, not look like a complete command: %q", got[len(got)-16:])
	}
}

func TestInvocationEmptyArgvIsEmpty(t *testing.T) {
	if got := formatInvocation(nil); got != "" {
		t.Fatalf("empty argv must give an empty invocation, got %q", got)
	}
}

// The redaction list is a map keyed on the trimmed flag name, so a typo in one
// entry silently disables it and nothing else notices. Pinning the spellings
// keeps `-api-key` and `-apikey` both live.
func TestSecretFlagNamesAreSpelledAsTheFlagsAre(t *testing.T) {
	for _, name := range []string{"token", "password", "secret", "api-key", "apikey", "authorization", "auth"} {
		if !secretFlags[name] {
			t.Errorf("secretFlags lost %q", name)
		}
	}
	if secretFlags["config"] || secretFlags["base"] || secretFlags["format"] {
		t.Fatal("a flag that carries a path is not a secret flag; redacting it defeats the column")
	}
}
