package engine

import (
	"strings"
	"testing"

	"metricgate/internal/model"
)

// checkWithAllow runs CheckSource with a pinned dependency allowlist, so the
// assertion does not depend on this repository's own go.mod.
func checkWithAllow(t *testing.T, allow []string, name, src string) map[string]int {
	t.Helper()
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	eng.depAllow = allow
	findings, err := eng.CheckSource(name, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.RuleID]++
	}
	return counts
}

func TestUnknownDependencyFlagsOnlyUndeclaredGoImports(t *testing.T) {
	src := `package gen

import (
	"fmt"
	"github.com/known/dep"
	"github.com/hallucinated/pkg"
)
`
	counts := checkWithAllow(t, []string{"github.com/known/dep"}, "gen/x.go", src)
	if counts["unknown-dependency"] != 1 {
		t.Errorf("unknown-dependency = %d, want exactly 1 (only the hallucinated import)\n%v",
			counts["unknown-dependency"], counts)
	}
}

func TestUnknownDependencyFlagsOnlyUndeclaredJavaImports(t *testing.T) {
	src := `package gen;

import java.util.List;
import org.slf4j.Logger;
import com.example.Hallucinated;

class X {}
`
	counts := checkWithAllow(t, []string{"org.slf4j"}, "gen/X.java", src)
	if counts["unknown-dependency"] != 1 {
		t.Errorf("unknown-dependency = %d, want exactly 1 (only com.example.Hallucinated)\n%v",
			counts["unknown-dependency"], counts)
	}
}

// A prefix must match on a boundary: a listed module must not silently cover a
// longer, different package name.
func TestUnknownDependencyPrefixMatchesOnBoundary(t *testing.T) {
	src := `package gen

import "github.com/known/dependency"

var _ = dependency.Thing
`
	counts := checkWithAllow(t, []string{"github.com/known/dep"}, "gen/x.go", src)
	if counts["unknown-dependency"] != 1 {
		t.Errorf("unknown-dependency = %d, want 1: the prefix must not cover 'dependency'", counts["unknown-dependency"])
	}
}

// The rule is a plain rule, so a policy can disable it and `metricgate: ignore`
// can suppress it — the same escape hatches as every other rule.
// A quoted include is a repository-local header, not an external dependency:
// trimming the quotes away would turn "local.h" into a bare package name that
// no manifest could ever declare, so it must never be flagged. A third-party
// angle-bracket include is still a real dependency and must still be flagged.
func TestUnknownDependencyQuotedIncludeIsRepoLocal(t *testing.T) {
	src := `#include "local.h"
#include <stdio.h>
#include <openssl/ssl.h>

int main(void) { return 0; }
`
	for _, tc := range []struct {
		name string
		lang string
	}{
		{"gen/main.c", "C"},
		{"gen/main.cpp", "C++"},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			counts := checkWithAllow(t, nil, tc.name, src)
			if counts["unknown-dependency"] != 1 {
				t.Errorf("unknown-dependency = %d, want exactly 1 (only <openssl/ssl.h>): a quoted include is repo-local and <stdio.h> is the standard library\n%v",
					counts["unknown-dependency"], counts)
			}
		})
	}
}

// The exemption must not swallow the allowlist: a declared third-party angle
// include stays quiet, an undeclared one is still reported.
func TestUnknownDependencyAngleIncludeRespectsAllowlist(t *testing.T) {
	src := `#include <real-lib/api.h>
#include <other-lib/api.h>
`
	counts := checkWithAllow(t, []string{"real-lib"}, "gen/main.c", src)
	if counts["unknown-dependency"] != 1 {
		t.Errorf("unknown-dependency = %d, want exactly 1 (only <other-lib/api.h>)\n%v",
			counts["unknown-dependency"], counts)
	}
}

func TestUnknownDependencyCarriesEvidenceAnchor(t *testing.T) {
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	eng.depAllow = []string{"github.com/known/dep"}
	findings, err := eng.CheckSource("gen/x.go", []byte("package gen\n\nimport \"github.com/hallucinated/pkg\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	var found *model.Finding
	for i := range findings {
		if findings[i].RuleID == "unknown-dependency" {
			found = &findings[i]
		}
	}
	if found == nil {
		t.Fatal("unknown-dependency finding missing")
	}
	if found.Evidence == nil || found.Evidence.Anchor != model.AnchorExact {
		t.Errorf("evidence = %+v, want an exact anchor on the import path", found.Evidence)
	}
	if !strings.Contains(found.Message, "github.com/hallucinated/pkg") {
		t.Errorf("message should name the import: %q", found.Message)
	}
}
