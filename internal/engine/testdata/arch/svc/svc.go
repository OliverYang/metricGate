// Package svc is the middle layer. It may talk to store, and nothing above it.
package svc

import (
	"metricgate/internal/engine/testdata/arch/api"
	"metricgate/internal/engine/testdata/arch/store"
)

// Value reads the storage layer (legal) and the api layer (illegal: it also
// closes an import cycle).
func Value() int { return store.Row() + api.Root() }
