package rules

import (
	"fmt"
	"strings"

	"metricgate/internal/model"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// PyRules is the built-in Python rule pack. Banned APIs live in policy
// (languages.python.banned) and are compiled by the engine, not here.
func PyRules() []*Rule {
	return []*Rule{
		pyEmptyBlock(),
		todoCommentRule("[(comment) @c]"),
		PyUnknownDependency(),
		pyChainDepth(),
		pyTestNoAssert(),
	}
}

// pyEmptyBlock flags empty control-flow blocks: branches (if/elif/else via
// the alternative choice), loop bodies and except handlers. Python blocks
// that contain only pass/... are not flagged: pass and Ellipsis are the
// language's own intentional placeholder statements.
func pyEmptyBlock() *Rule {
	return newEmptyBlockRule(`
(if_statement consequence: (block) @blk)
(if_statement alternative: (_) @blk)
(elif_clause consequence: (block) @blk)
(for_statement body: (block) @blk)
(while_statement body: (block) @blk)
(try_statement body: (block) @blk)
(else_clause body: (block) @blk)
(with_statement body: (block) @blk)
(match_statement body: (block) @blk)
`)
}

// pyChainDepth flags call chains deeper than max_depth. In the Python
// grammar a chain a.b(x).c(y) nests call nodes whose function field holds
// an attribute; the depth walk crosses each attribute hop to count the
// calls, and nestedInChain keeps only the outermost link reportable.
func pyChainDepth() *Rule {
	return chainDepthRule(`
(call
  function: (_) @fn) @chain
`, func(ctx *MatchCtx) int {
		next := func(n *sitter.Node) *sitter.Node {
			fn := n.ChildByFieldName("function")
			if fn != nil && fn.Kind() == "attribute" {
				fn = fn.ChildByFieldName("object")
			}
			return fn
		}
		// The matched node is itself a call: count it, then each inner call
		// the chain descends into. a.b().c().d() = 4.
		depth := 1
		for cur := next(ctx.Captures["chain"]); cur != nil && cur.Kind() == "call"; cur = next(cur) {
			depth++
		}
		return depth
	})
}

// pyTestNoAssert flags test functions that never call an assertion — the
// Python twin of goTestNoAssert (testsmells.org, FSE'20 UT test smell).
// The convention checked is pytest/unittest style names: functions named
// test_* (or ending with _test), plus test methods inside classes.
func pyTestNoAssert() *Rule {
	return &Rule{
		ID:              "test-no-assert",
		Title:           "Test function contains no assertion (UT test smell)",
		DefaultSeverity: model.SevWarning,
		DefaultEnabled:  true,
		Query: `
(function_definition
  name: (identifier) @name
  body: (block) @body) @testfunc
`,
		AnchorCapture: "testfunc",
		OnMatch: func(ctx *MatchCtx) *model.Finding {
			name := ctx.Text(ctx.Captures["name"])
			if !strings.HasPrefix(name, "test_") && !strings.HasPrefix(name, "test") && !strings.HasSuffix(name, "_test") && !strings.HasSuffix(name, "Test") {
				return nil
			}
			blk := ctx.Captures["body"]
			if pyBlockHasAssertion(ctx, blk) {
				return nil
			}
			return &model.Finding{
				StartLine: int(blk.StartPosition().Row) + 1,
				EndLine:   int(blk.EndPosition().Row) + 1,
				Message:   fmt.Sprintf("test %q has no assertion; add assert/raises (including inside nested callbacks), otherwise the test cannot fail", name),
			}
		},
	}
}

// pyBlockHasAssertion reports whether the block calls anything that looks
// like an assertion: bare assert statements, self.assertEqual-style calls
// whose callee name contains assert, unittest raises context managers, or
// pytest.raises. AST-only and naming-based by design (deterministic, no
// type info).
func pyBlockHasAssertion(ctx *MatchCtx, blk *sitter.Node) bool {
	found := false
	walkNodes(blk, func(n *sitter.Node) {
		if found {
			return
		}
		switch n.Kind() {
		case "assert_statement":
			found = true
		case "call":
			if fn := n.ChildByFieldName("function"); fn != nil {
				name := strings.ToLower(pyCalleeName(ctx, fn))
				if strings.Contains(name, "assert") || strings.Contains(name, "raises") {
					found = true
				}
			}
		}
	})
	return found
}

// pyCalleeName extracts the plain name a call is made through: the attribute
// of an attribute chain, else the node's own text (identifier or call).
func pyCalleeName(ctx *MatchCtx, fn *sitter.Node) string {
	if fn.Kind() == "attribute" {
		if attr := fn.ChildByFieldName("attribute"); attr != nil {
			return ctx.Text(attr)
		}
	}
	return ctx.Text(fn)
}

// PyUnknownDependency flags a Python import outside the declared dependency
// set. The allow list comes from the policy (requirements/pyproject deps fed
// in by the engine when available); the standard library is always allowed.
func PyUnknownDependency() *Rule {
	return unknownDepRule(
		`[
			(import_statement
				name: (dotted_name) @path)
			(import_statement
				name: (aliased_import
					name: (dotted_name) @path))
			(import_from_statement
				module_name: (dotted_name) @path)
			(import_from_statement
				module_name: (relative_import) @path)
		]`,
		func(path string) string { return path },
		pyStdlibImport,
	)
}

// pyStdlibImport reports whether an import is a Python standard-library
// module, which needs no declaration. The set is the stable top-level module
// list of CPython 3.10-3.13, minus platform-specific modules a check host may
// legitimately lack. Relative imports (leading dots) are inside the repo and
// never external dependencies.
func pyStdlibImport(path string) bool {
	if strings.HasPrefix(path, ".") {
		return true
	}
	// The set holds top-level modules, but an import is dotted:
	// "concurrent.futures" and "os.path" are stdlib because their first segment
	// is. Looking up the whole path would flag every submodule import as
	// undeclared. Java takes the same root-first view (javaJDKImport).
	if i := strings.IndexByte(path, '.'); i >= 0 {
		path = path[:i]
	}
	return pyStdlib[path]
}

// pyStdlib is the top-level standard-library module set (CPython 3.10+,
// cross-platform entries).
var pyStdlib = map[string]bool{
	"__future__": true, "_thread": true, "abc": true, "aifc": true,
	"argparse": true, "array": true, "ast": true, "asynchat": true,
	"asyncio": true, "asyncore": true, "atexit": true, "base64": true,
	"bdb": true, "binascii": true, "bisect": true, "builtins": true,
	"bz2":      true,
	"calendar": true, "cgi": true, "cgitb": true, "chunk": true,
	"cmath": true, "cmd": true, "code": true, "codecs": true,
	"codeop": true, "collections": true, "colorsys": true,
	"compileall": true, "concurrent": true, "configparser": true,
	"contextlib": true, "contextvars": true, "copy": true,
	"copyreg": true, "cProfile": true, "crypt": true, "csv": true,
	"ctypes": true, "curses": true,
	"dataclasses": true, "datetime": true, "dbm": true,
	"decimal": true, "difflib": true, "dis": true,
	"doctest": true,
	"email":   true, "encodings": true, "ensurepip": true, "enum": true,
	"errno":        true,
	"faulthandler": true, "fcntl": true, "filecmp": true,
	"fileinput": true, "fnmatch": true, "fractions": true, "ftplib": true,
	"functools": true,
	"gc":        true, "genericpath": true, "getopt": true, "getpass": true,
	"gettext": true, "glob": true, "graphlib": true, "grp": true,
	"gzip":    true,
	"hashlib": true, "heapq": true, "hmac": true, "html": true,
	"http": true, "imaplib": true, "importlib": true, "inspect": true,
	"io": true, "ipaddress": true, "itertools": true,
	"json": true, "keyword": true,
	"linecache": true, "locale": true, "logging": true, "lzma": true,
	"mailbox": true, "mailcap": true, "marshal": true, "math": true,
	"mimetypes": true, "mmap": true, "modulefinder": true,
	"multiprocessing": true,
	"netrc":           true, "nis": true, "nntplib": true, "numbers": true,
	"operator": true, "optparse": true, "os": true, "ossaudiodev": true,
	"pathlib": true, "pdb": true, "pickle": true, "pickletools": true,
	"pipes": true, "pkgutil": true, "platform": true, "plistlib": true,
	"poplib": true, "posix": true, "pprint": true, "profile": true,
	"pstats": true, "pty": true, "pwd": true, "py_compile": true,
	"pyclbr": true, "pydoc": true,
	"queue": true, "quopri": true,
	"random": true, "re": true, "readline": true, "reprlib": true,
	"resource": true, "rlcompleter": true, "runpy": true,
	"sched": true, "secrets": true, "select": true, "selectors": true,
	"shelve": true, "shlex": true, "shutil": true, "signal": true,
	"site": true, "smtpd": true, "smtplib": true, "sndhdr": true,
	"socket": true, "socketserver": true, "sqlite3": true,
	"ssl": true, "stat": true, "statistics": true, "string": true,
	"stringprep": true, "struct": true, "subprocess": true, "sunau": true,
	"symtable": true, "sys": true, "sysconfig": true, "syslog": true,
	"tabnanny": true, "tarfile": true, "telnetlib": true, "tempfile": true,
	"termios": true, "textwrap": true, "this": true, "threading": true,
	"time": true, "timeit": true, "tkinter": true, "token": true,
	"tokenize": true, "trace": true, "traceback": true, "tracemalloc": true,
	"tty": true, "turtle": true, "types": true, "typing": true,
	"unicodedata": true, "unittest": true, "urllib": true, "uu": true,
	"uuid":     true,
	"venv":     true,
	"warnings": true, "wave": true, "weakref": true, "webbrowser": true,
	"winreg": true, "winsound": true, "wsgiref": true,
	"xdrlib": true, "xml": true, "xmlrpc": true,
	"zipapp": true, "zipfile": true, "zipimport": true, "zlib": true,
	"zoneinfo": true,
}
