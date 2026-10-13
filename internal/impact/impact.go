// Package impact answers one question deterministically: if this symbol
// changes, what else is affected?
//
// It joins the definition index (internal/symbols) with the reference index
// (internal/refs) for a single name and reports the definition sites, every
// use site, and the set of files that would have to be revisited. It is the
// "blast radius" an agent needs before renaming a function or changing a
// signature — answered locally, in milliseconds, for zero tokens.
//
// The single hop is and stays the default. Options.Depth asks for the
// transitive closure: every reference is written inside some enclosing scope,
// and that scope is itself a name other code references, so depth 2 answers
// "who calls the callers". A name-based closure cannot claim to be exact, so
// every reached name carries the tier that says how far it can be trusted
// (see TierResolved) and the walk stops at a width cap instead of pretending
// a hub symbol has a small answer.
//
// The answer is explicitly AST-only: see refs.ConfidenceASTOnly for what that
// does and does not cover.
package impact

import (
	"fmt"
	"sort"
	"strings"

	"metricgate/internal/refs"
	"metricgate/internal/symbols"
)

// ConfidenceASTOnly marks a report produced without type resolution: the
// reference set is name-based, so a call made through an interface, a
// reflection lookup or a dynamically built name can be missed.
const ConfidenceASTOnly = "ast-only"

// DefaultDepth is the historical single hop: the symbol's own use sites and
// nothing about who uses those. It is the default so every existing answer
// stays byte-identical.
const DefaultDepth = 1

// DefaultMaxAffectedFiles caps how wide a closure may grow before the walk
// stops. Measured on a 1,190-file Java repository, 86.9% of defined symbols
// never exceed 25 affected files at any depth up to 4, so the cap removes
// almost no real answer while keeping hubs from producing thousands of files.
const DefaultMaxAffectedFiles = 25

// Reach tiers. They exist because a name with one definition and a name with
// eighteen answer different questions, and reporting one number for both
// would be exactly the lie this package refuses to tell.
const (
	// TierResolved: exactly one definition in the tree, so the name resolves
	// and the closure segment under it can be followed further.
	TierResolved = "resolved"
	// TierAmbiguous: several definitions share the name, so the closure is a
	// name-level union of symbols that have nothing to do with each other.
	TierAmbiguous = "ambiguous"
	// TierExternal: no definition in the tree — a dependency or generated code.
	TierExternal = "external"
)

// Report is the deterministic answer to "what does this symbol touch?".
type Report struct {
	Symbol        string        `json:"symbol"`
	Confidence    string        `json:"confidence"`
	Definitions   []symbols.Def `json:"definitions,omitempty"`
	References    []refs.Ref    `json:"references,omitempty"`
	AffectedFiles []string      `json:"affectedFiles,omitempty"`
	Calls         int           `json:"calls"`
	TypeUses      int           `json:"typeUses"`
	Note          string        `json:"note,omitempty"`
	// Depth is the requested transitive depth. Absent (1) is the single hop;
	// anything above 1 also fills Reach and may set HiddenAffectedFiles.
	Depth int `json:"depth,omitempty"`
	// Reach lists the names beyond the seed that the walk reached, each
	// carrying the tier that says how far it can be trusted.
	Reach []Reach `json:"reach,omitempty"`
	// HiddenAffectedFiles counts affected files dropped by the width cap, so a
	// truncated answer never reads as a complete one.
	HiddenAffectedFiles int `json:"hiddenAffectedFiles,omitempty"`
}

// Reach is one symbol reached while walking outward from the seed.
type Reach struct {
	Name  string   `json:"name"`
	Depth int      `json:"depth"`
	Tier  string   `json:"tier"`
	Files []string `json:"files,omitempty"`
}

// Options tunes the transitive walk. The zero value is the single hop
// Compute has always performed, so every existing answer is unchanged.
type Options struct {
	// Depth is how many hops outward to walk. Values below 1 mean
	// DefaultDepth, so a caller cannot accidentally ask for zero hops.
	Depth int
	// MaxAffectedFiles stops the walk once the affected-file set grows past
	// it. 0 means DefaultMaxAffectedFiles; a negative value disables the cap
	// (a hub symbol's real width is sometimes the whole point of the query).
	MaxAffectedFiles int
}

// depth normalises the requested depth.
func (o Options) depth() int {
	if o.Depth < 1 {
		return DefaultDepth
	}
	return o.Depth
}

// maxAffectedFiles normalises the width cap.
func (o Options) maxAffectedFiles() int {
	if o.MaxAffectedFiles == 0 {
		return DefaultMaxAffectedFiles
	}
	return o.MaxAffectedFiles
}

// Compute joins the definition index with the reference index for one name:
// the single hop, unchanged.
func Compute(name string, defs *symbols.Index, uses *refs.Index) Report {
	return ComputeWith(name, defs, uses, Options{Depth: DefaultDepth})
}

// ComputeWith is Compute plus an optional transitive walk. Depth 1 (and the
// zero Options) produces exactly what Compute produces.
func ComputeWith(name string, defs *symbols.Index, uses *refs.Index, opts Options) Report {
	direct := singleHop(name, defs, uses)
	if opts.depth() <= DefaultDepth {
		return direct
	}
	return walkOutward(direct, defs, uses, opts)
}

// singleHop is the historical answer, kept as its own function so the
// transitive walk starts from the same facts without re-deriving them.
func singleHop(name string, defs *symbols.Index, uses *refs.Index) Report {
	report := Report{Symbol: name, Confidence: ConfidenceASTOnly}
	report.Definitions = defs.Lookup(name)
	report.References = uses.Lookup(name)
	for _, r := range report.References {
		switch r.Kind {
		case refs.KindCall:
			report.Calls++
		case refs.KindType:
			report.TypeUses++
		}
	}
	report.AffectedFiles = affectedFiles(report.References)
	report.Note = noteFor(report)
	return report
}

