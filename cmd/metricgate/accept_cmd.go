package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"metricgate/internal/accept"
	"metricgate/internal/config"
	"metricgate/internal/configpath"
	"metricgate/internal/engine"
	"metricgate/internal/model"
	"metricgate/internal/parser"
)

// applyAcceptLedger loads the repository's concession ledger and tags
// warnings it covers before reports render and the gate counts.
func applyAcceptLedger(root string, summary *model.RunSummary, human io.Writer) {
	// A single-file check passes the file itself as root; the ledger is a
	// per-repository concept, so only look for it at a directory root.
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return
	}
	ledger, err := accept.Load(filepath.Join(root, accept.DefaultPath))
	if err != nil {
		fail(err)
	}
	if n := annotateAccepted(summary, ledger); n > 0 {
		fmt.Fprintf(human, "note: %d warning(s) accepted by ledger %s\n", n, accept.DefaultPath)
	}
}

// annotateAccepted marks findings covered by an unexpired ledger entry and
// returns how many warnings were conceded. Errors are never acceptable:
// an error finding means broken semantics, not taste, so the ledger ignores
// them regardless of what was recorded.
func annotateAccepted(summary *model.RunSummary, ledger *accept.Ledger) int {
	now := time.Now()
	n := 0
	for i := range summary.Findings {
		f := &summary.Findings[i]
		if f.Severity != model.SevWarning {
			continue
		}
		if ledger.IsAccepted(f.RuleID, f.File, f.StartLine, now) {
			f.Accepted = true
			n++
		}
	}
	return n
}

// cmdAccept manages the审美让步账本 (.metricgate/accepted.yaml). Three verbs:
//
//	metricgate accept add <ruleID> <file> -line N -owner NAME -reason TEXT [-expires RFC3339]
//	metricgate accept remove <ruleID> <file> -line N
//	metricgate accept list
//
// Every entry must name an owner and a reason — an anonymous concession
// defeats the ledger, which exists to make taste decisions auditable.
func cmdAccept(args []string) {
	if len(args) == 0 {
		fail(fmt.Errorf("usage: metricgate accept add|remove|list ..."))
	}
	verb, rest := args[0], args[1:]
	set := flag.NewFlagSet("accept", flag.ExitOnError)
	line := set.Int("line", 0, "finding start line (1-based)")
	owner := set.String("owner", "", "who concedes this (required for add)")
	reason := set.String("reason", "", "why this is acceptable (required for add)")
	expires := set.String("expires", "", "RFC3339 expiry; empty = permanent")
	path := set.String("ledger", accept.DefaultPath, "ledger file path")
	configPath := set.String("config", "", "policy file, used to tell an error from a warning")
	parseArgs(set, rest)

	l, err := accept.Load(*path)
	if err != nil {
		fail(err)
	}
	switch verb {
	case "add":
		acceptAdd(l, set, *line, *owner, *reason, *expires, *path, *configPath)
	case "remove":
		acceptRemove(l, set, *line, *path)
	case "list":
		acceptList(l, *path)
	default:
		fail(fmt.Errorf("unknown accept verb %q (want add|remove|list)", verb))
	}
}

