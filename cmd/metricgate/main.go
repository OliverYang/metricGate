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

	"metricgate/internal/apisurface"
	"metricgate/internal/clones"
	"metricgate/internal/config"
	"metricgate/internal/engine"
	"metricgate/internal/facts"
	"metricgate/internal/git"
	"metricgate/internal/hotspots"
	"metricgate/internal/metrics"
	"metricgate/internal/model"
	"metricgate/internal/report"
	"metricgate/internal/store"
	"metricgate/internal/symbols"
)

const toolVersion = "0.1.0"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "check":
		cmdCheck(os.Args[2:])
	case "metrics":
		cmdMetrics(os.Args[2:])
	case "context":
		cmdContext(os.Args[2:])
	case "bundle":
		cmdBundle(os.Args[2:])
	case "symbols":
		cmdSymbols(os.Args[2:])
	case "rules":
		cmdRules(os.Args[2:])
	case "api":
		cmdAPI(os.Args[2:])
	case "nearclones":
		cmdNearClones(os.Args[2:])
	case "clonedrift":
		cmdClonedrift(os.Args[2:])
	case "hotspots":
	case "trends":
		cmdTrends(os.Args[2:])
		cmdHotspots(os.Args[2:])
	case "init":
		cmdInit(os.Args[2:])
	case "version":
		fmt.Println("metricgate", toolVersion)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `metricgate %s — deterministic code metrics and constraint gates

usage:
  metricgate init [file]              write a starter policy (default policy.yaml)
  metricgate bundle [path] [flags]     deterministic, budgeted review input for a
                                      host agent (diff + constraints + facts)
  metricgate check [path] [flags]     run rules + metrics, enforce the gate
  metricgate check -diff [-base ref]  only fail on violations in changed lines
  metricgate context [path] [flags]   dense constraint pack for LLM prompts
  metricgate metrics [path] [flags]   print the metric table only
  metricgate symbols [path] -q NAME   look a symbol up in the cross-file
                                      definition index (near-miss suggestions
                                      when the exact name is unknown)
  metricgate rules explain [path]     explain selection + effective policy
  metricgate api snapshot -out F      capture the exported API surface
  metricgate api diff -base F [path]  breaking-change gate against a snapshot
                                      (removed/changed signatures exit 1)
  metricgate nearclones [path]        functions sharing >=80%% of their normalised
                                      tokens (copy+rename+small edit)
  metricgate hotspots [path]          churn × maintainability-debt hotspots
  metricgate clonedrift [path]         flag a change that edits one copy of a clone
                                      but not its twin (needs -base, default HEAD)
  metricgate trends [flags]           severity trend + top rules from the local
                                      history.db (P2 v1; needs store.enabled)
  metricgate version                  print the version
`, toolVersion)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "metricgate:", err)
	os.Exit(2)
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
	format := set.String("format", "text", "console output format: text|json")
	preview := set.Bool("preview", false, "show path selection decisions without parsing or running checks")
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
	cfg, eng, summary := runEngine(*configPath, root)
	defer eng.Close()
	if *diffMode {
		changed, err := git.ChangedLines("", git.ResolveBase("", *base))
		if err != nil {
			fail(err)
		}
		var hidden int
		summary, hidden = engine.FilterSummary(summary, changed)
		fmt.Fprintf(human, "diff-only: base=%s, %d new findings, %d pre-existing hidden\n\n", *base, len(summary.Findings), hidden)
	}
	emitReports(summary, cfg, reportFlags{format: *format, sarif: *sarifOut}, eng, human)
	enforceGate(summary, cfg, human)
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
	parseArgs(set, args)
	target := "policy.yaml"
	if set.NArg() > 0 {
		target = set.Arg(0)
	}
	if _, err := os.Stat(target); err == nil {
		fail(fmt.Errorf("%s already exists", target))
	}
	if err := os.WriteFile(target, []byte(config.DefaultYAML), 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("wrote %s — edit thresholds and rules to taste\n", target)
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
	charsSet, tokensSet := false, false
	set.Visit(func(f *flag.Flag) {
		if f.Name == "max-chars" {
			charsSet = true
		}
		if f.Name == "max-tokens" {
			tokensSet = true
		}
	})
	if charsSet && tokensSet {
		fail(fmt.Errorf("-max-chars and -max-tokens are mutually exclusive"))
	}
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
	budget, counter := *maxChars, facts.TokenCounter(facts.CharCounter{})
	if *maxTokens > 0 {
		budget, counter = *maxTokens, facts.CharEstimate{}
	}
	pack, err := facts.RenderBudgeted(files, cfg, facts.BudgetOptions{Budget: budget, Counter: counter})
	if err != nil {
		fail(fmt.Errorf("context budget: %w (required constraints need a larger budget)", err))
	}
	fmt.Print(pack.Text)
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

// cmdAPI snapshots or diffs the exported API surface (design doc B-3):
// `api snapshot -out api.json` writes the current surface; `api diff -base
// api.json` reports added (safe) and removed/changed (breaking) symbols,
// exiting 1 when anything broke — a SemVer gate in CI.
func cmdAPI(args []string) {
	if len(args) == 0 {
		fail(fmt.Errorf("api needs a subcommand: snapshot|diff"))
	}
	switch args[0] {
	case "snapshot":
		cmdAPISnapshot(args[1:])
	case "diff":
		cmdAPIDiff(args[1:])
	default:
		fail(fmt.Errorf("unknown api subcommand %q (want snapshot|diff)", args[0]))
	}
}

func cmdAPISnapshot(args []string) {
	set := flag.NewFlagSet("api snapshot", flag.ExitOnError)
	out := set.String("out", "api.json", "snapshot file to write")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	files, err := facts.Collect(root)
	if err != nil {
		fail(err)
	}
	snap := apisurface.Current(files)
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fail(err)
	}
	n := 0
	for _, entries := range snap.Packages {
		n += len(entries)
	}
	fmt.Printf("wrote %s: %d exported symbols across %d packages\n", *out, n, len(snap.Packages))
}

