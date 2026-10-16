package clones

import (
	"testing"

	"metricgate/internal/parser"
)

// smallCfg relaxes the size gates for synthetic fixtures that would never
// reach the real-world Default thresholds.
func smallCfg() Config {
	cfg := Default
	cfg.MinLines = 5
	cfg.MinTokens = 20
	return cfg
}

// extract parses src and returns its extracted functions.
func extract(t *testing.T, lang parser.LanguageID, path, src string, cfg Config) []Func {
	t.Helper()
	pp, err := parser.New(lang)
	if err != nil {
		t.Fatal(err)
	}
	defer pp.Close()
	tree, err := pp.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	return Extract(lang, path, []byte(src), tree, cfg)
}

const goBody = `
package a

// shortOne is structurally identical to alpha but below the default line
// threshold, so it must never join a clone class.
func shortOne(xs []int) int {
	s := 0
	for _, x := range xs {
		s += x
	}
	return s
}

// alpha is the original: a renamed but structurally identical twin of beta.
func alpha(ys []int) int {
	total := 0
	for _, y := range ys {
		total += y
	}
	total = total * 2
	total = total - 3
	total = total * 4
	total = total - 5
	total = total * 6
	total = total - 7
	total = total * 8
	total = total - 9
	total = total * 10
	total = total - 11
	return total
}

// beta is a Type-2 clone of alpha (renamed identifiers, same shape).
func beta(ns []int) int {
	sum := 0
	for _, n := range ns {
		sum += n
	}
	sum = sum * 2
	sum = sum - 3
	sum = sum * 4
	sum = sum - 5
	sum = sum * 6
	sum = sum - 7
	sum = sum * 8
	sum = sum - 9
	sum = sum * 10
	sum = sum - 11
	return sum
}

// different does something else (max instead of sum) and must not join.
func different(xs []int) int {
	best := 0
	for _, x := range xs {
		if x > best {
			best = x
		}
	}
	best = best * 2
	best = best - 3
	best = best * 4
	best = best - 5
	best = best * 6
	best = best - 7
	best = best * 8
	best = best - 9
	best = best * 10
	best = best - 11
	return best
}
`

func TestGoFindType2Clones(t *testing.T) {
	fs := extract(t, parser.LangGo, "a.go", goBody, Default)
	if len(fs) != 4 {
		t.Fatalf("extracted %d functions, want 4 (top-level declarations only)", len(fs))
	}
	groups := Find(fs, Default)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	g := groups[0]
	names := map[string]bool{}
	for _, f := range g.Funcs {
		names[f.Name] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("clone class = %v, want {alpha beta}", names)
	}
	if names["different"] || names["shortOne"] {
		t.Errorf("unrelated functions joined the clone class: %v", names)
	}
	// Members are sorted by file then line: alpha comes first.
	if g.Funcs[0].Name != "alpha" {
		t.Errorf("anchor = %s, want alpha", g.Funcs[0].Name)
	}
}

func TestOperatorsStayExact(t *testing.T) {
	src := `
package a

func first(xs []int, limit int) int {
	acc := 0
	for _, x := range xs {
		if x > limit {
			acc += x
		}
		acc = acc * 2
		acc = acc - 3
		acc = acc * 4
	}
	return acc
}

func second(ys []int, limit int) int {
	acc := 0
	for _, y := range ys {
		if y < limit {
			acc += y
		}
		acc = acc * 2
		acc = acc - 3
		acc = acc * 4
	}
	return acc
}
`
	fs := extract(t, parser.LangGo, "a.go", src, smallCfg())
	if groups := Find(fs, smallCfg()); len(groups) != 0 {
		t.Errorf("a>b vs a<b must not be clones, got %d groups", len(groups))
	}
}

func TestLiteralsDoNotHideClones(t *testing.T) {
	src := `
package a

func copyFile(srcPath string, dstPath string) error {
	buf := make([]byte, 4096)
	for {
		if len(buf) == 0 {
			break
		}
		buf = buf[:0]
		buf = buf[:0]
		buf = buf[:0]
		if len(buf) < 4096 {
			return closePair(srcPath, dstPath)
		}
	}
	return closePair(dstPath, srcPath)
}

func copyDir(srcPath string, dstPath string) error {
	buf := make([]byte, 8192)
	for {
		if len(buf) == 0 {
			break
		}
		buf = buf[:0]
		buf = buf[:0]
		buf = buf[:0]
		if len(buf) < 8192 {
			return closePair(srcPath, dstPath)
		}
	}
	return closePair(dstPath, srcPath)
}

func closePair(a string, b string) error { return nil }
`
	fs := extract(t, parser.LangGo, "a.go", src, Default)
	if groups := Find(fs, Default); len(groups) != 1 {
		t.Errorf("literals are normalised: 4096 vs 8192 copies should match, got %d groups", len(groups))
	}
}

func TestZeroConfigDisables(t *testing.T) {
	fs := extract(t, parser.LangGo, "a.go", goBody, Config{})
	if fs != nil {
		t.Errorf("zero config must extract nothing, got %d funcs", len(fs))
	}
	if groups := Find(nil, Config{}); groups != nil {
		t.Error("zero config must group nothing")
	}
}

