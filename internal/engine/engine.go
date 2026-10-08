// Package engine orchestrates one metricgate run: collect files, parse them
// in parallel, run metrics and rules, and apply policy thresholds. It is a
// pure library: no printing, no writing, no reading of configuration from
// disk — the CLI and the future service are thin shells around it.
package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/arch"
	"metricgate/internal/clones"
	"metricgate/internal/config"
	"metricgate/internal/deps"
	"metricgate/internal/metrics"
	"metricgate/internal/model"
	"metricgate/internal/parser"
	"metricgate/internal/regress"
	"metricgate/internal/rules"
)

// Engine runs the checker over a directory tree, with one rule runner per
// registered language.
type Engine struct {
	cfg     *config.Config
	langs   map[parser.LanguageID]*parser.Parser
	runners map[parser.LanguageID]*rules.Runner
	// depAllow is the declared-dependency allowlist for the current run root,
	// the one rule input that comes from the repository rather than the policy.
	depAllow []string
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
	path       string // reported path, so a parse error can name the file
	parseError string // non-empty when the tree had syntax errors
	findings   []model.Finding
	metrics    []model.MetricResult
	ignored    int
	// ignoredByRule is `ignored` split by rule id, so the policy audit can name
	// the rule a project keeps silencing.
	ignoredByRule map[string]int
	sups          []suppression  // the file's own ignore directives
	archFile      arch.File      // package + imports, for the dependency graph
	cloneFns      []clones.Func  // normalised function fingerprints, for clone grouping
	types         arch.TypeKinds // type census of the package, for abstractness
}

// parseErrorFinding is the info-level note a file with syntax errors carries:
// the partial results are still reported, but the caller is told they may be
// incomplete. It must be appended *after* analyze, which replaces the whole
// finding slice — appending before it silently discards the note.
func parseErrorFinding(file, message string) model.Finding {
	return model.Finding{
		RuleID: "parse-error", Severity: model.SevInfo, Source: model.SrcRule,
		File: file, StartLine: 1, EndLine: 1, Message: message,
	}
}

// Rules returns rule metadata for reports: built-in rules, the bans compiled
// from the policy, and the architecture checks when the policy declares them.
//
// The result is ordered by language (in parser.Supported order) and then by
// the pack's own order, so two engines built from the same policy produce the
// same catalogue.
func (e *Engine) Rules() []rules.Meta { return Catalog(e.cfg) }

// Catalog is the rule catalogue for a policy, without building an engine.
//
// It exists so that a command which only wants to *describe* the policy —
// rather than run it — reads the same list the run itself would, instead of a
// second table that can drift. Two of those existed and disagreed: the
// explain command enumerated only the per-language YAML keys, so every
// whole-graph rule was missing from the list users were told was complete.
func Catalog(cfg *config.Config) []rules.Meta {
	var out []rules.Meta
	for _, id := range parser.Supported() {
		for _, m := range rules.Metas(rules.RulesFor(id, cfg)) {
			m.Language = string(id)
			out = append(out, m)
		}
		out = append(out, metricMetas(string(id))...)
	}
	out = append(out, archRuleMetas(cfg)...)
	out = append(out, cloneRuleMeta(cfg))
	out = append(out, regress.Meta())
	return out
}

// metricMetas are the threshold rules of one language.
//
// Metrics are enforced by the engine, not by a tree-sitter query, so no rule
// pack contains them — and a catalogue built from the packs alone was missing
// exactly the rules that produce most findings. SARIF had been papering over
// this by inventing a title for any rule id it did not recognise.
func metricMetas(lang string) []rules.Meta {
	ids := metrics.IDsFor(lang)
	out := make([]rules.Meta, 0, len(ids))
	for _, id := range ids {
		title, ok := metrics.Titles[id]
		if !ok {
			title = id
		}
		out = append(out, rules.Meta{ID: id, Title: title, Language: lang})
	}
	return out
}

// RunResult is one run's summary plus the facts a policy audit needs that are
// not findings: the package nodes the dependency graph was built from.
type RunResult struct {
	Summary  *model.RunSummary
	Packages []string
	// Languages is the set of languages this run actually scanned. A policy
	// audit needs it: a rule for a language with no files is not silent, it is
	// inapplicable, and "0 findings" must not be read as "clean".
	Languages []string
}

// Run checks every supported file under root and returns the summary.
func (e *Engine) Run(root string) (*model.RunSummary, error) {
	res, err := e.RunDetailed(root)
	if err != nil {
		return nil, err
	}
	return res.Summary, nil
}

