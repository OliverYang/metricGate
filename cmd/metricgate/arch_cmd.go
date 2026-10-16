package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"metricgate/internal/arch"
	"metricgate/internal/facts"
	"metricgate/internal/parser"
)

// arch snapshot / arch diff (arch-guard WP3): freeze the package dependency
// graph's DIRECTIONS and diff the current tree against a blessed snapshot.
// Where api snapshot answers "did the exported surface change", this answers
// "did the shape of the architecture change" — the question the per-run
// layer gate cannot ask. Every drift is a human decision: bless the new
// shape by re-running snapshot, or revert the change.

func cmdArch(args []string) {
	dispatch("arch", "snapshot|diff", args, map[string]func([]string){
		"snapshot": cmdArchSnapshot,
		"diff":     cmdArchDiff,
	})
}

func cmdArchSnapshot(args []string) {
	set := flag.NewFlagSet("arch snapshot", flag.ExitOnError)
	out := set.String("out", "arch.json", "snapshot file to write")
	config := set.String("config", "", "policy file (applies excludes to the capture)")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	list, err := collectForArch(root, *config)
	if err != nil {
		fail(err)
	}
	snap := arch.Capture(arch.Build(archFiles(list)))
	if err := snap.Save(*out); err != nil {
		fail(err)
	}
	fmt.Printf("wrote %s: %d packages, %d dependency direction(s)\n", *out, len(snap.Packages), len(snap.Edges))
}

func cmdArchDiff(args []string) {
	set := flag.NewFlagSet("arch diff", flag.ExitOnError)
	base := set.String("base", "arch.json", "snapshot to diff against")
	config := set.String("config", "", "policy file (applies excludes to the capture)")
	format := set.String("format", "text", "output format: text|json")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	oldSnap, err := arch.LoadSnapshot(*base)
	if err != nil {
		fail(err)
	}
	list, err := collectForArch(root, *config)
	if err != nil {
		fail(err)
	}
	d := arch.Compare(oldSnap, arch.Capture(arch.Build(archFiles(list))))
	renderArchDiff(&d, *format)
	if d.Drifted() {
		fmt.Fprintf(os.Stderr, "arch: the dependency shape drifted; re-run arch snapshot to bless it, or revert\n")
		os.Exit(1)
	}
}

// collectForArch loads the policy (when given) and collects with its excludes;
// without -config it falls back to the unfiltered facts.Collect so bare
// invocations keep their old semantics.
func collectForArch(root, cfgFile string) ([]*facts.FileFacts, error) {
	if cfgFile == "" {
		// No -config: discover one at root so an unfiltered capture still
		// honours the repository's exclude list.
		cfg, err := loadPolicyFor("", root)
		if err != nil {
			return nil, err
		}
		return facts.CollectSelected(root, cfg)
	}
	cfg, err := loadPolicyFor(cfgFile, root)
	if err != nil {
		return nil, err
	}
	return facts.CollectSelected(root, cfg)
}

// archFiles converts collected facts into the graph's file view. It lives in
// the cmd layer because the core arch package cannot import the facts
// adapter; the conversion mirrors what the engine does inline for the gate
// (engine.archFileOf) minus per-import line positions, which a direction
// snapshot does not need.
//
// Import cleaning: facts keeps Go imports as spelled, with any alias as a
// prefix ("alias path", "_" and "." included), and Java imports as full
// statements ("import x.y.Z;", "import static x.y.Z;"). The graph wants
// bare paths.
func archFiles(list []*facts.FileFacts) []arch.File {
	out := make([]arch.File, 0, len(list))
	for _, f := range list {
		if f == nil || !productionPath(f.File) {
			continue // tests/fixtures are not the architecture contract
		}
		lang := parser.LanguageID(f.Language)
		java := lang == parser.LangJava
		pkg := f.Package
		if java {
			// Java spells its package in the file, slashed.
			pkg = strings.ReplaceAll(pkg, ".", "/")
		} else if lang == parser.LangCS {
			// C# namespaces stay dotted: they are compared against using
			// directives, which are dotted too. Slashing one side only
			// would make every namespace import unresolvable.
			pkg = f.Package
		} else {
			// Everything else does not name its own import path; its directory
			// relative to the run root is the graph node, same as the gate.
			pkg = filepath.ToSlash(filepath.Dir(f.File))
		}
		out = append(out, arch.File{
			Name:    f.File,
			Pkg:     pkg,
			Style:   arch.ImportStyle(lang),
			Imports: cleanImports(f.Imports, java),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// cleanImports turns fact-sheet import strings into bare paths.
//
// Java is the one language whose import separator differs from the graph's:
// facts stores the dotted statement, but the graph node for a Java package is
// the slashed form (archFiles slashes f.Package). Leaving the import dotted
// made it a single segment, so the leading-segment match in matchDir could
// never see a package deep enough — every Java edge resolved to nothing and
// a snapshot could freeze 200 packages and 0 directions while the gate
// reported layer violations on the same tree.
func cleanImports(raw []string, java bool) []arch.Import {
	out := make([]arch.Import, 0, len(raw))
	for _, imp := range raw {
		p := imp
		if java {
			p = strings.TrimPrefix(p, "import ")
			p = strings.TrimSuffix(strings.TrimSpace(p), ";")
			// "static x.y.Z" → the type/member path is the last word.
			if i := strings.LastIndex(p, " "); i >= 0 {
				p = p[i+1:]
			}
			p = strings.ReplaceAll(p, ".", "/")
		} else if i := strings.Index(p, " "); i >= 0 {
			p = p[i+1:] // drop the alias prefix; the path is the last word
		}
		p = strings.Trim(p, "\"`")
		if p != "" {
			out = append(out, arch.Import{Path: p})
		}
	}
	return out
}

// productionPath is the same test/fixture exclusion api snapshot applies:
// tests and fixtures are useful to the checker but are not the architecture
// contract the snapshot blesses.
func productionPath(path string) bool {
	if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "Test.java") {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == "testdata" || strings.HasPrefix(part, "Test") {
			return false
		}
	}
	return true
}

func renderArchDiff(d *arch.Diff, format string) {
	if format == "json" {
		out, err := json.MarshalIndent(d, "", "  ")
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(append(out, '\n'))
		return
	}
	printArchDiffText(d)
}

func printArchDiffText(d *arch.Diff) {
	for _, e := range d.Added {
		fmt.Printf("added     %s -> %s\n", e.From, e.To)
	}
	for _, e := range d.Removed {
		fmt.Printf("removed   %s -> %s\n", e.From, e.To)
	}
	for _, p := range d.PkgsNew {
		fmt.Printf("new pkg   %s\n", p)
	}
	for _, p := range d.PkgsGone {
		fmt.Printf("gone pkg  %s\n", p)
	}
	if !d.Drifted() {
		fmt.Println("no architecture drift")
	}
}
