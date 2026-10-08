package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"metricgate/internal/config"
	"metricgate/internal/digest"
	"metricgate/internal/engine"
	"metricgate/internal/extract"
	"metricgate/internal/facts"
	"metricgate/internal/rulegen"
	"metricgate/internal/status"
)

// cmdConstraints exposes deterministic constraint-consumption operations.
// digest is the pre-generation grounding deliverable: a task-scoped summary
// of the constraints that apply to one target package, rendered for an LLM
// audience from the policy / arch / deps / symbol index — zero LLM calls,
// deterministic, fully sourced (see docs/design/digest-engineering-checklist.md).
func cmdConstraints(args []string) {
	dispatch("constraints", "digest|extract|status|rule", args, map[string]func([]string){
		"digest":  cmdConstraintsDigest,
		"extract": cmdConstraintsExtract,
		"status":  cmdConstraintsStatus,
		"rule":    cmdConstraintsRule,
	})
}

// cmdConstraintsStatus reports the detection situation AFTER constraints
// land: for every rule the policy compiles to, did it fire, stay silent
// (suspicious: clean or never-matches?), or have no files to look at.
//
//	metricgate constraints status [path] [-config policy.yaml] [-format text|json]
func cmdConstraintsStatus(args []string) {
	set := flag.NewFlagSet("constraints status", flag.ExitOnError)
	var format, configPath string
	set.StringVar(&configPath, "config", "policy.yaml", "policy file to audit")
	set.StringVar(&format, "format", "text", "text|json")
	rest := set.Args()
	root := "."
	if len(rest) > 0 {
		root = rest[0]
	}
	parseArgs(set, args)
	cfg, err := loadPolicy(configPath)
	if err != nil {
		fail(err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		fail(err)
	}
	defer eng.Close()
	res, err := eng.RunDetailed(root)
	if err != nil {
		fail(err)
	}
	rep := status.Build(eng.Rules(), res.Languages, res.Summary, cfg.FailOn())
	renderStatus(rep, format)
}

// cmdConstraintsExtract turns non-standard constraint text (spec tables,
// incident reports, interview notes) into standardized rule candidates —
// the tool surface of conversion step T1b. Deterministic first-pass triage:
// every candidate still goes through SOP steps 2-5.
//
//	metricgate constraints extract -file clauses.md [-lang java]
//	                              [-config policy.yaml] [-format text|json]
//	                              [-out policy-candidates.yaml]
func cmdConstraintsExtract(args []string) {
	set := flag.NewFlagSet("constraints extract", flag.ExitOnError)
	var file, lang, format, configPath, out string
	set.StringVar(&file, "file", "", "non-standard constraint text: markdown tables (编号|条款) or free lines")
	set.StringVar(&lang, "lang", "go", "go|java — policy language section used for dedup")
	set.StringVar(&format, "format", "text", "text|json")
	set.StringVar(&configPath, "config", "policy.yaml", "existing policy file to dedup against")
	set.StringVar(&out, "out", "", "write deduped policy YAML candidates to this file")
	parseArgs(set, args)
	if file == "" {
		fail(fmt.Errorf("-file is required: non-standard constraint text to extract from"))
	}
	src, err := os.ReadFile(file)
	if err != nil {
		fail(err)
	}
	cfg, err := loadPolicy(configPath)
	if err != nil {
		fail(err)
	}
	rep := extract.Build(string(src), lang, cfg.Bans(lang))
	renderExtract(rep, format)
	if out != "" && writeCandidatesYAML(rep, lang, out) {
		fmt.Fprintf(os.Stderr, "wrote %d landable candidates to %s\n", countLandable(rep), out)
	}
}

// listFlag collects repeated flag values (-bad f1 -bad f2).
type listFlag []string

func (f *listFlag) String() string { return strings.Join(*f, ",") }

func (f *listFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

// cmdConstraintsRule is the rule-integration interface: an LLM (or any
// external agent) describes one ban rule as a JSON spec, the engine verifies
// it against caller-provided fixtures, and the merge-ready policy entry comes
// back. The integrator never reads metricgate source — spec in, verdict and
// artifact out. With -out every node (spec / fixtures / verified policy /
// report) lands on disk, so each step can be inspected, edited and re-run,
// or rolled back by keeping the previous file. Automation never touches
// policy.yaml directly: merging is a deliberate human step (SOP 4-5).
//
//	metricgate constraints rule -spec rule.json -bad bad.java [-good good.java]
//	                            [-config policy.yaml] [-format text|json] [-out dir]
func cmdConstraintsRule(args []string) {
	set := flag.NewFlagSet("constraints rule", flag.ExitOnError)
	var specPath, format, configPath, out string
	var bad, good listFlag
	set.StringVar(&specPath, "spec", "", "rule spec JSON file ('-' = stdin)")
	set.Var(&bad, "bad", "fixture file expected to HIT the rule (repeatable)")
	set.Var(&good, "good", "fixture file expected to stay CLEAN (repeatable)")
	set.StringVar(&configPath, "config", "", "existing policy to dedup against (optional)")
	set.StringVar(&format, "format", "text", "text|json")
	set.StringVar(&out, "out", "", "directory to persist every node (spec/fixtures/verified policy/report)")
	parseArgs(set, args)
	if specPath == "" {
		fail(fmt.Errorf("-spec is required: the rule description JSON"))
	}
	if len(bad) == 0 {
		fail(fmt.Errorf("-bad is required: at least one counter-example that must hit the rule"))
	}
	specBytes, err := readSpecInput(specPath)
	if err != nil {
		fail(err)
	}
	var base *config.Config
	if configPath != "" {
		base, err = loadPolicy(configPath)
		if err != nil {
			fail(err)
		}
	}
	badIn, err := readFixtures(bad)
	if err != nil {
		fail(err)
	}
	goodIn, err := readFixtures(good)
	if err != nil {
		fail(err)
	}
	res, err := rulegen.Verify(specBytes, badIn, goodIn, base)
	if err != nil {
		fail(err)
	}
	renderRule(res, format)
	if out != "" {
		persistRuleNodes(out, res, badIn, goodIn)
		fmt.Fprintf(os.Stderr, "nodes persisted to %s — edit and re-run to iterate, keep the old files to roll back\n", out)
	}
}

func readSpecInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func readFixtures(paths []string) ([]rulegen.FixtureInput, error) {
	var ins []rulegen.FixtureInput
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		ins = append(ins, rulegen.FixtureInput{Name: filepath.Base(p), Content: string(b)})
	}
	return ins, nil
}

// renderRule prints the verification result. Text mode renders the verdict
// table to stdout and the next-step line to stderr; JSON mode keeps stdout
// machine-parseable with the full VerifyResult.
func renderRule(res *rulegen.VerifyResult, format string) {
	if format == "json" {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			fail(err)
		}
		fmt.Println(string(data))
		fmt.Fprintf(os.Stderr, "rule verify: %s — %s\n", verdictWord(res), nextStep(res))
		return
	}
	fmt.Printf("rule %s (%s/%s) match=%s\n", res.RuleID, res.Spec.Lang, res.Spec.Kind, res.Spec.Match)
	if res.Existing {
		fmt.Println("dedup: base policy 已有同 kind+match 禁令 — 合并前确认语义是否叠加")
	}
	for _, fc := range res.Fixtures {
		fmt.Printf("fixture %-14s expect=%-5s verdict=%-4s hits=%d", fc.File, fc.Expect, fc.Verdict, len(fc.Hits))
		if len(fc.Hits) > 0 {
			fmt.Printf(" (%s @%d)", fc.Hits[0].Severity, fc.Hits[0].Line)
		}
		fmt.Println()
	}
	if res.CompileError != "" {
		fmt.Printf("compile: %s\n", res.CompileError)
	}
	for _, n := range res.Notes {
		fmt.Printf("note: %s\n", n)
	}
	fmt.Fprintf(os.Stderr, "rule verify: %s — %s\n", verdictWord(res), nextStep(res))
}

