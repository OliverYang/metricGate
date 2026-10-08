// Package feed is the structured-source compatibility layer of the
// conversion pipeline: machine-authored advisories (OSV supply-chain alerts
// today) are translated into the same banned-import vocabulary the gate,
// digest and status surfaces already speak. Policy is data — once an entry
// lands, check / digest / status all follow with zero engine changes.
//
// Honest boundaries (see docs/constraints/supply-chain-feed.md): source
// imports carry no versions (the ban is a tripwire, version ranges belong to
// dependency-tree tools), transitive dependencies are invisible at source
// level, and Go code cannot compile against a module that is not in go.mod —
// the tripwire's value is the inventory scan, the pre-merge block, and
// grounding the generator before it writes the import.
package feed

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Vuln is one advisory distilled to what a policy ban needs. One OSV vuln
// affecting packages in several ecosystems expands to one Vuln per supported
// (ecosystem, package) pair.
type Vuln struct {
	ID         string `json:"id"`
	CVE        string `json:"cve,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Package    string `json:"package"`   // raw OSV package name (group:artifact | module path)
	Ecosystem  string `json:"ecosystem"` // raw OSV ecosystem
	Introduced string `json:"introduced,omitempty"`
	Fixed      string `json:"fixed,omitempty"`
	Match      string `json:"match"` // derived import-ban match (empty when unsupported)
	Lang       string `json:"lang"`  // derived policy language ("" = unsupported ecosystem)
}

// Result separates what a feed run would change: Added are new bans,
// Existing deduped against the live policy, Skipped carry an unsupported
// ecosystem and land in the ledger, never in the gate.
type Result struct {
	Added    []Vuln `json:"added"`
	Existing []Vuln `json:"existing,omitempty"`
	Skipped  []Vuln `json:"skipped,omitempty"`
}

// BanRow is one rendered policy entry, decoupled from internal/config.
type BanRow struct {
	Kind   string
	Match  string
	Reason string
	Use    string
}

// LangGroup groups rows per policy language section for merging.
type LangGroup struct {
	Lang string
	Rows []BanRow
}

// osv is the minimal OSV JSON schema slice (https://api.osv.dev). Both the
// list shape {"vulns":[...]} and a single bare vuln object are accepted —
// GET /v1/vulns/<id> returns the latter, query endpoints the former.
type osvDoc struct {
	Vulns []osvVuln `json:"vulns"`
}
type osvVuln struct {
	ID       string        `json:"id"`
	Aliases  []string      `json:"aliases"`
	Summary  string        `json:"summary"`
	Affected []osvAffected `json:"affected"`
}
type osvAffected struct {
	Package osvPackage `json:"package"`
	Ranges  []osvRange `json:"ranges"`
}
type osvPackage struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
}
type osvRange struct {
	Events []map[string]string `json:"events"`
}

// ParseOSV parses an OSV advisory document (list or single) into Vulns,
// expanding each affected package in a supported ecosystem into its own
// entry with the import-ban match derived. Unsupported ecosystems are
// returned with Lang == "" so the caller can ledger them honestly.
func ParseOSV(data []byte) ([]Vuln, error) {
	var doc osvDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("not an OSV document: %w", err)
	}
	vulns := doc.Vulns
	if len(vulns) == 0 {
		var single osvVuln
		if err := json.Unmarshal(data, &single); err != nil || single.ID == "" {
			return nil, fmt.Errorf("no vulns array and no single vuln object found")
		}
		vulns = []osvVuln{single}
	}
	var out []Vuln
	for _, v := range vulns {
		for _, a := range v.Affected {
			out = append(out, derive(toVuln(v, a)))
		}
	}
	sort.Slice(out, func(i, j int) bool { return vulnKey(out[i]) < vulnKey(out[j]) })
	return out, nil
}

func toVuln(v osvVuln, a osvAffected) Vuln {
	out := Vuln{
		ID:        v.ID,
		Summary:   v.Summary,
		Package:   a.Package.Name,
		Ecosystem: a.Package.Ecosystem,
	}
	for _, alias := range v.Aliases {
		if strings.HasPrefix(alias, "CVE-") && out.CVE == "" {
			out.CVE = alias
		}
	}
	for _, r := range a.Ranges {
		for _, ev := range r.Events {
			if x, ok := ev["introduced"]; ok && out.Introduced == "" {
				out.Introduced = x
			}
			if x, ok := ev["fixed"]; ok {
				out.Fixed = x
			}
		}
	}
	return out
}

// derive fills the import-ban match from the OSV package coordinates.
// Maven "group:artifact" bans the groupId — import paths never carry the
// artifact, and the group prefix covers all its subpackages (importRule
// matches exact or prefix+"."); Go module paths are import prefixes as-is.
// Everything else (npm, PyPI, …) has no source-import vocabulary here and is
// left unsupported.
func derive(v Vuln) Vuln {
	switch {
	case v.Ecosystem == "Maven":
		if g, _, ok := strings.Cut(v.Package, ":"); ok {
			v.Match, v.Lang = g, "java"
		}
	case strings.HasPrefix(v.Ecosystem, "Go"):
		v.Match, v.Lang = v.Package, "go"
	}
	return v
}

func vulnKey(v Vuln) string { return v.Lang + "\x00" + v.Match + "\x00" + v.ID }

// Plan separates parsed vulns against the live policy: bans already present
// (kind+match dedup) become Existing, unsupported ecosystems become Skipped,
// the rest are new ban rows grouped per policy language.
func Plan(vulns []Vuln, existing map[string]bool) Result {
	var res Result
	for _, v := range vulns {
		if v.Lang == "" {
			res.Skipped = append(res.Skipped, v)
			continue
		}
		if existing[dedupKey(v.Lang, "import", v.Match)] {
			res.Existing = append(res.Existing, v)
			continue
		}
		res.Added = append(res.Added, v)
	}
	return res
}

// PlanGroups is Plan plus the per-language groups needed for merging.
func PlanGroups(vulns []Vuln, existing map[string]bool) (Result, []LangGroup) {
	res := Plan(vulns, existing)
	byLang := map[string][]BanRow{}
	for _, v := range res.Added {
		byLang[v.Lang] = append(byLang[v.Lang], RowFor(v))
	}
	var langs []string
	for lang := range byLang {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	var groups []LangGroup
	for _, lang := range langs {
		groups = append(groups, LangGroup{Lang: lang, Rows: byLang[lang]})
	}
	return res, groups
}

// DedupKeys flattens live policy bans into dedup keys, language-scoped.
func DedupKeys(bansByLang map[string][]BanRow) map[string]bool {
	out := map[string]bool{}
	for lang, bans := range bansByLang {
		for _, b := range bans {
			kind := b.Kind
			if kind == "" {
				kind = "call"
			}
			out[dedupKey(lang, kind, b.Match)] = true
		}
	}
	return out
}

func dedupKey(lang, kind, match string) string { return lang + "\x00" + kind + "\x00" + match }

// RowFor renders the advisory as a policy ban row. The message carries the
// advisory identity, the tripwire semantics and the removal condition so the
// finding text explains its own lifecycle.
func RowFor(v Vuln) BanRow {
	id := v.ID
	if v.CVE != "" && v.CVE != v.ID {
		id = fmt.Sprintf("%s (%s)", v.ID, v.CVE)
	}
	title := v.Summary
	if title == "" {
		title = v.Package
	}
	ver := "版本区间见 OSV 页面"
	if v.Introduced != "" && v.Fixed != "" {
		ver = fmt.Sprintf("影响 %s 起、%s 修复", v.Introduced, v.Fixed)
	}
	return BanRow{
		Kind:  "import",
		Match: v.Match,
		Reason: fmt.Sprintf("OSV %s: %s — 供应链告警 tripwire（源码引入点拦截；%s，版本判据由依赖树工具核验）；升级 %s 后移除本条",
			id, title, ver, v.Package),
		Use: fmt.Sprintf("升级 %s 至已修复版本后移除本条", v.Package),
	}
}

// Merge merges ban rows into policy YAML text. Structural cases: language
// section missing → block appended at EOF (creating the languages: key when
// absent); section present without banned-api → key inserted right after the
// section line; banned-api present → entries inserted after the last list
// item. Any unexpected shape (tab indentation, foreign formatting) is an
// error, never a best-effort rewrite — the caller validates the result with
// a YAML round-trip before writing anything.
func Merge(src []byte, groups []LangGroup) (string, error) {
	lines := splitLines(src)
	for _, g := range groups {
		if len(g.Rows) == 0 {
			continue
		}
		var err error
		if lines, err = mergeGroup(lines, g); err != nil {
			return "", err
		}
	}
	// keep POSIX text-file semantics: the merged policy always ends with a
	// newline even when the source did not (the YAML round-trip is unaffected).
	return strings.Join(lines, "\n") + "\n", nil
}

func splitLines(src []byte) []string {
	s := string(src)
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

func mergeGroup(lines []string, g LangGroup) ([]string, error) {
	if err := rejectTabs(lines); err != nil {
		return nil, err
	}
	sec := findLangSection(lines, g.Lang)
	if sec < 0 {
		return appendMissingSection(lines, g), nil
	}
	key := findBannedKey(lines, sec)
	if key < 0 {
		return insertBannedKey(lines, sec, g), nil
	}
	last := lastListItem(lines, key)
	out := append([]string{}, lines[:last+1]...)
	for _, r := range g.Rows {
		out = append(out, entryLines(r)...)
	}
	return append(out, lines[last+1:]...), nil
}

// rejectTabs refuses tab-indented input: YAML forbids tabs and the text-level
// merge would only corrupt the file further.
func rejectTabs(lines []string) error {
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimLeft(l, " "), "\t") {
			return fmt.Errorf("line %d uses tab indentation; refusing to merge", i+1)
		}
	}
	return nil
}

func appendMissingSection(lines []string, g LangGroup) []string {
	out := append([]string{}, lines...)
	if findTopKey(out, "languages") < 0 {
		out = append(out, "languages:")
	}
	out = append(out, "  "+g.Lang+":", "    banned-api:")
	for _, r := range g.Rows {
		out = append(out, entryLines(r)...)
	}
	return out
}

func insertBannedKey(lines []string, sec int, g LangGroup) []string {
	out := append([]string{}, lines[:sec+1]...)
	out = append(out, "    banned-api:")
	for _, r := range g.Rows {
		out = append(out, entryLines(r)...)
	}
	return append(out, lines[sec+1:]...)
}

// findBannedKey returns the index of the 4-space "banned-api:" key inside the
// language section starting at sec, or -1. The scan stops at the next
// language section line.
func findBannedKey(lines []string, sec int) int {
	for i := sec + 1; i < len(lines); i++ {
		if isLangSectionLine(lines[i]) {
			return -1
		}
		if indentOf(lines[i]) == 4 && strings.HasPrefix(strings.TrimLeft(lines[i], " "), "banned-api:") {
			return i
		}
	}
	return -1
}

// lastListItem returns the index of the last line belonging to the banned-api
// list block that starts at key. Blank lines stay in the block only when the
// next non-blank line is deeper-indented list content.
func lastListItem(lines []string, key int) int {
	last := key
	for i := key + 1; i < len(lines); i++ {
		t := lines[i]
		if strings.TrimSpace(t) == "" {
			j := i + 1
			for j < len(lines) && strings.TrimSpace(lines[j]) == "" {
				j++
			}
			if j < len(lines) && indentOf(lines[j]) > 4 {
				continue
			}
			break
		}
		if indentOf(t) > 4 {
			last = i
			continue
		}
		break
	}
	return last
}

func findLangSection(lines []string, lang string) int {
	for i, l := range lines {
		if l == "  "+lang+":" {
			return i
		}
	}
	return -1
}

func findTopKey(lines []string, key string) int {
	for _, l := range lines {
		if l == key+":" {
			return 1
		}
	}
	return -1
}

func isLangSectionLine(l string) bool {
	return len(l) > 2 && l[0] == ' ' && l[1] == ' ' && l[2] != ' ' && strings.HasSuffix(l, ":")
}

func indentOf(l string) int {
	n := 0
	for n < len(l) && l[n] == ' ' {
		n++
	}
	return n
}

func entryLines(r BanRow) []string {
	out := []string{"      - kind: " + r.Kind, "        match: " + yamlStr(r.Match)}
	if r.Reason != "" {
		out = append(out, "        reason: "+yamlStr(r.Reason))
	}
	if r.Use != "" {
		out = append(out, "        use: "+yamlStr(r.Use))
	}
	return out
}

// yamlStr double-quotes a scalar so CJK text, colons and hashes survive the
// round-trip.
func yamlStr(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
