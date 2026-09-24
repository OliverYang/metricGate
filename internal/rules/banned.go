package rules

import (
	"fmt"
	"strings"

	"metricgate/internal/config"
	"metricgate/internal/model"
	"metricgate/internal/parser"
)

// BanKind is how a banned entry is matched.
type BanKind string

const (
	// BanCall matches a call. The pattern is dot-separated: in Go
	// "pkg.Name" (or a bare "name" for an unqualified call), in Java
	// "receiver.method" or "Object.field.method".
	BanCall BanKind = "call"
	// BanImport matches an imported package path.
	BanImport BanKind = "import"
)

// Ban is one organisation-level banned API or import, as compiled from the
// policy's `banned-api:` list. This is the data form of the constraint layer:
// adding an organisational rule means editing YAML, not Go.
type Ban struct {
	Kind   BanKind
	Match  string   // pattern, with the trailing "*" folded into Prefix
	Prefix bool     // match by name prefix (pattern ended with "*")
	Reason string   // shown in the finding message and the context pack
	Except []string // file-name suffixes where the ban does not apply
}

// RuleID is the stable finding id of a ban: "banned-" plus the pattern with
// dots and slashes folded to dashes. A prefix match keeps its trailing "*", so
// the family "banned-fmt-Print*" and one member "banned-fmt-Printf" can be
// configured apart.
func (b Ban) RuleID() string {
	m := b.Match
	if b.Prefix {
		m += "*"
	}
	m = strings.ReplaceAll(m, ".", "-")
	return "banned-" + strings.ReplaceAll(m, "/", "-")
}

// Title is the one-line description used by context packs and SARIF.
func (b Ban) Title() string {
	if b.Reason != "" {
		return b.Reason
	}
	return "banned " + string(b.Kind) + ": " + b.Match
}

// RulesFor returns the full rule pack of a language: the built-in rules plus
// the rules compiled from the policy's ban list. The engine and the context
// generator both call it, so what a prompt promises and what the gate enforces
// cannot drift apart.
func RulesFor(id parser.LanguageID, cfg *config.Config) []*Rule {
	pack := append([]*Rule{}, ForLanguage(id)...)
	if cfg == nil {
		return pack
	}
	return append(pack, BannedRules(string(id), BansFromConfig(cfg.Bans(string(id))))...)
}

// BansFromConfig compiles policy entries, dropping the ones that cannot be
// expressed: a bad entry must never break a run.
func BansFromConfig(entries []config.BanConfig) []Ban {
	out := make([]Ban, 0, len(entries))
	for _, e := range entries {
		if b, ok := banFromConfig(e); ok {
			out = append(out, b)
		}
	}
	return out
}

func banFromConfig(e config.BanConfig) (Ban, bool) {
	kind := BanKind(strings.TrimSpace(e.Kind))
	if kind == "" {
		kind = BanCall
	}
	if kind != BanCall && kind != BanImport {
		return Ban{}, false
	}
	match := strings.TrimSpace(e.Match)
	if match == "" {
		return Ban{}, false
	}
	prefix := false
	if kind == BanCall && strings.HasSuffix(match, "*") {
		prefix, match = true, strings.TrimSuffix(match, "*")
	}
	if strings.Trim(match, ".") == "" {
		return Ban{}, false
	}
	return Ban{
		Kind:   kind,
		Match:  match,
		Prefix: prefix,
		Reason: strings.TrimSpace(e.Reason),
		Except: e.ExceptFiles,
	}, true
}

// banBuilders is the language registry for compiled bans: a language that
// cannot express a shape contributes nothing for it.
var banBuilders = map[string]map[BanKind]func(Ban) *Rule{
	"go":   {BanCall: goCallBan, BanImport: goImportBan},
	"java": {BanCall: javaCallBan, BanImport: javaImportBan},
}

// BannedRules turns compiled bans into rules for one language.
func BannedRules(lang string, bans []Ban) []*Rule {
	builders := banBuilders[lang]
	var out []*Rule
	for _, b := range bans {
		build := builders[b.Kind]
		if build == nil {
			continue
		}
		if r := build(b); r != nil {
			out = append(out, r)
		}
	}
	return out
}

// --- Go -------------------------------------------------------------------

func goCallBan(b Ban) *Rule {
	pkg, name := splitLast(b.Match)
	if pkg == "" {
		return callRule(b, `(call_expression function: (identifier) @fn) @call`,
			func(ctx *MatchCtx) (string, bool) {
				return matchedName(ctx, name, b)
			})
	}
	return callRule(b, `
(call_expression
  function: (selector_expression
    operand: (identifier) @pkg
    field: (field_identifier) @fn)) @call`,
		func(ctx *MatchCtx) (string, bool) {
			if ctx.Text(ctx.Captures["pkg"]) != pkg {
				return "", false
			}
			fn, ok := matchedName(ctx, name, b)
			return pkg + "." + fn, ok
		})
}

