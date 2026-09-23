// Package engine orchestrates one metricgate run: collect files, parse them
// in parallel, run metrics and rules, and apply policy thresholds. It is a
// pure library: no printing, no writing, no reading of configuration from
// disk — the CLI and the future service are thin shells around it.
package engine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"metricgate/internal/config"
	"metricgate/internal/metrics"
	"metricgate/internal/model"
	"metricgate/internal/parser"
	"metricgate/internal/rules"
)

// metricDefault is the built-in threshold pair for one metric. The same
// defaults apply to every language in v0; policies override per language.
type metricDefault struct {
	Warn, Error int
}

var metricDefaults = map[string]metricDefault{
	metrics.MetricCyclomatic: {Warn: 10, Error: 15},
	metrics.MetricCognitive:  {Warn: 15, Error: 25},
	metrics.MetricFuncLength: {Warn: 50, Error: 100},
	metrics.MetricFileLength: {Warn: 400, Error: 800},
	metrics.MetricNesting:    {Warn: 4, Error: 6},
	metrics.MetricParams:     {Warn: 5, Error: 8},
}

// Engine runs the checker over a directory tree, with one rule runner per
// registered language.
type Engine struct {
	cfg     *config.Config
	langs   map[parser.LanguageID]*parser.Parser
	runners map[parser.LanguageID]*rules.Runner
}

// New builds an engine for the given policy. All rule queries are compiled
// here so malformed patterns fail before any file is read.
func New(cfg *config.Config) (*Engine, error) {
	e := &Engine{
		cfg:     cfg,
		langs:   map[parser.LanguageID]*parser.Parser{},
		runners: map[parser.LanguageID]*rules.Runner{},
	}
	for _, id := range parser.Supported() {
		p, err := parser.New(id)
		if err != nil {
			e.Close()
			return nil, err
		}
		runner, err := rules.NewRunner(string(id), p.Lang, rules.ForLanguage(id))
		if err != nil {
			p.Close()
			e.Close()
			return nil, fmt.Errorf("compile %s rules: %w", id, err)
		}
		e.langs[id] = p
		e.runners[id] = runner
	}
	return e, nil
}

// Close releases engine resources.
func (e *Engine) Close() {
	for _, r := range e.runners {
		r.Close()
	}
	for _, p := range e.langs {
		p.Close()
	}
}

// Rules returns rule metadata for reports across all languages.
func (e *Engine) Rules() []rules.Meta {
	var metas []rules.Meta
	for _, id := range parser.Supported() {
		metas = append(metas, rules.Metas(rules.ForLanguage(id))...)
	}
	return metas
}

type fileResult struct {
	findings []model.Finding
	metrics  []model.MetricResult
}

// Run checks every supported file under root and returns the summary.
func (e *Engine) Run(root string) (*model.RunSummary, error) {
	start := time.Now()
	files, err := e.collect(root)
	if err != nil {
		return nil, err
	}
	summary := &model.RunSummary{Files: len(files)}

	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	if workers < 1 {
		workers = 1
	}

	jobs := make(chan string)
	results := make(chan fileResult, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := &worker{ps: map[parser.LanguageID]*parser.Parser{}}
			for path := range jobs {
				results <- e.checkFile(w, path)
			}
		}()
	}
	go func() {
		for _, f := range files {
			jobs <- f
		}
		close(jobs)
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	for res := range results {
		summary.Findings = append(summary.Findings, res.findings...)
		summary.Metrics = append(summary.Metrics, res.metrics...)
	}

	model.SortFindings(summary.Findings)
	model.SortMetrics(summary.Metrics)
	summary.Duration = time.Since(start)
	return summary, nil
}

// worker holds the per-goroutine parser pool; tree-sitter parsers are not
// thread-safe, so each worker lazily creates one per language it meets.
type worker struct {
	ps map[parser.LanguageID]*parser.Parser
}

func (w *worker) parserFor(id parser.LanguageID) (*parser.Parser, error) {
	if p := w.ps[id]; p != nil {
		return p, nil
	}
	p, err := parser.New(id)
	if err != nil {
		return nil, err
	}
	w.ps[id] = p
	return p, nil
}

