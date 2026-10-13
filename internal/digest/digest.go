// Package digest renders a task-scoped constraint digest: a deterministic,
// LLM-oriented summary of the constraints that apply to one target package.
//
// The digest is the "前置接地" (pre-generation grounding) deliverable of the
// LLM constraint paradigm: instead of letting the model discover the policy
// after generation (post-hoc gate + repair), hand it the relevant slice of the
// constraint model before it writes code. Every line is derived from the
// policy / architecture policy / dependency allowlist / symbol index — zero
// LLM calls, byte-identical for identical inputs (auditable, gateable).
//
// Compatibility (non-standard inputs) degrades explicitly, never crashes:
// no policy → builtin defaults + a "not configured" note; no layers → the
// layer section is omitted; no bans → "(none configured)"; unknown package →
// warning + repo-wide digest; unknown rule types → skipped.
package digest

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"metricgate/internal/arch"
	"metricgate/internal/config"
	"metricgate/internal/engine"
	"metricgate/internal/rules"
	"metricgate/internal/symbols"
)

// Options controls one digest render.
type Options struct {
	// Package is the target package path (Go import path or slash-normalised
	// Java package). Empty means "repo-wide": include constraints that are not
	// package-scoped (bans, thresholds, deps) and say so.
	Package string
	// Task is the task text for conditional injection (AutoGuide-mapped,
	// deterministic matcher — see docs/research/digest-open-questions-research.md
	// 问题一). When non-empty, the 禁用 API/导入 section keeps only bans whose
	// identifier segments appear in the task text (plus generic bans with no
	// scoping vocabulary, e.g. `panic`); a note line records the filtering and
	// how to get the full list. Empty (default) = full list, byte-identical to
	// previous behavior.
	Task string
	Lang string // a registered language id; empty resolves to "go"
	// MaxSymbols caps the reuse-symbol list. 0 → default 30.
	MaxSymbols int
	// Index is the prebuilt symbol index for reuse guidance. Nil → section omitted.
	Index *symbols.Index
	// TargetPkg is the package filter applied to Index entries. Empty = Index unfiltered.
	TargetPkg string
	// Imports are the raw import strings of the TARGET package's own files
	// (facts.FileFacts.Imports: Go carries an optional "alias path" prefix,
	// Java the whole "import x.y.Z;" statement). They feed the in-package
	// dependency-precedent section: the target package's existing legal
	// imports are the cheapest compliance injection (M2-B V1/V2: agents
	// imitate in-package precedents instead of reaching across layers).
	// Nil/empty → section omitted.
	Imports []string
	// PackageResolved reports whether the caller's facts walk confirmed the
	// target package exists. False + non-empty Package makes Build append the
	// unknown-target warning BEFORE rendering, so both text and JSON forms
	// carry it. Callers that skip the walk (no-reuse) pass true.
	PackageResolved bool
}

// Section is one rendered digest block with full provenance, so the R3
// harness can audit exactly which policy entries produced which digest line.
type Section struct {
	Name    string   `json:"name"`
	Lines   []string `json:"lines"`
	Sources []string `json:"sources"` // e.g. "policy.yaml#banned_api.go[3]", "builtin:metrics.cyclomatic"
}

// sectionBuilder renders one optional section; ok=false omits it.
type sectionBuilder func() (Section, bool)

// Result is the rendered digest plus audit metadata.
type Result struct {
	Target    string    `json:"target"`
	Lang      string    `json:"lang"`
	Warnings  []string  `json:"warnings,omitempty"`
	Sections  []Section `json:"sections"`
	UsedChars int       `json:"used_chars"`
	EstTokens int       `json:"est_tokens"`
	Rendered  string    `json:"rendered"`
}

const defaultMaxSymbols = 30

// thresholdRow is one metric the digest reports, in LLM-priority order.
// IDs are the full metric IDs (metrics.Metric* constants), not abbreviations:
// EffectiveThresholds resolves by exact ID, so "cyclomatic" would silently
// resolve to the zero value and the row would vanish.
var thresholdRows = []struct{ id, label string }{
	{"cyclomatic-complexity", "圈复杂度"},
	{"cognitive-complexity", "认知复杂度"},
	{"function-length", "单函数行数"},
	{"params-count", "函数参数数"},
	{"nesting-depth", "嵌套深度"},
	{"file-length", "单文件行数"},
}

