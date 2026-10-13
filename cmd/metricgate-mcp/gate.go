package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"metricgate/internal/accept"
	"metricgate/internal/git"
	"metricgate/internal/model"
	"metricgate/internal/parser"
)

// maxGateFiles caps how many changed files one pass re-checks, so a single
// bulk edit cannot turn the next tool call into a full-repo run. Files are
// sorted oldest-first, so the oldest edits are checked first; anything past
// the cap is reported as skipped rather than silently dropped.
const maxGateFiles = 20

// gateWatcher implements the "inline verdict" design: metricgate-mcp is the
// long-lived process, so it — not the harness — notices edits. On every tool
// call it re-checks source files that changed since the previous call and
// prepends the verdict to that call's response. No daemon, no harness hook:
// correctness rests on the file system plus one git invocation per call.
//
// The watcher only ever adds content; a failure anywhere in this path degrades
// to "no gate preamble" and never fails the tool call itself.
type gateWatcher struct {
	eng checker
	// root is the directory the MCP server was started in, resolved to the
	// Git worktree root once at startup. Empty (no worktree, git absent)
	// disables the watcher entirely.
	root string
	// ledger carries the repository's acceptance ledger (loaded once at
	// startup): accepted warnings stay visible in the preamble but don't
	// count toward the error/warning totals, matching CLI check semantics.
	ledger *accept.Ledger

	mu        sync.Mutex
	lastCheck time.Time
}

// checker is the slice of the engine the watcher needs; *engine.Engine
// satisfies it and tests use a stub.
type checker interface {
	CheckSource(name string, src []byte) ([]model.Finding, error)
}

// newGateWatcher resolves the session root once. Without a Git worktree there
// is no cheap change signal, so the watcher stands down — the CLI exit-code
// gate and pre-commit remain the correctness backstops.
func newGateWatcher(eng checker) *gateWatcher {
	g := &gateWatcher{eng: eng, root: git.Root(".")}
	if g.root != "" {
		// A missing ledger is normal (nothing ever accepted); other read
		// errors degrade to a nil ledger — the preamble still works, the
		// concessions just count as open until the file is fixed.
		if l, err := accept.Load(filepath.Join(g.root, accept.DefaultPath)); err == nil {
			g.ledger = l
		}
	}
	return g
}

// preamble returns the gate verdict for files changed since the last call, or
// "" when there is nothing to report. It marks the window before scanning, so
// an edit landing mid-scan is picked up next call, not lost.
func (g *gateWatcher) preamble() string {
	if g == nil || g.root == "" {
		return ""
	}
	g.mu.Lock()
	windowStart := g.lastCheck
	g.lastCheck = time.Now()
	g.mu.Unlock()

	files, skipped := g.changedFiles(windowStart)
	if len(files) == 0 && skipped == 0 {
		return ""
	}
	return g.render(files, skipped)
}

// changedFiles returns supported-language files under root modified after
// since, oldest first, plus how many were dropped by the cap. Untracked files
// come from LoadChanges; tracked edits are filtered by mtime so unchanged
// checkouts between calls report nothing.
func (g *gateWatcher) changedFiles(since time.Time) ([]string, int) {
	touched := g.touchedPaths()
	sort.Strings(touched)
	return g.selectFiles(touched, since)
}

// touchedPaths flattens the workspace change set into candidate paths,
// keeping the post-rename path when present.
func (g *gateWatcher) touchedPaths() []string {
	changes, err := git.LoadChanges(g.root, git.ChangeRequest{Mode: git.ModeWorkspace})
	if err != nil {
		return nil
	}
	var touched []string
	for _, ch := range changes {
		if ch.Status == "D" {
			continue
		}
		if ch.Path != "" {
			touched = append(touched, ch.Path)
			continue
		}
		if ch.OldPath != "" {
			touched = append(touched, ch.OldPath)
		}
	}
	return touched
}

// selectFiles filters candidates to supported languages whose mtime falls in
// the window, deduplicates, and enforces the per-pass cap.
func (g *gateWatcher) selectFiles(touched []string, since time.Time) ([]string, int) {
	var files []string
	skipped := 0
	seen := map[string]bool{}
	for _, p := range touched {
		full, ok := g.inWindow(p, since)
		if !ok || seen[full] {
			continue
		}
		seen[full] = true
		if len(files) == maxGateFiles {
			skipped++
			continue
		}
		files = append(files, full)
	}
	return files, skipped
}

// inWindow reports whether one candidate is a supported-language file that
// exists and was modified inside the window, returning its full path.
func (g *gateWatcher) inWindow(p string, since time.Time) (string, bool) {
	if _, ok := parser.LanguageForFile(filepath.Base(p)); !ok {
		return "", false
	}
	full := filepath.Join(g.root, p)
	info, err := os.Stat(full)
	if err != nil {
		return "", false
	}
	if !since.IsZero() && !info.ModTime().After(since) {
		return "", false
	}
	return full, true
}

