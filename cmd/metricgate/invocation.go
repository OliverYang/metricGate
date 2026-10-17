// Recording the command line that produced a run. `check` writes a row to the
// store, and the row is read back months later in Cockpit by someone who was
// not there. Without the invocation, a run says "0 errors, 431 warnings" and
// nothing about the flags that decided it: the same tree reports differently
// under -diff, under a different -arch-base, or with a different -config, and
// none of that is inferable from the counts.
//
// The command line is also the one piece of provenance that can carry a
// credential, so the redaction below is not optional decoration — a stored
// invocation is read by everyone who can open the dashboard.

package main

import (
	"os"
	"strings"
)

// maxInvocationRunes bounds the stored command line. A real invocation is a
// verb plus a handful of flags; the bound exists so a shell mistake (an
// operator pastes 10,000 file paths) cannot turn one run row into a
// multi-megabyte cell that the table then has to render.
const maxInvocationRunes = 512

// secretFlags are the flag names whose *value* must never reach the store.
// They are matched on the name only, so `-config ./metricgate.yaml` is kept
// whole and `-token abc123` is not. A flag that is not listed keeps its value
// verbatim: the point of the column is the configuration, so redacting
// something benign destroys the thing it was added for.
var secretFlags = map[string]bool{
	"token": true, "tokens": true, "password": true, "passwd": true,
	"secret": true, "secrets": true, "api-key": true, "apikey": true,
	"access-key": true, "authorization": true, "auth": true,
	"credentials": true, "private-key": true, "ssh-key": true,
}

// redactedValue is what replaces a secret flag's value. It is a fixed string
// rather than a length-preserving mask, because a mask that keeps the length
// leaks how long the secret was.
const redactedValue = "<redacted>"

// invocationLine renders the command line this process was started with. It
// returns "" when there is nothing to record, which callers treat exactly like
// an absent trace id: a legal state meaning "we do not know".
func invocationLine() string {
	return formatInvocation(os.Args)
}

// formatInvocation joins and redacts one argv. Split out from invocationLine
// so the rules are testable without replacing the process arguments.
//
// Every token is quoted if it needs it, rather than only the odd one, because
// a half-quoted line cannot be pasted back and a reader will paste it.
func formatInvocation(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	out := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		token := argv[i]
		// `-name=value` is the same flag as `-name value`; the redaction has to
		// catch both spellings or the operator's own habit decides whether a
		// secret is stored.
		if name, _, hasValue := strings.Cut(token, "="); hasValue && secretFlags[strings.TrimLeft(name, "-")] {
			out = append(out, quoteArg(name+"="+redactedValue))
			continue
		}
		// A secret flag can also take its value as the next argument. Only a
		// flag token starting with "-" may consume the following token, so a
		// positional named "token" cannot swallow the path after it.
		if isFlag(token) && secretFlags[strings.TrimLeft(token, "-")] && i+1 < len(argv) {
			out = append(out, quoteArg(token), quoteArg(redactedValue))
			i++
			continue
		}
		out = append(out, quoteArg(redactToken(token)))
	}
	line := strings.Join(out, " ")
	if r := []rune(line); len(r) > maxInvocationRunes {
		line = string(r[:maxInvocationRunes-1]) + "…"
	}
	return line
}

// isFlag reports whether one argv token is an option rather than a positional
// argument. `--` itself is the end-of-flags marker and does not consume.
func isFlag(token string) bool {
	if !strings.HasPrefix(token, "-") || token == "-" || token == "--" {
		return false
	}
	// Go's flag package stops at the first non-flag argument, so `-x -y` after
	// a positional is data, not an option.
	return !strings.Contains(token, " ")
}

// credentialPatterns catches secrets that arrive as a bare positional or an
// env-assignment argument, where no flag name is available to match against.
// A GitHub PAT or an AWS key id has a shape, and the shape is the only clue.
var credentialPrefixes = []string{
	"ghp_", "github_pat_", "gho_", "ghu_", "ghs_", "ghr_",
	"xoxb-", "xoxp-", "xoxa-", "AKIA", "ASIA", "AIza", "sk-",
}

// redactToken masks a token whose value has the shape of a credential.
func redactToken(token string) string {
	name, value, hasValue := strings.Cut(token, "=")
	if hasValue && looksLikeCredential(value) {
		return name + "=" + redactedValue
	}
	if looksLikeCredential(token) {
		return redactedValue
	}
	return token
}

// looksLikeCredential reports whether a bare value matches a known secret
// shape. Prefix only, never length or character class beyond the prefix, so an
// ordinary path or identifier cannot trip it.
func looksLikeCredential(value string) bool {
	// An env assignment passed as an argument (FOO=bar) is judged on its value.
	if _, v, ok := strings.Cut(value, "="); ok && v != value {
		value = v
	}
	for _, p := range credentialPrefixes {
		if strings.HasPrefix(value, p) && len(value) > len(p) {
			return true
		}
	}
	return false
}

// quoteArg wraps one token in single quotes when it contains anything a shell
// would re-interpret. Only single quotes are used, so no double-quote
// nesting rules are needed; an embedded single quote is spliced out of the
// quoted run, which is what a shell prompt does with it.
func quoteArg(token string) string {
	if token == "" {
		return "''"
	}
	if !strings.ContainsAny(token, " \t\n\"'\\$`;&|()<>#*?[]{}!~^") {
		return token
	}
	return "'" + strings.ReplaceAll(token, "'", `'\''`) + "'"
}
