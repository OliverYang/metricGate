package rules

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// CRules is the built-in C rule pack. Banned APIs live in policy
// (languages.c.banned) and are compiled by the engine, not here.
func CRules() []*Rule {
	return []*Rule{
		cEmptyBlock(),
		todoCommentRule("[(comment) @c]"),
		CUnknownDependency(),
		cChainDepth(),
	}
}

// CppRules is the built-in C++ rule pack. It shares the C control-flow
// shapes (the C grammar is embedded in the C++ one) and adds try/catch.
func CppRules() []*Rule {
	return []*Rule{
		cppEmptyBlock(),
		todoCommentRule("[(comment) @c]"),
		CppUnknownDependency(),
		cChainDepth(),
	}
}

// cEmptyBlock flags empty control-flow blocks: branches (if consequence /
// alternative — alternative is a choice type, so the wildcard form is
// required), loop bodies and the else of an if. A block holding only a
// comment still counts as empty: comments are trivia.
func cEmptyBlock() *Rule {
	return newEmptyBlockRule(`
(if_statement consequence: (compound_statement) @blk)
(if_statement alternative: (_) @blk)
(for_statement body: (compound_statement) @blk)
(while_statement body: (compound_statement) @blk)
(do_statement body: (compound_statement) @blk)
(else_clause (compound_statement) @blk)
`)
}

// cppEmptyBlock adds the C++ try body and catch body to the C set.
func cppEmptyBlock() *Rule {
	return newEmptyBlockRule(`
(if_statement consequence: (compound_statement) @blk)
(if_statement alternative: (_) @blk)
(for_statement body: (compound_statement) @blk)
(while_statement body: (compound_statement) @blk)
(do_statement body: (compound_statement) @blk)
(else_clause (compound_statement) @blk)
(try_statement body: (compound_statement) @blk)
(catch_clause body: (compound_statement) @blk)
`)
}

// cChainDepth flags call chains deeper than max_depth. In the C/C++ grammar
// a chain a().b().c() nests call nodes whose function field holds a
// field_expression; the depth walk crosses each field hop to count the
// calls, and nestedInChain keeps only the outermost link reportable.
func cChainDepth() *Rule {
	return chainDepthRule(`
(call_expression
  function: (_) @fn) @chain
`, func(ctx *MatchCtx) int {
		next := func(n *sitter.Node) *sitter.Node {
			fn := n.ChildByFieldName("function")
			if fn != nil && fn.Kind() == "field_expression" {
				fn = fn.ChildByFieldName("argument")
			}
			return fn
		}
		// The matched node is itself a call: count it, then each inner call
		// the chain descends into. a().b().c().d() = 4.
		depth := 1
		for cur := next(ctx.Captures["chain"]); cur != nil && cur.Kind() == "call_expression"; cur = next(cur) {
			depth++
		}
		return depth
	})
}

// CUnknownDependency flags a C #include outside the standard library and the
// declared dependency set. C has no package manifest convention, so the
// allow list is whatever the policy declares; angle-bracket system headers
// are always allowed.
func CUnknownDependency() *Rule {
	return unknownDepRule(
		`[(preproc_include path: (_) @path)]`,
		cIncludePath,
		cSystemInclude,
	)
}

// CppUnknownDependency flags a C++ #include outside the standard library and
// the declared dependency set. The C++ standard library header set extends
// the C one.
func CppUnknownDependency() *Rule {
	return unknownDepRule(
		`[(preproc_include path: (_) @path)]`,
		cIncludePath,
		cppSystemInclude,
	)
}

// cIncludePath extracts the include path, trimming the angle brackets or
// quotes of the string_literal / system_lib_string node. A quoted include is
// a repository-local header, so it is reported as "" — the caller treats that
// as "not an external dependency" — instead of being trimmed into looking
// like a bare package name (`local.h`) that no manifest could ever declare.
func cIncludePath(path string) string {
	if strings.HasPrefix(path, "\"") {
		return ""
	}
	return strings.Trim(path, "<>")
}

