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
//
// Pattern matching is done once per (policy entry × package), never per edge:
// a large repository has orders of magnitude more edges than packages, and the
// per-edge work is then two map lookups.
func Check(g *Graph, p Policy) []Violation {
	m := newMatcher(p, g.Packages())
	var out []Violation
	for _, e := range g.Edges() {
		if v, blocked := m.forbidden(e); blocked {
			out = append(out, v)
			continue // one finding per illegal edge: forbid supersedes layering
		}
		if v, blocked := m.layerViolation(e); blocked {
			out = append(out, v)
		}
	}
	out = append(out, cycleViolations(g)...)
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

// matcher holds the package sets a policy induces over one graph.
type matcher struct {
	forbids []compiledForbid
	layerOf map[string]Layer           // package → its layer (absent: unlayered)
	allowed map[string]map[string]bool // layer name → packages it may depend on
}

type compiledForbid struct {
	from, to map[string]bool
	reason   string
}

func newMatcher(p Policy, pkgs []string) matcher {
	m := matcher{
		forbids: compileForbids(p.Forbids, pkgs),
		layerOf: AssignLayers(p.Layers, pkgs),
		allowed: map[string]map[string]bool{},
	}
	m.compileAllowed(p.Layers, pkgs)
	return m
}

// compileForbids turns every forbid entry into the package sets it names.
func compileForbids(forbids []Forbid, pkgs []string) []compiledForbid {
	out := make([]compiledForbid, 0, len(forbids))
	for _, f := range forbids {
		out = append(out, compiledForbid{
			from:   matching(f.From, pkgs),
			to:     matching(f.To, pkgs),
			reason: f.Reason,
		})
	}
	return out
}

// matching returns the packages one pattern names.
func matching(pattern string, pkgs []string) map[string]bool {
	set := map[string]bool{}
	for _, pkg := range pkgs {
		if MatchesAny([]string{pattern}, pkg) {
			set[pkg] = true
		}
	}
	return set
}

// AssignLayers gives every package its first matching layer. A package absent
// from the result belongs to no layer, and is therefore unconstrained by
// layering (see the note on Layer).
//
// It is exported because a policy audit has to answer "which packages did this
// declaration actually cover?" — and that question must be answered by the same
// rule the checker uses, not by a second implementation that could disagree
// about overlapping patterns.
func AssignLayers(layers []Layer, pkgs []string) map[string]Layer {
	out := map[string]Layer{}
	for _, l := range layers {
		for _, pkg := range pkgs {
			if _, seen := out[pkg]; !seen && MatchesAny(l.Packages, pkg) {
				out[pkg] = l
			}
		}
	}
	return out
}

// compileAllowed precomputes, per layer, the packages it may depend on.
func (m matcher) compileAllowed(layers []Layer, pkgs []string) {
	for _, l := range layers {
		set := map[string]bool{}
		for _, pkg := range pkgs {
			if m.legalTarget(l, pkg) {
				set[pkg] = true
			}
		}
		m.allowed[l.Name] = set
	}
}

// legalTarget reports whether layer `from` may depend on package `pkg`: its own
// layer, a layer named in may_depend_on, or a package pattern listed there.
func (m matcher) legalTarget(from Layer, pkg string) bool {
	to, ok := m.layerOf[pkg]
	if ok && to.Name == from.Name {
		return true
	}
	for _, dep := range from.MayDependOn {
		if ok && dep == to.Name {
			return true
		}
		if MatchesAny([]string{dep}, pkg) {
			return true
		}
		if named, ok := m.layerOf[dep]; ok && to.Name == named.Name {
			return true
		}
	}
	return false
}

func (m matcher) forbidden(e Edge) (Violation, bool) {
	for _, f := range m.forbids {
		if !f.from[e.From] || !f.to[e.To] {
			continue
		}
		msg := fmt.Sprintf("%s must not depend on %s", e.From, e.To)
		if f.reason != "" {
			msg += ": " + f.reason
		}
		return atEdge(e, RuleForbiddenEdge, msg), true
	}
	return Violation{}, false
}

func (m matcher) layerViolation(e Edge) (Violation, bool) {
	from, ok := m.layerOf[e.From]
	if !ok {
		return Violation{}, false
	}
	to, ok := m.layerOf[e.To]
	if !ok || to.Name == from.Name || m.allowed[from.Name][e.To] {
		return Violation{}, false
	}
	msg := fmt.Sprintf("layer %q (%s) may not depend on layer %q (%s); allowed: [%s]",
		from.Name, e.From, to.Name, e.To, strings.Join(from.MayDependOn, ", "))
	// The finding must state the correct fix and exclude the wrong one
	// (pilot conclusion): the port-adapter idiom is the verified repair path
	// (R2 U04: the model re-derived it twice from findings alone), copying
	// code instead is the failure mode to name explicitly.
	msg += " — fix: define a port (interface) in layer \"" + from.Name +
		"\" and let layer \"" + to.Name + "\" implement it, injected at the composition root; do not copy code to bypass layering"
	return atEdge(e, RuleLayerViolation, msg), true
}

func atEdge(e Edge, rule, msg string) Violation {
	return Violation{RuleID: rule, File: e.Via, Line: e.Line, Message: msg}
}

// cycleViolations reports one finding per package cycle, anchored at the first
// import inside the cycle so the report points at real code.
func cycleViolations(g *Graph) []Violation {
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
