// Command metricgate is the CLI shell around the checker engine. All logic
// lives in internal packages so the same engine can be exposed later as a
// service or embedded as a library.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"metricgate/internal/clones"
	"metricgate/internal/config"
	"metricgate/internal/diff"
	"metricgate/internal/engine"
	"metricgate/internal/facts"
	"metricgate/internal/gatecheck"
	"metricgate/internal/git"
	"metricgate/internal/metrics"
	"metricgate/internal/model"
	"metricgate/internal/repomap"
	"metricgate/internal/report"
	"metricgate/internal/scorecard"
	"metricgate/internal/sources"
	"metricgate/internal/store"
	"metricgate/internal/symbols"
)

const toolVersion = "0.1.0"

// commands maps each subcommand to its entry point. A table keeps main a
// three-branch dispatcher no matter how many subcommands accumulate, instead
// of a switch that grows one path per command.
var commands = map[string]func([]string){
	"check":       cmdCheck,
	"metrics":     cmdMetrics,
	"stats":       cmdStats,
	"context":     cmdContext,
	"bundle":      cmdBundle,
	"symbols":     cmdSymbols,
	"impact":      cmdImpact,
	"map":         cmdMap,
	"rules":       cmdRules,
	"constraints": cmdConstraints,
	"rules-gaps":  cmdRulesGaps,
	"explain":     cmdExplain,
	"api":         cmdAPI,
	"arch":        cmdArch,
	"nearclones":  cmdNearClones,
	"clonedrift":  cmdClonedrift,
	"hotspots":    cmdHotspots,
	"trends":      cmdTrends,
	"calls":       cmdCalls,
	"init":        cmdInit,
	"version":     cmdVersion,
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	run, ok := commands[os.Args[1]]
	if !ok {
		usage()
		os.Exit(2)
	}
	run(os.Args[2:])
}

// cmdVersion prints the tool version.
func cmdVersion([]string) {
	fmt.Println("metricgate", toolVersion)
}

func usage() {
	fmt.Fprintf(os.Stderr, `metricgate %s — deterministic code metrics and constraint gates

usage:
  metricgate init [file] [flags]      write a starter policy (default policy.yaml)
                                      -preset NAME starts from a built-in gate
                                      tuning (e.g. agentic)
  metricgate bundle [path] [flags]     deterministic, budgeted review input for a
                                      host agent (diff + constraints + facts)
  metricgate check [path] [flags]     run rules + metrics, enforce the gate
  metricgate check -diff [-base ref]  only fail on violations in changed lines
  metricgate check -diff -regress     also report measurements that got worse
                                      than -base without crossing a threshold
  metricgate context [path] [flags]   dense constraint pack for LLM prompts
  metricgate constraints digest [-pkg P]  task-scoped constraint digest (-format json = provenance)
  metricgate metrics [path] [flags]   print the metric table only
  metricgate stats [path] [flags]     metric distributions (min/p50/p90/p95/max)
                                      plus the worst scopes per metric
  metricgate symbols [path] -q NAME   look a symbol up in the cross-file
                                      definition index (near-miss suggestions
                                      when the exact name is unknown)
  metricgate impact [path] -q NAME    what uses this symbol: every call and type
                                      reference, and the files it would affect
  metricgate map [path] [flags]       rank files by reference importance: the
                                      whole-repo "read these first" list
  metricgate rules explain list       every rule/metric and where its value came
                                      from (builtin | policy_file | preset:NAME)
  metricgate rules explain <ruleID>   the effective config of one rule or metric
  metricgate rules presets            the built-in gate presets and every value
                                      each one changes
  metricgate rules gaps [path]        audit policy completeness: which rules can
                                      fire, which are silent, and which packages
                                      no declared layer covers
  metricgate explain [path]           preview what would be scanned (no rule execution)
  metricgate api snapshot -out F      capture the exported API surface
  metricgate api diff -base F [path]  breaking-change gate against a snapshot
                                      (removed/changed signatures exit 1)
  metricgate arch snapshot -out F     capture the package dependency shape
  metricgate arch diff -base F [path] shape-drift gate against a snapshot
                                      (any added/removed edge exits 1)
  metricgate nearclones [path]        functions sharing >=80%% of their normalised
                                      tokens (copy+rename+small edit)
  metricgate hotspots [path]          churn × maintainability-debt hotspots
  metricgate clonedrift [path]         flag a change that edits one copy of a clone
                                      but not its twin (needs -base, default HEAD)
  metricgate trends [flags]           severity trend + top rules from the local
                                      history.db (P2 v1; needs store.enabled)
  metricgate calls [flags]            reduce the local MCP call log to call
                                      counts, intercept rate and fix rate — the
                                      evidence the gate is used in an agent loop
  metricgate version                  print the version
`, toolVersion)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "metricgate:", err)
	os.Exit(2)
}

