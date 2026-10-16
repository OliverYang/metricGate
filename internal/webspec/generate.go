package webspec

import _ "embed"

// generate.go carries the go:generate directive that writes the spec document
// the frontend imports.
//
// go:generate is used instead of a hand-written main so the generator ships
// inside the module with nothing to build separately, and so `go generate
// ./...` discovers it the way every other tool expects. The command writes to
// internal/cockpit/web/spec.json — the directory the embedded filesystem
// serves — so the browser and the Go side read one file.

//go:generate go run ./generate
