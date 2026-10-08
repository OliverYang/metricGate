// Package consumer exists so that stable has an afferent coupling: without a
// dependent, stable's instability would be 1 and there would be nothing to
// violate. consumer is itself fully unstable (I=1), so depending on stable is
// legal for it.
package consumer

import "metricgate/internal/engine/testdata/sdp/stable"

// Value calls down into stable.
func Value() int { return stable.Value() }
