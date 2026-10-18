package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

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
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, storeBareMessage(newPruneFlagSet()))
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

// storeBareMessage is what `metricgate store` prints when someone types the
// destructive verb and nothing after it. dispatch still refuses with exit 2 — a
// bare `store` must not look like it did something — but the refusal alone
// teaches nothing about the safe order of operations, so the usage block and the
// preview command come first.
//
// It renders from the same *flag.FlagSet that parses the real flags, for the same
// reason store -h does: a usage block missing a flag that exists (or carrying one
// that does not) is how docs/capabilities.md acquired a `-port` nobody could pass.
func storeBareMessage(set *flag.FlagSet) string {
	return pruneUsage(set) + "\n" +
		"to see what a prune would do before it does it:\n" +
		"  metricgate store prune -dry-run\n"
}

// runPrune is the whole operation minus flag parsing and policy discovery, so a
// test can point it at a temporary database and read back the exact numbers the
// operator would see. It performs no printing.
//
// It is a sequence of named steps rather than one body, because the steps have
// different reasons to fail and the reason matters on this path: an error raised
// after declarePrune returns means a human has already been asked, and an error
// after executePrune means rows are already gone. Inlined, those three states
// share one `if err != nil` and the function reads as though they were one.
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
	guard, declared, err := declarePrune(cfg, st, plan, f)
	if err != nil {
		return pruneOutput{}, err
	}
	report, err := executePrune(st, plan, f, before)
	if err != nil {
		return pruneOutput{}, err
	}
	after, err := st.Stats()
	if err != nil {
		return pruneOutput{}, err
	}
	out := pruneOutput{
		plan:     plan,
		dryRun:   f.dryRun,
		report:   report,
		declared: declared,
		before:   before,
		after:    after,
		path:     cfg.Store.Path,
		guard:    guard,
	}
	if err := recordPruneOutcome(cfg, st, out); err != nil {
		return pruneOutput{}, err
	}
	return applyVacuum(st, f, out)
}

// declarePrune counts what a real prune is about to remove and asks whether it
// may, returning the guard's own account of that decision for the report.
//
// A dry run declares nothing separately: the report it prints *is* the
// declaration, and counting it twice would be two numbers to disbelieve.
func declarePrune(cfg *config.Config, st *store.Store, plan prunePlan, f pruneFlags) (pruneGuard, store.PruneReport, error) {
	guard := pruneGuard{
		Required: guardRequired(plan, f),
		// Source is the origin of the policy that located the store, not the
		// origin of the bound: the guard cares about whether a file named these
		// bytes, and retention.source already answers the other question.
		Source:   cfg.SourceKind,
		Explicit: f.configPath != "",
	}
	if f.dryRun {
		return guard, store.PruneReport{}, nil
	}
	// The real prune counts the same selection before it deletes anything, so what
	// the operator approved can be compared to what the delete reported. It is the
	// query the dry run already runs; paying it again on the real path (~0.33s on
	// this repository's store) buys the only replayable evidence of what was
	// claimed.
	declared, err := st.PruneCounts(plan.policy)
	if err != nil {
		return pruneGuard{}, store.PruneReport{}, err
	}
	if err := authorizePrune(cfg, plan, declared, f, prunePromptFromSeams()); err != nil {
		return pruneGuard{}, store.PruneReport{}, err
	}
	// An authorization that was never required is not a confirmation worth
	// reporting; and if it was required, reaching here means the guard did not
	// refuse, which is the whole content of the claim.
	guard.Confirmed = guard.Required
	return guard, declared, nil
}

// executePrune picks the one outcome the policy and the flags select. They are
// mutually exclusive by construction: an unlimited policy spends no write
// transaction at all, a dry run only counts, and everything else is the delete.
func executePrune(st *store.Store, plan prunePlan, f pruneFlags, before store.Stats) (store.PruneReport, error) {
	switch {
	case plan.policy.Unlimited():
		// No bound: nothing is retired and no write transaction is spent on an
		// empty set. Reported as a refusal, not as a clean run.
		return store.PruneReport{Kept: before.RunCount}, nil
	case f.dryRun:
		return st.PruneCounts(plan.policy)
	default:
		return st.Prune(plan.policy)
	}
}

// recordPruneOutcome writes the declared/removed pair after a real prune, so the
// approval and the deletion can be compared by whichever process reads the store
// next. A dry run records nothing, and an unlimited policy records nothing:
// neither removed anything, and an intent for a pass that could not delete would
// bury the passes that did.
func recordPruneOutcome(cfg *config.Config, st *store.Store, out pruneOutput) error {
	if out.dryRun || out.plan.policy.Unlimited() {
		return nil
	}
	return st.RecordPruneIntent(store.PruneIntent{
		At:        time.Now().UTC(),
		Source:    cfg.SourceKind,
		Declared:  store.ReportJSONOf(out.declared),
		Removed:   store.ReportJSONOf(out.report),
		StorePath: out.path,
	})
}

// applyVacuum rewrites the file when the operator asked for it and the prune
// actually freed pages.
//
// It runs after the stats are read, so the reported free pages are the ones the
// prune produced. Rewriting a 500 MB file to reclaim zero pages is pure cost, and
// -dry-run is refused with -vacuum at parse time.
func applyVacuum(st *store.Store, f pruneFlags, out pruneOutput) (pruneOutput, error) {
	if !f.vacuum || out.report.Runs == 0 {
		return out, nil
	}
	if err := st.Vacuum(); err != nil {
		return out, fmt.Errorf("vacuum: %w", err)
	}
	out.vacuumed = true
	after, err := st.Stats()
	if err != nil {
		return out, err
	}
	out.after = after
	return out, nil
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
