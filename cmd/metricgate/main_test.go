package main

import (
	"flag"
	"reflect"
	"testing"
)

func newSet() *flag.FlagSet {
	set := flag.NewFlagSet("check", flag.ContinueOnError)
	set.String("config", "policy.yaml", "policy file")
	set.String("format", "text", "output format")
	set.Bool("diff", false, "diff mode")
	set.String("base", "HEAD", "git ref")
	return set
}

func TestParseArgsFlagsAfterPath(t *testing.T) {
	set := newSet()
	parseArgs(set, []string{"internal/diff", "-config", "p.yaml", "-diff"})
	if got := set.Arg(0); got != "internal/diff" {
		t.Errorf("root = %q, want internal/diff", got)
	}
	if v := set.Lookup("config").Value.String(); v != "p.yaml" {
		t.Errorf("-config after path = %q, want p.yaml", v)
	}
	if v := set.Lookup("diff").Value.String(); v != "true" {
		t.Errorf("-diff = %q, want true", v)
	}
}

func TestParseArgsFlagsBeforePath(t *testing.T) {
	set := newSet()
	parseArgs(set, []string{"-format", "json", "."})
	if v := set.Lookup("format").Value.String(); v != "json" {
		t.Errorf("format = %q, want json", v)
	}
	if got := set.Arg(0); got != "." {
		t.Errorf("root = %q, want .", got)
	}
}

// -base takes a value that can itself look like a path; the parser must not
// mistake it for a positional.
func TestParseArgsValueTakingFlagsKeepTheirValues(t *testing.T) {
	set := newSet()
	parseArgs(set, []string{".", "-base", "origin/main", "-format=json"})
	if v := set.Lookup("base").Value.String(); v != "origin/main" {
		t.Errorf("base = %q, want origin/main", v)
	}
	if v := set.Lookup("format").Value.String(); v != "json" {
		t.Errorf("format = %q, want json", v)
	}
	if args := set.Args(); !reflect.DeepEqual(args, []string{"."}) {
		t.Errorf("positionals = %v, want [.]", args)
	}
}

func TestParseArgsDoubleDashStopsFlagParsing(t *testing.T) {
	set := newSet()
	parseArgs(set, []string{"--", "-weird-dir-name"})
	if got := set.Arg(0); got != "-weird-dir-name" {
		t.Errorf("first positional = %q, want -weird-dir-name", got)
	}
}
