// Package arch builds the package dependency graph of a run and checks it
// against the architecture section of the policy: layer boundaries, forbidden
// edges and import cycles.
//
// It is the deterministic answer to "is this dependency direction legal?" —
// the constraint an LLM is most likely to get wrong when it invents a call from
// the storage layer into the HTTP layer, and the one no per-file linter can see
// at all.
package arch

import "strings"

// Package names are compared as slash-separated segments, so Go directories
// ("internal/rules") and Java packages ("com/foo/bar") share one matcher.

// segments splits a package name into its path segments.
func segments(name string) []string {
	name = strings.Trim(name, "/")
	if name == "" {
		return nil
	}
	return strings.Split(name, "/")
}

// equalSegs compares two segment slices element-wise.
func equalSegs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// matchesAnySeg reports whether a pattern segment matches a name segment.
func matchesAnySeg(pattern, name string) bool {
	return pattern == "*" || pattern == name
}

// matches reports whether a package name satisfies a policy pattern:
//
//	internal/rules    that package (also "mod/internal/rules": a name may
//	                  carry a module prefix the pattern leaves out)
//	internal/*        direct children of internal — same depth required
//	internal/**       internal and anything below it, at any remaining depth
//	*                 one package with a single name segment
//
// A literal pattern is matched as a trailing sequence of the name's segments,
// which keeps one policy file working whether it is read from the repository
// root or a subdirectory. Wildcards are stricter: "*" needs the same depth,
// and only "**" may span more segments.
func matches(pattern, name string) bool {
	ps, ns := segments(pattern), segments(name)
	if len(ps) == 0 || len(ns) == 0 {
		return false
	}
	if ps[len(ps)-1] == "**" {
		return matchesSubtree(ps[:len(ps)-1], ns)
	}
	if !hasWildcard(ps) {
		return len(ns) >= len(ps) && equalSegs(ns[len(ns)-len(ps):], ps)
	}
	return len(ns) == len(ps) && globSegs(ps, ns)
}

// matchesSubtree is the "**" rule: the pattern's leading segments must appear
// in the name, at any alignment, and anything may follow them.
func matchesSubtree(base, ns []string) bool {
	if len(base) == 0 {
		return true // "**" alone matches every package
	}
	for i := 0; i+len(base) <= len(ns); i++ {
		if globSegs(base, ns[i:i+len(base)]) {
			return true
		}
	}
	return false
}

func hasWildcard(ps []string) bool {
	for _, p := range ps {
		if p == "*" {
			return true
		}
	}
	return false
}

func globSegs(pattern, name []string) bool {
	for i, p := range pattern {
		if !matchesAnySeg(p, name[i]) {
			return false
		}
	}
	return true
}

// MatchesAny reports whether any pattern matches the package name.
func MatchesAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if matches(p, name) {
			return true
		}
	}
	return false
}

// resolves reports whether an import path points at a known package.
//
// Go import paths *are* package paths, and their final segment is the package
// name, so the package is matched as a trailing sequence. A Java import names a
// type inside a package ("com/foo/Bar") or a static member of one, so the
// package is matched as a leading sequence instead.
func resolves(imp, pkg string, java bool) bool {
	is, ps := segments(imp), segments(pkg)
	if len(ps) == 0 || len(is) < len(ps) {
		return false
	}
	if java {
		return equalSegs(is[:len(ps)], ps)
	}
	return equalSegs(is[len(is)-len(ps):], ps)
}
