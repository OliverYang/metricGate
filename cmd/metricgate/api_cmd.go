package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"metricgate/internal/apisurface"
	"metricgate/internal/facts"
)

func cmdAPI(args []string) {
	dispatch("api", "snapshot|diff", args, map[string]func([]string){
		"snapshot": cmdAPISnapshot,
		"diff":     cmdAPIDiff,
	})
}

func cmdAPISnapshot(args []string) {
	set := flag.NewFlagSet("api snapshot", flag.ExitOnError)
	out := set.String("out", "api.json", "snapshot file to write")
	config := set.String("config", "", "policy file (applies excludes to the capture)")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	files, err := collectForAPI(root, *config)
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
	config := set.String("config", "", "policy file (applies excludes to the capture)")
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
	files, err := collectForAPI(root, *config)
	if err != nil {
		fail(err)
	}
	d := apisurface.Compare(&oldSnap, apisurface.Current(files))
	renderAPIDiff(&d, *format)
	if len(d.Breaking) > 0 {
		fmt.Fprintf(os.Stderr, "api: %d breaking change(s)\n", len(d.Breaking))
		os.Exit(1)
	}
}

func renderAPIDiff(d *apisurface.Diff, format string) {	if format == "json" {
		out, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(append(out, '\n'))
		return
	}
	printAPIDiffText(d)
}

func printAPIDiffText(d *apisurface.Diff) {
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

// collectForAPI loads the policy (when given, else discovered at root) and
// collects facts with its excludes. Without this the snapshot captured every
// supported source file in the tree — including checkouts under .data/ and the
// vendored scripts under .trellis/ — so the same tree compared against itself
// reported hundreds of phantom breaking changes.
func collectForAPI(root, cfgFile string) ([]*facts.FileFacts, error) {
	cfg, err := loadPolicyFor(cfgFile, root)
	if err != nil {
		return nil, err
	}
	return facts.CollectSelected(root, cfg)
}
