package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"metricgate/internal/bundle"
	"metricgate/internal/facts"
	"metricgate/internal/git"
)

// bundleFlags is the parsed CLI surface for `metricgate bundle`.
type bundleFlags struct {
	configPath    string
	format        string
	out           string
	base          string
	from          string
	to            string
	commit        string
	tokenBudget   int
	maxChars      int
	charsSet      bool
	includeSource bool
}

func newBundleFlags() (*bundleFlags, *flag.FlagSet) {
	f := &bundleFlags{}
	set := flag.NewFlagSet("bundle", flag.ExitOnError)
	set.StringVar(&f.configPath, "config", "", "policy file")
	set.StringVar(&f.format, "format", "json", "json")
	set.StringVar(&f.out, "out", "", "write the bundle to this file (0600) instead of stdout")
	set.StringVar(&f.base, "base", "", "git base ref, merge-base with HEAD (empty = HEAD)")
	set.StringVar(&f.from, "from", "", "range mode: start ref (with -to)")
	set.StringVar(&f.to, "to", "", "range mode: end ref (with -from)")
	set.StringVar(&f.commit, "commit", "", "single-commit mode")
	set.IntVar(&f.tokenBudget, "token-budget", 0, "budget in estimated tokens (mutually exclusive with -max-chars)")
	set.IntVar(&f.maxChars, "max-chars", 6000, "budget in UTF-8 bytes")
	set.BoolVar(&f.includeSource, "include-source", false, "include full source of selected files (default: diff only)")
	return f, set
}

// cmdBundle emits a deterministic, budgeted review input for a host agent.
// It never calls an LLM and never writes source; CLI and MCP share bundle.Build.
func cmdBundle(args []string) {
	f, set := newBundleFlags()
	parseArgs(set, args)
	set.Visit(func(fl *flag.Flag) {
		if fl.Name == "max-chars" {
			f.charsSet = true
		}
	})
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	req, err := f.request(root)
	if err != nil {
		fail(err)
	}
	cfg, err := loadPolicyFor(f.configPath, root)
	if err != nil {
		fail(err)
	}
	b, err := bundle.Build(bundle.BuildOptions{Request: req, Config: cfg})
	if err != nil {
		fail(err)
	}
	if err := f.emit(b); err != nil {
		fail(err)
	}
}

// request translates CLI flags into a validated bundle request. Push the branch
// decisions here so cmdBundle stays a straight-line orchestrator.
func (f *bundleFlags) request(root string) (bundle.Request, error) {
	if f.format != "json" {
		return bundle.Request{}, fmt.Errorf("unsupported -format %q (only json is defined)", f.format)
	}
	if f.tokenBudget > 0 && f.charsSet {
		return bundle.Request{}, fmt.Errorf("-token-budget and -max-chars are mutually exclusive")
	}
	budget, counter := f.maxChars, facts.TokenCounter(facts.CharCounter{})
	if f.tokenBudget > 0 {
		budget, counter = f.tokenBudget, facts.CharEstimate{}
	}
	mode := git.ModeWorkspace
	base := f.base
	switch {
	case f.commit != "":
		mode = git.ModeCommit
	case f.from != "" || f.to != "":
		if f.from == "" || f.to == "" {
			return bundle.Request{}, fmt.Errorf("-from and -to must be provided together")
		}
		mode = git.ModeRange
	case base != "":
		base = git.ResolveBase(root, base)
	}
	return bundle.Request{
		Root: root, Mode: mode, From: f.from, To: f.to, Commit: f.commit, Base: base,
		Budget: budget, Counter: counter, IncludeSource: f.includeSource,
	}, nil
}

// emit writes the bundle to stdout or, when -out is set, to a 0600 file so a
// bundle that may contain source diff is not world-readable.
func (f *bundleFlags) emit(b *bundle.Bundle) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if f.out == "" {
		_, err := os.Stdout.Write(data)
		return err
	}
	if err := os.WriteFile(f.out, data, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote bundle to %s (%d bytes; may contain source diff)\n", f.out, len(data))
	return nil
}
