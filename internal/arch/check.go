package arch

import (
	"fmt"
	"sort"
	"strings"
)

// Rule identifiers reported for architecture findings.
const (
	// RuleLayerViolation is an edge across a declared layer boundary.
	RuleLayerViolation = "layer-violation"
	// RuleForbiddenEdge is an edge matched by an explicit forbid entry.
	RuleForbiddenEdge = "forbidden-edge"
	// RuleImportCycle is a strongly connected component of packages.
	RuleImportCycle = "import-cycle"
)

// Layer is a named group of packages with a declared set of legal
// dependencies. A package that belongs to no layer is unconstrained, and so is
// a dependency on code outside every layer: teams can describe the part of the
// architecture they care about without tripping over the rest.
type Layer struct {
	Name        string   `yaml:"name"`
	Packages    []string `yaml:"packages"`                // patterns: "internal/handler", "internal/*", "internal/**"
	MayDependOn []string `yaml:"may_depend_on,omitempty"` // layer names and/or package patterns
}

// Forbid is one explicitly banned dependency direction.
type Forbid struct {
	From   string `yaml:"from"`
	To     string `yaml:"to"`
	Reason string `yaml:"reason,omitempty"`
}

// Policy is the architecture section of the constraint model.
type Policy struct {
	Layers  []Layer
	Forbids []Forbid
}

// Empty reports whether the policy describes nothing, so callers can skip the
// whole graph pass.
func (p Policy) Empty() bool { return len(p.Layers) == 0 && len(p.Forbids) == 0 }

// Violation is one architecture finding, anchored at the import that caused it.
type Violation struct {
	RuleID  string
	File    string
	Line    int
	Message string
}

// Check walks every internal dependency and reports edges the policy forbids,
// plus any import cycle in the graph.
func Check(g *Graph, p Policy) []Violation {
	var out []Violation
	for _, e := range g.Edges() {
		if v, blocked := checkForbid(e, p.Forbids); blocked {
			out = append(out, v)
			continue
		}
		if v, blocked := checkLayer(e, p); blocked {
			out = append(out, v)
		}
	}
	out = append(out, checkCycles(g)...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].RuleID < out[j].RuleID
	})
	return out
}

func checkForbid(e Edge, forbids []Forbid) (Violation, bool) {
	for _, f := range forbids {
		if !MatchesAny([]string{f.From}, e.From) || !MatchesAny([]string{f.To}, e.To) {
			continue
		}
		msg := fmt.Sprintf("%s must not depend on %s", e.From, e.To)
		if f.Reason != "" {
			msg += ": " + f.Reason
		}
		return atEdge(e, RuleForbiddenEdge, msg), true
	}
	return Violation{}, false
}

func checkLayer(e Edge, p Policy) (Violation, bool) {
	from, ok := p.layerOf(e.From)
	if !ok {
		return Violation{}, false
	}
	to, ok := p.layerOf(e.To)
	if !ok || to.Name == from.Name {
		return Violation{}, false
	}
	if p.allowed(from, to, e.To) {
		return Violation{}, false
	}
	msg := fmt.Sprintf("layer %q (%s) may not depend on layer %q (%s); allowed: [%s]",
		from.Name, e.From, to.Name, e.To, strings.Join(from.MayDependOn, ", "))
	return atEdge(e, RuleLayerViolation, msg), true
}

func atEdge(e Edge, rule, msg string) Violation {
	return Violation{RuleID: rule, File: e.Via, Line: e.Line, Message: msg}
}

// allowed reports whether layer `from` may depend on layer `to`, named either
// by layer name or by a package pattern matching the target package.
func (p Policy) allowed(from, to Layer, toPkg string) bool {
	for _, dep := range from.MayDependOn {
		if dep == to.Name || MatchesAny([]string{dep}, toPkg) {
			return true
		}
		if l, ok := p.layerOf(dep); ok && l.Name == to.Name {
			return true
		}
	}
	return false
}

// layerOf finds the layer a package belongs to (first match wins).
func (p Policy) layerOf(pkg string) (Layer, bool) {
	for _, l := range p.Layers {
		if MatchesAny(l.Packages, pkg) {
			return l, true
		}
	}
	return Layer{}, false
}

// checkCycles reports one finding per package cycle, anchored at the first
// import inside the cycle so the report points at real code.
func checkCycles(g *Graph) []Violation {
	var out []Violation
	for _, comp := range g.Cycles() {
		in := map[string]bool{}
		for _, p := range comp {
			in[p] = true
		}
		v := Violation{
			RuleID:  RuleImportCycle,
			Message: "package cycle: " + strings.Join(comp, ", "),
		}
		for _, e := range g.Edges() {
			if in[e.From] && in[e.To] {
				v.File, v.Line = e.Via, e.Line
				break
			}
		}
		out = append(out, v)
	}
	return out
}

// Summary renders the policy's architecture constraints as context-pack lines,
// so a prompt states exactly the dependency rules the gate will enforce.
func Summary(p Policy) []string {
	if p.Empty() {
		return nil
	}
	var lines []string
	for _, l := range p.Layers {
		deps := "(nothing declared)"
		if len(l.MayDependOn) > 0 {
			deps = strings.Join(l.MayDependOn, " ")
		}
		lines = append(lines, "layer "+l.Name+" ("+strings.Join(l.Packages, " ")+") may depend on: "+deps)
	}
	for _, f := range p.Forbids {
		line := "forbid " + f.From + " -> " + f.To
		if f.Reason != "" {
			line += ": " + f.Reason
		}
		lines = append(lines, line)
	}
	return lines
}
