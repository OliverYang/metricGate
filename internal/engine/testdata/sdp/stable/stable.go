// Package stable is the fixture's well-depended-on package: one consumer
// leans on it (Ca=1) and it leans on exactly one thing (Ce=1), so its
// instability is 0.5 — lower than the package it depends on.
package stable

import "metricgate/internal/engine/testdata/sdp/unstable"

// Value reaches into unstable, which is less stable than stable: the
// dependency points the wrong way along the stability axis.
func Value() int { return unstable.Value() }
