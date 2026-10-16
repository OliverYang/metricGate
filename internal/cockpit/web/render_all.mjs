/*
 * Dev-only render driver. Not part of the release artifact: `go:embed web/*`
 * includes it, but nothing imports it and no build step consumes it. It exists
 * so `go test ./internal/cockpit/` can render every view against a fixture
 * without a browser — the S2/S6/S7 sections are pure, so this is the whole
 * browser surface minus the DOM writes.
 *
 * Usage: node render_all.mjs   ->  prints one line per (view, scope, history)
 * combination, or FAIL <...> and exits non-zero.
 */
import { readFileSync } from "node:fs";
import * as app from "./app.js";

const fixtures = JSON.parse(readFileSync(new URL("./fixtures.json", import.meta.url), "utf8"));

const state = () => ({
  view: "projects",
  scope: null,
  issueTab: "where",
  severity: "", status: "", rule: "", file: "", sort: "severity", page: 1, query: "",
  historyMode: "active",
  dashboard: fixtures.dashboard,
  policy: fixtures.policy,
  gate: fixtures.gate,
  measures: fixtures.measures,
  issues: fixtures.issues,
  // rulesSort is what the Rules table's headers read. The driver has to carry
  // it for the same reason it carries `facets`: leaving it out would only ever
  // exercise the undefined-sort path, and the sort is exactly what the rules
  // assertions below test.
  rulesSort: { key: "key", dir: "asc" },
  // facets is what the rail prefers over the response, so the driver has to
  // carry it: leaving it out would only ever exercise the fallback path.
  facets: null,
  selected: new Set(["fp1"]),
  runSelection: new Set(["1"]),
  openFingerprint: "fp1",
  source: {
    fingerprint: "fp1", file: "engine.go", language: "go",
    startLine: 1, endLine: 3, totalLines: 12, anchorLine: 2,
    anchorStart: 1, anchorEnd: 9, truncated: false,
    lines: [
      { number: 1, text: "package engine", class: "key" },
      { number: 2, text: "func loop() {", class: "key" },
      { number: 3, text: "\treturn", class: "key" },
    ],
  },
  callFilters: { query: "", phase: "", status: "", trace: "" },
  metricChoice: "",
  logPath: fixtures.dashboard.logPath,
  loading: false,
  error: "",
  editedAt: "",
});

// The handled set is the real S8 table, not a copy of it. Restating it here is
// what let an unregistered action pass: the list below once claimed to be the
// contract while silently lagging whatever ACTIONS actually holds.
const HANDLED = new Set(Object.keys(app.ACTIONS));

const problems = [];
for (const scope of [null, "/repo/alpha", ""]) {
  for (const view of Object.keys(app.VIEWS)) {
    for (const historyMode of ["active", "archived"]) {
      const s = state();
      Object.assign(s, { view, scope, historyMode });
      // The issue detail pane has one branch per tab; rendering only the
      // default would leave three of the four unexercised.
      const tabs = view === "issues" ? ["where", "why", "activity", "more"] : ["where"];
      for (const tab of tabs) {
        s.issueTab = tab;
        const label = `view=${view} tab=${tab} scope=${JSON.stringify(scope)} history=${historyMode}`;
        try {
          const out = app.VIEWS[view].render(s);
          const text = out && out.__raw ? out.value : String(out || "");
          // A fragment that survived to string conversion shows up as this
          // literal; every list in the UI is a mapped fragment, so this is the
          // regression the check exists for.
          if (text.includes("[object Object]")) {
            const at = text.indexOf("[object Object]");
            problems.push(`${label}: [object Object] rendered near "${text.slice(Math.max(0, at - 80), at + 20)}"`);
          }
          // Every action the view emits must be handled, or it is a dead control.
          for (const m of text.matchAll(/data-action="([^"]+)"/g)) {
            if (!HANDLED.has(m[1])) problems.push(`${label}: unhandled data-action ${m[1]}`);
          }
          if (text.includes("undefined</")) problems.push(`${label}: rendered the literal "undefined"`);
          // Double escaping: a fragment that reached interpolation as a bare
          // string arrives escaped, so the operator reads markup source on the
          // page instead of elements. A tag start is the tell — a template that
          // legitimately escapes source code never emits &lt; followed by a UI
          // tag name.
          if (/&lt;(button|td|div|span|tr|p|table|option)\b/.test(text)) {
            problems.push(`${label}: escaped markup reached the page (double escape)`);
          }
        } catch (error) {
          problems.push(`${label}: threw ${error.message}`);
        }
      }
    }
  }
}

