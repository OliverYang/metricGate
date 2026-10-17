package cockpit

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"metricgate/internal/webspec"
)

// The cockpit frontend is one JavaScript file with no bundler, so nothing
// except a test can enforce its internal structure. These are the assertions
// that keep a growing single file navigable, documented in
// docs/design/cockpit-single-file-tech.md §2 and §7.
//
// They are also the cheapest possible regression net: every one of them failed
// at some point in this file's life, and each failure was silent in the browser
// (a button that did nothing, a filter that showed nothing).

func readAppJS(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatalf("read web/app.js: %v", err)
	}
	return string(data)
}

// sectionOrder is the layering contract: tokens, utils, state, api, router,
// components, views, boot. Dependencies may only flow downward.
var sectionOrder = []string{
	"S1 · TOKENS", "S2 · UTILS", "S3 · STORE", "S4 · API",
	"S5 · ROUTER", "S6 · COMPONENTS", "S7 · VIEWS", "S8 · BOOT",
}

func TestAppJSSectionsAppearInLayeringOrder(t *testing.T) {
	src := readAppJS(t)
	last := -1
	for _, section := range sectionOrder {
		index := strings.Index(src, "//! "+section)
		if index < 0 {
			t.Fatalf("section %q is missing; the file's layers are part of its contract", section)
		}
		if index <= last {
			t.Errorf("section %q appears out of order; S%d must not be imported by an earlier one", section, section[1])
		}
		last = index
	}
}

var actionRe = regexp.MustCompile(`data-action="?([a-z]+[.a-z]+)"?`)

// readUI is the whole browser-facing surface: the markup in index.html and the
// behaviour in app.js. A data-action that only one of them knows about is a
// broken control in exactly the same way, so both are read as one text.
func readUI(t *testing.T) string {
	t.Helper()
	markup, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatalf("read web/index.html: %v", err)
	}
	return readAppJS(t) + "\n" + string(markup)
}

// actionNames is every data-action the UI declares.
func actionNames(src string) map[string]bool {
	names := map[string]bool{}
	for _, m := range actionRe.FindAllStringSubmatch(src, -1) {
		names[m[1]] = true
	}
	return names
}

// handledActions is every action the delegation table in S8 registers.
func handledActions(src string) map[string]bool {
	names := map[string]bool{}
	for _, m := range regexp.MustCompile(`ACTIONS\["([^"]+)"\]`).FindAllStringSubmatch(src, -1) {
		names[m[1]] = true
	}
	return names
}

// TestEveryTemplateActionIsHandled catches the classic single-file failure: a
// template names a data-action that no handler claims, so the button renders,
// accepts clicks, and does nothing.
func TestEveryTemplateActionIsHandled(t *testing.T) {
	declared := actionNames(readUI(t))
	handled := handledActions(readAppJS(t))
	for name := range declared {
		if !handled[name] {
			t.Errorf("data-action=%q is rendered but never handled; it would be a dead control", name)
		}
	}
}

// TestEveryHandledActionIsReachable is the mirror image: a handler for an
// action no template emits is dead code that will not be noticed as dead.
func TestEveryHandledActionIsReachable(t *testing.T) {
	declared := actionNames(readUI(t))
	for name := range handledActions(readAppJS(t)) {
		if !declared[name] {
			t.Errorf("ACTIONS[%q] handles an action no template emits", name)
		}
	}
}

// TestKeyboardShortcutsYieldToTyping pins the input-state guard: a bare
// keydown handler turns "type f in the search box" into "change status", which
// is a bug every keyboard-driven list eventually ships.
func TestKeyboardShortcutsYieldToTyping(t *testing.T) {
	src := readAppJS(t)
	if !strings.Contains(src, "typingInAField(event)") {
		t.Fatal("the keydown handler does not check for typing; shortcuts would fire inside inputs")
	}
	if !strings.Contains(src, `matches("input, textarea, select, [contenteditable]")`) {
		t.Error("typingInAField does not cover the form controls it must")
	}
}

// TestEventListenersAreCentralised counts the places wiring events. A single
// delegated click handler is the contract; one listener per control is the
// state that made this file unwieldy in the first place.
func TestEventListenersAreCentralised(t *testing.T) {
	src := readAppJS(t)
	addCalls := 0
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.Contains(trimmed, "addEventListener(") {
			addCalls++
		}
	}
	if addCalls > 6 {
		t.Errorf("app.js registers %d listeners; events should be delegated through S8's table", addCalls)
	}
}

