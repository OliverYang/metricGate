// The unknown-dependency rule: an import that is neither the standard library,
// nor part of this repository, nor a dependency the repository already
// declared. It is the deterministic guard against a model inventing a
// plausible package name — the failure mode that costs the most to discover
// late, because it only surfaces at build or resolve time.
package rules

import (
	"strings"

	"metricgate/internal/model"
)

// RuleUnknownDependency is the stable finding id of the import allowlist check.
const RuleUnknownDependency = "unknown-dependency"

// GoUnknownDependency flags a Go import outside the standard library and the
// declared module set. The allow list is supplied by the engine as the rule's
// "allow" option (module paths from go.mod plus the policy's own entries).
func GoUnknownDependency() *Rule {
	return unknownDepRule(
		`(import_spec path: (interpreted_string_literal) @path)`,
		func(path string) string { return strings.Trim(path, "\"`") },
		goStdlibImport,
	)
}

// JavaUnknownDependency flags a Java import outside the JDK and the declared
// dependency set. Java has no lockfile convention, so the allow list is
// whatever the policy declares.
func JavaUnknownDependency() *Rule {
	return unknownDepRule(
		`(import_declaration (scoped_identifier) @path)`,
		func(path string) string { return path },
		javaJDKImport,
	)
}

// unknownDepRule builds the rule over a language's import query. extract pulls
// the import path out of the matched node; isStdlib reports whether the path is
// part of the language's own platform, which needs no declaration.
func unknownDepRule(query string, extract func(string) string, isStdlib func(string) bool) *Rule {
	return &Rule{
		ID:              RuleUnknownDependency,
		Title:           "import is not a declared dependency (possible hallucinated package)",
		DefaultSeverity: model.SevWarning,
		DefaultEnabled:  true,
		Query:           query,
		AnchorCapture:   "path",
		OnMatch: func(ctx *MatchCtx) *model.Finding {
			path := extract(ctx.Text(ctx.Captures["path"]))
			if path == "" || isStdlib(path) || allowedImport(path, ctx.OptStrings("allow")) {
				return nil
			}
			return at(ctx, "path", unknownDepMessage(path))
		},
	}
}

// allowedImport reports whether a path is covered by the allow list. Entries
// match on a separator boundary, so a listed module never covers a longer,
// different one.
func allowedImport(path string, allow []string) bool {
	for _, entry := range allow {
		if entry == "" {
			continue
		}
		if path == entry || strings.HasPrefix(path, entry+"/") || strings.HasPrefix(path, entry+".") {
			return true
		}
	}
	return false
}

// goStdlibImport reports whether a Go import is standard library. Standard
// library paths are never domain-qualified: their first segment has no dot,
// while every third-party module is rooted at a domain. The same test also
// accepts this repository's own dotless module path.
func goStdlibImport(path string) bool {
	first := path
	if i := strings.IndexByte(path, '/'); i >= 0 {
		first = path[:i]
	}
	return !strings.Contains(first, ".")
}

// javaJDKImport reports whether a Java import is part of the JDK.
func javaJDKImport(path string) bool {
	for _, prefix := range []string{"java.", "javax.", "jdk.", "sun.", "com.sun."} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// unknownDepMessage explains the finding in the terms the fix needs.
func unknownDepMessage(path string) string {
	return "import " + path + " is not a declared dependency of this repository; " +
		"if it is not a typo, declare it in the build manifest or add it to the policy's dependencies.allow first"
}
