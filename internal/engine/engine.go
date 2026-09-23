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

// metricDefault is the built-in threshold pair for one metric.
type metricDefault struct {
	Warn, Error int
}

// goMetricDefaults mirrors the default policy; policy values override these.
var goMetricDefaults = map[string]metricDefault{
	metrics.MetricCyclomatic: {Warn: 10, Error: 15},
	metrics.MetricCognitive:  {Warn: 15, Error: 25},
	metrics.MetricFuncLength: {Warn: 50, Error: 100},
	metrics.MetricFileLength: {Warn: 400, Error: 800},
	metrics.MetricNesting:    {Warn: 4, Error: 6},
	metrics.MetricParams:     {Warn: 5, Error: 8},
}

// Engine runs the checker over a directory tree.
type Engine struct {
	cfg    *config.Config
	runner *rules.Runner
	goLang *parser.Parser // only used for its *sitter.Language handle
}

// New builds an engine for the given policy. All rule queries are compiled
// here so malformed patterns fail before any file is read.
func New(cfg *config.Config) (*Engine, error) {
	p, err := parser.New(parser.LangGo)
	if err != nil {
		return nil, err
	}
	runner, err := rules.NewRunner(string(parser.LangGo), p.Lang, rules.GoRules())
	if err != nil {
		p.Close()
		return nil, fmt.Errorf("compile rules: %w", err)
	}
	return &Engine{cfg: cfg, runner: runner, goLang: p}, nil
}

// Close releases engine resources.
func (e *Engine) Close() {
	e.runner.Close()
	e.goLang.Close()
}

// Rules returns rule metadata for reports.
func (e *Engine) Rules() []rules.Meta { return rules.GoMetas(rules.GoRules()) }

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
			pp, err := parser.New(parser.LangGo)
			if err != nil {
				return
			}
			defer pp.Close()
			for path := range jobs {
				results <- e.checkFile(pp, path)
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
func (e *Engine) checkFile(pp *parser.Parser, path string) fileResult {
	var res fileResult
	src, err := os.ReadFile(path)
	if err != nil {
		res.findings = append(res.findings, model.Finding{
			RuleID: "read-error", Severity: model.SevWarning, Source: model.SrcRule,
			File: path, StartLine: 1, EndLine: 1,
			Message: "could not read file: " + err.Error(),
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

	stats := metrics.AnalyzeFile(path, src, tree)
	res.metrics = e.metricResults(stats)
	res.findings = append(res.findings, e.metricFindings(stats)...)
	res.findings = append(res.findings, e.runner.Run(path, src, tree, e.cfg)...)
	return res
}

// thresholds resolves the effective warn/error thresholds for a metric.
func (e *Engine) thresholds(metricID string) (warn, errAt int) {
	def := goMetricDefaults[metricID]
	warn, errAt = def.Warn, def.Error
	w, er, enabled := e.cfg.MetricThresholds(string(parser.LangGo), metricID)
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
func (e *Engine) metricEnabled(metricID string) bool {
	_, _, enabled := e.cfg.MetricThresholds(string(parser.LangGo), metricID)
	return enabled == nil || *enabled
}

// metricResults records every measured value (for trend storage and the
// metrics command), regardless of whether a threshold was crossed.
func (e *Engine) metricResults(stats *metrics.FileStats) []model.MetricResult {
	if !e.metricEnabled(metrics.MetricFileLength) {
		return nil
	}
	warn, errAt := e.thresholds(metrics.MetricFileLength)
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
			if !e.metricEnabled(metric) {
				continue
			}
			w, er := e.thresholds(metric)
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
func (e *Engine) metricFindings(stats *metrics.FileStats) []model.Finding {
	var findings []model.Finding
	add := func(metric, scope string, value int) {
		if !e.metricEnabled(metric) {
			return
		}
		warn, errAt := e.thresholds(metric)
		if warn == 0 && errAt == 0 {
			return
		}
		var sev model.Severity
		var which string
		switch {
		case errAt > 0 && value > errAt:
			sev, which = model.SevError, "error"
		case warn > 0 && value > warn:
			sev, which = model.SevWarning, "warn"
		default:
			return
		}
		line := 1
		end := 1
		if scope != "" {
			for _, fn := range stats.Functions {
				if fn.Name == scope {
					line, end = fn.StartLine, fn.EndLine
					break
				}
			}
		}
		findings = append(findings, model.Finding{
			RuleID: metric, Severity: sev, Source: model.SrcMetric, File: stats.File,
			StartLine: line, EndLine: end,
			Message: fmt.Sprintf("%s of %q is %d (threshold %s %d)",
				metric, scopeName(scope, stats.File), value, which, map[bool]int{true: errAt, false: warn}[sev == model.SevError]),
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

func scopeName(scope, file string) string {
	if scope == "" {
		return file
	}
	return scope
}
