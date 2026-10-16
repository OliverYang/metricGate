// Command generate writes internal/cockpit/web/spec.json: the single document
// both sides of the cockpit contract read.
//
// It exists as a separate package rather than a test because the file is part
// of the build (it is embedded and served), so it must be rewritable without
// running the test suite — and as a *test* it would be rewritten by whoever
// runs the tests, which is a way for a generated file to change on a machine
// that never asked for it.
package main

import (
	"log"

	"metricgate/internal/webspec"
)

// SpecTarget is where the spec is written, relative to the repository root.
const SpecTarget = "internal/cockpit/web/spec.json"

func main() {
	if err := webspec.Write(SpecTarget); err != nil {
		log.Fatalf("write spec: %v", err)
	}
}
