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

// Parameter-aware bans (args_ban) fire only when a string-literal argument
// equals a banned value: the SEC-01 shape — ban MessageDigest.getInstance("MD5")
// without flagging the legitimate getInstance("SHA-256"). Variable arguments
// never fire (runtime-picked algorithms stay a review question).
func TestPolicyBanArgs(t *testing.T) {
	eng := newPolicyEngine(t, `
version: 1
languages:
  java:
    banned-api:
      - match: MessageDigest.getInstance
        args_ban: ["MD5", "MD2"]
        reason: MD5/MD2 做摘要已有已知碰撞
        use: 改用 SHA-256
`)
	findings, err := eng.CheckSource("svc/Digest.java", []byte(`package svc;

import java.security.MessageDigest;

class Digest {
    byte[] bad() throws Exception {
        return MessageDigest.getInstance("MD5").digest(new byte[0]);
    }
    byte[] good() throws Exception {
        return MessageDigest.getInstance("SHA-256").digest(new byte[0]);
    }
    byte[] runtime(String alg) throws Exception {
        return MessageDigest.getInstance(alg).digest(new byte[0]);
    }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	counts := countRules(findings)
	if counts["banned-MessageDigest-getInstance"] != 1 {
		t.Fatalf("banned-MessageDigest-getInstance = %d, want 1 (MD5 literal fires; SHA-256 and the variable do not)", counts["banned-MessageDigest-getInstance"])
	}
	for _, f := range findings {
		if f.RuleID == "banned-MessageDigest-getInstance" {
			if !strings.Contains(f.Message, "MD5") || !strings.Contains(f.Message, "禁值参数") {
				t.Errorf("message = %q, want the banned value named", f.Message)
			}
		}
	}

	// Go's selector-shaped call query carries the argument_list capture too.
	goEng := newPolicyEngine(t, `
version: 1
languages:
  go:
    banned-api:
      - match: exec.Command
        args_ban: ["sh", "bash"]
`)
	goFindings, err := goEng.CheckSource("svc/run.go", []byte(`package svc

func runShell(cmd string) error {
	c1 := exec.Command("sh", "-c", cmd)
	c2 := exec.Command("git", "log", "--oneline")
	_, _, _ = c1, c2, cmd
	return nil
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if n := countRules(goFindings)["banned-exec-Command"]; n != 1 {
		t.Errorf("banned-exec-Command = %d, want 1 (sh literal fires; git does not)", n)
	}
}

// Constructor bans (kind: constructor) see what call bans cannot:
// new SimpleDateFormat() is an object_creation_expression, not a
// method_invocation. The simple-type query fires on new SimpleDateFormat(...)
// wherever it appears; prefix patterns cover a class family.
func TestPolicyBanConstructor(t *testing.T) {
	eng := newPolicyEngine(t, `
version: 1
languages:
  java:
    banned-api:
      - match: SimpleDateFormat
        kind: constructor
        reason: 线程不安全，用 DateTimeFormatter
        use: DateTimeFormatter
      - match: Thread
        kind: constructor
`)
	findings, err := eng.CheckSource("svc/NewFix.java", []byte(`package svc;

import java.text.SimpleDateFormat;

class NewFix {
    Object bad() {
        return new SimpleDateFormat("yyyy-MM-dd");
    }
    Thread worker() {
        return new Thread(() -> {});
    }
    Object ok() {
        return new String("fine");
    }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	counts := countRules(findings)
	if counts["banned-SimpleDateFormat"] != 1 {
		t.Errorf("banned-SimpleDateFormat = %d, want 1 (new SimpleDateFormat fires; other constructors do not)", counts["banned-SimpleDateFormat"])
	}
	if counts["banned-Thread"] != 1 {
		t.Errorf("banned-Thread = %d, want 1", counts["banned-Thread"])
	}
	for _, f := range findings {
		if f.RuleID == "banned-SimpleDateFormat" && !strings.Contains(f.Message, "DateTimeFormatter") {
			t.Errorf("message = %q, want the compliant alternative", f.Message)
		}
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

// Deep receivers and FQN spellings used to fall past the receiver-shape
// enumeration (3+ segments were dropped, FQN constructors escaped). Bans now
// match by source spelling at any receiver depth, a short receiver also
// matches its FQN form, and folded multi-line spellings still compare equal.
func TestPolicyBanDeepReceiver(t *testing.T) {
	eng := newPolicyEngine(t, `
version: 1
languages:
  java:
    banned-api:
      - match: Executors.newFixedThreadPool
        reason: 公共 ForkJoinPool 不可控
        use: new ThreadPoolExecutor 显式定参
      - match: SimpleDateFormat
        kind: constructor
  go:
    banned-api:
      - match: time.Now
`)
	javaFindings, err := eng.CheckSource("svc/Deep.java", []byte(`package svc;

class Deep {
    java.util.concurrent.ExecutorService a() {
        return java.util.concurrent.Executors.newFixedThreadPool(2);
    }

    java.util.concurrent.ExecutorService b() {
        return java.util.concurrent
            .Executors
            .newFixedThreadPool(6);
    }

    Object c() {
        return new java.text.SimpleDateFormat("yyyy");
    }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	javaCounts := countRules(javaFindings)
	if javaCounts["banned-Executors-newFixedThreadPool"] != 2 {
		t.Errorf("banned-Executors-newFixedThreadPool = %d, want 2 (FQN inline + folded multi-line FQN)", javaCounts["banned-Executors-newFixedThreadPool"])
	}
	if javaCounts["banned-SimpleDateFormat"] != 1 {
		t.Errorf("banned-SimpleDateFormat = %d, want 1 (FQN constructor no longer escapes)", javaCounts["banned-SimpleDateFormat"])
	}
	joined := ""
	for _, f := range javaFindings {
		if f.RuleID == "banned-Executors-newFixedThreadPool" {
			joined += f.Message + "\n"
		}
	}
	if !strings.Contains(joined, "java.util.concurrent.Executors.newFixedThreadPool") {
		t.Errorf("messages = %q, want the real source spelling", joined)
	}
	if !strings.Contains(joined, "ThreadPoolExecutor") {
		t.Errorf("messages = %q, want the compliant alternative", joined)
	}

	goFindings, err := eng.CheckSource("svc/deep.go", []byte(`package svc

import "time"

func stamp() int64 {
	return time.Now().UnixNano()
}

func masked(x clock) int64 {
	return x.Now()
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if n := countRules(goFindings)["banned-time-Now"]; n != 1 {
		t.Errorf("banned-time-Now = %d, want 1 (receiver must match; x.Now does not)", n)
	}
}

// silent-catch flags catch blocks whose statements never reference the
// caught exception, never throw, and never log — the deterministic proxy for
// a swallowed exception. Empty blocks stay empty-block's territory; building
// a response from e.getMessage() counts as handling (information flows out).
// The rule is built-in (no policy entry needed) but severity stays
// overridable like any rule id.
func TestPolicySilentCatch(t *testing.T) {
	eng := newPolicyEngine(t, `
version: 1
languages:
  java:
    rules:
      silent-catch:
        severity: error
`)
	findings, err := eng.CheckSource("demo/Sample.java", []byte(`package demo;

class Sample {
    int commentOnly(Exception e) {
        try {
            return 1;
        } catch (Exception e2) {
            // 忽略
        }
        return 0;
    }

    int rethrown(Exception e) {
        try {
            return 1;
        } catch (Exception e2) {
            throw new IllegalStateException(e2);
        }
    }

    int logged(Exception e) {
        try {
            return 1;
        } catch (Exception e2) {
            log.error("failed", e2);
        }
        return 0;
    }

    int emptyBlock(Exception e) {
        try {
            return 1;
        } catch (Exception e2) {
        }
        return 0;
    }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	counts := countRules(findings)
	if counts["silent-catch"] != 1 {
		t.Fatalf("silent-catch = %d, want 1 (all: %v)", counts["silent-catch"], counts)
	}
	for _, f := range findings {
		if f.RuleID != "silent-catch" {
			continue
		}
		if f.Severity != model.SevError {
			t.Errorf("severity = %s, want policy override error", f.Severity)
		}
		if f.StartLine != 7 {
			t.Errorf("anchored at line %d, want 7 (the catch line where the body block starts)", f.StartLine)
		}
		if !strings.Contains(f.Message, "is caught but never referenced") {
			t.Errorf("message = %q", f.Message)
		}
		if !strings.Contains(f.Message, "\"e2\"") {
			t.Errorf("message must name the parameter, got %q", f.Message)
		}
	}
}

// A policy file that never mentions banned-api must not be *less* constrained
// than no policy file at all. Before the floor, merely adding
// ".metricgate.yaml" silently dropped the built-in bans.
func TestBanFloorSurvivesPolicyWithoutBannedAPI(t *testing.T) {
	src := []byte(`package svc

import "fmt"

func log() {
	fmt.Println("hi")
	panic("x")
}
`)
	withDefault := newPolicyEngine(t, config.DefaultYAML)
	bare := newPolicyEngine(t, "version: 1\ngate:\n  fail_on: error\n")
	defaults, err := withDefault.CheckSource("svc/a.go", src)
	if err != nil {
		t.Fatal(err)
	}
	policyOnly, err := bare.CheckSource("svc/a.go", src)
	if err != nil {
		t.Fatal(err)
	}
	want := countRules(defaults)
	got := countRules(policyOnly)
	for _, id := range []string{"banned-fmt-Print*", "banned-panic"} {
		if got[id] != want[id] || want[id] == 0 {
			t.Errorf("%s with a bare policy file = %d, want %d (same as builtin defaults)", id, got[id], want[id])
		}
	}
}

// The floor can still be retuned: an explicit ban of the same kind+match
// replaces it instead of stacking a second finding on the same call.
func TestBanFloorOverrideDoesNotDuplicate(t *testing.T) {
	eng := newPolicyEngine(t, `
version: 1
languages:
  go:
    rules:
      banned-fmt-Print*:
        severity: error
    banned-api:
      - match: fmt.Print*
        reason: our house rule
`)
	findings, err := eng.CheckSource("svc/b.go", []byte(`package svc

import "fmt"

func log() {
	fmt.Println("a")
}
`))
	if err != nil {
		t.Fatal(err)
	}
	counts := countRules(findings)
	if counts["banned-fmt-Print*"] != 1 {
		t.Fatalf("banned-fmt-Print* = %d, want 1 (override replaces the floor, not adds to it): %v", counts["banned-fmt-Print*"], counts)
	}
	for _, f := range findings {
		if f.RuleID != "banned-fmt-Print*" {
			continue
		}
		if f.Severity != model.SevError {
			t.Errorf("severity = %s, want error from the rules override", f.Severity)
		}
		if !strings.Contains(f.Message, "our house rule") {
			t.Errorf("message = %q, want the policy reason", f.Message)
		}
	}
}

// The floor is not a cage: rules.<id>.enabled: false still switches a built-in
// ban off, so projects that deliberately print (CLIs) keep a way out.
func TestBanFloorCanBeDisabled(t *testing.T) {
	eng := newPolicyEngine(t, `
version: 1
languages:
  go:
    rules:
      banned-fmt-Print*:
        enabled: false
`)
	findings, err := eng.CheckSource("svc/c.go", []byte(`package svc

import "fmt"

func log() {
	fmt.Println("a")
	panic("x")
}
`))
	if err != nil {
		t.Fatal(err)
	}
	counts := countRules(findings)
	if counts["banned-fmt-Print*"] != 0 {
		t.Errorf("banned-fmt-Print* = %d, want 0 with enabled: false", counts["banned-fmt-Print*"])
	}
	if counts["banned-panic"] != 1 {
		t.Errorf("banned-panic = %d, want 1 (only the named family is switched off)", counts["banned-panic"])
	}
}
