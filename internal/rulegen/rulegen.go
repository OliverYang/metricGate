// Package rulegen implements the rule-integration interface: an LLM (or any
// external agent) describes one ban rule as a JSON spec, the engine compiles
// it and verifies it against caller-provided fixture files, and the verified
// artifact is returned ready to merge into .metricgate.yaml.
//
// The integration contract is data-in/data-out — the caller never reads
// metricgate source. Every stage is a file (spec, fixture, policy, report),
// so each node of the flow can be inspected, edited and re-run, or rolled
// back by keeping the previous file.
//
//	metricgate constraints rule -spec rule.json -bad bad.java -good good.java
package rulegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"metricgate/internal/config"
	"metricgate/internal/engine"
	"metricgate/internal/model"
	"metricgate/internal/parser"
)

// Spec is the input contract: one ban rule described in policy vocabulary.
// The JSON keys mirror .metricgate.yaml field names on purpose — an LLM that can
// read the policy vocabulary can emit a valid spec.
type Spec struct {
	Lang        string   `json:"lang"`                   // any registered language id (go, java, python, typescript, csharp, rust, ...)
	Kind        string   `json:"kind"`                   // call | import | constructor (not every language can express every kind)
	Match       string   `json:"match"`                  // ban pattern; trailing * = prefix match
	ArgsBan     []string `json:"args_ban,omitempty"`     // fire only when a string-literal argument equals one of these
	Reason      string   `json:"reason,omitempty"`       // why it is banned (rendered in findings + digests)
	Use         string   `json:"use,omitempty"`          // compliant alternative (rendered in findings + digests)
	Severity    string   `json:"severity,omitempty"`     // "" = warning (observation period); error promotes to red line
	ExceptFiles []string `json:"except_files,omitempty"` // file-name suffixes exempt from the ban
}

// FixtureInput is one caller-provided fixture file.
type FixtureInput struct {
	Name    string // label only; the on-disk name is derived
	Content string // full file content; Go snippets without a package clause are wrapped automatically
}

