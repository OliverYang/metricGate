// Package risk aggregates the engine's per-file signals — rule and metric
// findings by severity, plus the file's maintainability debt — into one
// deterministic 0..100 score. It is the static companion to hotspots: where
// hotspots multiplies churn by debt (how often a file changes), risk answers
// "how much is already wrong with this file?" without touching git, so the
// same number is available to a pre-commit check, the MCP server, or a diff.
//
// Score per file, 0..100:
//
//	load     = 10*errors + 3*warnings + 1*infos   (severity-weighted findings)
//	loadNorm = load / (load + K), K = 10          (saturating, 0..1)
//	debtNorm = worstDebt / 100                     (0..1, worst function's 100-MI)
//	score    = 100 * ((1-W)*loadNorm + W*debtNorm), W = 0.3
//
// The saturating load keeps the number comparable across repositories and
// runs — it never depends on the repo's maximum — so a regression can be
// tracked; the fixed weights make two runs over the same tree byte-identical.
// A file below MinScore is not called a risk and is omitted: nearly every
// file carries a little MI debt, and a long tail of 0.x scores is noise, not
// signal.
package risk

import (
	"sort"

	"metricgate/internal/metrics"
	"metricgate/internal/model"
	"metricgate/internal/selection"
)

// MinScore is the floor below which a file is not reported. Nearly every file
// carries a little maintainability debt, so without a floor the ranking would
// end in a long tail of sub-1 scores that mean nothing; 1.0 keeps only files
// with a real finding or meaningful debt.
const MinScore = 1.0

// FileRisk is one file's aggregated risk together with the inputs that
// produced it, so a consumer can explain the number instead of trusting it.
type FileRisk struct {
	File     string  `json:"file"`
	Score    float64 `json:"score"` // 0..100
	Errors   int     `json:"errors"`
	Warnings int     `json:"warnings"`
	Infos    int     `json:"infos"`
	Debt     float64 `json:"debt"` // worst-function maintainability debt, 0..100
}

// Options tunes the aggregation. The zero value is usable: every field falls
// back to its default, so Options{Root: root} is a complete configuration.
type Options struct {
	Root          string  // walk root; finding paths are normalised relative to it
	ErrorWeight   float64 // weight of one error finding
	WarningWeight float64 // weight of one warning finding
	InfoWeight    float64 // weight of one info finding
	DebtShare     float64 // fraction of the score that comes from debt (0..1)
	K             float64 // saturation constant for the finding load
}

// DefaultOptions returns the standard weights.
func DefaultOptions() Options {
	return Options{ErrorWeight: 10, WarningWeight: 3, InfoWeight: 1, DebtShare: 0.3, K: 10}
}

// normalized fills in defaults for anything left unset, so a partial Options
// behaves exactly like DefaultOptions with the given fields overridden.
func (o Options) normalized() Options {
	if o.ErrorWeight <= 0 {
		o.ErrorWeight = 10
	}
	if o.WarningWeight <= 0 {
		o.WarningWeight = 3
	}
	if o.InfoWeight <= 0 {
		o.InfoWeight = 1
	}
	if o.K <= 0 {
		o.K = 10
	}
	if o.DebtShare < 0 || o.DebtShare > 1 {
		o.DebtShare = 0.3
	}
	return o
}

// Compute aggregates one engine run into per-file risk, sorted by score
// (descending) then path, dropping anything below MinScore. Paths are
// normalised through selection.SanitizePath so an absolute run root and a "."
// run root produce the same keys.
func Compute(findings []model.Finding, mets []model.MetricResult, opt Options) []FileRisk {
	o := opt.normalized()
	byFile := map[string]*FileRisk{}
	tallyFindings(byFile, findings, o.Root)
	tallyDebt(byFile, mets, o.Root)
	out := make([]FileRisk, 0, len(byFile))
	for _, r := range byFile {
		r.Score = scoreOf(r, o)
		if r.Score >= MinScore {
			out = append(out, *r)
		}
	}
	sortRisks(out)
	return out
}

// accumulator returns the per-file risk record for path, creating it on first
// use under the path normalised against root.
func accumulator(byFile map[string]*FileRisk, root, path string) *FileRisk {
	key := selection.SanitizePath(root, path)
	r := byFile[key]
	if r == nil {
		r = &FileRisk{File: key}
		byFile[key] = r
	}
	return r
}

// tallyFindings adds each finding to its file's severity counters.
func tallyFindings(byFile map[string]*FileRisk, findings []model.Finding, root string) {
	for _, f := range findings {
		r := accumulator(byFile, root, f.File)
		switch f.Severity {
		case model.SevError:
			r.Errors++
		case model.SevWarning:
			r.Warnings++
		default:
			r.Infos++
		}
	}
}

// tallyDebt folds each file's worst maintainability debt into its record.
func tallyDebt(byFile map[string]*FileRisk, mets []model.MetricResult, root string) {
	for _, m := range mets {
		if m.Metric != metrics.MetricMaintainability {
			continue
		}
		// MetricResult stores debt (100-MI), so the raw value is already the
		// 0..100 signal this score wants.
		if r := accumulator(byFile, root, m.File); float64(m.Value) > r.Debt {
			r.Debt = float64(m.Value)
		}
	}
}

// sortRisks orders the ranking by score, breaking ties on path so the output
// never depends on map iteration.
func sortRisks(out []FileRisk) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].File < out[j].File
	})
}

// scoreOf applies the documented formula to one file's components.
func scoreOf(r *FileRisk, o Options) float64 {
	load := o.ErrorWeight*float64(r.Errors) + o.WarningWeight*float64(r.Warnings) + o.InfoWeight*float64(r.Infos)
	loadNorm := load / (load + o.K)
	debtNorm := r.Debt / 100
	if debtNorm > 1 {
		debtNorm = 1
	}
	return 100 * ((1-o.DebtShare)*loadNorm + o.DebtShare*debtNorm)
}

// Scores flattens a ranking into a path -> score lookup, so a caller can
// annotate another ranking (hotspots, the repo map) without re-deriving it.
func Scores(risks []FileRisk) map[string]float64 {
	out := make(map[string]float64, len(risks))
	for _, r := range risks {
		out[r.File] = r.Score
	}
	return out
}
