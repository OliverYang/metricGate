package arch

import (
	"math"
	"strings"
	"testing"
)

func graph(edges ...[2]string) *Graph {
	var files []File
	pkgs := map[string]bool{}
	for _, e := range edges {
		pkgs[e[0]], pkgs[e[1]] = true, true
	}
	for p := range pkgs {
		files = append(files, File{Name: p + "/x.go", Pkg: p})
	}
	for _, e := range edges {
		files = append(files, File{Name: e[0] + "/x.go", Pkg: e[0], Imports: []Import{{Path: "mod/" + e[1], Line: 3}}})
	}
	return Build(files)
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestMartinMetricsChain(t *testing.T) {
	// handler -> service -> store: store is the stable core.
	g := graph([2]string{"handler", "service"}, [2]string{"service", "store"})
	ms := ComputePkgMetrics(g, nil)
	byPkg := map[string]PkgMetrics{}
	for _, m := range ms {
		byPkg[m.Pkg] = m
	}
	if !approx(byPkg["store"].I, 0) || byPkg["store"].Ca != 1 || byPkg["store"].Ce != 0 {
		t.Errorf("store = %+v, want I=0 Ca=1 Ce=0", byPkg["store"])
	}
	if !approx(byPkg["handler"].I, 1) || byPkg["handler"].Ca != 0 || byPkg["handler"].Ce != 1 {
		t.Errorf("handler = %+v, want I=1", byPkg["handler"])
	}
	if !approx(byPkg["service"].I, 0.5) {
		t.Errorf("service I = %v, want 0.5", byPkg["service"].I)
	}
	// Abstractness with a full census: store all concrete, service half.
	kinds := map[string]TypeKinds{
		"store":   {Abstract: 0, Total: 4},
		"service": {Abstract: 1, Total: 2},
	}
	ms = ComputePkgMetrics(g, kinds)
	for _, m := range ms {
		if m.Pkg == "service" && !approx(m.A, 0.5) {
			t.Errorf("service A = %v, want 0.5", m.A)
		}
		if m.Pkg == "service" && !approx(m.D, math.Abs(0.5+0.5-1)) {
			t.Errorf("service D = %v", m.D)
		}
	}
}

func TestMartinIsolatedPackage(t *testing.T) {
	// A package with no edges at all: I=0 by definition (most stable).
	g := Build([]File{{Name: "solo/x.go", Pkg: "solo"}})
	ms := ComputePkgMetrics(g, nil)
	if len(ms) != 1 || ms[0].Pkg != "solo" || ms[0].I != 0 || ms[0].D != 1 {
		t.Errorf("isolated = %+v, want I=0 D=1", ms)
	}
}

func TestSdpViolationDirection(t *testing.T) {
	// Three users make u stable (I=0.25); u depends on v, which churns
	// against two leaves of its own (I=2/3): a stable package depending
	// on an unstable one violates the stable-dependencies principle.
	g := graph(
		[2]string{"p1", "u"}, [2]string{"p2", "u"}, [2]string{"p3", "u"},
		[2]string{"u", "v"},
		[2]string{"v", "v1"}, [2]string{"v", "v2"},
	)
	ms := ComputePkgMetrics(g, nil)
	var u PkgMetrics
	for _, m := range ms {
		if m.Pkg == "u" {
			u = m
		}
	}
	if !approx(u.I, 0.25) {
		t.Fatalf("u I = %v, want 0.25", u.I)
	}
	if !u.SdpViolate || u.SdpTo != "v" {
		t.Errorf("u SDP = %v, want violation toward v", u)
	}
	vs := SdpViolations(g, ms)
	if len(vs) != 1 || vs[0].RuleID != RuleSdp {
		t.Fatalf("violations = %v", vs)
	}
	if vs[0].File != "u/x.go" || vs[0].Line != 3 {
		t.Errorf("violation anchor = %s:%d, want u/x.go:3", vs[0].File, vs[0].Line)
	}
}

func TestSdpNoViolationDownhill(t *testing.T) {
	// handler (I=1) depending on core (I=0) is the legal direction.
	g := graph([2]string{"handler", "core"})
	ms := ComputePkgMetrics(g, nil)
	vs := SdpViolations(g, ms)
	if len(vs) != 0 {
		t.Errorf("downhill dependency flagged: %v", vs)
	}
}

func TestSummaryPkgOrdersByD(t *testing.T) {
	g := graph([2]string{"handler", "service"}, [2]string{"service", "store"})
	kinds := map[string]TypeKinds{
		"handler": {Abstract: 0, Total: 3},
		"service": {Abstract: 0, Total: 3},
		"store":   {Abstract: 0, Total: 3},
	}
	lines := SummaryPkg(ComputePkgMetrics(g, kinds), 3)
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}
	if !strings.Contains(lines[0], "store") || !strings.Contains(lines[1], "service") || !strings.Contains(lines[2], "handler") {
		t.Errorf("order = %v", lines)
	}
}
