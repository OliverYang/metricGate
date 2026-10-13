// The reachability command: which symbols does nothing reference, and which
// ones no test names? A report, never a gate — a symbol with no AST reference
// is a deletion candidate or a dynamically reached one, and only a human can
// tell which.
package main

import (
	"encoding/json"
	"flag"
	"fmt"

	"metricgate/internal/facts"
	"metricgate/internal/owners"
	"metricgate/internal/reachability"
)

type reachabilityFlags struct {
	root, format, config string
	exportedOnly         bool
	max                  int
	oners                bool
}

// parseReachabilityFlags consumes the reachability command line.
func parseReachabilityFlags(args []string) reachabilityFlags {
	set := flag.NewFlagSet("reachability", flag.ExitOnError)
	format := set.String("format", "text", "text|json")
	configPath := set.String("config", "", "policy file, for its exclude list")
	exportedOnly := set.Bool("exported", true, "report only exported definitions (a private symbol the compiler already flags is not news)")
	max := set.Int("max", reachability.DefaultMax, "maximum candidates to list per section")
	oners := set.Bool("owners", false, "add an owner column from CODEOWNERS, so a deletion candidate lands in the right inbox")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	return reachabilityFlags{root: root, format: *format, config: *configPath, exportedOnly: *exportedOnly, max: *max, oners: *oners}
}

func cmdReachability(args []string) {
	f := parseReachabilityFlags(args)
	cfg, err := loadPolicyFor(f.config, f.root)
	if err != nil {
		fail(err)
	}
	files, _, uses, err := facts.CollectSelectedWithRefs(f.root, cfg)
	if err != nil {
		fail(err)
	}
	report := reachability.Collect(files, uses, reachability.Options{
		ExportedOnly: f.exportedOnly,
		Max:          f.max,
	})
	if f.format == "json" {
		emitReachabilityJSON(report)
		return
	}
	m, err := ownerMapFor(f.root, f.oners)
	if err != nil {
		fail(err)
	}
	if m.Empty() {
		printReachability(report)
		return
	}
	printReachabilityWithOwners(report, m)
}

// emitReachabilityJSON writes the machine-readable form, including the cap so a
// consumer can tell a truncated list from a complete one.
func emitReachabilityJSON(r reachability.Report) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fail(err)
	}
	fmt.Println(string(data))
}

// ownerMapFor loads CODEOWNERS only when the routing column was asked for. An
// unflagged run stays byte-identical: no extra file is opened, and the plain
// rendering is used even when a tree happens to carry the file.
func ownerMapFor(root string, wanted bool) (owners.Map, error) {
	if !wanted {
		return owners.Map{}, nil
	}
	return owners.Find(root)
}

// printReachability renders both sections with their full counts, so a capped
// list never reads as a complete one.
func printReachability(r reachability.Report) {
	if r.Definitions == 0 {
		fmt.Println("no definitions indexed — is this a supported source tree?")
		return
	}
	share := 0
	if r.Definitions > 0 {
		share = 100 * r.Unreferenced / r.Definitions
	}
	fmt.Printf("%d definitions, %d unreferenced (%d%%), %d not named by any test\n",
		r.Definitions, r.Unreferenced, share, r.UntestedCount)
	if r.Note != "" {
		fmt.Printf("note: %s\n", r.Note)
	}
	printReachabilitySection("unreferenced (deletion candidates)", len(r.Candidates), r.Unreferenced, r.Candidates)
	printReachabilitySection("not named by any test", len(r.Untested), r.UntestedCount, r.Untested)
}

// printReachabilityWithOwners is the same report with a routing column, so a
// deletion candidate can go to whoever claimed the file rather than to whoever
// ran the command.
//
// The ownership share is stated once, next to the sections: a table with a
// column full of "unknown" is a routing table nobody filled in, which is the
// same gap a missing CODEOWNERS is — and only the number says so.
func printReachabilityWithOwners(r reachability.Report, m owners.Map) {
	if r.Definitions == 0 {
		fmt.Println("no definitions indexed — is this a supported source tree?")
		return
	}
	share := 0
	if r.Definitions > 0 {
		share = 100 * r.Unreferenced / r.Definitions
	}
	unknown, total := m.UnknownShare(reachabilityFiles(r))
	fmt.Printf("%d definitions, %d unreferenced (%d%%), %d not named by any test\n",
		r.Definitions, r.Unreferenced, share, r.UntestedCount)
	fmt.Printf("owners: %s — %d of %d candidate file(s) owned by nobody (%s)\n",
		m.Describe(), unknown, total, owners.Unknown)
	if r.Note != "" {
		fmt.Printf("note: %s\n", r.Note)
	}
	printOwnedSection("unreferenced (deletion candidates)", len(r.Candidates), r.Unreferenced, r.Candidates, m)
	printOwnedSection("not named by any test", len(r.Untested), r.UntestedCount, r.Untested, m)
}

// reachabilityFiles lists the files the two sections name, for the unknown
// share. The counts are of candidates, but the routing gap is per file: one
// unowned file with three unreferenced symbols is one ownerless decision.
func reachabilityFiles(r reachability.Report) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range append(append([]reachability.Candidate{}, r.Candidates...), r.Untested...) {
		if c.File == "" || seen[c.File] {
			continue
		}
		seen[c.File] = true
		out = append(out, c.File)
	}
	return out
}

// printReachabilitySection prints one section with its cap remainder.
func printReachabilitySection(title string, shown, total int, cs []reachability.Candidate) {
	if total == 0 {
		return
	}
	fmt.Printf("\n%s:\n", title)
	for _, c := range cs {
		name := c.Name
		if c.Receiver != "" {
			name = c.Receiver + "." + c.Name
		}
		fmt.Printf("  %s:%d  %s\n", c.File, c.Line, name)
	}
	if total > shown {
		fmt.Printf("  … +%d more (raise -max to list them)\n", total-shown)
	}
}

// printOwnedSection is printReachabilitySection with the owner appended. The
// owner is the last column so the file:line:name triple stays where it was and
// the unchanged rendering stays byte-identical without the flag.
func printOwnedSection(title string, shown, total int, cs []reachability.Candidate, m owners.Map) {
	if total == 0 {
		return
	}
	fmt.Printf("\n%s:\n", title)
	for _, c := range cs {
		name := c.Name
		if c.Receiver != "" {
			name = c.Receiver + "." + c.Name
		}
		fmt.Printf("  %s:%d  %-34s %s\n", c.File, c.Line, name, m.Label(c.File))
	}
	if total > shown {
		fmt.Printf("  … +%d more (raise -max to list them)\n", total-shown)
	}
}
