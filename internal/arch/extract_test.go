package arch

import (
	"strings"
	"testing"

	"metricgate/internal/parser"
)

func parse(t *testing.T, lang parser.LanguageID, src string) *parser.Parser {
	t.Helper()
	p, err := parser.New(lang)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestExtractGo(t *testing.T) {
	src := `package model

import (
	"fmt"
	log "log"
	_ "net/http/pprof"
)

import "metricgate/internal/rules"

func F() { fmt.Println(log.Prefix(), _ ) }
`
	p := parse(t, parser.LangGo, src)
	tree, err := p.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()

	got := Extract(parser.LangGo, []byte(src), tree.RootNode())
	if got.Package != "model" {
		t.Errorf("package = %q, want model", got.Package)
	}
	want := []Import{
		{"fmt", 4},
		{"log", 5},
		{"net/http/pprof", 6},
		{"metricgate/internal/rules", 9},
	}
	if len(got.Imports) != len(want) {
		t.Fatalf("imports = %v, want %v", got.Imports, want)
	}
	for i, w := range want {
		if got.Imports[i] != w {
			t.Errorf("import %d = %v, want %v", i, got.Imports[i], w)
		}
	}
}

func TestExtractJava(t *testing.T) {
	src := `package com.demo.model;

import java.util.List;
import com.demo.repo.Repo;
import static java.lang.System.out;

class A {}
`
	p := parse(t, parser.LangJava, src)
	tree, err := p.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()

	got := Extract(parser.LangJava, []byte(src), tree.RootNode())
	if got.Package != "com/demo/model" {
		t.Errorf("package = %q, want com/demo/model (slash form)", got.Package)
	}
	want := []Import{
		{"java/util/List", 3},
		{"com/demo/repo/Repo", 4},
		{"java/lang/System/out", 5},
	}
	if len(got.Imports) != len(want) {
		t.Fatalf("imports = %v, want %v", got.Imports, want)
	}
	for i, w := range want {
		if got.Imports[i] != w {
			t.Errorf("import %d = %v, want %v", i, got.Imports[i], w)
		}
	}
}

// A file fragment (the shape an LLM returns) still yields what it declares.
func TestExtractHandlesPartialFile(t *testing.T) {
	src := "package x\n\nimport \"y\"\n"
	p := parse(t, parser.LangGo, src)
	tree, err := p.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()

	got := Extract(parser.LangGo, []byte(src), tree.RootNode())
	if len(got.Imports) != 1 || got.Imports[0].Path != "y" || got.Imports[0].Line != 3 {
		t.Errorf("imports = %v, want y at line 3", got.Imports)
	}
}

func TestExtractUnsupportedLanguage(t *testing.T) {
	if got := Extract(parser.LanguageID("ruby"), []byte("x"), nil); got.Package != "" || got.Imports != nil {
		t.Errorf("got %+v, want an empty result", got)
	}
}

// Each ported language must produce the same two things Go and Java do: the
// package it belongs to and the imports that leave it. A language that
// extracts nothing does not fail loudly — it produces a graph with no edges,
// which reads exactly like a repository with no violations.
func TestExtractEveryLanguageFindsImports(t *testing.T) {
	cases := []struct {
		name string
		lang parser.LanguageID
		src  string
		want []string
	}{
		{"python", parser.LangPy, "import os\nimport b.y as z\nfrom a.x import Q\nfrom . import rel\n", []string{"os", "b/y", "a/x"}},
		{"typescript", parser.LangTS, "import { B } from '../b/y';\nexport { C } from './c';\nconst D = require('pkg/d');\n", []string{"../b/y", "./c", "pkg/d"}},
		{"javascript", parser.LangJS, "import B from './b.js';\nconst A = require('./a');\n", []string{"./b.js", "./a"}},
		{"c", parser.LangC, "#include <stdio.h>\n#include \"local.h\"\n", []string{"stdio.h", "local.h"}},
		{"cpp", parser.LangCpp, "#include <vector>\n#include \"../b/y.h\"\n", []string{"vector", "../b/y.h"}},
		{"csharp", parser.LangCS, "using System;\nusing App.Core.Util;\n", []string{"System", "App.Core.Util"}},
		{"rust", parser.LangRust, "use crate::geo::Shape;\nuse std::collections::HashMap;\n", []string{"geo::Shape", "std::collections::HashMap"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertImports(t, c.lang, c.src, c.want)
		})
	}
}

// assertImports checks that one source extracts exactly want paths, and that
// every extracted import carries a line a violation could be anchored on.
func assertImports(t *testing.T, lang parser.LanguageID, src string, want []string) {
	t.Helper()
	got := extractImports(t, lang, src)
	if strings.Join(got.Paths, "|") != strings.Join(want, "|") {
		t.Errorf("imports = %v, want %v", got.Paths, want)
	}
	for _, imp := range got.Imports {
		if imp.Line < 1 {
			t.Errorf("import %q has no line; a violation cannot be anchored", imp.Path)
		}
	}
}

// extracted holds one extract's import list in both shapes the test needs.
type extracted struct {
	Imports []Import
	Paths   []string
}

func extractImports(t *testing.T, lang parser.LanguageID, src string) extracted {
	t.Helper()
	p := parse(t, lang, src)
	tree, err := p.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	got := Extract(lang, []byte(src), tree.RootNode())
	out := extracted{Imports: got.Imports}
	for _, imp := range got.Imports {
		out.Paths = append(out.Paths, imp.Path)
	}
	return out
}

// C# is the one ported language whose package is declared in the source rather
// than inferred from the directory: a file's namespace is what its imports
// resolve against.
func TestExtractCSharpNamespaceBecomesPackage(t *testing.T) {
	src := "namespace App.Core {\n  class Service {}\n}\n"
	p := parse(t, parser.LangCS, src)
	tree, err := p.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	if got := Extract(parser.LangCS, []byte(src), tree.RootNode()).Package; got != "App.Core" {
		t.Errorf("package = %q, want App.Core", got)
	}
}

func TestImportStylePerLanguage(t *testing.T) {
	cases := map[parser.LanguageID]string{
		parser.LangGo:   StyleGo,
		parser.LangJava: StyleLead,
		parser.LangPy:   StyleLead,
		parser.LangTS:   StyleLead,
		parser.LangJS:   StyleLead,
		parser.LangC:    StyleLead,
		parser.LangCpp:  StyleLead,
		parser.LangCS:   StyleLead,
		parser.LangRust: StyleRust,
	}
	for lang, want := range cases {
		if got := ImportStyle(lang); got != want {
			t.Errorf("ImportStyle(%s) = %q, want %q", lang, got, want)
		}
	}
}
