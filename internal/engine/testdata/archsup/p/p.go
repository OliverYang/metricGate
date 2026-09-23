// Package p is one half of a legacy cycle the team agreed to keep.
package p

// metricgate: ignore import-cycle legacy pairing, split tracked in #42
import "metricgate/internal/engine/testdata/archsup/q"

// Ping reaches into q.
func Ping() int { return q.Pong() }
