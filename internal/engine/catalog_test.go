package engine

import (
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/metrics"
	"metricgate/internal/parser"
)

// The rule catalogue is read by SARIF, by `rules explain list` and by the
// policy audit. These tests hold it to the two properties those consumers
// depend on: it is the same list however you ask for it, and it contains
// every rule that can actually produce a finding.

func catalogIDs(t *testing.T, cfg *config.Config) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	for _, m := range Catalog(cfg) {
		seen[m.ID] = true
	}
	return seen
}

func TestCatalogCoversEveryMeasuredMetric(t *testing.T) {
	cfg := testConfig(t)
	cases := []struct {
		dir  string
		lang string
	}{
		{"testdata/arch", string(parser.LangGo)},
		{"testdata/ck", string(parser.LangJava)},
		{"testdata/ckts", string(parser.LangTS)},
	}
	for _, c := range cases {
		assertMeasuredMetricsAreCatalogued(t, cfg, c.dir, c.lang)
	}
}

// assertMeasuredMetricsAreCatalogued fails if a run measures a metric that
// IDsFor does not list, which would leave the metric out of the catalogue and
// out of every audit built on it.
func assertMeasuredMetricsAreCatalogued(t *testing.T, cfg *config.Config, dir, lang string) {
	t.Helper()
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := eng.Run(dir)
	eng.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Metrics) == 0 {
		t.Errorf("%s recorded no metric snapshots, so this test proves nothing", dir)
		return
	}
	known := map[string]bool{}
	for _, id := range metrics.IDsFor(lang) {
		known[id] = true
	}
	for _, m := range summary.Metrics {
		// Package-level Martin metrics (pkg-i/a/d/ce/ca) are recorded without
		// a scope and never turned into findings — they exist for the metrics
		// table and the trend store. Being report-only is exactly why they are
		// not rules, so the catalogue omits them.
		if m.Scope == "" {
			continue
		}
		if !known[m.Metric] {
			t.Errorf("%s emits metric %q, which IDsFor(%q) does not list", dir, m.Metric, lang)
		}
	}
}

func TestCatalogListsMetricRulesPerLanguage(t *testing.T) {
	cfg := testConfig(t)
	var goMetas, javaMetas, tsMetas []string
	for _, m := range Catalog(cfg) {
		switch m.Language {
		case string(parser.LangGo):
			goMetas = append(goMetas, m.ID)
		case string(parser.LangJava):
			javaMetas = append(javaMetas, m.ID)
		case string(parser.LangTS):
			tsMetas = append(tsMetas, m.ID)
		}
	}
	assertContains(t, goMetas, metrics.MetricCyclomatic)
	assertContains(t, javaMetas, metrics.MetricCyclomatic)
	assertContains(t, tsMetas, metrics.MetricCyclomatic)
	// The CK family exists for every language that declares classes, and for no
	// other: listing it for Go would tell a Go project it has rules it can
	// never fire.
	assertMissing(t, goMetas, metrics.MetricWMC)
	assertContains(t, javaMetas, metrics.MetricWMC)
	assertContains(t, tsMetas, metrics.MetricWMC)
	assertContains(t, tsMetas, metrics.MetricDIT)
}

func TestCatalogStampsLanguageOnEveryEntry(t *testing.T) {
	cfg := testConfig(t)
	for _, m := range Catalog(cfg) {
		if m.ID == "" || m.Title == "" {
			t.Errorf("catalogue entry %+v has an empty id or title", m)
		}
	}
	// Whole-graph rules have no language; per-language ones must carry one.
	langs := map[string]bool{}
	for _, m := range Catalog(cfg) {
		if m.Language != "" {
			langs[m.Language] = true
		}
	}
	for _, want := range []string{string(parser.LangGo), string(parser.LangJava)} {
		if !langs[want] {
			t.Errorf("no catalogue entry is stamped language %q", want)
		}
	}
}

func TestEngineRulesEqualsCatalog(t *testing.T) {
	cfg := testConfig(t)
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	a, b := eng.Rules(), Catalog(cfg)
	if len(a) != len(b) {
		t.Fatalf("Rules() has %d entries, Catalog() %d — they have drifted apart", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("entry %d: Rules()=%+v Catalog()=%+v", i, a[i], b[i])
		}
	}
}

func assertContains(t *testing.T, all []string, want string) {
	t.Helper()
	for _, s := range all {
		if s == want {
			return
		}
	}
	t.Errorf("%q missing from %v", want, all)
}

func assertMissing(t *testing.T, all []string, unwanted string) {
	t.Helper()
	for _, s := range all {
		if s == unwanted {
			t.Errorf("%q must not appear in %v", unwanted, all)
			return
		}
	}
}
