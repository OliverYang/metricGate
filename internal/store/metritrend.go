package store

import (
	"database/sql"
	"sort"
)

// MetricPoint is one run's aggregate for one metric: the distribution of
// metric values across all snapshots recorded in that run, plus the
// thresholds in force at the time. A sequence of points answers "did the
// distribution move when the threshold changed" without re-running anything.
type MetricPoint struct {
	RunID    int64  `json:"runId"`
	Started  string `json:"startedAt"`
	Samples  int    `json:"samples"`
	P50      int    `json:"p50"`
	P90      int    `json:"p90"`
	Max      int    `json:"max"`
	Warn     int    `json:"warnThreshold"`
	Err      int    `json:"errorThreshold"`
	Violated int    `json:"violated"` // samples at or above the warn threshold

	// values buffers raw samples until finish() computes the percentiles;
	// it is never serialized.
	values []int
}

// finish computes the p50/p90 percentiles over the buffered samples.
func (p *MetricPoint) finish() {
	if len(p.values) == 0 {
		return
	}
	sort.Ints(p.values)
	p.P50 = p.values[(len(p.values)-1)/2]
	p.P90 = p.values[(len(p.values)*9)/10]
	p.values = nil
}

// MetricTrend is the metric-dimension view of the run window: one series per
// metric seen in the window, oldest first.
type MetricTrend struct {
	Runs   int                      `json:"runs"`
	Series map[string][]MetricPoint `json:"series"`
}

// MetricTrend folds the most recent limit runs (limit <= 0 = all) into
// per-metric distribution series. Warn thresholds are recorded per snapshot,
// so a policy change shows up as a step in the warn/error columns — the
// "experiment" half of the loop: adjust the threshold, run, compare.
func (s *Store) MetricTrend(limit int) (MetricTrend, error) {
	return s.MetricTrendIn(Window{Limit: limit})
}

// MetricTrendIn is MetricTrend scoped by a Window. A window carrying only
// Limit is MetricTrend, so the two renderers cannot disagree.
func (s *Store) MetricTrendIn(w Window) (MetricTrend, error) {
	t := MetricTrend{Series: map[string][]MetricPoint{}}
	if err := s.scanMetricTrend(&t, w); err != nil {
		return t, err
	}
	for m := range t.Series {
		for i := range t.Series[m] {
			t.Series[m][i].finish()
		}
	}
	t.Runs = countDistinctRuns(t.Series)
	return t, nil
}

// scanMetricTrend streams the snapshot rows for the run window into per-
// (run, metric) points. Rows arrive oldest-first, so appending preserves
// the series order without a final sort.
func (s *Store) scanMetricTrend(t *MetricTrend, w Window) error {
	// The run id subquery is scoped by the window, so the metric rows that
	// survive are exactly the runs the window admits — including the
	// project scope, which the snapshots themselves do not carry.
	where, filterArgs := w.runFilter()
	runsQ := `SELECT id FROM runs` + where
	if w.Limit > 0 {
		runsQ += ` ORDER BY started_at DESC, id DESC LIMIT ?`
	}
	q := `SELECT s.run_id, r.started_at, s.metric, s.value, s.warn_threshold, s.err_threshold
	      FROM metric_snapshots s JOIN runs r ON r.id = s.run_id
	      WHERE s.run_id IN (` + runsQ + `)
	      ORDER BY r.started_at ASC, r.id ASC`
	// Copied before extending: the LIMIT ? value is appended to this list, and
	// appending onto filterArgs itself would write through the slice runFilter
	// handed back. The one-append-per-line shape is also what keeps
	// `go vet ./...` quiet -- `append(filterArgs...)` with no values is the
	// "append with no values" defect this line used to carry.
	args := append([]any{}, filterArgs...)
	if w.Limit > 0 {
		args = append(args, w.Limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	idx := map[metricKey]int{} // series position per (run, metric)
	for rows.Next() {
		if err := absorbMetricRow(t, rows, idx); err != nil {
			return err
		}
	}
	return rows.Err()
}

// metricKey identifies a point's (run, metric) pair.
type metricKey struct {
	run  int64
	metr string
}

// absorbMetricRow folds one snapshot row into its metric point, creating the
// point on first sight of its (run, metric) pair.
func absorbMetricRow(t *MetricTrend, rows *sql.Rows, idx map[metricKey]int) error {
	var runID, value, warn, errT int64
	var started, metric string
	if err := rows.Scan(&runID, &started, &metric, &value, &warn, &errT); err != nil {
		return err
	}
	k := metricKey{runID, metric}
	i, ok := idx[k]
	if !ok {
		t.Series[metric] = append(t.Series[metric], MetricPoint{
			RunID: runID, Started: started, Warn: int(warn), Err: int(errT),
		})
		i = len(t.Series[metric]) - 1
		idx[k] = i
	}
	p := &t.Series[metric][i]
	p.Samples++
	p.values = append(p.values, int(value))
	if int(value) > p.Max {
		p.Max = int(value)
	}
	if int(value) >= int(warn) && warn > 0 {
		p.Violated++
	}
	return nil
}

// countDistinctRuns reports how many distinct runs the window covers (a run
// may be missing from some metrics if it produced no snapshots for them).
func countDistinctRuns(series map[string][]MetricPoint) int {
	seen := map[int64]bool{}
	for _, pts := range series {
		for _, p := range pts {
			seen[p.RunID] = true
		}
	}
	return len(seen)
}
