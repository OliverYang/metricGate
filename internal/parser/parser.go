// Package parser wraps tree-sitter for the languages metricgate supports.
// Each Parser instance owns its underlying C parser and must be used by a
// single goroutine; create one per worker.
package parser

import (
	"errors"
	"fmt"
	"path/filepath"
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
)

// LanguageID names a supported language.
type LanguageID string

const (
	LangGo   LanguageID = "go"
	LangJava LanguageID = "java"
)

// grammars is the language registry: adding a language is one entry here
// plus a rule pack and (if needed) a metrics profile.
var grammars = map[LanguageID]func() unsafe.Pointer{
	LangGo:   tree_sitter_go.Language,
	LangJava: tree_sitter_java.Language,
}

// Supported lists the registered languages in stable order.
func Supported() []LanguageID {
	return []LanguageID{LangGo, LangJava}
}

// Parser parses source files into tree-sitter trees for one language.
type Parser struct {
	ID   LanguageID
	Lang *sitter.Language

	p *sitter.Parser
}

// New creates a parser for the given language.
func New(id LanguageID) (*Parser, error) {
	grammar, ok := grammars[id]
	if !ok {
		return nil, fmt.Errorf("unsupported language %q", id)
	}
	lang := sitter.NewLanguage(grammar())
	p := sitter.NewParser()
	if err := p.SetLanguage(lang); err != nil {
		p.Close()
		return nil, fmt.Errorf("set language %s: %w", id, err)
	}
	return &Parser{ID: id, Lang: lang, p: p}, nil
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
	case ".java":
		return LangJava, true
	default:
		return "", false
	}
}

// Language exports for metrics package.
var (
	// TsGo is the tree-sitter-go language instance, exported for metrics profile use.
	TsGo *sitter.Language
	// TsJava is the tree-sitter-java language instance, exported for metrics profile use.
	TsJava *sitter.Language
)

func init() {
	TsGo = sitter.NewLanguage(tree_sitter_go.Language())
	TsJava = sitter.NewLanguage(tree_sitter_java.Language())
}
