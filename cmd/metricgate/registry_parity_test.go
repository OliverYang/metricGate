package main

import (
	"os"
	"path/filepath"
	"testing"

	"metricgate/internal/arch"
	"metricgate/internal/clones"
	"metricgate/internal/deps"
	"metricgate/internal/facts"
	"metricgate/internal/metrics"
	"metricgate/internal/parser"
	"metricgate/internal/parsetest"
	"metricgate/internal/refs"
	"metricgate/internal/rulegen"
	"metricgate/internal/rules"
	"metricgate/internal/stubs"
)

// Per-language registry parity, across every package.
//
// metricGate keeps its language knowledge in about fifteen separate tables —
// metrics profiles, facts declSpecs, clone declaration kinds, import queries,
// ref walkers, stub detectors, arch extractors, rule packs, ban vocabularies,
// the rulegen allowlist. iter-9 diagnosed that layout; iter-14 extended every
// table from ten languages to thirteen and found the sharper problem: only the
// metrics package and the parser iterated parser.Supported(). Every other
// table was guarded by hand-written per-language cases, so a fourteenth
// language added to Supported() could be missing from ten registries and no
// test would turn red. Absence and "this language genuinely has no such face"
// produced identical output.
//
// The fix is not to merge the tables (out of scope, and each package's shape
// is genuinely different). It is this file: one row per language, one column
// per registry, every cell a number measured from the current tree. Adding a
// language without adding its row fails with "no expectation". Adding it with
// an unfilled registry fails on the cell whose expected value is non-zero.
// Marking a cell zero is the explicit, reviewable way to say "no such face" —
// and docs/metrics.md has to carry the reason, because that is where the next
// reader looks.
//
// Cells were measured, not guessed: each fixture below is parsed once by the
// probe run recorded in docs/iterations/iter-14-three-languages.md.

type registryExpectation struct {
	// fixture is one file per language with a named function, a parameter, a
	// branch, a call, an import and a comment — so every registry can be
	// asked a question of the same source.
	fixture string
	// stubFixture is a declaration with an empty body: the stub detector's
	// only reliable input.
	stubFixture string
	// refFixture overrides fixture for the reference index, where a language
	// needs a call whose target is a bare identifier rather than a qualified
	// one (C++'s printf, for instance).
	refFixture string

	symbols    int // facts.Extract → len(Symbols)
	declKinds  int // clones.DeclKinds → len
	cloneFns   int // clones.Extract → len
	refs       int // refs.Extract → len
	stubs      int // stubs.Detect → len
	archPkg    int // arch.Extract → 1 when Package is non-empty
	archImprts int // arch.Extract → len(Imports)
	rulePack   int // rules.ForLanguage → len
	ckFamily   bool

	// banKinds are the policy-ban kinds the language can express, and the
	// kinds rulegen will accept. Same list, two sources: if they diverge the
	// gate's own error message starts offering a kind it then rejects.
	banKinds []string
}