// RunDetailed is Run plus the dependency-graph facts. It is the same single
// pass: the packages come from the graph the architecture stage already builds,
// so running a policy audit costs no second parse.
func (e *Engine) RunDetailed(root string) (*RunResult, error) {
	start := time.Now()
	e.depAllow = deps.ForRoot(root, e.cfg).Prefixes()
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
	packages := e.checkArchitecture(summary, archFiles, typeKinds, supsByFile)
	e.checkClones(summary, cloneFns, supsByFile)

	model.SortFindings(summary.Findings)
	model.SortMetrics(summary.Metrics)
	ensureEvidence(summary.Findings)
	summary.Duration = time.Since(start)
	return &RunResult{Summary: summary, Packages: packages, Languages: scannedLanguages(files)}, nil
}

// scannedLanguages reports which languages a file list covers, in
// parser.Supported order, so the result never depends on walk order.
func scannedLanguages(files []string) []string {
	seen := map[string]bool{}
	for _, f := range files {
		if lang, ok := parser.LanguageForFile(filepath.Base(f)); ok {
			seen[string(lang)] = true
		}
	}
	var out []string
	for _, id := range parser.Supported() {
		if seen[string(id)] {
			out = append(out, string(id))
		}
	}
	return out
}

// ensureEvidence gives every finding an anchor status. Rules that matched an
// AST node already set exact evidence; everything else (metrics, parse/read
// errors, architecture and clone findings) is honestly marked line_only, so a
// consumer can distinguish "byte-proof" from "location only" rather than
// guessing from a missing field.
func ensureEvidence(findings []model.Finding) {
	for i := range findings {
		if findings[i].Evidence == nil {
			findings[i].Evidence = &model.Evidence{Anchor: model.AnchorLineOnly}
		}
	}
}

// collect returns the selected filesystem paths using the same decisions
// exposed by Preview. The report paths are relative; the worker paths remain
// rooted as before.
func (e *Engine) collect(root string) ([]string, error) {
	_, selected, err := e.scanSelection(root)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(selected))
	for _, f := range selected {
		files = append(files, f.absolute)
	}
	return files, nil
}

// checkFile parses and analyses one file. Parse errors are reported as an
// info finding instead of failing the run: partial trees still yield useful
// findings, which matters when checking freshly generated LLM output.
func (e *Engine) checkFile(w *worker, root, path string) fileResult {
	var res fileResult
	res.path = path
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
		res.findings = append(res.findings, parseErrorFinding(path, err.Error()))
		return res
	}
	tree, err := pp.Parse(src)
	if err != nil {
		res.findings = append(res.findings, parseErrorFinding(path, err.Error()))
		return res
	}
	defer tree.Close()

	if tree.RootNode().HasError() {
		res.parseError = "file has syntax errors; results may be incomplete"
	}

	res.findings, res.metrics, res.ignored, res.ignoredByRule, res.sups = e.analyze(lang, path, src, tree)
	if res.parseError != "" {
		res.findings = append(res.findings, parseErrorFinding(path, res.parseError))
	}
	res.archFile = archFileOf(lang, root, path, src, tree)
	res.cloneFns = clones.Extract(lang, path, src, tree, e.cfg.ClonePolicy())
	res.types = typeCensus(lang, src, tree)
	return res
}

// analyze computes findings and metric results for one parsed file with
// suppressions applied, and returns the file's suppression regions so
// whole-graph stages (architecture) can honour them too.
//
// The suppression result comes back twice — as a total and split by rule —
// because `check` reports the total and the policy audit reports the split.
func (e *Engine) analyze(lang parser.LanguageID, name string, src []byte, tree *sitter.Tree) (findings []model.Finding, mets []model.MetricResult, ignored int, ignoredByRule map[string]int, sups []suppression) {
	stats := metrics.AnalyzeFile(name, src, tree, lang)
	mets = e.metricResults(lang, stats)
	findings = append(findings, e.metricFindings(lang, stats)...)
	findings = append(findings, e.runners[lang].Run(name, src, tree, e.policy())...)
	sups = buildSuppressions(lang, src, tree.RootNode())
	if len(sups) > 0 {
		findings, ignored, ignoredByRule = suppress(findings, sups)
	}
	return findings, mets, ignored, ignoredByRule, sups
}

// thresholds resolves the effective warn/error thresholds for a metric.
func (e *Engine) thresholds(lang parser.LanguageID, metricID string) (warn, errAt int) {
	return EffectiveThresholds(e.cfg, string(lang), metricID)
}

