// Package repomap ranks a repository's files by how much the rest of the code
// leans on them, using the reference graph and PageRank — the deterministic,
// zero-token answer to "which files should I read first?".
//
// It is deliberately the same shape as aider's repo map: build a graph from
// "file A uses a symbol defined in file B", weight each edge by the use, and
// let the ranking fall out. Nothing here needs a model, a network call or a
// persistent index; it is a pure function of the definitions and use sites the
// parse pass already produced.
//
// The graph is name-based, so a call made through an interface, reflection or
// a dynamically built name is invisible to it. That is the same "ast-only"
// bargain the rest of the tool makes: a ranked list that is useful and honest
// about its blind spot, never a call graph pretending to be complete.
package repomap

import (
	"fmt"
	"sort"
	"strings"

	"metricgate/internal/refs"
	"metricgate/internal/symbols"
)

// Options tunes the ranking. The zero value is not usable; use DefaultOptions.
type Options struct {
	Damping    float64 // PageRank damping factor
	Iterations int     // fixed iteration count, for byte-stable output
	TopSymbols int     // symbols shown per file in the detailed render
}

// DefaultOptions returns the standard ranking parameters.
func DefaultOptions() Options {
	return Options{Damping: 0.85, Iterations: 30, TopSymbols: 3}
}

// normalized fills in sane values for anything left zero.
func (o Options) normalized() Options {
	if o.Damping <= 0 || o.Damping >= 1 {
		o.Damping = 0.85
	}
	if o.Iterations <= 0 {
		o.Iterations = 30
	}
	if o.TopSymbols <= 0 {
		o.TopSymbols = 3
	}
	return o
}

// Symbol is one definition together with how many use sites reference its
// name — the reason it is worth reading.
type Symbol struct {
	Def  symbols.Def `json:"def"`
	Uses int         `json:"uses"`
}

// FileScore is one ranked file.
type FileScore struct {
	File    string  `json:"file"`
	Score   float64 `json:"score"`
	Defs    int     `json:"defs"`
	Uses    int     `json:"uses"`    // use sites located inside this file
	UsedBy  int     `json:"usedBy"`  // files whose symbols this file uses
	Callers int     `json:"callers"` // files that use this file's symbols
	// Risk is the optional static risk score (0..100, see internal/risk).
	// Build never sets it — it is a signal a caller joins in by path — so a
	// zero value means "not supplied" and is left out of the render.
	Risk float64  `json:"risk,omitempty"`
	Top  []Symbol `json:"top,omitempty"`
}

// edge is one weighted, sorted graph edge.
type edge struct {
	to string
	w  float64
}

// graph is the intermediate representation Build ranks over.
type graph struct {
	nodes   []string // sorted file names
	edges   map[string][]edge
	outSum  map[string]float64
	defsBy  map[string][]symbols.Def
	usesIn  map[string]int // file -> use sites located there
	usesBy  map[string]int // symbol name -> use sites anywhere
	callers map[string]int // file -> distinct files that reference it
}

// Build ranks the files covered by the definitions and use sites. Both inputs
// come straight from the parse pass; no second walk is needed.
func Build(defs []symbols.Def, uses []refs.Ref, opt Options) []FileScore {
	g := newGraph(defs, uses)
	rank := pageRank(g, opt.normalized())
	return assemble(g, rank, opt.normalized().TopSymbols)
}

// newGraph builds the reference graph: an edge F->D exists when F uses a name
// that D defines. A name defined in several files splits its weight evenly, so
// a common name does not falsely promote every definition of it.
func newGraph(defs []symbols.Def, uses []refs.Ref) *graph {
	g := &graph{
		edges:   map[string][]edge{},
		outSum:  map[string]float64{},
		defsBy:  map[string][]symbols.Def{},
		usesIn:  map[string]int{},
		usesBy:  map[string]int{},
		callers: map[string]int{},
	}
	defFiles := map[string]map[string]bool{} // name -> set of defining files
	nodes := map[string]bool{}
	for _, d := range defs {
		if d.File == "" {
			continue
		}
		g.defsBy[d.File] = append(g.defsBy[d.File], d)
		nodes[d.File] = true
		if d.Name == "" {
			continue
		}
		if defFiles[d.Name] == nil {
			defFiles[d.Name] = map[string]bool{}
		}
		defFiles[d.Name][d.File] = true
	}
	weights := map[string]map[string]float64{} // from -> to -> weight
	for _, u := range uses {
		if u.File == "" {
			continue
		}
		nodes[u.File] = true
		g.usesIn[u.File]++
		g.usesBy[u.Name]++
		addUses(weights, u, defFiles)
	}
	g.nodes = sortedKeys(nodes)
	finalizeEdges(g, weights)
	return g
}

// addUses records every F->D edge one use site implies, splitting the weight
// across all definitions of the name.
func addUses(weights map[string]map[string]float64, u refs.Ref, defFiles map[string]map[string]bool) {
	targets := defFiles[u.Name]
	if len(targets) == 0 {
		return
	}
	share := 1.0 / float64(len(targets))
	for to := range targets {
		if to == u.File {
			continue
		}
		if weights[u.File] == nil {
			weights[u.File] = map[string]float64{}
		}
		weights[u.File][to] += share
	}
}