// MatchPackage reports whether a fact entry (its package field plus file
// path) belongs to the target package. Go stores the SHORT package name in
// the package field ("engine"), so the import path is matched via the file's
// directory; Java stores the full dotted package, so slash forms are
// normalised to dots. One implementation here — the CLI's known-package
// check and the reuse section's filter both call it, so they cannot drift.
func MatchPackage(pkgField, file, target string) bool {
	if target == "" || pkgField == "" && file == "" {
		return false
	}
	if pkgField == target || pkgField == strings.ReplaceAll(target, "/", ".") {
		return true
	}
	return file != "" && filepath.ToSlash(filepath.Dir(file)) == target
}

// Build renders the digest. It never returns an error for "missing data":
// missing sections degrade with warnings, per the compatibility matrix in
// docs/design/digest-engineering-checklist.md.
func Build(cfg *config.Config, opts Options) Result {
	lang, maxSym := buildDefaults(opts)
	res := Result{Target: opts.Package, Lang: lang}
	res.Warnings = buildWarnings(cfg, lang, opts)
	res.Sections = buildSections(cfg, lang, opts, maxSym)
	res.Rendered = renderText(res)
	res.UsedChars = len(res.Rendered)
	res.EstTokens = res.UsedChars / 3 // CJK-heavy text: ~3 bytes/token conservative
	return res
}

// buildDefaults resolves the language fallback and the reuse-list cap.
func buildDefaults(opts Options) (string, int) {
	lang := opts.Lang
	if lang == "" {
		lang = "go"
	}
	maxSym := opts.MaxSymbols
	if maxSym <= 0 {
		maxSym = defaultMaxSymbols
	}
	return lang, maxSym
}

// buildWarnings collects digest-level warnings BEFORE rendering so the text
// form carries them too (JSON must not be the only auditable form):
// unknown-target degradation and repo-wide ban scope.
func buildWarnings(cfg *config.Config, lang string, opts Options) []string {
	var w []string
	if opts.Package != "" && !opts.PackageResolved {
		w = append(w, fmt.Sprintf(
			"目标包 %s 在仓库中不存在或不含受支持源文件：digest 按未匹配包降级（分层契约注明未覆盖）", opts.Package))
	}
	if _, _, warn := banSection(cfg, lang, opts.Package, opts.Task); warn != "" {
		w = append(w, warn)
	}
	return w
}

// buildSections assembles the optional sections in LLM-priority order
// (layer contract first, red lines last); a section with no lines is
// omitted. banSection is pure, so calling it here and in buildWarnings is
// cheap and keeps each helper single-purpose. 包内依赖先例 sits next to
// 依赖白名单: the allowlist says what is legal in general, the precedents
// say what THIS package already does (M2-B: in-package precedents are the
// cheapest compliance injection — V1/V2 agents imitated them and never
// reached across layers).
func buildSections(cfg *config.Config, lang string, opts Options, maxSym int) []Section {
	banLines, banSrcs, _ := banSection(cfg, lang, opts.Package, opts.Task)
	builders := []sectionBuilder{
		func() (Section, bool) {
			lines, srcs := layerSection(cfg, opts.Package)
			return wrap("分层契约", lines, srcs)
		},
		func() (Section, bool) {
			lines, srcs := idiomSection(cfg, opts.Package)
			return wrap("跨层惯用法", lines, srcs)
		},
		func() (Section, bool) {
			if opts.Task != "" {
				lines := append([]string{"（禁用清单已按任务文本相关度筛选；完整清单见 constraints digest 不带 -task）"}, banLines...)
				return wrap("禁用 API/导入", lines, banSrcs)
			}
			return wrap("禁用 API/导入", banLines, banSrcs)
		},
		func() (Section, bool) {
			lines, srcs := depsSection(cfg)
			return wrap("依赖白名单", lines, srcs)
		},
		func() (Section, bool) {
			lines, srcs := precedentSection(opts.Imports, opts.Package, opts.Lang, cfg)
			return wrap("包内依赖先例", lines, srcs)
		},
		func() (Section, bool) {
			lines, srcs := thresholdSection(cfg, lang)
			return wrap("生效阈值", lines, srcs)
		},
		func() (Section, bool) {
			lines, srcs := reuseSection(opts.Index, opts.TargetPkg, maxSym)
			return wrap("复用既有实现", lines, srcs)
		},
		redlineSection,
	}
	var secs []Section
	for _, b := range builders {
		if s, ok := b(); ok {
			secs = append(secs, s)
		}
	}
	return secs
}

