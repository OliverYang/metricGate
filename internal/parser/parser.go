// Package parser wraps tree-sitter for the languages metricgate supports.
// Each Parser instance owns its underlying C parser and must be used by a
// single goroutine; create one per worker.
package parser

import (
	"errors"
	"fmt"
	"path/filepath"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
)

// LanguageID names a supported language.
type LanguageID string

// LangGo is the only language in v0; the registry below is where new
// grammars plug in for v1+.
const LangGo LanguageID = "go"

// Parser parses source files into tree-sitter trees for one language.
type Parser struct {
	ID   LanguageID
	Lang *sitter.Language

	p *sitter.Parser
}

// New creates a parser for the given language.
func New(id LanguageID) (*Parser, error) {
	switch id {
	case LangGo:
		lang := sitter.NewLanguage(tree_sitter_go.Language())
		p := sitter.NewParser()
		if err := p.SetLanguage(lang); err != nil {
			p.Close()
			return nil, fmt.Errorf("set language %s: %w", id, err)
		}
		return &Parser{ID: id, Lang: lang, p: p}, nil
	default:
		return nil, fmt.Errorf("unsupported language %q", id)
	}
}

// Parse parses source bytes into a tree. The caller must Close the tree
// after finishing with all nodes derived from it.
func (pp *Parser) Parse(src []byte) (*sitter.Tree, error) {
	t := pp.p.Parse(src, nil)
	if t == nil {
		return nil, errors.New("tree-sitter returned no tree")
	}
	return t, nil
}

// Close releases the underlying C parser.
func (pp *Parser) Close() { pp.p.Close() }

// LanguageForFile maps a file name to its language. ok is false for
// extensions metricgate does not handle yet.
func LanguageForFile(name string) (LanguageID, bool) {
	switch filepath.Ext(name) {
	case ".go":
		return LangGo, true
	default:
		return "", false
	}
}