// collect walks root and returns files for supported languages, skipping
// excluded directory names at any depth.
func (e *Engine) collect(root string) ([]string, error) {
	skip := map[string]bool{}
	for _, name := range e.cfg.Exclude {
		skip[name] = true
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := parser.LanguageForFile(d.Name()); ok {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// checkFile parses and analyses one file. Parse errors are reported as an
// info finding instead of failing the run: partial trees still yield useful
// findings, which matters when checking freshly generated LLM output.
func (e *Engine) checkFile(w *worker, path string) fileResult {
	var res fileResult
	lang, ok := parser.LanguageForFile(filepath.Base(path))
	if !ok {
		return res
	}
	src, err := os.ReadFile(path)
	if err != nil {
		res.findings = append(res.findings, model.Finding{
			RuleID: "read-error", Severity: model.SevWarning, Source: model.SrcRule,
			File: path, StartLine: 1, EndLine: 1,
			Message: "could not read file: " + err.Error(),
		})
		return res
	}

	pp, err := w.parserFor(lang)
	if err != nil {
		res.findings = append(res.findings, model.Finding{
			RuleID: "parse-error", Severity: model.SevInfo, Source: model.SrcRule,
			File: path, StartLine: 1, EndLine: 1, Message: err.Error(),
		})
		return res
	}
	tree, err := pp.Parse(src)
	if err != nil {
		res.findings = append(res.findings, model.Finding{
			RuleID: "parse-error", Severity: model.SevInfo, Source: model.SrcRule,
			File: path, StartLine: 1, EndLine: 1, Message: err.Error(),
		})
		return res
	}
	defer tree.Close()

	if tree.RootNode().HasError() {
		res.findings = append(res.findings, model.Finding{
			RuleID: "parse-error", Severity: model.SevInfo, Source: model.SrcRule,
			File: path, StartLine: 1, EndLine: 1,
			Message: "file has syntax errors; results may be incomplete",
		})
	}

	stats := metrics.AnalyzeFile(path, src, tree, lang)
	res.metrics = e.metricResults(lang, stats)
	res.findings = append(res.findings, e.metricFindings(lang, stats)...)
	res.findings = append(res.findings, e.runners[lang].Run(path, src, tree, e.cfg)...)
	return res
}

// thresholds resolves the effective warn/error thresholds for a metric.
func (e *Engine) thresholds(lang parser.LanguageID, metricID string) (warn, errAt int) {
	def := metricDefaults[metricID]
	warn, errAt = def.Warn, def.Error
	w, er, enabled := e.cfg.MetricThresholds(string(lang), metricID)
	if enabled != nil && !*enabled {
		return 0, 0
	}
	if w > 0 {
		warn = w
	}
	if er > 0 {
		errAt = er
	}
	return warn, errAt
}

// metricEnabled reports whether a metric is switched on in policy.
func (e *Engine) metricEnabled(lang parser.LanguageID, metricID string) bool {
	_, _, enabled := e.cfg.MetricThresholds(string(lang), metricID)
	return enabled == nil || *enabled
}

// metricResults records every measured value (for trend storage and the
// metrics command), regardless of whether a threshold was crossed.
func (e *Engine) metricResults(lang parser.LanguageID, stats *metrics.FileStats) []model.MetricResult {
	if !e.metricEnabled(lang, metrics.MetricFileLength) {
		return nil
	}
	warn, errAt := e.thresholds(lang, metrics.MetricFileLength)
	out := []model.MetricResult{{
		Metric: metrics.MetricFileLength, File: stats.File, Scope: "",
		Value: stats.Lines, Warn: warn, Err: errAt,
	}}
	for _, fn := range stats.Functions {
		for metric, value := range map[string]int{
			metrics.MetricCyclomatic: fn.Cyclomatic,
			metrics.MetricCognitive:  fn.Cognitive,
			metrics.MetricFuncLength: fn.Length,
			metrics.MetricNesting:    fn.MaxNesting,
			metrics.MetricParams:     fn.Params,
		} {
			if !e.metricEnabled(lang, metric) {
				continue
			}
			w, er := e.thresholds(lang, metric)
			out = append(out, model.MetricResult{
				Metric: metric, File: stats.File, Scope: fn.Name,
				Value: value, Warn: w, Err: er,
			})
		}
	}
	return out
}

// metricFindings turns threshold crossings into findings. A value crosses a
// threshold when strictly greater than it.
func (e *Engine) metricFindings(lang parser.LanguageID, stats *metrics.FileStats) []model.Finding {
	var findings []model.Finding
	add := func(metric, scope string, value int) {
		if !e.metricEnabled(lang, metric) {
			return
		}
		warn, errAt := e.thresholds(lang, metric)
		if warn == 0 && errAt == 0 {
			return
		}
		var sev model.Severity
		switch {
		case errAt > 0 && value > errAt:
			sev = model.SevError
		case warn > 0 && value > warn:
			sev = model.SevWarning
		default:
			return
		}
		threshold := warn
		if sev == model.SevError {
			threshold = errAt
		}
		line, end := 1, 1
		if scope != "" {
			for _, fn := range stats.Functions {
				if fn.Name == scope {
					line, end = fn.StartLine, fn.EndLine
					break
				}
			}
		}
		scopeLabel := scope
		if scopeLabel == "" {
			scopeLabel = stats.File
		}
		findings = append(findings, model.Finding{
			RuleID: metric, Severity: sev, Source: model.SrcMetric, File: stats.File,
			StartLine: line, EndLine: end,
			Message: fmt.Sprintf("%s of %q is %d (threshold %s %d)",
				metric, scopeLabel, value, sev, threshold),
		})
	}
	add(metrics.MetricFileLength, "", stats.Lines)
	for _, fn := range stats.Functions {
		add(metrics.MetricCyclomatic, fn.Name, fn.Cyclomatic)
		add(metrics.MetricCognitive, fn.Name, fn.Cognitive)
		add(metrics.MetricFuncLength, fn.Name, fn.Length)
		add(metrics.MetricNesting, fn.Name, fn.MaxNesting)
		add(metrics.MetricParams, fn.Name, fn.Params)
	}
	return findings
}
