package rulegen

import (
	"fmt"
	"strings"
)

// banEntryYAML renders one BanConfig as a banned-api list entry with
// double-quoted scalars so CJK/colons survive the round trip.
func banEntryYAML(s *Spec) string {
	var b strings.Builder
	b.WriteString("      - kind: " + s.Kind + "\n")
	fmt.Fprintf(&b, "        match: %s\n", yamlStr(s.Match))
	if s.Reason != "" {
		fmt.Fprintf(&b, "        reason: %s\n", yamlStr(s.Reason))
	}
	if s.Use != "" {
		fmt.Fprintf(&b, "        use: %s\n", yamlStr(s.Use))
	}
	if len(s.ArgsBan) > 0 {
		quoted := make([]string, len(s.ArgsBan))
		for i, a := range s.ArgsBan {
			quoted[i] = yamlStr(a)
		}
		fmt.Fprintf(&b, "        args_ban: [%s]\n", strings.Join(quoted, ", "))
	}
	if len(s.ExceptFiles) > 0 {
		quoted := make([]string, len(s.ExceptFiles))
		for i, e := range s.ExceptFiles {
			quoted[i] = yamlStr(e)
		}
		fmt.Fprintf(&b, "        except_files: [%s]\n", strings.Join(quoted, ", "))
	}
	return b.String()
}

// entryYAML renders the merge-ready policy fragment: the banned-api key plus
// its first entry at list-item indent, dropped straight under an existing
// languages.<lang>: section (list items at 6 spaces, fields at 8).
func entryYAML(s *Spec) string {
	var b strings.Builder
	b.WriteString("    banned-api:\n")
	b.WriteString(banEntryYAML(s))
	return b.String()
}

// policyYAML renders the full minimal policy the verification runs against:
// the rule under test plus an unknown-dependency veto so fixture imports do
// not drown the verdict in unrelated dependency noise, and a warning-level
// fail_on so severity behaviour is exercised the same way gates enforce it.
func policyYAML(s *Spec) string {
	var b strings.Builder
	b.WriteString("gate:\n  fail_on: warning\nstore:\n  enabled: false\nlanguages:\n")
	fmt.Fprintf(&b, "  %s:\n", s.Lang)
	b.WriteString("    banned-api:\n")
	b.WriteString(banEntryYAML(s))
	b.WriteString("    rules:\n")
	fmt.Fprintf(&b, "      unknown-dependency:\n        enabled: false\n")
	sev := s.Severity
	if sev == "" {
		sev = "warning"
	}
	fmt.Fprintf(&b, "      %s:\n        severity: %s\n", RuleID(s.Match), sev)
	return b.String()
}

// yamlStr double-quotes a scalar, escaping backslashes and quotes.
func yamlStr(s string) string {
	q := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
	return `"` + q + `"`
}
