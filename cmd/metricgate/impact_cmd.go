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

// impactFlags holds the parsed impact command line, so cmdImpact reads as
// a sequence of steps instead of a wall of dereferenced flag pointers.
type impactFlags struct {
	query, format, root string
	max, depth          int
	maxAffected         int
}

// parseImpactFlags consumes the impact command line.
func parseImpactFlags(args []string) impactFlags {
	set := flag.NewFlagSet("impact", flag.ExitOnError)
	configPath := set.String("config", "", "policy file")
	query := set.String("q", "", "symbol name to analyse (required)")
	format := set.String("format", "text", "text|json")
	max := set.Int("max", 50, "maximum references to print")
	depth := set.Int("depth", impact.DefaultDepth, "transitive hops: 1 = this symbol's use sites, 2 = who calls the callers")
	maxAffected := set.Int("max-affected", impact.DefaultMaxAffectedFiles, "cap on affected files in a transitive walk (0 = default, -1 = no cap)")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	if *query == "" {
		fail(fmt.Errorf("impact needs -q NAME"))
	}
	_ = configPath // reserved: layer annotation of affected files lands with the arch wiring
	return impactFlags{query: *query, format: *format, root: root, max: *max, depth: *depth, maxAffected: *maxAffected}
}

func cmdImpact(args []string) {
	f := parseImpactFlags(args)
	files, defs, uses, err := facts.CollectWithRefs(f.root)
	if err != nil {
		fail(err)
	}
	_ = files
	report := impact.ComputeWith(f.query, defs, uses, impact.Options{
		Depth:            f.depth,
		MaxAffectedFiles: f.maxAffected,
	})
	if f.format == "json" {
		printImpactJSON(report)
		return
	}
	printImpact(report, defs, f.max)
}

// printImpactJSON renders the machine-readable form, including the tiered
// closure a text render cannot express.
func printImpactJSON(r impact.Report) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fail(err)
	}
	fmt.Println(string(data))
}

// printImpact renders the report, falling back to near-miss suggestions when
// the exact name is unknown (the same discipline as the symbols command).
func printImpact(r impact.Report, defs *symbols.Index, max int) {
	fmt.Printf("symbol %q — %d definition(s), %d call(s), %d type use(s), %d affected file(s)\n",
		r.Symbol, len(r.Definitions), r.Calls, r.TypeUses, len(r.AffectedFiles))
	fmt.Printf("confidence: %s — name-based; interface dispatch, reflection and dynamic names can be missed\n", r.Confidence)
	if r.Depth > 1 {
		fmt.Printf("depth: %d — reached %d further name(s); tiers say how far each can be trusted\n",
			r.Depth, len(r.Reach))
	}
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
	}
	printImpactTail(r)
}

// printImpactTail renders the parts that only a transitive walk produces: the
// reached names by tier, and the affected files with any width-cap remainder.
func printImpactTail(r impact.Report) {
	deep := r.Depth > 1
	if !deep && len(r.References) == 0 {
		return
	}
	if deep && len(r.Reach) > 0 {
		fmt.Printf("\nreached (depth 2..%d):\n", r.Depth)
		printReach(r.Reach)
	}
	heading := fmt.Sprintf("\naffected files (%d):", len(r.AffectedFiles))
	if r.HiddenAffectedFiles > 0 {
		heading += fmt.Sprintf(" (+%d past the width cap, not expanded)", r.HiddenAffectedFiles)
	}
	fmt.Println(heading)
	for _, f := range r.AffectedFiles {
		fmt.Printf("  %s\n", f)
	}
}

// printReach lists the transitively reached names with the tier that says how
// far each can be believed, so a wide answer is readable as a ranking instead
// of an undifferentiated wall of names.
func printReach(rs []impact.Reach) {
	byTier := map[string][]impact.Reach{}
	for _, r := range rs {
		byTier[r.Tier] = append(byTier[r.Tier], r)
	}
	for _, tier := range []string{impact.TierResolved, impact.TierAmbiguous, impact.TierExternal} {
		group := byTier[tier]
		if len(group) == 0 {
			continue
		}
		fmt.Printf("  %s (%d):\n", tier, len(group))
		for _, r := range group {
			where := ""
			if len(r.Files) > 0 {
				where = fmt.Sprintf("  %d file(s)", len(r.Files))
			}
			fmt.Printf("    d%d  %s%s\n", r.Depth, r.Name, where)
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