// noteFor states what the report cannot tell the caller, so a thin answer is
// never mistaken for "nothing depends on this".
func noteFor(r Report) string {
	switch {
	case len(r.Definitions) == 0 && len(r.References) == 0:
		return "no definition or reference found; check the spelling (near-miss names are suggested)"
	case len(r.Definitions) == 0:
		return "no definition in this tree; the symbol is external (dependency or generated code)"
	case len(r.References) == 0:
		return "no AST-visible reference; the symbol may be unused, or only reached via interface dispatch or reflection"
	default:
		return ""
	}
}

// affectedFiles is the sorted unique set of files a set of references names.
// Both the direct answer and a Reach hop ask exactly this question of the same
// shape, so it is one helper, not two Type-2 clones.
func affectedFiles(rs []refs.Ref) []string {
	return uniqueSorted(rs, func(r refs.Ref) string { return r.File })
}

// uniqueSorted collects the non-empty key of each item, deduped, in sorted
// order. The package asks exactly that question of the same shape twice (which
// files does this reference set name, which scopes does it sit in), so the
// loop exists once rather than as two Type-2 clones.
func uniqueSorted[T any](items []T, key func(T) string) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		k := key(it)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// walkOutward expands the direct answer: the scopes that reference the seed
// are themselves referenced, so each further hop adds the callers of the
// callers. It stops at the requested depth or at the width cap, and says which.
func walkOutward(direct Report, defs *symbols.Index, uses *refs.Index, opts Options) Report {
	report := direct
	report.Depth = opts.depth()

	w := &walker{
		defs:    defs,
		uses:    uses,
		visited: map[string]bool{direct.Symbol: true},
		files:   map[string]bool{},
		cap:     opts.maxAffectedFiles(),
	}
	for _, f := range direct.AffectedFiles {
		w.files[f] = true
	}
	frontier := enclosingNames(direct.References)
	for depth := 2; depth <= report.Depth && len(frontier) > 0; depth++ {
		frontier = w.level(frontier, depth)
		if w.stopped {
			break
		}
	}

	report.Reach = w.reach
	report.AffectedFiles, report.HiddenAffectedFiles = capFiles(w.sorted(), w.cap)
	report.Note = deepNote(report)
	return report
}

// walker carries the state of one outward walk so each step stays a short
// method rather than a function with five parameters.
type walker struct {
	defs    *symbols.Index
	uses    *refs.Index
	visited map[string]bool
	files   map[string]bool
	reach   []Reach
	cap     int
	stopped bool
}

// level records every unvisited name in the frontier as a Reach, folds the
// files of its use sites into the affected set, and returns the next frontier.
// It returns nil and marks itself stopped as soon as the width cap is passed:
// a closure that wide is not an answer, and pretending otherwise would be.
func (w *walker) level(frontier []string, depth int) []string {
	var next []string
	for _, name := range frontier {
		if w.visited[name] {
			continue
		}
		w.visited[name] = true
		rs := w.uses.Lookup(name)
		for _, r := range rs {
			w.files[r.File] = true
		}
		w.reach = append(w.reach, Reach{
			Name:  name,
			Depth: depth,
			Tier:  tierFor(w.defs, name),
			// The files of THIS name's own use sites, not the accumulated
			// walk: a hop that references nothing (an entrypoint, a test
			// that is run rather than called) has no files of its own, and
			// reporting the walk so far there would claim the closure
			// already stopped expanding when it did not.
			Files: affectedFiles(rs),
		})
		next = append(next, enclosingNames(rs)...)
		if w.cap >= 0 && len(w.files) > w.cap {
			w.stopped = true
			return nil
		}
	}
	return next
}

// sorted is the affected-file set in deterministic order.
func (w *walker) sorted() []string {
	out := make([]string, 0, len(w.files))
	for f := range w.files {
		if f != "" {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// tierFor says how far a reached name's answer can be trusted: one definition
// resolves by name, several means a name-level union, none means the name
// lives outside the tree.
func tierFor(defs *symbols.Index, name string) string {
	switch n := len(defs.Lookup(name)); {
	case n == 1:
		return TierResolved
	case n > 1:
		return TierAmbiguous
	default:
		return TierExternal
	}
}

// enclosingNames collects the scopes a set of references were written in:
// those are the functions whose own callers the next hop must look up.
func enclosingNames(rs []refs.Ref) []string {
	return uniqueSorted(rs, func(r refs.Ref) string { return r.Enclosing })
}

// capFiles truncates a sorted list to the cap and reports how much was left
// out, so a wide answer never reads as a complete one. A negative cap (the
// caller asked for the real width) keeps everything.
func capFiles(files []string, cap int) ([]string, int) {
	if cap < 0 || len(files) <= cap {
		return files, 0
	}
	return files[:cap], len(files) - cap
}

// deepNote states the two things a multi-hop answer cannot claim: that the
// seed is exactly one symbol, and that the walk saw everything there was.
func deepNote(r Report) string {
	var parts []string
	if n := len(r.Definitions); n > 1 && len(r.Reach) > 0 {
		parts = append(parts, fmt.Sprintf("name-level result unreliable: the seed has %d definitions, so the closure mixes unrelated symbols", n))
	}
	if r.HiddenAffectedFiles > 0 {
		parts = append(parts, fmt.Sprintf("%d affected file(s) past the width cap were not expanded", r.HiddenAffectedFiles))
	}
	return strings.Join(parts, "; ")
}
