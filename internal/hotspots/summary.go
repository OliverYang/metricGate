package hotspots

import (
	"metricgate/internal/metrics"
	"metricgate/internal/model"
	"metricgate/internal/risk"
)

// WorstFileMIs folds a run's maintainability snapshots into the worst-function
// MI per file — the debt input the churn score wants. Snapshots store debt
// (100-MI), so the lowest stored MI is the worst function in the file.
func WorstFileMIs(mets []model.MetricResult) map[string]float64 {
	mis := make(map[string]float64)
	for _, m := range mets {
		if m.Metric != metrics.MetricMaintainability {
			continue
		}
		mi := 100 - float64(m.Value)
		if prev, ok := mis[m.File]; !ok || mi < prev {
			mis[m.File] = mi
		}
	}
	return mis
}

// FromSummary builds Options for a run over root with a git history window,
// taking the maintainability and risk inputs straight from the engine summary.
// It is the one place the CLI `hotspots` verb and the MCP server derive those
// inputs, so the two cannot drift.
func FromSummary(root, since string, max int, summary *model.RunSummary) Options {
	opt := risk.DefaultOptions()
	opt.Root = root
	return Options{
		Since: since,
		Root:  root,
		Max:   max,
		MIs:   WorstFileMIs(summary.Metrics),
		Risk:  risk.Scores(risk.Compute(summary.Findings, summary.Metrics, opt)),
	}
}
