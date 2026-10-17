package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"strconv"

	"metricgate/internal/store"
)

// This file is the rendering half of `store prune`: the report structure, the
// two output formats, the -h text, and the byte/word formatters. It is separate
// from store_cmd.go because the parsing half and the printing half answer
// different questions — "what did the operator ask for" and "what they read back"
// — and a test of the second one should not have to wade through the first.

type pruneOutput struct {
	plan   prunePlan
	dryRun bool
	report store.PruneReport
	before store.Stats
	after  store.Stats
	path   string
}

// freedBytes estimates what the retired runs occupy, charged at this database's
// own average cost per run: bytes-on-disk divided by runs stored, indexes
// included. It is an estimate and is printed as one. The alternative — a
// per-row constant — is wrong about index pages, and being wrong about index
// pages on a store where two indexes hold 660 MB of the 2.9 GB is not a small
// error.
func (o pruneOutput) freedBytes() int64 {
	return int64(o.report.Runs) * o.before.AverageRunBytes()
}

func printPruneText(w io.Writer, o pruneOutput) {
	fmt.Fprintf(w, "retention: %s\n", o.plan.describe())
	fmt.Fprintf(w, "store: %d runs, %d findings, %d metric snapshots, %s\n",
		o.before.RunCount, o.before.Findings, o.before.Snapshots, humanBytes(o.before.Bytes()))
	// No bound is not the same statement as "bound, nothing to retire". Printing
	// `removed 0 runs` for the default policy would read as though a deletion set
	// was evaluated and found empty, when nothing was ever going to be removed —
	// and that is the exact line an operator skims while deciding whether the
	// command did what they meant.
	if o.plan.policy.Unlimited() {
		fmt.Fprintf(w, "nothing to retire: no bound is set, so %s stay in %s and nothing is ever removed by this command.\n",
			countUnit(o.before.RunCount, "run", "runs"), o.path)
		return
	}
	verb := "removed"
	if o.dryRun {
		verb = "would remove"
	}
	fmt.Fprintf(w, "%s %s (%s findings / %s snapshots), about %s\n",
		verb, countUnit(o.report.Runs, "run", "runs"),
		strconv.Itoa(o.report.Findings), strconv.Itoa(o.report.Snapshots),
		humanBytes(o.freedBytes()))
	if o.report.Runs == 0 {
		fmt.Fprintf(w, "nothing to retire: all %s are kept by policy (archived runs and the newest live run of each project_root are never removed).\n",
			countUnit(o.report.Kept, "run", "runs"))
		return
	}
	fmt.Fprintf(w, "kept: %s\n", countUnit(o.report.Kept, "run", "runs"))
	if o.dryRun {
		// The unchanged-page_count line is the dry run's proof of D22-1, so it is
		// printed even though nothing was expected to move.
		fmt.Fprintf(w, "dry run: database unchanged — page_count %d -> %d, runs %d -> %d\n",
			o.before.PageCount, o.after.PageCount, o.before.RunCount, o.after.RunCount)
		return
	}
	// Saying the file did not shrink in the same breath as the delete is the
	// honest version: the pages are free, the bytes are not back.
	fmt.Fprintf(w, "file: %s -> %s; %s is now free pages. VACUUM would return it (not run automatically).\n",
		humanBytes(o.before.Bytes()), humanBytes(o.after.Bytes()), humanBytes(o.after.FreeBytes()))
}

// pruneJSON is the machine-readable view of one prune. It carries the page
// counts on both sides of the operation rather than a derived "freed" boolean,
// because a caller comparing runs needs to be able to tell a dry run from a
// real one without re-reading prose.
type pruneJSON struct {
	Retention struct {
		Runs   int    `json:"runs"`
		Days   int    `json:"days"`
		Source string `json:"source"`
	} `json:"retention"`
	DryRun          bool   `json:"dry_run"`
	RunsRemoved     int    `json:"runs_removed"`
	FindingsRemoved int    `json:"findings_removed"`
	Snapshots       int    `json:"snapshots_removed"`
	Kept            int    `json:"kept"`
	EstimatedFree   int64  `json:"estimated_free_bytes"`
	Path            string `json:"store_path"`
	Before          struct {
		Runs      int   `json:"runs"`
		Findings  int   `json:"findings"`
		Snapshots int   `json:"snapshots"`
		Bytes     int64 `json:"bytes"`
		PageCount int64 `json:"page_count"`
	} `json:"before"`
	After struct {
		Runs      int   `json:"runs"`
		Bytes     int64 `json:"bytes"`
		PageCount int64 `json:"page_count"`
		FreePages int64 `json:"freelist_count"`
		FreeBytes int64 `json:"free_bytes"`
	} `json:"after"`
}

func printPruneJSON(w io.Writer, o pruneOutput) {
	var j pruneJSON
	j.Path = o.path
	j.Retention.Runs = o.plan.policy.Runs
	j.Retention.Days = o.plan.policy.Days
	j.Retention.Source = o.plan.source
	j.DryRun = o.dryRun
	j.RunsRemoved = o.report.Runs
	j.FindingsRemoved = o.report.Findings
	j.Snapshots = o.report.Snapshots
	j.Kept = o.report.Kept
	j.EstimatedFree = o.freedBytes()
	j.Before.Runs = o.before.RunCount
	j.Before.Findings = o.before.Findings
	j.Before.Snapshots = o.before.Snapshots
	j.Before.Bytes = o.before.Bytes()
	j.Before.PageCount = o.before.PageCount
	j.After.Runs = o.after.RunCount
	j.After.Bytes = o.after.Bytes()
	j.After.PageCount = o.after.PageCount
	j.After.FreePages = o.after.FreePages
	j.After.FreeBytes = o.after.FreeBytes()
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return
	}
	w.Write(append(data, '\n'))
}

// pruneUsage renders `-h` from the flag set that does the parsing, so help and
// accepted flags are one object. The prose says what the command does to the
// database, which no flag list can express.
func pruneUsage(set *flag.FlagSet) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "usage: metricgate %s %s [flags]\n\n", "store", "prune")
	b.WriteString(`Remove the runs store.retain excludes, with their findings and metric
snapshots, in one transaction. Archived runs are never removed, and the
newest live run of each project_root is always kept so a project card does
not go empty. DELETE frees pages without shortening the file; VACUUM is not
run automatically (see docs/iterations/iter-25-store-retention.md).

flags:
`)
	var names []string
	set.VisitAll(func(fl *flag.Flag) { names = append(names, fl.Name) })
	sort.Strings(names)
	for _, name := range names {
		fl := set.Lookup(name)
		def := fl.DefValue
		if def == "" || def == "false" || def == "0" {
			def = "none"
		} else {
			def = strconv.Quote(def)
		}
		fmt.Fprintf(&b, "  -%s\n      %s (default %s)\n", fl.Name, fl.Usage, def)
	}
	return b.String()
}

// humanBytes formats a byte count the way the rest of the tool reports sizes:
// one decimal, no separators, so a number on the terminal is greppable.
func humanBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func countUnit(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
