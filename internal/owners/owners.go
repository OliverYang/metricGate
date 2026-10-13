// Package owners answers "who is this code's owner?" from the repository's own
// declaration of the answer — a CODEOWNERS file — without inventing a second
// source of truth.
//
// A gate that reports a finding says nothing about who can act on it. The
// acceptance ledger (internal/accept) already records that something was
// conceded and by whom; this package closes the other half: the routing table
// that turns "this rule is firing" and "this file is in the blast radius" into
// a person or a team. The repository already declares the mapping in
// CODEOWNERS, so parsing it is the whole job — a parallel ownership config
// would immediately disagree with it.
//
// The syntax is deliberately the CODEOWNERS subset a static read can support:
// comment lines, a `pattern owner...` pair, and nothing else. It is parsed the
// way git itself reads it, and the last matching pattern wins, because that is
// the rule reviewers rely on when they re-read their own file. Report sections
// that need it simply ask for a file's owner and print the answer.
package owners

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultPaths are the CODEOWNERS locations, in the order they are tried.
// The .github/ form is what GitHub documents; the trailing forms are the
// historical spellings that still parse.
var DefaultPaths = []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}

// Unknown is what a file owned by nobody reports. It is explicit rather than
// an empty string, because "no one owns this" is itself an actionable finding:
// an unowned security-critical file is a gap, and a blank column would read as
// a rendering bug.
const Unknown = "unknown"

// Rule is one CODEOWNERS line: what it names, and who owns that.
type Rule struct {
	// Pattern is the CODEOWNERS pattern (already normalised: no leading '/'
	// and no trailing spaces, but otherwise verbatim).
	Pattern string `json:"pattern"`
	// Owners lists the owners the line declares. A CODEOWNERS line without an
	// owner is valid (it clears earlier matches) and yields an empty slice.
	Owners []string `json:"owners,omitempty"`
	// Line is the 1-based source line, for provenance in a report.
	Line int `json:"line"`
}

// Map is a parsed CODEOWNERS file plus the file it came from.
type Map struct {
	Rules  []Rule `json:"rules"`
	Source string `json:"source"`
}

// Find reads the first CODEOWNERS file that exists at root, and reports which
// location it used. An absent file is not an error: a repository with no
// CODEOWNERS is a repository where nobody has claimed the routing table, and
// the caller shows every owner as unknown.
func Find(root string) (Map, error) {
	for _, rel := range DefaultPaths {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		return ParseFile(string(data), rel), nil
	}
	return Map{}, nil
}

// ParseFile parses one CODEOWNERS document. rel is kept as the source so a
// report can name the file the answer came from.
func ParseFile(text, rel string) Map {
	m := Map{Source: rel}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rule, ok := parseLine(line, i+1)
		if !ok {
			continue // a line with no pattern is not a rule; skipping is the honest reading
		}
		m.Rules = append(m.Rules, rule)
	}
	return m
}

// parseLine breaks one CODEOWNERS line into its rule.
func parseLine(line string, lineNo int) (Rule, bool) {
	// A leading '!' inverts a pattern, which git supports and a static owner
	// lookup cannot honour: a rule that applies everywhere except here needs a
	// whole-file evaluation, not one line. Refusing is better than silently
	// keeping the earlier (wrong) match; the rejected line is skipped, so the
	// file keeps whatever the earlier rules said.
	if strings.HasPrefix(line, "!") {
		return Rule{}, false
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return Rule{}, false
	}
	pattern := strings.TrimPrefix(fields[0], "/")
	if pattern == "" {
		return Rule{}, false
	}
	return Rule{Pattern: pattern, Owners: fields[1:], Line: lineNo}, true
}

// OwnerOf returns the owner of one repository-relative path, or "" when nothing
// claims it. The last matching rule wins, which is the rule git applies —
// re-declaring a path later overrides what came before.
func (m Map) OwnerOf(file string) string {
	owner := ""
	for _, r := range m.Rules {
		if len(r.Owners) == 0 {
			continue // a clearing line: the file falls back to the earlier match
		}
		if matchPattern(r.Pattern, file) {
			owner = firstOwner(r.Owners)
		}
	}
	return owner
}

