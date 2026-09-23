package arch

import (
	"reflect"
	"strings"
	"testing"
)

func TestMatchesLiteralPattern(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"internal/rules", "internal/rules", true},
		{"internal/rules", "internal/ruleset", false},
		{"internal/rules", "metricgate/internal/rules", true}, // module prefix tolerated
		{"metricgate/internal/rules", "internal/rules", false},
		{"internal/rules", "", false},
		{"", "internal/rules", false},
	}
	for _, c := range cases {
		if got := matches(c.pattern, c.name); got != c.want {
			t.Errorf("matches(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestMatchesWildcards(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"internal/*", "internal/rules", true},
		{"internal/*", "internal/engine/rules", false}, // "*" is one segment
		{"internal/*", "metricgate/internal/rules", false},
		{"*/rules", "internal/rules", true},
		{"internal/**", "internal", true},
		{"internal/**", "internal/engine/rules", true},
		{"internal/**", "metricgate/internal/engine", true},
		{"internal/**", "vendor/internal/engine", true},
		{"**", "anything/at/all", true},
		{"app/handler/**", "other/handler", false},
	}
	for _, c := range cases {
		if got := matches(c.pattern, c.name); got != c.want {
			t.Errorf("matches(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestResolvesPerLanguage(t *testing.T) {
	cases := []struct {
		imp, pkg string
		java     bool
		want     bool
	}{{
		imp: "metricgate/internal/rules", pkg: "internal/rules", want: true,
	}, {
		imp: "com/foo/Bar", pkg: "com/foo", java: true, want: true, // type import
	}, {
		imp: "com/foo/Bar", pkg: "com/foo", java: false, want: false, // trailing match is wrong for Java
	}, {
		imp: "com/foobot/Bar", pkg: "com/foo", java: true, want: false,
	}, {
		imp: "fmt", pkg: "internal/rules", want: false,
	}}
	for _, c := range cases {
		if got := resolves(c.imp, c.pkg, c.java); got != c.want {
			t.Errorf("resolves(%q, %q, java=%v) = %v, want %v", c.imp, c.pkg, c.java, got, c.want)
		}
	}
}

func TestBuildDropsExternalImports(t *testing.T) {
	g := Build([]File{
		{Name: "a/a.go", Pkg: "a", Imports: []Import{{Path: "mod/b", Line: 3}, {Path: "fmt", Line: 4}}},
		{Name: "b/b.go", Pkg: "b"},
	})
	edges := g.Edges()
	if len(edges) != 1 {
		t.Fatalf("edges = %v, want only the internal one", edges)
	}
	want := Edge{From: "a", To: "b", Via: "a/a.go", Line: 3}
	if edges[0] != want {
		t.Errorf("edge = %+v, want %+v", edges[0], want)
	}
}

func TestBuildPrefersLongestPackage(t *testing.T) {
	g := Build([]File{
		{Name: "x/x.go", Pkg: "mod/api", Imports: []Import{{Path: "mod/api/v2", Line: 1}}},
		{Name: "y/y.go", Pkg: "mod/api/v2"},
	})
	edges := g.Edges()
	if len(edges) != 1 || edges[0].To != "mod/api/v2" {
		t.Fatalf("edges = %v, want the most specific package", edges)
	}
}

func TestBuildIgnoresSelfEdges(t *testing.T) {
	g := Build([]File{
		{Name: "a/a.go", Pkg: "mod/a", Imports: []Import{{Path: "mod/a", Line: 1}}},
	})
	if edges := g.Edges(); len(edges) != 0 {
		t.Errorf("edges = %v, want none", edges)
	}
}

func TestBuildResolvesJavaTypeImports(t *testing.T) {
	g := Build([]File{
		{Name: "A.java", Pkg: "com/app", Java: true, Imports: []Import{{Path: "com/store/Repo", Line: 4}}},
		{Name: "B.java", Pkg: "com/store"},
	})
	edges := g.Edges()
	if len(edges) != 1 || edges[0].To != "com/store" || edges[0].Line != 4 {
		t.Fatalf("edges = %v, want com/app → com/store at line 4", edges)
	}
}

func TestCyclesFindsStronglyConnectedComponents(t *testing.T) {
	g := Build([]File{
		{Name: "a", Pkg: "a", Imports: []Import{{Path: "b"}}},
		{Name: "b", Pkg: "b", Imports: []Import{{Path: "c"}}},
		{Name: "c", Pkg: "c", Imports: []Import{{Path: "a"}}},
		{Name: "d", Pkg: "d", Imports: []Import{{Path: "a"}}},
	})
	got := g.Cycles()
	if len(got) != 1 {
		t.Fatalf("cycles = %v, want one component", got)
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got[0], want) {
		t.Errorf("cycle = %v, want %v", got[0], want)
	}
}

// layered is a handler → service → storage policy with nothing else allowed.
func layered() Policy {
	return Policy{
		Layers: []Layer{
			{Name: "handler", Packages: []string{"app/handler"}, MayDependOn: []string{"service"}},
			{Name: "service", Packages: []string{"app/service"}, MayDependOn: []string{"storage"}},
			{Name: "storage", Packages: []string{"app/storage"}},
		},
	}
}

// nodes declares the three packages without creating dependencies.
func nodes() []File {
	return []File{
		{Name: "handler.go", Pkg: "app/handler"},
		{Name: "service.go", Pkg: "app/service"},
		{Name: "storage.go", Pkg: "app/storage"},
	}
}

func TestCheckLayerBoundary(t *testing.T) {
	files := append(nodes(),
		File{Name: "s.go", Pkg: "app/service", Imports: []Import{{Path: "app/handler", Line: 7}}},
		File{Name: "x.go", Pkg: "app/storage", Imports: []Import{{Path: "app/handler", Line: 5}}},
	)
	viol := Check(Build(files), layered())

	byFile := map[string]Violation{}
	for _, v := range viol {
		byFile[v.File] = v
	}
	if len(viol) != 2 {
		t.Fatalf("violations = %+v, want the two upward edges", viol)
	}
	for _, f := range []string{"s.go", "x.go"} {
		v, ok := byFile[f]
		if !ok {
			t.Errorf("%s not reported: %+v", f, viol)
			continue
		}
		if v.RuleID != RuleLayerViolation {
			t.Errorf("%s rule = %q, want %q", f, v.RuleID, RuleLayerViolation)
		}
	}
	if v := byFile["x.go"]; !strings.Contains(v.Message, `layer "storage"`) || !strings.Contains(v.Message, "app/handler") {
		t.Errorf("message = %q, want it to name the layer and the target", v.Message)
	}
}

func TestCheckLeavesUnlayeredCodeAlone(t *testing.T) {
	files := append(nodes(),
		File{Name: "m.go", Pkg: "misc", Imports: []Import{{Path: "app/handler", Line: 1}}},
		File{Name: "i.go", Pkg: "app/handler", Imports: []Import{{Path: "internal/logger", Line: 3}}},
	)
	if viol := Check(Build(files), layered()); len(viol) != 0 {
		t.Errorf("violations = %+v, want none: unlayered code is unconstrained", viol)
	}
}

func TestCheckForbidWinsOverLayers(t *testing.T) {
	p := layered()
	p.Forbids = []Forbid{{From: "app/storage", To: "app/service", Reason: "storage is a leaf"}}
	files := append(nodes(),
		File{Name: "s.go", Pkg: "app/storage", Imports: []Import{{Path: "app/service", Line: 9}}},
	)
	viol := Check(Build(files), p)
	if len(viol) != 1 {
		t.Fatalf("violations = %+v, want exactly one (no double reporting)", viol)
	}
	if viol[0].RuleID != RuleForbiddenEdge {
		t.Errorf("rule = %q, want %q", viol[0].RuleID, RuleForbiddenEdge)
	}
	if !strings.Contains(viol[0].Message, "storage is a leaf") {
		t.Errorf("message = %q, want it to carry the reason", viol[0].Message)
	}
	if viol[0].Line != 9 || viol[0].File != "s.go" {
		t.Errorf("anchor = %s:%d, want s.go:9", viol[0].File, viol[0].Line)
	}
}

func TestCheckReportsCycles(t *testing.T) {
	files := append(nodes(),
		File{Name: "a.go", Pkg: "app/handler", Imports: []Import{{Path: "app/service", Line: 4}}},
		File{Name: "b.go", Pkg: "app/service", Imports: []Import{{Path: "app/handler", Line: 6}}},
	)
	var cycles []Violation
	for _, v := range Check(Build(files), layered()) {
		if v.RuleID == RuleImportCycle {
			cycles = append(cycles, v)
		}
	}
	if len(cycles) != 1 {
		t.Fatalf("cycles = %+v, want one", cycles)
	}
	if !strings.Contains(cycles[0].Message, "app/handler, app/service") {
		t.Errorf("message = %q, want it to list the packages", cycles[0].Message)
	}
	if cycles[0].File != "a.go" {
		t.Errorf("anchor = %s, want the first import in the cycle", cycles[0].File)
	}
}

func TestMayDependOnAcceptsPackagePattern(t *testing.T) {
	p := Policy{Layers: []Layer{
		{Name: "api", Packages: []string{"svc/api"}, MayDependOn: []string{"svc/store"}},
		{Name: "store", Packages: []string{"svc/store"}},
	}}
	files := []File{
		{Name: "a.go", Pkg: "svc/api", Imports: []Import{{Path: "svc/store", Line: 1}}},
		{Name: "s.go", Pkg: "svc/store"},
	}
	if viol := Check(Build(files), p); len(viol) != 0 {
		t.Errorf("violations = %+v, want none: a pattern target should be allowed", viol)
	}
}

func TestSummaryLines(t *testing.T) {
	lines := Summary(layered())
	if len(lines) != 3 {
		t.Fatalf("lines = %v, want one per layer", lines)
	}
	if !strings.Contains(lines[0], "may depend on: service") {
		t.Errorf("line = %q, want the allowed targets", lines[0])
	}
	if !strings.Contains(lines[2], "may depend on: (nothing declared)") {
		t.Errorf("line = %q, want the empty-marker text", lines[2])
	}
	if got := Summary(Policy{}); got != nil {
		t.Errorf("empty policy lines = %v, want nil", got)
	}
}

func TestPolicyEmpty(t *testing.T) {
	if !(Policy{}).Empty() {
		t.Error("the zero policy must be Empty")
	}
	if (Policy{Forbids: []Forbid{{From: "a", To: "b"}}}).Empty() {
		t.Error("a policy with one forbid is not Empty")
	}
}
