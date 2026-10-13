package clones

import (
	"testing"

	"metricgate/internal/parser"
	"metricgate/internal/parsetest"
)

// shingleOf returns one shingle candidate per extracted declaration, using the
// body node Extract retained (no re-walk: re-locating by name breaks for
// grammars whose declarations are unnamed).
func shingleOf(t *testing.T, lang parser.LanguageID, path, src string) []NearFunc {
	t.Helper()
	tree, release := parsetest.Parse(t, lang, src)
	defer release()
	var out []NearFunc
	for _, f := range Extract(lang, path, []byte(src), tree, Default) {
		if f.body == nil {
			continue
		}
		out = append(out, NearFunc{Func: f, Shingles: FingerprintShingles([]byte(src), f.body)})
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