// render checks each file and renders the preamble the agent reads. Findings
// are capped in the text as well: the preamble rides along with an unrelated
// answer, so it must stay small.
func (g *gateWatcher) render(files []string, skipped int) string {
	lines, errors, warnings := g.findingLines(files)
	if errors == 0 && warnings == 0 && skipped == 0 {
		return ""
	}
	lines = append([]string{gateHead(errors, warnings)}, lines...)
	if skipped > 0 {
		lines = append(lines, fmt.Sprintf("  (+%d changed files past the %d-file cap: run check_path to cover the rest)", skipped, maxGateFiles))
	}
	return strings.Join(lines, "\n")
}

// gateHead renders the one-line verdict the agent reads first.
func gateHead(errors, warnings int) string {
	switch {
	case errors > 0:
		return fmt.Sprintf("[metricgate] ⛔ gate: %d error(s) in changed files — fix before finishing", errors)
	case warnings > 0:
		return fmt.Sprintf("[metricgate] ⚠ gate: %d warning(s) in changed files", warnings)
	default:
		return "[metricgate] gate: changed files checked"
	}
}

// findingLines checks every file and renders up to five finding lines plus
// the severity totals.
func (g *gateWatcher) findingLines(files []string) (lines []string, errors, warnings int) {
	shown := 0
	for _, p := range files {
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		findings, err := g.eng.CheckSource(p, src)
		if err != nil {
			continue
		}
		errors += countSeverity(findings, model.SevError)
		warnings += countOpenWarnings(findings, g.ledger)
		for _, f := range findings {
			if shown >= 5 {
				return lines, errors, warnings
			}
			tag := ""
			if f.Accepted {
				tag = "  [accepted by ledger]"
			}
			lines = append(lines, fmt.Sprintf("  %s:%d %s: %s%s", filepath.Base(p), f.StartLine, f.Severity, f.Message, tag))
			shown++
		}
	}
	return lines, errors, warnings
}

// countSeverity counts the findings at one severity level.
func countSeverity(findings []model.Finding, sev model.Severity) int {
	n := 0
	for _, f := range findings {
		if f.Severity == sev {
			n++
		}
	}
	return n
}

// countOpenWarnings counts warnings not covered by an unexpired acceptance
// ledger entry, matching the CLI gate's convention: a concession keeps the
// finding visible but stops it from tripping the verdict.
func countOpenWarnings(findings []model.Finding, ledger *accept.Ledger) int {
	n := 0
	now := time.Now()
	for i := range findings {
		f := &findings[i]
		if f.Severity != model.SevWarning {
			continue
		}
		if ledger != nil && ledger.IsAccepted(f.RuleID, f.File, f.StartLine, now) {
			f.Accepted = true
		}
		if !f.Accepted {
			n++
		}
	}
	return n
}

// wrapGate layers the inline-verdict preamble onto a tool handler: before the
// handler runs it collects files changed since the previous call, and on the
// way out it prepends their verdict as the first content block of the
// response. The preamble is advisory content, not an error flag, so the
// structured result and the handler's error are untouched. Any failure inside
// the gate path degrades to no preamble. A nil watcher disables the layer.
//
// A handler that returns a nil result relies on the SDK to serialise its Out
// into the text channel. Writing our own Content block into that nil result
// would make the SDK skip that serialisation, so the preamble would replace
// the payload instead of preceding it — the same query would return a verdict
// on one call and the answer on the next, depending only on whether a file had
// been edited in between. To keep both, the preamble block is followed by the
// serialised Out.
func wrapGate[In, Out any](g *gateWatcher, h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	if g == nil {
		return h
	}
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		res, out, err := h(ctx, req, in)
		if err != nil {
			return res, out, err
		}
		text := g.preamble()
		if text == "" {
			return res, out, err
		}
		block := &mcp.TextContent{Text: text}
		if res == nil {
			// The handler gave no content of its own, so its Out is what the
			// model would have read. Serialise it here to keep that view.
			payload, mErr := json.Marshal(out)
			if mErr != nil {
				// Without the payload the preamble would be the whole answer,
				// which is exactly the failure this guard exists to prevent.
				// Leave the result to the SDK so the output stays complete.
				return nil, out, err
			}
			res = &mcp.CallToolResult{Content: []mcp.Content{block, &mcp.TextContent{Text: string(payload)}}}
		} else {
			res.Content = append([]mcp.Content{block}, res.Content...)
		}
		return res, out, err
	}
}
