package clones

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/parser"
	"metricgate/internal/parsetest"
)

func shingleOf(t *testing.T, lang parser.LanguageID, path, src string) []NearFunc {
	t.Helper()
	tree, release := parsetest.Parse(t, lang, src)
	defer release()
	fns := Extract(lang, path, []byte(src), tree, Default)
	var out []NearFunc
	for _, f := range fns {
		var body *sitter.Node
		// Re-walk to find the body node (Extract does not retain it).
		var find func(n *sitter.Node)
		find = func(n *sitter.Node) {
			if n == nil || body != nil {
				return
			}
			if n.Kind() == "function_declaration" || n.Kind() == "method_declaration" {
				name := ""
				if nf := n.ChildByFieldName("name"); nf != nil {
					name = nodeText([]byte(src), nf)
				}
				if name == f.Name {
					body = n.ChildByFieldName("body")
				}
			}
			for i := uint(0); i < n.ChildCount(); i++ {
				find(n.Child(i))
			}
		}
		find(tree.RootNode())
		if body != nil {
			out = append(out, NearFunc{Func: f, Shingles: FingerprintShingles([]byte(src), body)})
		}
	}
	return out
}

const nearSrc1 = `package a

func SumSquares(limit int) int {
	total := 0
	for i := 0; i < limit; i++ {
		if i%3 == 0 {
			total += i * i
		} else if i%7 == 0 {
			total -= i
		} else {
			total += i
		}
	}
	if total > 1000 {
		total = 1000
	}
	return total
}

func SumCubes(limit int) int {
	acc := 0
	for i := 0; i < limit; i++ {
		if i%3 == 0 {
			acc += i * i * i
		} else if i%7 == 0 {
			acc -= i
		} else {
			acc += i
		}
	}
	if acc > 5000 {
		acc = 5000
	}
	return acc
}

func Unrelated(limit int) int {
	best := 0
	for i := 0; i < limit; i++ {
		if i > best {
			best = i * i * i * i
		}
	}
	return best
}
`

func TestFindNearCopyWithSmallEdit(t *testing.T) {
	cands := shingleOf(t, parser.LangGo, "a.go", nearSrc1)
	if len(cands) != 3 {
		t.Fatalf("candidates = %d, want 3", len(cands))
	}
	matches := FindNear(cands, DefaultNear)
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1 (SumSquares vs SumCubes): %+v", len(matches), matches)
	}
	m := matches[0]
	if m.Similarity < DefaultNear.Threshold {
		t.Errorf("similarity %.2f below threshold", m.Similarity)
	}
	// Anchor is the first-defined function.
	if m.Anchor.Name != "SumSquares" || m.Other.Name != "SumCubes" {
		t.Errorf("pair = %s/%s, want SumSquares/SumCubes", m.Anchor.Name, m.Other.Name)
	}
}

func TestFindNearNoMatchForDifferentShapes(t *testing.T) {
	cands := shingleOf(t, parser.LangGo, "a.go", nearSrc1)
	// Only Unrelated: no pair possible.
	var single []NearFunc
	for _, c := range cands {
		if c.Name == "Unrelated" {
			single = append(single, c)
		}
	}
	if matches := FindNear(single, DefaultNear); len(matches) != 0 {
		t.Errorf("unrelated matched: %+v", matches)
	}
}

func TestNearConfigZeroDisables(t *testing.T) {
	cands := shingleOf(t, parser.LangGo, "a.go", nearSrc1)
	cfg := DefaultNear
	cfg.Threshold = 0 // invalid/off
	if matches := FindNear(cands, cfg); len(matches) != 0 {
		t.Errorf("threshold 0 must disable, got %+v", matches)
	}
}