// Hit is one finding of the rule under test, projected for the report.
type Hit struct {
	RuleID   string `json:"rule_id"`
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

// FixtureCase is one fixture node: the input echo plus its verdict.
type FixtureCase struct {
	Name    string `json:"name"`
	File    string `json:"file"`
	Expect  string `json:"expect"`  // hit | clean
	Verdict string `json:"verdict"` // pass | fail
	Hits    []Hit  `json:"hits"`
}

// VerifyResult is the output contract: every node is inspectable and the
// merge-ready artifact is included, so a green run hands back exactly what a
// human (or a subsequent automated step) merges into .metricgate.yaml.
type VerifyResult struct {
	Pass         bool          `json:"pass"` // every fixture verdict pass AND compiled
	RuleID       string        `json:"rule_id"`
	Existing     bool          `json:"existing"` // same kind+match ban already in the base policy
	Spec         *Spec         `json:"spec"`
	YAML         string        `json:"yaml"`        // merge-ready entry fragment
	PolicyYAML   string        `json:"policy_yaml"` // the exact fixture policy that was verified
	Fixtures     []FixtureCase `json:"fixtures"`
	Compiled     bool          `json:"compiled"`
	CompileError string        `json:"compile_error,omitempty"`
	Errors       int           `json:"errors"`
	Warnings     int           `json:"warnings"`
	Notes        []string      `json:"notes,omitempty"`
}

// allowedKinds is the ban vocabulary each language can express. It mirrors
// the engine's ban builders: a kind listed here has a query that can see it,
// and a kind absent here would compile into a rule that can never match.
var allowedKinds = map[string][]string{
	"go":         {"call", "import"},
	"java":       {"call", "import", "constructor"},
	"typescript": {"call", "import", "constructor"},
	"tsx":        {"call", "import", "constructor"},
	"javascript": {"call", "import", "constructor"},
	"python":     {"call", "import"},
	"c":          {"call", "import"},
	"cpp":        {"call", "import", "constructor"},
	"csharp":     {"call", "import", "constructor"},
	"rust":       {"call", "import"},
	"sql":        {"call", "import"},
	"verilog":    {"call", "import"},
	"vbnet":      {"call", "import", "constructor"},
}

// langAliases maps the spellings a spec may use onto the engine's language id,
// so a caller writing "ts" or "c#" gets the right section instead of a
// rejection. The canonical ids are the ones in allowedKinds.
var langAliases = map[string]string{
	"ts":     "typescript",
	"tsx":    "tsx",
	"js":     "javascript",
	"py":     "python",
	"c++":    "cpp",
	"c#":     "csharp",
	"cs":     "csharp",
	"rs":     "rust",
	"golang": "go",
}

// CanonicalLang resolves a spec's language spelling to the engine's id.
func CanonicalLang(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	if alias, ok := langAliases[l]; ok {
		return alias
	}
	return l
}

// RuleID derives the stable finding id of the ban the same way the engine
// does: "banned-" plus the pattern with dots and slashes folded to dashes, a
// trailing "*" kept so prefix families stay configurable apart.
func RuleID(match string) string {
	m := strings.NewReplacer(".", "-", "/", "-").Replace(match)
	return "banned-" + m
}

// Validate checks the spec against the expressible vocabulary. Everything it
// rejects belongs on the gap ledger, not in a policy.
func Validate(s *Spec) error {
	var probs []string
	if _, ok := allowedKinds[s.Lang]; !ok {
		probs = append(probs, fmt.Sprintf("lang %q 不支持（可用 %s）",
			s.Lang, strings.Join(supportedLangNames(), "/")))
	} else if !kindAllowed(s.Lang, s.Kind) {
		probs = append(probs, fmt.Sprintf("kind %q 在 %s 上不支持（可用 %s；Go 工厂函数走 call）",
			s.Kind, s.Lang, strings.Join(allowedKinds[s.Lang], "/")))
	}
	if strings.TrimSpace(s.Match) == "" {
		probs = append(probs, "match 不能为空（禁令的匹配模式）")
	}
	switch s.Severity {
	case "", "warning", "error", "info":
	default:
		probs = append(probs, fmt.Sprintf("severity %q 不支持（可用 warning/error/info，缺省 warning 观察期）", s.Severity))
	}
	if len(probs) == 0 {
		return nil
	}
	return errors.New("spec 校验失败: " + strings.Join(probs, "; "))
}

// supportedLangNames lists the accepted language ids in a stable order for
// error messages.
func supportedLangNames() []string {
	names := make([]string, 0, len(allowedKinds))
	for _, id := range parser.Supported() {
		if _, ok := allowedKinds[string(id)]; ok {
			names = append(names, string(id))
		}
	}
	return names
}

func kindAllowed(lang, kind string) bool {
	for _, k := range allowedKinds[lang] {
		if k == kind {
			return true
		}
	}
	return false
}

// Verify runs the full pipeline: parse + validate the spec, stage fixtures
// and the minimal policy into one temp root, run the real engine over it,
// and grade every fixture case by whether the rule under test behaved as
// specified. Everything the run touched stays inspectable on the result.
func Verify(specBytes []byte, bad, good []FixtureInput, base *config.Config) (*VerifyResult, error) {
	s, err := parseSpec(specBytes)
	if err != nil {
		return nil, err
	}
	if len(bad) == 0 {
		return nil, errors.New("verify 至少需要一个 bad fixture（已知必命中的违规样例）——没有反例的规则不可信")
	}

	res := &VerifyResult{RuleID: RuleID(s.Match), Spec: &s}
	res.YAML = entryYAML(&s)
	res.PolicyYAML = policyYAML(&s)
	noteExisting(res, base, s)
	if err := verifyOnDisk(res, s, bad, good); err != nil {
		res.CompileError = err.Error()
	}
	res.Pass = res.Compiled && allPassed(res.Fixtures)
	return res, nil
}

// noteExisting records that the base policy already carries a ban of the same
// kind and match. The verdict still runs — a duplicate is a warning for the
// reviewer, not a rejection.
func noteExisting(res *VerifyResult, base *config.Config, s Spec) {
	if base == nil || !dedupHit(base, &s) {
		return
	}
	res.Existing = true
	res.Notes = append(res.Notes,
		fmt.Sprintf("base policy 已存在同 kind+match 禁令（%s/%s）——查重命中，合并前先确认语义是否叠加", s.Kind, s.Match))
}

// parseSpec decodes and validates one rule spec. A caller may spell the
// language the way its own ecosystem does ("ts", "c#", "py"), so the id is
// normalised before anything reads it: the policy section and the fixture
// extension must always use the engine's id.
func parseSpec(specBytes []byte) (Spec, error) {
	if !json.Valid(specBytes) {
		return Spec{}, errors.New("spec 不是合法 JSON")
	}
	var s Spec
	if err := json.Unmarshal(specBytes, &s); err != nil {
		return Spec{}, fmt.Errorf("spec: %w", err)
	}
	s.Lang = CanonicalLang(s.Lang)
	if err := Validate(&s); err != nil {
		return Spec{}, err
	}
	return s, nil
}

func allPassed(fcs []FixtureCase) bool {
	for _, fc := range fcs {
		if fc.Verdict != "pass" {
			return false
		}
	}
	return true
}

// verifyOnDisk stages the whole verification into one temp root (fixtures +
// policy), runs the engine, and fills verdicts, compile state and counts.
func verifyOnDisk(res *VerifyResult, s Spec, bad, good []FixtureInput) error {
	root, err := os.MkdirTemp("", "rulegen-verify-")
	if err != nil {
		return fmt.Errorf("创建验证目录失败: %w", err)
	}
	defer os.RemoveAll(root)

	res.stageFixtures(root, s, bad, "hit")
	res.stageFixtures(root, s, good, "clean")

	policyPath := filepath.Join(root, ".metricgate.yaml")
	if err := os.WriteFile(policyPath, []byte(res.PolicyYAML), 0o644); err != nil {
		return fmt.Errorf("写入 fixture policy 失败: %w", err)
	}
	cfg, err := config.LoadFile(policyPath)
	if err != nil {
		return fmt.Errorf("fixture policy 解析失败: %w", err)
	}
	eng, err := engine.New(cfg)
	if err != nil {
		return fmt.Errorf("引擎初始化失败: %w", err)
	}
	run, err := eng.RunDetailed(root)
	if err != nil {
		return fmt.Errorf("fixture 运行失败: %w", err)
	}
	res.Compiled = len(run.Summary.ParseErrors) == 0
	if !res.Compiled {
		return fmt.Errorf("fixture 解析失败 %d 处（语法错误的样例会吞掉命中，先修 fixture）: %s",
			len(run.Summary.ParseErrors), strings.Join(run.Summary.ParseErrors, "; "))
	}
	res.Errors = run.Summary.Count(model.SevError)
	res.Warnings = run.Summary.Count(model.SevWarning)
	gradeFixtures(res, run)
	return nil
}

// stageFixtures writes one expect-class of fixture inputs into the run root
// and appends their case nodes, carrying any staging notes onto the result.
func (res *VerifyResult) stageFixtures(root string, s Spec, ins []FixtureInput, expect string) {
	ext := parser.Extension(parser.LanguageID(s.Lang))
	for i, in := range ins {
		fc, note := stageFixture(root, in, fmt.Sprintf("%s-%d%s", expect, i+1, ext), expect, s.Lang)
		res.Fixtures = append(res.Fixtures, fc)
		if note != "" {
			res.Notes = append(res.Notes, fc.File+": "+note)
		}
	}
}

// stageFixture writes one fixture file into the run root and returns its
// case node. Go snippets without a package clause are wrapped so a bare
// LLM-produced body still parses.
func stageFixture(root string, in FixtureInput, file, expect, lang string) (FixtureCase, string) {
	content := in.Content
	note := ""
	if lang == "go" && !strings.HasPrefix(strings.TrimSpace(content), "package ") {
		content = "package fixtures\n\n" + content
		note = "Go 片段缺 package 声明，已自动补 package fixtures"
	}
	if err := os.WriteFile(filepath.Join(root, file), []byte(content), 0o644); err != nil {
		note = fmt.Sprintf("写入失败: %v", err)
	}
	return FixtureCase{Name: in.Name, File: file, Expect: expect}, note
}

// gradeFixtures projects the run's findings onto the fixture cases and
// grades each one. Hits from OTHER rules are ignored on purpose: the verdict
// asks only whether the rule under test behaves as specified.
func gradeFixtures(res *VerifyResult, run *engine.RunResult) {
	for i := range res.Fixtures {
		fc := &res.Fixtures[i]
		for _, f := range run.Summary.Findings {
			// the engine reports absolute paths when the run root is
			// absolute; fixture cases are keyed by base name.
			if f.RuleID != res.RuleID || filepath.Base(f.File) != fc.File {
				continue
			}
			fc.Hits = append(fc.Hits, Hit{RuleID: f.RuleID, Severity: string(f.Severity), File: f.File, Line: f.StartLine})
		}
		switch {
		case fc.Expect == "hit" && len(fc.Hits) > 0:
			fc.Verdict = "pass"
		case fc.Expect == "hit":
			fc.Verdict = "fail"
			res.Notes = append(res.Notes, fmt.Sprintf("%s: 期望命中但零命中——反例没打到禁令，或 match 写法与预期不符", fc.File))
		case fc.Expect == "clean" && len(fc.Hits) == 0:
			fc.Verdict = "pass"
		default:
			fc.Verdict = "fail"
			res.Notes = append(res.Notes, fmt.Sprintf("%s: 期望干净但命中——正向样例踩线，或 match 过宽（考虑 args_ban/except_files 收窄）", fc.File))
		}
	}
}

// dedupHit reports whether the base policy already carries a ban with the
// same kind + match for the spec's language.
func dedupHit(base *config.Config, s *Spec) bool {
	kind := s.Kind
	if kind == "" {
		kind = "call"
	}
	for _, b := range base.Bans(s.Lang) {
		k := b.Kind
		if k == "" {
			k = "call"
		}
		if k == kind && b.Match == s.Match {
			return true
		}
	}
	return false
}
