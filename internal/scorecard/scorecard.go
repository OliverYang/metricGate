// Package scorecard aggregates a diff-scoped engine summary into a
// deterministic "LLM change scorecard": a handful of counters that describe
// how a change touches the things this tool gates on — architecture
// boundaries, dependency legality, banned APIs, metric ceilings, metric
// regressions, clones, and pseudo-completion markers.
//
// The scorecard is report-only: it never changes gate semantics. It exists
// because agent-written changes fail differently (swallowed errors, banned
// APIs, TODO markers left behind, cloned code), and a flat finding list hides
// the shape of the change.
package scorecard

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"metricgate/internal/diff"
	"metricgate/internal/model"
	"metricgate/internal/regress"
	"metricgate/internal/selection"
	"metricgate/internal/stubs"
)

// Card is the scorecard for one diff. Every counter is scoped to the changed
// files, so pre-existing dirt in untouched code never muddies the picture.
type Card struct {
	ChangedFiles int  `json:"changedFiles"`
	ChangedLines int  `json:"changedLines"`
	TestsTouched bool `json:"testsTouched"`

	// Boundary and legality violations.
	StructureViolations  int `json:"structureViolations"`
	DependencyViolations int `json:"dependencyViolations"`
	BannedAPICalls       int `json:"bannedApiCalls"`

	// Metric pressure: values over a threshold, values that got worse than
	// the base revision, and copied code.
	ThresholdBreaches int `json:"thresholdBreaches"`
	MetricRegressions int `json:"metricRegressions"`
	ClonedCode        int `json:"clonedCode"`

	// Pseudo-completion: TODO/FIXME markers, stub bodies, parse failures.
	IncompleteWork int `json:"incompleteWork"`
	ParseErrors    int `json:"parseErrors"`

	// TopRules is the most frequent finding kinds in the diff, count-ordered
	// (ties broken by rule id), at most five.
	TopRules []RuleTally `json:"topRules,omitempty"`
}

// RuleTally is one row of TopRules.
type RuleTally struct {
	Rule  string `json:"rule"`
	Count int    `json:"count"`
}

// Options carries the diff scope and the repository root for the stub scan.
// ScanStubs costs a second parse pass; callers that cannot afford it still
// get a card, with IncompleteWork derived from findings alone.
type Options struct {
	Changed   map[string][]diff.Range
	Root      string
	ScanStubs bool
}

// Build folds a (diff-filtered) summary into a Card.
func Build(summary *model.RunSummary, opt Options) Card {
	changed := changedSet(opt.Changed)
	c := Card{ChangedFiles: len(opt.Changed)}
	c.ChangedLines, c.TestsTouched = diffTotals(opt.Changed)
	tally := map[string]int{}

	for _, f := range summary.Findings {
		if _, ok := changed[selection.SanitizePath(opt.Root, f.File)]; !ok {
			continue
		}
		tally[f.RuleID]++
		bumpFamily(&c, f)
	}

	// Stub bodies (panic("not implemented"), empty function bodies) are the
	// signature of work that only looks finished. A failed stub scan degrades
	// the card silently: the counter keeps its finding-derived value, because
	// the scorecard is a bonus view, never a gate input.
	if opt.ScanStubs {
		if items, err := stubs.Scan(opt.Root); err == nil {
			c.IncompleteWork += stubsInDiff(items, changed, opt.Root)
		}
	}

	for _, p := range summary.ParseErrors {
		// Entries look like "file:line:col: message"; scope them to the diff.
		if _, ok := changed[filePathOf(p)]; ok {
			c.ParseErrors++
		}
	}

	c.TopRules = topRules(tally, 5)
	return c
}

// diffTotals sums the added lines and reports whether any changed path is a
// test file.
func diffTotals(changed map[string][]diff.Range) (lines int, tests bool) {
	for path, ranges := range changed {
		for _, r := range ranges {
			if r.End >= r.Start {
				lines += r.End - r.Start + 1
			}
		}
		if isTestFile(path) {
			tests = true
		}
	}
	return lines, tests
}

