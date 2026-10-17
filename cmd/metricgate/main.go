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
	"runtime/debug"
	"sort"
	"strings"

	"metricgate/internal/clones"
	"metricgate/internal/config"
	"metricgate/internal/configpath"
	"metricgate/internal/engine"
	"metricgate/internal/facts"
	"metricgate/internal/metrics"
	"metricgate/internal/model"
	"metricgate/internal/repomap"
	"metricgate/internal/sources"
	"metricgate/internal/symbols"
)

// toolVersion is the declared release version. It is a var, not a const, so a
// release build can override it with
// -ldflags "-X main.toolVersion=v1.5.0" (the symbol is main.toolVersion — the
// package is `main` even though it lives in cmd/metricgate) without editing
// source; a local build keeps the fallback below. `metricgate version` prints
// this alongside buildStamp(), which reports the VCS revision actually linked
// in — the constant alone could not distinguish two builds of the same version.
//
// Bumping this is what seals a release: seal.yml reads it on every push to
// main, and a value whose tag already exists means the tree ships unsealed
// (see the workflow's header). This tree carries the merged cockpit work, the
// language parity pass, the review/merge CI chain and the issues query
// rewrite, none of which v0.1.0 contains.
var toolVersion = "0.2.0"

// commands maps each subcommand to its entry point. A table keeps main a
// three-branch dispatcher no matter how many subcommands accumulate, instead
// of a switch that grows one path per command.
var commands = map[string]func([]string){
	"check":        cmdCheck,
	"metrics":      cmdMetrics,
	"stats":        cmdStats,
	"context":      cmdContext,
	"bundle":       cmdBundle,
	"symbols":      cmdSymbols,
	"impact":       cmdImpact,
	"map":          cmdMap,
	"rules":        cmdRules,
	"constraints":  cmdConstraints,
	"rules-gaps":   cmdRulesGaps,
	"explain":      cmdExplain,
	"api":          cmdAPI,
	"arch":         cmdArch,
	"rank":         cmdRank,
	"nearclones":   cmdNearClones,
	"clonedrift":   cmdClonedrift,
	"hotspots":     cmdHotspots,
	"coupling":     cmdCoupling,
	"reachability": cmdReachability,
	"trends":       cmdTrends,
	"trace":        cmdTrace,
	"calls":        cmdCalls,
	"deps":         cmdDeps,
	"accept":       cmdAccept,
	"suppressions": cmdSuppressions,
	"cockpit":      cmdCockpit,
	"init":         cmdInit,
	"version":      cmdVersion,
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
	fmt.Println("metricgate", toolVersion, buildStamp())
}

// buildStamp identifies the build that is actually running, not the one the
// version constant claims.
//
// toolVersion is a source-level fallback, so every local build prints the same
// version string. That made "am I running the binary I just built?" unanswerable from
// the outside: a stale binary in PATH and a fresh one in the repo root report
// identically, and the only visible difference is the symptom - an old UI, or
// a check that does not know about a rule you just added. Go already records
// the VCS revision at link time, so the answer is to print it.
//
// The revision is suffixed with "+dirty" when the working tree had uncommitted
// changes at build time, because a revision alone would otherwise suggest the
// binary matches that commit exactly.
func buildStamp() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "(unknown build)"
	}
	revision, dirty := "", false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	return formatBuildStamp(revision, dirty)
}

