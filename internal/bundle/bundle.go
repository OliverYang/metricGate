// Package bundle assembles deterministic, read-only input for a host coding
// agent. It is an adapter layer: no LLM calls, no network access, and no
// changes to source files. The single shared builder is consumed by both the
// CLI and the MCP server so their output cannot drift.
package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"metricgate/internal/config"
	"metricgate/internal/diff"
	"metricgate/internal/engine"
	"metricgate/internal/facts"
	"metricgate/internal/git"
	"metricgate/internal/selection"
)

const SchemaVersion = 1

// Request selects the change set and budget. Mode defaults to workspace.
type Request struct {
	Root          string
	Mode          git.ChangeMode
	From          string
	To            string
	Commit        string
	Base          string
	Budget        int
	Counter       facts.TokenCounter
	IncludeSource bool
}

type PolicyInfo struct {
	Digest string `json:"digest"`
	Source string `json:"source"`
}

// RunInfo records what was compared. Root is always normalized to a relative
// path so an absolute machine path never enters the bundle.
type RunInfo struct {
	Root   string `json:"root"`
	Mode   string `json:"mode"`
	Base   string `json:"base,omitempty"`
	Head   string `json:"head,omitempty"`
	Commit string `json:"commit,omitempty"`
	// TraceID is the ambient trace id ($METRICGATE_TRACE_ID), or empty when
	// the operator set none. It is the key that ties this bundle to the runs
	// and tool calls produced by the same unit of work, and an empty value is
	// a legal state — "no trace" never means "no data", it means the host
	// chose not to attribute this envelope.
	TraceID string `json:"traceId,omitempty"`
}

type BudgetInfo struct {
	Requested       int    `json:"requested"`
	Used            int    `json:"used"`
	Counter         string `json:"counter"`
	Exact           bool   `json:"exact"`
	BudgetExhausted bool   `json:"budgetExhausted"`
}

type SelectionInfo struct {
	Candidates  int                  `json:"candidates"`
	Included    int                  `json:"included"`
	Excluded    int                  `json:"excluded"`
	Unsupported int                  `json:"unsupported"`
	Decisions   []selection.Decision `json:"decisions"`
}

// Constraint is one effective rule, metric, or global constraint. It is a flat
// projection of engine.ExplainItem plus the language, so consumers do not need
// to know the engine's internal shape.
type Constraint struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"` // rule | metric | constraint
	Language   string `json:"language,omitempty"`
	Title      string `json:"title,omitempty"`
	Enabled    bool   `json:"enabled"`
	Severity   string `json:"severity,omitempty"`
	Warn       int    `json:"warn,omitempty"`
	Error      int    `json:"error,omitempty"`
	Source     string `json:"source"`
	SourcePath string `json:"sourcePath,omitempty"`
	Explicit   bool   `json:"explicit"`
}

type Range struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type DiffInfo struct {
	Ranges []Range `json:"ranges"`
}

// FileFact is the compact set of facts for a file. Path and language live on
// the enclosing File, so they are not repeated here.
type FileFact struct {
	Package string         `json:"package,omitempty"`
	Imports []string       `json:"imports,omitempty"`
	Symbols []facts.Symbol `json:"symbols,omitempty"`
}

type File struct {
	Path     string    `json:"path"`
	OldPath  string    `json:"oldPath,omitempty"`
	Status   string    `json:"status"`
	Language string    `json:"language,omitempty"`
	Diff     DiffInfo  `json:"diff"`
	Facts    *FileFact `json:"facts,omitempty"`
	Source   string    `json:"source,omitempty"`
}

type Instructions struct {
	FindingSchema         string `json:"findingSchema"`
	OnlyReportChangedCode bool   `json:"onlyReportChangedCode"`
}

type Bundle struct {
	SchemaVersion int              `json:"schemaVersion"`
	Run           RunInfo          `json:"run"`
	Policy        PolicyInfo       `json:"policy"`
	Selection     SelectionInfo    `json:"selection"`
	Budget        BudgetInfo       `json:"budget"`
	Constraints   []Constraint     `json:"constraints"`
	Files         []File           `json:"files"`
	Pack          PackInfo         `json:"pack"`
	Omitted       []facts.Omission `json:"omitted"`
	Instructions  Instructions     `json:"instructions"`
}

