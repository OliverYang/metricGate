package metrics

import (
	"math"
	"testing"

	"metricgate/internal/parser"
	"metricgate/internal/parsetest"
)

// Hand-computed anchors for the MI formula (design doc: every metric gets
// a checkable worked example).
func TestMaintainabilityIndexHandComputed(t *testing.T) {
	// V=1000, CC=10, LOC=100:
	// 171 - 5.2*ln(1000) - 0.23*10 - 16.2*ln(100)
	//  = 171 - 35.936 - 2.3 - 74.576 = 58.188 -> *100/171 = 34.028
	mi := MaintainabilityIndex(1000, 10, 100)
	if math.Abs(mi-34.028) > 0.01 {
		t.Errorf("MI = %.3f, want 34.028", mi)
	}
	// Tiny clean file: V=20, CC=1, LOC=5 ->
	// 171 - 5.2*ln(20) - 0.23 - 16.2*ln(5) = 171 - 15.574 - 0.23 - 26.073
	//  = 129.123 -> 75.51
	mi = MaintainabilityIndex(20, 1, 5)
	if math.Abs(mi-75.51) > 0.01 {
		t.Errorf("MI = %.3f, want 75.51", mi)
	}
}

func TestMaintainabilityIndexGuards(t *testing.T) {
	// Degenerate inputs never NaN and stay in [0,100].
	for _, c := range [][3]int{
		{0, 0, 0}, // nothing at all
		{0, 5, 1}, // volumeless but complex
		{1000000, 500, 5000},
	} {
		mi := MaintainabilityIndex(c[0], c[1], c[2])
		if math.IsNaN(mi) || math.IsInf(mi, 0) || mi < 0 || mi > 100 {
			t.Errorf("MI(%v) = %v, want a number in [0,100]", c, mi)
		}
	}
}

func TestHalsteadCensusAndFileMI(t *testing.T) {
	src := "package a\n\nfunc Add(x int, y int) int {\n\treturn x + y\n}\n"
	tree, release := parsetest.Parse(t, parser.LangGo, src)
	defer release()
	stats := AnalyzeFile("x.go", []byte(src), tree, parser.LangGo)
	if len(stats.Functions) != 1 || stats.Functions[0].Volume <= 0 {
		t.Fatalf("function volume = %+v, want > 0", stats.Functions)
	}
	if stats.MI <= 0 || stats.MI > 100 {
		t.Errorf("MI = %v, want in (0,100)", stats.MI)
	}
	if stats.MIScope != "Add" {
		t.Errorf("MIScope = %q, want Add", stats.MIScope)
	}
	// A bigger, branchier file must score lower than this trivial one.
	big := "package a\n\nfunc Busy(x int) int {\n\tt := 0\n"
	for i := 0; i < 20; i++ {
		big += "\tif x > " + string(rune('0'+i%10)) + " {\n\t\tt += " + string(rune('0'+i%10)) + "\n\t}\n"
	}
	big += "\treturn t\n}\n"
	tree2, release2 := parsetest.Parse(t, parser.LangGo, big)
	defer release2()
	big2 := AnalyzeFile("busy.go", []byte(big), tree2, parser.LangGo)
	if big2.MI >= stats.MI {
		t.Errorf("branchy file MI %.1f >= trivial MI %.1f", big2.MI, stats.MI)
	}
}