// formatBuildStamp is the pure half, split out so the truncation and the dirty
// suffix are testable without controlling how the test binary was linked.
func formatBuildStamp(revision string, dirty bool) string {
	if revision == "" {
		// `go run` and `go test` build without VCS stamping.
		return "(no vcs stamp)"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if dirty {
		return revision + "+dirty"
	}
	return revision
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

// loadPolicy loads the policy for the given root, falling back to the built-in
// default policy when no policy file is found.
//
// The path is either an explicit -config value or empty. An empty value starts
// the discovery search at root (see configpath), so a command run against
// another repository reads that repository's policy instead of whatever
// ".metricgate.yaml" happens to sit in the process working directory.
func loadPolicy(path string) (*config.Config, error) {
	return loadPolicyFor(path, ".")
}

// loadPolicyFor resolves the policy for root. When nothing is found the
// built-in defaults are used and the search is reported on stderr, so a
// silent downgrade is never invisible.
func loadPolicyFor(path, root string) (*config.Config, error) {
	found := configpath.Discover(path, root)
	if !found.FoundAny() {
		fmt.Fprintf(os.Stderr, "note: %s\n", found.Display())
		return config.Default()
	}
	return config.LoadFile(found.Path)
}

// policyFor reports which policy file root would use, for the UIs that answer
// "where does this configuration come from?".
func policyFor(path, root string) configpath.Found {
	return configpath.Discover(path, root)
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
	cfg, err := loadPolicyFor(configPath, root)
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

// fileAgg folds one file's file-scope metrics into the columns `metrics` prints.
type fileAgg struct {
	lines, funcs, cc, cog, nest, params, mi int
}

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

// metricRows aggregates the summary into one row per file, sorted by path so
// the table is byte-identical between runs.
func metricRows(summary *model.RunSummary) []metricRow {
	byFile := map[string]*fileAgg{}
	for _, m := range summary.Metrics {
		a := byFile[m.File]
		if a == nil {
			a = &fileAgg{}
			byFile[m.File] = a
		}
		updateAgg(a, m)
	}
	rows := make([]metricRow, 0, len(byFile))
	for f, a := range byFile {
		rows = append(rows, metricRow{file: f, agg: *a})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].file < rows[j].file })
	return rows
}

type metricRow struct {
	file string
	agg  fileAgg
}

func cmdMetrics(args []string) {
	set := flag.NewFlagSet("metrics", flag.ExitOnError)
	configPath := set.String("config", "", "policy file")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	_, eng, summary := runEngine(*configPath, root)
	defer eng.Close()

	fmt.Printf("%-40s %6s %6s %7s %7s %6s %6s %6s\n", "FILE", "LINES", "FUNCS", "MAX-CC", "MAX-COG", "NEST", "PARAMS", "MI")
	for _, r := range metricRows(summary) {
		mi := "-"
		if r.agg.mi > 0 {
			mi = fmt.Sprint(100 - r.agg.mi) // debt back to MI
		}
		fmt.Printf("%-40s %6d %6d %7d %7d %6d %6d %6s\n", r.file, r.agg.lines, r.agg.funcs, r.agg.cc, r.agg.cog, r.agg.nest, r.agg.params, mi)
	}
}

// initBody picks the starter policy body: the built-in default, or a named
// preset's rendering. An unknown preset is a usage error, not a silent default.
func initBody(preset string) (string, error) {
	if preset == "" {
		return config.DefaultYAML, nil
	}
	p, ok := config.PresetByName(preset)
	if !ok {
		return "", fmt.Errorf("unknown preset %q (available: %s)", preset, strings.Join(config.PresetNames(), ", "))
	}
	return config.PresetYAML(p), nil
}

func cmdInit(args []string) {
	set := flag.NewFlagSet("init", flag.ExitOnError)
	preset := set.String("preset", "", "start from a built-in gate tuning (see: metricgate rules presets)")
	parseArgs(set, args)
	target := ".metricgate.yaml"
	if set.NArg() > 0 {
		target = set.Arg(0)
	}
	if _, err := os.Stat(target); err == nil {
		fail(fmt.Errorf("%s already exists", target))
	}
	body, err := initBody(*preset)
	if err != nil {
		fail(err)
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
	configPath := set.String("config", "", "policy file")
	maxChars := set.Int("max-chars", 6000, "UTF-8 byte budget for backward-compatible context sizing")
	maxTokens := set.Int("max-tokens", 0, "estimated model-token budget (approximate unless a tokenizer is supplied)")
	allFiles := set.Bool("all", false, "include files excluded by policy (legacy unfiltered facts mode)")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	cfg, err := loadPolicyFor(*configPath, root)
	if err != nil {
		fail(err)
	}
	files, err := contextFiles(root, cfg, *allFiles)
	if err != nil {
		fail(err)
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

// contextFiles collects the facts the context pack renders: the policy-filtered
// selection, or every file in the legacy -all mode.
func contextFiles(root string, cfg *config.Config, all bool) ([]*facts.FileFacts, error) {
	if all {
		return facts.Collect(root)
	}
	files, err := facts.CollectSelected(root, cfg)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no supported source files selected under %s", root)
	}
	return files, nil
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
	printNearClones(matches)
}

// printNearClones renders the pairs, or says there are none — silence would
// read as "the command did nothing" rather than "nothing matched".
func printNearClones(matches []clones.NearMatch) {
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
	cfg, err := loadPolicyFor(configPath, root)
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
