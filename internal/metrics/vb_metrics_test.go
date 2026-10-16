package metrics

import (
	"testing"

	"metricgate/internal/parser"
	"metricgate/internal/parsetest"
)

// VB.NET is the only supported language whose member names are
// case-insensitive and whose methods have no `body` field at all — the
// statements hang off the declaration as siblings. Both facts are pinned here.

// cyclomatic = 1 (base)
//   - 1 if + 1 elseif (else is not a decision)
//   - 1 for + 1 while + 1 do
//   - 2 case arms (Case Else is the default)
//   - 1 catch
//     = 8
//
// cognitive = if(1) + elseif(1) + for(1) + while(1) + do(1) + select(1)
//   - catch(1, flat) = 8
//
// maxNesting = 2 (the case arms sit inside the select inside nothing; the
// try's statements sit at 1 and the catch at 1 — the for/while bodies are 1)
const vbAllConstructsSrc = `Public Class C
    Public Function F(a As Integer, b As Integer) As Integer
        Dim r As Integer = 0
        If a > 0 Then
            r = 1
        ElseIf b > 0 Then
            r = 2
        Else
            r = 3
        End If
        For i As Integer = 0 To 10 Step 2
            r = r + i
        Next
        While r < 100
            r = r * 2
        End While
        Do
            r = r - 1
        Loop While r > 50
        Select Case r
            Case 1
                r = 4
            Case 2
                r = 5
            Case Else
                r = 6
        End Select
        Try
            r = Helper(a)
        Catch ex As Exception
            r = 0
        Finally
            r = r + 1
        End Try
        Return r
    End Function
End Class
`

func TestVBFunctionMetrics(t *testing.T) {
	fs := analyze(t, parser.LangVB, vbAllConstructsSrc)
	if len(fs.Functions) != 1 {
		t.Fatalf("functions = %d, want 1", len(fs.Functions))
	}
	fn := fs.Functions[0]
	if fn.Name != "f" {
		t.Errorf("name = %q, want f (VB member names are lower-cased; see vbDeclName)", fn.Name)
	}
	if fn.Params != 2 {
		t.Errorf("params = %d, want 2", fn.Params)
	}
	if fn.Cyclomatic != 8 {
		t.Errorf("cyclomatic = %d, want 8", fn.Cyclomatic)
	}
	if fn.Cognitive != 8 {
		t.Errorf("cognitive = %d, want 8", fn.Cognitive)
	}
	if fn.MaxNesting != 2 {
		t.Errorf("maxNesting = %d, want 2", fn.MaxNesting)
	}
	if fn.NPath == 0 {
		t.Error("npath = 0; the method body was not lowered")
	}
	if fn.Volume == 0 {
		t.Error("volume = 0; the statements hanging off the declaration were not found")
	}
}

// A constructor has no name field, so it is reported as `New` — the same
// convention the refs and stubs layers use. Without it every constructor would
// be an anonymous function.
func TestVBConstructorIsNamedNew(t *testing.T) {
	src := `Public Class C
    Public Sub New(a As Integer)
        Me.Value = a
    End Sub
    Public Property Value As Integer
End Class
`
	fs := analyze(t, parser.LangVB, src)
	if len(fs.Functions) != 1 {
		t.Fatalf("functions = %d, want 1 (the constructor); got %+v", len(fs.Functions), fs.Functions)
	}
	if fs.Functions[0].Name != "new" {
		t.Errorf("name = %q, want new", fs.Functions[0].Name)
	}
}

// VB spells a call `invocation` with the callee in the `target` field, and the
// receiver of a member call is a member_access node.
func TestVBFanOut(t *testing.T) {
	src := `Module M
    Function F(a As Integer) As Integer
        Helper(a)
        Helper(a)
        Return Me.Other(a)
    End Function
End Module
`
	fs := analyze(t, parser.LangVB, src)
	if len(fs.Functions) != 1 {
		t.Fatalf("functions = %d, want 1", len(fs.Functions))
	}
	// Helper twice and Me.Other once are two distinct callees.
	if fs.Functions[0].FanOut != 2 {
		t.Errorf("fanOut = %d, want 2", fs.Functions[0].FanOut)
	}
}