// wrap adapts the (name, lines, sources) section helpers to the builder
// shape: a section with no lines is omitted.
func wrap(name string, lines, sources []string) (Section, bool) {
	if len(lines) == 0 {
		return Section{}, false
	}
	return Section{Name: name, Lines: lines, Sources: sources}, true
}

// redlineSection is the one static section, aligned with acceptance-spec v1
// wording; it always renders.
func redlineSection() (Section, bool) {
	return Section{
		Name: "流程红线",
		Lines: []string{
			"不留 TODO/未实现占位（panic(\"not implemented\")、空函数体）",
			"不留空块；错误必须处理或显式返回",
			"动手前先读目标包既有代码；优先复用，禁止重造已有实现",
		},
		Sources: []string{"builtin:acceptance-spec-v1"},
	}, true
}

// renderText renders the LLM-oriented plain-text form: stable section
// headers, one "禁止/必须/已有" line per row, no YAML key names. Sections
// render in the order they were built (layer → bans → deps → thresholds →
// reuse → red lines).
func renderText(res Result) string {
	var b strings.Builder
	pkg := res.Target
	if pkg == "" {
		pkg = "（全仓）"
	}
	fmt.Fprintf(&b, "【任务约束摘要 — 目标包 %s（%s）】\n", pkg, res.Lang)
	for _, s := range res.Sections {
		fmt.Fprintf(&b, "■ %s\n", s.Name)
		for _, l := range s.Lines {
			fmt.Fprintf(&b, "  - %s\n", l)
		}
	}
	if len(res.Warnings) > 0 {
		fmt.Fprintf(&b, "⚠ 说明\n")
		for _, w := range res.Warnings {
			fmt.Fprintf(&b, "  - %s\n", w)
		}
	}
	return b.String()
}

// layerSection resolves the target package's layer membership and its
// allowed/forbidden dependency directions using arch's own pattern matcher.
func layerSection(cfg *config.Config, pkg string) ([]string, []string) {
	pol := arch.Policy{Layers: cfg.Architecture.Layers, Forbids: cfg.Architecture.Forbid}
	if pol.Empty() {
		return nil, nil
	}
	lines, srcs := layerMembershipLines(cfg.Architecture.Layers, pkg)
	fl, fs := forbidLines(cfg.Architecture.Forbid, pkg)
	lines = append(lines, fl...)
	srcs = append(srcs, fs...)
	nl, ns := layerNoMatchNote(cfg.Architecture.Layers, pkg)
	lines = append(lines, nl...)
	srcs = append(srcs, ns...)
	cl, cs := cycleLines(cfg.Architecture.Cycles)
	lines = append(lines, cl...)
	srcs = append(srcs, cs...)
	return lines, srcs
}

// layerMembershipLines renders one line per layer whose package patterns
// match the target, with the allowed-dependency contract.
func layerMembershipLines(layers []arch.Layer, pkg string) ([]string, []string) {
	var lines, srcs []string
	for i, l := range layers {
		if pkg == "" || !arch.MatchesAny(l.Packages, pkg) {
			continue
		}
		if len(l.MayDependOn) == 0 {
			lines = append(lines, fmt.Sprintf("本包属于「%s」层；未声明允许依赖 → 不依赖其他层的包", l.Name))
		} else {
			lines = append(lines, fmt.Sprintf("本包属于「%s」层；仅允许依赖：%s", l.Name, strings.Join(l.MayDependOn, ", ")))
		}
		srcs = append(srcs, fmt.Sprintf("policy#architecture.layers[%d]", i))
	}
	return lines, srcs
}

// forbidLines renders the matched forbidden dependency directions.
func forbidLines(forbids []arch.Forbid, pkg string) ([]string, []string) {
	var lines, srcs []string
	for i, f := range forbids {
		if pkg == "" || !arch.MatchesAny([]string{f.From}, pkg) {
			continue
		}
		line := fmt.Sprintf("禁止依赖匹配 %s 的包", f.To)
		if f.Reason != "" {
			line += " — " + f.Reason
		}
		lines = append(lines, line)
		srcs = append(srcs, fmt.Sprintf("policy#architecture.forbid[%d]", i))
	}
	return lines, srcs
}

