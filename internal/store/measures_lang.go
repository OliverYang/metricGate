package store

import (
	"fmt"
	"path/filepath"

	"metricgate/internal/parser"
)

// RunMeasuresByLang aggregates one run's metrics per language rather than
// across the whole run. The gate table renders one row per language, and a
// single merged value has to pick one language: a Go file at 7 and a Java file
// at 3 under the same metric name came back as one MAX for both, so the two
// language blocks looked like copies of each other.
//
// The language is re-derived from the snapshot's filename because
// metric_snapshots has no language column; parser.LanguageForFile is the one
// place that decides which extension is which language, so the store does not
// grow a second, drifting copy of that mapping.
//
// Files whose extension is not a registered language are dropped rather than
// folded into a bucket: every snapshot row is written by the engine for a file
// it parsed, and a parsed file always has a registered extension. If that ever
// stops being true, RunMeasures still reports the run-wide totals, and a
// missing language shows up as an absent key the caller renders as "no data"
// instead of a number borrowed from some other language.
func (s *Store) RunMeasuresByLang(runID int64) (map[string][]RunMeasure, error) {
	rows, err := s.db.Query(`SELECT metric, file, MAX(value),
	                                 SUM(CASE WHEN value >= warn_threshold THEN 1 ELSE 0 END)
	                          FROM metric_snapshots WHERE run_id = ?
	                          GROUP BY metric, file ORDER BY metric, file`, runID)
	if err != nil {
		return nil, fmt.Errorf("read per-language run measures: %w", err)
	}
	defer rows.Close()
	out := map[string][]RunMeasure{}
	at := map[string]int{}
	for rows.Next() {
		var metric, file string
		var m RunMeasure
		if err := rows.Scan(&metric, &file, &m.Worst, &m.Exceeded); err != nil {
			return nil, fmt.Errorf("read per-language run measures: %w", err)
		}
		lang, ok := parser.LanguageForFile(filepath.Base(file))
		if !ok {
			continue
		}
		m.Metric = metric
		mergeMeasure(out, at, string(lang), m)
	}
	return out, rows.Err()
}

// mergeMeasure folds one (metric, file) aggregate into its language bucket,
// worst taking the max and exceeded summing: they were computed per file above,
// and the gate asks for one worst and one count per language.
func mergeMeasure(out map[string][]RunMeasure, at map[string]int, lang string, m RunMeasure) {
	key := lang + "\x00" + m.Metric
	i, seen := at[key]
	if !seen {
		at[key] = len(out[lang])
		out[lang] = append(out[lang], m)
		return
	}
	cur := &out[lang][i]
	if m.Worst > cur.Worst {
		cur.Worst = m.Worst
	}
	cur.Exceeded += m.Exceeded
}
