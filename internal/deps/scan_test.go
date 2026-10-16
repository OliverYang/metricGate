package deps

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTree lays out a tiny multi-language repository for scan tests.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func draftPaths(d *Draft) map[string]Import {
	out := map[string]Import{}
	for _, imp := range d.Imports {
		out[imp.Path] = imp
	}
	return out
}

func TestScanRootDraftsUncoveredExternalImports(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod":       "module demo\n\ngo 1.26\nrequire known.example/lib v1.0.0\n",
		"main.go":      "package main\n\nimport (\n\t\"fmt\"\n\n\t\"known.example/lib\"\n\t\"unknown.example/ghost\"\n)\n\nfunc main() { fmt.Println(lib.X) }\n",
		"app.ts":       "import { x } from \"known-pkg\";\nimport { y } from \"mystery-pkg\";\nimport fs from \"node:fs\";\n",
		"package.json": "{\n  \"dependencies\": { \"known-pkg\": \"^1.0.0\" }\n}\n",
		"py/mod.py":    "import os\nimport numpy as np\nfrom unknown_py.dep import thing\n",
		"c/main.c":     "#include <stdio.h>\n#include <openssl/ssl.h>\n#include \"local.h\"\n",
		// tooling state must not leak into the import face
		".data/generated.go": "package gen\n\nimport \"hallu.example/fromdata\"\n",
	})

	d, err := ScanRoot(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Module != "demo" {
		t.Fatalf("module = %q, want demo", d.Module)
	}
	got := draftPaths(d)

	want := map[string]string{
		"unknown.example/ghost": "go",
		"mystery-pkg":           "typescript",
		"numpy":                 "python",
		"unknown_py.dep":        "python",
		"openssl/ssl.h":         "c",
	}
	for path, lang := range want {
		imp, ok := got[path]
		if !ok {
			t.Errorf("draft missing %q (have %d imports)", path, len(d.Imports))
			continue
		}
		if imp.Lang != lang {
			t.Errorf("%q lang = %q, want %q", path, imp.Lang, lang)
		}
		if imp.Line <= 0 {
			t.Errorf("%q line = %d, want >= 1", path, imp.Line)
		}
	}
	if _, ok := got["local"]; ok {
		t.Error(`quoted repo-local include "local.h" must not be drafted as a dependency`)
	}
	if _, ok := got["stdio.h"]; ok {
		t.Error("stdio.h is a system header, not a dependency")
	}
	// Imports lists every external face; covered ones must stay out of the
	// uncovered remainder the draft suggests.
	uncovered := map[string]bool{}
	for _, imp := range Uncovered(d) {
		uncovered[imp.Path] = true
	}
	for _, covered := range []string{"fmt", "known.example/lib", "known-pkg", "os", "node:fs", "hallu.example/fromdata"} {
		if uncovered[covered] {
			t.Errorf("%q should be covered (manifest/stdlib/internal), not uncovered", covered)
		}
	}
	if !uncovered["unknown.example/ghost"] || !uncovered["mystery-pkg"] {
		t.Errorf("uncovered must name the manifest gaps, got %v", uncovered)
	}
}

func TestScanRootCoverageUsesPolicyAllow(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": "package main\n\nimport \"partner.example/api\"\n\nfunc main() {}\n",
	})
	d, err := ScanRoot(dir, []string{"partner.example"})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(d.Imports); got != 1 {
		t.Fatalf("imports = %d, want 1 (still listed as covered)", got)
	}
	if !AnyCovers("partner.example/api", d.Covered) {
		t.Fatal("policy allow entry must count as covered")
	}
}

func TestScanRootSkipsDotDirectories(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"real.go":            "package real\n\nimport \"real.example/one\"\n",
		".metricgen/x.go":    "package gen\n\nimport \"fake.example/two\"\n",
		".data/deep/three.c": "#include <hallu.h>\n",
	})
	d, err := ScanRoot(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range d.Imports {
		if imp.Path == "fake.example/two" || imp.Path == "hallu" {
			t.Errorf("dot-directory import %q leaked into the draft", imp.Path)
		}
	}
	if len(d.Imports) != 1 || d.Imports[0].Path != "real.example/one" {
		t.Fatalf("imports = %+v, want only real.example/one", d.Imports)
	}
}

func TestScanRootRelativeAndOwnModuleAreInternal(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module own/module\n",
		"a/a.go": "package a\n\nimport (\n\t\"own/module/b\"\n\t\"./local\"\n)\n",
	})
	d, err := ScanRoot(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Imports) != 0 {
		t.Fatalf("internal imports must not appear in the draft, got %+v", d.Imports)
	}
}

// TestScanRootCountsEveryParsedLanguage pins the fix for the silent skip:
// before Files existed, a language with no import extractor was dropped
// without a trace, so an empty draft read as "no dependencies".
func TestScanRootCountsEveryParsedLanguage(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module demo\n\ngo 1.26\n",
		"a.vb":   "Imports MyApp.Core\n",
		"b.v":    "`include \"defs.vh\"\n",
		"c.sql":  "CREATE FUNCTION f() RETURNS int AS $$ SELECT 1; $$ LANGUAGE sql;\n",
	})
	d, err := ScanRoot(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"vbnet", "verilog", "sql"} {
		if d.Files[lang] != 1 {
			t.Errorf("Files[%s] = %d, want 1 (parsed but not counted)", lang, d.Files[lang])
		}
	}
	got := d.LanguagesWithoutImports()
	if len(got) != 1 || got[0] != "sql" {
		t.Errorf("LanguagesWithoutImports() = %v, want [sql]", got)
	}
}

// TestScanRootReadsVBAndVerilogImports covers the two new extractors. VB's
// Imports clause is a real namespace import; SystemVerilog's `include is a
// file reference, kept because a missing header is a missing dependency.
func TestScanRootReadsVBAndVerilogImports(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module demo\n\ngo 1.26\n",
		"a.vb":   "Imports MyApp.Core\nImports System\n",
		"b.v":    "`include \"defs.vh\"\n",
	})
	d, err := ScanRoot(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := draftPaths(d)
	if _, ok := got["MyApp.Core"]; !ok {
		t.Errorf("VB namespace import missing from draft: %v", got)
	}
	if _, ok := got["defs.vh"]; !ok {
		t.Errorf("SystemVerilog include missing from draft: %v", got)
	}
	if _, ok := got["System"]; ok {
		t.Errorf("System is a BCL namespace and must not be drafted: %v", got)
	}
}