// dispatch runs the subcommand handler named by args[0]. It is the one shape
// every multi-subcommand command shares, so the usage/unknown-subcommand
// handling lives in a single place instead of being copied per command.
func dispatch(name, usage string, args []string, subs map[string]func([]string)) {
	if len(args) == 0 {
		fail(fmt.Errorf("%s needs a subcommand: %s", name, usage))
	}
	run, ok := subs[args[0]]
	if !ok {
		fail(fmt.Errorf("unknown %s subcommand %q (want %s)", name, args[0], usage))
	}
	run(args[1:])
}

// loadPolicy loads policy.yaml, falling back to the built-in default policy
// when the file does not exist.
func loadPolicy(path string) (*config.Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "note: no policy file at %s, using built-in defaults\n", path)
		return config.Default()
	}
	return config.LoadFile(path)
}

// gitCommit returns the short HEAD commit, or "" outside a git repo.
func gitCommit() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// runEngine loads the policy, builds the engine and runs it over root.
func runEngine(configPath, root string) (*config.Config, *engine.Engine, *model.RunSummary) {
	cfg, err := loadPolicy(configPath)
	if err != nil {
		fail(err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		fail(err)
	}
	summary, err := eng.Run(root)
	if err != nil {
		eng.Close()
		fail(err)
	}
	return cfg, eng, summary
}

// parseArgs parses a command's arguments, accepting flags on either side of
// the positional path: `check . -format json` behaves like
// `check -format json .`. Go's flag package stops at the first non-flag
// argument, which silently drops trailing flags — a trap for a CLI whose
// documented usage is `check [path] [flags]`.
func parseArgs(set *flag.FlagSet, args []string) {
	flags, rest := splitFlags(set, args)
	_ = set.Parse(append(flags, rest...))
}

// splitFlags moves flag tokens (with their values) ahead of positionals.
func splitFlags(set *flag.FlagSet, args []string) (flags, rest []string) {
	takesValue := func(name string) bool {
		f := set.Lookup(name)
		if f == nil {
			return false // unknown flag: let flag.Parse report it
		}
		b, ok := f.Value.(interface{ IsBoolFlag() bool })
		return !ok || !b.IsBoolFlag()
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			// Keep the terminator: flag.Parse must treat the tail as paths.
			return flags, append([]string{"--"}, args[i+1:]...)
		}
		if len(a) < 2 || a[0] != '-' {
			rest = append(rest, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue // -flag=value carries its own value
		}
		if takesValue(name) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return flags, rest
}

func cmdCheck(args []string) {
	set := flag.NewFlagSet("check", flag.ExitOnError)
	configPath := set.String("config", "policy.yaml", "policy file")
	sarifOut := set.String("sarif", "", "write a SARIF 2.1.0 report to this file")
	diffMode := set.Bool("diff", false, "only report findings on lines changed vs -base (git)")
	base := set.String("base", "HEAD", "git ref to diff against (with -diff)")
	regressMode := set.Bool("regress", false, "with -diff, also report measurements that got worse than -base (warning)")
	scorecardMode := set.Bool("scorecard", false, "with -diff, print a deterministic change scorecard for the diff (report-only)")
	impactMode := set.Bool("impact-scope", false, "with -diff, gate the blast radius of changed symbols against the policy cap (LC-403, error)")
	testsMode := set.Bool("tests-touched", false, "with -diff, flag logic changes that touch no test file (LC-404, warning)")
	format := set.String("format", "text", "console output format: text|json")
	preview := set.Bool("preview", false, "show path selection decisions without parsing or running checks")
	maxCognitive := set.Int("max-cognitive", 0, "override the cognitive-complexity error threshold for every language")
	maxCyclomatic := set.Int("max-cyclomatic", 0, "override the cyclomatic-complexity error threshold for every language")
	parseArgs(set, args)
	// Human-readable lines go to stderr in json mode so stdout stays
	// machine-parseable.
	var human io.Writer = os.Stdout
	if *format == "json" {
		human = os.Stderr
	}
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}

	if *preview {
		cmdCheckPreview(*configPath, root, *format, human)
		return
	}
	cfg, err := loadPolicy(*configPath)
	if err != nil {
		fail(err)
	}
	if *maxCognitive > 0 || *maxCyclomatic > 0 {
		applyThresholdOverrides(cfg, *maxCognitive, *maxCyclomatic)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		fail(err)
	}
	defer eng.Close()
	summary, err := eng.Run(root)
	if err != nil {
		fail(err)
	}
	if *diffMode {
		summary = runDiffMode(cfg, root, *base, diffExtrasOpts{base: *base, wantRegress: *regressMode, wantScorecard: *scorecardMode, wantImpact: *impactMode, wantTests: *testsMode, jsonMode: *format == "json"}, summary, human)
	} else if *regressMode || *scorecardMode || *impactMode || *testsMode {
		fail(fmt.Errorf("-regress/-scorecard/-impact-scope/-tests-touched need -diff: they describe the working tree against -base"))
	}
	emitReports(summary, cfg, reportFlags{format: *format, sarif: *sarifOut}, eng, human)
	enforceGate(summary, cfg, human)
}

// runDiffMode filters the summary to changed lines and applies the optional
// -diff add-ons. It returns the (possibly replaced) filtered summary.
func runDiffMode(cfg *config.Config, root, base string, add diffExtrasOpts, summary *model.RunSummary, human io.Writer) *model.RunSummary {
	changed, err := git.ChangedLines("", git.ResolveBase("", base))
	if err != nil {
		fail(err)
	}
	var hidden int
	summary, hidden = engine.FilterSummary(summary, changed)
	fmt.Fprintf(human, "diff-only: base=%s, %d new findings, %d pre-existing hidden\n\n", base, len(summary.Findings), hidden)
	if err := diffExtras(cfg, root, summary, changed, add, human); err != nil {
		fail(err)
	}
	return summary
}

// diffExtrasOpts carries the -diff add-on switches and their shared inputs.
type diffExtrasOpts struct {
	base          string
	wantRegress   bool
	wantScorecard bool
	wantImpact    bool
	wantTests     bool
	jsonMode      bool
}

// diffExtras runs the -diff-only add-ons: metric regressions and the change
// scorecard. Both are report-only — they never touch gate semantics.
func diffExtras(cfg *config.Config, root string, summary *model.RunSummary, changed map[string][]diff.Range, opt diffExtrasOpts, human io.Writer) error {
	if opt.wantRegress {
		n, err := applyRegressions(cfg, opt.base, root, summary, changed)
		if err != nil {
			return err
		}
		fmt.Fprintf(human, "regressions: %d measurement(s) got worse than %s\n\n", n, opt.base)
	}
	if opt.wantScorecard {
		card := scorecard.Build(summary, scorecard.Options{Changed: changed, Root: root, ScanStubs: true})
		scorecard.Print(human, card, opt.jsonMode)
		fmt.Fprintln(human)
	}
	return applyGateChecks(cfg, root, summary, changed, opt, human)
}

// applyGateChecks runs the two gate-ized acceptance checks — impact scope
// (LC-403) and tests-touched (LC-404) — and appends their findings like
// -regress does. The CLI flags activate; policy can veto (enabled:false)
// or tune (cap, severity).
func applyGateChecks(cfg *config.Config, root string, summary *model.RunSummary, changed map[string][]diff.Range, opt diffExtrasOpts, human io.Writer) error {
	if !opt.wantImpact && !opt.wantTests {
		return nil
	}
	opts := gatecheck.Resolve(cfg)
	opts.Changed = changed
	opts.Root = root
	if !opt.wantImpact {
		opts.WantImpact = false
	}
	if !opt.wantTests {
		opts.WantTests = false
	}
	if !opts.WantImpact && !opts.WantTests {
		return nil
	}
	_, defs, uses, err := facts.CollectWithRefs(root)
	if err != nil {
		return err
	}
	findings := gatecheck.BlastRadiusCheck(defs, uses, opts)
	findings = append(findings, gatecheck.TestsTouchedCheck(opts)...)
	if len(findings) == 0 {
		fmt.Fprintln(human, "gate-checks: impact-scope and tests-touched clean")
		return nil
	}
	summary.Findings = append(summary.Findings, findings...)
	model.SortFindings(summary.Findings)
	nImpact, nTests := 0, 0
	for _, f := range findings {
		if f.RuleID == gatecheck.RuleIDImpactScope {
			nImpact++
		} else {
			nTests++
		}
	}
	fmt.Fprintf(human, "gate-checks: impact-scope %d over cap, tests-touched %d\n\n", nImpact, nTests)
	return nil
}

// applyThresholdOverrides overrides the error threshold of the cognitive and
// cyclomatic metrics in every configured language section, so
// `check -max-cognitive N` needs no policy edit. A zero argument means
// "leave the policy value alone".
func applyThresholdOverrides(cfg *config.Config, maxCognitive, maxCyclomatic int) {
	for lang, sec := range cfg.Languages {
		if sec.Metrics == nil {
			sec.Metrics = map[string]config.MetricConfig{}
		}
		if maxCognitive > 0 {
			sec.Metrics["cognitive-complexity"] = config.MetricConfig{Error: maxCognitive}
		}
		if maxCyclomatic > 0 {
			sec.Metrics["cyclomatic-complexity"] = config.MetricConfig{Error: maxCyclomatic}
		}
		cfg.Languages[lang] = sec
	}
}

// reportFlags carries the check-command output switches.
type reportFlags struct {
	format string
	sarif  string
}

// emitReports writes the console report, the optional SARIF file and stores
// the run when the policy enables the trend store.
func emitReports(summary *model.RunSummary, cfg *config.Config, flags reportFlags, eng *engine.Engine, human io.Writer) {
	switch flags.format {
	case "json":
		data, err := report.JSON(summary)
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(append(data, '\n'))
	default:
		fmt.Print(report.Text(summary, cfg, toolVersion))
	}
	if flags.sarif != "" {
		data, err := report.SARIF(summary, toolVersion, eng.Rules())
		if err != nil {
			fail(err)
		}
		if err := os.WriteFile(flags.sarif, data, 0o644); err != nil {
			fail(err)
		}
		fmt.Fprintf(human, "sarif written to %s\n", flags.sarif)
	}
	if cfg.Store.Enabled {
		st, err := store.Open(cfg.Store.Path)
		if err != nil {
			fail(err)
		}
		id, err := st.SaveRun(summary, toolVersion, gitCommit())
		st.Close()
		if err != nil {
			fail(err)
		}
		fmt.Fprintf(human, "run #%d stored in %s\n", id, cfg.Store.Path)
	}
}

// enforceGate exits 1 when findings at or above the policy threshold exist.
func enforceGate(summary *model.RunSummary, cfg *config.Config, human io.Writer) {
	failOn := cfg.FailOn()
	trips := summary.Count(model.SevError)
	if failOn == model.SevWarning {
		trips += summary.Count(model.SevWarning)
	}
	if trips > 0 {
		fmt.Fprintf(human, "gate: FAILED (fail_on=%s, %d findings)\n", failOn, trips)
		os.Exit(1)
	}
	fmt.Fprintf(human, "gate: PASSED (fail_on=%s)\n", failOn)
}

// fileAgg holds per-file maxima for the metrics table.
type fileAgg struct {
	lines, funcs, cc, cog, nest, params int
	mi                                  int // worst function MI (0 = none)
}

// updateAgg folds one metric snapshot into the per-file aggregate.
func updateAgg(a *fileAgg, m model.MetricResult) {
	switch m.Metric {
	case metrics.MetricFileLength:
		a.lines = m.Value
	case metrics.MetricCyclomatic:
		a.funcs++
		a.cc = max(a.cc, m.Value)
	case metrics.MetricCognitive:
		a.cog = max(a.cog, m.Value)
	case metrics.MetricNesting:
		a.nest = max(a.nest, m.Value)
	case metrics.MetricParams:
		a.params = max(a.params, m.Value)
	case metrics.MetricMaintainability:
		a.mi = m.Value // the debt snapshot: higher = worse
	}
}

func cmdMetrics(args []string) {
	set := flag.NewFlagSet("metrics", flag.ExitOnError)
	configPath := set.String("config", "policy.yaml", "policy file")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	_, eng, summary := runEngine(*configPath, root)
	defer eng.Close()

	byFile := map[string]*fileAgg{}
	for _, m := range summary.Metrics {
		a := byFile[m.File]
		if a == nil {
			a = &fileAgg{}
			byFile[m.File] = a
		}
		updateAgg(a, m)
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	fmt.Printf("%-40s %6s %6s %7s %7s %6s %6s %6s\n", "FILE", "LINES", "FUNCS", "MAX-CC", "MAX-COG", "NEST", "PARAMS", "MI")
	for _, f := range files {
		a := byFile[f]
		mi := "-"
		if a.mi > 0 {
			mi = fmt.Sprint(100 - a.mi) // debt back to MI
		}
		fmt.Printf("%-40s %6d %6d %7d %7d %6d %6d %6s\n", f, a.lines, a.funcs, a.cc, a.cog, a.nest, a.params, mi)
	}
}

func cmdInit(args []string) {
	set := flag.NewFlagSet("init", flag.ExitOnError)
	preset := set.String("preset", "", "start from a built-in gate tuning (see: metricgate rules presets)")
	parseArgs(set, args)
	target := "policy.yaml"
	if set.NArg() > 0 {
		target = set.Arg(0)
	}
	if _, err := os.Stat(target); err == nil {
		fail(fmt.Errorf("%s already exists", target))
	}
	body := config.DefaultYAML
	if *preset != "" {
		p, ok := config.PresetByName(*preset)
		if !ok {
			fail(fmt.Errorf("unknown preset %q (available: %s)", *preset, strings.Join(config.PresetNames(), ", ")))
		}
		body = config.PresetYAML(p)
	}
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("wrote %s — edit thresholds and rules to taste\n", target)
	if *preset != "" {
		fmt.Printf("preset %q active — `metricgate rules explain list` shows what it changed\n", *preset)
	}
}

// cmdContext prints the dense constraint context pack for a path: the block
// to paste into an LLM prompt before code generation (facts + active
// policy constraints, within a character budget).
func cmdContext(args []string) {
	set := flag.NewFlagSet("context", flag.ExitOnError)
	configPath := set.String("config", "policy.yaml", "policy file")
	maxChars := set.Int("max-chars", 6000, "UTF-8 byte budget for backward-compatible context sizing")
	maxTokens := set.Int("max-tokens", 0, "estimated model-token budget (approximate unless a tokenizer is supplied)")
	allFiles := set.Bool("all", false, "include files excluded by policy (legacy unfiltered facts mode)")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	cfg, err := loadPolicy(*configPath)
	if err != nil {
		fail(err)
	}
	var files []*facts.FileFacts
	if *allFiles {
		files, err = facts.Collect(root)
	} else {
		files, err = facts.CollectSelected(root, cfg)
	}
	if err != nil {
		fail(err)
	}
	if len(files) == 0 {
		fail(fmt.Errorf("no supported source files selected under %s", root))
	}
	budget, counter := contextBudget(set, *maxChars, *maxTokens)
	pack, err := facts.RenderBudgeted(files, cfg, facts.BudgetOptions{
		Budget: budget, Counter: counter, MapScores: repoMapFor(root, files, cfg),
	})
	if err != nil {
		fail(fmt.Errorf("context budget: %w (required constraints need a larger budget)", err))
	}
	fmt.Print(pack.Text)
}

// contextBudget resolves the -max-chars / -max-tokens pair, which are mutually
// exclusive, into one budget and the counter that measures it.
func contextBudget(set *flag.FlagSet, maxChars, maxTokens int) (int, facts.TokenCounter) {
	charsSet, tokensSet := false, false
	set.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "max-chars":
			charsSet = true
		case "max-tokens":
			tokensSet = true
		}
	})
	if charsSet && tokensSet {
		fail(fmt.Errorf("-max-chars and -max-tokens are mutually exclusive"))
	}
	if maxTokens > 0 {
		return maxTokens, facts.CharEstimate{}
	}
	return maxChars, facts.CharCounter{}
}

