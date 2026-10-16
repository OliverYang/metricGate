// Package baseline reads the metric values the tree had at the base revision.
//
// It is the "did this change make an existing measurement worse?" half of
// diff-aware gating, and it lives here rather than in the CLI shell because
// there is more than one gate entry point: the CLI (`check -diff`) and the
// MCP server (`check_path {diff_only:true}`) must answer that question from
// one implementation. Two copies would drift, and the drift would show up as
// the same finding reported to a human in a terminal and withheld from the
// agent that just wrote the code.
//
// The engine deliberately does not do this: it is a pure library with no
// process boundary, and building the base tree is a git operation. So the
// git call sits here, behind the same adapter layer internal/git already
// belongs to, and every entry point keeps only its own rendering.
package baseline

import (
	"fmt"
	"slices"

	"metricgate/internal/config"
	"metricgate/internal/diff"
	"metricgate/internal/engine"
	"metricgate/internal/git"
	"metricgate/internal/model"
	"metricgate/internal/selection"
)

// Split is the whole diff-only decision one entry point has to make: read the
// changed ranges, read what the base revision measured, and split the run's
// findings by what this change owns.
//
// It exists so there is exactly one answer to "is this finding new?". The CLI
// (`check -diff`) and the MCP server (`check_path {diff_only:true}`) each used
// to assemble these three steps themselves, and the MCP side had no base step
// at all, so a metric finding the CLI hid as "value unchanged" was reported to
// the agent that just wrote it — and the reverse mismatch was one refactor
// away. Entry points keep only their rendering; none of them chooses the
// git-side or the filter on its own.
//
// root is the check root the run analysed and base the revision to compare
// against ("HEAD" for both entry points today; the CLI also accepts -base).
// The Changed ranges are returned as well because the CLI's add-ons
// (regress, scorecard, impact, gate checks) consume them directly.
func Split(cfg *config.Config, root, base string, summary *model.RunSummary) (Result, error) {
	changed, err := git.ChangedLinesFor(root, base)
	if err != nil {
		return Result{}, err
	}
	baseValues, err := Snapshot(cfg, root, base, changed)
	if err != nil {
		return Result{}, err
	}
	return Result{DiffSplit: engine.SplitSummary(summary, changed, baseValues), Changed: changed}, nil
}

// Result is Split's answer: the filter's three verdict groups (Kept, the
// findings this change owns; PreExisting, those outside every changed range;
// Unchanged, those inside a changed range whose measured value did not move)
// plus the changed ranges the CLI's add-ons consume directly.
//
// The three groups stay explicit rather than collapsed into one count because
// "0 new findings" and "nothing is wrong" are different claims, and neither
// entry point may substitute 0 for "this tree had no base revision" (see
// Snapshot): a caller that only sees len(Kept) cannot tell those apart either.
type Result struct {
	engine.DiffSplit
	Changed map[string][]diff.Range
}

// Snapshot runs the engine over the base revision, restricted to the files the
// change actually touched, and returns the values indexed for the diff filter.
//
// Only touched files can produce a verdict whose value the filter has to
// compare, and a repository that carries thousands of legacy files would
// otherwise pay for a second full analysis to answer a question about a
// handful of them.
//
// An empty (nil) map is a meaningful result, not a failure: it says nothing
// touched exists at the base revision — every touched file is new — and the
// filter reads it as "no base values", therefore "keep everything". Callers
// must not substitute zero for it, or a brand-new file's findings would be
// compared against a base of 0 and every threshold would look like a
// regression.
//
// root is the check root the current run analysed; the diff's keys are
// relative to it, while the archive is built from the worktree root, so the
// two coordinate systems are resolved here, once, for the whole map.
func Snapshot(cfg *config.Config, root, base string, changed map[string][]diff.Range) (engine.BaseValues, error) {
	repoPaths, err := repoRelativePaths(root, changed)
	if err != nil {
		return nil, err
	}
	if len(repoPaths) == 0 {
		return nil, nil
	}
	dir, cleanup, err := git.ArchivePaths("", git.ResolveBase("", base), repoPaths)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if dir == "" {
		return nil, nil
	}
	eng, err := engine.New(cfg)
	if err != nil {
		return nil, err
	}
	defer eng.Close()
	summary, err := eng.Run(dir)
	if err != nil {
		return nil, err
	}
	return engine.IndexBaseValues(rebaseToCheckRoot(summary.Metrics, root)), nil
}

