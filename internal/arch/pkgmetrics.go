// Package metrics over the dependency graph: Robert C. Martin's OO design
// metrics (1994) computed from the same graph the architecture pass builds.
// All measures are per package; they flow into the metric snapshots (the
// trend store) and, where the policy asks, into the gate.
package arch

import (
	"fmt"
	"sort"
)

// RuleSdp is the finding id for a Stable Dependencies Principle violation.
const RuleSdp = "sdp-violation"

// PkgMetrics is the Martin metric set of one package.
type PkgMetrics struct {
	Pkg        string
	Ca         int     // afferent coupling: packages that depend on this one
	Ce         int     // efferent coupling: packages this one depends on
	I          float64 // instability = Ce/(Ca+Ce); 0 for isolated packages
	A          float64 // abstractness: abstract types / total types
	D          float64 // distance from the main sequence: |A + I - 1|
	SdpViolate bool    // depends on a less stable package (I(from) > I(to))
	SdpTo      string  // the offending dependency target
}

// TypeKinds is the type census a caller contributes for abstractness:
// per package, how many abstract and total type declarations it has.
type TypeKinds struct {
	Abstract int
	Total    int
}

// PkgOptions tunes the package-metrics pass. The zero value keeps Martin's
// strict reading, so existing callers are unaffected.
type PkgOptions struct {
	// SDPTolerance is the minimum instability gap that counts as a violation:
	// an edge from p to q is reported only when I(q)-I(p) > SDPTolerance.
	// Instability is a ratio over integer coupling counts, so its resolution
	// is about 1/(Ca+Ce) and gaps below that are rounding noise.
	SDPTolerance float64
}

// ComputePkgMetrics computes the Martin set over the graph. kinds maps a
// package to its type census (an absent package measures A=0). The result
// is sorted by package name so reports are stable.
func ComputePkgMetrics(g *Graph, kinds map[string]TypeKinds, opt PkgOptions) []PkgMetrics {
	efferent := map[string]map[string]bool{}
	afferent := map[string]map[string]bool{}
	for _, p := range g.Packages() {
		efferent[p] = map[string]bool{}
		afferent[p] = map[string]bool{}
	}
	for _, e := range g.Edges() {
		efferent[e.From][e.To] = true
		afferent[e.To][e.From] = true
	}

	instability := map[string]float64{}
	out := make([]PkgMetrics, 0, len(efferent))
	for _, p := range g.Packages() {
		m := PkgMetrics{
			Pkg: p,
			Ca:  len(afferent[p]),
			Ce:  len(efferent[p]),
		}
		if denom := m.Ca + m.Ce; denom > 0 {
			m.I = float64(m.Ce) / float64(denom)
		}
		if k := kinds[p]; k.Total > 0 {
			m.A = float64(k.Abstract) / float64(k.Total)
		}
		m.D = abs64(m.A + m.I - 1)
		instability[p] = m.I
		out = append(out, m)
	}

	// SDP: a dependency must point at a package at least as stable. Gaps at or
	// below the tolerance are rounding noise and are not reported.
	for i := range out {
		m := &out[i]
		if to, ok := sdpTarget(m.Pkg, efferent[m.Pkg], instability, opt.SDPTolerance); ok {
			m.SdpViolate = true
			m.SdpTo = to
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pkg < out[j].Pkg })
	return out
}

// sdpTarget picks the dependency a package is reported against: the least
// stable neighbour whose instability exceeds this package's by more than the
// tolerance. Ties are broken by package name, so the reported edge does not
// depend on Go's randomized map iteration.
func sdpTarget(pkg string, efferent map[string]bool, instability map[string]float64, tol float64) (string, bool) {
	best := ""
	for to := range efferent {
		if instability[to]-instability[pkg] <= tol {
			continue
		}
		if best == "" || instability[to] > instability[best] ||
			(instability[to] == instability[best] && to < best) {
			best = to
		}
	}
	return best, best != ""
}

// Violations renders SDP violations as architecture-style violations, so
// they ride the same finding pipeline. One violation per offending edge,
// anchored on the first file that declares the dependency.
func SdpViolations(g *Graph, ms []PkgMetrics) []Violation {
	byName := map[string]PkgMetrics{}
	for _, m := range ms {
		byName[m.Pkg] = m
	}
	seen := map[string]bool{}
	var out []Violation
	for _, e := range g.Edges() {
		from, okFrom := byName[e.From]
		if !okFrom || !from.SdpViolate {
			continue
		}
		if from.SdpTo != e.To {
			continue // report only the chosen (least stable) target
		}
		key := e.From + "->" + e.To
		if seen[key] {
			continue
		}
		seen[key] = true
		line := e.Line
		if line < 1 {
			line = 1
		}
		out = append(out, Violation{
			RuleID: RuleSdp,
			File:   e.Via,
			Line:   line,
			Message: fmt.Sprintf("package %q (I=%.2f) depends on less stable %q (I=%.2f); stable-dependencies principle — the reuse is fine, the direction is not: invert the edge behind an interface, or move the shared part down. Do not copy the code instead.",
				e.From, from.I, e.To, byName[e.To].I),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// SummaryPkg renders the design-debt lines for the context pack: packages
// furthest from the main sequence first (D is a signal, not a gate).
func SummaryPkg(ms []PkgMetrics, max int) []string {
	sorted := append([]PkgMetrics(nil), ms...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].D > sorted[j].D })
	lines := make([]string, 0, max)
	for i, m := range sorted {
		if i >= max {
			break
		}
		lines = append(lines, fmt.Sprintf(
			"package %s: Ca=%d Ce=%d I=%.2f A=%.2f D=%.2f — keep %s stable",
			m.Pkg, m.Ca, m.Ce, m.I, m.A, m.D, depSummary(m)))
	}
	return lines
}

func depSummary(m PkgMetrics) string {
	switch {
	case m.I >= 0.9:
		return "it is a leaf adapter"
	case m.I <= 0.1:
		return "it is core: nothing below it"
	default:
		return "changes propagate through it"
	}
}

func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
