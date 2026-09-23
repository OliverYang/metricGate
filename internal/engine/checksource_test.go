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