type PackInfo struct {
	Included []facts.PackRef `json:"included"`
	Counter  string          `json:"counter"`
	Exact    bool            `json:"exact"`
}

type BuildOptions struct {
	Request Request
	Config  *config.Config
}

// Build makes a deterministic host-agent bundle. The budget applies to the
// complete serialized JSON envelope, not just an inner text fragment: atomic
// items are packed first, then the full envelope is re-counted and optional
// blocks are dropped deterministically until it fits or nothing optional
// remains.
func Build(opts BuildOptions) (*Bundle, error) {
	req, cfg, err := validate(opts)
	if err != nil {
		return nil, err
	}
	eng, err := engine.New(cfg)
	if err != nil {
		return nil, err
	}
	defer eng.Close()

	b := newBundle(req, cfg)
	plan, err := collectChanges(eng, req, cfg)
	if err != nil {
		return nil, err
	}
	sort.Slice(plan.selection.Decisions, func(i, j int) bool { return plan.selection.Decisions[i].Path < plan.selection.Decisions[j].Path })
	b.Selection = plan.selection

	factsByPath, normalizedFacts, err := normalizeFacts(req.Root, plan.factPaths)
	if err != nil {
		return nil, err
	}
	b.Constraints = buildConstraints(eng, plan.decisions)

	items := addFilesAndItems(b, req, cfg, plan, normalizedFacts, git.ChangedLinesFromChanges(plan.changes))
	if err := fitEnvelope(b, req, items, factsByPath); err != nil {
		return nil, err
	}
	return b, nil
}

func validate(opts BuildOptions) (Request, *config.Config, error) {
	cfg := opts.Config
	if cfg == nil {
		return Request{}, nil, fmt.Errorf("bundle requires a policy config")
	}
	req := opts.Request
	if req.Root == "" {
		req.Root = "."
	}
	if req.Mode == "" {
		req.Mode = git.ModeWorkspace
	}
	if req.Budget <= 0 || req.Counter == nil {
		return Request{}, nil, fmt.Errorf("bundle requires a positive budget and explicit counter")
	}
	return req, cfg, nil
}

func newBundle(req Request, cfg *config.Config) *Bundle {
	b := &Bundle{
		SchemaVersion: SchemaVersion,
		Run:           RunInfo{Root: relativeRoot(req.Root), Mode: string(req.Mode), Base: req.From, Head: req.To, Commit: req.Commit, TraceID: traceID()},
		Policy:        policyInfo(cfg),
	}
	b.Instructions = Instructions{FindingSchema: "metricgate.finding.v1", OnlyReportChangedCode: req.Mode != "directory"}
	return b
}

// changePlan is the resolved change set plus the selection outcome and the
// on-disk paths worth parsing for facts.
type changePlan struct {
	changes   []git.FileChange
	decisions []selection.Decision
	factPaths []string
	selection SelectionInfo
}

func collectChanges(eng *engine.Engine, req Request, cfg *config.Config) (changePlan, error) {
	if req.Mode == "directory" {
		return collectDirectory(eng, req)
	}
	return collectGit(req, cfg)
}

func collectDirectory(eng *engine.Engine, req Request) (changePlan, error) {
	preview, err := eng.Preview(req.Root)
	if err != nil {
		return changePlan{}, err
	}
	var p changePlan
	p.selection.Candidates = preview.Candidate
	p.selection.Excluded = len(preview.Excluded) + len(preview.PrunedDirs)
	p.selection.Unsupported = len(preview.Unsupported)
	p.selection.Decisions = append(p.selection.Decisions, preview.Excluded...)
	p.selection.Decisions = append(p.selection.Decisions, preview.Unsupported...)
	p.selection.Decisions = append(p.selection.Decisions, preview.PrunedDirs...)
	for _, d := range preview.Included {
		p.decisions = append(p.decisions, d)
		p.factPaths = append(p.factPaths, filepath.Join(req.Root, filepath.FromSlash(d.Path)))
		p.changes = append(p.changes, git.FileChange{Path: d.Path, NewPath: d.Path, Status: "S"})
	}
	p.selection.Included = len(p.decisions)
	return p, nil
}

