// Package stubs is the fixture package for the stub inventory.
package stubs

// Missing does nothing yet — the canonical "LLM left this empty" case.
func Missing(x int) int {
}

// Punt raises the usual placeholder panic.
func Punt(x int) int {
	panic("not implemented")
}
