// Package q is the other half of the agreed-upon legacy cycle.
package q

import "metricgate/internal/engine/testdata/archsup/p"

// Pong reaches back into p.
func Pong() int { return p.Ping() }
