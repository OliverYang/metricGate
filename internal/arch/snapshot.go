package arch

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Snapshot and diff of the package dependency graph (arch-guard WP3).
//
// Where api snapshot freezes the exported surface, this freezes the
// dependency DIRECTIONS: the set of internal edges the current tree has.
// A diff against a blessed snapshot answers the question the per-run gate
// cannot — not "is this edge illegal" (that is arch.Check) but "did the
// shape of the architecture change at all". Every answer is a human
// decision: update the snapshot deliberately (bless the new shape) or
// revert the change.
//
// The snapshot aggregates per (from, to): the Via files and import lines
// are deliberately not stored. Drift gating is about directions, and
// line-level churn (moving an import up or down) would make the snapshot
// noisy to bless and review. The edge count is recorded for audit only;
// Compare ignores it.

// SnapshotEdge is one resolved internal dependency direction. Count is how
// many files declare it (audit information; not compared).
type SnapshotEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
}

// Snapshot is the frozen edge set of one run, plus the package nodes (a
// package with no internal edges is still part of the shape).
type Snapshot struct {
	Version  int            `json:"version"`
	Packages []string       `json:"packages"`
	Edges    []SnapshotEdge `json:"edges"`
}

// snapshotVersion bumps when the snapshot shape changes incompatibly.
const snapshotVersion = 1

// Capture freezes the graph's directions. Deterministic: identical graphs
// produce byte-identical snapshots, so the file can live in the repo.
func Capture(g *Graph) *Snapshot {
	counts := map[[2]string]int{}
	for _, e := range g.Edges() {
		counts[[2]string{e.From, e.To}]++
	}
	edges := make([]SnapshotEdge, 0, len(counts))
	for k, n := range counts {
		edges = append(edges, SnapshotEdge{From: k[0], To: k[1], Count: n})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	return &Snapshot{Version: snapshotVersion, Packages: g.Packages(), Edges: edges}
}

// Save writes the snapshot as stable, indented JSON.
func (s *Snapshot) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// LoadSnapshot reads a snapshot written by Save, rejecting shapes this
// binary cannot compare.
func LoadSnapshot(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse snapshot: %w", err)
	}
	if s.Version != snapshotVersion {
		return nil, fmt.Errorf("snapshot version %d not supported (want %d): re-run arch snapshot", s.Version, snapshotVersion)
	}
	return &s, nil
}

// EdgeDrift is one direction that appeared or disappeared.
type EdgeDrift struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"` // added | removed
	OldN int    `json:"old_count,omitempty"`
	NewN int    `json:"new_count,omitempty"`
}

// Diff is the direction drift between two snapshots.
type Diff struct {
	Added    []EdgeDrift `json:"added,omitempty"`
	Removed  []EdgeDrift `json:"removed,omitempty"`
	PkgsNew  []string    `json:"packages_added,omitempty"`
	PkgsGone []string    `json:"packages_removed,omitempty"`
}

// Drifted reports whether the architecture shape changed at all.
func (d Diff) Drifted() bool {
	return len(d.Added) > 0 || len(d.Removed) > 0 || len(d.PkgsNew) > 0 || len(d.PkgsGone) > 0
}

// Compare diffs old against new by direction only: an edge present in both
// snapshots is unchanged no matter how many files declare it.
func Compare(oldSnap, newSnap *Snapshot) Diff {
	oldEdges := edgeSet(oldSnap)
	newEdges := edgeSet(newSnap)
	var d Diff
	for _, e := range newSnap.Edges {
		k := [2]string{e.From, e.To}
		if _, ok := oldEdges[k]; !ok {
			d.Added = append(d.Added, EdgeDrift{From: e.From, To: e.To, Kind: "added", NewN: e.Count})
		}
	}
	for _, e := range oldSnap.Edges {
		k := [2]string{e.From, e.To}
		if _, ok := newEdges[k]; !ok {
			d.Removed = append(d.Removed, EdgeDrift{From: e.From, To: e.To, Kind: "removed", OldN: e.Count})
		}
	}
	d.PkgsNew, d.PkgsGone = setDiff(oldSnap.Packages, newSnap.Packages)
	sortDrift(&d)
	return d
}

func edgeSet(s *Snapshot) map[[2]string]SnapshotEdge {
	m := make(map[[2]string]SnapshotEdge, len(s.Edges))
	for _, e := range s.Edges {
		m[[2]string{e.From, e.To}] = e
	}
	return m
}

// setDiff returns the members of b not in a, and of a not in b (both sorted;
// snapshot package lists are already sorted, and LoadSnapshot input sorted
// by Capture — but a hand-edited file may not be, so sort defensively).
func setDiff(a, b []string) (newInB, goneFromA []string) {
	as := map[string]bool{}
	for _, x := range a {
		as[x] = true
	}
	for _, x := range b {
		if !as[x] {
			newInB = append(newInB, x)
		}
	}
	bs := map[string]bool{}
	for _, x := range b {
		bs[x] = true
	}
	for _, x := range a {
		if !bs[x] {
			goneFromA = append(goneFromA, x)
		}
	}
	sort.Strings(newInB)
	sort.Strings(goneFromA)
	return newInB, goneFromA
}

func sortDrift(d *Diff) {
	less := func(ds []EdgeDrift) func(i, j int) bool {
		return func(i, j int) bool {
			if ds[i].From != ds[j].From {
				return ds[i].From < ds[j].From
			}
			return ds[i].To < ds[j].To
		}
	}
	sort.Slice(d.Added, less(d.Added))
	sort.Slice(d.Removed, less(d.Removed))
}
