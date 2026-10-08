package digest

import (
	"strings"
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/symbols"
)

func testConfig(t *testing.T, yaml string) *config.Config {
	t.Helper()
	cfg, err := config.Load([]byte(yaml))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

const fullPolicy = `
version: 1
gate:
  fail_on: error
dependencies:
  self: ["org.osc.scan.portal"]
  allow: ["org.springframework", "org.slf4j"]
architecture:
  cycles: warning
  layers:
    - name: service
      packages: ["internal/engine", "org/osc/scan/portal/service/**"]
      may_depend_on: ["service", "dao"]
    - name: web
      packages: ["org/osc/scan/portal/controller/**"]
      may_depend_on: ["service"]
  forbid:
    - from: "**"
      to: "cmd/**"
      reason: "nothing may depend on the CLI shell"
languages:
  go:
    banned-api:
      - match: "fmt.Print*"
        reason: "use the project logger"
      - match: "crypto/md5"
        kind: import
        reason: "MD5 已有已知碰撞，安全用途禁用"
        use: "SHA-256 或以上"
      - match: "MessageDigest.getInstance"
        args_ban: ["MD5", "MD2"]
        reason: "MD5/MD2 做摘要已有已知碰撞"
        use: "改用 SHA-256：MessageDigest.getInstance(\"SHA-256\")"
      - match: "os/exec"
        kind: import
        reason: "only the git adapter may shell out"
        use: "注入命令执行端口接口（参见内部 git 适配层）"
      - match: "testing.Log"
        except_files: ["*_test.go"]
        reason: "test-only logging"
  java:
    banned-api:
      - match: "SimpleDateFormat"
        kind: constructor
        reason: "线程不安全，用 DateTimeFormatter"
        use: "DateTimeFormatter"
`

// A layered policy match surfaces the layer contract, bans, cycles and
// effective thresholds, and every section carries provenance.
func TestBuildFullPolicy(t *testing.T) {
	cfg := testConfig(t, fullPolicy)
	res := Build(cfg, Options{Package: "internal/engine", Lang: "go", PackageResolved: true})

	assertDigestContains(t, res.Rendered, "「service」层", "matched layer")
	assertDigestContains(t, res.Rendered, "替代写法：注入命令执行端口接口", "use hint rendered")
	assertDigestContains(t, res.Rendered, "禁止调用 fmt.Print*", "call bans")
	assertDigestContains(t, res.Rendered, "禁止 import os/exec", "import bans")
	assertDigestContains(t, res.Rendered, "禁止 import 环", "cycles")
	assertDigestContains(t, res.Rendered, "圈复杂度 ≤10", "effective thresholds")
	assertDigestContains(t, res.Rendered, "禁止调用 MessageDigest.getInstance（仅当参数为 MD5/MD2）", "args-ban parameter semantics rendered")

	// Constructor bans are Java-only: a go digest must not advertise them.
	jres := Build(cfg, Options{Package: "org/osc/scan/portal/service", Lang: "java", PackageResolved: true})
	assertDigestContains(t, jres.Rendered, "禁止 new SimpleDateFormat", "constructor ban rendered")
	assertDigestNotContains(t, res.Rendered, "SimpleDateFormat", "go digest omits java-only constructor bans")
	if len(res.Sections) == 0 {
		t.Fatal("no sections rendered")
	}
	for _, s := range res.Sections {
		if len(s.Sources) == 0 {
			t.Errorf("section %q has no provenance sources", s.Name)
		}
	}
}

// The reuse section lists exported non-test symbols of the target package
// only, with type bodies trimmed from signatures.
func TestBuildReuseList(t *testing.T) {
	cfg := testConfig(t, fullPolicy)
	ix := symbols.Build([]symbols.Def{
		{Name: "Run", File: "internal/engine/engine.go", Kind: "function", Visibility: "exported", Signature: "func Run(root string) error", Package: "engine"},
		{Name: "TestRun", File: "internal/engine/engine_test.go", Kind: "function", Visibility: "exported", Signature: "func TestRun(t *testing.T)", Package: "engine"},
		{Name: "helper", File: "internal/engine/util.go", Kind: "function", Visibility: "private", Signature: "func helper()", Package: "engine"},
	})
	res := Build(cfg, Options{Package: "internal/engine", Lang: "go", Index: ix, TargetPkg: "internal/engine", PackageResolved: true})

	assertDigestContains(t, res.Rendered, "已有 func Run(root string) error", "exported Run")
	assertDigestNotContains(t, res.Rendered, "TestRun", "test functions")
	assertDigestNotContains(t, res.Rendered, "func helper()", "unexported symbols")
}

func assertDigestContains(t *testing.T, got, want, what string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("digest should include %s (%q), got:\n%s", what, want, got)
	}
}