// layerNoMatchNote fires only when no LAYER matched: a global forbid (from
// "**") applies to every package and must not mask the fact that the layer
// policy says nothing about this target. Repo-wide digests say nothing.
func layerNoMatchNote(layers []arch.Layer, pkg string) ([]string, []string) {
	if pkg == "" || layerCovered(layers, pkg) {
		return nil, nil
	}
	return []string{fmt.Sprintf("（目标包 %s 未被任何分层声明覆盖 → 分层无约束）", pkg)},
		[]string{"builtin:arch.no-match"}
}

// cycleLines reports the import-cycle ban when the arch policy enables it.
func cycleLines(cycles string) ([]string, []string) {
	if cycles == "" || cycles == "off" {
		return nil, nil
	}
	return []string{"禁止 import 环（循环依赖）"}, []string{"policy#architecture.cycles"}
}

// idiomSection renders the cross-layer idiom hint: R3 U04 showed the
// B-architecture clause class is the one digest could not prevent — the
// port-adapter repair exists (the model re-derived it twice during R2
// repair) but only under repair-phase iteration, not one-shot generation
// pressure. The digest now carries the idiom explicitly, next to the layer
// contract it qualifies. Rendered only when the target package is covered
// by a layer declaration: an unlayered package has no cross-layer contract
// for the idiom to qualify.
func idiomSection(cfg *config.Config, pkg string) ([]string, []string) {
	if !layerCovered(cfg.Architecture.Layers, pkg) {
		return nil, nil
	}
	return []string{
		"需要使用其他层的包而当前层不允许依赖时：在当前层定义 port（interface），由被依赖层实现，在组装根（composition root，如 main/入口装配处）注入实现",
		"禁止用复制代码的方式绕过分层", // stated by pilot FP analysis: the SDP/layer message must exclude the wrong fix
	}, []string{"builtin:arch.port-adapter-idiom"}
}

// layerCovered reports whether any layer declaration matches the target
// package (repo-wide digests have no package to match).
func layerCovered(layers []arch.Layer, pkg string) bool {
	if pkg == "" {
		return false
	}
	for _, l := range layers {
		if arch.MatchesAny(l.Packages, pkg) {
			return true
		}
	}
	return false
}

// banSection lists bans whose exemption status does not cover the whole
// target package. A ban the package is fully exempt from is omitted entirely
// (anti-FP-fatigue): teaching the model about bans it may break erodes the
// digest.
// banSection lists bans whose exemption status does not cover the
// target package. A ban the package is fully exempt from is omitted entirely
// (anti-FP-fatigue): teaching the model about bans it may break erodes the
// digest. Task-based filtering is delegated to filterBans to keep complexity low.
func banSection(cfg *config.Config, lang, pkg string, task string) ([]string, []string, string) {
	bans := cfg.Bans(lang)
	// Apply task filtering early, before building lines
	var filteredBans []config.BanConfig
	var notePrefix string
	if task != "" {
		taskToks := taskTokens(task)
		filteredBans, notePrefix, _ = filterBans(bans, taskToks, task)
	} else {
		filteredBans = bans
	}
	var lines, srcs []string
	warn := ""
	if pkg == "" {
		warn = "未指定目标包：禁用 API 清单按全仓适用列出"
	}
	for i, b := range filteredBans {
		if pkg != "" && fullyExempt(b, pkg) {
			continue
		}
		kind := b.Kind
		if kind == "" {
			kind = "call"
		}
		var line string
		switch kind {
		case "call":
			line = fmt.Sprintf("禁止调用 %s", b.Match)
			if len(b.ArgsBan) > 0 {
				line += fmt.Sprintf("（仅当参数为 %s）", strings.Join(b.ArgsBan, "/"))
			}
		case "import":
			line = fmt.Sprintf("禁止 import %s（含其子包）", b.Match)
		case "constructor":
			if !rules.CanExpress(lang, rules.BanConstructor) {
				continue // never advertise what the gate cannot enforce
			}
			line = fmt.Sprintf("禁止 new %s", b.Match)
			if len(b.ArgsBan) > 0 {
				line += fmt.Sprintf("（仅当参数为 %s）", strings.Join(b.ArgsBan, "/"))
			}
		default:
			continue // unknown kind: skip, never crash
		}
		if b.Reason != "" {
			line += " — " + b.Reason
		}
		if b.Use != "" {
			line += "；替代写法：" + b.Use
		}
		if notePrefix != "" {
			line = notePrefix + line
			notePrefix = "" // only on first line
		}
		lines = append(lines, line)
		srcs = append(srcs, fmt.Sprintf("policy#languages.%s.banned-api[%d]", lang, i))
	}
	return lines, srcs, warn
}

