// Near-clone detection: functions that are not byte-identical after
// normalisation (so the exact-hash pass of clones.go missed them) but
// share most of their normalised token set (Phase B-4 of
// docs/phase-b-design.md — the "function-level Jaccard" compromise,
// deliberately not full winnowing: this catches "copy + rename + small
// edit", which is the LLM-generated scenario, not arbitrary partial
// duplication).
//
// Jaccard similarity over shingle sets: J = |A∩B| / |A∪B|. Candidates are
// paired only inside buckets that share at least one rare shingle, so a
// large run never pays the full n² comparison.
package clones

import (
	"os"
	"path/filepath"
	"sort"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/parser"
	"metricgate/internal/sources"
)

// NearConfig tunes the near-clone pass.
type NearConfig struct {
	MinLines     int     // minimum lines of a candidate function
	MinTokens    int     // minimum shingle-supported tokens
	Threshold    float64 // minimum Jaccard similarity to report (0.8)
	MaxPerAnchor int     // cap per anchor to bound report size (0 = 3)
}

// DefaultNear is the out-of-the-box sensitivity from the design doc.
var DefaultNear = NearConfig{MinLines: 10, MinTokens: 40, Threshold: 0.8}

// NearMatch is one near-clone pair above the threshold.
type NearMatch struct {
	Anchor     Func // the first-defined function
	Other      Func // the near-duplicate
	Similarity float64
}

// Shingles is the set of hashed k-token shingles of one body, with the
// token count kept for the union size of the Jaccard denominator.
type Shingles struct {
	Set    map[uint64]bool
	Tokens int
}

// FingerprintShingles extracts the normalised token stream of a body and
// returns its 3-gram shingle set. Must run inside the parse workers (it
// walks the tree).
func FingerprintShingles(src []byte, body *sitter.Node) Shingles {
	var toks []string
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil || n.IsExtra() {
			return
		}
		if n.ChildCount() == 0 {
			if n.IsNamed() {
				toks = append(toks, "id")
			} else {
				toks = append(toks, nodeText(src, n))
			}
			return
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(body)

	const k = 3
	set := make(map[uint64]bool, len(toks))
	var h uint64
	for i := 0; i+k <= len(toks); i++ {
		h = 0
		for j := 0; j < k; j++ {
			h = fnv1a(h, idMark)
			for x := 0; x < len(toks[i+j]); x++ {
				h = fnv1a(h, toks[i+j][x])
			}
		}
		set[h] = true
	}
	return Shingles{Set: set, Tokens: len(toks)}
}

// NearFunc carries a function plus its shingle set for the pairing stage.
type NearFunc struct {
	Func
	Shingles Shingles
}

// FindNear pairs near-clones: candidates that share at least one rare
// shingle are compared in full; pairs at or above the threshold are
// reported with the first-defined function as the anchor.
func FindNear(cands []NearFunc, cfg NearConfig) []NearMatch {
	if cfg.Threshold <= 0 || cfg.Threshold >= 1 {
		return nil
	}
	postings := shinglePostings(cands, cfg)
	pairs := rarePairs(postings)
	matches := similarPairs(cands, pairs, cfg)
	return capPerAnchor(matches, cfg.maxPerAnchor())
}

// maxPerAnchor resolves the report cap (0 means the default 3).
func (c NearConfig) maxPerAnchor() int {
	if c.MaxPerAnchor > 0 {
		return c.MaxPerAnchor
	}
	return 3
}

// shinglePostings inverts candidate shingles; functions below the size
// gates and shingles shared by nearly everything (no signal) drop out.
func shinglePostings(cands []NearFunc, cfg NearConfig) map[uint64][]int {
	sizeGate := Config{MinLines: cfg.MinLines, MinTokens: cfg.MinTokens}
	postings := map[uint64][]int{}
	for i, c := range cands {
		if !bigEnough(c.Func, sizeGate) {
			continue
		}
		for sh := range c.Shingles.Set {
			postings[sh] = append(postings[sh], i)
		}
	}
	return postings
}

// rarePairs collects the candidate index pairs that share at least one
// rare shingle (a shingle posted by few functions carries signal; a
// common one would explode the pairing).
func rarePairs(postings map[uint64][]int) map[[2]int]bool {
	pairs := map[[2]int]bool{}
	for _, idxs := range postings {
		if len(idxs) > 8 {
			continue
		}
		for a := 0; a < len(idxs); a++ {
			for b := a + 1; b < len(idxs); b++ {
				ia, ib := idxs[a], idxs[b]
				if ia > ib {
					ia, ib = ib, ia
				}
				pairs[[2]int{ia, ib}] = true
			}
		}
	}
	return pairs
}

// similarPairs scores each candidate pair and keeps the matches at or
// above the threshold, anchored on the first-defined member.
func similarPairs(cands []NearFunc, pairs map[[2]int]bool, cfg NearConfig) []NearMatch {
	var out []NearMatch
	seen := map[[2]int]bool{}
	for pk := range pairs {
		if seen[pk] {
			continue
		}
		seen[pk] = true
		a, b := cands[pk[0]], cands[pk[1]]
		if a.Lang != b.Lang {
			continue
		}
		sim := jaccard(a.Shingles, b.Shingles)
		if sim < cfg.Threshold {
			continue
		}
		first, second := a.Func, b.Func
		if lessFunc(second, first) {
			first, second = second, first
		}
		out = append(out, NearMatch{Anchor: first, Other: second, Similarity: sim})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return lessFunc(out[i].Anchor, out[j].Anchor)
	})
	return out
}