func assertDigestNotContains(t *testing.T, got, unwanted, what string) {
	t.Helper()
	if strings.Contains(got, unwanted) {
		t.Errorf("digest must not include %s (%q), got:\n%s", what, unwanted, got)
	}
}

// No policy at all: builtin defaults + everything degrades gracefully —
// no layer section, no bans beyond builtin, thresholds from defaults.
func TestBuildNoPolicy(t *testing.T) {
	cfg := testConfig(t, "version: 1\ngate:\n  fail_on: error\n")
	res := Build(cfg, Options{Package: "internal/engine", Lang: "go", PackageResolved: true})
	if strings.Contains(res.Rendered, "分层契约") {
		t.Errorf("empty arch policy → no layer section, got:\n%s", res.Rendered)
	}
	if strings.Contains(res.Rendered, "禁用 API/导入") {
		t.Errorf("no bans configured → no ban section, got:\n%s", res.Rendered)
	}
	if !strings.Contains(res.Rendered, "圈复杂度 ≤10") {
		t.Errorf("builtin thresholds should still render, got:\n%s", res.Rendered)
	}
	if !strings.Contains(res.Rendered, "流程红线") {
		t.Errorf("red-line section is static and must always render, got:\n%s", res.Rendered)
	}
}

// Unknown package: warning appended before rendering so the TEXT form
// carries it (JSON must not be the only auditable form), and the layer
// section notes the no-match instead of staying silent behind a global forbid.
func TestBuildUnknownPackage(t *testing.T) {
	cfg := testConfig(t, fullPolicy)
	res := Build(cfg, Options{Package: "internal/nope", Lang: "go", PackageResolved: false})
	if len(res.Warnings) == 0 {
		t.Fatal("unknown package must produce a warning")
	}
	if !strings.Contains(res.Rendered, "未被任何分层声明覆盖") {
		t.Errorf("layer no-match note missing, got:\n%s", res.Rendered)
	}
	if !strings.Contains(res.Rendered, "目标包 internal/nope 在仓库中不存在") {
		t.Errorf("warning must appear in the rendered text, got:\n%s", res.Rendered)
	}
}

// Unknown ban kinds are skipped, never a crash; repo-wide digests say so.
func TestBuildRepoWideAndUnknownKind(t *testing.T) {
	cfg := testConfig(t, `
version: 1
languages:
  go:
    banned-api:
      - match: "fmt.Print*"
      - match: "weird"
        kind: "telepathy"
`)
	res := Build(cfg, Options{Package: "", Lang: "go", PackageResolved: true})
	if strings.Contains(res.Rendered, "telepathy") {
		t.Errorf("unknown ban kind must be skipped, got:\n%s", res.Rendered)
	}
	if !strings.Contains(res.Rendered, "未指定目标包") {
		t.Errorf("repo-wide digest must warn about scope, got:\n%s", res.Rendered)
	}
	if !strings.Contains(res.Rendered, "禁止调用 fmt.Print*") {
		t.Errorf("known kinds still render, got:\n%s", res.Rendered)
	}
}

// Java packages arrive dot-normalised: the deps section and reuse matching
// must normalise slashes/dots consistently.
func TestBuildJavaDeps(t *testing.T) {
	cfg := testConfig(t, `
version: 1
dependencies:
  self: ["org.osc.scan.portal"]
  allow: ["org.springframework", "org.slf4j"]
languages:
  java:
    banned-api:
      - match: "System.out.print*"
`)
	res := Build(cfg, Options{Package: "org/osc/scan/portal/service", Lang: "java", PackageResolved: true})
	if !strings.Contains(res.Rendered, "本仓模块前缀：org.osc.scan.portal") {
		t.Errorf("deps self prefix missing, got:\n%s", res.Rendered)
	}
	if !strings.Contains(res.Rendered, "禁止调用 System.out.print*") {
		t.Errorf("java bans missing, got:\n%s", res.Rendered)
	}
}

// A ban whose except_files cover the whole target package is omitted
// (anti-FP-fatigue); it still shows for other packages.
func TestBuildBanExemption(t *testing.T) {
	cfg := testConfig(t, `
version: 1
languages:
  go:
    banned-api:
      - match: "testing.Log"
        except_files: ["_test.go"]
`)
	if res := Build(cfg, Options{Package: "internal/engine_test", Lang: "go", PackageResolved: true}); strings.Contains(res.Rendered, "testing.Log") {
		t.Errorf("fully-exempt package should not see the ban, got:\n%s", res.Rendered)
	}
	if res := Build(cfg, Options{Package: "internal/engine", Lang: "go", PackageResolved: true}); !strings.Contains(res.Rendered, "testing.Log") {
		t.Errorf("non-exempt package should see the ban, got:\n%s", res.Rendered)
	}
}

