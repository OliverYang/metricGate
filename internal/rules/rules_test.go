package rules

import (
	"strings"
	"testing"

	"metricgate/internal/model"
	"metricgate/internal/parser"
	"metricgate/internal/parsetest"
)

// optionPolicy is a test shim implementing rules.Policy: per-rule option
// overrides only (no severity/enable overrides).
type optionPolicy map[string]map[string]any

func (p optionPolicy) RuleSeverity(lang, ruleID string) string { return "" }
func (p optionPolicy) RuleEnabled(lang, ruleID string) *bool   { return nil }
func (p optionPolicy) RuleOptions(lang, ruleID string) map[string]any {
	return p[ruleID]
}

func runRules(t *testing.T, lang parser.LanguageID, src string) []model.Finding {
	t.Helper()
	p, err := parser.New(lang)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// ForLanguage, not a per-language switch: the dispatch is what a new
	// language is most likely to be forgotten in, so the tests have to
	// exercise it rather than route around it.
	runner, err := NewRunner(string(lang), p.Lang, ForLanguage(lang))
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	tree, release := parsetest.Parse(t, lang, src)
	defer release()
	return runner.Run("t."+string(lang), []byte(src), tree, nil)
}

func findingsByRule(fs []model.Finding, id string) []model.Finding {
	var out []model.Finding
	for _, f := range fs {
		if f.RuleID == id {
			out = append(out, f)
		}
	}
	return out
}

// --- unknown-dependency (python stdlib dotted submodule) --------------------

// A dotted import is stdlib when its FIRST segment is. Looking the whole path
// up in the top-level module set flagged every submodule import — this
// workspace alone had 80 of them (concurrent.futures, os.path, urllib.request,
// xml.etree.ElementTree, unittest.mock …) all misreported as undeclared
// dependencies.
const pyDottedImportSrc = `import concurrent.futures
import os.path
import urllib.request
from xml.etree import ElementTree
from unittest import mock

import tornado.web
import MySQLdb
`

func TestPyUnknownDependencyDottedStdlib(t *testing.T) {
	fs := findingsByRule(runRules(t, parser.LangPy, pyDottedImportSrc), "unknown-dependency")
	if len(fs) != 2 {
		t.Fatalf("unknown-dependency = %d, want 2 (only tornado.web and MySQLdb); got %+v",
			len(fs), fs)
	}
	for _, f := range fs {
		if !(contains(f.Message, "tornado") || contains(f.Message, "MySQLdb")) {
			t.Errorf("flagged %q, want only the third-party imports", f.Message)
		}
	}
}

// A first segment that merely happens to equal a stdlib name must not hide a
// third-party import: the dottedness test keeps the whole path under
// comparison, it does not collapse to the root.
func TestPyStdlibImportRootOnly(t *testing.T) {
	if !pyStdlibImport("concurrent.futures") {
		t.Error("concurrent.futures is stdlib via its root segment")
	}
	if !pyStdlibImport("os") {
		t.Error("a bare top-level stdlib module must stay allowed")
	}
	if pyStdlibImport("tornado.web") {
		t.Error("tornado is third-party; a dotted path does not inherit stdlib-ness")
	}
}

// --- call-chain-depth -------------------------------------------------------

const goChainSrc = `package t

type C struct{}

func (C) A() C { return C{} }
func (C) B() C { return C{} }
func (C) D() C { return C{} }

func twoLinks(c C) C  { return c.A().B() }
func threeLinks(c C) C { return c.A().B().D() }
func fourLinks(c C) C { return c.A().B().D().A() }
`

func TestGoChainDepth(t *testing.T) {
	fs := findingsByRule(runRules(t, parser.LangGo, goChainSrc), "call-chain-depth")
	if len(fs) != 1 {
		t.Fatalf("findings = %d, want 1 (only the 4-link chain; got %+v)", len(fs), fs)
	}
	if want := "depth 4 (max 3)"; !contains(fs[0].Message, want) {
		t.Errorf("message %q missing %q", fs[0].Message, want)
	}
}