// fullyExempt reports whether every file of the target package is exempt
// from the ban, so the digest line is omitted entirely (anti-FP-fatigue).
// except_files are filename suffixes or path patterns (the engine matches
// suffix / prefix / filepath.Match); without filesystem access the digest
// approximates: (a) path-scoped entries (contain "/") must cover the whole
// package directory; (b) bare filename suffixes match the package's file
// names, approximated by <pkgbase>.go — which is exact for the Go convention
// package foo_test ↔ *_test.go, the case except_files exists for.
func fullyExempt(b config.BanConfig, pkg string) bool {
	if len(b.ExceptFiles) == 0 || pkg == "" {
		return false
	}
	p := filepath.ToSlash(pkg)
	base := filepath.Base(p)
	for _, ex := range b.ExceptFiles {
		ex = strings.TrimSpace(filepath.ToSlash(ex))
		if ex == "" {
			continue
		}
		if strings.Contains(ex, "/") {
			prefix := strings.TrimSuffix(ex, "*")
			if strings.HasPrefix(p, prefix) || strings.HasPrefix(p+"/", prefix) {
				return true
			}
			continue
		}
		s := strings.TrimPrefix(ex, "*")
		if strings.HasSuffix(base+".go", s) || strings.HasSuffix(p, s) {
			return true
		}
	}
	return false
}

// depsSection renders the dependency allowlist summary. Only counts and root
// prefixes — the full list is not what the model needs (checklist item 7).
func depsSection(cfg *config.Config) ([]string, []string) {
	d := cfg.Dependencies
	if len(d.Allow) == 0 && len(d.Self) == 0 {
		return nil, nil
	}
	var lines, srcs []string
	if len(d.Self) > 0 {
		lines = append(lines, fmt.Sprintf("本仓模块前缀：%s", strings.Join(d.Self, ", ")))
		srcs = append(srcs, "policy#dependencies.self")
	}
	if len(d.Allow) > 0 {
		lines = append(lines, fmt.Sprintf("import 白名单（%d 个根前缀）：%s", len(d.Allow), strings.Join(d.Allow, ", ")))
		srcs = append(srcs, "policy#dependencies.allow")
	}
	lines = append(lines, "清单之外的 import 会被 unknown-dependency 拦截（std 库除外）")
	return lines, srcs
}

// precedentSection lists the target package's EXISTING in-repo imports as
// dependency precedents (M2-B V1/V2 finding: agents facing a blank choose
// the import path by imitation — in-package precedents made U04's core→adapter
// violation disappear without any digest injection, while V3 (no precedent)
// violated in L0). Rendered only for internal/ imports of a named target
// package: std and third-party imports are not architecture decisions.
// The section complements 依赖白名单 (what is allowed in general) with what
// THIS package already does (the concrete legal route to imitate).
func precedentSection(raw []string, pkg string, lang string, cfg *config.Config) ([]string, []string) {
	if pkg == "" || len(raw) == 0 {
		return nil, nil
	}
	var lines []string
	seen := map[string]bool{}
	for _, imp := range raw {
		p := precedentPath(imp, lang, cfg.Dependencies.Self)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		lines = append(lines, "本包已依赖 "+p)
	}
	if len(lines) == 0 {
		return nil, nil
	}
	sort.Strings(lines)
	lines = append(lines, "以上是包内已验证的合法依赖方向；新增依赖前先确认分层契约允许，需要跨层能力时优先复用既有依赖暴露的能力")
	return lines, []string{"facts:" + pkg + "#imports"}
}

// isInternalImport reports whether an import path points inside the repo's
// own internal/ tree (module prefix optional: "internal/x" and
// "metricgate/internal/x" both qualify). std and third-party imports are not
// architecture decisions, so they never become precedents.
func isInternalImport(p string) bool {
	return strings.HasPrefix(p, "internal/") || strings.Contains(p, "/internal/")
}

