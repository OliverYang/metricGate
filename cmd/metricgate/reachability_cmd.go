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
	"metricgate/internal/reachability"
)

type reachabilityFlags struct {
	root, format, config string
	exportedOnly         bool
	max                  int
}

// parseReachabilityFlags consumes the reachability command line.
func parseReachabilityFlags(args []string) reachabilityFlags {
	set := flag.NewFlagSet("reachability", flag.ExitOnError)
	format := set.String("format", "text", "text|json")
	configPath := set.String("config", "", "policy file, for its exclude list")
	exportedOnly := set.Bool("exported", true, "report only exported definitions (a private symbol the compiler already flags is not news)")
	max := set.Int("max", reachability.DefaultMax, "maximum candidates to list per section")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}
	return reachabilityFlags{root: root, format: *format, config: *configPath, exportedOnly: *exportedOnly, max: *max}
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
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fail(err)
		}
		fmt.Println(string(data))
		return
	}
	printReachability(report)
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
