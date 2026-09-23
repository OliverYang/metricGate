// Package sample deliberately contains violations so the engine tests can
// assert exact findings. It is excluded from default policy runs via the
// "testdata" exclude entry.
package sample

import "fmt"

// TODO: this comment is detected when todo-comment is enabled.

func Chain(a int) int {
	if a > 0 {
		if a > 1 {
			for i := 0; i < a; i++ {
				if i > 2 {
					fmt.Println(i)
				}
			}
		}
	}
	switch a {
	case 1:
		return 1
	case 2:
		return 2
	}
	return 0
}

func Empty(x int) {
	if x > 0 {
	}
	for i := 0; i < x; i++ {
	}
}

func Boom() {
	panic("boom")
}

func Many(a, b, c, d, e, f, g int) int {
	return a + b + c + d + e + f + g
}
