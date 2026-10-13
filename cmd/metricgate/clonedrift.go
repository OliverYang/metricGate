package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"metricgate/internal/clonedrift"
	"metricgate/internal/git"
	"metricgate/internal/sources"
)

// cmdClonedrift reports clone drift: a working-tree change that edits one copy
// of a duplicate without updating its twin. It compares the base revision's
// clones against the working tree, so it is inherently a diff-time check.
func cmdClonedrift(args []string) {
	set := flag.NewFlagSet("clonedrift", flag.ExitOnError)
	configPath := set.String("config", "", "policy file")
	base := set.String("base", "HEAD", "git ref to compare against (merge-base with HEAD)")
	format := set.String("format", "text", "text|json")
	parseArgs(set, args)
	root := "."
	if set.NArg() > 0 {
		root = set.Arg(0)
	}

	cfg, err := loadPolicyFor(*configPath, root)
	if err != nil {
		fail(err)
	}
	resolved := git.ResolveBase(root, *base)
	changed, err := git.ChangedLines(root, resolved)
	if err != nil {
		fail(fmt.Errorf("clonedrift needs a git repository: %w", err))
	}
	baseDir, cleanup, err := git.Archive(root, resolved)
	if err != nil {
		fail(err)
	}
	defer cleanup()

	// The composition root lists the files; the analysis package stays pure.
	workFiles, err := sources.Files(root)
	if err != nil {
		fail(err)
	}
	baseFiles, err := sources.Files(baseDir)
	if err != nil {
		fail(err)
	}
	findings, err := clonedrift.Detect(root, baseDir, baseFiles, workFiles, changed, cfg.ClonePolicy())
	if err != nil {
		fail(err)
	}
	if err := emitClonedrift(os.Stdout, *format, resolved, findings); err != nil {
		fail(err)
	}
}

// emitClonedrift writes the drift report and returns a non-zero exit through
// the caller's gate when drift is found (drift is a warning, not an error, so
// callers decide; this command exits 1 only on report failure).
func emitClonedrift(w io.Writer, format, base string, findings []clonedrift.Finding) error {
	if format == "json" {
		data, err := json.MarshalIndent(findings, "", "  ")
		if err != nil {
			return err
		}
		_, err = w.Write(append(data, '\n'))
		return err
	}
	if len(findings) == 0 {
		fmt.Fprintf(w, "clonedrift: no drifted clones vs %s\n", base)
		return nil
	}
	fmt.Fprintf(w, "clonedrift: %d drifted clone(s) vs %s\n", len(findings), base)
	for _, f := range findings {
		fmt.Fprintf(w, "  %s:%d  %s\n", f.File, f.StartLine, f.Message)
	}
	return nil
}
