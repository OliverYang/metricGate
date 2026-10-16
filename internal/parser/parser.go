// Package parser wraps tree-sitter for the languages metricgate supports.
// Each Parser instance owns its underlying C parser and must be used by a
// single goroutine; create one per worker.
package parser

import (
	"errors"
	"fmt"
	"path/filepath"
	"unsafe"

	tree_sitter_sql "github.com/DerekStride/tree-sitter-sql/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_csharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	tree_sitter_c "github.com/tree-sitter/tree-sitter-c/bindings/go"
	tree_sitter_cpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tree_sitter_vb "github.com/tree-sitter/tree-sitter-tree_sitter_vb_dotnet/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
	tree_sitter_verilog "github.com/tree-sitter/tree-sitter-verilog/bindings/go"
)

// LanguageID names a supported language.
type LanguageID string

const (
	LangGo      LanguageID = "go"
	LangJava    LanguageID = "java"
	LangTS      LanguageID = "typescript"
	LangTSX     LanguageID = "tsx"
	LangJS      LanguageID = "javascript"
	LangPy      LanguageID = "python"
	LangC       LanguageID = "c"
	LangCpp     LanguageID = "cpp"
	LangCS      LanguageID = "csharp"
	LangRust    LanguageID = "rust"
	LangSQL     LanguageID = "sql"
	LangVerilog LanguageID = "verilog"
	LangVB      LanguageID = "vbnet"
)

// grammars is the language registry: adding a language is one entry here
// plus a rule pack and (if needed) a metrics profile.
var grammars = map[LanguageID]func() unsafe.Pointer{
	LangGo:      tree_sitter_go.Language,
	LangJava:    tree_sitter_java.Language,
	LangTS:      tree_sitter_typescript.LanguageTypescript,
	LangTSX:     tree_sitter_typescript.LanguageTSX,
	LangJS:      tree_sitter_javascript.Language,
	LangPy:      tree_sitter_python.Language,
	LangC:       tree_sitter_c.Language,
	LangCpp:     tree_sitter_cpp.Language,
	LangCS:      tree_sitter_csharp.Language,
	LangRust:    tree_sitter_rust.Language,
	LangSQL:     tree_sitter_sql.Language,
	LangVerilog: tree_sitter_verilog.Language,
	LangVB:      tree_sitter_vb.Language,
}

// Supported lists the registered languages in stable order.
func Supported() []LanguageID {
	return []LanguageID{LangGo, LangJava, LangTS, LangTSX, LangJS, LangPy, LangC, LangCpp, LangCS, LangRust, LangSQL, LangVerilog, LangVB}
}

// extensions is the canonical extension per language, the inverse of
// LanguageForFile for the cases that need to name a file rather than read
// one (fixture staging, generated examples). A language is written with one
// spelling here even when it can be read from several: ".ts" not ".mts",
// because this direction produces a file a human will look at.
var extensions = map[LanguageID]string{
	LangGo:      ".go",
	LangJava:    ".java",
	LangTS:      ".ts",
	LangTSX:     ".tsx",
	LangJS:      ".js",
	LangPy:      ".py",
	LangC:       ".c",
	LangCpp:     ".cpp",
	LangCS:      ".cs",
	LangRust:    ".rs",
	LangSQL:     ".sql",
	LangVerilog: ".v",
	LangVB:      ".vb",
}

// Extension returns the canonical file extension of a language, or "" when
// the language is not registered.
func Extension(id LanguageID) string {
	return extensions[id]
}

// IsSupported reports whether a language is registered.
func IsSupported(id LanguageID) bool {
	_, ok := grammars[id]
	return ok
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
	case ".ts", ".mts", ".cts":
		return LangTS, true
	case ".tsx":
		return LangTSX, true
	case ".js", ".mjs", ".cjs", ".jsx":
		return LangJS, true
	case ".py":
		return LangPy, true
	case ".c", ".h":
		return LangC, true
	case ".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx", ".cc.in":
		return LangCpp, true
	case ".cs":
		return LangCS, true
	case ".rs":
		return LangRust, true
	case ".sql":
		return LangSQL, true
	case ".v", ".sv", ".svh", ".vh":
		return LangVerilog, true
	case ".vb":
		return LangVB, true
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
	// TsTypescript is the tree-sitter-typescript language instance, exported for metrics profile use.
	TsTypescript *sitter.Language
	// TsTsx is the tree-sitter tsx grammar (TypeScript plus JSX).
	TsTsx *sitter.Language
	// TsJavascript is the tree-sitter-javascript language instance, exported for metrics profile use.
	TsJavascript *sitter.Language
	// TsPython is the tree-sitter-python language instance, exported for metrics profile use.
	TsPython *sitter.Language
	// TsC, TsCpp, TsCSharp, TsRust are instances for the newer grammars.
	TsC      *sitter.Language
	TsCpp    *sitter.Language
	TsCSharp *sitter.Language
	TsRust   *sitter.Language
	// TsSQL, TsVerilog, TsVB are instances for the SQL, Verilog and VB.NET grammars.
	TsSQL     *sitter.Language
	TsVerilog *sitter.Language
	TsVB      *sitter.Language
)

func init() {
	TsGo = sitter.NewLanguage(tree_sitter_go.Language())
	TsJava = sitter.NewLanguage(tree_sitter_java.Language())
	TsTypescript = sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
	TsTsx = sitter.NewLanguage(tree_sitter_typescript.LanguageTSX())
	TsJavascript = sitter.NewLanguage(tree_sitter_javascript.Language())
	TsPython = sitter.NewLanguage(tree_sitter_python.Language())
	TsC = sitter.NewLanguage(tree_sitter_c.Language())
	TsCpp = sitter.NewLanguage(tree_sitter_cpp.Language())
	TsCSharp = sitter.NewLanguage(tree_sitter_csharp.Language())
	TsRust = sitter.NewLanguage(tree_sitter_rust.Language())
	TsSQL = sitter.NewLanguage(tree_sitter_sql.Language())
	TsVerilog = sitter.NewLanguage(tree_sitter_verilog.Language())
	TsVB = sitter.NewLanguage(tree_sitter_vb.Language())
}