// matchedName reads the "fn" capture and applies the ban's name matching.
func matchedName(ctx *MatchCtx, name string, b Ban) (string, bool) {
	fn := ctx.Text(ctx.Captures["fn"])
	if !nameMatch(fn, name, b.Prefix) {
		return "", false
	}
	return fn, true
}

func goImportBan(b Ban) *Rule {
	return importRule(b, `
(import_spec path: (interpreted_string_literal) @path)
`, func(ctx *MatchCtx) string {
		return strings.Trim(ctx.Text(ctx.Captures["path"]), `"`)
	})
}

// --- Java -----------------------------------------------------------------

// javaCallBan dispatches on the receiver shape: a bare method name, one
// receiver identifier, or the classic two-part "System.out.println". Deeper
// receivers would need type resolution and are deliberately out of scope.
func javaCallBan(b Ban) *Rule {
	parts := strings.Split(b.Match, ".")
	method, recv := parts[len(parts)-1], parts[:len(parts)-1]
	switch len(recv) {
	case 0:
		return callRule(b, `(method_invocation name: (identifier) @fn) @call`,
			func(ctx *MatchCtx) (string, bool) { return matchedName(ctx, method, b) })
	case 1:
		return callRule(b, `
(method_invocation
  object: (identifier) @obj
  name: (identifier) @fn) @call`,
			func(ctx *MatchCtx) (string, bool) {
				if ctx.Text(ctx.Captures["obj"]) != recv[0] {
					return "", false
				}
				fn, ok := matchedName(ctx, method, b)
				return recv[0] + "." + fn, ok
			})
	case 2:
		return callRule(b, `
(method_invocation
  object: (field_access
    object: (identifier) @sys
    field: (identifier) @stream)
  name: (identifier) @fn) @call`,
			func(ctx *MatchCtx) (string, bool) {
				if ctx.Text(ctx.Captures["sys"]) != recv[0] ||
					ctx.Text(ctx.Captures["stream"]) != recv[1] {
					return "", false
				}
				fn, ok := matchedName(ctx, method, b)
				return recv[0] + "." + recv[1] + "." + fn, ok
			})
	default:
		return nil
	}
}

func javaImportBan(b Ban) *Rule {
	return importRule(b, `(import_declaration (scoped_identifier) @path)`,
		func(ctx *MatchCtx) string { return ctx.Text(ctx.Captures["path"]) })
}

// --- shared builders ------------------------------------------------------

// callRule builds a ban rule over a call-shaped query. match returns the
// spelled call to report, or false when this match is not the banned one.
func callRule(b Ban, query string, match func(*MatchCtx) (string, bool)) *Rule {
	return &Rule{
		ID: b.RuleID(), Title: b.Title(),
		DefaultSeverity: model.SevWarning, DefaultEnabled: true,
		Query:         query,
		AnchorCapture: "call",
		OnMatch: func(ctx *MatchCtx) *model.Finding {
			if excluded(ctx, b) {
				return nil
			}
			seen, ok := match(ctx)
			if !ok {
				return nil
			}
			return at(ctx, "call", banMessage(b, seen))
		},
	}
}

// importRule builds a ban rule over an import-shaped query. path extracts the
// imported path from the match.
func importRule(b Ban, query string, path func(*MatchCtx) string) *Rule {
	return &Rule{
		ID: b.RuleID(), Title: b.Title(),
		DefaultSeverity: model.SevWarning, DefaultEnabled: true,
		Query:         query,
		AnchorCapture: "path",
		OnMatch: func(ctx *MatchCtx) *model.Finding {
			if excluded(ctx, b) {
				return nil
			}
			got := path(ctx)
			if got != b.Match && !strings.HasPrefix(got, b.Match+".") {
				return nil
			}
			return at(ctx, "path", banMessage(b, got))
		},
	}
}

func splitLast(match string) (head, tail string) {
	if i := strings.LastIndex(match, "."); i >= 0 {
		return match[:i], match[i+1:]
	}
	return "", match
}

func nameMatch(got, want string, prefix bool) bool {
	if want == "" {
		return prefix
	}
	if prefix {
		return strings.HasPrefix(got, want)
	}
	return got == want
}

// excluded reports whether this file is outside the ban's reach, through the
// ban's own except_files or through a per-rule allowed_files override.
func excluded(ctx *MatchCtx, b Ban) bool {
	for _, suffix := range append(append([]string{}, b.Except...), ctx.OptStrings("allowed_files")...) {
		if suffix != "" && strings.HasSuffix(ctx.File, suffix) {
			return true
		}
	}
	return false
}

func banMessage(b Ban, seen string) string {
	if b.Reason != "" {
		return fmt.Sprintf("%s is banned: %s", seen, b.Reason)
	}
	return fmt.Sprintf("%s is banned by policy", seen)
}

func at(ctx *MatchCtx, capture, msg string) *model.Finding {
	n := ctx.Captures[capture]
	return &model.Finding{
		StartLine: int(n.StartPosition().Row) + 1,
		EndLine:   int(n.EndPosition().Row) + 1,
		Message:   msg,
	}
}
