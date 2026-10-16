package main

import (
	"flag"
	"fmt"
	"reflect"
	"testing"
)

func newSet() *flag.FlagSet {
	set := flag.NewFlagSet("check", flag.ContinueOnError)
	set.String("config", ".metricgate.yaml", "policy file")
	set.String("format", "text", "output format")
	set.Bool("diff", false, "diff mode")
	set.String("base", "HEAD", "git ref")
	return set
}

func TestIsLoopbackAddress(t *testing.T) {
	cases := []struct {
		address string
		want    bool
	}{
		{address: "127.0.0.1:8787", want: true},
		{address: "[::1]:8787", want: true},
		{address: "localhost:8787", want: false},
		{address: "0.0.0.0:8787", want: false},
		{address: "192.168.1.10:8787", want: false},
		{address: "bad-address", want: false},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			if got := isLoopbackAddress(tc.address); got != tc.want {
				t.Errorf("isLoopbackAddress(%q) = %v, want %v", tc.address, got, tc.want)
			}
		})
	}
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

// The build stamp is what answers "am I running the binary I just built?".
// toolVersion is a constant, so every local build prints the same version; the
// revision is the only part that differs, and it is only useful if it is
// shortened consistently and never claims to be a clean build when the tree
// was dirty.
func TestFormatBuildStamp(t *testing.T) {
	cases := []struct {
		name     string
		revision string
		dirty    bool
		want     string
	}{
		{name: "clean short", revision: "5c1595c79c62", want: "5c1595c79c62"},
		{name: "clean long truncates", revision: "5c1595c79c6269bbc2d4d8bd06161cf5cf447d7e", want: "5c1595c79c62"},
		{name: "dirty marks the tree", revision: "5c1595c79c6269bbc2d4d8bd06161cf5cf447d7e", dirty: true, want: "5c1595c79c62+dirty"},
		// `go run` and `go test` link without VCS stamping. Returning "" would
		// print "metricgate 0.1.0 " with a trailing space, which reads as a
		// truncation bug rather than as a missing stamp.
		{name: "unstamped", revision: "", want: "(no vcs stamp)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatBuildStamp(tc.revision, tc.dirty); got != tc.want {
				t.Errorf("formatBuildStamp(%q, %v) = %q, want %q", tc.revision, tc.dirty, got, tc.want)
			}
		})
	}
}
