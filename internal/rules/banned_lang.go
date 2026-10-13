package rules

import (
	"strings"

	"metricgate/internal/parser"
)

// The call/import/constructor bans below are the same three questions asked in
// seven more grammars. The matching rules do not change with the language —
// only the node kinds do — so the shared builders in banned.go do the work and
// these functions supply the query text.
//
// Why this exists at all: a policy's banned-api list is the data form of the
// organisation's constraints, and a language missing from the registry meant
// the YAML was silently inert for it. The finding simply never appeared, which
// reads exactly like compliant code.

// tsCallBare and friends are the per-language query shapes. They are package
// level so a bad pattern fails once, at startup, in NewRunner.
const (
	tsCallBare = `(call_expression
    function: (identifier) @fn
    arguments: (arguments) @args) @call`
	tsCallRecv = `(call_expression
  function: (member_expression
    object: (_) @recv
    property: (property_identifier) @fn)
  arguments: (arguments) @args) @call`
	tsNew = `(new_expression
    constructor: (_) @fn
    arguments: (arguments) @args) @call`
	tsImport = `[(import_statement source: (string) @path)
 (export_statement source: (string) @path)]`

	pyCallBare = `(call
    function: (identifier) @fn
    arguments: (argument_list) @args) @call`
	pyCallRecv = `(call
  function: (attribute
    object: (_) @recv
    attribute: (identifier) @fn)
  arguments: (argument_list) @args) @call`
	pyImport = `[(import_statement name: (dotted_name) @path)
 (import_statement name: (aliased_import name: (dotted_name) @path))
 (import_from_statement module_name: (dotted_name) @path)]`

	cCallBare = `(call_expression
    function: (identifier) @fn
    arguments: (argument_list) @args) @call`
	cCallRecv = `(call_expression
  function: (field_expression
    argument: (_) @recv
    field: (field_identifier) @fn)
  arguments: (argument_list) @args) @call`
	cImport = `(preproc_include path: (_) @path)`

	cppCallQualified = `(call_expression
    function: (qualified_identifier) @fn
    arguments: (argument_list) @args) @call`
	cppNew = `(new_expression
    type: (_) @fn
    arguments: (argument_list) @args) @call`

	csCallBare = `(invocation_expression
    function: (identifier) @fn
    arguments: (argument_list) @args) @call`
	csCallRecv = `(invocation_expression
  function: (member_access_expression
    expression: (_) @recv
    name: (identifier) @fn)
  arguments: (argument_list) @args) @call`
	csNew = `(object_creation_expression
    type: (_) @fn
    arguments: (argument_list) @args) @call`
	csImport = `(using_directive (qualified_name) @path)`

	rustCallBare = `(call_expression
    function: (identifier) @fn
    arguments: (arguments) @args) @call`
	rustCallRecv = `(call_expression
  function: (field_expression
    value: (_) @recv
    field: (field_identifier) @fn)
  arguments: (arguments) @args) @call`
	rustCallQualified = `(call_expression
    function: (scoped_identifier) @fn
    arguments: (arguments) @args) @call`
	rustImport = `(use_declaration argument: (_) @path)`
)

// langCallBan builds a call ban from a bare-name query and a receiver query.
// "pkg.Name" in the policy means receiver "pkg" + method "Name"; a bare "Name"
// matches an unqualified call. The receiver is compared by source spelling,
// exactly as Java's is, so "console.log" and a longer spelling of the same
// receiver both match.
func langCallBan(b Ban, bare, recv string) *Rule {
	owner, method := splitLast(b.Match)
	if owner == "" {
		return callRule(b, bare, func(ctx *MatchCtx) (string, bool) {
			return matchedName(ctx, method, b)
		})
	}
	return callRule(b, recv, func(ctx *MatchCtx) (string, bool) {
		got := flatText(ctx, "recv")
		if !receiverMatch(got, owner) {
			return "", false
		}
		fn, ok := matchedName(ctx, method, b)
		if !ok {
			return "", false
		}
		return got + "." + fn, ok
	})
}

// langCallBanQualified is langCallBan for languages that can also spell a call
// with a module-qualified name rather than a receiver expression (C++'s
// std::printf, Rust's fs::read_to_string). Those spellings carry no receiver
// node, so they get their own query and are compared whole against the
// policy's dotted spelling with "::" folded to ".".
func langCallBanQualified(b Ban, bare, recv, qualified string) *Rule {
	rule := langCallBan(b, bare, recv)
	qualifiedRule := callRule(b, qualified, func(ctx *MatchCtx) (string, bool) {
		fn := ctx.Text(ctx.Captures["fn"])
		want := strings.ReplaceAll(b.Match, "::", ".")
		if !nameMatch(strings.ReplaceAll(fn, "::", "."), want, b.Prefix) {
			return "", false
		}
		return fn, true
	})
	// One ban may need either shape; the runner compiles both and reports
	// whichever matches.
	rule.Alternate = qualifiedRule
	return rule
}

