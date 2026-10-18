package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"metricgate/internal/config"
	"metricgate/internal/store"
)

// This file is the authorization half of the destructive path in `store prune`.
// It answers one question before any row is deleted: is this deletion aimed at a
// file that has been named, and if so, does the operator confirm it?
//
// It is separate from store_cmd.go because both of those happen *before* the
// delete and neither of them is the delete. A guard buried in runPrune would
// have to be read through the report-assembly code to be reviewed at all, and
// this is the code that decides whether a real prune runs.
//
// The guard exists because the store path comes from the working directory: a
// `store prune` run from a repository root finds that repository's
// .metricgate.yaml, reads its store.path, and deletes from a real development
// database. Measured in the round this was written: 78 runs, 29,503 findings
// and 1,869,629 snapshots removed from the live store, restored from a copy
// made by hand beforehand.

// policyFileSource is the one SourceKind that names the file a prune would act
// on: config.LoadFile tags a document it read from disk this way, while
// config.Default() tags the built-in template "builtin".
//
// The comparison is deliberately positive — a source that names a file, not
// "anything that is not built-in". A third kind added later (an environment
// overlay, a remote policy) would read as "named" under a negative test and be
// waved through the exact check it should have failed.
const policyFileSource = "policy_file"

// guardRequired reports whether a prune needs authorization at all. Only a real
// deletion needs it: a dry run reads nothing, and an unlimited policy deletes
// nothing (D22-5 — its refusal sentence already has a test pinning it).
func guardRequired(plan prunePlan, f pruneFlags) bool {
	return !f.dryRun && !plan.policy.Unlimited()
}

// errPruneCancelled is the answer to a prompt the operator did not confirm. It
// is a sentinel rather than a formatted string because the caller must be able
// to tell "the human said no" from "the guard could not resolve the store": the
// first is a normal outcome of asking, the second is a broken command.
var errPruneCancelled = errors.New("prune cancelled: nothing was removed")

// prunePrompt is the pair of process facts the guard asks about when it decides
// whether a human is actually there: the reader to ask through, and whether that
// reader is a terminal.
//
// They travel together because one without the other is meaningless — asking a
// question of a pipe is exactly the case this type exists to refuse — and because
// bundling them keeps authorizePrune's parameter list at the width the repository
// enforces: the guard's inputs are the policy, the plan and the counts, and a
// human being is one fact, not two.
type prunePrompt struct {
	stdin       io.Reader
	interactive bool
}

// authorizePrune decides whether the real prune may run.
//
// An error means no deletion happens; the caller must not fall back to deleting
// without it. prompt carries the reader and the terminal answer the production
// caller derives from os.Stdin, so the non-interactive pipeline case is testable
// without a pty.
func authorizePrune(cfg *config.Config, plan prunePlan, declared store.PruneReport, f pruneFlags, prompt prunePrompt) error {
	if !guardRequired(plan, f) {
		return nil
	}
	if cfg.SourceKind != policyFileSource {
		// The problem is not that the operator is unwilling to confirm: it is that
		// nothing has named the file. -yes must not unlock this, because the flag
		// answers "don't ask me" and the question here is "which file?".
		return fmt.Errorf(`refusing to prune: no policy file was named
  This store came from the built-in defaults, because no .metricgate.yaml was found from this directory.
  Run ` + "`store prune -dry-run`" + ` first, then name the policy explicitly:
    metricgate store prune -config /absolute/path/to/.metricgate.yaml [-yes]
  or add store.retain to that file so the bound comes from a named document.`)
	}
	path, err := filepath.Abs(cfg.Store.Path)
	if err != nil {
		return fmt.Errorf("resolve store path %s: %w", cfg.Store.Path, err)
	}
	fmt.Fprintf(os.Stderr, "about to remove %s\n", pruneDeletionLine(declared))
	fmt.Fprintf(os.Stderr, "  from %s\n", path)
	if f.yes {
		// -yes skips the prompt, not the evidence: the declaration above is
		// printed either way, because a scripted prune is still a deletion.
		return nil
	}
	if !prompt.interactive {
		return fmt.Errorf("refusing to prune non-interactively without -yes; add -yes to confirm %s", path)
	}
	if !confirmDeletion(os.Stderr, prompt.stdin) {
		// The same two next steps as the source refusal: "no" may come from a human
		// who changed their mind or from a stdin that never held one — /dev/null is
		// a character device, so an abandoned prompt lands here rather than on the
		// non-interactive branch. Both readings must leave the operator with
		// something to type, not just a cancellation.
		return fmt.Errorf("%w — re-run with -dry-run to preview, or -yes to confirm %s",
			errPruneCancelled, path)
	}
	return nil
}

// confirmDeletion asks one yes/no question and reads one line. A reader that is
// empty or closed (EOF) answers no, so an abandoned prompt deletes nothing
// rather than falling through to the default.
func confirmDeletion(w io.Writer, stdin io.Reader) bool {
	fmt.Fprintf(w, "remove this history? [y/N] ")
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

// pruneDeletionLine renders the counts a prune declares it will remove. It is
// shared by the pre-delete declaration and the text report's warning so the two
// cannot describe the same numbers differently.
func pruneDeletionLine(r store.PruneReport) string {
	return fmt.Sprintf("%s (%s findings / %s snapshots)", countUnit(r.Runs, "run", "runs"),
		strconv.Itoa(r.Findings), strconv.Itoa(r.Snapshots))
}

// stdinIsInteractive reports whether stdin is a terminal. os.Stdin.Stat plus
// os.ModeCharDevice is the standard-library answer; the alternative,
// github.com/mattn/go-isatty, is currently an indirect dependency of the
// toolchain and promoting it for one bit of a file mode would be a real
// change to go.mod for no behaviour.
//
// The error is treated as "not interactive": a stdin that cannot be described
// must not be assumed to be a human.
func stdinIsInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// pruneStdin and pruneIsInteractive are the two process-level facts the guard
// asks about, held as variables so a test can put a terminal — or a pipe, or a
// closed reader — in front of the deletion without spawning a pty.
//
// The guard must not read os.Stdin directly and stop there: "is a human
// watching" is precisely what makes a piped `store prune` refuse instead of
// deleting quietly, and a safety property that depends on the process's own file
// descriptors is worthless if no test can reach it. authorizePrune still decides
// from its own arguments, so the judgement itself is testable without touching
// these at all.
var (
	pruneStdin         io.Reader = os.Stdin
	pruneIsInteractive           = stdinIsInteractive
)

// prunePromptFromSeams is what the production caller passes: the two process
// facts read at call time, so a replacement made by a test applies to the next
// prune and nothing else.
func prunePromptFromSeams() prunePrompt {
	return prunePrompt{stdin: pruneStdin, interactive: pruneIsInteractive()}
}

// withPruneStdin points the two seams at r and at a fixed answer to "is this a
// terminal", returning the function that puts them back.
func withPruneStdin(r io.Reader, interactive bool) func() {
	oldReader, oldCheck := pruneStdin, pruneIsInteractive
	pruneStdin = r
	pruneIsInteractive = func() bool { return interactive }
	return func() {
		pruneStdin, pruneIsInteractive = oldReader, oldCheck
	}
}
