package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"metricgate/internal/config"
	"metricgate/internal/model"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Exclude = nil // include testdata/
	return cfg
}

func countsByLang(s *model.RunSummary) (goCounts, javaCounts map[string]int) {
	goCounts, javaCounts = map[string]int{}, map[string]int{}
	for _, f := range s.Findings {
		if f.Source != model.SrcRule {
			continue
		}
		if strings.HasSuffix(f.File, ".go") {
			goCounts[f.RuleID]++
		} else {
			javaCounts[f.RuleID]++
		}
	}
	return goCounts, javaCounts
}

func checkCounts(t *testing.T, want, got map[string]int, label string) {
	t.Helper()
	for id, n := range want {
		if got[id] != n {
			t.Errorf("%s %s = %d, want %d (all: %v)", label, id, got[id], n, got)
		}
	}
}

func TestRunOnTestdata(t *testing.T) {
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	s, err := eng.Run("testdata")
	if err != nil {
		t.Fatal(err)
	}
	// sample.go + java/Sample.java + the arch fixture (3) + the archsup
	// fixture (2) + the clone fixtures (2) + the clone-suppression fixture
	// (2) + the CK fixture (1) + the stub fixture (1)
	if s.Files != 13 {
		t.Fatalf("files = %d, want 13", s.Files)
	}
	if n := len(s.ParseErrors); n != 0 {
		t.Fatalf("unexpected parse errors: %v", s.ParseErrors)
	}
	if s.Count(model.SevError) != 0 {
		t.Errorf("errors = %d, want 0", s.Count(model.SevError))
	}

	goCounts, javaCounts := countsByLang(s)
	checkCounts(t, map[string]int{
		"banned-fmt-Print*": 1,
		"empty-block":       2,
		"banned-panic":      2, // sample.go and the stub fixture
		"function-clone":    2, // the fixture clone class minus anchor and suppression
	}, goCounts, "go")
	checkCounts(t, map[string]int{
		"banned-System-out-print*": 1,
		"print-stack-trace":        1,
		"empty-block":              2,
	}, javaCounts, "java")

	var paramsFinding bool
	for _, f := range s.Findings {
		if f.RuleID == "params-count" && strings.Contains(f.Message, "many") {
			paramsFinding = true
		}
	}
	if !paramsFinding {
		t.Error("expected params-count finding for Sample.many")
	}
}

func TestTodoRuleToggle(t *testing.T) {
	cfg := testConfig(t)
	enabled := true
	cfg.Languages["go"] = config.LanguageConfig{
		Rules: map[string]config.RuleConfig{
			"todo-comment": {Enabled: &enabled},
		},
	}
	cfg.Languages["java"] = config.LanguageConfig{
		Rules: map[string]config.RuleConfig{
			"todo-comment": {Enabled: &enabled},
		},
	}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	s, err := eng.Run("testdata")
	if err != nil {
		t.Fatal(err)
	}
	goCounts, javaCounts := countsByLang(s)
	if goCounts["todo-comment"] != 1 {
		t.Errorf("go todo-comment = %d, want 1", goCounts["todo-comment"])
	}
	if javaCounts["todo-comment"] != 1 {
		t.Errorf("java todo-comment = %d, want 1", javaCounts["todo-comment"])
	}
}

