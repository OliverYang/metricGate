// Package parsetest is a small helper shared by package tests that need a
// parsed tree-sitter tree. It exists because every package's test suite
// used to carry its own copy of the same parse-and-release dance — which
// metricgate's own duplicate-code gate rightly flagged.
//
// Non-test file so sibling packages' _test.go files can import it.
package parsetest

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"metricgate/internal/parser"
)

// Parse parses src with a fresh parser for lang and returns the tree plus
// a release func that closes both the tree and the parser. The caller must
// invoke the release func exactly once (defer it).
func Parse(t *testing.T, lang parser.LanguageID, src string) (*sitter.Tree, func()) {
	t.Helper()
	p, err := parser.New(lang)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := p.Parse([]byte(src))
	if err != nil {
		p.Close()
		t.Fatal(err)
	}
	return tree, func() {
		tree.Close()
		p.Close()
	}
}
