// Package clones is the fixture package for the duplicate-code pass:
// Original is the anchor of the fixture clone class.
package clones

// Original sums and then mangles its input; a structurally identical twin
// lives in twin.go under different names.
func Original(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	total = total * 2
	total = total - 3
	total = total * 4
	total = total - 5
	total = total * 6
	total = total - 7
	total = total * 8
	total = total - 9
	total = total * 10
	total = total - 11
	return total
}

// Unique does something no other fixture function does.
func Unique(limit int) int {
	out := 0
	for i := 0; i < limit; i++ {
		out += i * i
	}
	if out < 0 {
		out = 0
	}
	return out
}
