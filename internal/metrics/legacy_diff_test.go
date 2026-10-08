package metrics

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/parser"
)

// The IR engine took over scoring from a direct tree-sitter walk. This file
// keeps that retired walk as the reference, so the migration can be proven
// score-for-score identical on real code rather than only on hand-written
// cases.
//
// A failure here means the IR path moved a number: the adapters dropped a
// construct or the engine rescored one. Either way that is a policy change
// needing a deliberate decision, not a test edit.
//
//	go test ./internal/metrics -run TestIRMatchesLegacy

// legacyKinds is the decision-point table the pre-IR walk scored from.
type legacyKinds struct {
	branch   []string
	switches []string
	cases    []string
	flat     []string
	lambda   []string
	ternary  string
}

var (
	legacyGo = legacyKinds{
		branch:   []string{"if_statement", "for_statement"},
		switches: []string{"expression_switch_statement", "type_switch_statement", "select_statement"},
		cases:    []string{"expression_case", "type_case", "communication_case"},
		lambda:   []string{"func_literal"},
	}
	legacyJava = legacyKinds{
		branch:   []string{"if_statement", "for_statement", "enhanced_for_statement", "while_statement", "do_statement"},
		switches: []string{"switch_expression"},
		cases:    []string{"switch_label"},
		flat:     []string{"catch_clause"},
		lambda:   []string{"lambda_expression"},
		ternary:  "ternary_expression",
	}
)

// legacySpec pairs a language with everything the retired walk needed.
type legacySpec struct {
	lang      parser.LanguageID
	ext       string
	funcKinds []string
	kinds     legacyKinds
}

var legacySpecs = []legacySpec{
	{parser.LangGo, ".go", []string{"function_declaration", "method_declaration"}, legacyGo},
	{parser.LangJava, ".java", []string{"method_declaration", "constructor_declaration"}, legacyJava},
}

func legacyLogicOp(src []byte, n *sitter.Node) string {
	if op := n.ChildByFieldName("operator"); op != nil {
		if t := text(src, op); t == "&&" || t == "||" {
			return t
		}
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		if c := n.Child(i); !c.IsNamed() {
			if t := text(src, c); t == "&&" || t == "||" {
				return t
			}
		}
	}
	return ""
}

func legacyCyclomatic(k legacyKinds, body *sitter.Node, src []byte) int {
	cc := 1
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		isDefaultLabel := slices.Contains(k.cases, n.Kind()) && hasToken(src, n, "default")
		switch {
		case isDefaultLabel:
			// `default:` opens no new path.
		case slices.Contains(k.branch, n.Kind()), slices.Contains(k.cases, n.Kind()), k.ternary == n.Kind():
			cc++
		case n.Kind() == "binary_expression":
			if legacyLogicOp(src, n) != "" {
				cc++
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(body)
	return cc
}

func legacyCognitive(k legacyKinds, body *sitter.Node, src []byte) (cog, maxNest int) {
	var walk func(n *sitter.Node, nesting int)
	walk = func(n *sitter.Node, nesting int) {
		if n == nil {
			return
		}
		inner := nesting
		switch {
		case slices.Contains(k.branch, n.Kind()), slices.Contains(k.switches, n.Kind()):
			cog += 1 + nesting
			inner = nesting + 1
		case slices.Contains(k.flat, n.Kind()):
			cog++
		case slices.Contains(k.lambda, n.Kind()):
			inner = nesting + 1
		case n.Kind() == "binary_expression":
			if op := legacyLogicOp(src, n); op != "" {
				parent := n.Parent()
				if parent == nil || parent.Kind() != "binary_expression" || legacyLogicOp(src, parent) != op {
					cog++ // start of a run of the same logical operator
				}
			}
		}
		if nesting > maxNest {
			maxNest = nesting
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i), inner)
		}
	}
	walk(body, 0)
	return cog, maxNest
}

// legacyDiff is one function whose numbers disagree.
type legacyDiff struct {
	where string // file:line
	want  string // "cc/cog/nest" from the retired walk
	got   string
}

func (d legacyDiff) String() string { return d.where + " want " + d.want + ", got " + d.got }

// goRoot locates the Go source tree to widen the corpus, or "" when it cannot
// be found. It asks the toolchain rather than reading runtime.GOROOT, which is
// deprecated: that value is baked in at build time and stops meaning anything
// once the test binary is copied elsewhere.
func goRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Logf("go env GOROOT unavailable, scoring this repository only: %v", err)
		return ""
	}
	return strings.TrimSpace(string(out))
}

// TestIRMatchesLegacy scores real files twice — once with the retired walk,
// once through the IR engine — and fails on any disagreement. The corpus is
// this repository plus a thinned slice of the Go standard library, so the
// Java half leans on testdata/java: keep adding constructs there when the
// lowerer grows.
func TestIRMatchesLegacy(t *testing.T) {
	roots := []string{filepath.Join("..", "..")}
	if gr := goRoot(t); gr != "" {
		roots = append(roots, filepath.Join(gr, "src"))
	}

	for _, spec := range legacySpecs {
		p, err := parser.New(spec.lang)
		if err != nil {
			t.Fatal(err)
		}
		var files, funcs int
		var diffs []legacyDiff

		for _, root := range roots {
			for _, path := range legacyCorpus(root, spec.ext) {
				src, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				tree, err := p.Parse(src)
				if err != nil || tree == nil {
					continue
				}
				files++
				d := legacyCompare(spec, path, src, tree.RootNode(), &funcs)
				tree.Close()
				diffs = append(diffs, d...)
			}
		}

		for _, d := range diffs[:min(len(diffs), 10)] {
			t.Error(d.String())
		}
		if len(diffs) > 0 {
			t.Errorf("%s: %d of %d functions in %d files disagree with the legacy walk",
				spec.lang, len(diffs), funcs, files)
		} else {
			t.Logf("%s: %d functions in %d files, no differences", spec.lang, funcs, files)
		}
	}
}

func legacyCompare(spec legacySpec, path string, src []byte, root *sitter.Node, funcs *int) []legacyDiff {
	var out []legacyDiff
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if slices.Contains(spec.funcKinds, n.Kind()) {
			if body := n.ChildByFieldName("body"); body != nil {
				*funcs++
				wCC := legacyCyclomatic(spec.kinds, body, src)
				wCog, wNest := legacyCognitive(spec.kinds, body, src)
				gCC, gCog, gNest := irScore(spec.lang, src, n)
				if wCC != gCC || wCog != gCog || wNest != gNest {
					out = append(out, legacyDiff{
						where: path + ":" + strconv.Itoa(int(n.StartPosition().Row)+1),
						want:  score(wCC, wCog, wNest),
						got:   score(gCC, gCog, gNest),
					})
				}
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return out
}

func score(cc, cog, nest int) string {
	return strconv.Itoa(cc) + "/" + strconv.Itoa(cog) + "/" + strconv.Itoa(nest)
}

// legacyCorpus lists source files under root, capped and thinned so the stdlib
// pass stays a few seconds instead of a few minutes.
func legacyCorpus(root, ext string) []string {
	var found []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if slices.Contains([]string{".git", "vendor"}, filepath.Base(path)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ext) {
			return nil
		}
		found = append(found, path)
		return nil
	})

	const cap = 400
	if len(found) <= cap {
		return found
	}
	stride := len(found) / cap
	thinned := make([]string, 0, cap)
	for i := 0; i < len(found) && len(thinned) < cap; i += stride {
		thinned = append(thinned, found[i])
	}
	return thinned
}
