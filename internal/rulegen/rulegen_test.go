package rulegen

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"metricgate/internal/config"
)

const md5Spec = `{
  "lang": "java",
  "kind": "call",
  "match": "MessageDigest.getInstance",
  "args_ban": ["MD5", "MD2"],
  "reason": "MD5/MD2 已有已知碰撞，禁止用于摘要/签名",
  "use": "改用 SHA-256：MessageDigest.getInstance(\"SHA-256\")"
}`

func mustVerify(t *testing.T, spec string, bad, good []FixtureInput, base *config.Config) *VerifyResult {
	t.Helper()
	res, err := Verify([]byte(spec), bad, good, base)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return res
}

func TestVerifyHitAndClean(t *testing.T) {
	res := mustVerify(t, md5Spec,
		[]FixtureInput{{Name: "md5", Content: "class A { void b() { javax.crypto.MessageDigest.getInstance(\"MD5\"); } }"}},
		[]FixtureInput{{Name: "sha", Content: "class B { void g() { javax.crypto.MessageDigest.getInstance(\"SHA-256\"); } }"}},
		nil)
	if !res.Pass {
		t.Fatalf("want pass, got %+v\nnotes: %v", res, res.Notes)
	}
	if res.RuleID != "banned-MessageDigest-getInstance" {
		t.Fatalf("RuleID = %q", res.RuleID)
	}
	if res.Fixtures[0].Verdict != "pass" || len(res.Fixtures[0].Hits) != 1 {
		t.Fatalf("bad fixture verdict = %s hits = %d", res.Fixtures[0].Verdict, len(res.Fixtures[0].Hits))
	}
	if res.Fixtures[1].Verdict != "pass" || len(res.Fixtures[1].Hits) != 0 {
		t.Fatalf("good fixture verdict = %s hits = %d", res.Fixtures[1].Verdict, len(res.Fixtures[1].Hits))
	}
	if !strings.Contains(res.YAML, "args_ban") || !strings.Contains(res.YAML, "MessageDigest.getInstance") {
		t.Fatalf("YAML fragment missing fields:\n%s", res.YAML)
	}
	if !res.Compiled || res.CompileError != "" {
		t.Fatalf("compiled = %v err = %q", res.Compiled, res.CompileError)
	}
}

func TestVerifyGoImportBan(t *testing.T) {
	spec := `{"lang":"go","kind":"import","match":"crypto/md5","reason":"禁用 MD5","severity":"error"}`
	res := mustVerify(t, spec,
		[]FixtureInput{{Name: "gobad", Content: "import \"crypto/md5\"\n\nvar _ = md5.New"}},
		[]FixtureInput{{Name: "gogood", Content: "import \"crypto/sha256\"\n\nvar _ = sha256.New"}},
		nil)
	if !res.Pass {
		t.Fatalf("want pass, got %+v\nnotes: %v", res, res.Notes)
	}
	if res.Fixtures[0].Hits[0].Severity != "error" {
		t.Fatalf("severity = %q, want error", res.Fixtures[0].Hits[0].Severity)
	}
	if !strings.Contains(res.PolicyYAML, "banned-crypto-md5") {
		t.Fatalf("policy missing rule id:\n%s", res.PolicyYAML)
	}
}

func TestVerifyBadFixtureMissesIsFail(t *testing.T) {
	// The ban targets java.util.logging but the bad fixture imports logback:
	// the counter-example does not reach the ban -> fail, not pass.
	spec := `{"lang":"java","kind":"import","match":"java.util.logging"}`
	res := mustVerify(t, spec,
		[]FixtureInput{{Name: "wrong", Content: "import ch.qos.logback.classic.Logger;"}},
		nil, nil)
	if res.Pass {
		t.Fatal("want fail when the bad fixture never triggers the rule")
	}
	if res.Fixtures[0].Verdict != "fail" {
		t.Fatalf("verdict = %s", res.Fixtures[0].Verdict)
	}
}