// finalizeEdges turns the weight map into sorted edge lists (so the PageRank
// summation order is fixed) and counts each target's distinct callers.
func finalizeEdges(g *graph, weights map[string]map[string]float64) {
	for from, tos := range weights {
		outs := make([]edge, 0, len(tos))
		for to, weight := range tos {
			outs = append(outs, edge{to: to, w: weight})
			g.outSum[from] += weight
			g.callers[to]++
		}
		sort.Slice(outs, func(i, j int) bool { return outs[i].to < outs[j].to })
		g.edges[from] = outs
	}
}

// prState carries the per-iteration PageRank accumulators, so the inner
// distribution step needs only the node it is spreading from.
type prState struct {
	rank    map[string]float64
	next    map[string]float64
	damping float64
	n       float64
}

// pageRank runs a fixed number of iterations over the sorted graph. Sorted
// edge and node order is what makes the result byte-stable across runs.
func pageRank(g *graph, opt Options) map[string]float64 {
	rank := make(map[string]float64, len(g.nodes))
	n := float64(len(g.nodes))
	if n == 0 {
		return rank
	}
	for _, f := range g.nodes {
		rank[f] = 1 / n
	}
	base := (1 - opt.Damping) / n
	for i := 0; i < opt.Iterations; i++ {
		next := make(map[string]float64, len(g.nodes))
		for _, f := range g.nodes {
			next[f] = base
		}
		st := &prState{rank: rank, next: next, damping: opt.Damping, n: n}
		for _, from := range g.nodes {
			g.spread(from, st)
		}
		rank = next
	}
	return rank
}

// spread hands one node's rank to its out-neighbours. A node with no out-edges
// spreads evenly across the graph, so its mass is never silently dropped.
func (g *graph) spread(from string, st *prState) {
	if g.outSum[from] == 0 {
		share := st.damping * st.rank[from] / st.n
		for _, to := range g.nodes {
			st.next[to] += share
		}
		return
	}
	factor := st.damping * st.rank[from] / g.outSum[from]
	for _, e := range g.edges[from] {
		st.next[e.to] += factor * e.w
	}
}

// assemble pairs each file with its score and its most-referenced symbols,
// then sorts by score. Ties break on definition count then path, so the order
// never depends on map iteration.
func assemble(g *graph, rank map[string]float64, topN int) []FileScore {
	out := make([]FileScore, 0, len(g.nodes))
	for _, f := range g.nodes {
		defs := g.defsBy[f]
		out = append(out, FileScore{
			File:    f,
			Score:   rank[f],
			Defs:    len(defs),
			Uses:    g.usesIn[f],
			UsedBy:  len(g.edges[f]),
			Callers: g.callers[f],
			Top:     topSymbols(defs, g.usesBy, topN),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Defs != out[j].Defs {
			return out[i].Defs > out[j].Defs
		}
		return out[i].File < out[j].File
	})
	return out
}

// topSymbols returns the file's definitions ordered by how many use sites
// reference the name, most-referenced first.
func topSymbols(defs []symbols.Def, usesBy map[string]int, n int) []Symbol {
	ranked := make([]Symbol, 0, len(defs))
	for _, d := range defs {
		ranked = append(ranked, Symbol{Def: d, Uses: usesBy[d.Name]})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Uses != ranked[j].Uses {
			return ranked[i].Uses > ranked[j].Uses
		}
		return ranked[i].Def.Line < ranked[j].Def.Line
	})
	if len(ranked) > n {
		ranked = ranked[:n]
	}
	return ranked
}

// Render writes the ranked files, most important first. The section header is
// always emitted; file and symbol lines are added only while the total stays
// within budget, so a budget too small for even the header yields just the
// header. budget <= 0 means no limit.
func Render(scores []FileScore, budget int) string {
	var b strings.Builder
	b.WriteString("## repo map (ranked by reference importance; read these first)\n")
	b.WriteString("# name-based and AST-only: same-named symbols in different files count together\n")
	for i, fs := range scores {
		line := fmt.Sprintf("%d. %s  (score %.4f, defs %d, used-by %d%s)\n",
			i+1, fs.File, fs.Score, fs.Defs, fs.Callers, riskSuffix(fs.Risk))
		if budget > 0 && b.Len()+len(line) > budget {
			break
		}
		b.WriteString(line)
		for _, s := range fs.Top {
			sub := fmt.Sprintf("   - %s  (%d uses)\n", symbolLabel(s.Def), s.Uses)
			if budget > 0 && b.Len()+len(sub) > budget {
				break
			}
			b.WriteString(sub)
		}
	}
	return b.String()
}

// Brief renders just the file ranking as compact lines, for the context pack
// where every byte competes with the constraints.
func Brief(scores []FileScore, maxFiles int) string {
	var b strings.Builder
	b.WriteString("## repo map (read these first; name-based, AST-only)\n")
	for i, fs := range scores {
		if maxFiles > 0 && i >= maxFiles {
			break
		}
		fmt.Fprintf(&b, "- %s (score %.4f, defs %d%s)\n", fs.File, fs.Score, fs.Defs, riskSuffix(fs.Risk))
	}
	return b.String()
}

// riskSuffix renders the optional risk annotation. A zero risk means the
// caller supplied none, so the line is unchanged from a plain ranking.
func riskSuffix(risk float64) string {
	if risk <= 0 {
		return ""
	}
	return fmt.Sprintf(", risk %.1f", risk)
}

// symbolLabel renders a definition as one compact line.
func symbolLabel(d symbols.Def) string {
	sig := d.Signature
	if sig == "" {
		sig = d.Kind + " " + d.Name
	}
	return fmt.Sprintf("%s:%d %s", d.File, d.Line, strings.Join(strings.Fields(sig), " "))
}

// sortedKeys returns a set's members in sorted order.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
