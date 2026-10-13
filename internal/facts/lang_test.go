package facts

import (
	"testing"

	"metricgate/internal/parser"
)

// extractOne parses a snippet and returns its fact sheet.
func extractOne(t *testing.T, name, src string) *FileFacts {
	t.Helper()
	lang, ok := parser.LanguageForFile(name)
	if !ok {
		t.Fatalf("no language for %s", name)
	}
	p, err := parser.New(lang)
	if err != nil {
		t.Fatalf("parser.New(%s): %v", lang, err)
	}
	defer p.Close()
	tree, err := p.Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	defer tree.Close()
	return Extract(name, []byte(src), tree, lang)
}

// child returns the named member of sym, or nil.
func child(sym *Symbol, name string) *Symbol {
	if sym == nil {
		return nil
	}
	for i := range sym.Children {
		if sym.Children[i].Name == name {
			return &sym.Children[i]
		}
	}
	return nil
}

// find returns the definition named name, or nil. Members are searched too: a
// C++ class inside a namespace and a TypeScript interface method are both
// children of their owner, yet both are real definitions the index flattens.
func find(f *FileFacts, name string) *Symbol {
	for i := range f.Symbols {
		if f.Symbols[i].Name == name {
			return &f.Symbols[i]
		}
		if c := child(&f.Symbols[i], name); c != nil {
			return c
		}
	}
	return nil
}

// wantDef asserts a definition exists with the given kind and returns it.
func wantDef(t *testing.T, f *FileFacts, name, kind string) *Symbol {
	t.Helper()
	s := find(f, name)
	if s == nil {
		t.Fatalf("%s: %q not extracted, got %s", f.File, name, names(f))
	}
	if kind != "" && s.Kind != kind {
		t.Errorf("%s: %q kind = %q, want %q", f.File, name, s.Kind, kind)
	}
	return s
}

// wantMember asserts sym has a member with the given name and returns it.
func wantMember(t *testing.T, sym *Symbol, name string) *Symbol {
	t.Helper()
	if sym == nil {
		t.Fatalf("no owner symbol to look for member %q in", name)
	}
	c := child(sym, name)
	if c == nil {
		t.Fatalf("%s: has no member %q (has %s)", sym.Name, name, memberNames(sym))
	}
	return c
}

// wantAbsent asserts no definition of that name exists.
func wantAbsent(t *testing.T, f *FileFacts, name string) {
	t.Helper()
	if s := find(f, name); s != nil {
		t.Errorf("%s: %q should not be indexed, got %s", f.File, name, s.Kind)
	}
}

// wantVis asserts a symbol's visibility.
func wantVis(t *testing.T, s *Symbol, vis string) {
	t.Helper()
	if s.Visibility != vis {
		t.Errorf("%s: visibility = %q, want %q", s.Name, s.Visibility, vis)
	}
}

// wantReceiver asserts a symbol's owner.
func wantReceiver(t *testing.T, s *Symbol, recv string) {
	t.Helper()
	if s.Receiver != recv {
		t.Errorf("%s: receiver = %q, want %q", s.Name, s.Receiver, recv)
	}
}

// names renders a file's symbols for failure messages.
func names(f *FileFacts) string {
	out := ""
	for _, s := range f.Symbols {
		out += s.Kind + ":" + s.Name + " "
	}
	return out
}

// memberNames renders a symbol's members for failure messages.
func memberNames(s *Symbol) string {
	out := ""
	for _, c := range s.Children {
		out += c.Kind + ":" + c.Name + " "
	}
	return out
}

