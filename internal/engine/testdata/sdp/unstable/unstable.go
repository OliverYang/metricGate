// Package unstable is the fixture's least stable package: it leans on three
// leaves (Ce=3) and is leaned on by one package (Ca=1), so its instability
// is 0.75.
package unstable

import (
	"metricgate/internal/engine/testdata/sdp/l1"
	"metricgate/internal/engine/testdata/sdp/l2"
	"metricgate/internal/engine/testdata/sdp/l3"
)

// Value sums the three leaves.
func Value() int { return l1.Value() + l2.Value() + l3.Value() }