func TestVerifyGoodFixtureHitIsFail(t *testing.T) {
	spec := `{"lang":"java","kind":"import","match":"org.apache.commons.text"}`
	res := mustVerify(t, spec,
		[]FixtureInput{{Name: "ok", Content: "import org.apache.commons.text.WordUtils;"}},
		[]FixtureInput{{Name: "alsobad", Content: "import org.apache.commons.text.StringEscapeUtils;"}},
		nil)
	if res.Pass {
		t.Fatal("want fail when the good fixture triggers the rule (match too wide)")
	}
	if res.Fixtures[1].Verdict != "fail" {
		t.Fatalf("verdict = %s", res.Fixtures[1].Verdict)
	}
}

func TestValidateVocabulary(t *testing.T) {
	cases := []struct {
		spec  string
		badOK bool
	}{
		{`{"lang":"cobol","kind":"call","match":"x"}`, false},                   // unregistered language
		{`{"lang":"go","kind":"constructor","match":"X"}`, false},               // kind Go cannot express
		{`{"lang":"python","kind":"constructor","match":"X"}`, false},           // kind Python cannot express
		{`{"lang":"java","kind":"call","match":""}`, false},                     // empty match
		{`{"lang":"java","kind":"call","match":"x","severity":"fatal"}`, false}, // bad severity
		{`{"lang":"java","kind":"constructor","match":"SimpleDateFormat"}`, true},
		{`{"lang":"python","kind":"call","match":"requests.get"}`, true},
		{`{"lang":"typescript","kind":"constructor","match":"Foo"}`, true},
		{`{"lang":"rust","kind":"import","match":"openssl"}`, true},
		{`{"lang":"csharp","kind":"call","match":"Console.WriteLine"}`, true},
	}
	for i, c := range cases {
		err := Validate(mustSpec(t, c.spec))
		if c.badOK && err != nil {
			t.Fatalf("case %d: unexpected error %v", i, err)
		}
		if !c.badOK && err == nil {
			t.Fatalf("case %d: expected validation failure", i)
		}
	}
}

func TestVerifyRejectsInvalidInput(t *testing.T) {
	if _, err := Verify([]byte("not json"), []FixtureInput{{Content: "x"}}, nil, nil); err == nil {
		t.Fatal("want error on invalid JSON")
	}
	if _, err := Verify([]byte(`{"lang":"java","kind":"call","match":"x"}`), nil, nil, nil); err == nil {
		t.Fatal("want error when no bad fixture provided")
	}
}

func TestDedupAgainstBasePolicy(t *testing.T) {
	base := mustLoadPolicy(t, `
languages:
  java:
    banned-api:
      - kind: call
        match: Executors.newFixedThreadPool
`)
	spec := `{"lang":"java","kind":"call","match":"Executors.newFixedThreadPool"}`
	res := mustVerify(t, spec,
		[]FixtureInput{{Name: "x", Content: "class A { void b() { java.util.concurrent.Executors.newFixedThreadPool(2); } }"}},
		nil, base)
	if !res.Existing {
		t.Fatal("want existing=true when base policy already bans the same kind+match")
	}
}

func TestPolicyYAMLOverrideRoundTrip(t *testing.T) {
	// The rendered fixture policy must parse and carry the severity override
	// under the derived rule id — this is what makes fixture grading reflect
	// the requested severity.
	s := &Spec{Lang: "java", Kind: "call", Match: "Foo.bar", Severity: "error"}
	pol := policyYAML(s)
	if !strings.Contains(pol, "banned-Foo-bar:\n        severity: error") {
		t.Fatalf("policy missing severity override:\n%s", pol)
	}
	if !strings.Contains(pol, "unknown-dependency:\n        enabled: false") {
		t.Fatalf("policy missing unknown-dependency veto:\n%s", pol)
	}
}

func mustLoadPolicy(t *testing.T, yamlText string) *config.Config {
	t.Helper()
	path := t.TempDir() + "/base.yaml"
	if err := os.WriteFile(path, []byte(yamlText), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("base policy: %v", err)
	}
	return cfg
}

func mustSpec(t *testing.T, raw string) *Spec {
	t.Helper()
	var s Spec
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("spec: %v", err)
	}
	return &s
}
