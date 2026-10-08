// Package l1 is a fixture leaf: it depends on nothing, so its instability
// is 0 and depending on it is always allowed.
package l1

// Value is the leaf's only export.
func Value() int { return 1 }
