// Command metricgate's check pipeline lives here rather than in main.go:
// flag parsing, the -diff filter and its add-ons, the report/store writes and
// the gate verdict. main.go keeps the command registry and the smaller verbs.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"metricgate/internal/arch"
	"metricgate/internal/baseline"
	"metricgate/internal/bundle"
	"metricgate/internal/config"
	"metricgate/internal/diff"
	"metricgate/internal/engine"
	"metricgate/internal/facts"
	"metricgate/internal/gatecheck"
	"metricgate/internal/git"
	"metricgate/internal/model"
	"metricgate/internal/report"
	"metricgate/internal/scorecard"
	"metricgate/internal/store"
	"metricgate/internal/trace"
)

// checkFlags is the flag set of `check`, extracted so cmdCheck stays a
// short orchestration and adding a flag does not grow the function that
// runs the whole pipeline.
type checkFlags struct {
	configPath    *string
	sarifOut      *string
	diffMode      *bool
	base          *string
	hidden        *bool
	regressMode   *bool
	scorecardMode *bool
	impactMode    *bool
	testsMode     *bool
	archBase      *string
	format        *string
	preview       *bool
	maxCognitive  *int
	maxCyclomatic *int
}

func newCheckFlags() (*flag.FlagSet, checkFlags) {
	set := flag.NewFlagSet("check", flag.ExitOnError)
	return set, checkFlags{
		configPath:    set.String("config", "", "policy file"),
		sarifOut:      set.String("sarif", "", "write a SARIF 2.1.0 report to this file"),
		diffMode:      set.Bool("diff", false, "only report findings on lines changed vs -base (git)"),
		base:          set.String("base", "HEAD", "git ref to diff against (with -diff)"),
		hidden:        set.Bool("hidden", false, "with -diff, also list the pre-existing findings the gate hid"),
		regressMode:   set.Bool("regress", false, "with -diff, also report measurements that got worse than -base (warning)"),
		scorecardMode: set.Bool("scorecard", false, "with -diff, print a deterministic change scorecard for the diff (report-only)"),
		impactMode:    set.Bool("impact-scope", false, "with -diff, gate the blast radius of changed symbols against the policy cap (LC-403, error)"),
		testsMode:     set.Bool("tests-touched", false, "with -diff, flag logic changes that touch no test file (LC-404, warning)"),
		archBase:      set.String("arch-base", "", "snapshot file from `arch snapshot`; diff the current dependency shape against it and gate drift (error by default, works without -diff)"),
		format:        set.String("format", "text", "console output format: text|json"),
		preview:       set.Bool("preview", false, "show path selection decisions without parsing or running checks"),
		maxCognitive:  set.Int("max-cognitive", 0, "override the cognitive-complexity error threshold for every language"),
		maxCyclomatic: set.Int("max-cyclomatic", 0, "override the cyclomatic-complexity error threshold for every language"),
	}
}

// humanWriter picks where human-readable lines go: stdout normally, stderr
// in json mode so stdout stays machine-parseable.
func humanWriter(format string) io.Writer {
	if format == "json" {
		return os.Stderr
	}
	return os.Stdout
}

