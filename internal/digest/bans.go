// Ban rendering and task filtering for the digest.
//
// Bans come from two places: the built-in floor every policy inherits (see
// config.BanEntries) and the policy file's own banned-api block. This file
// keeps the rendering, the exemption test and the task-relevance filter
// together, so the floor and the policy entries cannot drift apart.

package digest

import (
	"fmt"
	"path/filepath"
	"strings"

	"metricgate/internal/config"
	"metricgate/internal/rules"
)

// banSection lists bans whose exemption status does not cover the whole
// target package. A ban the package is fully exempt from is omitted entirely
// (anti-FP-fatigue): teaching the model about bans it may break erodes the
// digest. Task-based filtering is delegated to filterBanEntries to keep
// complexity low.
func banSection(cfg *config.Config, lang, pkg string, task string) ([]string, []string, string) {
	bans := cfg.BanEntries(lang)
	// Apply task filtering early, before building lines
	var filteredBans []config.BanEntry
	var notePrefix string
	if task != "" {
		taskToks := taskTokens(task)
		filteredBans, notePrefix, _ = filterBanEntries(bans, taskToks, task)
	} else {
		filteredBans = bans
	}
	var lines, srcs []string
	warn := ""
	if pkg == "" {
		warn = "未指定目标包：禁用 API 清单按全仓适用列出"
	}
	for _, e := range filteredBans {
		b := e.Ban
		if pkg != "" && fullyExempt(b, pkg) {
			continue
		}
		kind := b.Kind
		if kind == "" {
			kind = "call"
		}
		line, ok := banLine(lang, b, kind)
		if !ok {
			continue // unknown kind or a constructor the gate cannot express
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
		srcs = append(srcs, banSource(lang, e))
	}
	return lines, srcs, warn
}

// banSource names where one rendered ban came from. The built-in floor is
// labelled "builtin" (the same prefix metrics provenance uses) so a reader can
// tell an inherited default from something the policy file actually wrote. The
// floor has no position in any file, so it is named by match instead of index.
func banSource(lang string, e config.BanEntry) string {
	if e.Source == "builtin" {
		return "builtin:languages." + lang + ".banned-api[" + e.Ban.Match + "]"
	}
	return fmt.Sprintf("%s#languages.%s.banned-api[%d]", e.Source, lang, e.Index)
}

// banLine renders one ban as a digest sentence, plus the 'use this instead'
// advice. ok=false means the entry must not be rendered: an unknown kind, or a
// constructor ban on a language whose query cannot see one.
func banLine(lang string, b config.BanConfig, kind string) (string, bool) {
	var line string
	switch kind {
	case "call":
		line = fmt.Sprintf("禁止调用 %s", b.Match)
	case "import":
		line = fmt.Sprintf("禁止 import %s（含其子包）", b.Match)
	case "constructor":
		if !rules.CanExpress(lang, rules.BanConstructor) {
			return "", false // never advertise what the gate cannot enforce
		}
		line = fmt.Sprintf("禁止 new %s", b.Match)
	default:
		return "", false // unknown kind: skip, never crash
	}
	if len(b.ArgsBan) > 0 {
		line += fmt.Sprintf("（仅当参数为 %s）", strings.Join(b.ArgsBan, "/"))
	}
	return line, true
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
	entries := make([]config.BanEntry, 0, len(bans))
	for _, b := range bans {
		entries = append(entries, config.BanEntry{Ban: b})
	}
	kept, note, dropped := filterBanEntries(entries, taskToks, taskRaw)
	out := make([]config.BanConfig, 0, len(kept))
	for _, e := range kept {
		out = append(out, e.Ban)
	}
	return out, note, dropped
}

// filterBanEntries is filterBans over entries, so filtering does not discard
// the provenance each rendered line needs. filterBans itself is kept for the
// plain-BanConfig callers and delegates here.
func filterBanEntries(entries []config.BanEntry, taskToks []string, taskRaw string) ([]config.BanEntry, string, bool) {
	if taskToks == nil {
		return entries, "", false
	}
	var out []config.BanEntry
	dropped := false
	for _, e := range entries {
		if !banRelevant(e.Ban, taskToks, taskRaw) {
			dropped = true
			continue
		}
		out = append(out, e)
	}
	var note string
	if dropped {
		note = "（禁用清单已按任务文本相关度筛选；完整清单见 constraints digest 不带 -task）\n"
	}
	return out, note, dropped
}
