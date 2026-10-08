// Package extract turns non-standard constraint text — spec tables, incident
// reports, interview notes — into standardized rule candidates. It is the
// tool surface of conversion step T1b: deterministic (no LLM call, no
// network), a first-pass triage for a human reviewer, never a final verdict.
// Every candidate still goes through SOP steps 2-5: policy dedup, fixture
// smoke, FP audit, observation period.
package extract

import (
	"fmt"
	"regexp"
	"strings"

	"metricgate/internal/config"
)

// Clause is one normative statement pulled from non-standard input.
type Clause struct {
	ID   string
	Text string
}

// Candidate is the standardized shape of one clause.
type Candidate struct {
	ID         string   `json:"id"`
	Text       string   `json:"text"`
	Action     string   `json:"action"` // ban | required
	Kind       string   `json:"kind"`   // call | import | constructor | print | loop | catch
	Tier       string   `json:"tier"`   // ✅ 可落地 | ⚠️ 需人工补全 | ❌ 台账
	RuleID     string   `json:"ruleId"` // engine-folded banned-… id (empty for non-ban shapes)
	Match      string   `json:"match"`  // extracted match token (empty when not derivable)
	ArgsBan    []string `json:"argsBan,omitempty"`
	Existing   string   `json:"existing,omitempty"` // policy dedup hit: "call db.Query"
	Deviations []string `json:"deviations,omitempty"`
}

// Report aggregates one extraction run.
type Report struct {
	Candidates []Candidate `json:"candidates"`
	Notes      []string    `json:"notes,omitempty"`
}