// repoMapFor returns the importance ranking the context pack should embed. When
// the fact set already covers the whole repo (root is "."), it is ranked
// directly; a scoped run gets a whole-repo ranking instead, so the pack still
// shows where the target sits. A ranking that cannot be built (unreadable
// tree, no files) returns nil and the pack falls back to ranking what it has.
func repoMapFor(root string, files []*facts.FileFacts, cfg *config.Config) []repomap.FileScore {
	if root == "." || root == "./" {
		return facts.RepoMap(files)
	}
	repoFiles, err := facts.CollectSelected(".", cfg)
	if err != nil || len(repoFiles) == 0 {
		return nil
	}
	return facts.RepoMap(repoFiles)
}

// cmdSymbols queries the cross-file definition index: exact definitions
// first, near-miss suggestions when the exact name does not exist — the
// deterministic "does this helper already exist?" check.
func cmdSymbols(args []string) {
	set := flag.NewFlagSet("symbols", flag.ExitOnError)
	query := set.String("q", "", "symbol name to look up (required)")
	max := set.Int("max", 5, "maximum suggestions")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	if *query == "" {
		fail(fmt.Errorf("symbols needs -q NAME"))
	}
	_, ix, err := facts.CollectWithIndex(root)
	if err != nil {
		fail(err)
	}
	for _, d := range ix.Lookup(*query) {
		fmt.Printf("%s:%d  %s %s\n", d.File, d.Line, kindLabel(d), oneLineSig(d.Signature))
	}
	if len(ix.Lookup(*query)) == 0 {
		for _, m := range ix.Similar(*query, *max) {
			fmt.Printf("%s:%d  %s %s  (near-miss, score %d)\n",
				m.Def.File, m.Def.Line, kindLabel(m.Def), oneLineSig(m.Def.Signature), m.Score)
		}
	}
}

