package metrics

import (
	"testing"

	"metricgate/internal/parser"
	"metricgate/internal/parsetest"
)

func analyze(t *testing.T, lang parser.LanguageID, src string) *FileStats {
	t.Helper()
	tree, release := parsetest.Parse(t, lang, src)
	defer release()
	return AnalyzeFile("x."+string(lang), []byte(src), tree, lang)
}

// Go: cc = 1 + if + && + for + if + || + case + case = 8
// cognitive = if(1) + for(2) + if(3) + &&(1) + ||(1) + switch(1) = 9
// max nesting = 3 (if inside for inside if; the top-level switch cases sit at 1)
const snippet = `package x

func A(a, b int) int {
	if a > 0 && b > 0 {
		for i := 0; i < a; i++ {
			if i > b || a > 100 {
				return i
			}
		}
	}
	switch a {
	case 1:
		return 1
	case 2:
		return 2
	}
	return 0
}
`

func TestGoFunctionMetrics(t *testing.T) {
	fs := analyze(t, parser.LangGo, snippet)
	if len(fs.Functions) != 1 {
		t.Fatalf("functions = %d, want 1", len(fs.Functions))
	}
	fn := fs.Functions[0]
	if fn.Name != "A" {
		t.Errorf("name = %q, want A", fn.Name)
	}
	if fn.Params != 2 {
		t.Errorf("params = %d, want 2", fn.Params)
	}
	if fn.Cyclomatic != 8 {
		t.Errorf("cyclomatic = %d, want 8", fn.Cyclomatic)
	}
	if fn.Cognitive != 9 {
		t.Errorf("cognitive = %d, want 9", fn.Cognitive)
	}
	if fn.MaxNesting != 3 {
		t.Errorf("maxNesting = %d, want 3", fn.MaxNesting)
	}
	if fn.Length != 16 {
		t.Errorf("length = %d, want 16", fn.Length)
	}
}

func TestGoMethodNaming(t *testing.T) {
	fs := analyze(t, parser.LangGo, "package x\n\ntype T struct{}\n\nfunc (t *T) Do() {}\n")
	if len(fs.Functions) != 1 {
		t.Fatalf("functions = %d, want 1", len(fs.Functions))
	}
	if got := fs.Functions[0].Name; got != "T.Do" {
		t.Errorf("name = %q, want T.Do", got)
	}
}

// Java: cc = 1 + if + && + for + if + || + case + case = 8 (default excluded)
// cognitive = if(1) + for(2) + if(3) + &&(1) + ||(1) + switch(1) = 9
const javaSnippet = `package x;

class A {
    int chain(int a, int b) {
        if (a > 0 && b > 0) {
            for (int i = 0; i < a; i++) {
                if (i > b || a > 100) {
                    return i;
                }
            }
        }
        switch (a) {
            case 1:
                return 1;
            case 2:
                return 2;
            default:
                return 0;
        }
    }
}
`

func TestJavaFunctionMetrics(t *testing.T) {
	fs := analyze(t, parser.LangJava, javaSnippet)
	if len(fs.Functions) != 1 {
		t.Fatalf("functions = %d, want 1", len(fs.Functions))
	}
	fn := fs.Functions[0]
	if fn.Name != "chain" {
		t.Errorf("name = %q, want chain", fn.Name)
	}
	if fn.Params != 2 {
		t.Errorf("params = %d, want 2", fn.Params)
	}
	if fn.Cyclomatic != 8 {
		t.Errorf("cyclomatic = %d, want 8", fn.Cyclomatic)
	}
	if fn.Cognitive != 9 {
		t.Errorf("cognitive = %d, want 9", fn.Cognitive)
	}
	if fn.MaxNesting != 3 {
		t.Errorf("maxNesting = %d, want 3", fn.MaxNesting)
	}
}

func TestJavaParamsAndVarargs(t *testing.T) {
	src := "package x;\n\nclass A {\n    int m(int a, String b, int... rest) { return 0; }\n}\n"
	fs := analyze(t, parser.LangJava, src)
	if len(fs.Functions) != 1 {
		t.Fatalf("functions = %d, want 1", len(fs.Functions))
	}
	if got := fs.Functions[0].Params; got != 3 {
		t.Errorf("params = %d, want 3", got)
	}
}

// countBoolParams: Go grouped declarations count every name in the group —
// `a, b bool` is two bool parameters, not one (M2-B N05 finding).
func TestBoolParamsGroupedDeclaration(t *testing.T) {
	fs := analyze(t, parser.LangGo, `package x

func Mixed(a bool, b, c bool, d int) { _ = a; _ = b; _ = c; _ = d }

func Solo(only bool) { _ = only }

func None(a, b int) { _ = a; _ = b }
`)
	var mixed, solo *FuncStats
	for i := range fs.Functions {
		switch fs.Functions[i].Name {
		case "Mixed":
			mixed = &fs.Functions[i]
		case "Solo":
			solo = &fs.Functions[i]
		}
	}
	if mixed == nil || solo == nil {
		t.Fatalf("functions missing: %+v", fs.Functions)
	}
	if mixed.BoolParams != 3 {
		t.Errorf("Mixed BoolParams = %d, want 3 (a + grouped b,c)", mixed.BoolParams)
	}
	if solo.BoolParams != 1 {
		t.Errorf("Solo BoolParams = %d, want 1", solo.BoolParams)
	}
}
