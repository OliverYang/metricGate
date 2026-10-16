package trace

import (
	"strings"
	"testing"
)

func TestParseKeepsWhitelistedKeys(t *testing.T) {
	attrs, dropped := Parse("model=gpt-5,harness=codex")
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	want := []Attr{{Key: "harness", Value: "codex"}, {Key: "model", Value: "gpt-5"}}
	if len(attrs) != len(want) {
		t.Fatalf("attrs = %+v, want %+v", attrs, want)
	}
	for i := range want {
		if attrs[i] != want[i] {
			t.Errorf("attrs[%d] = %+v, want %+v", i, attrs[i], want[i])
		}
	}
}

// An unknown key must be dropped and counted, never stored: the whitelist is
// what keeps a report grouped by an attribute trustworthy.
func TestParseDropsUnknownKeysAndCountsThem(t *testing.T) {
	attrs, dropped := Parse("model=gpt-5,modle=oops,temperature=0.2")
	if dropped != 2 {
		t.Fatalf("dropped = %d, want 2", dropped)
	}
	if len(attrs) != 1 || attrs[0].Key != "model" {
		t.Fatalf("attrs = %+v, want only model", attrs)
	}
}

func TestParseDropsMalformedAndEmpty(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		kept    int
		dropped int
	}{
		{"no equals", "model", 0, 1},
		{"empty value", "model=", 0, 1},
		{"empty key", "=gpt-5", 0, 1},
		{"blank pairs skipped not dropped", "model=gpt-5,, ", 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attrs, dropped := Parse(tc.raw)
			if len(attrs) != tc.kept || dropped != tc.dropped {
				t.Errorf("Parse(%q) = %d attrs / %d dropped, want %d / %d", tc.raw, len(attrs), dropped, tc.kept, tc.dropped)
			}
		})
	}
}

func TestParseTruncatesLongValues(t *testing.T) {
	long := strings.Repeat("a", MaxValueLen+50)
	attrs, _ := Parse("model=" + long)
	if len(attrs) != 1 {
		t.Fatalf("attrs = %+v, want 1", attrs)
	}
	if len(attrs[0].Value) != MaxValueLen {
		t.Errorf("value length = %d, want %d", len(attrs[0].Value), MaxValueLen)
	}
}

// The truncation must not split a multi-byte rune, or the stored value is not
// valid UTF-8.
func TestTruncateKeepsValidUTF8(t *testing.T) {
	// Each "é" is two bytes; 200 of them is 400 bytes, past the cap.
	value := strings.Repeat("é", 200)
	got := truncate(value)
	if len(got) > MaxValueLen {
		t.Fatalf("truncated length = %d, want <= %d", len(got), MaxValueLen)
	}
	if !isValidUTF8(got) {
		t.Errorf("truncate split a rune: %q", got)
	}
}

// isValidUTF8 is a tiny local check so the test does not depend on the
// unicode/utf8 package that truncate avoids.
func isValidUTF8(s string) bool {
	for i := 0; i < len(s); {
		b := s[i]
		switch {
		case b < 0x80:
			i++
		case b&0xE0 == 0xC0:
			if i+1 >= len(s) || s[i+1]&0xC0 != 0x80 {
				return false
			}
			i += 2
		case b&0xF0 == 0xE0:
			if i+2 >= len(s) || s[i+1]&0xC0 != 0x80 || s[i+2]&0xC0 != 0x80 {
				return false
			}
			i += 3
		default:
			return false
		}
	}
	return true
}

func TestLastWriteWinsWithoutDoubleCounting(t *testing.T) {
	attrs, dropped := Parse("model=first,model=second")
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
	if len(attrs) != 1 || attrs[0].Value != "second" {
		t.Fatalf("attrs = %+v, want one model=second", attrs)
	}
}
