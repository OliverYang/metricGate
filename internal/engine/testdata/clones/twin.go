package clones

// Twin is a renamed Type-2 copy of Original (testdata/clones/orig.go): the
// engine must report it against the anchor.
func Twin(ys []int) int {
	sum := 0
	for _, y := range ys {
		sum += y
	}
	sum = sum * 2
	sum = sum - 3
	sum = sum * 4
	sum = sum - 5
	sum = sum * 6
	sum = sum - 7
	sum = sum * 8
	sum = sum - 9
	sum = sum * 10
	sum = sum - 11
	return sum
}