func cmdCheck(args []string) {
	set, f := newCheckFlags()
	parseArgs(set, args)
	human := humanWriter(*f.format)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	if *f.preview {
		cmdCheckPreview(*f.configPath, root, *f.format, human)
		return
	}
	cfg, err := loadPolicyFor(*f.configPath, root)
	if err != nil {
		fail(err)
	}
	if *f.maxCognitive > 0 || *f.maxCyclomatic > 0 {
		applyThresholdOverrides(cfg, *f.maxCognitive, *f.maxCyclomatic)
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
	// The add-ons may REPLACE the summary with the diff-filtered one. The
	// return value is what everything below must use: the ledger, the
	// arch-drift check, the reports and the gate all act on the same finding
	// set the diff-only line just described.
	summary = runCheckAddOns(cfg, eng, root, *f.base, summary, human, checkAddOnOpts{
		diffMode:      *f.diffMode,
		regressMode:   *f.regressMode,
		scorecardMode: *f.scorecardMode,
		impactMode:    *f.impactMode,
		testsMode:     *f.testsMode,
		hiddenMode:    *f.hidden,
		jsonMode:      *f.format == "json",
	})
	// Acceptance ledger: annotate findings covered by a named concession
	// before reports and gate counting. Errors are never acceptable —
	// only warnings can be conceded, so fail_on=error semantics are intact.
	applyAcceptLedger(root, summary, human)
	if *f.archBase != "" {
		applyArchDrift(cfg, *f.archBase, root, summary, human)
	}
	emitReports(summary, cfg, reportFlags{format: *f.format, sarif: *f.sarifOut}, eng, root, human)
	enforceGate(summary, cfg, human)
}

// checkAddOnOpts carries the -diff add-on flags from cmdCheck down to
// runCheckAddOns, which owns the "these flags need -diff" guard.
type checkAddOnOpts struct {
	diffMode      bool
	regressMode   bool
	scorecardMode bool
	impactMode    bool
	testsMode     bool
	hiddenMode    bool
	jsonMode      bool
}

// runCheckAddOns runs the diff filter and every -diff add-on. The flags only
// make sense against a base, so using one without -diff is a usage error
// rather than a silently ignored request.
// runCheckAddOns applies the -diff add-ons and returns the (possibly
// replaced) filtered summary. The return value is load-bearing: the gate,
// the console report and the JSON envelope must all see the filtered
// summary, or a run can print "0 new findings, 1 pre-existing hidden" and
// then fail the gate on the finding it just hid.
func runCheckAddOns(cfg *config.Config, eng *engine.Engine, root, base string, summary *model.RunSummary, human io.Writer, opt checkAddOnOpts) *model.RunSummary {
	if !opt.diffMode {
		if opt.regressMode || opt.scorecardMode || opt.impactMode || opt.testsMode || opt.hiddenMode {
			fail(fmt.Errorf("-regress/-scorecard/-impact-scope/-tests-touched/-hidden need -diff: they describe the working tree against -base"))
		}
		return summary
	}
	return runDiffMode(cfg, root, base, diffExtrasOpts{base: base, eng: eng, wantRegress: opt.regressMode, wantScorecard: opt.scorecardMode, wantImpact: opt.impactMode, wantTests: opt.testsMode, jsonMode: opt.jsonMode, wantHidden: opt.hiddenMode}, summary, human)
}

// runDiffMode filters the summary to changed lines and applies the optional
// -diff add-ons. It returns the (possibly replaced) filtered summary.
func runDiffMode(cfg *config.Config, root, base string, add diffExtrasOpts, summary *model.RunSummary, human io.Writer) *model.RunSummary {
	// One shared call: changed ranges, base-revision values and the split are
	// the same three steps the MCP server runs, from the same function.
	split, err := baseline.Split(cfg, root, base, summary)
	if err != nil {
		fail(err)
	}
	changed := split.Changed
	fmt.Fprintf(human, "diff-only: base=%s, %d new findings, %d pre-existing hidden", base, len(split.Kept), len(split.PreExisting))
	if len(split.Unchanged) > 0 {
		fmt.Fprintf(human, ", %d unchanged metric(s) dropped", len(split.Unchanged))
	}
	fmt.Fprint(human, "\n\n")
	if add.wantHidden {
		listHiddenFindings(human, split.DiffSplit)
	}
	if err := diffExtras(cfg, root, split.RunSummary, changed, add, human); err != nil {
		fail(err)
	}
	return split.RunSummary
}

// listHiddenFindings prints what the gate hid, so "0 new findings" can be
// checked instead of trusted. The count alone reads as "nothing is wrong";
// the list shows what was wrong before this change and is still wrong now.
func listHiddenFindings(human io.Writer, split engine.DiffSplit) {
	fmt.Fprintf(human, "hidden (pre-existing, not reported as new): %d\n", len(split.PreExisting))
	for _, f := range split.PreExisting {
		fmt.Fprintf(human, "  %s:%d: [%s] %s — %s\n", f.File, f.StartLine, f.Severity, f.RuleID, f.Message)
	}
	if len(split.Unchanged) > 0 {
		fmt.Fprintf(human, "unchanged (value identical to the base revision): %d\n", len(split.Unchanged))
		for _, f := range split.Unchanged {
			fmt.Fprintf(human, "  %s:%d: [%s] %s — %s\n", f.File, f.StartLine, f.Severity, f.RuleID, f.Message)
		}
	}
	fmt.Fprint(human, "\n")
}

// diffExtrasOpts carries the -diff add-on switches and their shared inputs.
type diffExtrasOpts struct {
	base          string
	eng           *engine.Engine
	wantRegress   bool
	wantScorecard bool
	wantImpact    bool
	wantTests     bool
	jsonMode      bool
	wantHidden    bool
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
	applyCouplingGate(opt.eng, root, summary, changed, human)
	return applyGateChecks(cfg, root, summary, changed, opt, human)
}

// applyCouplingGate appends the change-coupling findings. It is policy-gated
// (architecture.coupling.enabled), so the default policy adds nothing; when
// the gate is on, a cross-layer pair whose BOTH files are in this change set
// becomes a finding on the shared pipeline.
//
// A shallow clone is announced rather than passed over in silence: the gate
// declines to report there, and "no findings" must not be read as "no
// coupling" when the history that would have shown it is missing.
func applyCouplingGate(eng *engine.Engine, root string, summary *model.RunSummary, changed map[string][]diff.Range, human io.Writer) {
	if eng == nil || !eng.CouplingEnabled() {
		return
	}
	history := gitCouplingHistory{root: root, since: eng.CouplingSince()}
	if history.IsShallow() {
		fmt.Fprintln(human, "coupling: skipped — shallow clone, git history is truncated (the gate would undercount)")
		return
	}
	findings := eng.CouplingFindings(root, changed, history)
	if len(findings) == 0 {
		fmt.Fprintln(human, "coupling: no cross-layer co-change pair in this change")
		return
	}
	summary.Findings = append(summary.Findings, findings...)
	model.SortFindings(summary.Findings)
	fmt.Fprintf(human, "coupling: %d cross-layer co-change pair(s) in this change\n\n", len(findings))
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
	opts.WantImpact = opt.wantImpact
	opts.WantTests = opt.wantTests
	if !opts.WantImpact && !opts.WantTests {
		return nil
	}
	_, defs, uses, err := facts.CollectWithRefs(root)
	if err != nil {
		return err
	}
	findings := gatecheck.BlastRadiusCheck(defs, uses, opts)
	findings = append(findings, gatecheck.TestsTouchedCheck(opts)...)
	reportGateChecks(summary, findings, human)
	return nil
}

// reportGateChecks folds the gate-check findings into the summary and prints
// one line: a clean result and a finding count are both information, and the
// reader must be able to tell them apart at a glance.
func reportGateChecks(summary *model.RunSummary, findings []model.Finding, human io.Writer) {
	if len(findings) == 0 {
		fmt.Fprintln(human, "gate-checks: impact-scope and tests-touched clean")
		return
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
}

// applyArchDrift runs the arch-drift gate check: re-captures the working
// tree's dependency shape, diffs it against the blessed snapshot, and appends
// one finding per drift item so enforceGate blocks until a human re-blesses
// or reverts. It runs on the WHOLE summary (not diff-scoped): a shape change
// is repo-wide by definition. Severity follows the go policy section's
// rules.arch-drift.severity when set (error by default).
func applyArchDrift(cfg *config.Config, baseFile, root string, summary *model.RunSummary, human io.Writer) {
	oldSnap, err := arch.LoadSnapshot(baseFile)
	if err != nil {
		fail(err)
	}
	list, err := facts.CollectSelected(root, cfg)
	if err != nil {
		fail(err)
	}
	d := arch.Compare(oldSnap, arch.Capture(arch.Build(archFiles(list))))
	sev := model.SevError
	if s := cfg.RuleSeverity("go", gatecheck.RuleIDArchDrift); s != "" {
		sev = model.Severity(s)
	}
	findings := gatecheck.ArchDriftCheck(d, sev)
	if len(findings) == 0 {
		fmt.Fprintln(human, "arch-drift: no architecture drift")
		return
	}
	summary.Findings = append(summary.Findings, findings...)
	model.SortFindings(summary.Findings)
	fmt.Fprintf(human, "arch-drift: %d shape change(s) vs %s\n\n", len(findings), baseFile)
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
//
// The store is written first and its row id rides in every machine-readable
// output, because "which run is this verdict from" is a question a consumer
// can only answer when the id is inside the document it just read. The human
// line about the stored run stays last, where a reader has already finished
// the verdict — the ordering both output modes had before the id existed.
func emitReports(summary *model.RunSummary, cfg *config.Config, flags reportFlags, eng *engine.Engine, root string, human io.Writer) {
	runID, stored := storeRun(cfg, root, summary, human)
	runIDText := storedRunID(runID, stored)
	switch flags.format {
	case "json":
		// The envelope rather than the bare summary: errors/warnings and the
		// verdict are what a consumer would otherwise have to re-derive, and
		// re-deriving them wrongly (ignoring fail_on, or the ledger) is how a
		// script ends up disagreeing with the gate.
		data, err := report.JSONEnvelopeWithRun(summary, cfg, runIDText)
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(append(data, '\n'))
	default:
		fmt.Print(report.Text(summary, cfg, toolVersion))
	}
	writeSARIF(flags, summary, cfg, eng, human, runIDText)
	noteStoredRun(human, runID, stored, cfg.Store.Path)
}

// noteStoredRun prints the store's bookkeeping line, or nothing when the run
// was not stored. It exists separately from storeRun so the line can be
// emitted after every other output, keeping the console order a reader sees
// independent of when the row was written.
func noteStoredRun(human io.Writer, runID int64, stored bool, path string) {
	if !stored {
		return
	}
	fmt.Fprintf(human, "run #%d stored in %s\n", runID, path)
}

// storedRunID renders the store's row id, or "" when nothing was stored. An
// empty value is a legal state: a policy with the store disabled produces a
// run every bit as real as one that is recorded, it just cannot be looked up.
func storedRunID(id int64, stored bool) string {
	if !stored || id <= 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

// writeSARIF emits the optional SARIF 2.1.0 report. SARIF carries no gate
// verdict, so the console line is the only place the file's existence is noted.
// The run id rides in the run properties, so an uploaded document can be
// traced back to the run that produced it.
func writeSARIF(flags reportFlags, summary *model.RunSummary, cfg *config.Config, eng *engine.Engine, human io.Writer, runID string) {
	if flags.sarif == "" {
		return
	}
	data, err := report.SARIFWithRun(summary, toolVersion, eng.Rules(), cfg, runID)
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(flags.sarif, data, 0o644); err != nil {
		fail(err)
	}
	fmt.Fprintf(human, "sarif written to %s\n", flags.sarif)
}

// storeRun persists the run for the trend views when the policy enables the
// store. The project root travels with it so Cockpit can group runs by repo,
// the policy digest so a run can be told apart from a run produced by a
// different version of the rules, and the trace id so the run can be tied
// back to the unit of work that produced it.
//
// It returns the row id and whether anything was written. The caller decides
// where the human line goes, because the id's two homes have two different
// orderings: inside the machine-readable output it is written before anything
// else, and on the console it is the last line of the run.
func storeRun(cfg *config.Config, root string, summary *model.RunSummary, human io.Writer) (int64, bool) {
	if !cfg.Store.Enabled {
		return 0, false
	}
	st, err := store.Open(cfg.Store.Path)
	if err != nil {
		fail(err)
	}
	meta := store.RunMeta{
		PolicyDigest: bundle.PolicyDigest(cfg),
		TraceID:      traceID(),
		Attrs:        traceAttrs(),
	}
	id, err := st.SaveRunFull(summary, toolVersion, gitCommit(), git.Root(root), meta)
	if err != nil {
		st.Close()
		fail(err)
	}
	pruneAfterWrite(cfg, st, human)
	st.Close()
	return id, true
}

// pruneAfterWrite applies the retention policy at the end of a run when the
// operator opted in with store.prune_on_write. Off is the default and the
// common case, so the whole branch stays silent unless a bound is set.
//
// The prune cannot fail the run: the check already produced its verdict and the
// run is committed, so a database that refused to shrink must not turn a
// passing gate into an error. The one line it prints says what went wrong
// instead, because "retention did not run" is information the operator needs
// before believing the file size tomorrow.
func pruneAfterWrite(cfg *config.Config, st *store.Store, human io.Writer) {
	if !cfg.Store.PruneOnWrite {
		return
	}
	policy := storeRetentionPolicy(cfg)
	if policy.Unlimited() {
		return
	}
	report, err := st.Prune(policy)
	if err != nil {
		fmt.Fprintf(human, "store prune after write failed: %v\n", err)
		return
	}
	if report.Runs == 0 {
		return
	}
	fmt.Fprintf(human, "store prune after write: retired %s, kept %d\n",
		countUnit(report.Runs, "run", "runs"), report.Kept)
}

// traceID returns the ambient trace id, or "" when the operator set none. An
// empty trace is a legal state: it means the host chose not to attribute this
// run to a unit of work, which is not the same as the run not existing.
func traceID() string {
	return strings.TrimSpace(os.Getenv("METRICGATE_TRACE_ID"))
}

// traceAttrs reads the ambient trace attributes from $METRICGATE_TRACE_ATTRS.
// Unknown keys are dropped; a drop is worth a note on stderr because a host
// that set "modle=gpt-5" would otherwise see its grouping silently vanish.
func traceAttrs() []store.Attr {
	raw := os.Getenv(trace.Env)
	if raw == "" {
		return nil
	}
	attrs, dropped := trace.Parse(raw)
	if dropped > 0 {
		fmt.Fprintf(os.Stderr, "note: %d trace attribute(s) dropped (unknown key, empty value, or missing '='); allowed keys: %s\n",
			dropped, strings.Join(trace.Keys(), ", "))
	}
	out := make([]store.Attr, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, store.Attr{Key: a.Key, Value: a.Value})
	}
	return out
}

// enforceGate exits 1 when findings at or above the policy threshold exist.
// Accepted warnings — concessions recorded with an owner and reason in the
// acceptance ledger — do not count: they are visible, auditable, and durable,
// not disappeared.
func enforceGate(summary *model.RunSummary, cfg *config.Config, human io.Writer) {
	// The verdict is computed by the same function the JSON envelope calls, so
	// the printed gate line and the machine-readable one cannot disagree.
	v := report.VerdictOf(summary, cfg)
	if !v.Passed {
		fmt.Fprintf(human, "gate: FAILED (fail_on=%s, %d findings)\n", v.FailOn, v.Trips)
		gateNextSteps(human)
		os.Exit(1)
	}
	if v.Accepted > 0 {
		fmt.Fprintf(human, "gate: PASSED (fail_on=%s, %d accepted warning(s) by ledger)\n", v.FailOn, v.Accepted)
		return
	}
	fmt.Fprintf(human, "gate: PASSED (fail_on=%s)\n", v.FailOn)
}