// Reuse symbols cap deterministically; symbol-name asc order.
func TestReuseCapAndOrder(t *testing.T) {
	cfg := testConfig(t, "version: 1\n")
	var defs []symbols.Def
	for _, n := range []string{"Zeta", "Alpha", "Mid"} {
		defs = append(defs, symbols.Def{Name: n, File: "p/x.go", Kind: "function", Visibility: "exported", Signature: "func " + n + "()", Package: "p"})
	}
	ix := symbols.Build(defs)
	res := Build(cfg, Options{Package: "p", Lang: "go", Index: ix, TargetPkg: "p", MaxSymbols: 2, PackageResolved: true})
	if got := res.Rendered; !strings.Contains(got, "Alpha") || !strings.Contains(got, "Mid") {
		t.Errorf("cap=2 should keep Alpha+Mid (name asc), got:\n%s", got)
	}
	if strings.Contains(res.Rendered, "Zeta") {
		t.Errorf("Zeta should be capped out, got:\n%s", res.Rendered)
	}
}

// MatchPackage: Go short package names resolve via the file directory; Java
// dotted packages match the slash target either direction.
func TestMatchPackage(t *testing.T) {
	cases := []struct {
		pkgField, file, target string
		want                   bool
	}{
		{"engine", "internal/engine/run.go", "internal/engine", true},
		{"engine", "internal/other/run.go", "internal/engine", false},
		{"", "internal/engine/run.go", "internal/engine", true}, // file dir fallback
		{"", "internal/other/run.go", "internal/engine", false}, // file dir fallback
		{"org.o.p", "", "org/o/p", true},                        // java dotted == slash target
		{"", "", "anything", false},                             // nothing to match with
		{"other", "", "internal/engine", false},
	}
	for _, c := range cases {
		if got := MatchPackage(c.pkgField, c.file, c.target); got != c.want {
			t.Errorf("MatchPackage(%q,%q,%q) = %v, want %v", c.pkgField, c.file, c.target, got, c.want)
		}
	}
}

// Byte-identical renders for identical inputs (R4 governance: a digest used
// in prompts must be reproducible).
func TestBuildDeterministic(t *testing.T) {
	cfg := testConfig(t, fullPolicy)
	ix := symbols.Build([]symbols.Def{
		{Name: "Run", File: "p/a.go", Kind: "function", Visibility: "exported", Signature: "func Run()", Package: "p"},
		{Name: "Aux", File: "p/b.go", Kind: "function", Visibility: "exported", Signature: "func Aux()", Package: "p"},
	})
	o := Options{Package: "p", Lang: "go", Index: ix, TargetPkg: "p", PackageResolved: true}
	if Build(cfg, o).Rendered != Build(cfg, o).Rendered {
		t.Error("identical inputs must render byte-identical digests")
	}
}

// The cross-layer idiom section renders only when the target package is
// covered by a layer declaration (an unlayered package has no cross-layer
// contract for the idiom to qualify), and it names both the port-adapter
// repair and the forbidden shortcut.
func TestBuildCrossLayerIdiom(t *testing.T) {
	cfg := testConfig(t, fullPolicy)

	res := Build(cfg, Options{Package: "internal/engine", Lang: "go", PackageResolved: true})
	assertDigestContains(t, res.Rendered, "■ 跨层惯用法", "idiom section for a layered package")
	assertDigestContains(t, res.Rendered, "port（interface）", "port-adapter repair named")
	assertDigestContains(t, res.Rendered, "composition root", "injection point named")
	assertDigestContains(t, res.Rendered, "禁止用复制代码的方式绕过分层", "wrong fix excluded")
	found := false
	for _, s := range res.Sections {
		if s.Name == "跨层惯用法" {
			found = true
			if len(s.Sources) == 0 || s.Sources[0] != "builtin:arch.port-adapter-idiom" {
				t.Errorf("idiom sources = %v, want builtin:arch.port-adapter-idiom", s.Sources)
			}
		}
	}
	if !found {
		t.Fatal("idiom section missing from Sections")
	}

	// A package no layer covers gets no idiom: nothing to qualify.
	un := Build(cfg, Options{Package: "some/elsewhere", Lang: "go", PackageResolved: true})
	assertDigestNotContains(t, un.Rendered, "跨层惯用法", "unlayered package has no idiom section")

	// Repo-wide digests have no target package to match either.
	repo := Build(cfg, Options{Lang: "go"})
	assertDigestNotContains(t, repo.Rendered, "跨层惯用法", "repo-wide digest has no idiom section")
}
