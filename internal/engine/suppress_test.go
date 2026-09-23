package engine

import (
	"strings"
	"testing"
)

func TestSuppressLineDirective(t *testing.T) {
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	src := `package g

import "fmt"

func A() {
	fmt.Println("kept")
	fmt.Println("ignored on this line") // metricgate: ignore no-fmt-print demo
	fmt.Println("kept too")
}
`
	findings, err := eng.CheckSource("g/a.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	prints := 0
	for _, f := range findings {
		if f.RuleID == "no-fmt-print" {
			prints++
			if strings.Contains(f.Message, "ignored on this line") {
				t.Error("line-directed finding was not suppressed")
			}
		}
	}
	if prints != 2 {
		t.Errorf("no-fmt-print = %d, want 2 (one suppressed)", prints)
	}
}

func TestSuppressAboveDeclaration(t *testing.T) {
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	src := `package g

// metricgate: ignore params-count legacy signature
func B(a, b, c, d, e, f, g int) int {
	return a + b + c + d + e + f + g
}

func C(a, b, c, d, e, f, g int) int {
	return a + b + c + d + e + f + g
}
`
	findings, err := eng.CheckSource("g/b.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if f.RuleID == "params-count" && strings.Contains(f.Message, "B") {
			t.Error("params-count for B should be suppressed by preceding directive")
		}
	}
}

func TestSuppressBeginEnd(t *testing.T) {
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	src := `package g

import "fmt"

// metricgate: ignore-begin no-fmt-print
func D() {
	fmt.Println("in range")
}

func E() {
	fmt.Println("also in range until end")
	// metricgate: ignore-end
	fmt.Println("outside range")
}
`
	findings, err := eng.CheckSource("g/d.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.RuleID]++
	}
	if counts["no-fmt-print"] != 1 {
		t.Errorf("no-fmt-print = %d, want 1 (two suppressed by range)", counts["no-fmt-print"])
	}
}