func cmdAPIDiff(args []string) {
	set := flag.NewFlagSet("api diff", flag.ExitOnError)
	base := set.String("base", "api.json", "snapshot to diff against")
	format := set.String("format", "text", "output format: text|json")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	data, err := os.ReadFile(*base)
	if err != nil {
		fail(fmt.Errorf("read snapshot: %w", err))
	}
	var oldSnap apisurface.Snapshot
	if err := json.Unmarshal(data, &oldSnap); err != nil {
		fail(fmt.Errorf("parse snapshot: %w", err))
	}
	files, err := facts.Collect(root)
	if err != nil {
		fail(err)
	}
	d := apisurface.Compare(&oldSnap, apisurface.Current(files))
	switch *format {
	case "json":
		out, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(append(out, '\n'))
	default:
		for _, c := range d.Breaking {
			fmt.Printf("BREAKING  %s %s.%s  %s\n", c.Kind, c.Package, c.Name, changeDetail(c))
		}
		for _, c := range d.Added {
			fmt.Printf("added     %s.%s at %s:%d\n", c.Package, c.Name, c.File, c.Line)
		}
		for _, c := range d.Moved {
			fmt.Printf("moved     %s.%s %s -> %s\n", c.Package, c.Name, c.Old, c.New)
		}
		if !d.Changed() {
			fmt.Println("no API changes")
		}
	}
	if len(d.Breaking) > 0 {
		fmt.Fprintf(os.Stderr, "api: %d breaking change(s)\n", len(d.Breaking))
		os.Exit(1)
	}
}