func collectGit(req Request, cfg *config.Config) (changePlan, error) {
	if req.Mode != git.ModeWorkspace && req.Mode != git.ModeRange && req.Mode != git.ModeCommit {
		return changePlan{}, fmt.Errorf("unsupported bundle mode %q", req.Mode)
	}
	changes, err := git.LoadChanges(req.Root, git.ChangeRequest{Mode: req.Mode, From: req.From, To: req.To, Commit: req.Commit, Base: req.Base})
	if err != nil {
		return changePlan{}, err
	}
	var p changePlan
	p.changes = changes
	p.selection.Candidates = len(changes)
	for _, change := range changes {
		d := selection.Decide(change.Path, false, cfg.Exclude)
		p.selection.Decisions = append(p.selection.Decisions, d)
		switch d.Status {
		case selection.Included:
			p.selection.Included++
			p.decisions = append(p.decisions, d)
			p.factPaths = appendExistingFile(p.factPaths, req.Root, change.NewPath)
		case selection.Excluded:
			p.selection.Excluded++
		case selection.Unsupported:
			p.selection.Unsupported++
		}
	}
	return p, nil
}

func appendExistingFile(paths []string, root, newPath string) []string {
	if newPath == "" {
		return paths
	}
	p := filepath.Join(root, filepath.FromSlash(newPath))
	if _, err := os.Stat(p); err == nil {
		return append(paths, p)
	}
	return paths
}

// normalizeFacts parses the selected files; facts.CollectPaths already returns
// root-relative File paths, so the bundle never carries absolute machine paths.
func normalizeFacts(root string, factPaths []string) (map[string]*facts.FileFacts, []*facts.FileFacts, error) {
	fileFacts, err := facts.CollectPaths(root, unique(factPaths))
	if err != nil {
		return nil, nil, err
	}
	byPath := map[string]*facts.FileFacts{}
	for _, f := range fileFacts {
		byPath[f.File] = f
	}
	return byPath, fileFacts, nil
}

// addFilesAndItems appends required diff/source items and populates b.Files.
// changedRanges is the same derivation used by the -diff filter pipeline, so
// the bundle's ranges equal the filter's input by construction.
func addFilesAndItems(b *Bundle, req Request, cfg *config.Config, plan changePlan, normalizedFacts []*facts.FileFacts, changedRanges map[string][]diff.Range) []facts.PackItem {
	items := facts.BuildPackItems(normalizedFacts, cfg, nil)
	langByPath := map[string]string{}
	for _, d := range plan.decisions {
		langByPath[d.Path] = d.Language
	}
	for _, change := range plan.changes {
		if !isIncluded(plan.decisions, change.Path) {
			continue
		}
		path := changePath(change)
		items = append(items, facts.PackItem{ID: "diff:" + path, Section: "diff", Order: facts.OrderDiff, Priority: 0, Required: true, Source: path, Text: renderDiff(change)})
		entry := fileEntry(req, change, path, langByPath[change.Path], changedRanges)
		if entry.Source != "" {
			items = append(items, facts.PackItem{ID: "source:" + path, Section: "source", Order: facts.OrderSource, Priority: 5, Source: path, Text: "## full source " + path + "\n```\n" + entry.Source + "\n```\n"})
		}
		b.Files = append(b.Files, entry)
	}
	sort.Slice(b.Files, func(i, j int) bool { return b.Files[i].Path < b.Files[j].Path })
	return items
}

func changePath(c git.FileChange) string {
	if c.NewPath != "" {
		return c.NewPath
	}
	return c.Path
}

func fileEntry(req Request, change git.FileChange, path, language string, changedRanges map[string][]diff.Range) File {
	entry := File{Path: path, OldPath: change.OldPath, Status: change.Status, Language: language, Diff: DiffInfo{}}
	for _, r := range changedRanges[path] {
		entry.Diff.Ranges = append(entry.Diff.Ranges, Range{Start: r.Start, End: r.End})
	}
	if req.IncludeSource && change.NewPath != "" {
		if data, err := os.ReadFile(filepath.Join(req.Root, filepath.FromSlash(change.NewPath))); err == nil {
			entry.Source = string(data)
		}
	}
	return entry
}