// precedentPath normalises one raw import string to a bare precedent path, or
// "" when the import is not an in-repo dependency. What counts as "ours" is
// spelled differently in every language — a relative specifier anywhere, Go's
// module prefix, Java's dotted self prefix, a quoted C include, Rust's crate
// root — while an import that only reaches outside the repository is never an
// architecture decision worth imitating.
func precedentPath(raw, lang string, self []string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return ""
	}
	// A relative specifier is inside the repository in every language that
	// has one (TypeScript, JavaScript, Python, C/C++ includes).
	if strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") {
		return p
	}
	switch lang {
	case "rust":
		return rustPrecedent(p, self)
	case "c", "cpp":
		// A quoted include is a file in this repository; an angled one names
		// a system or third-party header and is not an architecture decision.
		if strings.HasPrefix(p, "\"") {
			return strings.Trim(p, "\"")
		}
		return ""
	case "java":
		p = javaImportPath(p)
		if !hasSelfPrefix(p, self) {
			return ""
		}
		return p
	}
	// Go and the dotted languages (Python, C#, bare TS/JS specifiers): drop an
	// alias prefix if present, then keep only what is inside the repository.
	if i := strings.Index(p, " "); i >= 0 {
		p = p[i+1:] // Go alias prefix: the path is the last word
	}
	p = strings.Trim(p, "\"'")
	if lang == "go" {
		if !isInternalImport(p) {
			return ""
		}
		return p
	}
	if hasSelfPrefix(p, self) {
		return p
	}
	return ""
}

// javaImportPath cleans a stored Java import statement down to its path.
func javaImportPath(p string) string {
	p = strings.TrimPrefix(p, "import ")
	p = strings.TrimSuffix(strings.TrimSpace(p), ";")
	if i := strings.LastIndex(p, " "); i >= 0 {
		p = p[i+1:] // static import: the path is the last word
	}
	return strings.Trim(p, "\"")
}

// rustPrecedent reduces a Rust use path to a precedent, or "" when it points
// outside the crate. "crate::" and "self::" are this crate by definition;
// "super::" is a parent module, also inside. A bare first segment is another
// crate unless the policy declares it as our own.
func rustPrecedent(p string, self []string) string {
	p = strings.TrimSuffix(strings.TrimSpace(p), ";")
	path := strings.ReplaceAll(p, "::", "/")
	root := path
	if i := strings.Index(path, "/"); i >= 0 {
		root = path[:i]
	}
	switch root {
	case "crate", "self", "super":
		return path
	}
	if hasSelfPrefix(root, self) || hasSelfPrefix(path, self) {
		return path
	}
	return ""
}

// hasSelfPrefix reports whether p lives inside one of the declared module
// self prefixes (the repo's own Java code).
func hasSelfPrefix(p string, self []string) bool {
	for _, s := range self {
		if strings.HasPrefix(p, s) {
			return true
		}
	}
	return false
}

// thresholdSection reports effective thresholds via the engine's own
// resolution (builtin floor + policy override), so digest numbers always
// equal gate numbers.
func thresholdSection(cfg *config.Config, lang string) ([]string, []string) {
	var lines, srcs []string
	for _, row := range thresholdRows {
		w, e := engine.EffectiveThresholds(cfg, lang, row.id)
		if w == 0 && e == 0 {
			continue // metric disabled (or both levels off)
		}
		switch {
		case w > 0 && e > 0:
			lines = append(lines, fmt.Sprintf("%s ≤%d（超过 %d 为错误）— 超出即拆函数", row.label, w, e))
		case w > 0:
			lines = append(lines, fmt.Sprintf("%s ≤%d（error 已关闭，只告警）— 超出即拆函数", row.label, w))
		default:
			lines = append(lines, fmt.Sprintf("%s 超过 %d 为错误（warn 已关闭）", row.label, e))
		}
		srcs = append(srcs, "builtin:metrics."+row.id)
	}
	return lines, srcs
}

