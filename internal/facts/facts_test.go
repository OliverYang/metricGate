package facts

import (
	"strings"
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/parser"
	"metricgate/internal/parsetest"
)

func extract(t *testing.T, lang parser.LanguageID, src string) *FileFacts {
	t.Helper()
	tree, release := parsetest.Parse(t, lang, src)
	defer release()
	return Extract("x."+string(lang), []byte(src), tree, lang)
}

func index(syms []Symbol) map[string]Symbol {
	m := make(map[string]Symbol, len(syms))
	for _, s := range syms {
		m[s.Name] = s
	}
	return m
}

func keys(syms []Symbol) []string {
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		out = append(out, s.Name)
	}
	return out
}

const goSrc = `package service

import (
	"fmt"
	log "log/slog"
)

// Store is the persistence contract.
type Store interface {
	Get(id string) (string, error)
	Put(id string, v string) error
}

type memStore struct{ items int }

func NewStore() Store { return &memStore{} }

func (s *memStore) Get(id string) (string, error) { return "", nil }
`

func TestGoFacts(t *testing.T) {
	f := extract(t, parser.LangGo, goSrc)
	if f.Package != "service" {
		t.Errorf("package = %q, want service", f.Package)
	}
	if joined := strings.Join(f.Imports, "; "); !strings.Contains(joined, "log/slog") {
		t.Errorf("imports = %v, want alias import log/slog", f.Imports)
	}

	byName := index(f.Symbols)
	want := []Symbol{
		{Kind: "interface", Name: "Store", Visibility: "exported"},
		{Kind: "function", Name: "NewStore", Visibility: "exported"},
		{Kind: "method", Name: "Get", Receiver: "memStore", Visibility: "private"},
	}
	for _, w := range want {
		got, ok := byName[w.Name]
		if !ok {
			t.Fatalf("symbol %s missing; have %v", w.Name, keys(f.Symbols))
		}
		if got.Kind != w.Kind || got.Visibility != w.Visibility || got.Receiver != w.Receiver {
			t.Errorf("%s = %s/%s/recv=%q, want %s/%s/recv=%q",
				w.Name, got.Kind, got.Visibility, got.Receiver, w.Kind, w.Visibility, w.Receiver)
		}
	}

	iface := byName["Store"]
	if len(iface.Children) != 2 {
		t.Fatalf("Store methods = %d, want 2", len(iface.Children))
	}
	if sig := iface.Children[0].Signature; !strings.Contains(sig, "Get(id string) (string, error)") {
		t.Errorf("Get signature = %q", sig)
	}
	if sig := byName["Get"].Signature; !strings.Contains(sig, "(string, error)") || strings.Contains(sig, "{") {
		t.Errorf("method signature must exclude the body: %q", sig)
	}
}

const javaSrc = `package com.acme;

import java.util.List;

public interface Repo {
    String find(String id);
}

public class UserRepo implements Repo {
    private final List<String> cache;

    public UserRepo() {
    }

    @Override
    public String find(String id) {
        return null;
    }
}
`

func TestJavaFacts(t *testing.T) {
	f := extract(t, parser.LangJava, javaSrc)
	if f.Package != "com.acme" {
		t.Errorf("package = %q, want com.acme", f.Package)
	}
	if len(f.Imports) != 1 || !strings.Contains(f.Imports[0], "java.util.List") {
		t.Errorf("imports = %v", f.Imports)
	}

	byName := index(f.Symbols)
	checkRepoInterface(t, byName, keys(f.Symbols))
	checkUserRepoClass(t, byName)
}

func checkRepoInterface(t *testing.T, byName map[string]Symbol, names []string) {
	t.Helper()
	iface, ok := byName["Repo"]
	if !ok {
		t.Fatalf("interface Repo missing; have %v", names)
	}
	if iface.Kind != "interface" || iface.Visibility != "exported" {
		t.Errorf("Repo = %s/%s, want interface/exported", iface.Kind, iface.Visibility)
	}
	if len(iface.Children) != 1 || !strings.Contains(iface.Children[0].Signature, "String find(String id)") {
		t.Errorf("Repo methods = %+v", iface.Children)
	}
}

func checkUserRepoClass(t *testing.T, byName map[string]Symbol) {
	t.Helper()
	class, ok := byName["UserRepo"]
	if !ok {
		t.Fatalf("class UserRepo missing")
	}
	kinds := map[string]int{}
	for _, c := range class.Children {
		kinds[c.Kind]++
		if c.Kind == "method" && c.Visibility != "exported" {
			t.Errorf("find visibility = %s, want exported", c.Visibility)
		}
	}
	if kinds["field"] != 1 || kinds["constructor"] != 1 || kinds["method"] != 1 {
		t.Errorf("UserRepo members = %v, want 1 field + 1 constructor + 1 method", kinds)
	}
}

func TestRenderBudget(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	files := []*FileFacts{
		extract(t, parser.LangGo, goSrc+strings.Repeat("func Spare%d() {}\n", 40)),
		extract(t, parser.LangJava, javaSrc),
	}
	for _, budget := range []int{300, 1200, 6000, 20000} {
		pack := Render(files, cfg, budget)
		if len(pack) > budget {
			t.Errorf("budget %d: pack is %d chars (over budget)", budget, len(pack))
		}
	}
	pack := Render(files, cfg, 20000)
	for _, want := range []string{
		"Store interface",             // Go interface fact
		"constraints (policy)",        // constraints section
		"limit cyclomatic-complexity", // metric limits
	} {
		if !strings.Contains(pack, want) {
			t.Errorf("pack missing %q", want)
		}
	}
}
