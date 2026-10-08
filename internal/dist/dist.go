// Package dist summarises a measured metric across all of its scopes: the
// percentiles that answer "how bad is this codebase overall?", and the top-N
// scopes that answer "where is the worst of it?".
//
// Percentiles use the nearest-rank method on the sorted values — no
// interpolation — so an integer metric always yields a value that was actually
// measured, and two runs over the same tree produce byte-identical output.
// Ties in the top-N are broken by file, then line, then scope name, so the
// selection never depends on map iteration order.
package dist

import (
	"sort"

	"metricgate/internal/model"
)

// Summary is the distribution of one metric over its measured scopes.
type Summary struct {
	Metric string  `json:"metric"`
	Count  int     `json:"count"`
	Min    int     `json:"min"`
	P50    int     `json:"p50"`
	P90    int     `json:"p90"`
	P95    int     `json:"p95"`
	Max    int     `json:"max"`
	Mean   float64 `json:"mean"`
}

// Scope is one measured value together with where it came from.
type Scope struct {
	File  string `json:"file"`
	Scope string `json:"scope,omitempty"`
	Line  int    `json:"line,omitempty"`
	Value int    `json:"value"`
}

// Report is one metric's distribution plus its worst scopes.
type Report struct {
	Summary
	Top []Scope `json:"top,omitempty"`
}

// Summarize computes the distribution of values. An empty slice yields a zero
// Count and zero percentiles rather than a panic.
func Summarize(metric string, values []int) Summary {
	s := Summary{Metric: metric, Count: len(values)}
	if len(values) == 0 {
		return s
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	s.Min = sorted[0]
	s.Max = sorted[len(sorted)-1]
	s.P50 = percentile(sorted, 50)
	s.P90 = percentile(sorted, 90)
	s.P95 = percentile(sorted, 95)
	sum := 0
	for _, v := range sorted {
		sum += v
	}
	s.Mean = float64(sum) / float64(len(sorted))
	return s
}

// percentile returns the nearest-rank p-th percentile of a sorted slice:
// rank = ceil(p/100 * n), clamped to a valid index.
func percentile(sorted []int, p int) int {
	n := len(sorted)
	rank := (p*n + 99) / 100 // integer ceil(p*n/100)
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1]
}

// Reports groups a run's metric snapshots by metric and returns one Report per
// metric, each carrying its topN worst scopes. topN <= 0 omits the top lists
// entirely, leaving the distribution table. Reports are ordered by metric name
// so the output is stable.
func Reports(mets []model.MetricResult, topN int) []Report {
	byMetric := map[string][]Scope{}
	for _, m := range mets {
		byMetric[m.Metric] = append(byMetric[m.Metric], Scope{
			File: m.File, Scope: m.Scope, Line: m.StartLine, Value: m.Value,
		})
	}
	out := make([]Report, 0, len(byMetric))
	for metric, scopes := range byMetric {
		values := make([]int, 0, len(scopes))
		for _, s := range scopes {
			values = append(values, s.Value)
		}
		r := Report{Summary: Summarize(metric, values)}
		if topN > 0 {
			r.Top = Top(scopes, topN)
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Metric < out[j].Metric })
	return out
}

// Top returns the n largest scopes, worst first. n <= 0 means "all", so a
// caller that wants no list at all should skip the call (as Reports does).
func Top(scopes []Scope, n int) []Scope {
	sorted := append([]Scope(nil), scopes...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Value != sorted[j].Value {
			return sorted[i].Value > sorted[j].Value
		}
		if sorted[i].File != sorted[j].File {
			return sorted[i].File < sorted[j].File
		}
		if sorted[i].Line != sorted[j].Line {
			return sorted[i].Line < sorted[j].Line
		}
		return sorted[i].Scope < sorted[j].Scope
	})
	if n > 0 && len(sorted) > n {
		sorted = sorted[:n]
	}
	return sorted
}
