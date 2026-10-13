package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"metricgate/internal/coupling"
	"metricgate/internal/git"
)

// cmdCoupling reports files that change together without depending on each
// other — change coupling (Tornhill 2015). The architecture graph cannot see
// this: a cross-layer pair here still satisfies every layering rule while the
// two files already move as one unit, which is erosion in progress.
//
// The history comes from internal/git.History, the same facts `hotspots`
// ranks, so churn and coupling can never disagree about what changed.
func cmdCoupling(args []string) {
	set := flag.NewFlagSet("coupling", flag.ExitOnError)
	since := set.String("since", "", "git history window (e.g. 90.days ago); empty = all history")
	maxN := set.Int("max", coupling.DefaultMax, "top N pairs")
	minCount := set.Int("min-count", coupling.DefaultMinCount, "minimum shared commits for a pair")
	maxFiles := set.Int("max-commit-files", coupling.DefaultMaxCommitFiles, "drop commits touching more files than this (sweeps)")
	format := set.String("format", "text", "text|json")
	allowShallow := set.Bool("allow-shallow", false, "report a shallow clone's truncated history instead of refusing")
	configPath := set.String("config", "", "policy file (architecture layers annotate the pairs)")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}

	cfg, err := loadPolicyFor(*configPath, root)
	if err != nil {
		fail(err)
	}
	report, err := runCoupling(root, *since, coupling.Options{
		MaxCommitFiles: *maxFiles,
		MinCount:       *minCount,
		Max:            *maxN,
		Exclude:        cfg.Architecture.CouplingExclude,
		LayerOf:        coupling.LayerResolver(cfg.Architecture.Layers),
	}, *allowShallow)
	if err != nil {
		fail(err)
	}
	renderCoupling(report, *format)
}

// gitCouplingHistory adapts the git adapter to the port the engine declares
// (engine.CouplingHistory). Keeping the adapter on the CLI side of the seam is
// what lets the checker core stay a pure library with no process boundary.
type gitCouplingHistory struct {
	root  string
	since string
}

func (h gitCouplingHistory) IsShallow() bool { return git.IsShallow(h.root) }

func (h gitCouplingHistory) Commits() ([]coupling.Commit, error) {
	commits, err := git.History(h.root, h.since)
	if err != nil {
		return nil, err
	}
	return couplingCommits(commits), nil
}

// couplingCommits narrows the git adapter's history to the shape the analysis
// consumes. Only paths matter: co-change is about which files moved together,
// not how many lines each of them moved.
func couplingCommits(commits []git.Commit) []coupling.Commit {
	out := make([]coupling.Commit, 0, len(commits))
	for _, c := range commits {
		files := make([]coupling.File, 0, len(c.Files))
		for _, f := range c.Files {
			files = append(files, coupling.File{Path: f.Path})
		}
		out = append(out, coupling.Commit{Files: files})
	}
	return out
}

// runCoupling is the shared entry point of the CLI verb and the MCP tool: it
// enforces the shallow-clone guard once, then analyzes.
func runCoupling(root, since string, opts coupling.Options, allowShallow bool) (coupling.Report, error) {
	shallow := git.IsShallow(root)
	if shallow && !allowShallow {
		return coupling.Report{}, fmt.Errorf(
			"shallow clone: git history is truncated, so co-change counts would be silently too small (clone with full history, or pass -allow-shallow to report the partial numbers)")
	}
	commits, err := git.History(root, since)
	if err != nil {
		return coupling.Report{}, err
	}
	report := coupling.Analyze(couplingCommits(commits), opts)
	report.Shallow = shallow
	return report, nil
}

func renderCoupling(report coupling.Report, format string) {
	if format == "json" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(append(data, '\n'))
		return
	}
	printCouplingText(report)
}

func printCouplingText(report coupling.Report) {
	if report.Shallow {
		fmt.Fprintln(os.Stderr, "warning: shallow clone — co-change counts are undercounted")
	}
	if len(report.Pairs) == 0 {
		fmt.Println("no co-change pairs in the selected history window")
		return
	}
	for _, line := range coupling.Summarize(report.Pairs) {
		fmt.Println(line)
	}
	printCouplingNotes(report)
}

// printCouplingNotes reports what was dropped, so the ranking can be read as
// "top N of a history filtered this way" rather than as the whole truth.
func printCouplingNotes(report coupling.Report) {
	var notes []string
	if report.SkippedSweeps > 0 {
		notes = append(notes, fmt.Sprintf("%d sweep commit(s) over the file threshold dropped", report.SkippedSweeps))
	}
	if report.SkippedFiles > 0 {
		notes = append(notes, fmt.Sprintf("%d lock/generated/mirrored file(s) ignored", report.SkippedFiles))
	}
	if len(notes) > 0 {
		fmt.Fprintf(os.Stderr, "note: %s\n", strings.Join(notes, "; "))
	}
}
