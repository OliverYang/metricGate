package deps

// Per-language import-face extractors for deps scan. The queries mirror the
// unknown-dependency rules in internal/rules (verified there against each
// grammar's node-types), but with the stdlib filter removed: a scan drafts
// *everything* external, and the checker's stdlib knowledge is not needed to
// list what the code imports — the covered/undeclared split happens against
// manifest truth instead.

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/parser"
)

// scanQuery runs one compiled query over a source file and returns the text
// of every capture named "path", with its 1-based line.
func scanQuery(lang parser.LanguageID, q string, src []byte) []impHit {
	p, err := parser.New(lang)
	if err != nil {
		return nil
	}
	tree, err := p.Parse(src)
	if err != nil || tree == nil {
		p.Close()
		return nil
	}
	// The parser must outlive the query: closing it early invalidates the
	// language pointer and the matches come back empty (probed).
	defer p.Close()
	defer tree.Close()
	root := tree.RootNode()
	if scanDebug != nil {
		scanDebug("root=%s", root.Kind())
	}

	// Note: NewQuery can return a non-nil error alongside a usable query in
	// this binding version (observed: err != nil with %v "<nil>" while the
	// query compiles and matches fine). Gate on the query itself, not the
	// error, so the scan never silently loses a language to a phantom error.
	query, err := sitter.NewQuery(p.Lang, q)
	if query == nil {
		if scanDebug != nil && err != nil {
			scanDebug("query failed: %v", err)
		}
		return nil // a scan must never fail on a query: draft, not gate
	}
	defer query.Close()
	if scanDebug != nil {
		scanDebug("captures=%v", query.CaptureNames())
	}
	// CaptureNames indexes align with QueryCapture.Index, so the path
	// capture is resolved once instead of per match.
	pathIdx := -1
	for i, name := range query.CaptureNames() {
		if name == "path" {
			pathIdx = i
			break
		}
	}
	if pathIdx < 0 {
		if scanDebug != nil {
			scanDebug("pathIdx missing in %v", query.CaptureNames())
		}
		return nil
	}
	return collectHits(query, root, src, pathIdx)
}

// collectHits runs the query cursor and folds matching captures into hits.
func collectHits(query *sitter.Query, root *sitter.Node, src []byte, pathIdx int) []impHit {
	qc := sitter.NewQueryCursor()
	defer qc.Close()
	matches := qc.Matches(query, root, src)
	var out []impHit
	for {
		m := matches.Next()
		if m == nil {
			break
		}
		for _, c := range m.Captures {
			if int(c.Index) != pathIdx {
				continue
			}
			raw := c.Node.Utf8Text(src)
			out = append(out, impHit{
				path: cleanImportText(raw),
				raw:  raw,
				line: int(c.Node.StartPosition().Row) + 1,
			})
		}
	}
	return out
}

// rawOrPath prefers the untrimmed text (C system includes are recognized by
// their angle brackets) and falls back to the trimmed path.
func (h impHit) rawOrPath() string {
	if h.raw != "" {
		return h.raw
	}
	return h.path
}

// cleanImportText trims the punctuation grammars keep around import paths:
// string quotes for module languages, angle brackets for C includes.
func cleanImportText(s string) string {
	return strings.Trim(s, "\"'`<>")
}

// langQuery builds an importQueryFunc over one language and query text.
func langQuery(lang parser.LanguageID, q string) importQueryFunc {
	return func(src []byte) []impHit { return scanQuery(lang, q, src) }
}

// importQueries maps languages with a known import surface to their
// extractor. Languages absent from the map are skipped by the scan and
// reported as such in the CLI output.
var importQueries = map[parser.LanguageID]importQueryFunc{
	parser.LangGo: langQuery(parser.LangGo, `
		(import_spec path: (interpreted_string_literal) @path)`),
	parser.LangJava: langQuery(parser.LangJava, `
		(import_declaration (scoped_identifier) @path)`),
	parser.LangTS: langQuery(parser.LangTS, `
		[
			(import_statement source: (string) @path)
			(export_statement source: (string) @path)
			(call_expression
				function: (identifier) @fn (#eq? @fn "require")
				arguments: (arguments (string) @path))
		]`),
	parser.LangTSX: langQuery(parser.LangTSX, `
		[
			(import_statement source: (string) @path)
			(export_statement source: (string) @path)
			(call_expression
				function: (identifier) @fn (#eq? @fn "require")
				arguments: (arguments (string) @path))
		]`),
	parser.LangJS: langQuery(parser.LangJS, `
		[
			(import_statement source: (string) @path)
			(export_statement source: (string) @path)
			(call_expression
				function: (identifier) @fn (#eq? @fn "require")
				arguments: (arguments (string) @path))
		]`),
	parser.LangPy: langQuery(parser.LangPy, `
		[
			(import_statement name: (dotted_name) @path)
			(import_statement name: (aliased_import name: (dotted_name) @path))
			(import_from_statement module_name: (dotted_name) @path)
		]`),
	parser.LangC: langQuery(parser.LangC, `
		[(preproc_include path: (_) @path)]`),
	parser.LangCpp: langQuery(parser.LangCpp, `
		[(preproc_include path: (_) @path)]`),
	parser.LangCS: langQuery(parser.LangCS, `
		[(using_directive (qualified_name) @path)]`),
	parser.LangRust: rustImporter(),
	parser.LangVB: langQuery(parser.LangVB, `
		[(imports_statement namespace: (namespace_name) @path)]`),
	parser.LangVerilog: langQuery(parser.LangVerilog, `
		(include_compiler_directive
			[
				(double_quoted_string) @path
				(include_compiler_directive_standard) @path
			])`),
}

// rustImporter reduces Rust use trees to their crate root: the scan drafts
// crate names (what Cargo.toml spells), not item paths.
func rustImporter() importQueryFunc {
	hits := langQuery(parser.LangRust, `
		[(use_declaration argument: (_) @path)]`)
	return func(src []byte) []impHit {
		var out []impHit
		for _, h := range hits(src) {
			if c, ok := crateRoot(h.path); ok {
				h.path = c
				out = append(out, h)
			}
		}
		return out
	}
}

// crateRoot reduces one use-tree text to its leading crate name. "crate"
// and "self" are intra-crate (internal, filtered later); super likewise.
func crateRoot(path string) (string, bool) {
	root := path
	if i := strings.Index(path, "::"); i > 0 {
		root = path[:i]
	}
	root = strings.TrimPrefix(root, "crate")
	if root == "" {
		return "", false
	}
	switch root {
	case "self", "super", "crate":
		return "", false
	}
	return root, true
}

// scanDebug is a test-only hook for diagnosing the scan pipeline.
var scanDebug func(format string, args ...any)