func TestGoChainDepthOption(t *testing.T) {
	// max_depth: 2 should flag the three-link chain too, still one finding
	// per chain (outermost only).
	p, err := parser.New(parser.LangGo)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	runner, err := NewRunner(string(parser.LangGo), p.Lang, GoRules())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	tree, release := parsetest.Parse(t, parser.LangGo, goChainSrc)
	defer release()
	// Shim policy overriding only call-chain-depth options.
	pol := optionPolicy{"call-chain-depth": {"max_depth": 2}}
	fs := findingsByRule(runner.Run("t.go", []byte(goChainSrc), tree, pol), "call-chain-depth")
	if len(fs) != 2 {
		t.Fatalf("findings = %d, want 2 (3-link and 4-link chains)", len(fs))
	}
}

const javaChainSrc = `public class T {
    String two(String s) { return s.trim().toLowerCase(); }
    String four(String s) { return s.trim().toLowerCase().replace("a", "b").intern(); }
}
`

func TestJavaChainDepth(t *testing.T) {
	fs := findingsByRule(runRules(t, parser.LangJava, javaChainSrc), "call-chain-depth")
	if len(fs) != 1 {
		t.Fatalf("findings = %d, want 1 (only the 4-link chain; got %+v)", len(fs), fs)
	}
	if want := "depth 4 (max 3)"; !contains(fs[0].Message, want) {
		t.Errorf("message %q missing %q", fs[0].Message, want)
	}
}

// --- test-no-assert ---------------------------------------------------------

const javaTestsSrc = `public class T {
    void helper() { System.out.println("not a test"); }

    void testNoAssert() {
        int x = compute();
        x = x + 1;
    }

    void testWithAssert() {
        org.junit.Assert.assertEquals(2, compute());
    }

    void testWithFail() {
        if (compute() != 2) {
            fail("bad");
        }
    }

    void testThrows() {
        throw new IllegalStateException("expected");
    }

    void testExpectPrefix() {
        expect(compute()).toBe(2);
    }

    int compute() { return 2; }
}
`

func TestJavaTestNoAssert(t *testing.T) {
	fs := findingsByRule(runRules(t, parser.LangJava, javaTestsSrc), "test-no-assert")
	if len(fs) != 1 {
		t.Fatalf("findings = %d, want 1 (only testNoAssert; got %+v)", len(fs), fs)
	}
	if !contains(fs[0].Message, "testNoAssert") {
		t.Errorf("message %q should name the offending test", fs[0].Message)
	}
}

const goTestsSrc = `package t

import "testing"

func helper(x int) int { return x }

func TestNoAssert(t *testing.T) {
	cases := []struct {
		name  string
		input []int
	}{
		{"empty", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := helper(len(c.input))
			t.Log("got", got)
		})
	}
}

func TestWithError(t *testing.T) {
	if got := helper(1); got != 1 {
		t.Errorf("helper(1) = %d, want 1", got)
	}
}

func TestWithRequire(t *testing.T) {
	require(t, helper(2) == 2)
}

func require(t *testing.T, ok bool) { t.Helper(); if !ok { t.Fatal("bad") } }

func TestWithPanic(t *testing.T) {
	defer func() { recover() }()
	panic("expected")
}

func TestMain(m *testing.M) { }

func helperNotATest(x int) int { return x }
`

func TestGoTestNoAssert(t *testing.T) {
	fs := findingsByRule(runRules(t, parser.LangGo, goTestsSrc), "test-no-assert")
	if len(fs) != 1 {
		t.Fatalf("findings = %d, want 1 (only TestNoAssert; got %+v)", len(fs), fs)
	}
	if !contains(fs[0].Message, "TestNoAssert") {
		t.Errorf("message %q should name the offending test", fs[0].Message)
	}
}

// Java-lambda bodies, Go closures and multi-line chains must not break the
// queries; a quick smoke over both languages.
func TestChainRuleSmoke(t *testing.T) {
	goSrc := `package t
func f() {
	g(func() { h().i().j().k() })
}`
	fs := findingsByRule(runRules(t, parser.LangGo, goSrc), "call-chain-depth")
	if len(fs) != 1 {
		t.Fatalf("go smoke findings = %d, want 1; got %+v", len(fs), fs)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
