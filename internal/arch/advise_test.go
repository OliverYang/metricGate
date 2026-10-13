package arch

import (
	"strings"
	"testing"
)

func layerTestPolicy() Policy {
	return Policy{Layers: []Layer{
		{Name: "handler", Packages: []string{"app/handler/*"}, MayDependOn: []string{"service"}},
		{Name: "service", Packages: []string{"app/service/*"}},
		{Name: "model", Packages: []string{"app/model/*"}},
		// A misplaced package: declared, but nothing may legally reach it.
		{Name: "interceptor", Packages: []string{"app/interceptor/*"}},
	}}
}

func buildTestGraph(t *testing.T, edges [][2]string) *Graph {
	t.Helper()
	var files []File
	pkgs := map[string]bool{}
	for _, e := range edges {
		pkgs[e[0]], pkgs[e[1]] = true, true
	}
	for p := range pkgs {
		files = append(files, File{Pkg: p})
	}
	g := Build(files)
	// Build derives edges from imports; for a unit test the two-package shape
	// is all the advice needs, so edges are injected directly.
	for _, e := range edges {
		g.edges = append(g.edges, Edge{From: e[0], To: e[1], Via: e[0] + "/x.go", Line: 3})
	}
	sortEdges(g.edges)
	return g
}

// TestSuggestMisplacedSharedPackage covers the pilot's 77/132 root cause: a
// package referenced from several layers. The boundary is right; the package
// should move, not the callers.
func TestSuggestMisplacedSharedPackage(t *testing.T) {
	g := buildTestGraph(t, [][2]string{
		{"app/handler/web", "app/interceptor/ctx"},
		{"app/service/user", "app/interceptor/ctx"},
	})
	out := Check(g, layerTestPolicy())
	if len(out) == 0 {
		t.Fatal("expected a violation for handler → interceptor")
	}
	msg := out[0].Message
	if !strings.Contains(msg, "misplaced") || !strings.Contains(msg, "move it (or its shared parts) to a lower shared layer") {
		t.Fatalf("advice missing misplaced-package suggestion: %s", msg)
	}
}

// TestSuggestOneOffDependency covers the non-shared case: a single cross-layer
// pull is more likely an undeclared edge than a misplaced package.
func TestSuggestOneOffDependency(t *testing.T) {
	g := buildTestGraph(t, [][2]string{
		{"app/handler/web", "app/model/order"},
	})
	out := Check(g, layerTestPolicy())
	if len(out) == 0 {
		t.Fatal("expected a violation for handler → model")
	}
	msg := out[0].Message
	if !strings.Contains(msg, `add "app/model/order" to layer "handler"'s may_depend_on`) {
		t.Fatalf("advice missing may_depend_on suggestion: %s", msg)
	}
	if strings.Contains(msg, "misplaced") {
		t.Fatalf("single importer misclassified as misplaced: %s", msg)
	}
}

// TestAdviceNotOnLegalEdges makes sure the advice machinery never mutates
// messages on edges the policy allows.
func TestAdviceNotOnLegalEdges(t *testing.T) {
	p := layerTestPolicy()
	p.Layers[1].MayDependOn = []string{"interceptor"} // service → interceptor is legal
	g := buildTestGraph(t, [][2]string{
		{"app/handler/web", "app/service/user"},
		{"app/service/user", "app/interceptor/ctx"},
	})
	out := Check(g, p)
	// service → interceptor is unlayered-importer? service is layered and
	// interceptor is not, so no violation there; handler → service is legal.
	for _, v := range out {
		if strings.Contains(v.Message, "suggest") {
			t.Fatalf("legal edge carried advice: %+v", v)
		}
	}
}
