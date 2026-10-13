package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"metricgate/internal/deps"
)

// cmdDeps dispatches the deps subcommands; scan is the only one today.
func cmdDeps(args []string) {
	if len(args) == 0 || args[0] != "scan" {
		fmt.Fprintln(os.Stderr, "usage: metricgate deps scan [-format text|json] [-allow a,b,...]")
		os.Exit(2)
	}
	cmdDepsScan(args[1:])
}

// cmdDepsScan walks the repository's import face and drafts the
// dependencies.allow entries a policy would need. It is an onboarding tool:
// the checker reads manifest truth, and this command tells you what to write
// there, splitting what is already covered from what is not.
func cmdDepsScan(args []string) {
	set := flag.NewFlagSet("deps", flag.ExitOnError)
	format := set.String("format", "text", "output format: text|json")
	allow := set.String("allow", "", "comma-separated policy dependencies.allow entries to count as covered")
	parseArgs(set, args)

	var cfgAllow []string
	if *allow != "" {
		cfgAllow = strings.Split(*allow, ",")
	}

	d, err := deps.ScanRoot(".", cfgAllow)
	if err != nil {
		fail(err)
	}

	out := os.Stdout
	switch *format {
	case "json":
		emitDepsJSON(out, d)
	default:
		emitDepsText(out, d)
	}
}

// suggestNames renders the allow entries one import path needs: Go at module
// granularity, every other language at first-segment granularity.
func suggestNames(path, lang string) string {
	if lang == "go" {
		return path
	}
	first := path
	if i := strings.IndexAny(path, "/."); i > 0 {
		first = path[:i]
	}
	return first
}

func emitDepsText(out io.Writer, d *deps.Draft) {
	if d.Module != "" {
		fmt.Fprintf(out, "module: %s\n", d.Module)
	}
	missing := deps.Uncovered(d)
	fmt.Fprintf(out, "imports: %d distinct, %d uncovered by %d declared name(s)\n\n",
		len(d.Imports), len(missing), len(d.Covered))
	if len(missing) == 0 {
		fmt.Fprintln(out, "every external import is covered — nothing to draft")
		return
	}
	fmt.Fprintln(out, "draft dependencies.allow additions:")
	for _, imp := range missing {
		fmt.Fprintf(out, "  %-14s %s   # %s:%d (+%d more file(s))\n",
			imp.Lang, suggestNames(imp.Path, imp.Lang), imp.File, imp.Line, imp.Count-1)
	}
}

type depsJSON struct {
	Module    string        `json:"module"`
	Covered   []string      `json:"covered"`
	All       []deps.Import `json:"all"`
	Uncovered []deps.Import `json:"uncovered"`
}

func emitDepsJSON(out io.Writer, d *deps.Draft) {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(depsJSON{
		Module:    d.Module,
		Covered:   d.Covered,
		All:       d.Imports,
		Uncovered: deps.Uncovered(d),
	})
}
