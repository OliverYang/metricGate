package metrics

import (
	"testing"

	"metricgate/internal/parser"
)

// The per-language tables in this package were each written as their own
// switch, and three of them ended in a fallback to Go: ProfileFor returned
// goProfile, lowererFor returned goLowerer{}, and callSpellingFor silently
// kept Go's spelling. A language added to parser.Supported() but forgotten
// here was therefore measured with Go's node kinds — plausible numbers for a
// grammar that has none of those nodes, i.e. an empty file that looked like a
// clean one.
//
// This test is the tripwire. Every language in parser.Supported() must be
// answered by name, and the answer must be "measured" or "explicitly not
// measured", never "whatever Go does".

// TestLanguageParityProfile pins ProfileFor to the supported set.
//
// Every language in Supported() must appear in langProfiles by name. "Not in
// the map" used to mean "nil profile, contributes nothing", which is
// indistinguishable from "nobody added it yet" — the exact silence this goal
// exists to remove. A language that genuinely has no function-level unit now
// has to say so by registering an empty profile, not by being absent.
func TestLanguageParityProfile(t *testing.T) {
	for _, id := range parser.Supported() {
		prof, ok := langProfiles[id]
		if !ok {
			t.Errorf("%s: no entry in langProfiles; "+
				"an absent language is indistinguishable from a forgotten one", id)
			continue
		}
		if prof == nil {
			continue // registered as deliberately empty: no function-level unit
		}
		if prof.FuncKinds == nil {
			t.Errorf("%s: profile has no FuncKinds; register a nil entry to declare none", id)
			continue
		}
		if prof.FuncKinds.Language == nil {
			t.Errorf("%s: FuncKinds has no grammar, so Has always reports false", id)
		}
	}
}

// TestLanguageParityLowererAndSpelling answers the same question for the two
// metric tables that are consulted only when a function has a body: a language
// must be present in langLowerers and callSpellings, and presence with a nil
// value is the explicit way to say "no control-flow score". Absence used to
// mean the same thing, which is how a new language could inherit Go's node
// kinds without anybody noticing.
func TestLanguageParityLowererAndSpelling(t *testing.T) {
	for _, id := range parser.Supported() {
		if _, ok := langLowerers[id]; !ok {
			t.Errorf("%s: no entry in langLowerers; "+
				"an absent language is indistinguishable from a forgotten one", id)
		}
		if _, ok := callSpellings[id]; !ok {
			t.Errorf("%s: no entry in callSpellings; "+
				"an absent language is indistinguishable from a forgotten one", id)
		}
	}
	if got := lowererFor(parser.LanguageID("no-such-language")); got != nil {
		t.Errorf("lowererFor(unknown) = %T, want nil", got)
	}
	if got := callSpellingFor(parser.LanguageID("no-such-language")); got != nil {
		t.Errorf("callSpellingFor(unknown) = %+v, want nil", *got)
	}
}

// TestLanguageParityProfileUnknownIsEmpty is the behavioural half of the
// tripwire: an unregistered language must contribute no functions at all.
// Before the fix it inherited goProfile and reported Go's function kinds.
func TestLanguageParityProfileUnknownIsEmpty(t *testing.T) {
	if prof := ProfileFor(parser.LanguageID("no-such-language")); prof != nil {
		t.Fatalf("ProfileFor(unknown) = %+v, want nil", prof)
	}
	// A nil profile must be safe to query through HasFunc.
	var prof *LangProfile
	if prof.HasFunc(nil) {
		t.Error("(*LangProfile)(nil).HasFunc = true, want false")
	}
}

// TestLanguageParityFunctionKindsAreReal asks each grammar whether it knows
// the kinds its profile claims. A kind that no grammar names is a typo, and a
// typo here is invisible in production: the function is simply never seen, so
// the file reports zero complexity rather than an error.
func TestLanguageParityFunctionKindsAreReal(t *testing.T) {
	for _, id := range parser.Supported() {
		prof := ProfileFor(id)
		if prof == nil || prof.FuncKinds == nil {
			continue
		}
		if got := len(prof.FuncKinds.Kinds); got == 0 {
			t.Errorf("%s: profile lists no function kinds", id)
			continue
		}
		grammar := prof.FuncKinds.Language
		if grammar == nil {
			t.Errorf("%s: profile has no grammar to check kinds against", id)
			continue
		}
		for _, kind := range prof.FuncKinds.Kinds {
			if grammar.IdForNodeKind(kind, true) == 0 {
				t.Errorf("%s: grammar does not name node kind %q", id, kind)
			}
		}
	}
}
