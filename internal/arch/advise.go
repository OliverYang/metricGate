package arch

import "fmt"

// This file generates structural repair advice for layer violations.
//
// The pilot report (docs/pilot/conversion-map.md, lesson 5) found that 77 of
// 132 layer violations shared one root cause: a class placed in the wrong
// package (a context holder inside the interceptor package) was referenced
// from everywhere. The dependency direction the checker flags is real, but
// the fix is moving the misplaced package down into a shared layer — not
// rewiring the callers. The advice below reads the graph's fan-in shape to
// tell the three situations apart instead of emitting one generic hint.

// fanIn is the reverse-dependency shape of the graph: per package, how many
// distinct packages depend on it and from how many distinct layers.
type fanIn struct {
	pkgs   map[string]int
	layers map[string]int
}

// computeFanIn folds the edge list into per-package reverse counts. Distinct
// from-layers get their own pass: two importers may share a layer, and
// counting packages instead of layers would overstate cross-layer pull.
func computeFanIn(g *Graph, layerOf map[string]Layer) fanIn {
	f := fanIn{pkgs: map[string]int{}, layers: map[string]int{}}
	seen := map[string]map[string]bool{}     // pkg → distinct from-packages
	layerSet := map[string]map[string]bool{} // pkg → distinct from-layers
	for _, e := range g.Edges() {
		if seen[e.To] == nil {
			seen[e.To] = map[string]bool{}
		}
		if !seen[e.To][e.From] {
			seen[e.To][e.From] = true
			f.pkgs[e.To]++
		}
		if l, ok := layerOf[e.From]; ok {
			if layerSet[e.To] == nil {
				layerSet[e.To] = map[string]bool{}
			}
			if !layerSet[e.To][l.Name] {
				layerSet[e.To][l.Name] = true
				f.layers[e.To]++
			}
		}
	}
	return f
}

// suggestFix returns the structural advice for one layer violation. Both
// branches assume the target is layered: an unlayered target can never
// violate. The heuristics:
//
//  1. Misplaced shared package: the target is depended on from two or more
//     distinct layers. The declared boundary is correct; the package sits on
//     the wrong side of it (the pilot's 77/132 root cause). Suggest moving
//     it to a shared/lower layer.
//  2. Otherwise: a one-off cross-layer pull — suggest declaring the edge if
//     intentional, or relocating the dependent code.
func (m matcher) suggestFix(e Edge) string {
	if m.fanIn.layers[e.To] >= 2 {
		return fmt.Sprintf("suggest: %s is depended on from %d layers — the boundary is right, the package is misplaced; move it (or its shared parts) to a lower shared layer and keep its importers as-is",
			e.To, m.fanIn.layers[e.To])
	}
	return fmt.Sprintf("suggest: if the dependency is intentional, add %q to layer %q's may_depend_on; otherwise move the dependent code into layer %q or behind a port interface",
		e.To, m.layerOf[e.From].Name, m.layerOf[e.To].Name)
}
