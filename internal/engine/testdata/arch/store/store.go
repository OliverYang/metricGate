// Package store is the bottom layer of the fixture and must stay a leaf.
package store

import "metricgate/internal/engine/testdata/arch/svc"

// Row calls back into svc, which the policy forbids in both ways.
func Row() int { return svc.Value() }
