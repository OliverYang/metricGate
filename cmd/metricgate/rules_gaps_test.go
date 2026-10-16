package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"metricgate/internal/policygap"
)

// These tests cover the command that answers "is a clean run clean, or is the
// policy thin?" — the one question a finding list cannot answer. They use a
// real run over a real tree rather than a hand-built summary, because the audit
// is only worth having if it agrees with `check`.

const gapsGoSource = "package main\n\nfunc main() {\n\tprintln(1)\n}\n"

// writeGapsFixture builds a one-file Go tree and writes a policy into it.
func writeGapsFixture(t *testing.T, policy string) (dir, policyPath string) {
	t.Helper()
	dir = t.TempDir()
	policyPath = filepath.Join(dir, ".metricgate.yaml")
	writeGapsFile(t, policyPath, policy)
	writeGapsFile(t, filepath.Join(dir, "main.go"), gapsGoSource)
	return dir, policyPath
}

func writeGapsFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func gapsStatuses(t *testing.T, rep policygap.Report) map[string]policygap.Status {
	t.Helper()
	out := map[string]policygap.Status{}
	for _, r := range rep.Rules {
		out[r.RuleID] = r.Status
	}
	return out
}

func TestRulesGapsReportsUndeclaredArchitecture(t *testing.T) {
	dir, policy := writeGapsFixture(t, "version: 1\n")
	rep := runGaps(policy, dir, false)
	status := gapsStatuses(t, rep)
	for _, id := range []string{"layer-violation", "forbidden-edge", "import-cycle"} {
		if status[id] != policygap.StatusUndeclared {
			t.Errorf("%s = %s, want undeclared with no architecture policy", id, status[id])
		}
	}
}

func TestRulesGapsArmsDeclaredArchitecture(t *testing.T) {
	dir, policy := writeGapsFixture(t, `version: 1
architecture:
  layers:
    - name: root
      packages: ["."]
      may_depend_on: [root]
`)
	rep := runGaps(policy, dir, false)
	status := gapsStatuses(t, rep)
	for _, id := range []string{"layer-violation", "import-cycle"} {
		if status[id] != policygap.StatusArmed {
			t.Errorf("%s = %s, want armed once architecture.layers is declared", id, status[id])
		}
	}
	// forbid is a separate section; declaring layers must not imply it.
	if status["forbidden-edge"] != policygap.StatusUndeclared {
		t.Errorf("forbidden-edge = %s, want undeclared", status["forbidden-edge"])
	}
}

func TestRulesGapsIsDeterministic(t *testing.T) {
	dir, policy := writeGapsFixture(t, "version: 1\n")
	a, _ := json.Marshal(runGaps(policy, dir, false))
	b, _ := json.Marshal(runGaps(policy, dir, false))
	if string(a) != string(b) {
		t.Errorf("two audits of the same tree disagree:\n%s\n%s", a, b)
	}
}

func TestGraphSourceSaysHowToArm(t *testing.T) {
	cases := []struct {
		rule policygap.GraphRule
		want string
	}{
		{policygap.GraphRule{ID: "x", Armed: true, ArmedBy: "architecture.layers", InCheck: true}, "armed by architecture.layers"},
		{policygap.GraphRule{ID: "x", ArmedBy: "check -diff -regress"}, "needs a flag: check -diff -regress"},
		{policygap.GraphRule{ID: "x", ArmedBy: "architecture.forbid", InCheck: true}, "not armed — declare architecture.forbid"},
	}
	for _, c := range cases {
		if got := graphSource(c.rule); got != c.want {
			t.Errorf("graphSource(%+v) = %q, want %q", c.rule, got, c.want)
		}
	}
}

// TestRulesGapsRoutesByOwner covers the routing section. A tree with a
// CODEOWNERS that claims the file must show the owner with the firing rule, and
// the count is of findings — not of rules — because that is what makes the
// group worth opening.
func TestRulesGapsRoutesByOwner(t *testing.T) {
	dir, policy := writeGapsFixture(t, `version: 1
languages:
  go:
    banned-api:
      - match: println
        reason: test fixture
`)
	if err := os.MkdirAll(filepath.Join(dir, ".github"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeGapsFile(t, filepath.Join(dir, ".github", "CODEOWNERS"), "*.go @gophers\n")
	rep := runGaps(policy, dir, true)
	if len(rep.Owners) == 0 {
		t.Fatalf("no owner groups with a CODEOWNERS claiming *.go: %+v", rep.Owners)
	}
	found := false
	for _, g := range rep.Owners {
		if g.Owner == "@gophers" {
			found = true
			if g.Findings == 0 {
				t.Errorf("@gophers group reports 0 findings")
			}
			if len(g.RuleIDs) == 0 {
				t.Errorf("@gophers group names no rule")
			}
			for _, id := range g.RuleIDs {
				if id != "banned-println" {
					t.Errorf("unexpected rule %q routed to @gophers", id)
				}
			}
		}
	}
	if !found {
		t.Errorf("no @gophers group in %+v", rep.Owners)
	}
}

// TestRulesGapsWithoutCodeownersDoesNotRoute states the off-switch instead of
// printing an empty section: with no CODEOWNERS the audit says the catalogue
// cannot be routed, rather than implying every owner was checked and found
// quiet.
func TestRulesGapsWithoutCodeownersDoesNotRoute(t *testing.T) {
	dir, policy := writeGapsFixture(t, `version: 1
languages:
  go:
    banned-api:
      - match: println
        reason: test fixture
`)
	rep := runGaps(policy, dir, true)
	if len(rep.Owners) != 0 {
		t.Errorf("owner groups = %+v, want none without a CODEOWNERS", rep.Owners)
	}
	if rep.Unattributed == 0 {
		t.Errorf("Unattributed = 0, want the catalogued rule count")
	}
}
