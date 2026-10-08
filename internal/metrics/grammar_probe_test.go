package metrics

import (
	"fmt"
	"os"
	"strings"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/parser"
	"metricgate/internal/parsetest"
)

// The adapters are written against a grammar's node kinds and field names, and
// those names are the one thing a tree-sitter upgrade can change silently. This
// test dumps the real CST of a dense sample per language so a suspected
// mismatch can be checked instead of argued about.
//
// It is off by default — the output is a few hundred lines, and nothing here
// asserts. To look: MG_GRAMMAR_PROBE=1 go test ./internal/metrics -run TestGrammarProbe -v

var grammarProbeSrc = map[parser.LanguageID]string{
	parser.LangGo: `package x

func A(a, b int, f func() int) int {
	if a > 0 && b > 0 && a != b {
		switch a {
		case 1, 2:
			for i := 0; i < a; i++ {
				continue
			}
		default:
			f()
		}
	} else if a < 0 {
	} else {
	}
	switch y := any(a).(type) {
	case int:
		_ = y
	default:
	}
	select {
	case <-ch:
		x := 1
	default:
	}
Loop:
	for {
		break Loop
	}
	for i := range xs {
		_ = i
	}
	g := func() int { if a > b { return 1 }; return 0 }
	return g() + A(a, b, f)
}
`,
	parser.LangJava: `class A {
    int chain(int a, int b) {
        if (a > 0 && b > 0) {
            switch (a) {
                case 1:
                case 2: return 1;
                default: break;
            }
            int x = a > b ? 1 : 2;
            try { foo(); } catch (E1 e) { bar(); } finally { z(); }
        } else if (a < 0) {
            a++;
        } else {
            for (String s : list) { }
        }
        outer: for (int i = 0; i < 3; i++) { switch (a) { case 1 -> { chain(a, b); } default -> { } } }
        return chain(a, b);
    }
    A() { this(1, 2); }
}
`,
}

func TestGrammarProbe(t *testing.T) {
	if os.Getenv("MG_GRAMMAR_PROBE") == "" {
		t.Skip("grammar probe is opt-in; see the header comment")
	}
	for _, lang := range []parser.LanguageID{parser.LangGo, parser.LangJava} {
		t.Run(string(lang), func(t *testing.T) {
			tree, release := parsetest.Parse(t, lang, grammarProbeSrc[lang])
			defer release()
			b := &strings.Builder{}
			dumpCST(tree.RootNode(), 0, "", b)
			t.Log("\n" + b.String())
		})
	}
}

// dumpCST prints every node with its grammar field name, which is what the
// adapters key off — named vs anonymous and error nodes are marked, since a
// field name only exists on the child links the grammar declares.
func dumpCST(n *sitter.Node, depth int, field string, b *strings.Builder) {
	if n == nil {
		return
	}
	label := n.Kind()
	if field != "" {
		label = field + "=" + label
	}
	if !n.IsNamed() {
		label += " (anon)"
	}
	if n.IsError() {
		label += " !ERR"
	}
	fmt.Fprintf(b, "%s%s\n", strings.Repeat("  ", depth), label)
	for i := uint(0); i < n.ChildCount(); i++ {
		dumpCST(n.Child(i), depth+1, n.FieldNameForChild(uint32(i)), b)
	}
}