// reuseSection lists exported symbols already living in the target package
// (R1b: seeing the existing implementation drives 100% reuse; not seeing it,
// 0%). Deterministic order: symbol name asc, then file asc. Test files and
// Test*/Benchmark*/Example*/Fuzz* functions are excluded: they are exported
// but are not reuse targets, and they would crowd the capped list with noise.
func reuseSection(ix *symbols.Index, pkg string, max int) ([]string, []string) {
	if ix == nil || pkg == "" {
		return nil, nil
	}
	rows := reuseRows(ix, pkg)
	if len(rows) == 0 {
		return nil, nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	if len(rows) > max {
		rows = rows[:max]
	}
	lines := make([]string, 0, len(rows)+1)
	for _, d := range rows {
		lines = append(lines, "已有 "+sigLine(d.Signature))
	}
	lines = append(lines, "动手前先查上述既有实现；能复用则复用，禁止重造")
	return lines, []string{"symbols:" + pkg}
}

// reuseRows collects the eligible, de-duplicated definitions: exported,
// in-package, non-test, first occurrence per (name, signature).
func reuseRows(ix *symbols.Index, pkg string) []symbols.Def {
	var rows []symbols.Def
	seen := map[string]bool{}
	for _, d := range ix.All() {
		key := d.Name + "|" + d.Signature
		if seen[key] || !reuseEligible(d, pkg) {
			continue
		}
		seen[key] = true
		rows = append(rows, d)
	}
	return rows
}

// reuseEligible filters one definition: exported, in the target package, not
// from a test file, not a Go test-harness entry point.
func reuseEligible(d symbols.Def, pkg string) bool {
	if d.Visibility != "exported" || !MatchPackage(d.Package, d.File, pkg) {
		return false
	}
	return !strings.HasSuffix(d.File, "_test.go") && !isTestFuncName(d.Name)
}

// isTestFuncName reports whether a Go symbol is a test/benchmark/example
// harness entry point rather than a reusable implementation symbol.
func isTestFuncName(name string) bool {
	for _, p := range []string{"Test", "Benchmark", "Example", "Fuzz"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// sigLine trims a definition signature for digest display: type declarations
// arrive with their whole body (facts renders the decl up to the body end),
// so anything from the first '{' on is dropped; long signatures are capped.
func sigLine(sig string) string {
	if i := strings.IndexByte(sig, '{'); i >= 0 {
		sig = strings.TrimSpace(sig[:i])
	}
	const sigCap = 120
	r := []rune(sig)
	if len(r) > sigCap {
		r = append(r[:sigCap-1], '…')
	}
	return string(r)
}

// taskTokens splits the task text into lowercase alphanumeric segments.
// Deterministic by construction: tokenization is plain ASCII segmentation,
// no stemming, no stopwords — the same task text always yields the same
// token set (claims #10 discipline).
func taskTokens(task string) []string {
	fields := strings.FieldsFunc(strings.ToLower(task), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r >= 0x80)
	})
	return fields
}

// banRelevant decides whether one ban stays in a task-filtered digest. A ban
// is relevant when any of its scoping identifier segments (≥3 chars) appears
// in the task tokens. Bans whose match has no such segment (e.g. `panic`) —
// vocabulary too generic to ever appear in a task description — are kept:
// when in doubt, the constraint stays (fail-closed; a dropped constraint is
// a prevention hole, a kept one costs one digest line).
func banRelevant(b config.BanConfig, taskToks []string, taskRaw string) bool {
	m := strings.ToLower(strings.TrimSuffix(b.Match, "*"))
	segs := strings.FieldsFunc(m, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r >= 0x80)
	})
	namespaced := strings.ContainsAny(m, "./")
	lower := strings.ToLower(taskRaw)
	for _, seg := range segs {
		if len(seg) < 3 {
			continue
		}
		for _, tok := range taskToks {
			if tok == seg {
				return true
			}
		}
		if strings.Contains(lower, seg) {
			return true
		}
	}
	// Bare language-level idioms (panic, no namespace) carry no scoping
	// vocabulary a task description could echo — they stay unconditionally.
	return !namespaced
}

// filterBans applies task-based filtering to a list of bans. Returns filtered
// bans, a note prefix (if anything was filtered), and a boolean indicating
// whether filtering occurred. Empty task → no filtering, full list returned.
func filterBans(bans []config.BanConfig, taskToks []string, taskRaw string) ([]config.BanConfig, string, bool) {
	if taskToks == nil {
		return bans, "", false
	}
	var out []config.BanConfig
	dropped := false
	for _, b := range bans {
		if !banRelevant(b, taskToks, taskRaw) {
			dropped = true
			continue
		}
		out = append(out, b)
	}
	var note string
	if dropped {
		note = "（禁用清单已按任务文本相关度筛选；完整清单见 constraints digest 不带 -task）\n"
	}
	return out, note, dropped
}