// changeDetail renders old/new for a breaking change.
func changeDetail(c apisurface.Change) string {
	switch c.Kind {
	case "signature-changed":
		return oneLineSig(c.Old) + " -> " + oneLineSig(c.New)
	case "removed":
		return "was " + oneLineSig(c.Old)
	default:
		return ""
	}
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
	matches, err := clones.ScanNear(root, cfg)
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

// cmdHotspots ranks churn × maintainability-debt hotspots from git history
// and the current engine MI snapshot (design doc B-5).
func cmdHotspots(args []string) {
	set := flag.NewFlagSet("hotspots", flag.ExitOnError)
	since := set.String("since", "3.months ago", "git history window (e.g. 90.days ago)")
	maxN := set.Int("max", 10, "top N files")
	format := set.String("format", "text", "text|json")
	configPath := set.String("config", "policy.yaml", "policy file for MI computation")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	_, eng, summary := runEngine(*configPath, root)
	defer eng.Close()
	mis := map[string]float64{}
	for _, m := range summary.Metrics {
		if m.Metric == metrics.MetricMaintainability {
			mi := 100 - float64(m.Value) // snapshots store debt (100-MI)
			if prev, ok := mis[m.File]; !ok || mi < prev {
				mis[m.File] = mi // worst function in the file
			}
		}
	}
	h, err := hotspots.Compute(hotspots.Options{Since: *since, Root: root, MIs: mis, Max: *maxN})
	if err != nil {
		fail(err)
	}
	if *format == "json" {
		data, err := json.MarshalIndent(h, "", "  ")
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(append(data, '\n'))
		return
	}
	if len(h) == 0 {
		fmt.Println("no changed files in the selected history window")
		return
	}
	fmt.Printf("%-48s %10s %7s %8s\n", "FILE", "CHURN", "MI", "SCORE")
	for _, f := range h {
		mi := "-"
		if f.MI > 0 {
			mi = fmt.Sprintf("%.0f", f.MI)
		}
		fmt.Printf("%-48s %10d %7s %8.1f\n", f.File, f.Churn, mi, f.Score)
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

// cmdRules exposes deterministic rule/policy explain operations.
func cmdRules(args []string) {
	if len(args) == 0 || args[0] != "explain" {
		fail(fmt.Errorf("rules needs subcommand: explain"))
	}
	set := flag.NewFlagSet("rules explain", flag.ExitOnError)
	configPath := set.String("config", "policy.yaml", "policy file")
	format := set.String("format", "text", "text|json")
	parseArgs(set, args[1:])
	path := ""
	if set.NArg() > 0 {
		path = set.Arg(0)
	}
	if path == "" {
		fail(fmt.Errorf("rules explain needs a source path"))
	}
	cfg, err := loadPolicy(*configPath)
	if err != nil {
		fail(err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		fail(err)
	}
	defer eng.Close()
	explanation := eng.ExplainPath(path)
	if *format == "json" {
		data, err := json.MarshalIndent(explanation, "", "  ")
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(append(data, '\n'))
		return
	}
	writeExplainText(os.Stdout, explanation)
}

// writeExplainText renders a PathExplanation as aligned human-readable lines.
func writeExplainText(w io.Writer, explanation engine.PathExplanation) {
	fmt.Fprintf(w, "%s: %s", explanation.Path, explanation.Selection.Status)
	if explanation.Language != "" {
		fmt.Fprintf(w, " (%s)", explanation.Language)
	}
	fmt.Fprintf(w, " — %s", explanation.Selection.Reason)
	if explanation.Selection.MatchedExclude != "" {
		fmt.Fprintf(w, " [%s]", explanation.Selection.MatchedExclude)
	}
	fmt.Fprintln(w)
	for _, item := range explanation.Items {
		fmt.Fprintf(w, "  %-12s %-28s %-8s", item.Kind, item.ID, stateLabel(item.Enabled))
		printExplainItem(w, item)
	}
}

func stateLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

// printExplainItem appends the optional per-item fields and the trailing newline.
func printExplainItem(w io.Writer, item engine.ExplainItem) {
	if item.Severity != "" {
		fmt.Fprintf(w, " severity=%s", item.Severity)
	}
	if item.Kind == "metric" {
		fmt.Fprintf(w, " warn>%d error>%d", item.Warn, item.Error)
	}
	fmt.Fprintf(w, " source=%s", item.Source)
	if item.SourcePath != "" {
		fmt.Fprintf(w, " (%s)", item.SourcePath)
	}
	fmt.Fprintln(w)
}