// TestExtractEveryTableLanguageHasDefinitions is the regression for the bug
// this file fixes: before the table-driven extractors, only Go and Java
// populated f.Symbols, so the symbol index and `impact` were empty everywhere
// else.
func TestExtractEveryTableLanguageHasDefinitions(t *testing.T) {
	samples := map[string]string{
		"m.py":  "class Base:\n    def parse(self, x):\n        return x\n\ndef top(n):\n    return n\n",
		"m.ts":  "export class Circle { area(): number { return 1; } }\nexport function areaOf(c: Circle) { return c.area(); }\n",
		"m.js":  "class Widget { render() { return 1; } }\nfunction build(n) { return new Widget(); }\n",
		"m.c":   "struct Point { int x; };\nint add(int a, int b) { return a + b; }\n",
		"m.cpp": "namespace geo {\nclass Circle {\npublic:\n  double area() const { return 1; }\n};\n}\n",
		"m.cs":  "public class Circle {\n  public double Area() { return 1; }\n}\n",
		"m.rs":  "pub struct Point { pub x: i32 }\nimpl Point {\n    pub fn new(x: i32) -> Point { Point { x } }\n}\n",
	}
	for file, src := range samples {
		f := extractOne(t, file, src)
		if len(f.Symbols) == 0 {
			t.Errorf("%s: no symbols extracted", file)
		}
	}
}

func TestExtractPythonDefinitionsAreIndexed(t *testing.T) {
	src := `class BaseToolReport:
    def parse(self, x):
        return x

    def _helper(self):
        return 1

    def __init__(self):
        pass

class Sub(BaseToolReport):
    pass

def ParseGeneralToolReport(name):
    return name
`
	f := extractOne(t, "m.py", src)
	if f.Package != "" {
		t.Errorf("python has no package clause, got %q", f.Package)
	}
	base := wantDef(t, f, "BaseToolReport", "class")
	parse := wantMember(t, base, "parse")
	wantReceiver(t, parse, "BaseToolReport")
	wantVis(t, parse, "exported")
	// Python's only visibility convention is the leading underscore, and
	// dunder names are protocol surface rather than private helpers.
	wantVis(t, wantMember(t, base, "_helper"), "private")
	wantVis(t, wantMember(t, base, "__init__"), "exported")
	wantDef(t, f, "Sub", "class")
	wantDef(t, f, "ParseGeneralToolReport", "function")
}

// TestExtractSkipsFunctionBodies guards the walker's central invariant: a
// definition nested inside a function body is not API surface and must not be
// indexed (otherwise local classes would flood the symbol index).
func TestExtractSkipsFunctionBodies(t *testing.T) {
	src := `def outer():
    class Local:
        pass
    def inner():
        return 1
    return inner
`
	f := extractOne(t, "m.py", src)
	wantDef(t, f, "outer", "function")
	wantAbsent(t, f, "Local")
	wantAbsent(t, f, "inner")
}

func TestExtractCVisibilityAndDeclaratorNames(t *testing.T) {
	src := `#include <stdio.h>
static int internal_add(int a, int b) { return a + b; }
int add(int a, int b) { return internal_add(a, b); }
void log_point(struct Point *p) { printf("%d", p->x); }
`
	f := extractOne(t, "m.c", src)
	// The name of a C function lives in a nested declarator, not a field.
	wantVis(t, wantDef(t, f, "internal_add", "function"), "private")
	wantVis(t, wantDef(t, f, "add", "function"), "exported")
	wantVis(t, wantDef(t, f, "log_point", "function"), "exported")
	if sig := find(f, "add").Signature; sig[len(sig)-1] != ')' {
		t.Errorf("signature should stop before the body: %q", sig)
	}
}

func TestExtractTypeScriptExportsAndArrowFunctions(t *testing.T) {
	src := `export interface Shape { area(): number }
export type ID = string
export class Circle {
  private r: number;
  area(): number { return 1; }
}
export const helper = (n: number): number => n * 2;
export const MAX_SIZE = 10;
`
	f := extractOne(t, "m.ts", src)
	shape := wantDef(t, f, "Shape", "interface")
	wantDef(t, f, "area", "method")
	wantDef(t, f, "ID", "type")
	circle := wantDef(t, f, "Circle", "class")
	wantVis(t, wantMember(t, circle, "r"), "private")
	// A const holding a function literal is API surface; a plain const is not.
	wantDef(t, f, "helper", "function")
	wantAbsent(t, f, "MAX_SIZE")
	_ = shape
}