// fitEnvelope packs atomic items, then re-counts the complete serialized JSON
// envelope, dropping optional blocks until it fits or nothing optional remains.
func fitEnvelope(b *Bundle, req Request, items []facts.PackItem, factsByPath map[string]*facts.FileFacts) error {
	pack, err := facts.PackItemsBudgeted(items, facts.BudgetOptions{Budget: req.Budget, Counter: req.Counter})
	if err != nil {
		if err == facts.ErrBudgetExhausted {
			return fmt.Errorf("bundle budget_exhausted: required constraints and diffs need more than %d units", req.Budget)
		}
		return err
	}
	b.Pack = PackInfo{Included: pack.Included, Counter: req.Counter.ID(), Exact: pack.Exact}
	b.Omitted = pack.Omitted
	b.Budget = BudgetInfo{Requested: req.Budget, Counter: req.Counter.ID(), Exact: pack.Exact}
	for i, f := range b.Files {
		if fact, ok := factsByPath[f.Path]; ok && packIncludes(pack, "file:"+f.Path) {
			b.Files[i].Facts = &FileFact{Package: fact.Package, Imports: fact.Imports, Symbols: fact.Symbols}
		}
	}
	for {
		encoded, err := json.Marshal(b)
		if err != nil {
			return err
		}
		used, exact, err := req.Counter.Count(string(encoded))
		if err != nil {
			return err
		}
		b.Budget.Used = used
		b.Budget.Exact = b.Budget.Exact && exact
		if used <= req.Budget {
			normalizeBundle(b)
			return nil
		}
		if !dropOneOptional(&b.Files) {
			b.Budget.BudgetExhausted = true
			return fmt.Errorf("bundle budget_exhausted: envelope and required diff/constraints exceed %d units", req.Budget)
		}
	}
}