// TestTemplateInterpolationGoesThroughHTML checks that the escaping template is
// actually the thing being used, not a helper that exists while the old concat
// style lingers. One concat is allowed: the html helper itself.
func TestTemplateInterpolationGoesThroughHTML(t *testing.T) {
	src := readAppJS(t)
	assignments := strings.Count(src, "innerHTML =")
	if assignments != 1 {
		t.Errorf("innerHTML is assigned %d times; exactly one place (render) should own the DOM write", assignments)
	}
}

// TestMappedListsAreJoinedFragments catches the silent failure that a string
// template invites: `items.map(card).join("")` stringifies each fragment before
// it is interpolated, so every list renders the literal text "[object Object]"
// and no error is raised anywhere. The blank list looks like missing data, so it
// is the kind of bug that survives review. join() keeps the fragments raw.
func TestMappedListsAreJoinedFragments(t *testing.T) {
	src := readAppJS(t)
	// The failure mode is specific — a template fragment produced by map()
	// and then joined as a string — so the check looks for the delimiter that
	// idiom uses rather than for every join in the file. Joining already
	// escaped strings (a search haystack, a sentence) is ordinary JavaScript.
	targets := []string{`.join("")`, ".join('')"}
	for i, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		for _, target := range targets {
			if !strings.Contains(trimmed, target) {
				continue
			}
			// interpolate is the one place allowed to join this way: it joins
			// strings it already escaped, not fragments waiting to be escaped.
			if strings.Contains(trimmed, "value.map(interpolate)") {
				continue
			}
			t.Errorf("web/app.js:%d joins a template result directly (%s); use list() so fragments are not stringified into [object Object]", i+1, strings.TrimSpace(trimmed))
		}
	}
}

// TestRenderAllViews is the pure-function layer's regression net: every view
// against a fixture, for every scope, in both history modes, plus the router
// round trip.
//
// It goes through node because the views are JavaScript and this test has no
// browser. Node is a development dependency only — nothing in the release path
// runs it, and the test skips when the binary is absent (see
// docs/design/cockpit-single-file-tech.md §8.2). What it buys is coverage the
// static assertions above cannot give: a field the server renamed, a list joined
// as a string, an undefined interpolated into a template. Every one of those
// renders something plausible and breaks only when an operator reads it.
func TestRenderAllViews(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; frontend render coverage is skipped (dev-only check)")
	}
	cmd := exec.Command(node, "render_all.mjs")
	cmd.Dir = "web"
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render_all.mjs failed:\n%s", out)
	}
	if !strings.Contains(string(out), "router round trips ok") {
		t.Fatalf("render_all.mjs produced no summary:\n%s", out)
	}
	t.Logf("%s", strings.TrimSpace(string(out)))
}

