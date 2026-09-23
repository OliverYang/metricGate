// Package diff is the pure half of -diff mode: the line-range arithmetic that
// decides whether a finding belongs to changed code.
//
// It deliberately knows nothing about git. Producing the ranges is the job of
// internal/git, an adapter that shells out; keeping the decision logic here
// means the checker core depends on arithmetic, not on a process boundary.
package diff

// Range is an inclusive 1-based line range of ADDED lines in the current
// working tree.
type Range struct {
	Start, End int
}

// Intersects reports whether [start,end] overlaps any of the ranges.
func Intersects(ranges []Range, start, end int) bool {
	for _, r := range ranges {
		if start <= r.End && r.Start <= end {
			return true
		}
	}
	return false
}

// Len sums the number of lines covered by the ranges.
func Len(ranges []Range) int {
	n := 0
	for _, r := range ranges {
		n += r.End - r.Start + 1
	}
	return n
}
