package rules

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"metricgate/internal/config"
	"metricgate/internal/model"
	"metricgate/internal/parser"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// BanKind is how a banned entry is matched.
type BanKind string

const (
	// BanCall matches a call. The pattern is dot-separated: in Go
	// "pkg.Name" (or a bare "name" for an unqualified call), in Java
	// "receiver.method" — the receiver may be any depth, matched by its
	// source spelling ("System.out.println", a fully qualified
	// "java.util.concurrent.Executors.newFixedThreadPool"); a short
	// receiver also matches its fully-qualified form.
	BanCall BanKind = "call"
	// BanImport matches an imported package path.
	BanImport BanKind = "import"
	// BanConstructor matches instance creation: new X(...) is an
	// object_creation_expression in Java, which call bans (method_invocation
	// queries) cannot see — the CONC-02-shaped gap. Java-only: Go has no
	// constructor syntax worth gating (its "new"/"make" are plain calls and
	// are already reachable via call bans). The match is the simple class
	// name; a trailing "*" prefix-matches.
	BanConstructor BanKind = "constructor"
)

// Ban is one organisation-level banned API or import, as compiled from the
// policy's `banned-api:` list. This is the data form of the constraint layer:
// adding an organisational rule means editing YAML, not Go.
type Ban struct {
	Kind    BanKind
	Match   string   // pattern, with the trailing "*" folded into Prefix
	Prefix  bool     // match by name prefix (pattern ended with "*")
	Reason  string   // shown in the finding message and the context pack
	Use     string   // compliant alternative ("use X instead"); appended to findings and digests
	ArgsBan []string // parameter-aware: fire only when a string-literal argument equals one of these
	Except  []string // file-name suffixes where the ban does not apply
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
// the rules compiled from the policy's ban list and the project's custom
// DSL assertions. The engine and the context generator both call it, so
// what a prompt promises and what the gate enforces cannot drift apart.
// langPtr is the grammar, needed to validate DSL node kinds; nil skips DSL
// rules (callers that only enumerate rule metadata).
func RulesFor(id parser.LanguageID, cfg *config.Config, langPtr *sitter.Language) []*Rule {
	pack := append([]*Rule{}, ForLanguage(id)...)
	if cfg == nil {
		return pack
	}
	pack = append(pack, BannedRules(string(id), BansFromConfig(cfg.Bans(string(id))))...)
	if langPtr != nil {
		pack = append(pack, DslRules(string(id), langPtr, cfg.CustomRules(string(id)))...)
	}
	return pack
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
	if kind != BanCall && kind != BanImport && kind != BanConstructor {
		return Ban{}, false
	}
	match := strings.TrimSpace(e.Match)
	if match == "" {
		return Ban{}, false
	}
	prefix := false
	if (kind == BanCall || kind == BanConstructor) && strings.HasSuffix(match, "*") {
		prefix, match = true, strings.TrimSuffix(match, "*")
	}
	if strings.Trim(match, ".") == "" {
		return Ban{}, false
	}
	argsBan := e.ArgsBan
	if kind == BanImport {
		argsBan = nil // imports have no arguments
	}
	return Ban{
		Kind:    kind,
		Match:   match,
		Prefix:  prefix,
		Reason:  strings.TrimSpace(e.Reason),
		Use:     strings.TrimSpace(e.Use),
		ArgsBan: argsBan,
		Except:  e.ExceptFiles,
	}, true
}

// banBuilders is the language registry for compiled bans: a language that
// cannot express a shape contributes nothing for it.
var banBuilders = langBanBuilders()

// CanExpress reports whether a language has a builder for a ban kind. It is
// the one place that answers "could this policy entry ever fire here?", which
// the digest needs so it never advertises a constraint the gate cannot enforce
// and a caller needs before writing a ban into a language section.
func CanExpress(lang string, kind BanKind) bool {
	return banBuilders[lang][kind] != nil
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
		return callRule(b, `(call_expression
    function: (identifier) @fn
    arguments: (argument_list) @args) @call`,
			func(ctx *MatchCtx) (string, bool) {
				return matchedName(ctx, name, b)
			})
	}
	return callRule(b, `
(call_expression
  function: (selector_expression
    operand: (_) @recv
    field: (field_identifier) @fn)
  arguments: (argument_list) @args) @call`,
		func(ctx *MatchCtx) (string, bool) {
			recv := flatText(ctx, "recv")
			if !receiverMatch(recv, pkg) {
				return "", false
			}
			fn, ok := matchedName(ctx, name, b)
			if !ok {
				return "", false
			}
			return recv + "." + fn, ok
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

// flatText returns the captured node's source text with all whitespace
// removed, so a line-wrapped qualified name still compares equal to its
// policy spelling ("java.util.concurrent\n\t.Executors.newFixedThreadPool").
func flatText(ctx *MatchCtx, key string) string {
	n := ctx.Captures[key]
	if n == nil {
		return ""
	}
	return stripSpace(ctx.Text(n))
}

func stripSpace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// receiverMatch reports whether a captured receiver spells the banned one:
// exact equality, or the fully-qualified form "pkg.Recv" matching a short
// "Recv" ban (the inline-FQN escape).
func receiverMatch(got, want string) bool {
	return got == want || strings.HasSuffix(got, "."+want)
}

// bannedArg reports the first string-literal argument of the call whose
// value equals one of the ban's banned values ("" + false when none).
// Non-literal arguments (variables, method calls) never match: an algorithm
// picked at runtime stays a review question, not a false positive.
func bannedArg(ctx *MatchCtx, values []string) (string, bool) {
	args := ctx.Captures["args"]
	if args == nil {
		return "", false
	}
	for i := uint(0); i < args.NamedChildCount(); i++ {
		lit := stringLiteral(ctx.Src, args.NamedChild(i))
		if lit == "" {
			continue
		}
		for _, v := range values {
			if lit == v {
				return lit, true
			}
		}
	}
	return "", false
}

// stringLiteral returns the unquoted text of a string-literal node, or ""
// when the node is not one (Go "interpreted_string_literal", Java
// "string_literal", C# and VB "string_literal"). SQL has no separate string
// kind — quotes, numbers and booleans all come back as "literal" — so the
// quotes are what decide, and an unquoted literal is not a string.
func stringLiteral(src []byte, n *sitter.Node) string {
	if n == nil {
		return ""
	}
	switch n.Kind() {
	case "string", "string_literal", "interpreted_string_literal":
	case "literal":
		if !strings.HasPrefix(strings.TrimSpace(string(src[n.StartByte():n.EndByte()])), "'") {
			return ""
		}
	default:
		return ""
	}
	return strings.Trim(string(src[n.StartByte():n.EndByte()]), `"'`)
}

func goImportBan(b Ban) *Rule {
	return importRule(b, `
(import_spec path: (interpreted_string_literal) @path)
`, func(ctx *MatchCtx) string {
		return strings.Trim(ctx.Text(ctx.Captures["path"]), `"`)
	})
}

// --- Java -----------------------------------------------------------------

// javaCallBan matches a bare method name, or — for any receiver depth —
// captures the whole receiver expression and compares its text: identifier
// ("obj.help"), field_access chains ("System.out.println") and fully
// qualified names all fall out of one query. Matching stays by source
// spelling (no type resolution); a short receiver also matches its FQN form.
func javaCallBan(b Ban) *Rule {
	parts := strings.Split(b.Match, ".")
	method, recv := parts[len(parts)-1], parts[:len(parts)-1]
	if len(recv) == 0 {
		return callRule(b, `(method_invocation
    name: (identifier) @fn
    arguments: (argument_list) @args) @call`,
			func(ctx *MatchCtx) (string, bool) { return matchedName(ctx, method, b) })
	}
	want := strings.Join(recv, ".")
	return callRule(b, `
(method_invocation
  object: (_) @recv
  name: (identifier) @fn
  arguments: (argument_list) @args) @call`,
		func(ctx *MatchCtx) (string, bool) {
			got := flatText(ctx, "recv")
			if !receiverMatch(got, want) {
				return "", false
			}
			fn, ok := matchedName(ctx, method, b)
			if !ok {
				return "", false
			}
			return got + "." + fn, ok
		})
}

// javaConstructorBan matches instance creation. The type capture is a
// wildcard, so both "new SimpleDateFormat()" and the FQN spelling
// "new java.text.SimpleDateFormat()" (a scoped type node) match; the name
// compared is the type's last dotted segment. Generic types (new Foo<T>())
// are still outside the ban's reach.
func javaConstructorBan(b Ban) *Rule {
	return callRule(b, `
(object_creation_expression
  type: (_) @fn
  arguments: (argument_list) @args) @call`,
		func(ctx *MatchCtx) (string, bool) {
			typ := flatText(ctx, "fn")
			if i := strings.LastIndex(typ, "."); i >= 0 {
				typ = typ[i+1:]
			}
			if !nameMatch(typ, b.Match, b.Prefix) {
				return "", false
			}
			return typ, true
		})
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
			msg := banMessage(b, seen)
			if len(b.ArgsBan) > 0 {
				arg, hit := bannedArg(ctx, b.ArgsBan)
				if !hit {
					return nil
				}
				msg += fmt.Sprintf("（命中禁值参数 %q）", arg)
			}
			return at(ctx, "call", msg)
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
	return fileMatches(append(append([]string{}, b.Except...), ctx.OptStrings("allowed_files")...), ctx.File)
}

// fileMatches reports whether a file is named by any of the patterns. It is
// the one place that decides what a policy file pattern means, shared by a
// ban's except_files/allowed_files and a DSL rule's files so a policy author
// only has to learn the vocabulary once: an exact path, a path prefix
// ("cmd/app/"), a suffix ("_test.go"), or a glob matched against either the
// whole path or the base name.
//
// A leading "**/" means "at any depth" ("**/*_test.go" == every test file).
// It has to be handled here rather than handed to filepath.Match, which does
// not let a wildcard cross a separator: "**/*_test.go" would otherwise match
// only files exactly one directory down and silently skip the rest. Deeper
// "**" is left as an ordinary "*" (one path segment) on purpose — guessing
// at partial glob dialects is how a pattern quietly stops meaning anything.
func fileMatches(patterns []string, file string) bool {
	for _, pat := range patterns {
		if filePatternMatch(pat, file) {
			return true
		}
	}
	return false
}

// filePatternMatch reports whether one pattern names the file.
func filePatternMatch(pat, file string) bool {
	if pat == "" {
		return false
	}
	if pat == "**" {
		return true
	}
	if rest, ok := strings.CutPrefix(pat, "**/"); ok {
		return filePatternMatch(rest, file)
	}
	if strings.HasSuffix(file, pat) || strings.HasPrefix(file, pat) {
		return true
	}
	if m, _ := filepath.Match(pat, filepath.Base(file)); m {
		return true
	}
	m, _ := filepath.Match(pat, file)
	return m
}

func banMessage(b Ban, seen string) string {
	if b.Reason == "" && b.Use == "" {
		return fmt.Sprintf("%s is banned by policy", seen)
	}
	if b.Reason == "" {
		return fmt.Sprintf("%s is banned；替代写法：%s", seen, b.Use)
	}
	if b.Use != "" {
		return fmt.Sprintf("%s is banned: %s；替代写法：%s", seen, b.Reason, b.Use)
	}
	return fmt.Sprintf("%s is banned: %s", seen, b.Reason)
}

func at(ctx *MatchCtx, capture, msg string) *model.Finding {
	n := ctx.Captures[capture]
	return &model.Finding{
		StartLine: int(n.StartPosition().Row) + 1,
		EndLine:   int(n.EndPosition().Row) + 1,
		Message:   msg,
	}
}
