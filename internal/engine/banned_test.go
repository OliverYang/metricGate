package engine

import (
	"strings"
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/model"
)

// newPolicyEngine builds an engine from raw policy text.
func newPolicyEngine(t *testing.T, policy string) *Engine {
	t.Helper()
	cfg, err := config.Load([]byte(policy))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return eng
}

func countRules(findings []model.Finding) map[string]int {
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.RuleID]++
	}
	return counts
}

// A ban written in YAML must behave exactly like a compiled-in rule: same
// findings, same severities, same suppression syntax.
func TestPolicyBanCall(t *testing.T) {
	eng := newPolicyEngine(t, `
version: 1
languages:
  go:
    banned-api:
      - match: strings.ToLower
        reason: use the locale-aware normaliser
      - match: os.Getenv*
`)
	findings, err := eng.CheckSource("svc/a.go", []byte(`package svc

import "strings"

func norm(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
`))
	if err != nil {
		t.Fatal(err)
	}
	counts := countRules(findings)
	if counts["banned-strings-ToLower"] != 1 {
		t.Fatalf("banned-strings-ToLower = %d, want 1 (all: %v)", counts["banned-strings-ToLower"], counts)
	}
	for _, f := range findings {
		if f.RuleID == "banned-strings-ToLower" {
			if !strings.Contains(f.Message, "locale-aware normaliser") {
				t.Errorf("message = %q, want the policy reason", f.Message)
			}
			if f.Severity != model.SevWarning || f.Source != model.SrcRule {
				t.Errorf("severity/source = %s/%s", f.Severity, f.Source)
			}
		}
	}
}

// Prefix bans cover the whole family; the generated id keeps the "*" so the
// family and a single member can be configured apart.
func TestPolicyBanPrefixAndSeverityOverride(t *testing.T) {
	eng := newPolicyEngine(t, `
version: 1
languages:
  go:
    rules:
      banned-fmt-Print*:
        severity: error
    banned-api:
      - match: fmt.Print*
`)
	findings, err := eng.CheckSource("svc/b.go", []byte(`package svc

import "fmt"

func log() {
	fmt.Println("a")
	fmt.Printf("%d", 1)
	fmt.Sprintf("%d", 2)
}
`))
	if err != nil {
		t.Fatal(err)
	}
	counts := countRules(findings)
	if counts["banned-fmt-Print*"] != 2 {
		t.Fatalf("banned-fmt-Print* = %d, want 2 (Println, Printf; Sprintf is not a statement call)", counts["banned-fmt-Print*"])
	}
	var spellings []string
	for _, f := range findings {
		if f.RuleID != "banned-fmt-Print*" {
			continue
		}
		if f.Severity != model.SevError {
			t.Errorf("severity = %s, want error from policy override", f.Severity)
		}
		spellings = append(spellings, f.Message)
	}
	joined := strings.Join(spellings, "\n")
	for _, want := range []string{"fmt.Println is banned", "fmt.Printf is banned"} {
		if !strings.Contains(joined, want) {
			t.Errorf("messages = %q, want %q", joined, want)
		}
	}
}

// except_files keeps legitimate homes of a pattern (CLI entrypoints, tests)
// out of a ban without touching the ban itself.
func TestPolicyBanExceptFiles(t *testing.T) {
	eng := newPolicyEngine(t, `
version: 1
languages:
  go:
    banned-api:
      - match: panic
        except_files: ["_test.go"]
`)
	src := []byte("package svc\n\nfunc Boom() {\n\tpanic(\"x\")\n}\n")
	findings, err := eng.CheckSource("svc/svc_test.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if n := countRules(findings)["banned-panic"]; n != 0 {
		t.Errorf("banned-panic in _test.go = %d, want 0 (except_files)", n)
	}
	findings, err = eng.CheckSource("svc/svc.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if n := countRules(findings)["banned-panic"]; n != 1 {
		t.Errorf("banned-panic in svc.go = %d, want 1", n)
	}
}

// Import bans work for both grammars.
func TestPolicyBanImport(t *testing.T) {
	eng := newPolicyEngine(t, `
version: 1
languages:
  go:
    banned-api:
      - kind: import
        match: os/exec
        reason: the checker never shells out
  java:
    banned-api:
      - kind: import
        match: java.util.concurrent.Executors
`)
	goFindings, err := eng.CheckSource("svc/c.go", []byte(`package svc

import (
	"fmt"
	"os/exec"
)

func Run() {
	_ = fmt.Sprint(exec.Command("ls"))
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if n := countRules(goFindings)["banned-os-exec"]; n != 1 {
		t.Errorf("banned-os-exec = %d, want 1", n)
	}

	javaFindings, err := eng.CheckSource("svc/C.java", []byte(`package svc;

import java.util.concurrent.Executors;
import java.util.List;

class C {
    void m() { Executors.newSingleThreadExecutor(); }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if n := countRules(javaFindings)["banned-java-util-concurrent-Executors"]; n != 1 {
		t.Errorf("java import ban = %d, want 1", n)
	}
}

// Java bans cover unqualified calls, one-receiver calls and console streams.
func TestPolicyBanJavaCalls(t *testing.T) {
	eng := newPolicyEngine(t, `
version: 1
languages:
  java:
    banned-api:
      - match: System.exit*
      - match: Thread.sleep*
`)
	findings, err := eng.CheckSource("svc/D.java", []byte(`package svc;

class D {
    void m() throws Exception {
        System.exit(0);
        Thread.sleep(10);
        Runtime.getRuntime().exit(0);
    }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	counts := countRules(findings)
	if counts["banned-System-exit*"] != 1 {
		t.Errorf("banned-System-exit* = %d, want 1", counts["banned-System-exit*"])
	}
	if counts["banned-Thread-sleep*"] != 1 {
		t.Errorf("banned-Thread-sleep* = %d, want 1", counts["banned-Thread-sleep*"])
	}
}

// A ban that cannot be expressed must be dropped, not fail the run.
func TestPolicyBanMalformedEntriesDropped(t *testing.T) {
	eng := newPolicyEngine(t, `
version: 1
languages:
  go:
    banned-api:
      - match: ""
      - kind: regex
        match: "fmt.*"
      - match: strings.ToUpper
`)
	findings, err := eng.CheckSource("svc/e.go", []byte(`package svc

import "strings"

func up(s string) string { return strings.ToUpper(s) }
`))
	if err != nil {
		t.Fatal(err)
	}
	if n := countRules(findings)["banned-strings-ToUpper"]; n != 1 {
		t.Errorf("banned-strings-ToUpper = %d, want 1 (valid entry still applied)", n)
	}
}

// Policy bans appear in the SARIF rule catalogue so tooling sees them.
func TestPolicyBansAppearInRuleCatalogue(t *testing.T) {
	eng := newPolicyEngine(t, `
version: 1
languages:
  go:
    banned-api:
      - match: fmt.Print*
`)
	seen := map[string]bool{}
	for _, m := range eng.Rules() {
		seen[m.ID] = true
	}
	for _, id := range []string{"banned-fmt-Print*", "empty-block", "todo-comment"} {
		if !seen[id] {
			t.Errorf("rule %q missing from catalogue", id)
		}
	}
}
