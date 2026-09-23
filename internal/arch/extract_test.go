package arch

import (
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