// perLanguage holds one row per supported language. Kept keyed by id so a
// missing row is a lookup miss, not a silent zero value.
var perLanguage = map[parser.LanguageID]registryExpectation{
	parser.LangGo: {
		fixture:     "package p\n\nimport \"fmt\"\n\nfunc A(a int) bool {\n\tif a > 0 {\n\t\tfmt.Println(a)\n\t\treturn true\n\t}\n\treturn false\n}\n",
		stubFixture: "package p\n\nfunc A(a int) bool {\n}\n",
		symbols:     1, declKinds: 2, cloneFns: 1, refs: 3, stubs: 1,
		archPkg: 1, archImprts: 1, rulePack: 5, banKinds: []string{"call", "import"},
	},
	parser.LangJava: {
		fixture:     "package p;\n\nimport java.util.List;\n\nclass A {\n\tboolean f(int a) {\n\t\tif (a > 0) { System.out.println(a); return true; }\n\t\treturn false;\n\t}\n}\n",
		stubFixture: "class A {\n\tboolean f(int a) {\n\t}\n}\n",
		symbols:     1, declKinds: 2, cloneFns: 1, refs: 1, stubs: 1,
		archPkg: 1, archImprts: 1, rulePack: 7, ckFamily: true,
		banKinds: []string{"call", "import", "constructor"},
	},
	parser.LangTS: {
		fixture:     "import { B } from \"./b\";\n\nexport function f(a: number): boolean {\n\tif (a > 0) { B(a); return true; }\n\treturn false;\n}\n",
		stubFixture: "export function f(a: number): boolean {\n}\n",
		symbols:     1, declKinds: 3, cloneFns: 1, refs: 1, stubs: 1,
		archPkg: 0, archImprts: 1, rulePack: 5, ckFamily: true,
		banKinds: []string{"call", "import", "constructor"},
	},
	parser.LangTSX: {
		fixture:     "import { B } from \"./b\";\n\nexport function f(a: number): boolean {\n\tif (a > 0) { B(a); return true; }\n\treturn false;\n}\n",
		stubFixture: "export function f(a: number): boolean {\n}\n",
		symbols:     1, declKinds: 3, cloneFns: 1, refs: 1, stubs: 1,
		archPkg: 0, archImprts: 1, rulePack: 5, ckFamily: true,
		banKinds: []string{"call", "import", "constructor"},
	},
	parser.LangJS: {
		fixture:     "import { B } from \"./b\";\n\nfunction f(a) {\n\tif (a > 0) { B(a); return true; }\n\treturn false;\n}\n",
		stubFixture: "function f(a) {\n}\n",
		symbols:     1, declKinds: 3, cloneFns: 1, refs: 1, stubs: 1,
		archPkg: 0, archImprts: 1, rulePack: 5, ckFamily: true,
		banKinds: []string{"call", "import", "constructor"},
	},
	parser.LangPy: {
		fixture:     "import os\n\ndef f(a):\n    if a > 0:\n        os.stat(a)\n        return True\n    return False\n",
		stubFixture: "def f(a):\n    pass\n",
		symbols:     1, declKinds: 1, cloneFns: 1, refs: 1, stubs: 1,
		archPkg: 0, archImprts: 1, rulePack: 5, ckFamily: true,
		banKinds: []string{"call", "import"},
	},
	parser.LangC: {
		fixture:     "#include <stdio.h>\n\nint f(int a) {\n\tif (a > 0) { printf(\"%d\", a); return 1; }\n\treturn 0;\n}\n",
		stubFixture: "int f(int a) {\n}\n",
		symbols:     1, declKinds: 1, cloneFns: 1, refs: 1, stubs: 1,
		archPkg: 0, archImprts: 1, rulePack: 4, ckFamily: true,
		banKinds: []string{"call", "import"},
	},
	parser.LangCpp: {
		fixture:     "#include <vector>\n\nint f(int a) {\n\tif (a > 0) { std::sort(a); return 1; }\n\treturn 0;\n}\n",
		stubFixture: "int f(int a) {\n}\n",
		// std::sort resolves through a qualified name the C++ ref walker does
		// not index; a bare call is what it was written for.
		refFixture: "#include <vector>\n\nint f(int a) {\n\tif (a > 0) { printf(\"%d\", a); return 1; }\n\treturn 0;\n}\n",
		symbols:    1, declKinds: 1, cloneFns: 1, refs: 1, stubs: 1,
		archPkg: 0, archImprts: 1, rulePack: 4, ckFamily: true,
		banKinds: []string{"call", "import", "constructor"},
	},
	parser.LangCS: {
		fixture:     "using System;\n\nclass A {\n\tpublic bool F(int a) {\n\t\tif (a > 0) { Console.WriteLine(a); return true; }\n\t\treturn false;\n\t}\n}\n",
		stubFixture: "class A {\n\tpublic bool F(int a) {\n\t}\n}\n",
		symbols:     1, declKinds: 5, cloneFns: 1, refs: 8, stubs: 1,
		archPkg: 0, archImprts: 1, rulePack: 5, ckFamily: true,
		banKinds: []string{"call", "import", "constructor"},
	},
	parser.LangRust: {
		fixture:     "use openssl::ssl;\n\nfn f(a: i32) -> bool {\n\tif a > 0 { ssl::connect(a); return true; }\n\tfalse\n}\n",
		stubFixture: "fn f(a: i32) -> bool {\n}\n",
		symbols:     1, declKinds: 1, cloneFns: 1, refs: 1, stubs: 1,
		archPkg: 0, archImprts: 1, rulePack: 5, ckFamily: true,
		banKinds: []string{"call", "import"},
	},
	// The three languages iter-14 added. Their zeros are the interesting
	// cells: refs and arch are zero because the grammar has no import or
	// cross-reference surface this pass can read, not because a walk found
	// nothing. Each is stated in docs/metrics.md.
	parser.LangSQL: {
		fixture:     "-- TODO: check\nCREATE FUNCTION f(a int) RETURNS bool AS $$\nBEGIN\n  RETURN a > 0;\nEND;\n$$ LANGUAGE sql;\n",
		stubFixture: "-- TODO: check\nCREATE FUNCTION f(a int) RETURNS bool AS $$\nBEGIN\n  RETURN a > 0;\nEND;\n$$ LANGUAGE sql;\n",
		symbols:     1, declKinds: 2, cloneFns: 1, refs: 0, stubs: 0,
		archPkg: 0, archImprts: 0, rulePack: 3, ckFamily: false,
		banKinds: []string{"call", "import"},
	},
	parser.LangVerilog: {
		fixture:     "// TODO: check\n`include \"defs.vh\"\nmodule m;\n  function automatic int f(input int a);\n    if (a > 0) begin\n      $display(\"%d\", a);\n      return 1;\n    end\n    return 0;\n  endfunction\nendmodule\n",
		stubFixture: "module m;\n  function automatic int f(input int a);\n  endfunction\nendmodule\n",
		refFixture:  "module m;\n  function automatic int f(input int a);\n    helper(a);\n    return 0;\n  endfunction\nendmodule\n",
		symbols:     2, declKinds: 3, cloneFns: 1, refs: 0, stubs: 1,
		archPkg: 0, archImprts: 0, rulePack: 4, ckFamily: false,
		banKinds: []string{"call", "import"},
	},
	parser.LangVB: {
		fixture:     "' TODO: check\nImports MyApp.Core\n\nModule M\n    Function F(a As Integer) As Boolean\n        If a > 0 Then\n            Console.WriteLine(a)\n            Return True\n        End If\n        Return False\n    End Function\nEnd Module\n",
		stubFixture: "Module M\n    Function F(a As Integer) As Boolean\n    End Function\nEnd Module\n",
		refFixture:  "Module M\n    Function F(a As Integer) As Boolean\n        G(a)\n        Return True\n    End Function\nEnd Module\n",
		symbols:     2, declKinds: 2, cloneFns: 1, refs: 0, stubs: 1,
		archPkg: 0, archImprts: 0, rulePack: 4, ckFamily: false,
		banKinds: []string{"call", "import", "constructor"},
	},
}