// buildConstraints projects the engine's effective rules, metrics, and global
// constraints once per (language, kind, id), sorted deterministically.
func buildConstraints(eng *engine.Engine, decisions []selection.Decision) []Constraint {
	var out []Constraint
	seen := map[string]bool{}
	for _, d := range decisions {
		if d.Language == "" {
			continue
		}
		// Collect constraints for this language
		for _, ruleID := range []string{
			"empty-block", "todo-comment", "print-stack-trace",
		} {
			key := d.Language + "|rule|" + ruleID
			if seen[key] {
				continue
			}
			seen[key] = true

			explain := eng.ExplainRule(d.Language, ruleID)
			out = append(out, Constraint{
				ID:       ruleID,
				Kind:     "rule",
				Language: d.Language,
				Enabled:  explain.Enabled,
				Severity: explain.Severity,
				Source:   explain.PolicySource,
			})
		}
		// Add metrics
		for _, metricID := range []string{
			"cyclomatic-complexity", "cognitive-complexity", "function-length",
			"file-length", "nesting-depth", "params-count",
		} {
			key := d.Language + "|metric|" + metricID
			if seen[key] {
				continue
			}
			seen[key] = true

			explain := eng.ExplainRule(d.Language, metricID)
			out = append(out, Constraint{
				ID:       "metric:" + metricID,
				Kind:     "metric",
				Language: d.Language,
				Enabled:  explain.Enabled,
				Warn:     explain.Thresholds.Warn,
				Error:    explain.Thresholds.Error,
				Source:   explain.PolicySource,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, c := out[i], out[j]
		if a.Language != c.Language {
			return a.Language < c.Language
		}
		if a.Kind != c.Kind {
			return a.Kind < c.Kind
		}
		return a.ID < c.ID
	})
	return out
}

// normalizeBundle makes empty collections serialize as [] rather than null, so
// a host agent never mistakes "not computed" for "empty".
func normalizeBundle(b *Bundle) {
	if b.Constraints == nil {
		b.Constraints = []Constraint{}
	}
	if b.Files == nil {
		b.Files = []File{}
	}
	if b.Omitted == nil {
		b.Omitted = []facts.Omission{}
	}
	if b.Pack.Included == nil {
		b.Pack.Included = []facts.PackRef{}
	}
	for i := range b.Files {
		if b.Files[i].Diff.Ranges == nil {
			b.Files[i].Diff.Ranges = []Range{}
		}
	}
}

func packIncludes(pack facts.PackResult, id string) bool {
	for _, ref := range pack.Included {
		if ref.ID == id {
			return true
		}
	}
	return false
}

// dropOneOptional removes the lowest-value optional envelope block (facts
// before source, both below the required diff/constraints) so the envelope
// shrinks deterministically when the metadata pushes it over budget.
func dropOneOptional(files *[]File) bool {
	for i := len(*files) - 1; i >= 0; i-- {
		if (*files)[i].Facts != nil {
			(*files)[i].Facts = nil
			return true
		}
	}
	for i := len(*files) - 1; i >= 0; i-- {
		if (*files)[i].Source != "" {
			(*files)[i].Source = ""
			return true
		}
	}
	return false
}

// PolicyDigest returns the fingerprint of the policy document actually in
// force, as "sha256:<hex>". It is the one definition shared by the bundle
// envelope, the run store and the machine-readable reports, so "which version
// of the rules produced this verdict" can never have two answers.
//
// The digest covers the EFFECTIVE document, not its file: when a policy
// extends another, the merged result is what ran, so that is what is hashed.
// Digging the file instead would give a repository and its parent the same
// fingerprint while they enforce different rules, and two different merging
// repos different fingerprints while they enforce identical ones.
//
// A policy loaded from bytes rather than a file (the built-in default, a test,
// a stdin policy) has no path to read, so the bytes it was given are digested.
func PolicyDigest(cfg *config.Config) string {
	data := []byte(config.DefaultYAML)
	switch {
	case cfg == nil:
		// keep the default
	case cfg.PolicyDocument() != nil:
		data = cfg.PolicyDocument()
	case cfg.SourcePath != "":
		if raw, err := os.ReadFile(cfg.SourcePath); err == nil {
			data = raw
		}
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// traceID returns the ambient trace id, or "" when the operator set none.
//
// It is read on every call rather than cached, because a bundle is built once
// per request and a process may serve several requests under different traces
// over its life. An empty value is the normal state for a hand-run check: the
// key is only meaningful when a host attributes the work it is doing.
func traceID() string {
	return strings.TrimSpace(os.Getenv("METRICGATE_TRACE_ID"))
}

// policyInfo describes the policy for the envelope. The digest is the shared
// PolicyDigest; Source keeps its own label logic (a basename for a file, the
// source kind otherwise).
func policyInfo(cfg *config.Config) PolicyInfo {
	return PolicyInfo{Digest: PolicyDigest(cfg), Source: PolicySourceLabel(cfg)}
}

// PolicySourceLabel names where the policy in force came from, for the
// human-facing half of the provenance pair: a file basename when a file was
// read, the source kind otherwise. Exported because the SARIF writer stamps
// the same pair, and two spellings of "which policy" would be worse than one.
func PolicySourceLabel(cfg *config.Config) string {
	source := "builtin"
	if cfg.SourcePath != "" {
		if _, err := os.ReadFile(cfg.SourcePath); err == nil {
			source = filepath.Base(cfg.SourcePath)
		}
	} else if cfg.SourceKind != "" && cfg.SourceKind != "builtin" {
		source = cfg.SourceKind
	}
	return source
}

func renderDiff(c git.FileChange) string {
	path := c.Path
	if c.NewPath != "" {
		path = c.NewPath
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## diff %s [%s]\n", path, c.Status)
	if c.OldPath != "" && c.NewPath != "" && c.OldPath != c.NewPath {
		fmt.Fprintf(&b, "renamed from: %s\n", c.OldPath)
	}
	for _, h := range c.Hunks {
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldCount, h.NewStart, h.NewCount)
		for _, line := range h.Deleted {
			b.WriteString("-")
			b.WriteString(line)
			b.WriteByte('\n')
		}
		for _, line := range h.Added {
			b.WriteString("+")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')
	return b.String()
}

func isIncluded(ds []selection.Decision, path string) bool {
	for _, d := range ds {
		if d.Path == path && d.Status == selection.Included {
			return true
		}
	}
	return false
}

func unique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, p := range in {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// relativeRoot keeps an absolute root out of the bundle; the workspace-relative
// contract is what makes the output portable across machines.
func relativeRoot(root string) string {
	if filepath.IsAbs(root) {
		return "."
	}
	root = filepath.ToSlash(filepath.Clean(root))
	root = strings.TrimPrefix(root, "./")
	if root == "" || root == "." {
		return "."
	}
	return root
}