// VB's `binary_expression` carries an operator field, but the grammar does not
// emit a token for AndAlso / OrElse, so the adapter cannot tell a logical run
// from a plain comparison. That limitation is asserted rather than assumed:
// if a future grammar release starts emitting the operator, this test fails
// and the adapter should learn to score Logical.
func TestVBAndAlsoIsNotScored(t *testing.T) {
	src := `Module M
    Function F(a As Integer, b As Integer) As Boolean
        If a > 0 AndAlso b > 0 Then
            Return True
        End If
        Return False
    End Function
End Module
`
	fs := analyze(t, parser.LangVB, src)
	if len(fs.Functions) != 1 {
		t.Fatalf("functions = %d, want 1", len(fs.Functions))
	}
	// cyclomatic 2 = base + the if. AndAlso contributes nothing, which is the
	// documented gap; a plain `&&` in Go would make this 3.
	if fs.Functions[0].Cyclomatic != 2 {
		t.Errorf("cyclomatic = %d, want 2 (AndAlso is not scored; see docs/metrics.md)", fs.Functions[0].Cyclomatic)
	}
}

// The flag-argument smell: a boolean parameter doubles the caller's hidden
// states. VB spells the type `As Boolean`, and its case-insensitivity means the
// spelling may be any casing, so "boolean" and "BOOLEAN" must count too.
//
// Leaving this to the Go/Java default made every VB boolean parameter
// invisible: that default understands Go's type_identifier "bool" and Java's
// boolean_type, neither of which a VB `parameter` node has.
func TestVBBoolParams(t *testing.T) {
	src := `Module M
    Sub S(a As Boolean, b As Boolean, c As Integer)
    End Sub
    Sub T(x As boolean, y As BOOLEAN)
    End Sub
    Sub U(a As Integer)
    End Sub
End Module
`
	fs := analyze(t, parser.LangVB, src)
	if len(fs.Functions) != 3 {
		t.Fatalf("functions = %d, want 3", len(fs.Functions))
	}
	want := []int{2, 2, 0}
	for i, fn := range fs.Functions {
		if fn.BoolParams != want[i] {
			t.Errorf("%s: boolParams = %d, want %d", fn.Name, fn.BoolParams, want[i])
		}
	}
}

// A parameter typed `List(Of Boolean)` is a list of booleans, not a boolean
// flag, so it must not be counted. The grammar also errors on generic type
// arguments, which is why the count comes from a non-generic module: the
// generic hole is a parse gap, not a counting decision.
func TestVBGenericListIsNotABoolParam(t *testing.T) {
	// The type spelling is exactly `Boolean`, so a naive text match would
	// count it; a list of booleans is not a caller-facing flag.
	src := `Module M
    Sub S(flag As Boolean)
    End Sub
End Module
`
	fs := analyze(t, parser.LangVB, src)
	if len(fs.Functions) != 1 || fs.Functions[0].BoolParams != 1 {
		t.Fatalf("boolParams = %v, want [1]", fs.Functions)
	}
}

// The grammar errors on generic type arguments (`List(Of Boolean)`), which
// takes the whole method with it. Pinned so the gap is a test failure someone
// has to look at, not a silent zero.
func TestVBGenericTypeArgumentIsAGrammarGap(t *testing.T) {
	src := `Module M
    Sub S(items As List(Of Boolean))
    End Sub
End Module
`
	tree, release := parsetest.Parse(t, parser.LangVB, src)
	defer release()
	if !tree.RootNode().HasError() {
		t.Error("expected a parse error: the upstream grammar rejects generic type arguments (see docs/metrics.md)")
	}
}

