package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"metricgate/internal/gittest"
	"metricgate/internal/model"
)

// The MCP server and the CLI are the same gate seen from two surfaces: an
// agent inside a harness and a developer at a terminal, or both at once on the
// same tree. If the two disagree about which findings survive -diff, one of
// them is lying, and the one that lies is the one nobody notices -- the server
// used to skip the value-delta baseline the CLI applied, so it reported metric
// findings the terminal had already dropped as "unchanged vs HEAD".
//
// These tests do not poke the filter directly. They build both real binaries,
// point them at one controlled dirty tree, and compare what each one reports.
// That is the only observation that can catch an entry point re-deriving the
// rules for itself, which is what had happened.
//
// Fixture shape, and why it is this shape: a.go is modified inside A()'s span,
// so the line-only part of the ratchet keeps every A() finding and only the
// comparison against the base revision's measured values can drop them; b.go is
// never touched, so its findings must be hidden by the line filter alone. A
// fixture without a changed range that overlaps a metric's span would match the
// CLI even if the caller dropped the baseline entirely, and the parity check
// would pass while proving nothing.

// dirtyTreeA is modified by the fixture (one string literal, inside the deepest
// nest) so its line count and every complexity measure stay as they were at
// HEAD.
const dirtyTreeA = `package basepkg

import "fmt"

func A(p1, p2, p3, p4, p5, p6 int) int {
	if p1 > 0 {
		if p2 > 0 {
			if p3 > 0 {
				if p4 > 0 {
					if p5 > 0 {
						if p6 > 0 {
							fmt.Println("deep")
						}
					}
				}
			}
		}
	}
	return p1
}
`

// dirtyTreeB is never touched: each of its findings must be hidden by the line
// filter, which is what makes `hidden` a number worth comparing.
const dirtyTreeB = `package basepkg

import "fmt"

func B(p1, p2, p3, p4, p5, p6 int) int {
	if p1 > 0 {
		if p2 > 0 {
			fmt.Println("untouched")
		}
	}
	return p1
}
`

// dirtyTreePolicy disables the history store -- the fixture lives in a temp dir
// and a test that writes a multi-gigabyte database there is a far worse surprise
// than a test that fails. The todo-comment rule is off so the fixture's findings
// come only from metrics and the banned-api rule, which is what the ratchet
// filters.
const dirtyTreePolicy = `version: 1
store:
  enabled: false
gate:
  fail_on: error
languages:
  go:
    rules:
      todo-comment:
        enabled: false
`

// newDirtyTree builds the fixture described above and returns its root. It is
// its own git worktree at one commit, so it does not depend on this repository's
// working-tree state -- other sessions dirty this tree continuously, and a test
// that measured against it would drift under them.
func newDirtyTree(t *testing.T) string {
	t.Helper()
	dir := gittest.InitRepo(t)
	gittest.Write(t, dir, ".metricgate.yaml", dirtyTreePolicy)
	gittest.Write(t, dir, "basepkg/a.go", dirtyTreeA)
	gittest.Write(t, dir, "basepkg/b.go", dirtyTreeB)
	gittest.Run(t, dir, "add", "-A")
	gittest.Run(t, dir, "commit", "-qm", "init")
	gittest.Write(t, dir, "basepkg/a.go", strings.Replace(dirtyTreeA, `"deep"`, `"deeper"`, 1))
	if out := gittest.Run(t, dir, "status", "--porcelain"); out != "M basepkg/a.go" {
		t.Fatalf("fixture is not in the expected dirty state: %q", out)
	}
	return dir
}

// buildGatePair compiles both real entry points from the current tree.
func buildGatePair(t *testing.T) (cliBin, mcpBin string) {
	t.Helper()
	root := repoRoot(t)
	out := t.TempDir()
	cliBin = filepath.Join(out, "metricgate")
	mcpBin = filepath.Join(out, "metricgate-mcp")
	for _, spec := range []struct{ bin, pkg string }{{cliBin, "./cmd/metricgate"}, {mcpBin, "./cmd/metricgate-mcp"}} {
		build := exec.Command("go", "build", "-o", spec.bin, spec.pkg)
		build.Dir = root
		if b, err := build.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", spec.pkg, err, b)
		}
	}
	return cliBin, mcpBin
}

// tuple is the identity of a finding for comparison: rule, file, line,
// severity. Message text is excluded -- both surfaces read the same policy, so
// it adds no discriminating power -- while these four fields are exactly what an
// agent acts on.
type tuple [4]string

