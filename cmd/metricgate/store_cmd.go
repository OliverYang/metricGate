package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"metricgate/internal/config"
	"metricgate/internal/store"
)

// cmdStore is the one entry point that removes history. Until it existed,
// `check` wrote the trend database and nothing deleted from it, which is how
// the store reached 2.9 GB (measured 2026-10-16: 1417 runs, 21.2M metric
// snapshots). The verb exists so the deletion is something an operator does on
// purpose, with the numbers printed before and after it.
//
// The help tokens are answered here rather than by dispatch because `store` is
// the destructive verb: "what will this delete, and with which flags" has to be
// answerable without first guessing the subcommand, and the answer must come
// from the flag set that does the parsing. It goes to stderr with exit 0, the
// same shape `check -h` and `trends -h` have.
func cmdStore(args []string) {
	if len(args) > 0 && isHelpToken(args[0]) {
		fmt.Fprint(os.Stderr, pruneUsage(newPruneFlagSet()))
		return
	}
	dispatch("store", storeSubcommandList, args, map[string]func([]string){
		"prune": cmdStorePrune,
	})
}

// isHelpToken reports the three spellings of "show me the help" this command
// accepts. No other command in the tree answers them at the parent level, so
// the helper stays local instead of changing dispatch for everyone.
func isHelpToken(arg string) bool {
	switch arg {
	case "-h", "-help", "--help":
		return true
	}
	return false
}

const storeSubcommandList = "prune"

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

// prunePlan is the policy the command will act on plus where it came from. The
// source is printed because "keep the newest 200 runs (from store.retain)" and
// "keep the newest 200 runs (from -runs)" are different statements about the
// repository's configuration, and an operator comparing a dry run to the real
// thing must be able to see which one they tested.
type prunePlan struct {
	policy store.RetainPolicy
	source string
}

// storeRetentionPolicy reads the retention bounds off the policy. Both the
// `store prune` verb and the write path use it, so "what does this repo keep"
// has one answer: the only way they could drift is if one of them read the
// config keys a second time.
func storeRetentionPolicy(cfg *config.Config) store.RetainPolicy {
	return store.RetainPolicy{Runs: cfg.Store.Retain.Runs, Days: cfg.Store.Retain.Days}
}

func resolvePrunePlan(cfg *config.Config, f pruneFlags) prunePlan {
	p := storeRetentionPolicy(cfg)
	source := "store.retain"
	if override(f.runs) {
		p.Runs = f.runs
		source = "-runs/-days"
	}
	if override(f.days) {
		p.Days = f.days
		source = "-runs/-days"
	}
	return prunePlan{policy: p, source: source}
}

func (p prunePlan) describe() string {
	bounds := []string{}
	if p.policy.Runs > 0 {
		bounds = append(bounds, fmt.Sprintf("newest %d runs", p.policy.Runs))
	}
	if p.policy.Days > 0 {
		bounds = append(bounds, fmt.Sprintf("runs started in the last %d days", p.policy.Days))
	}
	if len(bounds) == 0 {
		return "unlimited (no bound set): nothing is ever removed"
	}
	return "keep " + strings.Join(bounds, " or ") + " [" + p.source + "]"
}

// cmdStorePrune retires the runs the retention policy excludes, together with
// their findings and metric snapshots.
//
// -dry-run and the real prune share everything up to the delete: same policy
// resolution, same store.PruneCounts selection, same output shape. A dry run
// that computed its own estimate would be a second number to disbelieve, which
// is what this iteration is meant to end.
func cmdStorePrune(args []string) {
	f, err := parsePruneFlags(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			// set.Usage already printed the flags; -h is a successful answer, not
			// an error. `check -h` and `cockpit serve -h` exit 0 the same way.
			os.Exit(0)
		}
		fail(err)
	}
	cfg, err := loadPolicyFor(f.configPath, ".")
	if err != nil {
		fail(err)
	}
	out, err := runPrune(cfg, f)
	if err != nil {
		fail(err)
	}
	if f.format == "json" {
		printPruneJSON(os.Stdout, out)
		return
	}
	printPruneText(os.Stdout, out)
}

// runPrune is the whole operation minus flag parsing and policy discovery, so a
// test can point it at a temporary database and read back the exact numbers the
// operator would see. It performs no printing.
func runPrune(cfg *config.Config, f pruneFlags) (pruneOutput, error) {
	plan := resolvePrunePlan(cfg, f)
	st, err := openStoreForPrune(cfg)
	if err != nil {
		return pruneOutput{}, err
	}
	defer st.Close()

	before, err := st.Stats()
	if err != nil {
		return pruneOutput{}, err
	}
	var report store.PruneReport
	switch {
	case plan.policy.Unlimited():
		// No bound: nothing is retired and no write transaction is spent on an
		// empty set. Reported as a refusal, not as a clean run.
		report = store.PruneReport{Kept: before.RunCount}
	case f.dryRun:
		report, err = st.PruneCounts(plan.policy)
	default:
		report, err = st.Prune(plan.policy)
	}
	if err != nil {
		return pruneOutput{}, err
	}
	after, err := st.Stats()
	if err != nil {
		return pruneOutput{}, err
	}
	return pruneOutput{plan: plan, dryRun: f.dryRun, report: report, before: before, after: after, path: cfg.Store.Path}, nil
}

// openStoreForPrune opens the configured database, naming the path in the
// failure: "run `metricgate check` first" is only actionable when the operator
// can see which file was being opened.
func openStoreForPrune(cfg *config.Config) (*store.Store, error) {
	if cfg.Store.Path == "" {
		return nil, errors.New("store.path is empty; set store: {path: ...} in the policy or pass -config")
	}
	st, err := store.Open(cfg.Store.Path)
	if err != nil {
		return nil, fmt.Errorf("open store: %w (run `metricgate check` first to create %s)", err, cfg.Store.Path)
	}
	return st, nil
}