// bumpFamily routes one in-scope finding into exactly one family counter.
// The order matters: a metric-regression finding carries the metric source,
// so it must be recognized by rule id before the generic metric branch.
func bumpFamily(c *Card, f model.Finding) {
	switch {
	case isStructure(f.RuleID):
		c.StructureViolations++
	case f.RuleID == "unknown-dependency":
		c.DependencyViolations++
	case strings.HasPrefix(f.RuleID, "banned-"):
		c.BannedAPICalls++
	case f.RuleID == regress.RuleID:
		c.MetricRegressions++
	case f.Source == model.SrcMetric:
		c.ThresholdBreaches++
	case isClone(f.RuleID):
		c.ClonedCode++
	}
	if f.RuleID == "todo-comment" {
		c.IncompleteWork++
	}
}

// stubsInDiff counts stub items that live on changed files.
func stubsInDiff(items []stubs.Item, changed map[string]bool, root string) int {
	n := 0
	for _, it := range items {
		if _, ok := changed[selection.SanitizePath(root, it.File)]; ok {
			n++
		}
	}
	return n
}

// Print renders the card for humans. In json mode it renders one
// pretty-printed JSON object instead, so machine consumers can keep parsing
// stdout untouched (the caller wires this to the same human/err split the
// check command already uses).
func Print(w io.Writer, c Card, jsonMode bool) {
	if jsonMode {
		b, _ := json.MarshalIndent(c, "", "  ")
		fmt.Fprintln(w, string(b))
		return
	}
	fmt.Fprintln(w, "scorecard (agent diff):")
	fmt.Fprintf(w, "  files %d · lines %d · tests touched: %s\n",
		c.ChangedFiles, c.ChangedLines, yesNo(c.TestsTouched))
	fmt.Fprintf(w, "  structure %d · dependency %d · banned-api %d\n",
		c.StructureViolations, c.DependencyViolations, c.BannedAPICalls)
	fmt.Fprintf(w, "  threshold %d · regression %d · clone %d\n",
		c.ThresholdBreaches, c.MetricRegressions, c.ClonedCode)
	fmt.Fprintf(w, "  incomplete-work %d · parse-error %d\n", c.IncompleteWork, c.ParseErrors)
	if len(c.TopRules) > 0 {
		parts := make([]string, len(c.TopRules))
		for i, t := range c.TopRules {
			parts[i] = fmt.Sprintf("%s×%d", t.Rule, t.Count)
		}
		fmt.Fprintf(w, "  top rules: %s\n", strings.Join(parts, ", "))
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// changedSet normalizes the diff into a lookup set of repo-relative paths.
func changedSet(changed map[string][]diff.Range) map[string]bool {
	out := make(map[string]bool, len(changed))
	for path := range changed {
		out[strings.TrimSpace(path)] = true
	}
	return out
}

// isStructure reports whether the rule guards an architecture boundary:
// layer edges, package cycles, stable-dependency direction, or any
// engine-level forbid rule.
func isStructure(ruleID string) bool {
	switch ruleID {
	case "layer-violation", "import-cycle", "sdp-violation":
		return true
	}
	return strings.HasPrefix(ruleID, "forbid")
}

// isClone reports whether the rule flags copied code (function clones,
// near-clones, or any future *-clone family member).
func isClone(ruleID string) bool {
	if ruleID == "near-clone" || ruleID == "clone-drift" {
		return true
	}
	return strings.HasSuffix(ruleID, "-clone")
}

// isTestFile is the cross-language test-path heuristic: Go's _test.go
// suffix, Java's src/test/java tree, and the FooTest.java convention.
func isTestFile(path string) bool {
	if strings.HasSuffix(path, "_test.go") {
		return true
	}
	if strings.Contains(path, "/test/") {
		return true
	}
	return strings.HasSuffix(path, "Test.java")
}

// filePathOf extracts the leading path from a "file:line:col: message" string.
func filePathOf(s string) string {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return s[:i]
	}
	return s
}

// topRules returns up to n rules by descending count, ties in rule-id order,
// so the same diff always prints the same card.
func topRules(tally map[string]int, n int) []RuleTally {
	if len(tally) == 0 {
		return nil // an empty diff yields a true zero Card
	}
	out := make([]RuleTally, 0, len(tally))
	for rule, count := range tally {
		out = append(out, RuleTally{Rule: rule, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Rule < out[j].Rule
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
