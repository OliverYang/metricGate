package report

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"metricgate/internal/config"
	"metricgate/internal/model"
)

// Text renders the human-readable report.
func Text(s *model.RunSummary, cfg *config.Config, toolVersion string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "metricgate %s — %d files, %s\n\n", toolVersion, s.Files, s.Duration.Round(time.Millisecond))
	for _, f := range s.Findings {
		fmt.Fprintf(&b, "  %s:%d: [%s] %s (%s)\n", f.File, f.StartLine, f.Severity, f.RuleID, f.Message)
	}
	if len(s.ParseErrors) > 0 {
		fmt.Fprintf(&b, "\n  parse errors: %s\n", strings.Join(s.ParseErrors, ", "))
	}
	fmt.Fprintf(&b, "\nsummary: %d errors, %d warnings, %d notes, %d metric snapshots\n",
		s.Count(model.SevError), s.Count(model.SevWarning), s.Count(model.SevInfo), len(s.Metrics))
	return b.String()
}

// JSON renders the raw summary for tool-to-tool consumption (this is the
// payload the LLM feedback loop will read in v2).
func JSON(s *model.RunSummary) ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}