func tuplesOf(findings []model.Finding) []tuple {
	out := make([]tuple, 0, len(findings))
	for _, f := range findings {
		out = append(out, tuple{f.RuleID, filepath.ToSlash(f.File), fmt.Sprint(f.StartLine), string(f.Severity)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

func (t tuple) key() string    { return strings.Join(t[:], "\x00") }
func (t tuple) String() string { return fmt.Sprintf("(%s, %s, line %s, %s)", t[0], t[1], t[2], t[3]) }

// diffTuples returns what each side has that the other does not.
func diffTuples(a, b []tuple) (onlyA, onlyB []tuple) {
	inA, inB := map[tuple]bool{}, map[tuple]bool{}
	for _, x := range a {
		inA[x] = true
	}
	for _, x := range b {
		inB[x] = true
	}
	for _, x := range a {
		if !inB[x] {
			onlyA = append(onlyA, x)
		}
	}
	for _, x := range b {
		if !inA[x] {
			onlyB = append(onlyB, x)
		}
	}
	return onlyA, onlyB
}

func formatTuples(ts []tuple) string {
	if len(ts) == 0 {
		return "(none)"
	}
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = t.String()
	}
	return strings.Join(parts, ", ")
}

// gateReport is one entry point's -diff answer: the findings it kept plus the
// two counters describing what it declined to report.
type gateReport struct {
	Findings  []model.Finding
	Files     int
	Errors    int
	Warnings  int
	Hidden    int
	Unchanged int
}

// cliDiffReport decodes the CLI's json envelope. `verdict` is an object, so it
// is decoded into a nested struct even though only `passed` is read.
type cliDiffReport struct {
	Findings []model.Finding `json:"findings"`
	Files    int             `json:"files"`
	Errors   int             `json:"errors"`
	Warnings int             `json:"warnings"`
	Verdict  struct {
		Passed bool `json:"passed"`
	} `json:"verdict"`
}

// diffLine parses the CLI's human summary, the only place the CLI reports what
// it filtered OUT:
//
//	diff-only: base=HEAD, 1 new findings, 2 pre-existing hidden, 3 unchanged metric(s) dropped
//
// The unchanged clause is absent when no metric finding was dropped, which is
// exactly the subdirectory case, so it is optional here and asserted separately.
var diffLine = regexp.MustCompile(`diff-only: base=(\S+), (\d+) new findings, (\d+) pre-existing hidden(?:, (\d+) unchanged metric\(s\) dropped)?`)

// runCLI asks the real CLI to gate a root from within dir.
func runCLI(t *testing.T, cliBin, dir, root string) gateReport {
	t.Helper()
	cmd := exec.Command(cliBin, "check", root, "-diff", "-format", "json")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// exit 2 only on a real gate failure; fail_on=error here keeps exit at 0.
	if err := cmd.Run(); err != nil {
		t.Fatalf("cli check %s -diff: %v\nstdout: %s\nstderr: %s", root, err, firstLine(stdout.String()), stderr.String())
	}
	var rep cliDiffReport
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("decode cli json (%d bytes): %v\n%s", stdout.Len(), err, firstLine(stdout.String()))
	}
	m := diffLine.FindStringSubmatch(stderr.String())
	if m == nil {
		t.Fatalf("cli printed no diff-only summary, so its counters are unknown.\nstderr: %s", firstLine(stderr.String()))
	}
	out := gateReport{Findings: rep.Findings, Files: rep.Files, Errors: rep.Errors, Warnings: rep.Warnings}
	if _, err := fmt.Fscanf(strings.NewReader(m[3]), "%d", &out.Hidden); err != nil {
		t.Fatalf("parse hidden count %q: %v", m[3], err)
	}
	if m[4] != "" {
		if _, err := fmt.Fscanf(strings.NewReader(m[4]), "%d", &out.Unchanged); err != nil {
			t.Fatalf("parse unchanged count %q: %v", m[4], err)
		}
	}
	return out
}

// runFullCLI gates without -diff, to measure how much the ratchet removed.
func runFullCLI(t *testing.T, cliBin, dir, root string) gateReport {
	t.Helper()
	cmd := exec.Command(cliBin, "check", root, "-format", "json")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("cli check %s: %v\nstderr: %s", root, err, firstLine(stderr.String()))
	}
	var rep cliDiffReport
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("decode cli json: %v\n%s", err, firstLine(stdout.String()))
	}
	return gateReport{Findings: rep.Findings, Files: rep.Files, Errors: rep.Errors, Warnings: rep.Warnings}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