// capPerAnchor bounds how many matches one anchor may contribute, so a
// fan-out clone family cannot flood the report.
func capPerAnchor(matches []NearMatch, maxPer int) []NearMatch {
	perAnchor := map[string]int{}
	var kept []NearMatch
	for _, m := range matches {
		k := m.Anchor.File + "|" + m.Anchor.Name + "|" + itoa2(m.Anchor.StartLine)
		if perAnchor[k] >= maxPer {
			continue
		}
		perAnchor[k]++
		kept = append(kept, m)
	}
	return kept
}

// jaccard is |A∩B| / |A∪B| over shingle sets, using token counts for the
// union when sets overlap (a set cannot exceed its token count).
func jaccard(a, b Shingles) float64 {
	if a.Tokens == 0 || b.Tokens == 0 {
		return 0
	}
	inter := 0
	for sh := range a.Set {
		if b.Set[sh] {
			inter++
		}
	}
	union := len(a.Set) + len(b.Set) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func itoa2(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// ScanNear walks root, parses every supported file and reports near-clone
// pairs across the whole tree. It is a standalone pass (its own parse
// sweep) because retaining shingle sets for the whole run in the engine's
// per-file pipeline would hold every parse tree open; near-clones are
// report-only (design doc round 4), so a separate command is enough.
func ScanNear(root string, cfg NearConfig) ([]NearMatch, error) {
	files, err := sources.Files(root)
	if err != nil {
		return nil, err
	}
	parsers := map[parser.LanguageID]*parser.Parser{}
	defer closeParsers(parsers)
	cands, err := collectShingles(files, parsers, cfg)
	if err != nil {
		return nil, err
	}
	return FindNear(cands, cfg), nil
}

func closeParsers(parsers map[parser.LanguageID]*parser.Parser) {
	for _, p := range parsers {
		p.Close()
	}
}

// collectShingles parses every file and extracts one candidate per
// function-like declaration, keeping its shingle set.
func collectShingles(files []string, parsers map[parser.LanguageID]*parser.Parser, cfg NearConfig) ([]NearFunc, error) {
	var cands []NearFunc
	for _, path := range files {
		lang, ok := parser.LanguageForFile(filepath.Base(path))
		if !ok {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		tree, err := parseWith(parsers, lang, src)
		if err != nil {
			continue
		}
		cands = append(cands, fileShingles(lang, path, src, tree, cfg)...)
		tree.Close()
	}
	return cands, nil
}

// parseWith returns a parsed tree from the shared per-language parser.
func parseWith(parsers map[parser.LanguageID]*parser.Parser, lang parser.LanguageID, src []byte) (*sitter.Tree, error) {
	pp := parsers[lang]
	if pp == nil {
		fresh, err := parser.New(lang)
		if err != nil {
			return nil, err
		}
		pp, parsers[lang] = fresh, fresh
	}
	return pp.Parse(src)
}

// fileShingles extracts one shingle candidate per declaration in a file.
func fileShingles(lang parser.LanguageID, path string, src []byte, tree *sitter.Tree, cfg NearConfig) []NearFunc {
	var out []NearFunc
	for _, f := range Extract(lang, path, src, tree, Config{MinLines: cfg.MinLines, MinTokens: cfg.MinTokens}) {
		body := findBody(tree.RootNode(), src, f)
		if body == nil {
			continue
		}
		out = append(out, NearFunc{Func: f, Shingles: FingerprintShingles(src, body)})
	}
	return out
}

// findBody locates the body node of a previously extracted declaration
// (Extract returns values, not nodes, so the declaration is re-located by
// name and start line).
func findBody(root *sitter.Node, src []byte, f Func) *sitter.Node {
	var body *sitter.Node
	var find func(n *sitter.Node)
	find = func(n *sitter.Node) {
		if n == nil || body != nil {
			return
		}
		if isDeclKind(n) && int(n.StartPosition().Row)+1 == f.StartLine {
			if name := nodeText(src, n.ChildByFieldName("name")); name == f.Name {
				body = n.ChildByFieldName("body")
				return
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			find(n.Child(i))
		}
	}
	find(root)
	return body
}

// isDeclKind reports whether a node is a function-like declaration.
func isDeclKind(n *sitter.Node) bool {
	switch n.Kind() {
	case "function_declaration", "method_declaration", "constructor_declaration":
		return true
	}
	return false
}
