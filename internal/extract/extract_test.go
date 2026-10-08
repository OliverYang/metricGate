package extract

import (
	"strings"
	"testing"

	"metricgate/internal/config"
)

func TestClausesFromTextTableAndFree(t *testing.T) {
	src := "" +
		"| 编号 | 条款 |\n" +
		"|---|---|\n" +
		"| SEC-01 | 严禁调用 MessageDigest.getInstance 处理密码 |\n" +
		"| CONC-02 | 禁止导入 java.text.SimpleDateFormat |\n" +
		"自由文本：禁止在循环内调用 Thread.sleep\n"
	got := ClausesFromText(src)
	if len(got) != 3 {
		t.Fatalf("clauses = %d, want 3: %+v", len(got), got)
	}
	if got[0].ID != "SEC-01" || got[1].ID != "CONC-02" {
		t.Errorf("ids = %s, %s", got[0].ID, got[1].ID)
	}
	if got[2].ID != "U01" || !strings.Contains(got[2].Text, "Thread.sleep") {
		t.Errorf("free text clause = %+v", got[2])
	}
}

func TestClassifyCallBan(t *testing.T) {
	c := Classify(Clause{ID: "SEC-01", Text: "严禁调用 MessageDigest.getInstance 处理密码摘要"}, "java")
	if c.Tier != "✅ 可落地" || c.Kind != "call" || c.Match != "MessageDigest.getInstance" {
		t.Fatalf("call classify = %+v", c)
	}
	if c.RuleID != "banned-MessageDigest-getInstance" {
		t.Errorf("ruleID = %q", c.RuleID)
	}
}

func TestClassifyImportAndConstructor(t *testing.T) {
	imp := Classify(Clause{ID: "C1", Text: "禁止导入 org.apache.log4j（含子包）"}, "java")
	if imp.Kind != "import" || imp.Match != "org.apache.log4j" || imp.RuleID != "banned-org-apache-log4j" {
		t.Fatalf("import classify = %+v", imp)
	}
	ctor := Classify(Clause{ID: "C2", Text: "禁止 new SimpleDateFormat，用 DateTimeFormatter"}, "java")
	if ctor.Kind != "constructor" || ctor.Match != "SimpleDateFormat" || ctor.RuleID != "banned-SimpleDateFormat" {
		t.Fatalf("constructor classify = %+v", ctor)
	}
}

func TestClassifyArgsBan(t *testing.T) {
	c := Classify(Clause{ID: "S1", Text: "禁止调用 MessageDigest.getInstance，仅当参数为 \"MD5\" 时报错"}, "java")
	if c.Tier != "✅ 可落地" || len(c.ArgsBan) != 1 || c.ArgsBan[0] != "MD5" {
		t.Fatalf("args classify = %+v", c)
	}
}

func TestClassifyBuiltinCarried(t *testing.T) {
	loop := Classify(Clause{ID: "P1", Text: "禁止在循环内调用 Thread.sleep 轮询"}, "go")
	if loop.Tier != "✅ 内置规则已覆盖" || loop.RuleID != "" {
		t.Fatalf("loop classify = %+v", loop)
	}
}

func TestClassifyMissingMatchAndRequired(t *testing.T) {
	noMatch := Classify(Clause{ID: "X1", Text: "禁止调用不安全的序列化方法"}, "go")
	if noMatch.Tier != "⚠️ 需人工补全 match" {
		t.Fatalf("no-match classify = %+v", noMatch)
	}
	req := Classify(Clause{ID: "X2", Text: "所有对外接口必须记录访问日志"}, "go")
	if req.Action != "required" || !strings.Contains(req.Tier, "台账") {
		t.Fatalf("required classify = %+v", req)
	}
}

func TestBuildDedupAgainstPolicy(t *testing.T) {
	bans := []config.BanConfig{{Kind: "call", Match: "db.Query"}, {Kind: "import", Match: "org.apache.log4j"}}
	rep := Build("禁止导入 org.apache.log4j\n禁止调用 db.Query 读写数据\n禁止调用 fmt.Println 调试输出\n", "go", bans)
	if len(rep.Candidates) != 3 {
		t.Fatalf("candidates = %d", len(rep.Candidates))
	}
	if rep.Candidates[0].Existing == "" || !strings.Contains(rep.Candidates[0].Existing, "org.apache.log4j") {
		t.Errorf("import dedup missed: %+v", rep.Candidates[0])
	}
	if rep.Candidates[1].Existing == "" || !strings.Contains(rep.Candidates[1].Existing, "db.Query") {
		t.Errorf("call dedup missed: %+v", rep.Candidates[1])
	}
	if rep.Candidates[2].Existing != "" {
		t.Errorf("fresh clause flagged as existing: %+v", rep.Candidates[2])
	}
}

func TestSnippetForms(t *testing.T) {
	call := Snippet(Candidate{Kind: "call", Match: "db.Query", ArgsBan: []string{"MD5"}})
	if !strings.Contains(call, "kind: call") || !strings.Contains(call, "args_ban: [MD5]") {
		t.Errorf("call snippet = %q", call)
	}
	if s := Snippet(Candidate{Kind: "catch"}); !strings.Contains(s, "silent-catch") {
		t.Errorf("catch snippet = %q", s)
	}
}

func TestScopeStripping(t *testing.T) {
	c := Classify(Clause{ID: "B1", Text: "在 service 层中禁止调用 db.Exec"}, "go")
	if c.Match != "db.Exec" {
		t.Fatalf("scope strip failed: %+v", c)
	}
}