func TestExtractRustImplAnchorsReceiver(t *testing.T) {
	src := `pub trait Shape { fn area(&self) -> f64; }
pub struct Point { pub x: i32 }
impl Shape for Point {
    fn area(&self) -> f64 { 0.0 }
}
impl Point {
    pub fn new(x: i32) -> Point { Point { x } }
    fn hidden(&self) -> i32 { self.x }
}
`
	f := extractOne(t, "m.rs", src)
	wantDef(t, f, "Shape", "trait")
	// Both impl blocks anchor their methods to Point, so the methods are
	// reported against the type rather than as free functions.
	newFn := wantDef(t, f, "new", "function")
	wantReceiver(t, newFn, "Point")
	wantVis(t, newFn, "exported")
	wantVis(t, wantDef(t, f, "hidden", "function"), "private")
	wantAbsent(t, f, "impl")
}

func TestExtractCSharpMembersAndFields(t *testing.T) {
	src := `namespace Demo {
  public interface IShape { double Area(); }
  public class Circle : IShape {
    private double r;
    public double Area() { return r; }
    public string Name { get; set; }
    ~Circle() { }
  }
  public enum Kind { A, B }
}
`
	f := extractOne(t, "m.cs", src)
	circle := wantDef(t, f, "Circle", "class")
	// C# field_declaration has no fields of its own: the name is one level
	// down, inside the variable_declaration it wraps.
	wantVis(t, wantMember(t, circle, "r"), "private")
	wantDef(t, f, "Name", "property")
	// The destructor is a member named after the type, so it is only
	// unambiguous as a member (the class itself carries the same name).
	if d := wantMember(t, circle, "Circle"); d.Kind != "destructor" {
		t.Errorf("member Circle kind = %q, want destructor", d.Kind)
	}
	wantDef(t, f, "Kind", "enum")
	wantDef(t, f, "A", "field")
}

func TestExtractCppNestedClassMethods(t *testing.T) {
	src := `namespace geo {
class Circle {
public:
  double area() const { return 1; }
private:
  double r_;
};
double total(const Circle& c) { return c.area(); }
}
`
	f := extractOne(t, "m.cpp", src)
	ns := wantDef(t, f, "geo", "namespace")
	circle := wantMember(t, ns, "Circle")
	// C++ method definitions are function_definition nodes inside the class
	// body, so they carry the class as their receiver.
	wantReceiver(t, wantMember(t, circle, "area"), "Circle")
	wantReceiver(t, wantMember(t, ns, "total"), "geo")
}

func TestExtractEmptyFileHasNoSymbols(t *testing.T) {
	if f := extractOne(t, "m.py", "\n"); len(f.Symbols) != 0 {
		t.Errorf("empty file produced symbols: %s", names(f))
	}
}

// TestExtractSignatureStopsBeforeBody keeps the constraint pack readable: a
// signature that swallowed the body would blow the token budget.
func TestExtractSignatureStopsBeforeBody(t *testing.T) {
	f := extractOne(t, "m.py", "def f(a, b):\n    return a + b\n")
	s := wantDef(t, f, "f", "function")
	if s.Signature != "def f(a, b):" {
		t.Errorf("signature = %q, want the declaration only", s.Signature)
	}
	if s.StartLine != 1 {
		t.Errorf("start line = %d, want 1", s.StartLine)
	}
}

// TestExtractMemberNamesUseOwnerReceiver keeps the flattened index unambiguous:
// two classes may both define parse, and the receiver is the only thing that
// tells them apart.
func TestExtractMemberNamesUseOwnerReceiver(t *testing.T) {
	src := "class A:\n    def parse(self):\n        return 1\n\nclass B:\n    def parse(self):\n        return 2\n"
	f := extractOne(t, "m.py", src)
	a := wantDef(t, f, "A", "class")
	b := wantDef(t, f, "B", "class")
	wantReceiver(t, wantMember(t, a, "parse"), "A")
	wantReceiver(t, wantMember(t, b, "parse"), "B")
	if a.Children[0].StartLine == b.Children[0].StartLine {
		t.Error("same-named members of different classes must stay distinguishable")
	}
	_ = memberNames(a)
}
