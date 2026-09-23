package arch

import "sort"

// Import is one import statement of a file, with its 1-based line so a
// violation can be anchored where it was written.
type Import struct {
	Path string
	Line int
}

// File is the dependency-relevant part of one source file: the package it
// belongs to and what it imports.
type File struct {
	Name string // path, for reporting
	Pkg  string // package the file belongs to
	// Java marks a Java file: its imports name a type inside a package, so
	// resolution runs on the leading segments instead of the trailing ones.
	Java    bool
	Imports []Import
}

// Edge is a resolved dependency between two packages of this run.
type Edge struct {
	From, To string
	Via      string // file that declares the import
	Line     int
}

// Graph is the package dependency graph of a run.
type Graph struct {
	nodes map[string]bool
	edges []Edge
}

// Build resolves every import against the set of packages seen in the run:
// imports of third-party or standard-library code are not nodes and drop out,
// which is what makes the graph about *our* architecture.
func Build(files []File) *Graph {
	g := &Graph{nodes: map[string]bool{}}
	for _, f := range files {
		if f.Pkg != "" {
			g.nodes[f.Pkg] = true
		}
	}
	idx := newPkgIndex(g.Packages())

	for _, f := range files {
		for _, imp := range f.Imports {
			to := idx.resolve(imp.Path, f.Java)
			if to == "" || to == f.Pkg {
				continue
			}
			g.edges = append(g.edges, Edge{From: f.Pkg, To: to, Via: f.Name, Line: imp.Line})
		}
	}
	sortEdges(g.edges)
	return g
}

// pkgIndex buckets the known packages by first and last segment, so an import
// only competes with the few candidates that can possibly match it. Without
// this, a large repository pays packages × imports in segment comparisons.
type pkgIndex struct {
	byFirst map[string][]string
	byLast  map[string][]string
	segs    map[string][]string
}

func newPkgIndex(pkgs []string) *pkgIndex {
	ix := &pkgIndex{
		byFirst: map[string][]string{},
		byLast:  map[string][]string{},
		segs:    map[string][]string{},
	}
	for _, p := range pkgs {
		ps := segments(p)
		if len(ps) == 0 {
			continue
		}
		ix.segs[p] = ps
		ix.byFirst[ps[0]] = append(ix.byFirst[ps[0]], p)
		ix.byLast[ps[len(ps)-1]] = append(ix.byLast[ps[len(ps)-1]], p)
	}
	return ix
}

// resolve returns the longest known package an import path points at, or "".
func (ix *pkgIndex) resolve(imp string, java bool) string {
	is := segments(imp)
	if len(is) == 0 {
		return ""
	}
	cands := ix.byLast[is[len(is)-1]]
	if java {
		cands = ix.byFirst[is[0]]
	}
	best := ""
	for _, p := range cands {
		if len(p) <= len(best) {
			continue
		}
		if matchDir(is, ix.segs[p], java) {
			best = p
		}
	}
	return best
}

func sortEdges(edges []Edge) {
	sort.SliceStable(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		if a.Via != b.Via {
			return a.Via < b.Via
		}
		return a.Line < b.Line
	})
}

// Packages lists the graph's packages in stable order.
func (g *Graph) Packages() []string {
	out := make([]string, 0, len(g.nodes))
	for p := range g.nodes {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Edges lists the resolved internal dependencies in stable order.
func (g *Graph) Edges() []Edge { return g.edges }

// Cycles returns the strongly connected components with more than one member.
// A single package is never reported: a self-edge is a resolution artifact, not
// a cycle.
func (g *Graph) Cycles() [][]string {
	adj := map[string][]string{}
	for _, e := range g.edges {
		if !contains(adj[e.From], e.To) {
			adj[e.From] = append(adj[e.From], e.To)
		}
	}
	t := newTarjan()
	t.run(g.Packages(), adj)

	var out [][]string
	for _, comp := range t.components {
		if len(comp) > 1 {
			sort.Strings(comp)
			out = append(out, comp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
