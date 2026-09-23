package arch

// Tarjan's strongly-connected-components algorithm, kept local so the graph
// layer has no dependency beyond the standard library. Cycles are the single
// most expensive architectural fact to discover after the fact, and the
// cheapest to prevent: one pass over the package graph finds them all.

type tarjan struct {
	index      int
	stack      []string
	onStack    map[string]bool
	number     map[string]int
	low        map[string]int
	components [][]string
}

func newTarjan() *tarjan {
	return &tarjan{
		onStack: map[string]bool{},
		number:  map[string]int{},
		low:     map[string]int{},
	}
}

func (t *tarjan) run(nodes []string, adj map[string][]string) {
	for _, n := range nodes {
		if _, seen := t.number[n]; !seen {
			t.visit(n, adj)
		}
	}
}

func (t *tarjan) visit(n string, adj map[string][]string) {
	t.number[n], t.low[n] = t.index, t.index
	t.index++
	t.stack = append(t.stack, n)
	t.onStack[n] = true

	for _, m := range adj[n] {
		if !t.seen(m) {
			t.visit(m, adj)
			t.low[n] = imin(t.low[n], t.low[m])
			continue
		}
		if t.onStack[m] {
			t.low[n] = imin(t.low[n], t.number[m])
		}
	}

	if t.low[n] == t.number[n] {
		var comp []string
		for {
			last := len(t.stack) - 1
			m := t.stack[last]
			t.stack = t.stack[:last]
			t.onStack[m] = false
			comp = append(comp, m)
			if m == n {
				break
			}
		}
		t.components = append(t.components, comp)
	}
}

// seen distinguishes "not yet numbered" from "numbered as 0".
func (t *tarjan) seen(n string) bool {
	_, ok := t.number[n]
	return ok
}

func imin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