// acceptAdd validates and records one concession.
//
// The verification step exists because the ledger only shields warnings: an
// error finding means broken semantics, not taste, so an entry recorded
// against one would print a success and change nothing — the worst shape a
// command can take, since the reader believes the gate is handled. Instead the
// command runs the policy over the named file and refuses when the finding at
// that line is an error, pointing at the two paths that actually work.
func acceptAdd(l *accept.Ledger, set *flag.FlagSet, line int, owner, reason, expires, path, configPath string) {
	if set.NArg() < 2 {
		fail(fmt.Errorf("usage: metricgate accept add <ruleID> <file> -line N -owner NAME -reason TEXT"))
	}
	ruleID, file := set.Arg(0), set.Arg(1)
	e := accept.Entry{
		RuleID: ruleID, File: file, Line: line,
		Owner: owner, Reason: reason, Expires: expires,
	}
	if err := e.Validate(); err != nil {
		fail(err)
	}
	if sev, found := findingSeverity(configPath, file, line, ruleID); found && sev != model.SevWarning {
		fail(fmt.Errorf("%s %s:%d is %s severity, which the ledger cannot concede — an error means broken semantics, not taste\n"+
			"either fix it, or change the threshold/severity in %s and re-run", ruleID, file, line, sev, configPath))
	}
	if l.IsAccepted(e.RuleID, e.File, e.Line, time.Now()) {
		fmt.Printf("already accepted: %s %s:%d\n", e.RuleID, e.File, e.Line)
		return
	}
	l.Entries = append(l.Entries, e)
	if err := l.Save(path); err != nil {
		fail(err)
	}
	fmt.Printf("accepted %s %s:%d (owner=%s)\n  reason: %s\n", e.RuleID, e.File, e.Line, e.Owner, e.Reason)
	if e.Expires != "" {
		fmt.Printf("  expires: %s — after that the warning counts as open again\n", e.Expires)
	}
}

// findingSeverity runs the policy over one file and reports the severity of
// the finding at ruleID and line. found is false when no finding matches —
// the code may already be fixed, or the file may not exist — and the caller
// then records the concession without a verdict, which is the honest answer
// when there is nothing to judge.
func findingSeverity(configPath, file string, line int, ruleID string) (model.Severity, bool) {
	cfg, err := loadPolicyQuietFor(configPath, file)
	if err != nil {
		return "", false
	}
	eng, err := engine.New(cfg)
	if err != nil {
		return "", false
	}
	defer eng.Close()
	summary, err := eng.Run(file)
	if err != nil {
		return "", false
	}
	for _, f := range summary.Findings {
		if f.RuleID != ruleID || f.StartLine != line {
			continue
		}
		if base := filepath.Base(f.File); base != filepath.Base(file) {
			continue
		}
		return f.Severity, true
	}
	return "", false
}

// loadPolicyQuietFor resolves the policy for root without the "no policy
// file" note: the accept verb may run in a repository that has no policy yet,
// and saying so on every command would be noise. An explicit -config path is
// still honoured, and a file that does not exist is left to config.LoadFile
// so the read error names the path the user asked for.
func loadPolicyQuietFor(path, root string) (*config.Config, error) {
	found := configpath.Discover(path, root)
	if !found.FoundAny() {
		cfg, _ := config.Default()
		return cfg, nil
	}
	cfg, err := config.LoadFile(found.Path)
	if err != nil {
		// A policy that fails to parse must not silently disable the check
		// that is deciding whether a concession is even reachable: any
		// unreadable policy means the verdict is unknown, not "default".
		return nil, err
	}
	return cfg, nil
}

// acceptRemove drops the concession, returning the warning to open state.
func acceptRemove(l *accept.Ledger, set *flag.FlagSet, line int, path string) {
	if set.NArg() < 2 {
		fail(fmt.Errorf("usage: metricgate accept remove <ruleID> <file> -line N"))
	}
	n := l.Remove(set.Arg(0), set.Arg(1), line)
	if n == 0 {
		fail(fmt.Errorf("no entry for %s %s:%d", set.Arg(0), set.Arg(1), line))
	}
	if err := l.Save(path); err != nil {
		fail(err)
	}
	fmt.Printf("removed %d entr%s: %s %s:%d — the warning counts as open again\n", n, pluralY(n), set.Arg(0), set.Arg(1), line)
}