// EffectiveThresholds resolves a metric's warn/error thresholds the way a run
// does: built-in defaults first, policy overrides on top.
//
// It is exported because a rule that fires depends on these numbers, so
// anything that asks "can this rule fire?" — the policy audit — has to ask it
// here rather than keep a second copy of the resolution order. Reading the
// policy alone gives 0 for every metric the policy never mentions, which made
// every default-thresholded metric look unable to fire.
func EffectiveThresholds(cfg *config.Config, lang, metricID string) (warn, errAt int) {
	def := metrics.DefaultThresholds[metricID]
	warn, errAt = def.Warn, def.Error
	w, er, enabled := cfg.MetricThresholds(lang, metricID)
	if enabled != nil && !*enabled {
		return 0, 0
	}
	// An explicit `warn: off` / `error: off` disables that severity level
	// entirely (resolved to 0, which the verdict logic reads as "cannot fire");
	// it takes precedence over any value — including the default — for that
	// level only. The other level keeps default-or-override semantics.
	warnOff, errOff := cfg.MetricWarnOff(lang, metricID), cfg.MetricErrorOff(lang, metricID)
	if warnOff {
		warn = 0
	} else if w > 0 {
		warn = w
	}
	if errOff {
		errAt = 0
	} else if er > 0 {
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
		Value: stats.Lines, Warn: warn, Err: errAt, StartLine: 1, EndLine: 1,
	}}
	for _, fn := range stats.Functions {
		for metric, value := range map[string]int{
			metrics.MetricCyclomatic: fn.Cyclomatic,
			metrics.MetricCognitive:  fn.Cognitive,
			metrics.MetricFuncLength: fn.Length,
			metrics.MetricNesting:    fn.MaxNesting,
			metrics.MetricParams:     fn.Params,
			metrics.MetricNPath:      int(fn.NPath),
			metrics.MetricFanOut:     fn.FanOut,
			metrics.MetricBoolParams: fn.BoolParams,
		} {
			if !e.metricEnabled(lang, metric) {
				continue
			}
			w, er := e.thresholds(lang, metric)
			out = append(out, model.MetricResult{
				Metric: metric, File: stats.File, Scope: fn.Name,
				Value: value, Warn: w, Err: er,
				StartLine: fn.StartLine, EndLine: fn.EndLine,
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
				StartLine: c.StartLine, EndLine: c.EndLine,
			})
		}
	}
	if out == nil && !e.metricEnabled(lang, metrics.MetricMaintainability) {
		return nil
	}
	// MI is "higher is better", the pipeline is "value > threshold is bad",
	// so we snapshot the debt (100-MI) and threshold that. MI is 0 when the
	// file has no functions, which means "not measured" — not "worst
	// possible" — so a type-only file is left out rather than recorded as
	// debt 100 (which would rank a clean 10-line file as unmaintainable).
	if mi := e.metricEnabled(lang, metrics.MetricMaintainability); mi && stats.MI > 0 {
		w, er := e.thresholds(lang, metrics.MetricMaintainability)
		debt := 100 - int(stats.MI+0.5)
		out = append(out, model.MetricResult{
			Metric: metrics.MetricMaintainability, File: stats.File, Scope: "",
			Value: debt, Warn: w, Err: er, StartLine: 1, EndLine: 1,
		})
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
		add(metrics.MetricNPath, fn.Name, int(fn.NPath))
		add(metrics.MetricFanOut, fn.Name, fn.FanOut)
		add(metrics.MetricBoolParams, fn.Name, fn.BoolParams)
	}
	for _, c := range stats.Classes {
		add(metrics.MetricWMC, c.Name, c.WMC)
		add(metrics.MetricNOM, c.Name, c.NOM)
		add(metrics.MetricLCOM, c.Name, c.LCOM)
		add(metrics.MetricCBO, c.Name, c.CBO)
		add(metrics.MetricDIT, c.Name, c.DIT)
	}
	if e.metricEnabled(lang, metrics.MetricMaintainability) && stats.MI > 0 {
		add(metrics.MetricMaintainability, stats.MIScope, 100-int(stats.MI+0.5))
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
	e.ensureDeps()
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
	parseError := ""
	if tree.RootNode().HasError() {
		parseError = "source has syntax errors; results may be incomplete"
	}
	findings, _, _, _, _ = e.analyze(lang, name, src, tree)
	if parseError != "" {
		findings = append(findings, parseErrorFinding(name, parseError))
	}
	model.SortFindings(findings)
	ensureEvidence(findings)
	return findings, nil
}
