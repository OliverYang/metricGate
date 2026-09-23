// The Maintainability Index (Oman & Hagemeister 1992; the scale-restored
// variant Microsoft published in 2007), computed per file from the
// Halstead volume, the total cyclomatic complexity and the line count:
//
//	MI = max(0, (171 - 5.2*ln(V) - 0.23*CC - 16.2*ln(LOC)) * 100 / 171)
//
// Ranges: >=85 maintainable, 65..85 worth a look, <65 hard to maintain
// (the classic red/yellow/green bands).
package metrics

import (
	"math"
)

// MetricMaintainability is the metric id for the file-level MI.
const MetricMaintainability = "maintainability"

// MaintainabilityIndex computes the MI from raw inputs. Guarded: a volume
// below 1 counts as 1 so empty and trivial files never produce NaN, and
// the result never leaves [0,100].
func MaintainabilityIndex(volume, cyclomatic, lines int) float64 {
	v := math.Max(1, float64(volume))
	loc := math.Max(1, float64(lines))
	mi := 171 - 5.2*math.Log(v) - 0.23*float64(cyclomatic) - 16.2*math.Log(loc)
	mi *= 100.0 / 171.0
	if mi < 0 {
		return 0
	}
	if mi > 100 {
		return 100
	}
	return mi
}