func TestMinLines(t *testing.T) {
	src := `
package a

func wideOne(x int) int { y := x + 1; z := y * 2; w := z - 3; v := w * 4; u := v - 5; return u / 6 }

func wideTwo(x int) int { y := x + 2; z := y * 3; w := z - 4; v := w * 5; u := v - 6; return u / 7 }
`
	cfg := Default
	cfg.MinLines = 3 // both one-liners are shorter: no groups
	fs := extract(t, parser.LangGo, "a.go", src, cfg)
	if groups := Find(fs, cfg); len(groups) != 0 {
		t.Errorf("MinLines must filter short functions, got %d groups", len(groups))
	}
}

func TestJavaClones(t *testing.T) {
	src := `
package a;

public class Sample {
    int first(int[] xs) {
        int acc = 0;
        for (int x : xs) {
            acc += x;
        }
        return acc;
    }

    int second(int[] ys) {
        int total = 0;
        for (int y : ys) {
            total += y;
        }
        return total;
    }
}
`
	fs := extract(t, parser.LangJava, "Sample.java", src, Default)
	if len(fs) != 2 {
		t.Fatalf("extracted %d java methods, want 2", len(fs))
	}
	cfg := smallCfg()
	cfg.MinLines = 5 // the synthetic methods are 7 physical lines
	if groups := Find(fs, cfg); len(groups) != 1 {
		t.Errorf("java clone class missing, got %d groups", len(groups))
	}
}

func TestCrossFileGroupingAndOrder(t *testing.T) {
	// Two files, each carrying one member of the same clone family.
	src1 := "package a\n\nfunc one(xs []int) int {\n\ts := 0\n\tfor _, x := range xs {\n\t\ts += x\n\t}\n\ts = s * 2\n\ts = s - 3\n\ts = s * 4\n\ts = s - 5\n\ts = s * 6\n\ts = s - 7\n\ts = s * 8\n\treturn s\n}\n"
	src2 := "package b\n\nfunc two(ys []int) int {\n\tt := 0\n\tfor _, y := range ys {\n\t\tt += y\n\t}\n\tt = t * 2\n\tt = t - 3\n\tt = t * 4\n\tt = t - 5\n\tt = t * 6\n\tt = t - 7\n\tt = t * 8\n\treturn t\n}\n"
	fs := append(
		extract(t, parser.LangGo, "b.go", src2, Default),
		extract(t, parser.LangGo, "a.go", src1, Default)...,
	)
	cfg := Default
	cfg.MinTokens = 20 // the synthetic bodies are small
	groups := Find(fs, cfg)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	g := groups[0]
	if g.Funcs[0].File != "a.go" {
		t.Errorf("anchor = %s, want a.go (sorted by file)", g.Funcs[0].File)
	}
	if len(g.Funcs) != 2 {
		t.Errorf("members = %d, want 2", len(g.Funcs))
	}
}

func TestSummaryLines(t *testing.T) {
	g := []Group{{
		Hash: 1,
		Funcs: []Func{
			{File: "a.go", Name: "alpha", StartLine: 10},
			{File: "b.go", Name: "beta", StartLine: 20},
		},
	}}
	lines := Summary(g)
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	want := "clone of a.go:10 (alpha) also at b.go:20 — reuse it, do not reimplement"
	if lines[0] != want {
		t.Errorf("line = %q, want %q", lines[0], want)
	}
}

func TestFingerprintIgnoresCommentsAndLayout(t *testing.T) {
	same := []string{
		`
package a
func long(xs []int) int {
	// a comment that must not change the fingerprint
	total := 0
	for _, x := range xs { /* inline note */
		total += x
	}
	total = total * 2
	total = total - 3
	total = total * 4
	total = total - 5
	total = total * 6
	total = total - 7
	total = total * 8
	total = total - 9
	return total
}`,
		`
package a
func long2(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	total = total * 2
	total = total - 3
	total = total * 4
	total = total - 5
	total = total * 6
	total = total - 7
	total = total * 8
	total = total - 9
	// trailing comment, different whitespace above
	return total
}`,
	}
	fs1 := extract(t, parser.LangGo, "a.go", same[0], Default)
	fs2 := extract(t, parser.LangGo, "b.go", same[1], Default)
	cfg := Default
	cfg.MinTokens = 20
	groups := Find(append(fs1, fs2...), cfg)
	if len(groups) != 1 {
		t.Errorf("comments and layout must not affect the fingerprint, got %d groups", len(groups))
	}
}

// sqlTwin is one member of a Type-2 pair: renamed, same shape. The bodies are
// written in the `CASE` form the SQL grammar parses without error (the
// PL/pgSQL `IF` and `PERFORM` forms do not survive it).
const sqlTwinA = `CREATE FUNCTION alpha(score int) RETURNS text AS $$
  SELECT CASE
    WHEN score >= 90 THEN 'a'
    WHEN score >= 80 THEN 'b'
    WHEN score >= 70 THEN 'c'
    WHEN score >= 60 THEN 'd'
    ELSE 'f'
  END;
$$ LANGUAGE sql;
`

