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
	policyPath = filepath.Join(dir, "policy.yaml")
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
	rep := runGaps(policy, dir)
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
	rep := runGaps(policy, dir)
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
	a, _ := json.Marshal(runGaps(policy, dir))
	b, _ := json.Marshal(runGaps(policy, dir))
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