// cloneCfg is the sensitivity the parity fixtures are sized for. The shipped
// default (10 lines / 60 tokens) is deliberately shallower than one toy
// function, so the row would read zero for every language and prove nothing.
var cloneCfg = clones.Config{MinLines: 2, MinTokens: 5}

func TestPerLanguageRegistryParity(t *testing.T) {
	for _, lang := range parser.Supported() {
		lang := lang
		t.Run(string(lang), func(t *testing.T) {
			exp, ok := perLanguage[lang]
			if !ok {
				t.Fatalf("%s: no row in perLanguage — every supported language "+
					"must answer every registry column, including with an explicit 0 "+
					"(and a reason in docs/metrics.md)", lang)
			}
			name := "a" + parser.Extension(lang)
			src := []byte(exp.fixture)
			tree, release := parsetest.Parse(t, lang, exp.fixture)
			defer release()

			// metrics.ProfileFor is the one column in the metrics package; the
			// rest of that package's tables are pinned by
			// internal/metrics/langparity_test.go, which can see the unexported
			// maps. Here we check the observable consequence instead.
			if got := metrics.AnalyzeFile(name, src, tree, lang); len(got.Functions) == 0 {
				t.Errorf("metrics: no function measured, expected the fixture to be a unit")
			}
			if got := metrics.HasClasses(string(lang)); got != exp.ckFamily {
				t.Errorf("metrics.HasClasses(%s) = %v, want %v (CK family)", lang, got, exp.ckFamily)
			}

			ff := facts.Extract(name, src, tree, lang)

			refName, refSrc, refTree := name, src, tree
			if exp.refFixture != "" {
				refSrc = []byte(exp.refFixture)
				var refRelease func()
				refTree, refRelease = parsetest.Parse(t, lang, exp.refFixture)
				defer refRelease()
			}

			stubTree, stubRelease := parsetest.Parse(t, lang, exp.stubFixture)
			defer stubRelease()

			raw := arch.Extract(lang, src, tree.RootNode())
			hasPkg := 0
			if raw.Package != "" {
				hasPkg = 1
			}

			// One row per pinned column: the read sits beside the name it
			// reports, so adding a registry is one row here (plus a field on
			// registryExpectation) instead of another block in a function that
			// every language has to be re-read to audit.
			for _, col := range []struct {
				name string
				want int
				got  int
			}{
				{"facts.symbols", exp.symbols, len(ff.Symbols)},
				{"clones.declKinds", exp.declKinds, len(clones.DeclKinds(lang))},
				{"clones.Extract", exp.cloneFns, len(clones.Extract(lang, name, src, tree, cloneCfg))},
				{"refs.Extract", exp.refs, len(refs.Extract(refName, refSrc, refTree, lang))},
				{"stubs.Detect", exp.stubs, len(stubs.Detect(name, []byte(exp.stubFixture), stubTree, lang))},
				{"arch.Package", exp.archPkg, hasPkg},
				{"arch.Imports", exp.archImprts, len(raw.Imports)},
				{"rules.ForLanguage", exp.rulePack, len(rules.ForLanguage(lang))},
			} {
				if col.got != col.want {
					t.Errorf("%s: %d, want %d", col.name, col.got, col.want)
				}
			}

			checkBanVocabulary(t, lang, exp.banKinds)
		})
	}
}

