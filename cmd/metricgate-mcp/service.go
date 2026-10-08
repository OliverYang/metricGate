package main

import (
	"metricgate/internal/config"
	"metricgate/internal/engine"
)

// service implements the MCP tool handlers on top of one long-lived engine
// instance, so the rule queries are compiled once at startup rather than per
// call.
type service struct {
	eng *engine.Engine
	cfg *config.Config
	// log records every tool call when an operator enabled it, and is nil
	// otherwise. Handlers never touch it directly: registerTools routes each
	// one through wrapTool, which tolerates a nil logger.
	log *callLogger
}