// TestCheckSourceAnchorsFindings proves every finding carries an anchor and
// that a rule match on an AST node yields an exact byte span + snippet hash,
// while a file-level metric is honestly line_only.
func TestCheckSourceAnchorsFindings(t *testing.T) {
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	src := "package x\n\nfunc F() {\n\tif true {\n\t}\n}\n"
	findings, err := eng.CheckSource("a.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	empty := findingByRule(findings, "empty-block")
	if empty == nil {
		t.Fatalf("no empty-block finding: %+v", findings)
	}
	assertExactAnchor(t, src, empty)
	if metric := findingByRule(findings, "cyclomatic-complexity"); metric != nil {
		assertLineOnlyAnchor(t, metric)
	}
	for _, f := range findings {
		if f.Evidence == nil {
			t.Errorf("finding %s has no anchor", f.RuleID)
		}
	}
}

func findingByRule(findings []model.Finding, ruleID string) *model.Finding {
	for i := range findings {
		if findings[i].RuleID == ruleID {
			return &findings[i]
		}
	}
	return nil
}

func assertExactAnchor(t *testing.T, src string, f *model.Finding) {
	t.Helper()
	if f.Evidence == nil || f.Evidence.Anchor != model.AnchorExact {
		t.Fatalf("anchor = %+v, want exact", f.Evidence)
	}
	span := src[f.Evidence.StartByte:f.Evidence.EndByte]
	if span != "{\n\t}" && span != "{}" {
		t.Errorf("anchor span = %q, want the empty block", span)
	}
	if !strings.HasPrefix(f.Evidence.SnippetHash, "sha256:") {
		t.Errorf("snippet hash = %q", f.Evidence.SnippetHash)
	}
}

func assertLineOnlyAnchor(t *testing.T, f *model.Finding) {
	t.Helper()
	if f.Evidence == nil || f.Evidence.Anchor != model.AnchorLineOnly {
		t.Errorf("metric anchor = %+v, want line_only", f.Evidence)
	}
}

// TestEvidenceHashChangesWhenSpanEdited proves the anchor is revalidation:
// editing the span changes the hash, so a host agent can detect a stale anchor.
func TestEvidenceHashChangesWhenSpanEdited(t *testing.T) {
	first := model.NewEvidence([]byte("if x {\n}\n"), 0, 6)
	second := model.NewEvidence([]byte("if y {\n}\n"), 0, 6)
	if first.SnippetHash == second.SnippetHash {
		t.Errorf("hash did not change when the span changed: %s", first.SnippetHash)
	}
	if first.Anchor != model.AnchorExact {
		t.Errorf("anchor = %q, want exact", first.Anchor)
	}
}

// TestNewEvidenceDegradesOutOfRangeSpan keeps the anchor honest: a bad span
// must not claim an exact location.
func TestNewEvidenceDegradesOutOfRangeSpan(t *testing.T) {
	got := model.NewEvidence([]byte("short"), 0, 999)
	if got.Anchor != model.AnchorLineOnly {
		t.Errorf("out-of-range anchor = %q, want line_only", got.Anchor)
	}
	if got.SnippetHash != "" {
		t.Errorf("out-of-range span must not carry a hash: %q", got.SnippetHash)
	}
}

// TestEvidenceByteOffsetsSurviveCRLFAndNonASCII closes the D4 acceptance line
// "Go/Java CRLF/non-ASCII anchor revalidation": tree-sitter byte offsets are
// byte-based, so the reported span must slice the same raw bytes — and the
// hash must match — even with CRLF line endings or multibyte content before
// the block that shifts every offset.
func TestEvidenceByteOffsetsSurviveCRLFAndNonASCII(t *testing.T) {
	cases := map[string]string{
		"crlf.go": "package x\r\n\r\nfunc F() {\r\n\tif true {\r\n\t}\r\n}\r\n",
		"uni.go":  "package x\n\n// \u4e2d\u6587\u6ce8\u91ca \u00e9\u00e8\u00ea\nfunc F() {\n\tif true {\n\t}\n}\n",
	}
	eng, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	for name, src := range cases {
		findings, err := eng.CheckSource(name, []byte(src))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		f := findingByRule(findings, "empty-block")
		if f == nil || f.Evidence == nil || f.Evidence.Anchor != model.AnchorExact {
			t.Fatalf("%s: no exact empty-block anchor: %+v", name, f)
		}
		raw := []byte(src)
		span := raw[f.Evidence.StartByte:f.Evidence.EndByte]
		sum := sha256.Sum256(span)
		if got := "sha256:" + hex.EncodeToString(sum[:]); got != f.Evidence.SnippetHash {
			t.Errorf("%s: hash mismatch: span=%q got=%s want=%s", name, span, got, f.Evidence.SnippetHash)
		}
	}
}
