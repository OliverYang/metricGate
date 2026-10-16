package metrics

import (
	"sort"
	"testing"

	"metricgate/internal/parser"
	"metricgate/internal/parsetest"
)

// ckLang measures a source file in the given language and indexes its classes
// by name. The Java helper ck in classes_test.go stays as the reference
// implementation the other languages are compared against.
func ckLang(t *testing.T, lang parser.LanguageID, name, src string) map[string]ClassStats {
	t.Helper()
	tree, release := parsetest.Parse(t, lang, src)
	defer release()
	stats := AnalyzeFile(name, []byte(src), tree, lang)
	byName := map[string]ClassStats{}
	for _, c := range stats.Classes {
		byName[c.Name] = c
	}
	return byName
}

// wantClass fails when the class was not measured at all, which is the
// failure mode that matters here: a language whose member spelling is wrong
// silently reports zero classes.
func wantClass(t *testing.T, got map[string]ClassStats, name, kind string) ClassStats {
	t.Helper()
	c, ok := got[name]
	if !ok {
		t.Fatalf("class %q not measured; measured %v", name, sortedNames(got))
	}
	if c.Kind != kind {
		t.Errorf("%s kind = %q, want %q", name, c.Kind, kind)
	}
	return c
}

func sortedNames(got map[string]ClassStats) []string {
	out := make([]string, 0, len(got))
	for name := range got {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Fixture sources live outside the table so each case function stays a short,
// readable list of expectations. They are the exact inputs whose measurements
// the expectations below were taken from.
const (
	ckTSSrc = `export interface Repo { find(id: string): string; }
export class Service implements Repo {
  private repo: Store;
  count: number = 0;
  constructor(repo: Store) { this.repo = repo; }
  find(id: string): string { return this.repo.read(id); }
}
export class Store extends Base {
  private items: Map<string, string> = new Map();
  read(id: string): string { return ""; }
}
`
	ckJSSrc = `class Base {}
class Store extends Base {
  items;
  #hidden;
  constructor() { this.items = 1; }
  read(id) { return this.items; }
}
`
	ckPySrc = `class Base:
    pass

class Store(Base):
    items = {}
    hidden = 0

    def __init__(self, repo):
        self.repo = repo

    def read(self, i):
        return self.items
`
	ckCppSrc = `class Base { public: virtual int area() const; };
class Shape : public Base {
public:
  int id;
  double area2;
  int area() const { return id; }
  void go() = 0;
};
`
	ckCSrc = "struct Point {\n  int x;\n  int y;\n};\n"

	ckCSSrc = `namespace App {
  interface IShape { int Area(); }
  class Circle : Base, IShape {
    private int r;
    public int P { get; set; }
    public int Q => 1;
    public Circle(int r) { this.r = r; }
    public int Area() { return r; }
  }
}
`
	ckRustSrc = `struct Svc { items: Vec<i32> }
trait Draw { fn draw(&self); }
impl Svc {
    fn read(&self, i: i32) -> i32 { i }
}
impl Draw for Svc {
    fn draw(&self) {}
}
`
)

// classCase is one language's spelling of a class, as the CK family reads it.
type classCase struct {
	lang  parser.LanguageID
	file  string
	src   string
	name  string
	kind  string
	nom   int
	nof   int
	cbo   int
	dit   int
	notes string
}

// classCases holds one fixture per language that declares classes. Every
// value was measured first and then written down, so a change in a grammar
// spelling shows up as a diff here rather than as a silent zero. The table is
// split per language only to keep each function readable.
func classCases() []classCase {
	out := make([]classCase, 0, 7)
	out = append(out, classCasesScript()...)
	out = append(out, classCasesNative()...)
	return out
}

// classCasesScript covers the scripting languages (TS, JS, Python), where
// members are declared by keyword rather than by type.
func classCasesScript() []classCase {
	return []classCase{
		{
			lang: parser.LangTS, file: "s.ts", name: "Service", kind: "class",
			nom: 2, nof: 2, cbo: 2, dit: 1,
			src:   ckTSSrc,
			notes: "implements target and field type both count as coupling",
		},
		{
			lang: parser.LangJS, file: "s.js", name: "Store", kind: "class",
			nom: 2, nof: 2, cbo: 1, dit: 1,
			src:   ckJSSrc,
			notes: "extends is the only type reference JavaScript has",
		},
		{
			lang: parser.LangPy, file: "s.py", name: "Store", kind: "class",
			nom: 2, nof: 2, cbo: 1, dit: 1,
			src: ckPySrc,
		},
	}
}

// classCasesNative covers the languages whose classes carry declared field
// types (C++, C, C#, Rust).
func classCasesNative() []classCase {
	return []classCase{
		{
			lang: parser.LangCpp, file: "s.cpp", name: "Shape", kind: "class",
			nom: 2, nof: 2, cbo: 1, dit: 1,
			src: ckCppSrc,
		},
		{
			lang: parser.LangC, file: "s.c", name: "Point", kind: "struct",
			nom: 0, nof: 2, cbo: 0, dit: 0,
			src:   ckCSrc,
			notes: "C has no methods and no inheritance",
		},
		{
			lang: parser.LangCS, file: "S.cs", name: "Circle", kind: "class",
			nom: 2, nof: 3, cbo: 2, dit: 1,
			src:   ckCSSrc,
			notes: "properties are fields; member names must not inflate CBO",
		},
		{
			lang: parser.LangRust, file: "s.rs", name: "Svc", kind: "struct",
			nom: 2, nof: 1, cbo: 1, dit: 0,
			src:   ckRustSrc,
			notes: "impl blocks attach their methods to the type, not the trait",
		},
	}
}

// TestClassMetricsPerLanguage pins the class spelling of every language that
// declares classes: which node is a class, which members are fields versus
// methods, and where the inheritance depth comes from.
func TestClassMetricsPerLanguage(t *testing.T) {
	for _, tc := range classCases() {
		t.Run(string(tc.lang)+"/"+tc.name, func(t *testing.T) {
			got := ckLang(t, tc.lang, tc.file, tc.src)
			c := wantClass(t, got, tc.name, tc.kind)
			if c.NOM != tc.nom {
				t.Errorf("NOM = %d, want %d (%s)", c.NOM, tc.nom, tc.notes)
			}
			if c.NOF != tc.nof {
				t.Errorf("NOF = %d, want %d (%s)", c.NOF, tc.nof, tc.notes)
			}
			if c.CBO != tc.cbo {
				t.Errorf("CBO = %d, want %d (%s)", c.CBO, tc.cbo, tc.notes)
			}
			if c.DIT != tc.dit {
				t.Errorf("DIT = %d, want %d (%s)", c.DIT, tc.dit, tc.notes)
			}
		})
	}
}

// TestClassMetricsCSharpNestedFieldNames pins the C# field spelling: a field
// declaration carries no name itself, the declarator sits inside a
// variable_declaration node.
func TestClassMetricsCSharpNestedFieldNames(t *testing.T) {
	got := ckLang(t, parser.LangCS, "V.cs", `class V { public int X; public string Name; }`)
	c := wantClass(t, got, "V", "class")
	if c.NOF != 2 {
		t.Fatalf("NOF = %d, want 2 (field names live under variable_declaration)", c.NOF)
	}
	if c.CBO != 0 {
		t.Errorf("CBO = %d, want 0: a member name is not a type reference", c.CBO)
	}
}

// TestClassMetricsGoHasNoClasses pins the boundary: Go is in
// parser.Supported() but has no class spelling, so it must contribute no
// classes and must not be told it has CK rules.
func TestClassMetricsGoHasNoClasses(t *testing.T) {
	got := ckLang(t, parser.LangGo, "x.go", `package x

type T struct{ A int }

func (t T) M() int { return t.A }
`)
	if len(got) != 0 {
		t.Fatalf("Go produced %d classes, want 0", len(got))
	}
	if HasClasses("go") {
		t.Error("HasClasses(go) = true, want false")
	}
	for _, id := range IDsFor("go") {
		if id == MetricWMC || id == MetricNOM || id == MetricLCOM || id == MetricCBO || id == MetricDIT {
			t.Errorf("IDsFor(go) contains %s; Go can never fire it", id)
		}
	}
}

// TestHasClassesMatchesSpecs keeps HasClasses and the spelling table in sync:
// every language with a class spec advertises the CK family, and the two
// languages without one do not.
func TestHasClassesMatchesSpecs(t *testing.T) {
	want := map[string]bool{
		"go": false, "java": true, "typescript": true, "tsx": true, "javascript": true,
		"python": true, "c": true, "cpp": true, "csharp": true, "rust": true,
		// SQL and Verilog have no class construct at all; VB.NET has class
		// blocks, but the CK family needs member-level visibility and
		// inheritance modelling this grammar does not carry, so it is not
		// advertised for them either.
		"sql": false, "verilog": false, "vbnet": false,
	}
	for _, id := range parser.Supported() {
		got := HasClasses(string(id))
		expected, known := want[string(id)]
		if !known {
			t.Errorf("language %s has no expectation in this test", id)
			continue
		}
		if got != expected {
			t.Errorf("HasClasses(%s) = %v, want %v", id, got, expected)
		}
		advertised := false
		for _, m := range IDsFor(string(id)) {
			if m == MetricWMC {
				advertised = true
			}
		}
		if advertised != expected {
			t.Errorf("IDsFor(%s) advertises WMC = %v, want %v", id, advertised, expected)
		}
	}
}