// rebaseToCheckRoot moves base-revision snapshot paths out of the worktree's
// coordinate system and into the check root's, so they can be joined against
// the run being gated.
//
// The archive is built from the worktree, so the second engine run reports
// paths relative to it; the run the gate is filtering reports paths relative
// to the check root. At the worktree root those two are the same string, which
// is why this looked unnecessary: under a subdirectory they share no key at
// all, so every value comparison missed and the metric half of the ratchet
// degraded to keep-everything without saying so.
//
// Metrics outside the check root are dropped, not guessed at. They cannot
// correspond to anything in the gated run, and inventing a path for them would
// be exactly the silent substitution this function exists to remove. A check
// root that is not inside a git worktree yields the metrics unchanged, which
// is the conservative direction: the callers treat an unjoinable key as "no
// base value" and keep the finding.
func rebaseToCheckRoot(ms []model.MetricResult, root string) []model.MetricResult {
	rel, ok := git.NewRepoRelativizer(root)
	if !ok {
		return ms
	}
	out := make([]model.MetricResult, 0, len(ms))
	for _, m := range ms {
		path, ok := rel.Unrel(m.File)
		if !ok {
			continue
		}
		m.File = path
		out = append(out, m)
	}
	return out
}

// FullSnapshot is Snapshot over the whole base tree, returned as raw metric
// results rather than an index.
//
// The regression report needs every scope at the base revision, not only the
// touched ones, because "this change made the file's fan-out worse" is one
// question and "these measurements drifted" is another. Paths come back
// relative to root — the same coordinate system the current run's snapshots
// use — so the two sides join without leaking a machine absolute path into the
// comparison, and without depending on root being the worktree root.
func FullSnapshot(cfg *config.Config, root, base string) ([]model.MetricResult, error) {
	dir, cleanup, err := git.Archive("", git.ResolveBase("", base))
	if err != nil {
		return nil, err
	}
	defer cleanup()
	eng, err := engine.New(cfg)
	if err != nil {
		return nil, err
	}
	defer eng.Close()
	summary, err := eng.Run(dir)
	if err != nil {
		return nil, err
	}
	return rebaseToCheckRoot(RelativizeMetrics(summary.Metrics, dir), root), nil
}

// RelativizeMetrics rewrites every snapshot path relative to root, so two
// runs share one key space.
//
// Exported because the current tree's snapshots need the same treatment as the
// base tree's before the two are compared; a caller that relativizes only one
// side joins on nothing.
//
// Note the asymmetry it hides: this only rewrites *absolute* paths (see
// selection.SanitizePath). A caller that hands it an already-relative path gets
// it back unchanged, so passing a check root while holding worktree-relative
// paths silently produces the wrong key space. rebaseToCheckRoot is the
// conversion for that case.
func RelativizeMetrics(ms []model.MetricResult, root string) []model.MetricResult {
	out := make([]model.MetricResult, len(ms))
	for i, m := range ms {
		m.File = selection.SanitizePath(root, m.File)
		out[i] = m
	}
	return out
}

// repoRelativePaths maps the diff's check-root-relative keys onto the worktree
// root, because the archive is produced from the worktree. Paths that escape
// the worktree are dropped rather than guessed.
//
// The result is sorted: git.ArchivePaths feeds it straight into a subprocess
// argument list, and an unsorted slice built from a map would make the base
// run — and therefore the verdict — depend on map iteration order.
//
// This is only half of the round trip. The archive comes back keyed
// worktree-relatively, and the run being gated is keyed check-root-relatively,
// so whichever caller indexes the archive's metrics must map them back with
// git.RepoRelativizer.Unrel before comparing. RepoRelative alone joins the two
// systems only when the check root is the worktree root.
func repoRelativePaths(root string, changed map[string][]diff.Range) ([]string, error) {
	rel, ok := git.NewRepoRelativizer(root)
	if !ok {
		return nil, fmt.Errorf("%s is not inside a git worktree", root)
	}
	out := make([]string, 0, len(changed))
	for file := range changed {
		if repoPath, ok := rel.Rel(file); ok {
			out = append(out, repoPath)
		}
	}
	slices.Sort(out)
	return out, nil
}