// langImportBan builds an import ban from an import query. clean turns the
// captured node text into the path the policy spells (stripping quotes, angle
// brackets and Rust's "crate::" root).
func langImportBan(b Ban, query string, clean func(string) string) *Rule {
	return importRule(b, query, func(ctx *MatchCtx) string {
		return clean(ctx.Text(ctx.Captures["path"]))
	})
}

// trimImportDelims removes the delimiters a language wraps an import path in:
// quotes (TS/JS/Python/C string literals) and angle brackets (C/C++ system
// includes). Python's dotted names and C#'s qualified names carry neither.
func trimImportDelims(s string) string {
	return strings.Trim(strings.TrimSpace(s), "\"'<>")
}

// --- per-language builders ------------------------------------------------

func tsCallBan(b Ban) *Rule {
	return langCallBan(b, tsCallBare, tsCallRecv)
}

func tsImportBan(b Ban) *Rule {
	return langImportBan(b, tsImport, trimImportDelims)
}

func tsConstructorBan(b Ban) *Rule {
	return constructorRule(b, tsNew)
}

func pyCallBan(b Ban) *Rule {
	return langCallBan(b, pyCallBare, pyCallRecv)
}

func pyImportBan(b Ban) *Rule {
	return langImportBan(b, pyImport, trimImportDelims)
}

func cCallBan(b Ban) *Rule {
	return langCallBan(b, cCallBare, cCallRecv)
}

func cImportBan(b Ban) *Rule {
	return langImportBan(b, cImport, trimImportDelims)
}

func cppCallBan(b Ban) *Rule {
	return langCallBanQualified(b, cCallBare, cCallRecv, cppCallQualified)
}

func cppImportBan(b Ban) *Rule {
	return langImportBan(b, cImport, trimImportDelims)
}

func cppConstructorBan(b Ban) *Rule {
	return constructorRule(b, cppNew)
}

func csCallBan(b Ban) *Rule {
	return langCallBan(b, csCallBare, csCallRecv)
}

func csImportBan(b Ban) *Rule {
	return langImportBan(b, csImport, trimImportDelims)
}

func csConstructorBan(b Ban) *Rule {
	return constructorRule(b, csNew)
}

func rustCallBan(b Ban) *Rule {
	return langCallBanQualified(b, rustCallBare, rustCallRecv, rustCallQualified)
}

func rustImportBan(b Ban) *Rule {
	return langImportBan(b, rustImport, rustImportPath)
}

// rustImportPath reduces a Rust use path to the crate root the policy can
// name: "crate::geo::Shape" is a dependency on the file's own crate, not on a
// package called "crate", and "super::util" is not a dependency at all.
func rustImportPath(raw string) string {
	path := strings.TrimSuffix(strings.TrimSpace(raw), ";")
	parts := strings.Split(path, "::")
	if len(parts) == 0 {
		return ""
	}
	switch root := strings.TrimSpace(parts[0]); root {
	case "crate", "self", "super":
		return ""
	default:
		return root
	}
}

// constructorRule builds a constructor ban: the captured type's last dotted
// segment is compared, so "new SimpleDateFormat()" and its fully-qualified
// spelling both match.
func constructorRule(b Ban, query string) *Rule {
	return callRule(b, query, func(ctx *MatchCtx) (string, bool) {
		typ := strings.ReplaceAll(flatText(ctx, "fn"), "::", ".")
		if i := strings.LastIndex(typ, "."); i >= 0 {
			typ = typ[i+1:]
		}
		if !nameMatch(typ, b.Match, b.Prefix) {
			return "", false
		}
		return typ, true
	})
}

// langBanBuilders is the registry of languages that can express each ban kind.
// A language absent here has no builder for that kind and contributes nothing,
// which is deliberate: a call ban in a language whose calls are not calls is a
// rule that can only ever misfire.
func langBanBuilders() map[string]map[BanKind]func(Ban) *Rule {
	return map[string]map[BanKind]func(Ban) *Rule{
		string(parser.LangGo): {
			BanCall: goCallBan, BanImport: goImportBan,
		},
		string(parser.LangJava): {
			BanCall: javaCallBan, BanImport: javaImportBan, BanConstructor: javaConstructorBan,
		},
		string(parser.LangTS): {
			BanCall: tsCallBan, BanImport: tsImportBan, BanConstructor: tsConstructorBan,
		},
		string(parser.LangJS): {
			BanCall: tsCallBan, BanImport: tsImportBan, BanConstructor: tsConstructorBan,
		},
		string(parser.LangTSX): {
			BanCall: tsCallBan, BanImport: tsImportBan, BanConstructor: tsConstructorBan,
		},
		string(parser.LangPy): {
			BanCall: pyCallBan, BanImport: pyImportBan,
		},
		string(parser.LangC): {
			BanCall: cCallBan, BanImport: cImportBan,
		},
		string(parser.LangCpp): {
			BanCall: cppCallBan, BanImport: cppImportBan, BanConstructor: cppConstructorBan,
		},
		string(parser.LangCS): {
			BanCall: csCallBan, BanImport: csImportBan, BanConstructor: csConstructorBan,
		},
		string(parser.LangRust): {
			BanCall: rustCallBan, BanImport: rustImportBan,
		},
	}
}
