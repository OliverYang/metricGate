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
	maxPer := cfg.MaxPerAnchor
	if maxPer <= 0 {
		maxPer = 3
	}
	// Inverted index: shingle -> candidate indexes. Rare shingles only:
	// a shingle shared by nearly everything (e.g. the "return" idiom)
	// carries no signal and would explode the candidate pairs.
	postings := map[uint64][]int{}
	for i, c := range cands {
		if !bigEnough(c.Func, Config{MinLines: cfg.MinLines, MinTokens: cfg.MinTokens}) {
			continue
		}
		for sh := range c.Shingles.Set {
			postings[sh] = append(postings[sh], i)
		}
	}
	rare := func(sh uint64) bool {
		return len(postings[sh]) <= 8
	}

	// Candidate pairs from shared rare shingles.
	type pairKey struct{ a, b int }
	pairs := map[pairKey]bool{}
	for sh, idxs := range postings {
		if !rare(sh) {
			continue
		}
		for a := 0; a < len(idxs); a++ {
			for b := a + 1; b < len(idxs); b++ {
				ia, ib := idxs[a], idxs[b]
				if ia > ib {
					ia, ib = ib, ia
				}
				pairs[pairKey{ia, ib}] = true
			}
		}
	}

	var out []NearMatch
	seenPair := map[pairKey]bool{}
	for pk := range pairs {
		if seenPair[pk] {
			continue
		}
		seenPair[pk] = true
		a, b := cands[pk.a], cands[pk.b]
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
	// Anchor cap + stable ordering.
	sort.SliceStable(out, func(i, j int) bool {
		return lessFunc(out[i].Anchor, out[j].Anchor)
	})
	perAnchor := map[string]int{}
	var kept []NearMatch
	for _, m := range out {
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
	defer func() {
		for _, p := range parsers {
			p.Close()
		}
	}()
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
		pp := parsers[lang]
		if pp == nil {
			fresh, err := parser.New(lang)
			if err != nil {
				continue
			}
			pp, parsers[lang] = fresh, fresh
		}
		tree, err := pp.Parse(src)
		if err != nil {
			continue
		}
		for _, f := range Extract(lang, path, src, tree, Config{MinLines: cfg.MinLines, MinTokens: cfg.MinTokens}) {
			// Locate this declaration's body again (Extract returns no node).
			var body *sitter.Node
			var find func(n *sitter.Node)
			find = func(n *sitter.Node) {
				if n == nil || body != nil {
					return
				}
				if isDeclKind(n) {
					if name := nodeText(src, n.ChildByFieldName("name")); name == f.Name &&
						int(n.StartPosition().Row)+1 == f.StartLine {
						body = n.ChildByFieldName("body")
					}
				}
				for i := uint(0); i < n.ChildCount(); i++ {
					find(n.Child(i))
				}
			}
			find(tree.RootNode())
			if body != nil {
				cands = append(cands, NearFunc{Func: f, Shingles: FingerprintShingles(src, body)})
			}
		}
		tree.Close()
	}
	return FindNear(cands, cfg), nil
}

// isDeclKind reports whether a node is a function-like declaration.
func isDeclKind(n *sitter.Node) bool {
	switch n.Kind() {
	case "function_declaration", "method_declaration", "constructor_declaration":
		return true
	}
	return false
}