// checkBanVocabulary pins the ban vocabulary: the rules engine and the rule
// generator must agree about which ban kinds a language can express, in both
// directions. A kind the engine accepts but rulegen rejects is a rule nobody
// can write; the reverse is a rule that silently never fires.
func checkBanVocabulary(t *testing.T, lang parser.LanguageID, banKinds []string) {
	t.Helper()
	for _, kind := range []string{"call", "import", "constructor"} {
		want := false
		for _, k := range banKinds {
			if k == kind {
				want = true
			}
		}
		if got := rules.CanExpress(string(lang), rules.BanKind(kind)); got != want {
			t.Errorf("rules.CanExpress(%s, %s) = %v, want %v", lang, kind, got, want)
		}
		specErr := rulegen.Validate(&rulegen.Spec{Lang: string(lang), Kind: kind, Match: "x"})
		if want && specErr != nil {
			t.Errorf("rulegen rejects %s/%s even though the engine can express it: %v", lang, kind, specErr)
		}
		if !want && specErr == nil {
			t.Errorf("rulegen accepts %s/%s even though the engine cannot express it", lang, kind)
		}
	}
}

// TestPerLanguageTableHasNoOrphanRows is the other half of the tripwire: the
// table must not keep rows for languages that are no longer supported, or a
// deletion in parser.go looks like a passing test suite.
func TestPerLanguageTableHasNoOrphanRows(t *testing.T) {
	supported := map[parser.LanguageID]bool{}
	for _, id := range parser.Supported() {
		supported[id] = true
	}
	for id := range perLanguage {
		if !supported[id] {
			t.Errorf("perLanguage has a row for %s, which parser.Supported() no longer lists", id)
		}
	}
}

// TestDepsImportSurfaceMatchesTheTable covers the deps registry, whose map is
// unexported. The observable face is ScanRoot: every language must be counted,
// and a language with no import extractor must be named by
// LanguagesWithoutImports rather than dropped. Both halves matter — counting
// only languages that have an extractor is how "no extractor" and "no imports"
// became the same number.
func TestDepsImportSurfaceMatchesTheTable(t *testing.T) {
	dir := t.TempDir()
	for lang, exp := range perLanguage {
		_ = exp
		name := "a" + parser.Extension(lang)
		if name == "a" {
			t.Fatalf("%s: no canonical extension", lang)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(perLanguage[lang].fixture), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d, err := deps.ScanRoot(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	missing := map[string]bool{}
	for _, s := range d.LanguagesWithoutImports() {
		missing[s] = true
	}
	for lang := range perLanguage {
		if got := d.Files[string(lang)]; got != 1 {
			t.Errorf("deps: counted %d files for %s, want 1", got, lang)
		}
	}
	// SQL is the one supported language with no import surface; if that ever
	// changes the row above and this assertion move together.
	if !missing["sql"] {
		t.Errorf("deps: sql is not reported as lacking an import extractor")
	}
	for lang := range perLanguage {
		if string(lang) == "sql" {
			continue
		}
		if missing[string(lang)] {
			t.Errorf("deps: %s reported as lacking an import extractor", lang)
		}
	}
}