// The display value is one name, not a list: a report column has to stay
// readable, and a list of three people in a column reads as noise rather than
// routing information. The other names stay reachable through OwnersOf.
func firstOwner(owners []string) string {
	for _, o := range owners {
		if o = strings.TrimSpace(o); o != "" {
			return o
		}
	}
	return ""
}

// OwnersOf returns every owner named by the winning rule, so a caller that
// wants the full list does not have to reconstruct it from firstOwner.
func (m Map) OwnersOf(file string) []string {
	var best []string
	for _, r := range m.Rules {
		if len(r.Owners) == 0 {
			continue
		}
		if matchPattern(r.Pattern, file) {
			best = r.Owners
		}
	}
	return best
}

// Label is the column value: the owner, or an explicit unknown. A report prints
// it verbatim and never special-cases the empty string.
func (m Map) Label(file string) string {
	if o := m.OwnerOf(file); o != "" {
		return o
	}
	return Unknown
}

// UnknownShare reports how many of the given files nobody owns. It is the one
// number that makes the column actionable: a routing table that covers nothing
// is the same gap a missing CODEOWNERS is.
func (m Map) UnknownShare(files []string) (unknown, total int) {
	for _, f := range files {
		total++
		if m.OwnerOf(f) == "" {
			unknown++
		}
	}
	return unknown, total
}

// Owners lists every distinct owner that claims at least one of the files,
// sorted. The list drives a "group by owner" report: each owner's group is
// built by one pass over the files, not by another parse.
func (m Map) Owners(files []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		o := m.OwnerOf(f)
		if o == "" || seen[o] {
			continue
		}
		seen[o] = true
		out = append(out, o)
	}
	sort.Strings(out)
	return out
}

// Empty reports whether no rules were parsed, so a caller can say "no
// CODEOWNERS found" distinctly from "nothing claims these files".
func (m Map) Empty() bool { return len(m.Rules) == 0 }

// Describe renders one line naming the source and how many rules it carries,
// for a report header.
func (m Map) Describe() string {
	if m.Empty() {
		return "no CODEOWNERS file found — every owner shows as " + Unknown
	}
	return fmt.Sprintf("%s: %d rule(s)", m.Source, len(m.Rules))
}

// matchPattern applies the CODEOWNERS pattern vocabulary. The shapes supported
// are the ones that actually appear in repositories:
//
//   - everything
//     docs/**              everything under docs/ at any depth
//     docs/                the docs/ subtree
//     *.go                 any Go file, at any depth
//     apps/web             an exact path, or a path prefix
//     internal/            the internal/ subtree
//
// The matching is deliberately the mirror of a policy file pattern
// (internal/rules fileMatches): CODEOWNERS patterns are the reverse of a rule's
// target list, so reusing one matcher for both would make one of the two wrong.
func matchPattern(pattern, file string) bool {
	if pattern == "" || file == "" {
		return false
	}
	if pattern == "*" || pattern == "**" {
		return true
	}
	if subtree, ok := strings.CutSuffix(pattern, "/**"); ok {
		return inSubtree(subtree, file)
	}
	if dir := strings.TrimSuffix(pattern, "/"); dir != pattern {
		return inSubtree(dir, file)
	}
	return matchPath(pattern, file)
}

// inSubtree reports whether file is dir or anything under it.
func inSubtree(dir, file string) bool {
	return file == dir || strings.HasPrefix(file, dir+"/")
}

// matchPath applies the path-level vocabulary: a "**/" prefix names a
// basename at any depth, an exact or prefix spelling names the path, and
// anything else is a glob.
func matchPath(pattern, file string) bool {
	if rest, ok := strings.CutPrefix(pattern, "**/"); ok {
		return strings.HasSuffix(file, rest) || globMatch(rest, filepath.Base(file))
	}
	if strings.HasSuffix(file, pattern) || inSubtree(pattern, file) {
		return true
	}
	return globMatch(pattern, filepath.Base(file)) || globMatch(pattern, file)
}

// globMatch runs filepath.Match, treating an invalid pattern as a non-match:
// a malformed CODEOWNERS line should not panic, and silently claiming files
// would be worse.
func globMatch(pattern, name string) bool {
	ok, err := filepath.Match(pattern, name)
	return err == nil && ok
}