const sqlTwinB = `CREATE FUNCTION beta(grade int) RETURNS text AS $$
  SELECT CASE
    WHEN grade >= 90 THEN 'x'
    WHEN grade >= 80 THEN 'y'
    WHEN grade >= 70 THEN 'z'
    WHEN grade >= 60 THEN 'w'
    ELSE 'v'
  END;
$$ LANGUAGE sql;
`

// TestSQLClones covers the language whose routine name lives in an
// object_reference child and whose body lives in a `function_body` child: both
// are outside any field, so a naive reader finds no name and no body at all.
func TestSQLClones(t *testing.T) {
	fs := extract(t, parser.LangSQL, "f.sql", sqlTwinA+sqlTwinB, smallCfg())
	if len(fs) != 2 {
		t.Fatalf("extracted %d sql routines, want 2", len(fs))
	}
	if fs[0].Name != "alpha" || fs[1].Name != "beta" {
		t.Fatalf("names = %q, %q; want alpha, beta", fs[0].Name, fs[1].Name)
	}
	if groups := Find(fs, smallCfg()); len(groups) != 1 {
		t.Errorf("sql clone class missing, got %d groups", len(groups))
	}
}

const verilogTwins = `module m;
  function automatic int alpha(int a, int b);
    int t;
    t = a + b;
    t = t * 2;
    t = t - 3;
    return t;
  endfunction

  function automatic int beta(int x, int y);
    int s;
    s = x + y;
    s = s * 2;
    s = s - 3;
    return s;
  endfunction
endmodule
`

// TestVerilogClones covers the grammar with no fields on its declarations at
// all: the name is behind a doubly-nested `function_identifier` and the body
// sits under `function_body_declaration`, which also holds the header.
func TestVerilogClones(t *testing.T) {
	fs := extract(t, parser.LangVerilog, "m.v", verilogTwins, smallCfg())
	if len(fs) != 2 {
		t.Fatalf("extracted %d verilog routines, want 2", len(fs))
	}
	if fs[0].Name != "alpha" || fs[1].Name != "beta" {
		t.Fatalf("names = %q, %q; want alpha, beta", fs[0].Name, fs[1].Name)
	}
	if groups := Find(fs, smallCfg()); len(groups) != 1 {
		t.Errorf("verilog clone class missing, got %d groups", len(groups))
	}
}

const vbTwins = `Module M
    Function Alpha(a As Integer, b As Integer) As Integer
        Dim t As Integer
        t = a + b
        t = t * 2
        t = t - 3
        t = t * 4
        t = t - 5
        Return t
    End Function

    Function Beta(x As Integer, y As Integer) As Integer
        Dim s As Integer
        s = x + y
        s = s * 2
        s = s - 3
        s = s * 4
        s = s - 5
        Return s
    End Function
End Module
`

// TestVBClones covers the grammar with no body node: a VB method is its header
// followed by a flat list of `statement` siblings, so the fingerprint has to
// cover every one of them.
func TestVBClones(t *testing.T) {
	fs := extract(t, parser.LangVB, "M.vb", vbTwins, smallCfg())
	if len(fs) != 2 {
		t.Fatalf("extracted %d vb methods, want 2", len(fs))
	}
	if fs[0].Name != "Alpha" || fs[1].Name != "Beta" {
		t.Fatalf("names = %q, %q; want Alpha, Beta", fs[0].Name, fs[1].Name)
	}
	if groups := Find(fs, smallCfg()); len(groups) != 1 {
		t.Errorf("vb clone class missing, got %d groups", len(groups))
	}
}

// TestVBFlatBodyCoversEveryStatement is the guard for the flat-statement-list
// bug: two methods that share only their opening statements must not be
// reported as clones. Hashing just the first statement would give them the
// same fingerprint.
//
// The assertion is on the fingerprints themselves, not on Find: a truncated
// body is also short enough that Find's size gate would drop both members and
// report "no clones" for the wrong reason.
func TestVBFlatBodyCoversEveryStatement(t *testing.T) {
	src := `Module M
    Function Alpha(a As Integer) As Integer
        Dim t As Integer
        t = a + 1
        t = t * 2
        t = t - 3
        t = t * 4
        Return t
    End Function

    Function Beta(a As Integer) As Integer
        Dim t As Integer
        t = a + 1
        t = t / 2
        t = t + 30
        t = t / 4
        Return t
    End Function
End Module
`
	fs := extract(t, parser.LangVB, "M.vb", src, Config{MinLines: 1, MinTokens: 1})
	if len(fs) != 2 {
		t.Fatalf("extracted %d vb methods, want 2", len(fs))
	}
	if fs[0].Hash == fs[1].Hash {
		t.Errorf("methods differing after their opening line share fingerprint %d; the body is being truncated to its first statement", fs[0].Hash)
	}
	if fs[0].Tokens != fs[1].Tokens {
		t.Errorf("twin token counts differ: %d vs %d", fs[0].Tokens, fs[1].Tokens)
	}
}
