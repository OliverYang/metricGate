package engine

import (
	"testing"

	"metricgate/internal/model"
)

func TestCheckSource(t *testing.T) {
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	src := `package gen

import "fmt"

func Generate(id string) {
	fmt.Println("working", id)
	panic("not implemented")
}
`
	findings, err := eng.CheckSource("gen/service.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.RuleID]++
		if f.File != "gen/service.go" {
			t.Errorf("finding file = %q, want virtual name", f.File)
		}
	}
	for _, rule := range []string{"banned-fmt-Print*", "banned-panic"} {
		if counts[rule] == 0 {
			t.Errorf("%s missing from %v", rule, counts)
		}
	}
}

func TestCheckSourceRejectsUnknownLanguage(t *testing.T) {
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	if _, err := eng.CheckSource("x.py", []byte("print(1)")); err == nil {
		t.Fatal("expected error for unsupported language")
	}
}

func TestCheckSourceEmptyBodyIsNotARuleFinding(t *testing.T) {
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	findings, err := eng.CheckSource("gen/x.java", []byte("package g;\nclass X {\n    void empty() {\n    }\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.Severity == model.SevError {
			t.Errorf("unexpected error finding: %+v", f)
		}
	}
}

// A snippet that does not parse must still say so. This is the MCP check_code
// path, i.e. the one a coding agent calls on freshly generated code: returning
// a clean result for unparseable input is the worst possible failure mode for
// a gate, because the agent reads "0 errors" and moves on.
func TestCheckSourceReportsParseErrors(t *testing.T) {
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	findings, err := eng.CheckSource("gen/broken.go", []byte("package gen\n\nfunc F( {\n\treturn 1\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, f := range findings {
		if f.RuleID != "parse-error" {
			continue
		}
		seen = true
		if f.Severity != model.SevInfo {
			t.Errorf("parse-error severity = %s, want info (it must not fail the gate)", f.Severity)
		}
		if f.File != "gen/broken.go" {
			t.Errorf("parse-error file = %q, want the virtual name", f.File)
		}
	}
	if !seen {
		t.Errorf("no parse-error finding for unparseable source; got %v", findings)
	}
}