// acceptList prints every entry; expired ones are flagged STALE so the
// reader sees that the concession no longer shields the finding.
func acceptList(l *accept.Ledger, path string) {
	if len(l.Entries) == 0 {
		fmt.Printf("no accepted warnings in %s\n", path)
		return
	}
	now := time.Now()
	for _, e := range l.Entries {
		status := "accepted"
		if e.Expired(now) {
			status = "STALE (expired — counts as open)"
		}
		fmt.Printf("%s %s:%d  owner=%s  %s\n  reason: %s\n", e.RuleID, e.File, e.Line, e.Owner, status, e.Reason)
	}
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// gateNextSteps lists the paths out of a failing verdict. A gate that says
// FAILED without naming its options forces the reader to go read the
// documentation, which is the point at which a tool stops being used; and
// listing anything would be worse than listing the few that actually work,
// because two of them are the sanctioned ways to keep a judgement out of the
// gate without hiding it. Nothing here executes: the reader picks.
func gateNextSteps(human io.Writer) {
	fmt.Fprint(human, "next steps — pick one:\n")
	fmt.Fprint(human, "  fix      correct the code, then re-run the same command\n")
	fmt.Fprint(human, "  suppress put `// metricgate: ignore <ruleID> [reason]` on the line above a one-off site\n")
	fmt.Fprint(human, "  concede  metricgate accept add <ruleID> <file> -line N -owner NAME -reason TEXT\n")
	fmt.Fprint(human, "           records a named, expiring concession for a warning, visible in every later run\n")
	fmt.Fprint(human, "  adjust   change the threshold or severity in policy.yaml (languages.<lang>.metrics.<id>)\n")
	fmt.Fprint(human, "  hidden   re-run with -diff -hidden to list the pre-existing findings that were not reported\n")
}

// suppressDirectives collects every `metricgate: ignore` directive under root.
//
// It exists because the two concession systems are otherwise invisible in
// different directions: the ledger has `accept list`, while a directive is
// only ever a count in `summary.IgnoredByRule`. A count answers "how many" but
// not "which lines, and why did someone write them" — which is exactly the
// question an audit asks, and the question a reviewer asks when deciding
// whether a concession is still deserved.
//
// The directives are collected with the engine's own parse rather than a text
// grep, so a directive inside a string literal or a comment of an unsupported
// file is not mistaken for one.
func suppressDirectives(root string) ([]engine.Directive, error) {
	var out []engine.Directive
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if directiveDirSkipped(root, path, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dir, ok := fileDirectives(root, path)
		if ok {
			out = append(out, dir...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Start < out[j].Start
	})
	return out, nil
}

// directiveDirSkipped reports whether the walk should not descend into this
// entry: the same directories the engine's selection skips, so a suppression
// site outside the analysed tree is not reported.
func directiveDirSkipped(root, path string, d os.DirEntry) bool {
	if !d.IsDir() {
		return false
	}
	name := d.Name()
	if path == root {
		return false
	}
	return name == ".git" || name == "vendor" || name == "node_modules" || name == "testdata" || name == ".data"
}

// fileDirectives parses one file with the engine's own parser and returns its
// suppression directives with the path rewritten relative to the run root.
func fileDirectives(root, path string) ([]engine.Directive, bool) {
	lang, ok := parser.LanguageForFile(filepath.Base(path))
	if !ok {
		return nil, false
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, false // unreadable file: not a suppression the gate applies
	}
	p, err := parser.New(lang)
	if err != nil {
		return nil, false
	}
	defer p.Close()
	tree, err := p.Parse(src)
	if err != nil {
		return nil, false
	}
	defer tree.Close()
	dirs := engine.Directives(src, tree.RootNode())
	if len(dirs) == 0 {
		return nil, false
	}
	if rel, err := filepath.Rel(root, path); err == nil {
		for i := range dirs {
			dirs[i].File = filepath.ToSlash(rel)
		}
	}
	return dirs, true
}

// cmdSuppressions prints both concession systems side by side: the inline
// `metricgate: ignore` directives and the acceptance ledger.
//
// They are shown together because that is how a reader uses them. "Is this
// repo suppressing anything?" has two answers that live in two different
// places, and answering it required two commands with two output shapes. A
// stale ledger entry and a three-year-old directive both mean the same thing —
// a judgement nobody re-examined — and only one list can show that.
func cmdSuppressions(args []string) {
	set := flag.NewFlagSet("suppressions", flag.ExitOnError)
	format := set.String("format", "text", "output format: text|json")
	ledgerPath := set.String("ledger", accept.DefaultPath, "ledger file path")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}

	dirs, err := suppressDirectives(root)
	if err != nil {
		fail(err)
	}
	ledger, err := accept.Load(*ledgerPath)
	if err != nil {
		fail(err)
	}
	report := suppressionReport{
		Directives:              dirs,
		Ledger:                  ledger.Entries,
		DirectivesWithoutReason: directiveReasons(dirs),
		StaleLedger:             staleLedgerEntries(ledger),
	}
	if *format == "json" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(append(data, '\n'))
		return
	}
	printSuppressions(report)
}