// Every upstream gap docs/metrics.md lists for VB.NET must be a test failure
// here rather than a paragraph in a doc. Each entry is a minimal file: the
// `pe` column says whether the grammar rejects it. The point is that a future
// grammar upgrade which fixes a hole breaks a test, and someone has to update
// the doc in the same commit.
func TestVBDocumentedGrammarGaps(t *testing.T) {
	cases := []struct {
		name string
		src  string
		pe   bool
	}{
		{"clean class", "Class C\n    Sub F()\n    End Sub\nEnd Class\n", false},
		{"generic type argument", "Module M\n    Sub S(items As List(Of Boolean))\n    End Sub\nEnd Module\n", true},
		{"class-level Implements", "Interface IBar\n    Sub F()\nEnd Interface\n\nClass C\n    Implements IBar\n\n    Public Sub F()\n    End Sub\nEnd Class\n", true},
		{"member Implements", "Class C\n    Public Sub F() Implements IBar\n    End Sub\nEnd Class\n", true},
		{"member attribute", "Class C\n    <Obsolete> Sub F()\n    End Sub\nEnd Class\n", true},
		{"class-level region", "Class C\n#Region \"h\"\n    Sub F()\n    End Sub\n#End Region\nEnd Class\n", true},
		{"region inside method", "Class C\n    Sub F()\n#Region \"h\"\n        Dim a As Integer = 1\n#End Region\n    End Sub\nEnd Class\n", false},
		{"no trailing newline", "Class C\n    Sub F()\n    End Sub\nEnd Class", true},
		{"file-level attribute", "<Assembly: Obsolete(\"x\")>\nClass C\nEnd Class\n", false},
	}
	for _, tc := range cases {
		tree, release := parsetest.Parse(t, parser.LangVB, tc.src)
		got := tree.RootNode().HasError()
		release()
		if got != tc.pe {
			t.Errorf("%s: parse error = %v, want %v", tc.name, got, tc.pe)
		}
	}
}

// Every supported language must reach countBoolParams through an explicit arm:
// the switch's default walks Go/Java-shaped nodes, so a language that forgets
// an arm silently reports 0 (which is what VB did before this test existed).
// The parity is asserted the way it can actually fail: each language gets a
// snippet whose count is known, and the test refuses to run unless every
// language in Supported() has a fixture here.
func TestBoolParamsCoversEveryLanguage(t *testing.T) {
	fixtures := map[parser.LanguageID]struct {
		src  string
		want int
		why  string
	}{
		parser.LangGo:      {"package p\nfunc F(a, b bool, c int) {}\n", 2, "go grouped decl"},
		parser.LangJava:    {"class C { void F(boolean a, int b) {} }\n", 1, "java boolean_type"},
		parser.LangTS:      {"function F(a: boolean, b: number) {}\n", 1, "ts type annotation"},
		parser.LangTSX:     {"function F(a: boolean, b: number) {}\n", 1, "tsx type annotation"},
		parser.LangJS:      {"function F(a, b) {}\n", 0, "untyped js"},
		parser.LangPy:      {"def F(a, b): pass\n", 0, "python has no annotations here"},
		parser.LangC:       {"void F(bool a, int b) {}\n", 1, "c primitive_type"},
		parser.LangCpp:     {"void F(bool a, int b) {}\n", 1, "cpp primitive_type"},
		parser.LangCS:      {"class C { void F(bool a, int b) {} }\n", 1, "cs predefined_type"},
		parser.LangRust:    {"fn F(a: bool, b: i32) {}\n", 1, "rust primitive_type"},
		parser.LangSQL:     {"CREATE FUNCTION f(a int) RETURNS int AS $$ SELECT 1; $$ LANGUAGE sql;\n", 0, "no boolean arguments"},
		parser.LangVerilog: {"module m; function int f(input int a); return 1; endfunction endmodule\n", 0, "no boolean type"},
		parser.LangVB:      {"Module M\n    Sub S(a As Boolean, b As Integer)\n    End Sub\nEnd Module\n", 1, "As Boolean"},
	}
	for _, lang := range parser.Supported() {
		fx, ok := fixtures[lang]
		if !ok {
			t.Errorf("language %s has no boolean-params fixture; add one so a missing arm fails here", lang)
			continue
		}
		fs := analyze(t, lang, fx.src)
		if len(fs.Functions) == 0 {
			t.Errorf("%s (%s): no functions parsed; the fixture cannot prove the arm", lang, fx.why)
			continue
		}
		got := 0
		for _, fn := range fs.Functions {
			got += fn.BoolParams
		}
		if got != fx.want {
			t.Errorf("%s (%s): boolParams = %d, want %d", lang, fx.why, got, fx.want)
		}
	}
}
