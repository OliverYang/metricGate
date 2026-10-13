package main

import (
	"fmt"
	"os"

	"metricgate/internal/config"
	"metricgate/internal/engine"
)

// service implements the MCP tool handlers on top of one long-lived engine
// instance, so the rule queries are compiled once at startup rather than per
// call.
type service struct {
	eng *engine.Engine
	cfg *config.Config
	// gate is the inline-verdict watcher; nil when the session root is not a
	// Git worktree. addTool layers it in front of every handler.
	gate *gateWatcher
	// log records every tool call when an operator enabled it, and is nil
	// otherwise. Handlers never touch it directly: registerTools routes each
	// one through wrapTool, which tolerates a nil logger.
	log *callLogger
}

// newService assembles the service with its gate watcher and call logger,
// announcing the gate when it has a worktree to watch.
func newService(eng *engine.Engine, cfg *config.Config, callLogPath string) *service {
	svc := &service{eng: eng, cfg: cfg, log: newCallLogger(callLogPath), gate: newGateWatcher(eng)}
	if svc.gate != nil && svc.gate.root != "" {
		fmt.Fprintf(os.Stderr, "metricgate-mcp: gate watching changes under %s\n", svc.gate.root)
	}
	return svc
}