// suppressionReport is the machine-readable form of both concession systems.
type suppressionReport struct {
	Directives []engine.Directive `json:"directives"`
	Ledger     []accept.Entry     `json:"ledger"`
	// DirectivesWithoutReason counts directives with no reason written. The
	// count is in the report because it is the actionable finding: an inline
	// suppression without a reason is anonymous, and anonymous concessions are
	// what the ledger exists to prevent.
	DirectivesWithoutReason int `json:"directives_without_reason"`
	// StaleLedger counts ledger entries whose expiry has passed. An expired
	// concession is an open finding, so the count belongs in the report rather
	// than only in the date column — otherwise a stale concession reads the
	// same as a live one.
	StaleLedger int `json:"stale_ledger_entries"`
}

// staleLedgerEntries counts ledger entries whose expiry has lapsed. The gate
// treats an expired concession as open, so a listing that shows it as a live
// one is telling the reader the gate ignores it — the opposite of the truth.
func staleLedgerEntries(l *accept.Ledger) int {
	now := time.Now()
	n := 0
	for _, e := range l.Entries {
		if l.IsStale(e.RuleID, e.File, e.Line, now) {
			n++
		}
	}
	return n
}

// directiveReasons counts directives whose author wrote no justification.
func directiveReasons(dirs []engine.Directive) int {
	n := 0
	for _, d := range dirs {
		if strings.TrimSpace(d.Reason) == "" {
			n++
		}
	}
	return n
}

// printSuppressions renders the unified view. Directives first, because they
// are the cheaper mechanism and the more abundant one.
func printSuppressions(rep suppressionReport) {
	fmt.Printf("suppressions: %d inline directive(s), %d ledger entry(s)\n\n", len(rep.Directives), len(rep.Ledger))
	if len(rep.Directives) > 0 {
		fmt.Println("inline directives — metricgate: ignore (no owner, no expiry):")
		for _, d := range rep.Directives {
			fmt.Printf("  %s:%d-%d  %s\n", d.File, d.Start, d.End, directiveRuleLabel(d))
			if d.Reason != "" {
				fmt.Printf("      reason: %s\n", d.Reason)
			} else {
				fmt.Printf("      reason: (none written)\n")
			}
		}
		if rep.DirectivesWithoutReason > 0 {
			fmt.Printf("\n  %d directive(s) carry no reason: add one after the rule list\n", rep.DirectivesWithoutReason)
		}
		fmt.Println()
	}
	if len(rep.Ledger) > 0 {
		fmt.Println("acceptance ledger — named, reasoned, expiring concessions:")
		for _, e := range rep.Ledger {
			exp, stale := e.Expires, e.Expired(time.Now())
			if exp == "" {
				exp = "never"
			}
			status := ""
			if stale {
				status = "  STALE (expired — the finding counts as open again)"
			}
			fmt.Printf("  %s %s:%d owner=%s expires=%s%s\n", e.RuleID, e.File, e.Line, e.Owner, exp, status)
			fmt.Printf("      reason: %s\n", e.Reason)
		}
		fmt.Println()
	}
}

// directiveRuleLabel names what a directive silences. An empty rule list means
// every rule, which is the most consequential case and must not render as an
// empty string — a reader would take that for a rendering bug.
func directiveRuleLabel(d engine.Directive) string {
	if len(d.Rules) == 0 {
		return "all"
	}
	return strings.Join(d.Rules, ",")
}