var (
	tableRow  = regexp.MustCompile(`^\s*\|(.+)\|\s*$`)
	clauseID  = regexp.MustCompile(`^[A-Z]{2,6}-\d{2,3}$`)
	quotedArg = regexp.MustCompile(`"([^"]+)"|「([^」]+)」`)
	dotted    = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)+`)
)

var banVerbs = []string{"严禁", "禁止", "禁用", "不得", "不允许"}

// requiredMarks open the positive-requirement half of a clause ("禁止 X，
// 必须 Y"): the ban target lives before them, the mandated alternative
// after. Required-shaped clauses belong to the ledger, never to a ban.
var requiredMarks = []string{"必须", "应当", "建议", "推荐", "统一使用", "改用"}

// banSegment cuts the ban remainder at the first positive-requirement marker
// so the match target is extracted from the BANNED half only — pulling the
// target from the required half inverts the clause (it would ban the
// mandated API).
func banSegment(rest string) string {
	best := -1
	for _, m := range requiredMarks {
		if i := strings.Index(rest, m); i > 0 && (best < 0 || i < best) {
			best = i
		}
	}
	if best < 0 {
		return rest
	}
	return strings.TrimSpace(strings.TrimRight(rest[:best], "，；;、,. "))
}

// ClausesFromText splits non-standard input into clauses. Markdown table
// rows contribute their ID + text cells; any other non-empty line is one
// clause with an auto-generated U## id. Separator/header/comment lines are
// skipped.
func ClausesFromText(src string) []Clause {
	var out []Clause
	auto := 0
	inFence := false
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			inFence = !inFence // code examples are evidence, not clauses
			continue
		}
		if inFence || line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ">") {
			continue
		}
		if cells := tableCells(line); cells != nil {
			if cl, ok := clauseFromCells(cells, &auto); ok {
				out = append(out, cl)
			}
			continue
		}
		auto++
		out = append(out, Clause{ID: fmt.Sprintf("U%02d", auto), Text: line})
	}
	return out
}

// clauseFromCells maps one table row to a clause. The first cell is an ID
// when it matches the ID shape; the clause text is then the LONGEST remaining
// cell — real layouts interleave short level cells (编号|级别|条款), and the
// level column must not be mistaken for the clause body.
func clauseFromCells(cells []string, auto *int) (Clause, bool) {
	if len(cells) < 2 {
		return Clause{}, false
	}
	id, text := cells[0], cells[1]
	if clauseID.MatchString(id) {
		for _, c := range cells[1:] {
			if len([]rune(c)) > len([]rune(text)) {
				text = c
			}
		}
	} else {
		id, text = "", cells[0]
	}
	if !clauseWanted(text) {
		return Clause{}, false
	}
	if id == "" {
		*auto++
		id = fmt.Sprintf("U%02d", *auto)
	}
	return Clause{ID: id, Text: text}, true
}

var placeholderCells = map[string]bool{
	"条款": true, "clause": true, "编号": true, "id": true, "说明": true,
	"级别": true, "level": true, "优先级": true, "priority": true, "状态": true, "status": true,
}

// clauseWanted filters separator rows and placeholder header cells.
func clauseWanted(text string) bool {
	if text == "" || strings.HasPrefix(text, "-") || strings.HasPrefix(text, ":") {
		return false
	}
	return !placeholderCells[strings.ToLower(text)]
}

func tableCells(line string) []string {
	m := tableRow.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	parts := strings.Split(m[1], "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// Classify maps one clause to a candidate shape. Ban-shaped clauses with a
// derivable match token are ✅; ban clauses missing a token are ⚠️; required
// clauses go to the ❌ ledger (P0 lands ban-shaped rules only).
func Classify(cl Clause, lang string) Candidate {
	c := Candidate{ID: cl.ID, Text: cl.Text}
	rest, ok := banRest(cl.Text)
	if !ok {
		c.Action, c.Kind, c.Tier = "required", "unknown", "❌ 台账（P0 仅落禁令型条款）"
		c.Deviations = append(c.Deviations, "非禁令型条款：进缺口台账，等待人工处置或复评触发")
		return c
	}
	c.Action = "ban"
	rest = banSegment(rest)
	c.Kind = kindOf(cl.Text, rest)
	c.Match = targetOf(c.Kind, rest)
	if c.Match == "" && needsMatch(c.Kind) {
		c.Tier = "⚠️ 需人工补全 match"
		c.Deviations = append(c.Deviations, "未能从条款文本确定目标符号，人工补全后按 SOP 烟测")
		return c
	}
	c.Tier = "✅ 可落地"
	if c.Kind == "loop" || c.Kind == "catch" {
		c.Tier = "✅ 内置规则已覆盖"
	}
	c.RuleID = ruleIDFor(c.Kind, c.Match)
	if strings.Contains(rest, "仅当") || strings.Contains(rest, "参数为") {
		c.ArgsBan = quotedArgs(rest)
	}
	_ = lang
	return c
}

// banRest finds a ban verb and returns the remainder of the clause, with a
// trailing scope ("在 X 中/内") stripped. ok=false when the clause is not
// ban-shaped.
func banRest(text string) (string, bool) {
	idx, verb := -1, ""
	for _, v := range banVerbs {
		if i := strings.Index(text, v); i >= 0 && (idx < 0 || i < idx) {
			idx, verb = i, v
		}
	}
	if idx < 0 {
		return "", false
	}
	rest := strings.TrimSpace(text[idx+len(verb):])
	if strings.HasPrefix(rest, "在") {
		if i := strings.Index(rest, "中"); i > 0 {
			rest = strings.TrimSpace(rest[i+len("中"):])
		} else if i := strings.Index(rest, "内"); i > 0 {
			rest = strings.TrimSpace(rest[i+len("内"):])
		}
	}
	return rest, true
}

func containsAny(text string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// kindOf classifies the clause body. Order matters: import/constructor/
// catch are more specific shapes than a generic call. Loop position markers
// ("在循环内"…) live in the scope phrase that banRest strips, so they are
// checked against the FULL clause text; everything else reads the stripped
// remainder.
func kindOf(full, rest string) string {
	switch {
	case containsAny(rest, "导入", "引入", "import", "依赖"):
		return "import"
	case strings.Contains(rest, "new "), strings.Contains(rest, "实例化"):
		return "constructor"
	case containsAny(rest, "catch", "捕获", "吞异常", "吞掉"):
		return "catch"
	case containsAny(rest, "print", "打印", "输出"):
		return "print"
	case containsAny(full, "循环内", "循环中", "循环里", "在循环", "每轮"):
		// Loop-class clauses carry a POSITION marker (the bad pattern is the
		// call inside a loop). Bare "sleep"/"轮询" in the clause body name the
		// call target itself — those are plain call bans, not loop rules.
		return "loop"
	default:
		return "call"
	}
}

func needsMatch(kind string) bool {
	return kind == "call" || kind == "import" || kind == "constructor" || kind == "print"
}

var symbolTok = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*\*?$`)

// symbolToken keeps only identifier-shaped tokens: a match target for the
// engine must be a symbol, not prose. Returns "" for CJK/descriptive text.
func symbolToken(s string) string {
	if symbolTok.MatchString(s) {
		return s
	}
	return ""
}

// targetOf extracts the match token for matchable kinds. Loop/catch clauses
// need no match (built-in rules carry them).
func targetOf(kind, text string) string {
	switch kind {
	case "import":
		if m := dottedToken(text); m != "" {
			return m
		}
		return symbolToken(afterKeyword(text, "导入", "引入"))
	case "constructor":
		if i := strings.Index(text, "new "); i >= 0 {
			return symbolToken(firstToken(text[i+len("new "):]))
		}
		return symbolToken(afterKeyword(text, "实例化"))
	case "call", "print":
		if m := dottedToken(text); m != "" {
			return m
		}
		if tok := symbolToken(afterKeyword(text, "调用")); tok != "" {
			return tok
		}
		// “不允许使用 X” is the most common colloquial ban form.
		return symbolToken(afterKeyword(text, "使用"))
	default:
		return ""
	}
}