func verdictWord(res *rulegen.VerifyResult) string {
	if res.Pass {
		return "PASS"
	}
	return "FAIL"
}

func nextStep(res *rulegen.VerifyResult) string {
	if res.Pass {
		return "verified — human review then merge policy-entry.yaml under languages." + res.Spec.Lang + ".banned-api (SOP 4-5: severity + FP audit)"
	}
	return "do not merge — fix the spec or fixtures and re-run"
}

// persistRuleNodes writes every node of the flow to one directory: the spec
// echo, the raw fixtures, the exact verified policy, the merge-ready entry
// fragment, and the full report — the edit/re-run/rollback surface.
func persistRuleNodes(dir string, res *rulegen.VerifyResult, badIn, goodIn []rulegen.FixtureInput) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			fail(err)
		}
	}
	write("spec.json", mustJSON(res.Spec))
	for _, in := range append(append([]rulegen.FixtureInput{}, badIn...), goodIn...) {
		write(filepath.Base(in.Name), in.Content)
	}
	write("policy-verified.yaml", res.PolicyYAML)
	write("policy-entry.yaml", res.YAML)
	write("report.json", mustJSON(res))
}

func mustJSON(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fail(err)
	}
	return string(data)
}

// renderExtract prints the extraction report. Text mode renders the triage
// table to stdout; JSON mode keeps stdout machine-parseable and routes the
// human summary line to stderr (same channel split as digest/check).
func renderExtract(rep extract.Report, format string) {
	if format == "json" {
		data, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fail(err)
		}
		fmt.Println(string(data))
		fmt.Fprintf(os.Stderr, "extract: %d candidates\n", len(rep.Candidates))
		return
	}
	for _, c := range rep.Candidates {
		fmt.Printf("%s  %s  [%s]", c.ID, c.Tier, c.Kind)
		if c.Match != "" {
			fmt.Printf(" match=%s", c.Match)
		}
		if len(c.ArgsBan) > 0 {
			fmt.Printf(" args_ban=[%s]", strings.Join(c.ArgsBan, "/"))
		}
		if c.Existing != "" {
			fmt.Printf("  与现有 policy 重复: %s", c.Existing)
		}
		fmt.Printf("\n    %s\n", c.Text)
	}
	for _, n := range rep.Notes {
		fmt.Printf("note: %s\n", n)
	}
}

