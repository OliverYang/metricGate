// Package clones is the fixture package for the duplicate-code pass:
// Original is the anchor of the fixture clone class.
package clones

// Original sums and then mangles its input; SuppressedTwin in twin.go is a
// renamed copy carrying an inline suppression directive.
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