// cSystemInclude reports whether an include is a C standard-library header
// (ISO C99/C11 libc), which needs no declaration. Quoted includes ("my.h")
// are repository-local and never external dependencies.
func cSystemInclude(path string) bool {
	return cStdHeaders[path]
}

// cppSystemInclude reports whether an include is a C or C++ standard-library
// header. Quoted includes are repository-local; the foo.h C-compatible
// headers (<cassert> style) and the C set are both system.
func cppSystemInclude(path string) bool {
	if cStdHeaders[path] {
		return true
	}
	if base, ok := strings.CutPrefix(path, "c"); ok && cppStdHeaders[base+".h"] {
		return true
	}
	return cppStdHeaders[path]
}

// cStdHeaders is the ISO C library header set (C99/C11/C17).
var cStdHeaders = map[string]bool{
	"assert.h": true, "complex.h": true, "ctype.h": true, "errno.h": true,
	"fenv.h": true, "float.h": true, "inttypes.h": true, "iso646.h": true,
	"limits.h": true, "locale.h": true, "math.h": true, "setjmp.h": true,
	"signal.h": true, "stdalign.h": true, "stdarg.h": true,
	"stdatomic.h": true, "stdbool.h": true, "stddef.h": true,
	"stdint.h": true, "stdio.h": true, "stdlib.h": true,
	"stdnoreturn.h": true, "string.h": true, "tgmath.h": true,
	"threads.h": true, "time.h": true, "uchar.h": true, "wchar.h": true,
	"wctype.h": true,
}

// cppStdHeaders is the C++ standard-library header set (C++17/20), the
// .h-free form plus the compatibility headers that keep the .h suffix.
var cppStdHeaders = map[string]bool{
	"algorithm": true, "any": true, "array": true, "atomic": true,
	"barrier": true, "bit": true, "bitset": true, "charconv": true,
	"chrono": true, "codecvt": true, "compare": true, "complex": true,
	"concepts": true, "condition_variable": true, "coroutine": true,
	"deque": true, "exception": true, "execution": true,
	"expected": true, "filesystem": true, "flat_map": true,
	"flat_set": true, "format": true, "forward_list": true,
	"fstream": true, "functional": true, "future": true,
	"generator": true, "hazard_pointer": true, "initializer_list": true,
	"iomanip": true, "ios": true, "iosfwd": true, "iostream": true,
	"istream": true, "iterator": true, "latch": true, "limits": true,
	"list": true, "locale": true, "map": true, "mdspan": true,
	"memory": true, "memory_resource": true, "mutex": true, "new": true,
	"numbers": true, "numeric": true, "optional": true, "ostream": true,
	"print": true, "queue": true, "random": true, "ranges": true,
	"ratio": true, "regex": true, "scoped_allocator": true,
	"semaphore": true, "set": true, "shared_mutex": true,
	"source_location": true, "span": true, "spanstream": true,
	"sstream": true, "stack": true, "stacktrace": true,
	"stdfloat": true, "stop_token": true, "streambuf": true,
	"string": true, "string_view": true, "strstream": true,
	"syncstream": true, "system_error": true, "thread": true,
	"tuple": true, "typeindex": true, "typeinfo": true,
	"type_traits": true, "unordered_map": true, "unordered_set": true,
	"utility": true, "valarray": true, "variant": true, "vector": true,
	"version": true,
	// C compatibility headers keep the .h suffix in C++.
	"assert.h": true, "ctype.h": true, "errno.h": true, "fenv.h": true,
	"float.h": true, "inttypes.h": true, "limits.h": true,
	"locale.h": true, "math.h": true, "setjmp.h": true, "signal.h": true,
	"stdarg.h": true, "stddef.h": true, "stdint.h": true,
	"stdio.h": true, "stdlib.h": true, "string.h": true, "time.h": true,
	"uchar.h": true, "wchar.h": true, "wctype.h": true,
}

// cTestNoAssert is intentionally absent for C/C++: there is no dominant test
// naming convention (Check, CppUTest, gtest all differ), so a name-based
// rule would mostly misfire.
