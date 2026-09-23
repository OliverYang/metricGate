// Package api sits at the top of the fixture layering: it may talk to svc.
package api

import "metricgate/internal/engine/testdata/arch/svc"

// Root calls the service layer, which is legal.
func Root() int { return svc.Value() }
