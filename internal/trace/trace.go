// Package trace defines the small, closed vocabulary of attributes that may be
// attached to a trace id: the LLM-side dimensions (model, prompt fingerprint,
// harness, agent, branch, tool-invocation count) that change far too fast to
// each earn a fixed column in the run schema.
//
// The set is a whitelist on purpose. A trace_attrs table with no gate becomes a
// junk drawer — anyone can write any key, and a report grouped by that key is
// no longer trustworthy. Unknown keys are dropped and counted rather than
// stored, the same discipline the rule DSL applies to invalid entries: a typo
// must be visible as a number, not silently admitted.
//
// This package carries no storage. It is the vocabulary and the parser; the
// store persists what it returns, and the CLIs read the ambient value.
package trace

import (
	"sort"
	"strings"
)

// Env is the environment variable a host sets to attach attributes to the
// ambient trace ($METRICGATE_TRACE_ID). Its value is a comma-separated list of
// key=value pairs, e.g.:
//
//	METRICGATE_TRACE_ATTRS="model=gpt-5,harness=codex,prompt_digest=sha256:ab12"
//
// A single variable rather than one per key keeps the surface small: the key
// set is fixed by the whitelist below, so there is nothing to discover by
// naming six separate variables.
const Env = "METRICGATE_TRACE_ATTRS"

// MaxValueLen caps a stored value. A truncated fingerprint is still useful for
// grouping; an unbounded value turns the table into a place to smuggle source
// code, which the call log's own constraints forbid.
const MaxValueLen = 256

// Attr is one key/value pair attached to a trace.
type Attr struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// keys is the whitelist. It is a map so membership reads as a set lookup, and
// Keys() hands back a sorted slice for deterministic output.
var keys = map[string]bool{
	"model":            true,
	"prompt_digest":    true,
	"harness":          true,
	"agent":            true,
	"branch":           true,
	"tool_invocations": true,
}

// Keys returns the whitelisted attribute names, sorted, so a caller can list
// them (an error message, a help line) without re-deriving the order.
func Keys() []string {
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Allowed reports whether key is in the whitelist.
func Allowed(key string) bool { return keys[key] }

// Parse reads a raw $METRICGATE_TRACE_ATTRS value into the attributes that
// survive the whitelist and the value cap.
//
// It returns the kept attributes sorted by key, and how many entries were
// dropped (unknown key, empty key, or empty value). The dropped count is
// returned rather than logged because the caller — a check run, an MCP call —
// is where "your model attribute did not take effect" has to be said.
//
// A malformed pair (no '=') is dropped, not guessed at: silently treating
// "gpt-5" as a key with an empty value would store a row nobody can group by.
func Parse(raw string) (attrs []Attr, dropped int) {
	seen := map[string]bool{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !ok || key == "" || value == "" || !Allowed(key) {
			dropped++
			continue
		}
		value = truncate(value)
		if seen[key] {
			// Last write wins, but do not double-count the key.
			for i := range attrs {
				if attrs[i].Key == key {
					attrs[i].Value = value
					break
				}
			}
			continue
		}
		seen[key] = true
		attrs = append(attrs, Attr{Key: key, Value: value})
	}
	sort.Slice(attrs, func(i, j int) bool { return attrs[i].Key < attrs[j].Key })
	return attrs, dropped
}

// truncate caps a value at MaxValueLen bytes. The cut is on a rune boundary so
// a truncated multi-byte value stays valid UTF-8; a value already short enough
// is returned unchanged.
func truncate(value string) string {
	if len(value) <= MaxValueLen {
		return value
	}
	cut := MaxValueLen
	for cut > 0 && !utf8Start(value[cut]) {
		cut--
	}
	return value[:cut]
}

// utf8Start reports whether b is a UTF-8 sequence start byte (not a
// continuation byte 10xxxxxx). It lets truncate back off to a rune boundary
// without pulling in the whole unicode/utf8 package for one test.
func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
