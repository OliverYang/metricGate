// The impact command: "if this symbol changes, what is affected?" answered
// from the definition and reference indexes, deterministically and offline.
package main

import (
	"encoding/json"
	"flag"
	"fmt"

	"metricgate/internal/facts"
	"metricgate/internal/impact"
	"metricgate/internal/refs"
	"metricgate/internal/symbols"
)

func cmdImpact(args []string) {
	set := flag.NewFlagSet("impact", flag.ExitOnError)
	configPath := set.String("config", "policy.yaml", "policy file")
	query := set.String("q", "", "symbol name to analyse (required)")
	format := set.String("format", "text", "text|json")
	max := set.Int("max", 50, "maximum references to print")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	if *query == "" {
		fail(fmt.Errorf("impact needs -q NAME"))
	}
	_ = configPath // reserved: layer annotation of affected files lands with the arch wiring
	files, defs, uses, err := facts.CollectWithRefs(root)
	if err != nil {
		fail(err)
	}
	_ = files
	report := impact.Compute(*query, defs, uses)
	if *format == "json" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fail(err)
		}
		fmt.Println(string(data))
		return
	}
	printImpact(report, defs, *max)
}

// printImpact renders the report, falling back to near-miss suggestions when
// the exact name is unknown (the same discipline as the symbols command).
func printImpact(r impact.Report, defs *symbols.Index, max int) {
	fmt.Printf("symbol %q — %d definition(s), %d call(s), %d type use(s), %d affected file(s)\n",
		r.Symbol, len(r.Definitions), r.Calls, r.TypeUses, len(r.AffectedFiles))
	fmt.Printf("confidence: %s — name-based; interface dispatch, reflection and dynamic names can be missed\n", r.Confidence)
	if r.Note != "" {
		fmt.Printf("note: %s\n", r.Note)
	}
	if len(r.Definitions) == 0 && len(r.References) == 0 {
		printNearMiss(defs, r.Symbol)
		return
	}
	if len(r.Definitions) > 0 {
		fmt.Println("\ndefinitions:")
		for _, d := range r.Definitions {
			fmt.Printf("  %s:%d  %s  %s\n", d.File, d.Line, defLabel(d), oneLineSig(d.Signature))
		}
	}
	if len(r.References) > 0 {
		fmt.Printf("\nreferences:\n")
		printRefs(r.References, max)
		fmt.Printf("\naffected files (%d):\n", len(r.AffectedFiles))
		for _, f := range r.AffectedFiles {
			fmt.Printf("  %s\n", f)
		}
	}
}

// printRefs lists the use sites, capped at max with an explicit remainder.
func printRefs(rs []refs.Ref, max int) {
	shown := rs
	if max > 0 && len(shown) > max {
		shown = shown[:max]
	}
	for _, ref := range shown {
		where := ""
		if ref.Enclosing != "" {
			where = "  (in " + ref.Enclosing + ")"
		}
		recv := ""
		if ref.Receiver != "" {
			recv = ref.Receiver + "."
		}
		fmt.Printf("  %s:%d  %-4s  %s%s%s\n", ref.File, ref.Line, ref.Kind, recv, ref.Name, where)
	}
	if len(rs) > len(shown) {
		fmt.Printf("  … +%d more\n", len(rs)-len(shown))
	}
}

// printNearMiss suggests similar existing names when the query found nothing.
func printNearMiss(defs *symbols.Index, query string) {
	for _, m := range defs.Similar(query, 5) {
		fmt.Printf("  near-miss: %s:%d  %s  %s  (score %d)\n",
			m.Def.File, m.Def.Line, defLabel(m.Def), oneLineSig(m.Def.Signature), m.Score)
	}
}

// defLabel renders "kind Receiver.Name" so a definition reads as one token
// rather than "method Index." glued to the signature.
func defLabel(d symbols.Def) string {
	kind := d.Kind
	if kind == "" {
		kind = "symbol"
	}
	name := d.Name
	if d.Receiver != "" {
		name = d.Receiver + "." + d.Name
	}
	return kind + " " + name
}
