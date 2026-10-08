package facts

import (
	"fmt"

	"metricgate/internal/config"
	"metricgate/internal/model"
	"metricgate/internal/repomap"
	"metricgate/internal/risk"
	"metricgate/internal/selection"
)

// RankedFile is one entry of the importance ranking. It aliases the repo-map
// score so a caller names the element type through this package — where the
// function that produces the ranking lives — instead of each caller adding its
// own dependency on internal/repomap.
type RankedFile = repomap.FileScore

// RankedFiles returns the whole-repo importance ranking for root. When
// findings and metric snapshots are supplied, each file's static risk score is
// joined in; passing nil simply leaves every file's risk at zero. It is the
// one implementation behind the CLI `map` verb and the MCP repo-map tool, so
// the two cannot drift.
func RankedFiles(root string, cfg *config.Config, findings []model.Finding, mets []model.MetricResult) ([]RankedFile, error) {
	files, err := CollectSelected(root, cfg)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no supported source files selected under %s", root)
	}
	scores := repomap.Build(AllDefs(files), AllRefs(files), repomap.DefaultOptions())
	annotateRisk(scores, findings, mets, root)
	return scores, nil
}

// annotateRisk joins each ranked file's static risk score. Both sides are
// normalised through selection.SanitizePath against the same root, so an
// absolute run root and "." produce the same keys — including the single-file
// case, where the map path is the root itself.
func annotateRisk(scores []RankedFile, findings []model.Finding, mets []model.MetricResult, root string) {
	opt := risk.DefaultOptions()
	opt.Root = root
	risks := risk.Scores(risk.Compute(findings, mets, opt))
	for i := range scores {
		scores[i].Risk = risks[selection.SanitizePath(root, scores[i].File)]
	}
}