// dottedToken returns the first dotted (or trailing-*) qualified token,
// e.g. db.Query, org.apache.log4j, Executors.new*.
func dottedToken(text string) string {
	m := dotted.FindStringIndex(text)
	if m == nil {
		return ""
	}
	tok := text[m[0]:m[1]]
	if m[1] < len(text) && text[m[1]] == '*' {
		tok += "*"
	}
	return tok
}

// afterKeyword returns the first token following any of the keywords.
func afterKeyword(text string, keywords ...string) string {
	for _, kw := range keywords {
		i := strings.Index(text, kw)
		if i < 0 {
			continue
		}
		rest := strings.TrimLeft(strings.TrimSpace(text[i+len(kw):]), " ：:、")
		if rest == "" {
			return ""
		}
		return firstToken(rest)
	}
	return ""
}

// firstToken cuts the string at the first delimiter.
func firstToken(s string) string {
	s = strings.TrimPrefix(s, "new ")
	for _, d := range []string{" ", "（", "(", "，", ",", "。", "；", ";", "\t"} {
		if i := strings.Index(s, d); i >= 0 {
			s = s[:i]
		}
	}
	return s
}

// ruleIDFor folds dots and slashes to dashes exactly like the engine does,
// so severity-override keys in the generated snippet line up with rule IDs.
func ruleIDFor(kind, match string) string {
	if match == "" || (kind != "call" && kind != "import" && kind != "constructor" && kind != "print") {
		return ""
	}
	id := strings.NewReplacer(".", "-", "/", "-").Replace(match)
	return "banned-" + id
}

// quotedArgs collects quoted literal arguments, used only when the clause
// text itself gates on parameters ("仅当"/"参数为"), mirroring the args_ban
// primitive.
func quotedArgs(text string) []string {
	var out []string
	for _, m := range quotedArg.FindAllStringSubmatch(text, -1) {
		switch {
		case m[1] != "":
			out = append(out, m[1])
		case m[2] != "":
			out = append(out, m[2])
		}
	}
	return out
}

// Build runs the full pass: split, classify, dedup against the policy's
// existing ban list for the language.
func Build(src string, lang string, bans []config.BanConfig) Report {
	var rep Report
	for _, cl := range ClausesFromText(src) {
		c := Classify(cl, lang)
		if c.Action == "ban" && c.Match != "" {
			if ex := dedupBan(c, bans); ex != "" {
				c.Existing = ex
				c.Deviations = append(c.Deviations, "与现有 policy 同型禁令重复（查重命中），复核后跳过或去重")
			}
		}
		rep.Candidates = append(rep.Candidates, c)
	}
	rep.Notes = []string{
		"判型是人工复核前的第一道分诊，不是终审：每条候选仍需走 SOP 第 2-5 步（查重/烟测/FP 审计/观察期）",
	}
	return rep
}

func dedupBan(c Candidate, bans []config.BanConfig) string {
	for _, b := range bans {
		if b.Match == c.Match && sameKind(b.Kind, c.Kind) {
			return fmt.Sprintf("%s %s", kindLabel(b.Kind), b.Match)
		}
	}
	return ""
}

// sameKind normalizes the policy kind (empty = call) against the candidate.
func sameKind(policyKind, candKind string) bool {
	if policyKind == "" {
		policyKind = "call"
	}
	if candKind == "print" {
		candKind = "call"
	}
	return policyKind == candKind
}

func kindLabel(k string) string {
	if k == "" {
		return "call"
	}
	return k
}

// Snippet renders the candidate as a policy YAML fragment (or a pointer to
// the built-in rule that carries it).
func Snippet(c Candidate) string {
	switch c.Kind {
	case "import":
		return fmt.Sprintf("- kind: import\n  match: %s", c.Match)
	case "constructor":
		return fmt.Sprintf("- kind: constructor\n  match: %s", c.Match)
	case "call", "print":
		s := fmt.Sprintf("- kind: call\n  match: %s", c.Match)
		if len(c.ArgsBan) > 0 {
			s += fmt.Sprintf("\n  args_ban: [%s]", strings.Join(c.ArgsBan, ", "))
		}
		return s
	case "loop":
		return "# 由内置规则承载：nesting-depth 阈值 / 循环类提示条款"
	case "catch":
		return "# 由内置规则承载：silent-catch（观察期）+ empty-block"
	default:
		return ""
	}
}
