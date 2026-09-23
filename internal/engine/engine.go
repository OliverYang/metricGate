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
	"sort"
	"time"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/arch"
	"metricgate/internal/clones"
	"metricgate/internal/config"
	"metricgate/internal/metrics"
	"metricgate/internal/model"
	"metricgate/internal/parser"
	"metricgate/internal/rules"
)

// Engine runs the checker over a directory tree, with one rule runner per
// registered language.
type Engine struct {
	cfg     *config.Config
	langs   map[parser.LanguageID]*parser.Parser
	runners map[parser.LanguageID]*rules.Runner
	packs   []*rules.Rule // built-in rules plus the policy bans
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
		pack := rules.RulesFor(id, cfg)
		runner, err := rules.NewRunner(string(id), p.Lang, pack)
		if err != nil {
			p.Close()
			e.Close()
			return nil, fmt.Errorf("compile %s rules: %w", id, err)
		}
		e.langs[id] = p
		e.runners[id] = runner
		e.packs = append(e.packs, pack...)
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

type fileResult struct {
	findings []model.Finding
	metrics  []model.MetricResult
	ignored  int
	sups     []suppression // the file's own ignore directives
	archFile arch.File     // package + imports, for the dependency graph
	cloneFns []clones.Func // normalised function fingerprints, for clone grouping
	types    arch.TypeKinds // type census of the package, for abstractness
}

// Rules returns rule metadata for reports: built-in rules, the bans compiled
// from the policy, and the architecture checks when the policy declares them.
func (e *Engine) Rules() []rules.Meta {
	out := append(rules.Metas(e.packs), archRuleMetas(e.cfg)...)
	out = append(out, cloneRuleMeta(e.cfg))
	return out
}

// Run checks every supported file under root and returns the summary.
func (e *Engine) Run(root string) (*model.RunSummary, error) {
	start := time.Now()
	files, err := e.collect(root)
	if err != nil {
		return nil, err
	}
	summary := &model.RunSummary{Files: len(files)}

	results := e.runWorkers(root, files)

	var archFiles []arch.File
	var cloneFns []clones.Func
	typeKinds := map[string]arch.TypeKinds{}
	supsByFile := foldResults(summary, results, &archFiles, &cloneFns, typeKinds)
	e.checkArchitecture(summary, archFiles, typeKinds, supsByFile)
	e.checkClones(summary, cloneFns, supsByFile)

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
func (e *Engine) checkFile(w *worker, root, path string) fileResult {
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

	res.findings, res.metrics, res.ignored, res.sups = e.analyze(lang, path, src, tree)
	res.archFile = archFileOf(lang, root, path, src, tree)
	res.cloneFns = clones.Extract(lang, path, src, tree, e.cfg.ClonePolicy())
	res.types = typeCensus(lang, src, tree)
	return res
}

// analyze computes findings and metric results for one parsed file with
// suppressions applied, and returns the file's suppression regions so
// whole-graph stages (architecture) can honour them too.
func (e *Engine) analyze(lang parser.LanguageID, name string, src []byte, tree *sitter.Tree) (findings []model.Finding, mets []model.MetricResult, ignored int, sups []suppression) {
	stats := metrics.AnalyzeFile(name, src, tree, lang)
	mets = e.metricResults(lang, stats)
	findings = append(findings, e.metricFindings(lang, stats)...)
	findings = append(findings, e.runners[lang].Run(name, src, tree, e.cfg)...)
	sups = buildSuppressions(lang, src, tree.RootNode())
	if len(sups) > 0 {
		findings, ignored = suppress(findings, sups)
	}
	return findings, mets, ignored, sups
}

// thresholds resolves the effective warn/error thresholds for a metric.
func (e *Engine) thresholds(lang parser.LanguageID, metricID string) (warn, errAt int) {
	def := metrics.DefaultThresholds[metricID]
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
	for _, c := range stats.Classes {
		for metric, value := range map[string]int{
			metrics.MetricWMC:  c.WMC,
			metrics.MetricNOM:  c.NOM,
			metrics.MetricLCOM: c.LCOM,
			metrics.MetricCBO:  c.CBO,
			metrics.MetricDIT:  c.DIT,
		} {
			if !e.metricEnabled(lang, metric) {
				continue
			}
			w, er := e.thresholds(lang, metric)
			out = append(out, model.MetricResult{
				Metric: metric, File: stats.File, Scope: c.Name,
				Value: value, Warn: w, Err: er,
			})
		}
	}
	return out
}

// metricVerdict resolves the severity a measured value earns for a metric
// and the threshold that fired; ok is false when nothing is crossed. A
// value crosses a threshold when strictly greater than it.
func (e *Engine) metricVerdict(lang parser.LanguageID, metric string, value int) (sev model.Severity, threshold int, ok bool) {
	warn, errAt := e.thresholds(lang, metric)
	if warn == 0 && errAt == 0 {
		return "", 0, false
	}
	switch {
	case errAt > 0 && value > errAt:
		return model.SevError, errAt, true
	case warn > 0 && value > warn:
		return model.SevWarning, warn, true
	default:
		return "", 0, false
	}
}

// scopeRange returns the reported range of a named function or class; a
// file-scoped metric (empty scope) reports line 1.
func scopeRange(stats *metrics.FileStats, scope string) (line, end int) {
	line, end = 1, 1
	if scope == "" {
		return line, end
	}
	for _, fn := range stats.Functions {
		if fn.Name == scope {
			return fn.StartLine, fn.EndLine
		}
	}
	for _, c := range stats.Classes {
		if c.Name == scope {
			return c.StartLine, c.EndLine
		}
	}
	return line, end
}

// metricFindings turns threshold crossings into findings, anchored on the
// measured scope (function, class, or the file).
func (e *Engine) metricFindings(lang parser.LanguageID, stats *metrics.FileStats) []model.Finding {
	var findings []model.Finding
	add := func(metric, scope string, value int) {
		if !e.metricEnabled(lang, metric) {
			return
		}
		sev, threshold, crossed := e.metricVerdict(lang, metric, value)
		if !crossed {
			return
		}
		line, end := scopeRange(stats, scope)
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
	for _, c := range stats.Classes {
		add(metrics.MetricWMC, c.Name, c.WMC)
		add(metrics.MetricNOM, c.Name, c.NOM)
		add(metrics.MetricLCOM, c.Name, c.LCOM)
		add(metrics.MetricCBO, c.Name, c.CBO)
		add(metrics.MetricDIT, c.Name, c.DIT)
	}
	return findings
}

// thresholdsByLang resolves a language-agnostic metric's thresholds as the
// most permissive configured value across the known languages, so a policy
// only has to set it once.
func (e *Engine) thresholdsByLang(langs ...string) (warn, errAt int) {
	for _, lang := range langs {
		w, er, enabled := e.cfg.MetricThresholds(lang, "pkg-coupling")
		if enabled != nil && !*enabled {
			continue
		}
		if w > warn {
			warn = w
		}
		if er > errAt {
			errAt = er
		}
	}
	return warn, errAt
}

// CheckSource analyses in-memory source as if it were the virtual file name
// (the extension selects the language) and returns its findings. It exists
// for programmatic callers such as the MCP server: the LLM's edited snippet
// can be checked without writing it to disk.
func (e *Engine) CheckSource(name string, src []byte) ([]model.Finding, error) {
	lang, ok := parser.LanguageForFile(filepath.Base(name))
	if !ok {
		return nil, fmt.Errorf("unsupported language for %q (use .go or .java)", name)
	}
	pp, err := parser.New(lang)
	if err != nil {
		return nil, err
	}
	defer pp.Close()
	tree, err := pp.Parse(src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	var findings []model.Finding
	if tree.RootNode().HasError() {
		findings = append(findings, model.Finding{
			RuleID: "parse-error", Severity: model.SevInfo, Source: model.SrcRule,
			File: name, StartLine: 1, EndLine: 1,
			Message: "source has syntax errors; results may be incomplete",
		})
	}
	findings, _, _, _ = e.analyze(lang, name, src, tree)
	model.SortFindings(findings)
	return findings, nil
}
