package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
)

// This file is the question `store prune` asks the operator: what did you
// mean? It holds the flag set, its parsing, and the validation a destructive
// command cannot skip. Split out of store_cmd.go because the two halves answer
// different questions — which flags exist, and what the command does with them —
// and a reviewer checking "can -yes unlock the guard?" should not have to read
// the delete to find out.

// pruneFlags is what `store prune` accepts. The retention overrides are here
// because the dry run is a question about a policy an operator is *considering*
// setting: writing a candidate bound into .metricgate.yaml to see its effect
// would edit the policy file every other command and every other session in
// this repository reads.
type pruneFlags struct {
	configPath string
	dryRun     bool
	runs       int
	days       int
	format     string
	vacuum     bool
	yes        bool
}

// unsetBound is the sentinel "this flag was not given", so 0 keeps its meaning
// from the policy file: store.retain.runs: 0 is unlimited, and -runs 0 must not
// mean something else than the key it overrides.
const unsetBound = -1

// newPruneFlagSet builds the flag set for `store prune`. It is a function
// returning the set rather than a flag.ExitOnError set because the -h body and
// the tests need the same object the parser uses: a help text written out by
// hand is how a `-port` that never existed ended up in docs/capabilities.md.
func newPruneFlagSet() *flag.FlagSet {
	set := flag.NewFlagSet("store prune", flag.ContinueOnError)
	set.Usage = func() { fmt.Fprint(os.Stderr, pruneUsage(set)) }
	set.String("config", "", "policy file (used to locate store.path and store.retain)")
	set.Bool("dry-run", false, "report what would be removed and change nothing")
	set.Int("runs", unsetBound, "override store.retain.runs for this invocation (-1 = use the policy, 0 = unlimited)")
	set.Int("days", unsetBound, "override store.retain.days for this invocation (-1 = use the policy, 0 = unlimited)")
	set.String("format", "text", "text or json")
	set.Bool("vacuum", false, "after a real prune, VACUUM the file to return the freed pages (needs free disk equal to the database)")
	set.Bool("yes", false, "do not ask before removing history; the deletion is still reported and recorded")
	return set
}

// parsePruneFlags returns an error rather than exiting so the validation of a
// destructive flag set is something a test can reach.
//
// The parse error is fatal, not swallowed: this is the one verb that deletes
// history, and a mistyped -dry-run that the parser quietly ignored would run
// the real prune while the operator believes they only asked for a preview.
func parsePruneFlags(args []string) (pruneFlags, error) {
	set := newPruneFlagSet()
	flags, rest := splitFlags(set, args)
	if err := set.Parse(append(flags, rest...)); err != nil {
		return pruneFlags{}, err
	}
	f := pruneFlags{
		configPath: flagText(set, "config"),
		dryRun:     flagText(set, "dry-run") == "true",
		format:     flagText(set, "format"),
		vacuum:     flagText(set, "vacuum") == "true",
		yes:        flagText(set, "yes") == "true",
	}
	// -dry-run means "change nothing", and a VACUUM rewrites the file. Refusing
	// the pair is the same discipline as parsing the destructive flag set
	// strictly: the operator asked for a preview, and a preview must not touch
	// the bytes it is previewing.
	if f.vacuum && f.dryRun {
		return pruneFlags{}, errors.New("-vacuum and -dry-run contradict: a dry run changes nothing, a vacuum rewrites the file")
	}
	for _, name := range []string{"runs", "days"} {
		n, err := boundFlag(set, name)
		if err != nil {
			return pruneFlags{}, err
		}
		switch name {
		case "runs":
			f.runs = n
		case "days":
			f.days = n
		}
	}
	if f.format != "text" && f.format != "json" {
		return pruneFlags{}, fmt.Errorf("-format must be text or json, got %q", f.format)
	}
	return f, nil
}

// override reports whether the operator gave this bound on the command line.
func override(v int) bool { return v != unsetBound }

// flagText reads one flag's value as text. Going through the parsed set rather
// than a parallel struct keeps `-h` and the parser on one source of truth.
func flagText(set *flag.FlagSet, name string) string { return set.Lookup(name).Value.String() }

// boundFlag validates a retention override. The message spells out all three
// meanings because -1, 0 and N are three different intents and the flag that
// takes them belongs to the one verb that deletes history.
func boundFlag(set *flag.FlagSet, name string) (int, error) {
	n, err := strconv.Atoi(flagText(set, name))
	if err != nil {
		return 0, fmt.Errorf("-%s: %w", name, err)
	}
	if n < unsetBound {
		return 0, fmt.Errorf("-%s: %d is not a bound; -1 uses the policy value, 0 means unlimited, anything else is the number of runs or days to keep", name, n)
	}
	return n, nil
}