/*
 * The gate table's headroom column. Its whole point is that a condition with no
 * measurement must not read like a comfortable pass, so the four tones are
 * asserted directly instead of trusting the fixture to happen to contain one.
 * A table that silently drops the column renders fine and says nothing, which
 * is why this is a count and not a smoke check.
 */
{
  const row = (c) => String(app.gateHeadroom(c).text);
  const tone = (c) => String(app.gateHeadroom(c).tone);
  const cases = [
    [{ hasData: true, operator: "≤", value: 15, current: 12 }, "余 3", "under"],
    [{ hasData: true, operator: "≤", value: 15, current: 15 }, "恰好压线", "at"],
    [{ hasData: true, operator: "≤", value: 15, current: 17 }, "超出 2", "over"],
    // The one that matters: no measurement is not a margin of zero.
    [{ hasData: false, operator: "≤", value: 15, current: 0 }, "无数据", "unknown"],
  ];
  for (const [c, wantText, wantTone] of cases) {
    if (row(c) !== wantText) problems.push(`gate headroom: ${JSON.stringify(c)} drew "${row(c)}", expected "${wantText}"`);
    if (tone(c) !== wantTone) problems.push(`gate headroom: ${JSON.stringify(c)} tone "${tone(c)}", expected "${wantTone}"`);
  }
  const drawn = String(app.VIEWS.gate.render(Object.assign(state(), { view: "gate" })).value || "");
  const cells = (drawn.match(/class="headroom headroom-/g) || []).length;
  const rows = (fixtures.gate.conditions || []).length;
  if (rows && cells !== rows) {
    problems.push(`gate: ${rows} conditions in the payload but ${cells} headroom cells in the table`);
  }
  // The status column has to disagree between a measured pass and an
  // unmeasured condition. Both used to render the green "通过" badge, which
  // reads as "this threshold was met" when nothing was measured at all.
  const condition = (patch) => Object.assign(
    { measure: "m", label: "M", operator: "≤", value: 15, current: 0, hasData: true, failing: false, source: "policy_file" }, patch);
  const render = (patch) => String(app.VIEWS.gate.render(Object.assign(state(), {
    view: "gate",
    gate: { failOn: "error", status: "passed", reason: "ok", conditions: [condition(patch)] },
  })).value || "");
  const measured = render({});
  const unmeasured = render({ hasData: false });
  if (!measured.includes('class="badge pass"')) {
    problems.push("gate: a measured condition that passes does not render the pass badge");
  }
  if (!unmeasured.includes('class="badge unknown"')) {
    problems.push("gate: an unmeasured condition does not render the unknown badge");
  }
  if (unmeasured.includes('class="badge pass"')) {
    problems.push("gate: an unmeasured condition renders as passed — a threshold nobody measured has not been met");
  }
  if (!/class="cell-unknown">无数据</.test(unmeasured) || unmeasured.includes('>—</')) {
    problems.push("gate: an unmeasured condition still draws the em-dash its value used to be");
  }
  if (!measured.includes(">0</td>") || unmeasured.includes(">0</td>")) {
    problems.push("gate: a measured 0 and an unmeasured value are not distinguishable in the table");
  }
}

/*
 * The verdict's provenance. The point of the line is what it does NOT render:
 * the same three facts are wanted on pages whose payloads do not carry them
 * (L6), so a field that is absent has to produce nothing at all rather than an
 * empty label or a dash. Both halves are asserted because "renders the fields
 * it has" passes trivially when the fixture has all of them.
 */
{
  const meta = (g) => {
    const out = app.gateMeta(g);
    return out && out.__raw ? out.value : String(out || "");
  };
  const items = (g) => (meta(g).match(/class="gate-meta-item"/g) || []).length;
  const full = { failOn: "error", runId: 1, commit: "cafe1234", startedAt: "2026-10-15T13:48:43Z" };
  if (items(full) !== 4) problems.push(`gate meta: four facts in the payload drew ${items(full)} items`);
  if (meta({ runId: 1 }).includes("fail_on") || items({ runId: 1 }) !== 1) {
    problems.push("gate meta: a payload with only runId rendered a label it has no value for");
  }
  if (meta({}) !== "") problems.push(`gate meta: an empty gate drew "${meta({})}", expected nothing`);
  if (meta(null) !== "") problems.push("gate meta: a missing gate drew something");
  if (/<code><\/code>|>—</.test(meta({ failOn: "error" }))) {
    problems.push("gate meta: an absent field left an empty placeholder behind");
  }
  // The fixture carries all four, so the rendered page has to show them.
  const page = String(app.VIEWS.gate.render(Object.assign(state(), { view: "gate" })).value || "");
  if (items(fixtures.gate) !== items(full) || !page.includes("gate-meta")) {
    problems.push("gate: the page does not render the verdict's provenance line");
  }
  // The "worst single file" convention bounds what the numbers in the table
  // mean, so it has to stay with the panel rather than inside the scrolling
  // wrap. The wrap ends at the first </div> after </table> — matching on the
  // first nested </div> instead would end the slice before the note in every
  // layout and report it as absent no matter where it sat.
  const wrapStart = page.indexOf('<div class="table-wrap">');
  const tableEnd = page.indexOf("</table>");
  const wrapEnd = tableEnd < 0 ? -1 : page.indexOf("</div>", tableEnd);
  const wrap = wrapStart < 0 || wrapEnd < 0 ? "" : page.slice(wrapStart, wrapEnd);
  if (!page.includes("最差单文件")) {
    problems.push("gate: the panel no longer states the worst-single-file convention");
  }
  // The convention note also has to match how the column is actually filled.
  // It used to promise one run-wide value per measure; now each language is
  // measured against its own files, and a note that still says "不分语言" would
  // describe the bug the column no longer has.
  if (!page.includes("按语言分别取数")) {
    problems.push("gate: the panel does not say the measurement column is read per language");
  }
  if (page.includes("不分语言")) {
    problems.push("gate: the panel still claims the measurement column is not split by language");
  }
  if (wrap.includes("最差单文件")) {
    problems.push("gate: the convention note is inside .table-wrap, so it scrolls with the columns");
  }
  if (!page.includes('class="panel-foot metric-convention"')) {
    problems.push("gate: the convention note is not a panel footnote");
  }
}

/*
 * The condition table's language grouping. A policy with three language
 * sections gates on three sets of bands, and the payload repeats the same
 * measure once per language; the rows are identical in every column the table
 * had, so "Cyclomatic complexity ≤ 15" appeared three times with nothing
 * saying which one covered which file. Both halves are asserted — a group
 * header that renders but does not carry the band is as useless as none, and
 * a band rendered without its header reads as a duplicate row.
 */
{
  const gate = JSON.parse(JSON.stringify(fixtures.gate));
  gate.conditions = [
    { language: "go", measure: "cyclomatic-complexity", label: "Cyclomatic complexity", operator: "≤", value: 15, current: 12, hasData: true, source: "policy_file" },
    { language: "java", measure: "cyclomatic-complexity", label: "Cyclomatic complexity", operator: "≤", value: 15, current: 3, hasData: true, source: "policy_file" },
    { language: "javascript", measure: "file-length", label: "File length", operator: "≤", value: 4000, current: 0, hasData: false, source: "builtin" },
  ];
  const page = String(app.VIEWS.gate.render(Object.assign(state(), { view: "gate", gate })).value || "");
  // One header per language section. The rows are NOT merged: the same measure
  // with the same band in two languages is still two conditions the checker
  // applies separately, and a header is the only thing that says which is
  // which.
  const groups = (page.match(/class="gate-lang"/g) || []).length;
  if (groups !== 3) problems.push(`gate: three language sections drew ${groups} group headers`);
  if (!page.includes(">go<") || !page.includes(">java<") || !page.includes(">javascript<")) {
    problems.push("gate: a language group header does not name its language");
  }
  // Each language's own measurement has to reach its own row. The two go/java
  // conditions carry the same measure and the same band and differ only in the
  // measured value, so a renderer that collapsed them (or that reprinted one
  // language's value under the other's header) would still pass a
  // "grouping exists" assertion while showing the wrong number. Slicing per
  // section is what makes the value-to-language pairing the thing under test:
  // a bare `page.includes(">12<")` also matches a threshold cell.
  const section = (name) => {
    const at = page.indexOf(`<span class="gate-lang">${name}</span>`);
    if (at < 0) return "";
    const next = page.indexOf('class="gate-lang-row"', at);
    return page.slice(at, next < 0 ? page.length : next);
  };
  const goSection = section("go");
  const javaSection = section("java");
  if (!/<td>12<\/td>/.test(goSection)) {
    problems.push("gate: the go section does not show go's own measured value");
  }
  if (!/<td>3<\/td>/.test(javaSection)) {
    problems.push("gate: the java section does not show java's own measured value");
  }
  if (/<td>3<\/td>/.test(goSection)) {
    problems.push("gate: java's measured value was printed under the go header");
  }
  // The rows must survive the grouping: three conditions, three bands.
  if (!page.includes(">15<") || !page.includes(">4000<")) {
    problems.push("gate: grouping dropped a condition");
  }
  // A payload without the field (an older server) must not print an empty
  // header: an unlabelled group is worse than no grouping.
  const legacy = String(app.VIEWS.gate.render(Object.assign(state(), {
    view: "gate",
    gate: { conditions: [{ measure: "file-length", label: "File length", operator: "≤", value: 800, current: 0, hasData: false }] },
  })).value || "");
  if ((legacy.match(/class="gate-lang"/g) || []).length !== 0) {
    problems.push("gate: a condition with no language still drew a group header");
  }
  if (!legacy.includes("File length")) {
    problems.push("gate: a condition with no language was dropped instead of shown ungrouped");
  }
}

/*
 * The facet rail's counts. This is the one part of the triage page whose
 * correctness is a property of which response they came from, not of the render
 * path, so it gets its own assertions instead of relying on the sweep above:
 * the server counts severities/statuses before the filters and rules after them
 * (countRules(filtered)), so a filtered response must never drive the rail when
 * an unfiltered read is cached, and it must say so when it has to.
 */
{
  // Deliberately different numbers on both sides: the fixture's own counts are
  // 1 and 1, so a collapse would be invisible without a cache the response
  // cannot be confused with.
  const all = {
    severities: { error: 41, warning: 7 },
    statuses: { open: 44, false_positive: 4 },
    rules: [{ RuleID: "cyclomatic-complexity", Count: 33 }, { RuleID: "file-length", Count: 12 }],
  };
  const narrowed = {
    ...fixtures.issues,
    severities: { error: 1 },
    statuses: { open: 1 },
    rules: [{ RuleID: "cyclomatic-complexity", Count: 1 }],
  };
  const rail = (patch) => {
    const s = Object.assign(state(), { issues: narrowed, facets: all }, patch);
    return (app.VIEWS.issues.render(s) || {}).value || "";
  };
  const counts = (text) => [...text.matchAll(/class="facet-count">(\d+)</g)].map((m) => m[1]).join(",");

  // Cached, and a chip is active: every number still comes from the cache.
  const CACHE_COUNTS = "41,7,44,4,33,12";
  const cached = rail({ severity: "error", facets: all });
  if (counts(cached) !== CACHE_COUNTS) {
    problems.push(`facets: a filtered view with a cached read drew ${counts(cached)}, expected ${CACHE_COUNTS}`);
  }
  if (!cached.includes("不随筛选变化")) {
    problems.push("facets: a filtered view with a cached read does not state its 口径");
  }
  // No cache yet: the response is the only number available, and it is a
  // filtered one, so the rail has to admit that instead of implying it is the
  // whole window.
  const stale = rail({ severity: "error", facets: null });
  if (counts(stale) !== "1,1,1") {
    problems.push(`facets: with no cache the rail should draw the response (1,1,1), drew ${counts(stale)}`);
  }
  if (!stale.includes("尚无全量计数")) {
    problems.push("facets: a filtered read with no cache passed itself off as the full count");
  }
  // Unfiltered with no cache yet: the response IS the full window, so it is
  // drawn and no warning is owed.
  const direct = (app.VIEWS.issues.render(Object.assign(state(), { facets: null })) || {}).value || "";
  if (counts(direct) !== "1,1,1,1,1,1") {
    problems.push(`facets: the first unfiltered read should draw the response, drew ${counts(direct)}`);
  }
  if (direct.includes("尚无全量计数")) {
    problems.push("facets: the unfiltered first read carries a warning it has not earned");
  }
  // facetCache is what fetchIssues writes: a payload missing a facet must not
  // turn the cache into undefined and take the whole rail out.
  if (JSON.stringify(app.facetCache({})) !== '{"severities":{},"statuses":{},"rules":[]}') {
    problems.push(`facets: facetCache({}) produced ${JSON.stringify(app.facetCache({}))}`);
  }
}

/*
 * T2.3 — the status control says what is in force, and the range line is a
 * sentence rather than an id.
 *
 * Two separate ways the same fact can be missing. The status is carried by a
 * background tint and nothing else, so a screen reader hears four identical
 * buttons; and the range line is whatever the server put in `window`, which is
 * human-readable only as long as the server keeps rendering it that way — so
 * the assertion pins the sentence the operator reads, not just the field.
 */
{
  const v = (x) => (x && typeof x === "object" ? String(x.value ?? "") : String(x));

  // 1. The live facet is announced, not only painted. A filter that is applied
  // but invisible to a screen reader makes "why are there only 4 issues"
  // unanswerable without sight.
  const active = v(app.facetRow("status", "open", "未处理", 4, true));
  const idle = v(app.facetRow("status", "open", "未处理", 4, false));
  if (!active.includes("is-active")) {
    problems.push("issues status: the live status chip is not marked is-active");
  }
  if (!/aria-current=["']?true/.test(active)) {
    problems.push("issues status: the live status chip carries no aria-current, so the applied filter is sight-only");
  }
  if (/aria-current=["']?true/.test(idle)) {
    problems.push("issues status: an unselected chip announces itself as current");
  }
  if (idle.includes("is-active")) {
    problems.push("issues status: an unselected chip drew the active highlight");
  }

  // 2. The range line reads as a sentence. The payload's `window` is produced
  // by store.Window.String, and the assertion states the contract: what the
  // operator reads is that sentence, with a label saying what it describes.
  const withWindow = Object.assign(state(), { issues: Object.assign({}, fixtures.issues, { window: "last 20 runs, project /repo/alpha" }) });
  const page = v(app.VIEWS.issues.render(withWindow));
  if (!page.includes("范围：")) {
    problems.push("issues range: the header does not label the range line");
  }
  if (!page.includes("last 20 runs")) {
    problems.push("issues range: the range line did not render the server's sentence");
  }
  if (!page.includes('title="服务端实际查询的范围"')) {
    problems.push("issues range: the range line lost its explanation of what it describes");
  }
  // An older server that sends no `window` must still say what the list spans:
  // the header falls back to a label built from the two controls on screen
  // (`issuesWindow` -> `localWindowLabel`), so the line reads as a window, never
  // as an empty "范围：" and never as "unknown".
  const noWindow = Object.assign(state(), { issues: Object.assign({}, fixtures.issues, { window: "" }) });
  const blank = v(app.VIEWS.issues.render(noWindow));
  if (/范围：\s*<\//.test(blank) || /范围：\s*$/.test(blank)) {
    problems.push("issues range: a payload without a window drew an empty range line");
  }
  if (!/\u8303\u56f4\uff1a\u6700\u65b0 \d+ \u6b21\u8fd0\u884c/.test(blank)) {
    problems.push("issues range: a payload without a window did not fall back to the local window label");
  }

  // 3. The scope name is a name. A breadcrumb that prints the raw scope token
  // is the internal id the D2 item is about, so the states are asserted one by
  // one rather than trusted to the fixture happening to hold a friendly root.
  const label = (patch) => app.scopeLabel(Object.assign(state(), patch));
  const cases = [
    [{ scope: "" }, "未归属记录", "unassigned"],
    [{ scope: null }, "全部项目", "everything"],
    [{ scope: "/repo/alpha", dashboard: fixtures.dashboard }, "alpha", "a known project's name, not its path"],
    [{ scope: "/repo/gone", dashboard: fixtures.dashboard }, "未知项目", "a path no project matches"],
  ];
  for (const [patch, want, name] of cases) {
    const got = label(patch);
    if (got !== want) problems.push(`issues scope: ${name} labelled ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
  }
  // The not-yet-loaded state must show the path's last segment rather than the
  // whole token: deep-linking to another page's issues used to print the path.
  const pending = label({ scope: "/Users/somebody/work/checkout", dashboard: null });
  if (pending !== "checkout") {
    problems.push(`issues scope: an unloaded scope labelled ${JSON.stringify(pending)}, want the trailing name "checkout"`);
  }
}

/*
 * The overview page may read exactly three fields off the top-level dashboard.
 * It has to, because the project record the page is otherwise built from does
 * not carry them: ProjectSummary (internal/cockpit/dashboard.go:62-77) has
 * runs/archivedRuns/trend/archivedTrend/findings/archivedFindings only, while
 * summary (:42), accepted (:52) and metricTrend (:46) live on the top-level
 * Dashboard. Reading anything else from s.dashboard inside this segment would
 * mean the page quietly mixes scopes — the defect this iteration exists to
 * remove — so the allow-list is enforced on the source rather than left to
 * review. Every entry must also keep the L8 comment above it that says why.
 *
 * This list is meant to fail loudly: a legitimate change to one of the three
 * reads has to be acknowledged here, and a fourth read has to justify itself.
 */
{
  const source = readFileSync(new URL("./app.js", import.meta.url), "utf8");
  const lines = source.split("\n");
  const start = lines.findIndex((line) => line.startsWith("export const metricCards"));
  const end = lines.findIndex((line) => line.startsWith("VIEWS.policy"));
  if (start < 0 || end <= start) {
    problems.push("overview scope: cannot locate the metricCards..VIEWS.policy segment in app.js");
  } else {
    const ALLOWED = [
      'const accepted = s.dashboard?.accepted || [];',
      'const mt = s.dashboard?.metricTrend || { series: {} };',
      'const skipped = field(field(s.dashboard, "summary") || {}, "skipped");',
    ];
    const hits = [];
    for (let i = start; i < end; i++) {
      if (lines[i].includes("s.dashboard")) hits.push({ line: i + 1, text: lines[i].trim() });
    }
    const found = hits.map((hit) => hit.text).sort();
    if (JSON.stringify(found) !== JSON.stringify(ALLOWED)) {
      problems.push(`overview scope: app.js reads ${JSON.stringify(found)} off s.dashboard, expected exactly ${JSON.stringify(ALLOWED)}`);
    }
    for (const hit of hits) {
      if (!lines[hit.line - 2].includes("L8")) {
        problems.push(`overview scope: app.js:${hit.line} reads s.dashboard with no L8 comment above it`);
      }
    }
  }
}

/*
 * The overview's measure tiles and the trend's wording.
 *
 * Both are "renders fine while saying nothing" defects, which is why neither
 * had an assertion: a tile that drops its A–E box, its threshold line or its
 * exceeded count still paints a label and a number, and a trend caption that
 * calls the unassigned series a global one still paints a caption. The fixture
 * carries exactly one measure, so the counts below are driven by the fixture
 * rather than by a number typed here.
 */
{
  const measures = (fixtures.measures || {}).measures || [];
  const render = (scope) => {
    const s = state();
    Object.assign(s, { view: "overview", scope });
    const out = app.VIEWS.overview.render(s);
    return out && out.__raw ? out.value : String(out || "");
  };
  const global = render(null);
  if (measures.length) {
    const cards = (global.match(/class="measure-card"/g) || []).length;
    if (cards !== measures.length) {
      problems.push(`overview: ${measures.length} measures in the payload but ${cards} measure-card(s) rendered`);
    }
    for (const measure of measures) {
      if (!global.includes(`class="measure-grade grade-${measure.grade}"`)) {
        problems.push(`overview: measure "${measure.id}" did not render its grade box (grade-${measure.grade})`);
      }
      const bands = `warn ${measure.warnThreshold} · err ${measure.errThreshold}`;
      if (!global.includes(bands)) {
        problems.push(`overview: measure "${measure.id}" did not render its threshold line ("${bands}")`);
      }
      if (measure.exceeded > 0 && !global.includes(`${measure.exceeded} 个位置超阈`)) {
        problems.push(`overview: measure "${measure.id}" exceeded ${measure.exceeded} position(s) but the tile does not say so`);
      }
    }
  } else {
    problems.push("overview: fixtures carry no measures, so the measure tile assertions cannot run");
  }
  /*
   * 「质量运行」一卡必须与「质量运行」页的列表同数。
   *
   * 两者曾经不同源：卡片读 `agg.trend.runs`（全局 scope 下是未归属运行数、
   * 项目 scope 下是窗口计数），列表读 `runsInScope(s).runs.length`。夹具里顶层
   * trend.runs=9 而并集 runs 只有 1 条，于是概览写 9、运行页写 1 条——同一份
   * 数据同一屏两个数字。断言取列表的算法算期望值，不写死数字，所以夹具改大
   * 小也不会失效。
   */
  for (const scope of [null, "/repo/alpha", ""]) {
    for (const historyMode of ["active", "archived"]) {
      const st = state();
      Object.assign(st, { view: "overview", scope, historyMode });
      const out = app.VIEWS.overview.render(st);
      const text = out && out.__raw ? out.value : String(out || "");
      const expected = app.runsInScope(st).runs.length;
      const card = text.match(/<p class="metric-label">质量运行<\/p><p class="metric-value">(\d+)<\/p>/);
      if (!card) {
        problems.push(`overview: the 质量运行 card is missing at scope=${JSON.stringify(scope)} history=${historyMode}`);
      } else if (Number(card[1]) !== expected) {
        problems.push(`overview: the 质量运行 card says ${card[1]} at scope=${JSON.stringify(scope)} history=${historyMode} but the run list holds ${expected}`);
      }
    }
  }
  // The caption is the whole point of standard 5: the series the picker
  // receives is the store's unassigned one, not a merged global one, and the
  // caption has to say so — otherwise the workspace landing reads as a total
  // the payload never sent. A project scope has its own series and must not
  // borrow the wording.
  const unassigned = render("");
  const project = render("/repo/alpha");
  if (!unassigned.includes("未归属")) {
    problems.push("overview: the unassigned scope's trend caption does not say the series is unassigned");
  }
  if (!global.includes("未归属")) {
    problems.push("overview: the picker's trend caption does not say the series it draws is the store's unassigned one");
  }
  if (project.includes("未归属")) {
    problems.push("overview: a project scope's trend caption calls its own series unassigned");
  }
}

/*
 * The Rules table's sortable headers.
 *
 * The page lists every effective key — 138 of them in this repository, all in
 * the server's alphabetical order — so a table locked to that order cannot
 * answer "which of these did this file not write", which is the one question
 * the 出处 column exists for. The sort therefore has three properties worth
 * asserting rather than trusting: every column is a control, exactly one
 * column advertises its state via aria-sort, and clicking actually reorders
 * the rows. A header that renders an arrow while the rows stay put is the same
 * lie either way, and it is invisible in a screenshot.
 */
{
  const RULES_KEYS = [
    { key: "zzz.tolerance", source: "this policy", explicit: true, value: "3" },
    { key: "aaa.first", source: "builtin", explicit: false, value: "" },
    { key: "mmm.middle", source: "preset:strict", explicit: true, value: "1" },
  ];
  const render = (sort) => (app.VIEWS.rules.render({ policy: { keys: RULES_KEYS }, rulesSort: sort }) || {}).value || "";
  const thStates = (sort) => {
    const out = render(sort);
    const states = {};
    for (const m of out.matchAll(/<th aria-sort="([a-z]+)"\s*><button[^>]*data-key="(\w+)"[^>]*><span>[^<]*<\/span><i class="sort-arrow">(\S+)<\/i>/g)) {
      states[m[2]] = { sort: m[1], arrow: m[3] };
    }
    return states;
  };
  const rows = (sort) => {
    const out = render(sort);
    return [...out.matchAll(/<tr><td>([^<]*)<\/td><td>([^<]*)<\/td><td>([^<]*)<\/td>/g)]
      .map((m) => m.slice(1).join("|"));
  };

  // Exactly one column advertises its state; the rest say none. Rendering once
  // with 键 active and reading all three is the honest check — asking each
  // column in turn while the active one moves is how the header ends up
  // asserting against itself.
  {
    const arrows = (sort) => {
      const flat = {};
      for (const [key, v] of Object.entries(thStates(sort))) flat[key] = v.arrow;
      return flat;
    };
    // The glyph is not decoration: app.js documents it as "for the reader who
    // does not trust an attribute", so a header whose aria-sort says descending
    // while the arrow points up is that lie. Binding the two here also pins the
    // inactive glyph, otherwise a column nobody sorted can advertise an arrow.
    const arrowStates = arrows({ key: "key", dir: "asc" });
    if (arrowStates.key !== "↑") {
      problems.push(`rules: the ascending active column draws ${arrowStates.key}, not ↑`);
    }
    const descArrows = arrows({ key: "key", dir: "desc" });
    if (descArrows.key !== "↓") {
      problems.push(`rules: the descending active column draws ${descArrows.key}, not ↓ (aria-sort and the glyph disagree)`);
    }
    for (const [key, glyph] of Object.entries(descArrows)) {
      if (key !== "key" && glyph !== "↕") {
        problems.push(`rules: inactive column ${key} advertises an arrow (${glyph}) it did not sort by`);
      }
    }
  }

  {
    const states = thStates({ key: "key", dir: "asc" });
    if (states.key.sort !== "ascending") {
      problems.push(`rules: the active column advertises aria-sort=${states.key.sort}, not ascending`);
    }
    for (const inactive of ["value", "source"]) {
      if (states[inactive].sort !== "none") {
        problems.push(`rules: inactive column ${inactive} advertises aria-sort=${states[inactive].sort}`);
      }
    }
    const descStates = thStates({ key: "key", dir: "desc" });
    if (descStates.key.sort !== "descending") {
      problems.push(`rules: the active column advertises aria-sort=${descStates.key.sort}, not descending`);
    }
  }

  const asc = rows({ key: "key", dir: "asc" });
  const desc = rows({ key: "key", dir: "desc" });
  if (asc.length !== 3) problems.push(`rules: 3 keys produced ${asc.length} rows`);
  if (asc[0] !== "aaa.first|—|内置默认值") {
    problems.push(`rules: ascending order starts at ${asc[0]}, expected aaa.first`);
  }
  if (JSON.stringify(desc) !== JSON.stringify([...asc].reverse())) {
    problems.push(`rules: descending is not the reverse of ascending (${desc.join(",")})`);
  }

  // 取值 and 出处 are derived columns: the value is a display string that is
  // empty for every key today (L10) and the source goes through
  // policySourceLabel. Sorting has to follow what the cell shows, so an empty
  // value sorts first ascending rather than the rows keeping their old order.
  const byValue = rows({ key: "value", dir: "asc" });
  if (byValue[0].split("|")[1] !== "—") {
    problems.push(`rules: sorting by 取值 does not put the empty value first (${byValue[0]})`);
  }
  // The real property for 出处 is that the derived label drives the order — the
  // rows must move off their alphabetical key order. Pinning an exact position
  // would just restate the zh pinyin collation (来 < 内置), which is the
  // locale's business, not the sort's.
  const bySource = rows({ key: "source", dir: "asc" });
  if (JSON.stringify(bySource) === JSON.stringify(asc)) {
    problems.push("rules: sorting by 出处 leaves the rows in key order — the derived label is ignored");
  }

  // The click handler is what makes the header a control. A second click on the
  // same column reverses; a click on another column replaces the order.
  if (typeof app.ACTIONS["rules.sort"] !== "function") {
    problems.push("rules: ACTIONS has no rules.sort, so the headers render but do nothing");
  } else {
    const flip = (from, key) => {
      const before = app.getState().rulesSort;
      app.set({ rulesSort: before });
      app.ACTIONS["rules.sort"]({ dataset: { key } });
      const after = app.getState().rulesSort;
      if (from.key === key) return after.dir === "desc" ? "reversed" : "kept";
      return after.key === key ? "moved" : "stayed";
    };
    if (flip({ key: "key", dir: "asc" }, "key") !== "reversed") {
      problems.push("rules: a second click on the active column does not reverse the order");
    }
    if (flip({ key: "key", dir: "asc" }, "value") !== "moved") {
      problems.push("rules: clicking another column does not become the active sort");
    }
    app.set({ rulesSort: { key: "key", dir: "asc" } });
  }
}

/*
 * 活动页趋势图的刻度与空态（契约 04-2）。
 *
 * 这类缺陷的特征是「照样画得出来」：删掉峰值说明、删掉图例上的量纲，柱子和
 * 坐标都还在，页面看起来没坏，但读者无从知道一根柱代表什么。所以断言不能只
 * 数 trend-column，得盯住那两句解释性文字。夹具的 project trend 有 2 个点、
 * 顶层 trend 是空序列，两个分支都要走到。
 */
{
  const renderActivity = (scope) => {
    const st = state();
    Object.assign(st, { view: "activity", scope });
    const out = app.VIEWS.activity.render(st);
    return out && out.__raw ? out.value : String(out || "");
  };
  const projectSeries = ((fixtures.dashboard.projects || [])[0]?.trend || {}).series || [];
  if (projectSeries.length) {
    const withChart = renderActivity("/repo/alpha");
    if (!withChart.includes("trend-chart")) {
      problems.push(`activity: the fixture carries ${projectSeries.length} trend points but no chart was rendered`);
    }
    // 峰值：算出夹具的真实最大值，再要求它出现在说明与图例上。写死数字会让
    // 夹具一改就失效，而这条断言的全部价值就是「数字来自数据」。
    const peak = Math.max(1, ...projectSeries.map((p) =>
      Number(p.Errors || 0) + Number(p.Warnings || 0) + Number(p.Infos || 0)));
    if (!withChart.includes("chart-caption")) {
      problems.push("activity: the trend chart renders without its caption, so a bar has no stated unit");
    } else if (!new RegExp(`纵轴峰值 <strong>${peak}</strong>`).test(withChart)) {
      problems.push(`activity: the caption does not state the peak (${peak}) the bars are drawn against`);
    }
    if (!withChart.includes("legend-scale")) {
      problems.push("activity: the legend does not repeat the chart's peak, so the axis has no scale in words");
    }
  } else {
    problems.push("activity: the fixture carries no project trend points, so the scale assertions cannot run");
  }
  // 空态：没有事件时必须给一句解释，而不是留一片空白。空态由 events.length
  // 驱动而不是由 trend 驱动，所以这里把载荷里的运行与调用都清掉再渲染。
  const emptyState = state();
  Object.assign(emptyState, {
    view: "activity",
    scope: null,
    dashboard: Object.assign({}, fixtures.dashboard, { runs: [], calls: [], projects: [], unassignedCalls: [] }),
  });
  const noEvents = app.VIEWS.activity.render(emptyState);
  const noEventsText = noEvents && noEvents.__raw ? noEvents.value : String(noEvents || "");
  if (!noEventsText.includes("还没有事件")) {
    problems.push("activity: a scope with no runs and no calls renders no empty state, so the page is just blank");
  }
}

// Router round trip. The property that matters is stability: decoding an
// encoded route must produce the same route state, because the back button
// re-reads the hash the encoder wrote. Defaults are omitted by design, so the
// comparison fills them in on both sides rather than demanding byte equality.
//
// Two assertions, because they catch different mistakes:
//
//  1. Per-row, against a hand-written non-default value for each key. This is
//     the half that catches a row *dropped from the table*: with the row gone
//     the encoder never writes the parameter, so the value is lost while both
//     sides of a table-driven comparison agree (both read the same shorter
//     table). Dropping the `severity` row used to pass every check here.
//  2. Table-driven, so a newly added row is covered the day it lands without
//     anyone remembering to add it to the list above.
const routeSamples = {
  view: "issues", scope: "/repo/alpha", severity: "error", status: "open",
  rule: "file-length", file: "engine.go", query: "loop", sort: "file",
  historyMode: "archived", page: 3, limit: 60,
  openFingerprint: "fp1", issueTab: "activity",
};
// The expected parameter names, written out here on purpose.
//
// A loop over app.ROUTE_KEYS cannot catch a row *deleted* from that table: the
// loop iterates the shorter table and agrees with itself. Deleting the
// `severity`, `sort`, `page`, `rule` and `tab` rows one at a time all left this
// block green. The list below is the independent copy — the shareable state the
// router promises — and the table has to contain it.
const EXPECTED_ROUTE_PARAMS = [
  "view", "scope", "limit", "severity", "status", "rule", "file", "q", "sort",
  "history", "page", "open", "tab",
];
const actualParams = app.ROUTE_KEYS.map(([param]) => param);
for (const param of EXPECTED_ROUTE_PARAMS) {
  if (!actualParams.includes(param)) {
    problems.push(`router: ROUTE_KEYS no longer carries "${param}", so that state can no longer be shared or reloaded`);
  }
}
for (const param of actualParams) {
  if (!EXPECTED_ROUTE_PARAMS.includes(param)) {
    problems.push(`router: ROUTE_KEYS carries "${param}", which this contract does not list; add it above if it is really shareable state`);
  }
}
for (const [param, key] of app.ROUTE_KEYS) {
  const value = routeSamples[key];
  if (value === undefined) {
    problems.push(`router: no sample value for ROUTE_KEYS row ${param} -> ${key}; add one so a dropped row is caught`);
    continue;
  }
  const seed = Object.assign(state(), { [key]: value });
  const encoded = app.encodeRoute(seed);
  if (!new URLSearchParams(encoded).has(param)) {
    problems.push(`router: ${key}=${JSON.stringify(value)} never reaches the URL (no ${param}= in "${encoded}")`);
    continue;
  }
  const back = app.decodeRoute(`#/${encoded}`);
  if (String(back[key]) !== String(value)) {
    problems.push(`router: ${param} round trip changed ${key} ${JSON.stringify(value)} -> ${JSON.stringify(back[key])}`);
  }
}
// Same two halves for the call-trace filters, which are a second table beside
// ROUTE_KEYS ("status" names both an issue verdict and a call verdict, so only
// namespacing the second keeps a shared link from overwriting the first).
const EXPECTED_CALL_PARAMS = ["cq", "phase", "callStatus", "trace"];
const actualCallParams = app.CALL_PARAM_KEYS.map(([param]) => param);
for (const param of EXPECTED_CALL_PARAMS) {
  if (!actualCallParams.includes(param)) {
    problems.push(`router: CALL_PARAM_KEYS no longer carries "${param}", so that call filter can no longer be shared`);
  }
}
for (const param of actualCallParams) {
  if (!EXPECTED_CALL_PARAMS.includes(param)) {
    problems.push(`router: CALL_PARAM_KEYS carries "${param}", which this contract does not list`);
  }
}
for (const [param, key] of app.CALL_PARAM_KEYS) {
  const value = `sample-${param}`;
  const seed = Object.assign(state(), { callFilters: Object.assign({}, state().callFilters, { [key]: value }) });
  const encoded = app.encodeRoute(seed);
  if (!new URLSearchParams(encoded).has(param)) {
    problems.push(`router: call filter ${key} never reaches the URL (no ${param}= in "${encoded}")`);
    continue;
  }
  const back = app.decodeRoute(`#/${encoded}`);
  const got = (back.callFilters || {})[key];
  if (String(got) !== String(value)) {
    problems.push(`router: call filter ${param} round trip changed ${key} ${JSON.stringify(value)} -> ${JSON.stringify(got)}`);
  }
}

// The code window's anchor marks, which nothing else can see.
//
// `issue.select` + `ensureSource` is the only path that fills state.source, and
// it is a fetch — render_all has no network, so the fixture supplies the window
// directly. That makes this the one place the anchor rendering is pinned: the
// highlight class, the line's own repeat of the issue message, and the
// truncation notice all live here and nowhere else. Deleting any of the three
// leaves a window that renders, scrolls and reads plausibly.
{
  const s = state();
  Object.assign(s, { view: "issues", issueTab: "where" });
  const text = (app.VIEWS.issues.render(s).__raw ? app.VIEWS.issues.render(s).value : "");
  const anchorLine = s.source.anchorLine;
  // The class list carries the syntax tier after the anchor mark
  // (`source-line is-anchor hl-key`), so the match is on the prefix.
  const anchorRows = text.match(/class="source-line[^"]*\bis-anchor\b/g) || [];
  if (anchorRows.length !== 1) {
    problems.push(`source window: ${anchorRows.length} lines carry is-anchor, but the payload names one anchor line (${anchorLine})`);
  }
  if (!text.includes('<span class="source-anchor-note">')) {
    problems.push("source window: the anchor line does not repeat the issue message, so the reader has to look away from the line");
  }
  // The notice is conditional on the payload's own flag; both sides are pinned
  // because a hardcoded "· 已截断" reads as data and a dropped one hides a cut.
  const truncated = Object.assign(state(), { view: "issues", issueTab: "where", source: Object.assign({}, s.source, { truncated: true }) });
  const cutText = app.VIEWS.issues.render(truncated).value;
  if (!cutText.includes("· 已截断")) {
    problems.push("source window: a truncated window does not say so, so the reader cannot tell the file continues");
  }
  if (text.includes("· 已截断")) {
    problems.push("source window: a complete window claims to be truncated");
  }

  // Every line carries its number, and the payload's own tier class reaches the
  // row. The window is read to answer "which line fired this", so a window that
  // rendered the code without numbers, or dropped `hl-*`, still looks like a
  // code viewer while answering nothing.
  const numbers = text.match(/class="source-no">\s*\d+/g) || [];
  if (numbers.length !== s.source.lines.length) {
    problems.push(`source window: ${numbers.length} numbered lines for ${s.source.lines.length} payload lines`);
  }
  const tiered = s.source.lines.filter((l) => l.class);
  if (tiered.length && !tiered.every((l) => text.includes(`hl-${l.class}`))) {
    problems.push("source window: a line's tier class never reached its row, so the highlight is unpainted");
  }
  if (text.includes("hl-") === false) {
    problems.push("source window: no line carries a highlight class at all");
  }

  // A window with no lines is a state, not an empty frame. The server sends
  // `lines: []` when the file is gone or unreadable, and the first version of
  // this view drew "第 0-0 行 / 共 0 行" above an empty <ol> — which reads as a
  // file whose content is nothing, not a window that failed to fill.
  const hollow = Object.assign(state(), { view: "issues", issueTab: "where", source: Object.assign({}, s.source, { lines: [], startLine: 0, endLine: 0, totalLines: 0, anchorLine: 0 }) });
  const hollowText = app.VIEWS.issues.render(hollow).value;
  if (!hollowText.includes("代码窗口为空")) {
    problems.push("source window: an empty window did not say it is empty");
  }
  if (hollowText.includes("<ol class=\"source-lines\">")) {
    problems.push("source window: an empty window still drew the list frame, which reads as an empty file");
  }
  if (/\u7b2c 0-0 行/.test(hollowText)) {
    problems.push("source window: an empty window printed \"第 0-0 行\" as if it were a range");
  }
  // The reason branch keeps priority: a moved file is explained, not called empty.
  const moved = Object.assign(state(), { view: "issues", issueTab: "where", source: { fingerprint: "f", reason: "文件已移动", lines: [] } });
  const movedText = app.VIEWS.issues.render(moved).value;
  if (!movedText.includes("代码窗口不可用") || !movedText.includes("文件已移动")) {
    problems.push("source window: a payload with a reason was not reported as unavailable");
  }
}

/*
 * T2.1 — verdict, status column, and the jump from one to the other.
 *
 * These fail together, which is why they are asserted in one block: a banner
 * whose three states share a class name, a status column that paints a
 * condition sitting in the warning band the same green as a comfortable one,
 * and a locate control whose target row the table does not render. The last
 * is the one a screenshot cannot catch — button exists, row exists, anchor
 * points elsewhere.
 *
 * html`` returns an object, so every rendered value is read through `.value`
 * exactly as the sweep above does.
 */
{
  const v = (x) => (x && typeof x === "object" ? String(x.value ?? "") : String(x));
  const renderGate = (gate) => v(app.VIEWS.gate.render(Object.assign(state(), { view: "gate", gate })));

  // 1. Three verdicts, three distinct classes. A shared class would render the
  // same picture for "no run here" and "this failed", which is the mistake a
  // grey UNKNOWN state exists to prevent.
  const verdicts = [
    { name: "passed", want: "is-passed", gate: { status: "passed", reason: "所有条件均通过", runId: 42, conditions: [] } },
    { name: "failed", want: "is-failed", gate: { status: "failed", reason: "1 项超出阈值", runId: 43, conditions: [{ language: "go", measure: "cyclomatic-complexity", operator: "≤", value: 12, current: 15, hasData: true, failing: true }] } },
    { name: "no-data", want: "is-unknown", gate: { status: "unknown", reason: "没有可用的运行记录，无法给出结论", conditions: [] } },
  ];
  const seenClass = new Map();
  for (const c of verdicts) {
    const page = renderGate(c.gate);
    if (!page.includes(`class="gate-banner ${c.want}"`)) {
      problems.push(`gate banner: ${c.name} did not draw ${c.want}`);
    }
    if (seenClass.has(c.want)) {
      problems.push(`gate banner: ${c.name} and ${seenClass.get(c.want)} share the class ${c.want}`);
    }
    seenClass.set(c.want, c.name);
  }
  if (seenClass.size !== verdicts.length) {
    problems.push(`gate banner: drew ${seenClass.size} distinct classes, want ${verdicts.length}`);
  }

  // 2. The status column's four badges are three colours plus a grey. The
  // warning tone is the one that had no input at all before gateBand returned
  // a warn band, so an over-warning row was indistinguishable from a pass.
  const tone = (label) => (v(app.badge(label)).match(/class="badge ([a-z]+)"/) || [])[1];
  const labels = [["发现问题", "error"], ["接近阈值", "flagged"], ["通过", "pass"], ["无数据", "unknown"]];
  const byTone = new Map();
  for (const [label, want] of labels) {
    const got = tone(label);
    if (got !== want) problems.push(`badge: ${label} drew tone ${got}, want ${want}`);
    if (byTone.has(got)) problems.push(`badge: ${label} and ${byTone.get(got)} share tone ${got}`);
    byTone.set(got, label);
  }

  // 3. The row picks its badge from the flags rather than from a default. The
  // warning row showing 通过 is the exact reading the table exists to prevent.
  const cond = (extra) => Object.assign({ language: "go", measure: "m", label: "M", operator: "≤", value: 10, source: "builtin" }, extra);
  const rowHas = (extra, label) => v(app.gateConditionRows([cond(extra)])).includes(label);
  const rowCases = [
    [{ current: 12, hasData: true, failing: true }, "发现问题", "failing"],
    [{ current: 9, hasData: true, warning: true }, "接近阈值", "warning"],
    [{ current: 3, hasData: true }, "通过", "passing"],
    [{ hasData: false }, "无数据", "unmeasured"],
  ];
  for (const [extra, label, name] of rowCases) {
    if (!rowHas(extra, label)) problems.push(`gate status: the ${name} row did not draw ${label}`);
  }
  if (rowHas({ current: 9, hasData: true, warning: true }, "通过")) {
    problems.push("gate status: a warning row also drew 通过 — the two bands are not distinguished");
  }
  if (rowHas({ hasData: false }, "通过")) {
    problems.push("gate status: an unmeasured row drew 通过");
  }

  // 4. The locate wiring: the banner's target must be a row id the table
  // actually renders, and it must be the row that failed — not the first row.
  // The warning row is deliberately listed first so a "first condition wins"
  // implementation fails here.
  const failing = cond({ measure: "cyclomatic-complexity", current: 15, hasData: true, failing: true });
  const warning = cond({ language: "java", measure: "file-length", current: 9, hasData: true, warning: true });
  const failedGate = { status: "failed", reason: "1 项超阈", runId: 43, conditions: [warning, failing] };
  const page = renderGate(failedGate);
  const target = app.gateLocateTarget(failedGate);
  if (target !== app.conditionId(failing)) {
    problems.push(`gate locate: target ${JSON.stringify(target)}, want the failing row ${app.conditionId(failing)}`);
  }
  if (!page.includes(`data-action="gate.locate" data-target="${target}"`)) {
    problems.push(`gate locate: the banner has no locate control aimed at ${target}`);
  }
  if (!page.includes(`id="${target}"`)) {
    problems.push(`gate locate: the banner targets ${target} but no row renders that id`);
  }
  if (app.conditionId({ language: "go", measure: "m" }) === app.conditionId({ language: "java", measure: "m" })) {
    problems.push("gate locate: two languages share one anchor id, so a jump can land on the other language's row");
  }
  // A passing verdict has no row to blame: no control, not a dead one.
  const passedGate = { status: "passed", reason: "ok", conditions: [cond({ current: 3, hasData: true })] };
  if (renderGate(passedGate).includes('data-action="gate.locate"')) {
    problems.push("gate locate: a passing gate offers a jump to a row that triggered nothing");
  }
  if (app.gateLocateTarget(passedGate) !== "") {
    problems.push("gate locate: a passing gate reported a target");
  }
}

// The table-driven half: a whole route, through the encoder and back.
const routeKeys = app.ROUTE_KEYS.map(([, key]) => key);
const fieldValue = (state, key) => {
  const v = state[key];
  return v === undefined ? null : v;
};
const ROUTE_HASHES = [
  "#/",
  "#/view=issues",
  "#/view=runs&scope=/repo/alpha&history=archived",
  "#/view=calls&q=check&phase=verify&status=error&trace=t1",
  "#/view=issues&severity=error&status=open&sort=file&page=3",
  // The detail pane's own state. `open` alone and `open`+`tab` are separate
  // rows because the tab is meaningful only beside an issue, and a round trip
  // that silently dropped one of the two would still pass the other.
  "#/view=issues&open=f0843fbe1dce6fec",
  "#/view=issues&open=f0843fbe1dce6fec&tab=activity",
];
for (const hash of ROUTE_HASHES) {
  const first = Object.assign(state(), app.decodeRoute(hash));
  const encoded = app.encodeRoute(first);
  const second = Object.assign(state(), app.decodeRoute(`#/${encoded}`));
  for (const key of routeKeys) {
    const a = fieldValue(first, key);
    const b = fieldValue(second, key);
    // Page is a number on one side and a parsed string on the other, so compare
    // the wire form: what matters is that the second decode produces the same
    // screen, not that the two JS values share a type.
    if (String(a) !== String(b)) {
      problems.push(`router: ${hash} changed ${key} ${JSON.stringify(a)} -> ${JSON.stringify(b)}`);
    }
  }
  const callA = first.callFilters || {};
  const callB = second.callFilters || {};
  for (const key of ["query", "phase", "status", "trace"]) {
    if (String(callA[key] || "") !== String(callB[key] || "")) {
      problems.push(`router: ${hash} changed call filter ${key} ${JSON.stringify(callA[key])} -> ${JSON.stringify(callB[key])}`);
    }
  }
}

if (problems.length) {
  for (const p of problems) console.log(`FAIL ${p}`);
  process.exit(1);
}
console.log(`render_all: ${Object.keys(app.VIEWS).length} views x 3 scopes x 2 modes (+4 issue tabs) and ${ROUTE_HASHES.length} router round trips ok`);
