package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"metricgate/internal/engine"
	"metricgate/internal/policygap"
)

// cmdRulesGaps audits policy completeness: which rules can fire under this
// policy, which are silent because the policy never armed them, and which are
// firing so often that the threshold has stopped describing the code.
//
// It is the answer to "the gate passed — is that because the code is clean, or
// because the policy is thin?" A finding list cannot distinguish the two.
func cmdRulesGaps(args []string) {
	set := flag.NewFlagSet("rules gaps", flag.ExitOnError)
	configPath := set.String("config", "", "policy file")
	format := set.String("format", "text", "text|json")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	rep := runGaps(*configPath, root)
	if *format == "json" {
		data, _ := json.MarshalIndent(rep, "", "  ")
		os.Stdout.Write(append(data, '\n'))
		return
	}
	printGaps(rep)
}

// runGaps runs the engine once and audits the result. The audit reads the same
// run rather than a second pass, so what it reports is what `check` saw.
func runGaps(configPath, root string) policygap.Report {
	cfg, err := loadPolicyFor(configPath, root)
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
	return policygap.Analyze(policygap.Input{
		Root:      root,
		Config:    cfg,
		Catalog:   engine.Catalog(cfg),
		Summary:   res.Summary,
		Packages:  res.Packages,
		Languages: res.Languages,
	})
}

func printGaps(rep policygap.Report) {
	fmt.Printf("policy gaps: %s — %d files, %d packages, %d rules catalogued, %d firing\n\n",
		rep.Root, rep.Files, rep.Packages, rep.Catalogued, rep.Firing)
	printStatusGroup(rep, policygap.StatusUndeclared,
		"not armed — these rules cannot fire until the policy declares them:")
	printStatusGroup(rep, policygap.StatusFlagOnly,
		"needs a flag — a plain `check` says nothing about them:")
	printStatusGroup(rep, policygap.StatusReportOnly,
		"report-only — enabled but no threshold set, so they can never fire:")
	printStatusGroup(rep, policygap.StatusDisabled,
		"switched off — a decision, not a gap:")
	printStatusGroup(rep, policygap.StatusFiring, "firing:")
	printStatusGroup(rep, policygap.StatusNoFiles, "no files of their language:")
	printArmedSummary(rep)
	printHot(rep)
	printLayers(rep)
	printSuppressed(rep)
}

// printStatusGroup lists one status's rules with the reason each carries.
func printStatusGroup(rep policygap.Report, status policygap.Status, heading string) {
	var lines []string
	for _, r := range rep.Rules {
		if r.Status != status {
			continue
		}
		lines = append(lines, "  "+ruleLine(r))
	}
	if len(lines) == 0 {
		return
	}
	fmt.Printf("%s\n", heading)
	for _, l := range lines {
		fmt.Println(l)
	}
	fmt.Println()
}

// ruleLine renders one audit entry. A firing rule shows its count, which is
// what makes the list readable; every other status shows why it is quiet.
func ruleLine(r policygap.RuleAudit) string {
	if r.Status == policygap.StatusFiring {
		return fmt.Sprintf("%-24s %d finding(s)", r.RuleID, r.Findings)
	}
	if r.Detail == "" {
		return r.RuleID
	}
	return fmt.Sprintf("%-24s %s", r.RuleID, r.Detail)
}

// printArmedSummary collapses the armed-and-clean rules to one line: they are
// the majority in a healthy policy, and listing them buries the actual findings.
func printArmedSummary(rep policygap.Report) {
	n := 0
	for _, r := range rep.Rules {
		if r.Status == policygap.StatusArmed {
			n++
		}
	}
	if n == 0 {
		return
	}
	fmt.Printf("armed and clean — no action (%d rules)\n\n", n)
}

func printHot(rep policygap.Report) {
	if len(rep.Hot) == 0 {
		return
	}
	fmt.Printf("hot rules — firing on more than %.0f%% of what they measure (min %d findings):\n",
		policygap.HotShare()*100, policygap.HotMinFindings())
	for _, h := range rep.Hot {
		fmt.Printf("  %-24s %d/%d = %.0f%%\n", h.RuleID, h.Findings, h.Scopes, h.Share*100)
	}
	fmt.Println("  a rule this hot is usually reporting a threshold that no longer\n  describes the code, not a codebase that is independently bad.")
	fmt.Println()
}

func printLayers(rep policygap.Report) {
	if len(rep.Layers) == 0 && len(rep.Unlayered) == 0 {
		return
	}
	for _, l := range rep.Layers {
		fmt.Printf("layer %-12s %s\n", l.Name, packageList(l.Packages))
	}
	if len(rep.Unlayered) > 0 {
		fmt.Printf("\n%d package(s) match no layer, so no layer rule protects them:\n  %s\n",
			len(rep.Unlayered), strings.Join(rep.Unlayered, ", "))
	}
	fmt.Println()
}

func printSuppressed(rep policygap.Report) {
	if len(rep.Suppressed) == 0 {
		return
	}
	fmt.Println("silenced by inline directives — per-rule count; `suppressions` lists each site and reason")
	for _, s := range rep.Suppressed {
		fmt.Printf("  %-24s %d\n", s.RuleID, s.Count)
	}
	fmt.Println("  the acceptance ledger is the named sibling: `suppressions` lists both")
}

// packageList renders a layer's packages, or says when a declared layer covers
// nothing — a layer no package matches is a policy that looks armed and is not.
func packageList(pkgs []string) string {
	if len(pkgs) == 0 {
		return "(no package matches this layer)"
	}
	return strings.Join(pkgs, ", ")
}