// renderStatus prints the detection report: hits first, then silent rules
// (the suspicious zero-hit ambiguity), then not-applicable. JSON mode keeps
// stdout machine-parseable.
func renderStatus(rep status.Report, format string) {
	if format == "json" {
		data, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fail(err)
		}
		fmt.Println(string(data))
		return
	}
	hit, silent, na := 0, 0, 0
	for _, r := range rep.Rules {
		switch r.State {
		case status.StateHit:
			hit++
		case status.StateSilent:
			silent++
		case status.StateNA:
			na++
		}
	}
	fmt.Printf("检出情况： %d 规则命中 / %d 零命中（语言已扫） / %d 不适用（语言未扫） — %d files, %s, %dms\n",
		hit, silent, na, rep.Files, strings.Join(rep.Languages, "+"), rep.DurationMS)
	fmt.Printf("闸门预判: %s (fail_on=%s, trips=%d) — errors %d / warnings %d / infos %d\n\n",
		rep.Gate, rep.GateFailOn, rep.GateTrips, rep.Errors, rep.Warnings, rep.Infos)
	for _, r := range rep.Rules {
		switch r.State {
		case status.StateHit:
			fmt.Printf("  ✘ %-36s %d 处 (E%d/W%d/I%d)", r.ID, r.Hits, r.Errors, r.Warnings, r.Infos)
			if r.Ignored > 0 {
				fmt.Printf(" +%d 抑制", r.Ignored)
			}
			fmt.Printf(" — %s\n", r.Title)
		case status.StateSilent:
			fmt.Printf("  ○ %-36s 0 处，语言已扫：干净还是静默失效？烟测验证 — %s\n", r.ID, r.Title)
		case status.StateNA:
			fmt.Printf("  · %-36s 不适用（%s 不在扫描树中） — %s\n", r.ID, r.Language, r.Title)
		}
	}
	for _, n := range rep.Notes {
		fmt.Printf("note: %s\n", n)
	}
}

// writeCandidatesYAML writes the deduped, matchable ban candidates as a
// standalone YAML policy fragment under the given language section; returns
// false when there is nothing landable.
func writeCandidatesYAML(rep extract.Report, lang, path string) bool {
	var lines []string
	for _, c := range rep.Candidates {
		if c.Action != "ban" || c.Match == "" || c.Existing != "" {
			continue
		}
		lines = append(lines, extract.Snippet(c))
	}
	if len(lines) == 0 {
		return false
	}
	body := fmt.Sprintf("# generated by metricgate constraints extract — review, add reason/use, then fixture-smoke (SOP steps 2-5)\nlanguages:\n  %s:\n    banned-api:\n%s\n", lang, strings.Join(prefixLines(lines, "      "), "\n"))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		fail(err)
	}
	return true
}