// TestCSSVariablesAreDefined catches the class of bug where a stylesheet uses a
// token nobody declared. The browser silently drops the declaration, so a
// border or colour just quietly stops applying and no error appears anywhere.
func TestCSSVariablesAreDefined(t *testing.T) {
	css, err := os.ReadFile("web/style.css")
	if err != nil {
		t.Fatalf("read web/style.css: %v", err)
	}
	declared := map[string]bool{}
	for _, m := range regexp.MustCompile(`(--[a-z0-9-]+)\s*:`).FindAllStringSubmatch(string(css), -1) {
		declared[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`var\((--[a-z0-9-]+)`).FindAllStringSubmatch(string(css), -1) {
		if !declared[m[1]] {
			t.Errorf("style.css uses var(%s) but never declares it; the rule silently does nothing", m[1])
		}
	}
}

// TestNoColourLiteralsOutsideTokens is the second half of the token contract.
// TestCSSVariablesAreDefined catches a var() that points nowhere; this catches
// the opposite drift, a colour typed straight into a rule. Both were previously
// checked by hand — an awk one-liner in the ledger — and a hand check cannot
// fail, so the two rules that make the palette changeable in one place were the
// two rules nothing enforced. A hardcoded colour survives every refactor and
// only shows up when someone changes the theme and one panel does not move.
//
// The literal may live in any :root block (style.css has two: the Echoes
// primitives and the semantic aliases), so the blocks are cut out rather than
// one line range being skipped.
func TestNoColourLiteralsOutsideTokens(t *testing.T) {
	css, err := os.ReadFile("web/style.css")
	if err != nil {
		t.Fatalf("read web/style.css: %v", err)
	}
	outside := rootBlocksRe.ReplaceAllString(string(css), "")
	literals := colourLiteralRe.FindAllString(outside, -1)
	if len(literals) > 0 {
		t.Errorf("style.css uses %d colour literal(s) outside a :root token block (first: %s); a literal here does not follow the palette and will not move with a theme change", len(literals), literals[0])
	}
}

// rootBlocksRe matches a whole `:root { ... }` block, non-greedy to the first
// closing brace at column zero. Nested braces are not used in these blocks.
var rootBlocksRe = regexp.MustCompile(`(?m)^:root \{[\s\S]*?^\}`)

// colourLiteralRe matches the hex and functional forms the design system bans
// outside the token table. Bare colour keywords (red, black) are deliberately
// not matched: `currentColor` and `transparent` are legitimate and a keyword
// scan would need an allow-list longer than the rule is worth.
var colourLiteralRe = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(`)

// viewsRe matches the route table's entries: `VIEWS.overview = {`.
var viewsRe = regexp.MustCompile(`(?m)^VIEWS\.([a-z]+) = \{`)

// TestViewTableMatchesSpec is the contract's own guard. The whole reason
// internal/webspec exists is that the two sides of this UI share one list of
// names, and the enumeration was written from memory: it named six views while
// the frontend had ten, so the file that is supposed to catch drift was itself
// the drift.
//
// Both directions matter. A view in spec.views with no VIEWS entry is a nav
// button that paints nothing; one in VIEWS with no spec entry is reachable only
// by a URL nobody documented, and the rail cannot know whether to highlight it.
func TestViewTableMatchesSpec(t *testing.T) {
	src := readAppJS(t)
	got := []string{}
	seen := map[string]bool{}
	for _, m := range viewsRe.FindAllStringSubmatch(src, -1) {
		if seen[m[1]] {
			t.Errorf("VIEWS.%s is defined twice; the second silently replaces the first", m[1])
		}
		seen[m[1]] = true
		got = append(got, m[1])
	}
	want := webspec.Views()
	if len(got) != len(want) {
		t.Errorf("the frontend has %d views (%v), the spec declares %d (%v); the two are one contract",
			len(got), got, len(want), want)
	}
	for _, name := range want {
		if !seen[name] {
			t.Errorf("spec declares view %q but VIEWS has no entry for it", name)
		}
	}
	for _, name := range got {
		if !containsString(want, name) {
			t.Errorf("VIEWS.%s is not in webspec.Views(); it is unreachable from a shared link", name)
		}
	}
}

// TestSidebarViewsExist is the third side of the same name list: the rail is
// hand-written markup, so a typo there is a button that navigates to a view
// that render() silently replaces with the default.
func TestSidebarViewsExist(t *testing.T) {
	src := readAppJS(t)
	index, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatalf("read web/index.html: %v", err)
	}
	for _, m := range regexp.MustCompile(`data-view="([a-z]+)"`).FindAllStringSubmatch(string(index), -1) {
		if !strings.Contains(src, "VIEWS."+m[1]+" = {") {
			t.Errorf("the sidebar routes to view %q, which VIEWS does not define", m[1])
		}
	}
}

// TestAppShellMountPointIsInMarkup pins the boot guard to the document it
// checks. The guard reads #app-shell and the shell only had class="app-shell",
// so boot() never ran and the whole cockpit was a static page — a failure mode
// so total that it looked like nothing was wrong, because no error is thrown by
// a script that declines to start.
func TestAppShellMountPointIsInMarkup(t *testing.T) {
	src := readAppJS(t)
	if !strings.Contains(src, `getElementById("app-shell")`) {
		t.Fatal("boot() no longer checks a mount point; the guard exists so a stale shell fails loudly")
	}
	index, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatalf("read web/index.html: %v", err)
	}
	if !strings.Contains(string(index), `id="app-shell"`) {
		t.Error(`index.html has no id="app-shell", so boot() returns before rendering anything`)
	}
}

// TestShellHasNoSecondNav is the other half of TestSidebarViewsExist. That test
// proves the rail lists every view; this one proves the rail is the *only* list.
// A stage-level nav was removed because two navigations in one shell let the
// same view be reachable two ways and the rail highlight stopped meaning "where
// am I". The removal was recorded as a grep in the ledger, and a grep cannot
// fail a build — restoring the duplicate nav would have been silent.
func TestShellHasNoSecondNav(t *testing.T) {
	src := readAppJS(t)
	if strings.Contains(src, "workspaceNav") {
		t.Error("app.js declares workspaceNav again; the rail must stay the only navigation or two lists disagree about the current view")
	}
	index, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatalf("read web/index.html: %v", err)
	}
	// The real invariant is not a nav count — the shell legitimately has the
	// breadcrumb nav plus the rail's two groups — it is that no view button
	// lives outside the rail. A second navigation in the stage would satisfy
	// every "does the rail list every view" check while making the highlight
	// ambiguous.
	markup := string(index)
	start := strings.Index(markup, `<aside class="sidebar">`)
	end := strings.Index(markup, "</aside>")
	if start < 0 || end < start {
		t.Fatalf("index.html no longer has a sidebar aside; the rail's home moved and this test cannot locate it")
	}
	rail, outside := markup[start:end], markup[:start]+markup[end:]
	if got := strings.Count(outside, `data-action="nav.view"`); got != 0 {
		t.Errorf("index.html has %d view button(s) outside the sidebar; a second navigation makes the rail highlight ambiguous", got)
	}
	if got := strings.Count(rail, `data-action="nav.view"`); got == 0 {
		t.Error("the sidebar has no view buttons, so the rail renders nothing to navigate with")
	}
}

// TestFragmentsAreNotJoinedToBareStrings is the escape-coverage half of the
// [object Object] guard. join() returning a plain string passes every render
// assertion — the text is correct — but interpolation then escapes it, and the
// operator sees the page's own markup as visible source.
func TestFragmentsAreNotJoinedToBareStrings(t *testing.T) {
	src := readAppJS(t)
	if !strings.Contains(src, "export const join = (fragments) => raw(interpolate(fragments));") {
		t.Error("join() no longer returns a raw fragment; every list() would be double-escaped on insert")
	}
}

// TestEventsAreDispatchedByControlType pins the delegation contract that a
// click-only dispatcher quietly breaks: a <select> still holds its previous
// value when it fires click, so six wired controls act on what the operator was
// leaving rather than what they chose.
func TestEventsAreDispatchedByControlType(t *testing.T) {
	src := readAppJS(t)
	for _, want := range []string{
		`addEventListener("click"`, `addEventListener("change"`, `addEventListener("input"`,
		"controlEventMatches(el, type)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("app.js is missing %s; controls whose value only exists on change/input would be dead", want)
		}
	}
	// Every select and search box the templates emit has to name an action, or
	// it has no handler on any event type at all. Tags are read whole because a
	// control's attributes routinely wrap across lines.
	for _, tag := range controlTags(src) {
		if !strings.Contains(tag, "data-action") {
			t.Errorf("app.js emits a control with no data-action (it would render and do nothing): %s",
				strings.Join(strings.Fields(tag), " "))
		}
	}
}

// controlTags returns every <select> and every search <input> in the source,
// spanning lines the way the templates do.
func controlTags(src string) []string {
	// Comment lines are blanked first: the prose in this file names the very
	// tags the templates emit, and a match inside a comment would report a
	// control that exists in the DOM as one that does not.
	var kept []string
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
			kept = append(kept, "")
			continue
		}
		kept = append(kept, line)
	}
	src = strings.Join(kept, "\n")
	var out []string
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`(?s)<select\b.*?>`),
		regexp.MustCompile(`(?s)<input\b[^>]*type="search"[^>]*>`),
	} {
		for _, m := range re.FindAllString(src, -1) {
			out = append(out, m)
		}
	}
	return out
}

// colWidthPattern matches one column's declared width inside a scoped table
// rule, e.g. `.runs-table .col-when { width: 10rem; }`. %s is the table class.
const colWidthPattern = `\.%s\s+\.col-([a-z]+)\s*\{[^}]*?width:\s*([0-9.]+)rem`

// tableMinWidthPattern matches the scroll floor declared on the table itself.
const tableMinWidthPattern = `\.%s\s*\{[^}]*?min-width:\s*([0-9.]+)rem`

// panelContentRem is how much width a panel actually has on the narrowest
// desktop layout: 1100px is the first breakpoint style.css declares, the nav rail
// is 15rem, and a panel loses 1.125rem on each side to padding plus 1px borders.
// A table whose columns fit inside that needs no scroll floor; one that does not
// must declare it, or its columns get squeezed instead of the wrapper scrolling.
const panelContentRem = (1100.0 - 15.0*16 - 2*1.125*16) / 16.0

// TestRunTableWidthsAddUp guards the arithmetic of a fixed-layout table.
//
// With table-layout: fixed, a declared width is a promise the browser keeps only
// while the table is at least as wide as the sum of its columns. The min-width is
// what makes that true: below it, the browser shrinks every column proportionally
// and the ellipses appear in places the author believed had room. That failure is
// invisible in a screenshot of a wide window and invisible in the source, which is
// how the run table first shipped with a 90rem floor for 96rem of columns: the
// command line — the reason the column exists — rendered as a few clipped pixels
// on every laptop, and nothing anywhere reported it.
//
// The check reads the stylesheet rather than the browser because the numbers are
// all in one file and the relationship between them is arithmetic, not layout.
// columnWidths reads every declared width for one table's columns, keyed by
// column class. A class named by two rules keeps the first, which is the
// unscoped declaration; the second is always a state override (hover, selected)
// and does not resize the column.
func columnWidths(css, table string) map[string]float64 {
	out := map[string]float64{}
	for _, m := range regexp.MustCompile(fmt.Sprintf(colWidthPattern, table)).FindAllStringSubmatch(css, -1) {
		if _, seen := out[m[1]]; seen {
			continue
		}
		out[m[1]], _ = strconv.ParseFloat(m[2], 64)
	}
	return out
}

// sumWidths adds the declared column widths.
func sumWidths(widths map[string]float64) float64 {
	var sum float64
	for _, w := range widths {
		sum += w
	}
	return sum
}

// tableMinWidth reads the scroll floor, or 0 when the table declares none.
func tableMinWidth(css, table string) float64 {
	m := regexp.MustCompile(fmt.Sprintf(tableMinWidthPattern, table)).FindStringSubmatch(css)
	if m == nil {
		return 0
	}
	min, _ := strconv.ParseFloat(m[1], 64)
	return min
}

func TestRunTableWidthsAddUp(t *testing.T) {
	css, err := os.ReadFile("web/style.css")
	if err != nil {
		t.Fatalf("read web/style.css: %v", err)
	}
	for _, table := range []string{"runs-table", "issues-table"} {
		source := string(css)
		widths := columnWidths(source, table)
		if len(widths) == 0 {
			t.Errorf("%s: no column widths found; the regexp no longer matches the stylesheet", table)
			continue
		}
		sum := sumWidths(widths)
		if sum <= panelContentRem {
			// The columns fit the narrowest panel, so there is nothing to scroll to
			// and a min-width would only force a scrollbar where none is needed.
			continue
		}
		min := tableMinWidth(source, table)
		switch {
		case min == 0:
			t.Errorf("%s: %d columns declare %.0frem of widths, more than the %.0frem a panel has at the 1100px breakpoint, and the table sets no min-width — so they are squeezed below their content instead of scrolling",
				table, len(widths), sum, panelContentRem)
		case min < sum:
			t.Errorf("%s: min-width is %.0frem but its %d columns need %.0frem, so every column is clipped proportionally below %.0frem — raise min-width to the sum (or narrow a column) rather than letting the widest one absorb the lie",
				table, min, len(widths), sum, sum)
		}
	}
}

// TestRunTableColumnsAreAllDeclared catches the other half: a column the markup
// emits and the stylesheet never sizes. Under table-layout: fixed an undeclared
// column absorbs the leftover space, which is a deliberate choice for exactly one
// column per table (the free-form message) and an accident everywhere else.
func TestRunTableColumnsAreAllDeclared(t *testing.T) {
	css, err := os.ReadFile("web/style.css")
	if err != nil {
		t.Fatalf("read web/style.css: %v", err)
	}
	src := readAppJS(t)
	// runTable's header is the list of columns the table actually has.
	head := regexp.MustCompile(`(?s)export const runTable = .*?</thead>`).FindString(src)
	if head == "" {
		t.Fatal("app.js no longer has a runTable header to read")
	}
	declared := columnWidths(string(css), "runs-table")
	for _, m := range regexp.MustCompile(`col-([a-z]+)`).FindAllStringSubmatch(head, -1) {
		if _, ok := declared[m[1]]; !ok {
			t.Errorf("the run table emits col-%s but style.css never declares a width for it, so it silently absorbs the leftover space", m[1])
		}
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
