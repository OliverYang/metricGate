package facts

import (
	"strings"
	"testing"
	"unicode/utf8"

	"metricgate/internal/config"
)

func budgetFixture(t *testing.T) ([]*FileFacts, *config.Config) {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	files := []*FileFacts{
		{File: "pkg/a.go", Language: "go", Package: "pkg", Imports: []string{"fmt", "strings"}, Symbols: []Symbol{
			{Name: "Run", Kind: "function", Signature: "func Run(input string) error", Visibility: "exported", StartLine: 3},
			{Name: "privateHelper", Kind: "function", Signature: "func privateHelper() string", Visibility: "private", StartLine: 9},
		}},
		{File: "pkg/b.go", Language: "go", Package: "pkg", Symbols: []Symbol{
			{Name: "Handle", Kind: "function", Signature: "func Handle(ctx Context) Result", Visibility: "exported", StartLine: 1},
		}},
	}
	return files, cfg
}

func TestRenderBudgetedDeterministicWithinBudgets(t *testing.T) {
	files, cfg := budgetFixture(t)
	for _, budget := range []int{1100, 2000, 6000} {
		a, err := RenderBudgeted(files, cfg, BudgetOptions{Budget: budget, Counter: CharCounter{}, TargetFiles: []string{"pkg/a.go"}})
		if err != nil {
			t.Fatalf("budget %d: %v", budget, err)
		}
		b, err := RenderBudgeted(files, cfg, BudgetOptions{Budget: budget, Counter: CharCounter{}, TargetFiles: []string{"pkg/a.go"}})
		if err != nil {
			t.Fatal(err)
		}
		if a.Text != b.Text || len(a.Text) > budget || !utf8.ValidString(a.Text) {
			t.Errorf("budget %d unstable/over/invalid: used=%d exact=%v", budget, a.Used, a.Exact)
		}
		if !strings.Contains(a.Text, "## constraints (policy)") {
			t.Errorf("budget %d omitted hard constraints", budget)
		}
		if !containsRef(a.Included, "file:pkg/a.go") {
			t.Errorf("budget %d omitted required target file", budget)
		}
	}
}

func TestRenderBudgetedReportsRequiredOverflow(t *testing.T) {
	files, cfg := budgetFixture(t)
	res, err := RenderBudgeted(files, cfg, BudgetOptions{Budget: 30, Counter: CharCounter{}, TargetFiles: []string{"pkg/a.go"}})
	if err == nil || err != ErrBudgetExhausted || !res.BudgetExhausted {
		t.Fatalf("result=%+v err=%v, want explicit budget exhaustion", res, err)
	}
	if res.Text != "" {
		t.Errorf("overflow must not return a misleading partial pack: %q", res.Text)
	}
	if len(res.Omitted) == 0 {
		t.Error("overflow response must identify required items that did not fit")
	}
}

func TestRenderBudgetedOptionalOmissionIsAtomic(t *testing.T) {
	files, cfg := budgetFixture(t)
	// Required header+constraints fit; optional facts (and the repo map) are
	// omitted as whole items while all policy lines remain intact. The budget
	// tracks the required set's size — currently ~960 chars with the full
	// default rule catalog — plus headroom for one optional item to be admitted.
	res, err := RenderBudgeted(files, cfg, BudgetOptions{Budget: 1000, Counter: CharCounter{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "## constraints (policy)") {
		t.Fatal("constraints missing")
	}
	for _, o := range res.Omitted {
		if o.Reason != "budget" {
			t.Errorf("omission=%+v", o)
		}
	}
	if res.Used > res.Budget || !utf8.ValidString(res.Text) {
		t.Errorf("invalid result=%+v", res)
	}
}

func TestTokenEstimateDeclaresApproximation(t *testing.T) {
	files, cfg := budgetFixture(t)
	res, err := RenderBudgeted(files, cfg, BudgetOptions{Budget: 1500, Counter: CharEstimate{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Exact || res.Counter != "bytes-div-4-estimate-v1" || res.Used <= 0 {
		t.Errorf("estimate metadata = %+v", res)
	}
}

func containsRef(refs []PackRef, id string) bool {
	for _, r := range refs {
		if r.ID == id {
			return true
		}
	}
	return false
}

// TestPackOrdersRequiredBeforeOptionalInText proves the ordering contract at
// the text level: in the emitted pack, the (required) constraints block comes
// before any optional per-file fact block, so the most important content is
// near the top of the prompt.
func TestPackOrdersRequiredBeforeOptionalInText(t *testing.T) {
	// Inject a required constraints item plus optional fact items directly, so
	// the assertion is about ordering, not about how items are built.
	items := []PackItem{
		{ID: "facts2", Section: "facts", Order: OrderFacts, Priority: 1, Text: "## pkg/b.go (go)\n- func Handle()\n\n"},
		{ID: "constraints", Section: "constraints", Order: OrderConstraints, Priority: 0, Required: true, Text: "## constraints (policy)\n- banned-api: fmt.Print*\n\n"},
		{ID: "header", Section: "header", Order: OrderHeader, Priority: 0, Required: true, Text: "# metricgate context\n\n"},
		{ID: "facts1", Section: "facts", Order: OrderFacts, Priority: 1, Text: "## pkg/a.go (go)\n- func Run()\n\n"},
	}
	res, err := PackItemsBudgeted(items, BudgetOptions{Budget: 10000, Counter: CharCounter{}})
	if err != nil {
		t.Fatal(err)
	}
	hi := strings.Index(res.Text, "# metricgate context")
	ci := strings.Index(res.Text, "## constraints")
	fi := strings.Index(res.Text, "## pkg/a.go")
	if hi < 0 || ci < 0 || fi < 0 {
		t.Fatalf("missing block in text:\n%s", res.Text)
	}
	if !(hi < ci && ci < fi) {
		t.Errorf("order header(%d) < constraints(%d) < facts(%d) violated:\n%s", hi, ci, fi, res.Text)
	}
	if res.Included[0].Section != "header" || res.Included[1].Section != "constraints" {
		t.Errorf("included order = %+v, want header then constraints", res.Included)
	}
}