// countLandable counts deduped, matchable ban candidates.
func countLandable(rep extract.Report) int {
	n := 0
	for _, c := range rep.Candidates {
		if c.Action == "ban" && c.Match != "" && c.Existing == "" {
			n++
		}
	}
	return n
}

func prefixLines(lines []string, prefix string) []string {
	var out []string
	for _, l := range lines {
		// Snippets are multi-line; every physical line needs the indent or
		// the YAML continuation fields land at column 0 and break parsing.
		for _, part := range strings.Split(l, "\n") {
			out = append(out, prefix+part)
		}
	}
	return out
}

// cmdConstraintsDigest renders the task-scoped constraint digest.
//
//	metricgate constraints digest -pkg internal/engine [-config policy.yaml]
//	                             [-lang go] [-format text|json] [-max-symbols 30] [-no-reuse]
//
// Compatibility matrix (degrade, never crash): no policy file → builtin
// defaults via loadPolicy; unknown -pkg → warning appended, digest still
// rendered; -no-reuse → symbol walk skipped, reuse section omitted.
func cmdConstraintsDigest(args []string) {
	set := flag.NewFlagSet("constraints digest", flag.ExitOnError)
	in := digestFlags{}
	set.StringVar(&in.configPath, "config", "policy.yaml", "policy file")
	set.StringVar(&in.pkg, "pkg", "", "target package (Go import path or slash-normalised Java package; empty = repo-wide)")
	set.StringVar(&in.format, "format", "text", "text|json")
	set.StringVar(&in.lang, "lang", "", "go|java (default: auto from target package files, fallback go)")
	set.IntVar(&in.maxSymbols, "max-symbols", 30, "cap on the reuse-symbol list")
	set.BoolVar(&in.noReuse, "no-reuse", false, "skip the reuse-symbols section (saves a repo walk)")
	parseArgs(set, args)
	cfg, err := loadPolicy(in.configPath)
	if err != nil {
		fail(err)
	}
	opts := collectWalk(cfg, in)
	res := digest.Build(cfg, opts)
	renderDigest(res, in.format)
}

// digestFlags carries the parsed command line of `constraints digest`.
type digestFlags struct {
	configPath, pkg, format, lang string
	maxSymbols                    int
	noReuse                       bool
}

// collectWalk runs the single facts walk feeding language detection, the
// known-package check and the reuse index; walk failure degrades to a
// no-reuse digest (compatibility matrix).
func collectWalk(cfg *config.Config, in digestFlags) digest.Options {
	opts := digest.Options{
		Package:         in.pkg,
		Lang:            in.lang,
		MaxSymbols:      in.maxSymbols,
		TargetPkg:       in.pkg,
		PackageResolved: in.pkg == "",
	}
	if in.noReuse {
		return opts
	}
	files, err := facts.CollectSelected(".", cfg)
	if err != nil {
		return opts
	}
	for _, f := range files {
		if in.pkg != "" && digest.MatchPackage(f.Package, f.File, in.pkg) {
			opts.PackageResolved = true
			if opts.Lang == "" {
				opts.Lang = f.Language
			}
			// In-package import precedents for the dependency-precedent
			// section: production files only (tests chase whatever they like).
			if !strings.HasSuffix(f.File, "_test.go") && !strings.HasSuffix(f.File, "Test.java") {
				opts.Imports = append(opts.Imports, f.Imports...)
			}
		}
	}
	if opts.Lang == "" {
		opts.Lang = "go"
	}
	opts.Index = facts.Index(files)
	return opts
}

// renderDigest prints the digest in the requested form; the text tail points
// at -format json for full provenance.
func renderDigest(res digest.Result, format string) {
	switch format {
	case "json":
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
	default:
		fmt.Print(res.Rendered)
		fmt.Printf("(digest: %d sections, ~%d tokens; provenance via -format json)\n",
			len(res.Sections), res.EstTokens)
	}
}
