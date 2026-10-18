package main

import (
	"errors"
	"flag"
	"fmt"
	"metricgate/internal/config"
	"metricgate/internal/store"
	"os"
	"strings"
)

// This file turns a policy and a set of flags into the one plan `store prune`
// acts on, plus the provenance line the report prints. Split out of store_cmd.go
// because "which bound won, and where did it come from" is the question an operator
// asks *before* the guard's "may I delete this", and the guard's answer is only
// meaningful once the plan is settled.

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