// kindLabel renders the kind and receiver of a definition.
func kindLabel(d symbols.Def) string {
	kind := d.Kind
	if kind == "" {
		kind = "symbol"
	}
	if d.Receiver != "" {
		return kind + " " + d.Receiver + "."
	}
	return kind + " "
}

// oneLineSig collapses a signature to one line, "" stays "-".
func oneLineSig(sig string) string {
	if sig == "" {
		return "-"
	}
	s := strings.Join(strings.Fields(sig), " ")
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// cmdNearClones reports near-clone pairs (design doc B-4): functions that
// share most of their normalised token set without being exact Type-2
// clones — the "copy, rename, small edit" pattern.
func cmdNearClones(args []string) {
	set := flag.NewFlagSet("nearclones", flag.ExitOnError)
	threshold := set.Float64("threshold", 0.8, "minimum Jaccard similarity (0..1)")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	cfg := clones.DefaultNear
	cfg.Threshold = *threshold
	files, err := sources.Files(root)
	if err != nil {
		fail(err)
	}
	matches, err := clones.ScanNear(files, cfg)
	if err != nil {
		fail(err)
	}
	if len(matches) == 0 {
		fmt.Println("no near-clone pairs")
		return
	}
	fmt.Printf("%d near-clone pair(s):\n", len(matches))
	for _, m := range matches {
		fmt.Printf("  %s:%d (%s)  ~%.0f%%  %s:%d (%s)\n",
			m.Anchor.File, m.Anchor.StartLine, m.Anchor.Name,
			m.Similarity*100,
			m.Other.File, m.Other.StartLine, m.Other.Name)
	}
}

func cmdCheckPreview(configPath, root, format string, human io.Writer) {
	cfg, err := loadPolicy(configPath)
	if err != nil {
		fail(err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		fail(err)
	}
	defer eng.Close()
	preview, err := eng.Preview(root)
	if err != nil {
		fail(err)
	}
	if format == "json" {
		data, err := json.MarshalIndent(preview, "", "  ")
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(append(data, '\n'))
		return
	}
	fmt.Fprintf(human, "preview: %s — %d candidate files, %d included, %d excluded, %d unsupported, %d pruned dirs\n",
		preview.Root, preview.Candidate, len(preview.Included), len(preview.Excluded), len(preview.Unsupported), len(preview.PrunedDirs))
	for _, d := range preview.Excluded {
		fmt.Fprintf(human, "  excluded    %s (%s: %s)\n", d.Path, d.Reason, d.MatchedExclude)
	}
	for _, d := range preview.Unsupported {
		fmt.Fprintf(human, "  unsupported %s (%s)\n", d.Path, d.Reason)
	}
	for _, d := range preview.PrunedDirs {
		fmt.Fprintf(human, "  pruned dir  %s (%s: %s)\n", d.Path, d.Reason, d.MatchedExclude)
	}
}
