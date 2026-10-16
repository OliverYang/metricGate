package parser

import (
	"testing"
)

// TestSupportedMatchesGrammars pins the two hand-written language lists
// together. Adding a language to Supported without a grammar (or the reverse)
// used to be invisible until a user hit "unsupported language" at runtime, or
// silently got Go's tables from metrics.ProfileFor; this test is the tripwire.
func TestSupportedMatchesGrammars(t *testing.T) {
	// The count is pinned on purpose: growing the language set must be a
	// deliberate edit here, so a half-finished registration cannot ship by
	// merely matching grammars against itself.
	const wantLanguages = 13

	supported := Supported()
	if got := len(supported); got != wantLanguages {
		t.Fatalf("Supported() has %d languages, want %d", got, wantLanguages)
	}
	if got, want := len(supported), len(grammars); got != want {
		t.Fatalf("Supported() has %d languages, grammars has %d", got, want)
	}
	for _, id := range supported {
		if _, ok := grammars[id]; !ok {
			t.Errorf("Supported() lists %q but grammars has no entry", id)
		}
	}
	for id := range grammars {
		if !IsSupported(id) {
			t.Errorf("grammars has %q but IsSupported reports false", id)
		}
	}
}

// TestGrammarPointersNonNil builds every registered grammar. A grammar whose
// cgo binding is missing or misnamed still compiles; it fails here.
func TestGrammarPointersNonNil(t *testing.T) {
	for _, id := range Supported() {
		fn := grammars[id]
		if fn == nil {
			t.Errorf("%s: grammar constructor is nil", id)
			continue
		}
		if ptr := fn(); ptr == nil {
			t.Errorf("%s: grammar constructor returned a nil pointer", id)
		}
	}
}

// TestNewParsesEveryLanguage goes one step further than a non-nil pointer: it
// sets the language on a parser and parses a trivial source. SetLanguage is
// where a tree-sitter ABI mismatch surfaces.
func TestNewParsesEveryLanguage(t *testing.T) {
	for _, id := range Supported() {
		p, err := New(id)
		if err != nil {
			t.Errorf("%s: New: %v", id, err)
			continue
		}
		tree, err := p.Parse([]byte("\n"))
		if err != nil {
			t.Errorf("%s: Parse: %v", id, err)
			p.Close()
			continue
		}
		if root := tree.RootNode(); root == nil {
			t.Errorf("%s: Parse returned a tree with no root node", id)
		}
		tree.Close()
		p.Close()
	}
}

// TestExtensionsUniqueAndInvertible keeps the extension table a function in
// both directions. Two languages claiming one extension would make
// LanguageForFile pick whichever switch arm comes first, and an extension with
// no LanguageForFile arm would break the staging path that writes a file and
// reads it back.
func TestExtensionsUniqueAndInvertible(t *testing.T) {
	seen := make(map[string]LanguageID, len(extensions))
	for _, id := range Supported() {
		ext, ok := extensions[id]
		if !ok || ext == "" {
			t.Errorf("%s: no canonical extension", id)
			continue
		}
		if other, dup := seen[ext]; dup {
			t.Errorf("extension %q claimed by both %s and %s", ext, other, id)
			continue
		}
		seen[ext] = id

		got, ok := LanguageForFile("sample" + ext)
		if !ok {
			t.Errorf("%s: LanguageForFile(%q) reports unsupported", id, "sample"+ext)
			continue
		}
		if got != id {
			t.Errorf("extension %q: LanguageForFile says %s, extensions says %s", ext, got, id)
		}
	}
	for id := range extensions {
		if !IsSupported(id) {
			t.Errorf("extensions has %q which is not a registered language", id)
		}
	}
}

// TestLanguageForFileRejectsUnknown keeps the negative direction honest: the
// switch must have a default that reports failure rather than returning the
// zero LanguageID with ok true.
func TestLanguageForFileRejectsUnknown(t *testing.T) {
	for _, name := range []string{"main.rs.txt", "Makefile", "index.php", "app.rb", ""} {
		if id, ok := LanguageForFile(name); ok {
			t.Errorf("LanguageForFile(%q) = %q, true; want false", name, id)
		}
	}
}
