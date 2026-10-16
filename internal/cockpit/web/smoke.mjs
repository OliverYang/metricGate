/*
 * Dev-only end-to-end render driver.
 *
 * render_all.mjs renders the views against fixtures.json, which is written by
 * the same hand as app.js: if the server renames a field and the fixture is
 * updated to match, the test still passes and the page is still broken. This
 * file takes the fixture out of the loop. The payloads come from the real Go
 * handler, the fetch layer is the real one, and the views are the real ones, so
 * a name that exists on one side and not the other renders zero rows.
 *
 * Two modes:
 *   node smoke.mjs --probe        print the requests the frontend would make
 *   node smoke.mjs payloads.json  render against those payloads and assert
 *
 * The probe exists so the request shapes come from the frontend rather than
 * from a test that guesses them: an endpoint renamed, or a parameter the server
 * refuses, is invisible to a fixture written next to the code that reads it.
 */
import { readFileSync } from "node:fs";

/*
 * A DOM stub, deliberately minimal: the point is to run the browser path —
 * render() painting the stage, the rail syncing, the router writing its hash —
 * not to emulate a browser. Each stub member is the one thing a production call
 * touches; anything else throws, which is how an accidental new dependency on
 * the DOM shows up here instead of on an operator's screen.
 */
class Element {
  constructor(id = "") {
    this.id = id;
    this.hidden = false;
    this.innerHTML = "";
    this.textContent = "";
    this.scrollTop = 0;
    this.value = "";
    this.selectionStart = 0;
    this.disabled = false;
    this.dataset = {};
    // Records what the paint asked for instead of applying it, so the rail's
    // active state can be asserted without a real class engine.
    this.classes = new Set();
    this.classList = {
      toggle: (name, on) => (on ? this.classes.add(name) : this.classes.delete(name)),
      add: (name) => this.classes.add(name),
      remove: (name) => this.classes.delete(name),
      was: (name) => this.classes.has(name),
      contains: (name) => this.classes.has(name),
    };
  }
  querySelectorAll() { return rail; }
  focus() {}
  setSelectionRange() {}
}

/** listeners is the registry behind the stub's addEventListener. */
const listeners = {};

/**
 * dispatch runs one event through the delegated handlers boot() registered.
 *
 * This is the only way to test the delegation itself: a data-action attribute
 * that no dispatcher ever reads renders fine, so a string-level check cannot
 * tell a wired control from a dead one. closest() resolves through the same
 * parent the browser would use, so a control nested in a clickable card is
 * addressed the way it is on screen.
 */
function dispatch(type, target) {
  const el = {
    ...target,
    closest: (selector) => (selector === "[data-action]" ? target : null),
  };
  for (const fn of listeners[type] || []) {
    fn({ target: el, key: target.key });
  }
}

/**
 * A location whose hash is a real write, because the router's whole contract is
 * that assigning it round-trips through hashchange back into the same state.
 */
const windowStub = {
  location: {
    origin: "http://127.0.0.1:7788",
    hash: "",
    set hash(value) {
      this._hash = value;
      for (const fn of listeners.hashchange || []) fn();
    },
    get hash() { return this._hash || ""; },
  },
  history: { replaceState: (_d, _t, url) => { windowStub.location._hash = String(url).replace(/^[^#]*/, ""); } },
  addEventListener: (type, fn) => { (listeners[type] ||= []).push(fn); },
};

/** navElements mirrors the sidebar's route table (index.html's nav). */
const navElements = (indexSource) => {
  const views = [...indexSource.matchAll(/data-view="([a-z]+)"/g)].map((m) => m[1]);
  return views.map((view) => {
    const el = new Element();
    el.dataset.view = view;
    return el;
  });
};

const shell = new Element("app-shell");
const stage = new Element("viewStage");
const byId = {
  "app-shell": shell,
  viewStage: stage,
  pageTitle: new Element("pageTitle"),
  pageSubtitle: new Element("pageSubtitle"),
  updatedAt: new Element("updatedAt"),
  logPath: new Element("logPath"),
  refreshBtn: new Element("refreshBtn"),
  errorBanner: new Element("errorBanner"),
  // Header chrome. The breadcrumb moved out of the stage and into the top bar,
  // so the stub needs the element or the paint silently skips it.
  shellCrumb: new Element("shellCrumb"),
};

let rail = navElements(readFileSync(new URL("./index.html", import.meta.url), "utf8"));

globalThis.window = windowStub;
globalThis.document = {
  title: "",
  activeElement: null,
  getElementById: (id) => byId[id] || null,
  // The production selector only ever asks for rail buttons; anything else
  // getting a result here would mean a view reached outside the stage.
  querySelectorAll: (selector) => (selector.includes("[data-view]") ? rail : []),
  addEventListener: windowStub.addEventListener,
};
globalThis.HTMLElement = class HTMLElement {};

/**
 * tick lets whatever boot() started settle before the driver takes over.
 *
 * The stub above is complete enough that app.js genuinely boots — which is the
 * point, an unbootable frontend is the failure this file exists to catch — but
 * that means an in-flight first load is racing every renderView below. Reading
 * the stage mid-flight yields the page from before the data arrived, which looks
 * exactly like a payload whose rows never rendered.
 */
const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

const argv = process.argv.slice(2);
const probe = argv[0] === "--probe";
const payloads = probe ? {} : JSON.parse(readFileSync(argv[0] || "payloads.json", "utf8"));

const requested = new Map();
/*
 * A call count, not just the set of URLs.
 *
 * `requested` is keyed by pathname+search, so a second read of the same
 * endpoint is invisible to it — and a repeated read of the same source window
 * is precisely what the 原因 tab must not cause. Counting calls is what makes
 * "zero reads" mean zero requests rather than zero new URLs.
 */
let requestCount = 0;

/**
 * firstRequest records the first key seen per endpoint.
 *
 * It has to be the frontend's own order rather than the object's: the same
 * endpoint is asked twice once an interaction changes the sort or the search
 * box, and JSON round-trips a Go map in sorted key order, so "whichever entry
 * comes first" picks the q=loop page while the page being asserted on is the
 * one the sort=severity request produced. The counts then disagree over nothing
 * but lookup order.
 */
const firstRequest = new Map();

// The transport stub only. Everything above it — URL building, in-flight
// de-duplication, error normalisation, state writes — is app.js's own code.
globalThis.fetch = async (url) => {
  const parsed = new URL(url);
  const key = parsed.pathname + (parsed.search || "");
  requested.set(key, true);
  requestCount += 1;
  if (!firstRequest.has(parsed.pathname)) firstRequest.set(parsed.pathname, key);
  const body = payloads[key];
  if (body === undefined) {
    return { ok: false, status: 404, async text() { return `{"error":"no payload for ${key}"}`; } };
  }
  return { ok: true, status: 200, async text() { return body; } };
};

/*
 * The import comes after the stub, not before it.
 *
 * app.js ends with `if (isBrowser) boot()`, and the stub above is complete
 * enough that this file is a browser: importing it starts a real first load
 * before a single assertion runs. With the stub installed afterwards that first
 * load goes to node's own fetch, fails against a server that is not listening,
 * and api() parks the rejected promise in its in-flight map under the request
 * URL. The map is keyed by URL, so the first view load below — same endpoint,
 * same key — does not make a request at all; it reuses the failure. The page
 * then paints the error instead of the payload, and only the first scope is
 * affected, because .finally clears the entry and every later scope fetches
 * normally. Installing the stub first removes the race instead of hiding it.
 */
const app = await import("./app.js");

const problems = [];

/** paint drives the real render path and returns what reached the stage. */
function paintedText() { return stage.innerHTML; }
const fail = (message) => problems.push(message);

/**
 * A render throw normally arrives as an unhandled rejection rather than through
 * renderView's catch, because set() calls rerender() without awaiting it. The
 * view that cannot paint does not fail the call that triggered it — it fails
 * later, and node exits before any assertion below runs, which turns one broken
 * page into a stack trace about something else. Recorded as a problem instead:
 * every other view still gets checked, and the report names this one.
 */
process.on("unhandledRejection", (error) => fail(`rejected outside the render path: ${error?.message || error}`));

/** guard runs one step that may throw and records it instead of exiting. */
async function guard(label, step) {
  try {
    await step();
  } catch (error) {
    fail(`${label}: threw ${error.message}`);
  }
}

/**
 * payload reads the captured response for one endpoint, ignoring its query.
 *
 * The order is the frontend's own request order, not the object's key order: an
 * interaction assertion changes the sort or types in the search box, which makes
 * the server answer the same endpoint with a different page. Reading whichever
 * variant happened to serialise first compares the page against the wrong rows.
 */
function payload(path) {
  const key = firstRequest.get(path)
    ?? [...requested.keys()].find((k) => k === path || k.startsWith(path + "?"))
    ?? Object.keys(payloads).find((k) => k === path || k.startsWith(path + "?"));
  if (key === undefined) {
    fail(`no captured payload for ${path}`);
    return {};
  }
  return JSON.parse(payloads[key]);
}

/**
 * count repeats a marker that appears exactly once per rendered unit.
 *
 * The markers are data-action attributes rather than class names, because a
 * class prefix collides: "issue-card" also occurs in "issue-card-head", so a
 * count of three per row reads as three rows and every assertion passes on a
 * page that rendered one.
 */
const count = (text, attribute) => text.split(`data-action="${attribute}"`).length - 1;

/** renderView drives one view's real load, then paints it. */
async function renderView(view, patch = {}) {
  const loaded = app.VIEWS[view];
  if (!loaded) {
    fail(`${view}: not in VIEWS, so the contract names a route with no page`);
    return "";
  }
  let text = "";
  try {
    app.set({ view, ...patch });
    await tick();
    if (loaded.load) await loaded.load(app.filters(app.state));
    // app.render is the browser's paint entry (S8), not the view's own template
    // function: VIEWS[x].render only returns a fragment, so asserting on it
    // would skip the title, the rail sync, the caret restore and the single DOM
    // write — every one of which has been the cause of a blank page.
    app.render(app.state);
    text = paintedText();
  } catch (error) {
    // A throw is reported per view and the run continues: one broken page
    // should not hide whether the other eight render, and in a browser this
    // same throw is what leaves the stage blank with an error in the console.
    fail(`${view}: threw ${error.message}`);
    return "";
  }
  if (text.includes("[object Object]")) fail(`${view}: a fragment was stringified`);
  if (text.includes("undefined</")) fail(`${view}: rendered the literal "undefined"`);
  if (/&lt;(button|td|div|span|tr|p|table|option)\b/.test(text)) fail(`${view}: double-escaped markup`);
  for (const m of text.matchAll(/data-action="([^"]+)"/g)) {
    if (!(m[1] in app.ACTIONS)) fail(`${view}: unhandled data-action ${m[1]}`);
  }
  return text;
}

if (probe) {
  for (const view of app.SPEC.views) await renderView(view);
  for (const key of requested.keys()) console.log(`REQUEST ${key}`);
  process.exit(0);
}

// Every view in the contract renders, in every scope, and renders with data.
//
// The three scopes are the picker, one project, and the unassigned records. All
// three are painted because the workspace views differ by scope rather than by
// route: a template that tests scope for truthiness drops the unassigned
// records (whose scope * is* the empty string), and a view that reads
// projectOf(s) is empty on the picker. The scope has to be cleared between them
// or a leftover breadcrumb passes the check for a view that renders none.
//
// The dashboard is read here because the matrix needs a project root to iterate.
// It is safe to read early: nothing requests that endpoint twice, so there is no
// variant to confuse it with. The rest wait until the matrix has run.
const dash = payload("/api/dashboard");
const projectRoot = (dash.projects || [])[0]?.root || null;
const scopes = [["global", null], ["project", projectRoot], ["unassigned", ""]];
const rendered = {};
for (const view of app.SPEC.views) {
  for (const [label, scope] of scopes) {
    if (label === "project" && !projectRoot) continue;
    app.set({ scope: null });
    const text = await renderView(view, { scope });
    rendered[`${view}:${label}`] = text;
    if (!text || text.length < 40) fail(`${view} (${label}): rendered nothing`);
    // The breadcrumb is header chrome now, so it is asserted on the header: the
    // picker must show no way back, and every scoped page must keep one. A view
    // that used to render its own crumb and no longer does is invisible from the
    // stage text, which is exactly why this check reads shellCrumb.
    const crumb = byId.shellCrumb;
    if (label === "global" && !crumb.hidden) {
      fail(`${view} (${label}): the picker shows a breadcrumb with nowhere to go back to`);
    }
    if (label !== "global" && (crumb.hidden || !crumb.innerHTML.includes("scope.clear"))) {
      fail(`${view} (${label}): no breadcrumb, so the operator has no way back`);
    }
  }
}
/*
 * The remaining payloads are read after the matrix above, not before it:
 * payload() resolves an endpoint to the request the frontend made *first*, and
 * that order only exists once the views have run. Reading early falls back to
 * the object's key order, which a Go map serialises sorted, so the assertion
 * would compare the page against whichever variant happened to sort first —
 * typically the one a later interaction asked for.
 */
const issues = payload("/api/issues");
const measures = payload("/api/measures");
const gate = payload("/api/gate");

/** base reads the global paint: the payload's totals describe the whole store. */
const base = (view) => rendered[`${view}:global`];

// Each assertion below names data the server sent and checks the page shows it.
// A rename on either side turns the count to zero, which is the failure a
// hand-written fixture hides by being updated alongside the code.
if (count(base("projects"), "scope.open") !== (dash.projects || []).length) {
  fail(`projects: ${count(base("projects"), "scope.open")} cards for ${(dash.projects || []).length} projects`
    + `\n--- state.scope=${JSON.stringify(app.state.scope)} dashboard.projects=${JSON.stringify((app.state.dashboard || {}).projects)}`
    + `\n--- text:\n${(rendered.projects || "").slice(0, 400)}`);
}
if ((dash.unassignedCalls || 0) > 0 && !base("projects").includes("unassigned-card")) {
  fail("projects: unassigned calls exist but the door to them is missing");
}
if (count(base("issues"), "issue.select") !== (issues.issues || []).length) {
  fail(`issues: ${count(base("issues"), "issue.select")} rows for ${(issues.issues || []).length} rows in the payload (total ${issues.total})`);
}
if (!(issues.issues || []).length) fail("issues: the seeded store produced no rows to check");
// The window is the server's own statement of what it queried. Shown, not
// assumed: a count that reads as project-scoped while it is not is the kind of
// wrong answer a local audit tool must not give.
if (!issues.window) fail("issues: the server sent no window, so the scope claim cannot be checked");
if (issues.window && !base("issues").includes(issues.window)) {
  fail(`issues: the server reported window "${issues.window}" and the page hides it`);
}
/*
 * The facet rail's 口径, driven through the real request path.
 *
 * The server counts severities and statuses before its filters and rules after
 * them (countRules(filtered) in internal/store/issues_query.go), so a filtered
 * response is not a full window. The frontend caches the unfiltered read and
 * draws the rail from that cache. This asks for a narrowed page and checks the
 * numbers painted are still the cached ones: a rail whose counts collapse
 * toward the selection hides what the filter removed, which is the difference
 * between a triage tool and a control that gaslights the operator.
 */
{
  const drawn = (text) => [...text.matchAll(/class="facet-count">(\d+)</g)].map((m) => m[1]).join(",");
  const full = drawn(base("issues"));
  if (!full) fail("issues: the facet rail drew no counts at all");
  if (!base("issues").includes("不随筛选变化")) {
    fail(`issues: the facet rail does not state that its counts (${full}) are the full window`);
  }
  const probe = Object.keys(issues.severities || {})[0] || "error";
  // The scope is pinned to the one base("issues") was painted in. The matrix
  // loop above leaves state.scope at "unassigned", and a scope change is a new
  // window — the rail is right to refresh there, so leaving it in would make
  // this assert a filter's effect on the wrong window.
  const filtered = await renderView("issues", { severity: probe, scope: null });
  const after = drawn(filtered);
  if (after !== full) {
    fail(`issues: the facet counts moved with the filter — drew ${after}, full window is ${full}`);
  }
  if (app.state.facets === null) {
    fail("issues: the filtered read cleared the cached facet counts instead of keeping them");
  }
  if (filtered.includes("尚无全量计数")) {
    fail("issues: a filtered read with a cache claimed it had no full-window counts");
  }
  await renderView("issues", { severity: "" });
}
// The facet rail is the triage entry point, so its chips have to carry both
// halves of a filter: which facet and which value. A chip missing its value
// clears the filter it was supposed to set, which reads as a dead control.
const chips = [...base("issues").matchAll(/data-action="facet.toggle" data-kind=([a-z]+) data-value=([^\s>]+)/g)];
if (!chips.length) fail(`issues: no facet chip from severities ${JSON.stringify(issues.severities)}`);
for (const [, kind, value] of chips) {
  if (!value || value === '""') fail(`issues: a ${kind} chip carries no value`);
}
const facetTotal = Object.keys(issues.severities || {}).length
  + Object.keys(issues.statuses || {}).length
  + (issues.rules || []).length;
if (chips.length < facetTotal) {
  fail(`issues: ${chips.length} facet chips for ${facetTotal} facet values the server counted`);
}
if ((measures.measures || []).length && !base("overview").includes("measure-card")) {
  fail("overview: measures in the payload, no measure card rendered");
}
/*
 * The live payload carries nine measures and the fixture carries one, so a tile
 * loop that only ever painted the first measure passes the check above and the
 * offline driver alike. Counting against the payload is what closes that gap.
 * Each tile also has to carry its grade box and its threshold line: a tile that
 * drops either still paints a label and a number.
 */
{
  const list = measures.measures || [];
  const cards = (base("overview").match(/class="measure-card"/g) || []).length;
  if (cards !== list.length) {
    fail(`overview: ${list.length} measures in the payload but ${cards} measure card(s) rendered`);
  }
  for (const measure of list) {
    if (!base("overview").includes(`class="measure-grade grade-${measure.grade}"`)) {
      fail(`overview: measure "${measure.id}" rendered without its grade box (grade-${measure.grade})`);
    }
    if (!base("overview").includes(`${measure.warnThreshold} · err ${measure.errThreshold}`)) {
      fail(`overview: measure "${measure.id}" rendered without its threshold line`);
    }
  }
}
/*
 * 「质量运行」一卡必须与运行页列表同数。两处曾经不同源：卡片读
 * `agg.trend.runs`（全局 scope 下是未归属运行数、项目 scope 下是窗口计数），
 * 列表读并集长度。真实载荷里顶层 `runs` 为空而项目有 1 条，于是同一屏上
 * 概览写一个数、运行页写另一个数。这里用载荷自己算期望值。
 */
for (const [label, scope] of scopes) {
  const text = rendered[`overview:${label}`];
  if (!text) continue;
  const st = { dashboard: dash, scope, historyMode: "active" };
  const expected = app.runsInScope(st).runs.length;
  const card = text.match(/<p class="metric-label">质量运行<\/p><p class="metric-value">(\d+)<\/p>/);
  if (!card) {
    fail(`overview (${label}): the 质量运行 card is missing`);
  } else if (Number(card[1]) !== expected) {
    fail(`overview (${label}): the 质量运行 card says ${card[1]} but the run list holds ${expected}`);
  }
}
/*
 * L1 is a two-sided contract and only one side is visible in the markup: the
 * card has to show a short name (a forty-character temp prefix pushes the line
 * number off screen) AND keep the full path reachable (two worktrees both have
 * their own engine.go, so the short name alone is ambiguous). Checking only
 * that fileBase() is called would pass a card that dropped the path entirely,
 * so the assertion is on both halves at once, driven by the payload's own
 * absolute paths rather than a fixture.
 */
{
  const rows = (issues.issues || []).filter((issue) => String(issue.file || "").includes("/"));
  const shown = rows.find((issue) => base("issues").includes(`title="${issue.file}:${issue.startLine}"`));
  if (rows.length && !shown) {
    const sample = rows[0];
    fail(`issues: no card carries the full path of "${sample.file}" in a title, so the short name is the only way back to the file`);
  }
  if (rows.length) {
    const shortName = rows[0].file.split("/").pop();
    if (base("issues").includes(`title="${shortName}:`)) {
      fail(`issues: a card's title is the bare file name "${shortName}", which two worktrees share`);
    }
  }
  /*
   * The other half: the visible text has to be the short name. A title alone
   * passes a card that prints the forty-character temp prefix as its label and
   * pushes the line number off screen — the exact defect L1 removed. The
   * assertion looks for the absolute path as a text node (preceded by `>`), so
   * the same string inside a title attribute is not a hit.
   */
  const absolute = rows.filter((issue) => base("issues").includes(`>${issue.file}:${issue.startLine}<`));
  if (absolute.length) {
    fail(`issues: the card shows the full path as visible text ("${absolute[0].file}:${absolute[0].startLine}"), which is what fileBase() exists to prevent`);
  }
}
/*
 * L8 tripwire: the three fields the overview page reads off the top-level
 * dashboard are exactly the ones ProjectSummary (internal/cockpit/dashboard.go:62-77)
 * does not carry. That is why those reads exist, and the day the project record
 * grows them the reads become a scope mix-up instead of a documented workaround.
 *
 * The payload is checked here rather than in render_all.mjs because render_all
 * only ever sees the hand-written fixture: if the Go struct gains a field, the
 * fixture will not, so the check would keep passing while the defect came back.
 *
 * `accepted` and `metricTrend` are `omitempty`, so an empty ledger omits them
 * entirely. Absence is normal and is deliberately not asserted against — an
 * earlier draft demanded the key and failed on the real payload for that reason.
 */
{
  const project = (dash.projects || [])[0];
  if (project) {
    const leaked = ["accepted", "metricTrend", "summary"].filter((key) => key in project);
    if (leaked.length) {
      fail(`overview: ProjectSummary now carries ${leaked.join(", ")} (internal/cockpit/dashboard.go:62-77) — L8 is fixed, so the top-level reads in app.js must become scope-aware`);
    }
  }
  if (!("summary" in dash)) {
    fail("overview: the dashboard payload carries no summary at all, so the skipped-count read has no source");
  }
}
if (gate.status && !base("overview").includes(gate.status === "passed" ? "PASSED" : gate.status.toUpperCase())) {
  fail(`overview: the gate says "${gate.status}" and no banner carries it`);
}
/*
 * The banner has three states, not two. "unknown" is what the backend reports
 * when no stored run exists (`internal/cockpit/gate_api.go:99`), and giving it
 * the passed styling would turn "never ran here" into a verdict. The assertion
 * reads the class and the glyph rather than the words: the words are l10n, the
 * shapes are the contract.
 */
{
  const strip = (fragment) => (fragment && fragment.__raw ? fragment.value : String(fragment));
  const tone = (g) => (strip(app.gateBanner(g)).match(/gate-banner is-([a-z]+)/) || [])[1];
  const glyph = (g) => (strip(app.gateBanner(g)).match(/gate-glyph[^>]*>([^<]*)</) || [])[1];
  for (const [status, want] of [["passed", "passed"], ["failed", "failed"], ["pending", "unknown"]]) {
    if (tone({ status }) !== want) {
      fail(`gate banner: status "${status}" rendered as is-${tone({ status })}, not is-${want}`);
    }
  }
  if (glyph({ status: "passed" }) === glyph({ status: "failed" })) {
    fail("gate banner: passed and failed share a glyph, so the verdict lives only in the colour");
  }
  if (!/is-unknown/.test(strip(app.gateBanner(null)))) {
    fail("gate banner: a missing gate is not rendered as unknown");
  }
}
/*
 * The gate page against the payload the server really sent.
 *
 * render_all.mjs asserts the same four shapes on a hand-written fixture, which
 * is exactly why this half exists: the fixture carries one condition, and the
 * live payload carries 25 across three language sections. A table that draws
 * only the first group, or drops the headroom column entirely, renders a
 * perfectly valid-looking page and would keep the fixture check green.
 *
 * Every condition in this payload is unmeasured (hasData=false) — the run in
 * the store has no metric snapshots — so the unknown tone is the one that has
 * to show up, and "no data" must not be dressed as a comfortable margin.
 */
if ((gate.conditions || []).length) {
  const page = base("gate");
  const rows = gate.conditions.length;
  const headroom = (page.match(/class="headroom headroom-/g) || []).length;
  if (headroom !== rows) {
    fail(`gate: ${rows} conditions in the payload but ${headroom} headroom cells in the table`);
  }
  // One header per language section, in payload order. The rows are grouped but
  // never merged: two languages can gate the same measure at the same band and
  // they are still two conditions.
  const sections = [];
  const headerRe = /class="gate-lang">([^<]+)</g;
  let hit;
  while ((hit = headerRe.exec(page)) !== null) if (sections[sections.length - 1] !== hit[1]) sections.push(hit[1]);
  const want = [];
  for (const c of gate.conditions) if (c.language && want[want.length - 1] !== c.language) want.push(c.language);
  if (JSON.stringify(sections) !== JSON.stringify(want)) {
    fail(`gate: the payload's language sections are ${JSON.stringify(want)} but the table drew ${JSON.stringify(sections)}`);
  }
  // An unmeasured condition must read as unknown in both columns that describe
  // it, and must never carry the pass badge or the em-dash it used to print.
  const unknown = (page.match(/class="badge unknown"/g) || []).length;
  if (unknown !== rows) {
    fail(`gate: ${rows} conditions have no measurement but only ${unknown} rendered the unknown badge`);
  }
  if (/class="badge pass"/.test(page)) {
    fail("gate: a threshold nobody measured rendered as passed");
  }
  if (/class="cell-unknown">无数据</.test(page) === false) {
    fail("gate: an unmeasured condition does not say 无数据 in the measured column");
  }
  if (page.includes(">—</td>")) {
    fail("gate: an unmeasured condition still draws the em-dash its value used to be");
  }
  // The provenance line and the convention footnote are on this page too, and
  // this is the only payload that carries all four facts.
  const meta = (page.match(/class="gate-meta-item"/g) || []).length;
  if (meta !== 4) fail(`gate: the payload carries four provenance facts but the page drew ${meta}`);
  if (!page.includes("最差单文件")) fail("gate: the panel no longer states the worst-single-file convention");
}
if ((dash.calls || []).length && count(base("calls"), "call.detail") === 0) {
  fail(`calls: ${(dash.calls || []).length} calls in the payload, no trace row rendered`);
}
if ((dash.runs || []).length && count(base("runs"), "run.findings") === 0) {
  fail(`runs: ${(dash.runs || []).length} stored runs, no run row rendered`);
}
if (base("activity").includes("timeline-row") === false && ((dash.runs || []).length || (dash.calls || []).length)) {
  fail("activity: events exist in the payload but the timeline rendered none");
}
// One row is one event, and it must carry all three of its parts: the kind icon,
// the subject, and the relative time. A row that renders the subject and the
// time but no icon looks like an unlabelled list; a row that drops the time
// cannot answer "when" without hovering.
if (base("activity").includes("timeline-row") && (dash.runs || []).length) {
  const oneRow = base("activity").match(/<article class="timeline-row[\s\S]*?<\/article>/);
  if (oneRow && (!/timeline-kind/.test(oneRow[0]) || !/timeline-body/.test(oneRow[0]) || !/timeline-when/.test(oneRow[0]))) {
    fail("activity: a timeline row is missing its kind icon, body or time");
  }
}
// The timeline's runs now come from the same aggregate as the run history, so the
// unassigned scope must show the store's runs and no other run. This is the
// assertion that keeps L3 from coming back: while activityEvents fell back to
// dashboard.runs, a project scope borrowed unassigned runs that carry no
// project identity and showed them beside the project's own ones.
const unassignedRunTitles = (dash.runs || []).map((r) => `运行 #${r.ID ?? r.id}`);
for (const title of unassignedRunTitles) {
  if (!rendered["activity:unassigned"].includes(title)) {
    fail(`activity (unassigned): run "${title}" is in the payload but not in the scope's timeline`);
  }
}
const projectRuns = ((dash.projects || [])[0]?.runs || []);
for (const run of projectRuns) {
  const title = `运行 #${run.ID ?? run.id}`;
  if (rendered["activity:unassigned"].includes(title)) {
    fail(`activity (unassigned): a run with a project identity ("${title}") was drawn under the unassigned scope`);
  }
}
// The timeline is only half of an activity page; the series says which way the
// repository is moving, so a non-empty trend has to draw bars.
// The activity chart reads the scope's series, so each scope is checked against
// the series it is supposed to use: the picker uses the store's, a project uses
// its own, and the unassigned records use the store's again.
const globalTrend = (dash.trend || {}).series || [];
const projectTrend = ((dash.projects || [])[0]?.trend || {}).series || [];
if (globalTrend.length && !base("activity").includes("trend-column")) {
  fail(`activity (global): ${globalTrend.length} trend points, no column rendered`);
}
if (projectTrend.length && !rendered["activity:project"].includes("trend-column")) {
  fail(`activity (project): ${projectTrend.length} trend points, no column rendered`);
}
if (globalTrend.length && !rendered["activity:unassigned"].includes("trend-column")) {
  fail("activity (unassigned): the store's series exists, no column rendered");
}
if (count(base("policy"), "policy.save") !== 1) fail("policy: the editor's save control is missing");
if (!(base("rules").includes("table-empty") || base("rules").includes("<td>"))) {
  fail("rules: neither a row nor an empty state — the table rendered nothing");
}

// A failed request has to become visible. Eighteen places record an error and
// one place shows it, so the wiring between those two is what this checks — a
// cockpit that swallows a refused triage write looks like a dead button.
app.set({ error: "synthetic read failure" });
await guard("error banner", () => app.render(app.state));
if (!byId.errorBanner.textContent.includes("synthetic read failure")) {
  fail("error: a recorded failure never reached #errorBanner");
}
if (byId.errorBanner.hidden) fail("error: the banner is present but stays hidden");
app.set({ error: "" });
await guard("error banner cleared", () => app.render(app.state));
if (!byId.errorBanner.hidden) fail("error: a cleared failure left the banner showing");

// The refresh control has to reflect an in-flight read, the only feedback a
// local tool over a large store can give.
app.set({ loading: true });
await guard("loading paint", () => app.render(app.state));
if (!byId.refreshBtn.disabled) fail("loading: the refresh button stays clickable during a read");
if (!byId.updatedAt.textContent.includes("读取")) fail(`loading: the status label reads "${byId.updatedAt.textContent}"`);
app.set({ loading: false });
await guard("loaded paint", () => app.render(app.state));
if (byId.refreshBtn.disabled) fail("loading: the refresh button stayed disabled after a read");

// The rail marks exactly one entry per view, including the workspace views that
// fold into the project entry.
for (const view of app.SPEC.views) await guard(`nav:${view}`, () => {
  app.set({ view, scope: null });
  app.render(app.state);
  const active = rail.filter((button) => button.classes.has("active"));
  if (active.length !== 1) fail(`nav: view ${view} marks ${active.length} rail entries active`);
});

// One triage click, end to end: the action must read a real fingerprint and
// reach the real /api/source payload through the real fetch layer.
if ((issues.issues || []).length) {
  const fingerprint = issues.issues[0].fingerprint;
  try {
    app.set({ view: "issues", issueTab: "where" });
    await app.ACTIONS["issue.select"]({ dataset: { fingerprint } });
    // issue.select fetches without awaiting; the in-flight de-duplication means
    // a second call returns the same promise, so waiting on it again is the join.
    await app.ensureSource();
    await tick();
    app.render(app.state);
    if (!paintedText().includes("source-line")) {
      fail("issue.select: the code window rendered no lines from the captured /api/source payload");
    }

    /*
     * The detail pane's own location line, the half of L1 the list assertion
     * above cannot see: the pane only exists while an issue is open. Same two
     * sides — short name visible, absolute path reachable in a title — but on
     * a different element, so deleting fileBase() here leaves the list check
     * green.
     */
    {
      const pane = paintedText();
      const opened = (issues.issues || [])[0];
      const at = pane.indexOf('class="issue-detail-loc"');
      const slice = at < 0 ? "" : pane.slice(at, pane.indexOf("</p>", at));
      if (at < 0) {
        fail("issue.select: the detail pane rendered no location line");
      } else {
        if (!slice.includes(`title="${opened.file}"`)) {
          fail(`issue.select: the detail location line dropped the full path of "${opened.file}"`);
        }
        if (!slice.includes(`>${opened.file.split("/").pop()}:${opened.startLine}<`)) {
          fail(`issue.select: the detail location line does not show the short name "${opened.file.split("/").pop()}:${opened.startLine}"`);
        }
      }
    }

    /*
     * The detail pane is a third column beside the list, not a block under it.
     * The class is the assertion because the failure is invisible in the
     * markup: the same pane renders in both shapes, and only the grid decides
     * whether reading it scrolls the cards away.
     */
    if (!paintedText().includes("issues-layout has-detail")) {
      fail("issue.select: an open issue did not switch the layout to three columns");
    }
    app.record({ openFingerprint: null });
    app.render(app.state);
    if (paintedText().includes("issues-layout has-detail")) {
      fail("issue.select: with nothing open the layout still reserves the detail column");
    }
    app.record({ openFingerprint: fingerprint });
    app.render(app.state);

    /*
     * The tab is URL state, so a shared link has to restore it — and restoring
     * it must not cost a source read. The read counter is the assertion that
     * matters: "the tab came back" is satisfied by a fetch that happens to
     * refill the same pane, and that fetch is the one read this file must never
     * spend by accident.
     */
    const before = requestCount;
    // Drop the cached window first. Without this the fingerprint guard alone
    // answers the call and the tab guard is never exercised — the assertion
    // would stay green with the tab check deleted, which is exactly the shape
    // of a test that proves nothing.
    app.record({ source: null });
    app.set({ issueTab: "why" });
    await app.ensureSource();
    if (requestCount !== before) {
      fail("ensureSource: switching to the 原因 tab read the operator's source, which that tab never shows");
    }
    if (!app.encodeRoute(app.state).includes("tab=why")) {
      fail(`ensureSource: the 原因 tab is not in the URL (${app.encodeRoute(app.state)}), so a shared link loses it`);
    }
    // A pasted link: decode the hash the encoder wrote and restore through the
    // same path boot() uses, then confirm the pane came back on the right tab.
    const shared = app.encodeRoute(app.state);
    app.record(app.decodeRoute(`#/${shared}`));
    app.render(app.state);
    if (app.state.issueTab !== "why" || app.state.openFingerprint !== fingerprint) {
      fail(`ensureSource: restoring #/${shared} landed on tab=${app.state.issueTab} open=${app.state.openFingerprint}`);
    }
    if (!paintedText().includes("detail-grid")) {
      fail("ensureSource: a restored link to the 原因 tab rendered no detail pane");
    }
    // Back to 位置: now the read IS owed, because the restored link named a tab
    // that shows source and no window is cached for it any more.
    app.set({ issueTab: "where" });
    app.record({ source: null });
    await app.ensureSource();
    await tick();
    if (!app.state.source || !app.state.source.fingerprint) {
      fail("ensureSource: returning to the 位置 tab did not load the code window");
    }
    /*
     * The second guard. The window is in hand now, so asking for the same
     * issue again must not re-read the operator's file: the tab guard passes
     * (we are on 位置) and only the fingerprint check can answer this.
     */
    const cached = requestCount;
    await app.ensureSource();
    await app.ensureSource();
    if (requestCount !== cached) {
      fail("ensureSource: asking for an issue whose code window is already loaded re-read the file");
    }
  } catch (error) {
    fail(`issue.select: threw ${error.message}`);
  }
}

/*
 * The delegation itself. boot() registered click/change/input on the stub, so
 * dispatching here runs the same path a click does — a data-action that renders
 * but reaches no dispatcher is invisible to every string-level check above.
 */
await guard("delegation", async () => {
  const before = app.state.view;
  dispatch("click", { dataset: { action: "nav.view", view: "issues" } });
  await tick();
  if (app.state.view === before && before !== "issues") fail("delegation: a click on nav.view did not change the view");

  // A <select> has to be read on change and ignored on click, because at click
  // time it still carries the value the operator was leaving.
  const sortBefore = app.state.sort;
  dispatch("click", { tagName: "SELECT", type: "select-one", value: "file", dataset: { action: "sort.set" } });
  await tick();
  if (app.state.sort !== sortBefore) fail("delegation: a click on a select changed the sort from the value being left");
  dispatch("change", { tagName: "SELECT", type: "select-one", value: "file", dataset: { action: "sort.set" } });
  await tick();
  if (app.state.sort === sortBefore) fail("delegation: change on the sort select never reached the handler");

  // The verdict dropdown: the same shape, and the one that writes to the server.
  const verdict = { tagName: "SELECT", type: "select-one", value: "false_positive", dataset: { action: "issue.status", fingerprint: issues.issues[0].fingerprint } };
  dispatch("click", verdict);
  await tick();
  dispatch("change", verdict);
  await tick();

  // A search box answers input, not click.
  dispatch("input", { tagName: "INPUT", type: "search", value: "loop", dataset: { action: "issues.query" } });
  await tick();
  if (app.state.query !== "loop") fail("delegation: typing in the issue search never reached the handler");

  // The URL has to follow the state, and come back from it.
  if (!windowStub.location.hash.includes("view=issues")) {
    fail(`delegation: the hash is "${windowStub.location.hash}" after navigating, so a shared link or a reload lands somewhere else`);
  }
  const encoded = windowStub.location.hash;
  dispatch("click", { dataset: { action: "nav.view", view: "gate" } });
  await tick();
  // Back: the browser hands the earlier hash to the listener, as a real
  // navigation would.
  windowStub.location.hash = encoded;
  await tick();
  if (app.state.view !== "issues") fail(`delegation: returning to ${encoded} landed on ${app.state.view}, not issues`);
});

/*
 * The policy editor's unsaved edit is state, not DOM: a repaint rebuilds the
 * textarea from the server's copy of the document, so an edit that lives only in
 * the element is lost by any unrelated refresh — and in a tool whose whole job is
 * editing a config file, "lost" means the operator believes a threshold they
 * typed is in force.
 */
await guard("policy draft", async () => {
  const server = JSON.parse(payloads[[...firstRequest.values()].find((k) => k.startsWith("/api/policy"))]).content || "";
  const edited = server + "# 未保存的临时调整\n";
  app.record({ policyDraft: edited });
  app.set({ view: "policy" });
  await tick();
  app.render(app.state);
  const text = paintedText();
  if (!text.includes("未保存的临时调整")) {
    fail("policy: a repaint dropped the operator's unsaved edit and showed the server's copy");
  }
  if (!text.includes("有未保存的改动")) fail("policy: an unsaved edit is not marked as unsaved");
  app.record({ policyDraft: null });
});

/*
 * The requests this run made are reported whether or not they were answered.
 * The Go capture reads these lines, answers any it had not predicted, and runs
 * again — so the endpoint set is a fixed point of the frontend's own behaviour
 * rather than a list a test guessed once.
 */
// Printed unconditionally: the capture round needs them even on a run whose
// only other output is a summary line. The Go side logs just the summary.
for (const key of requested.keys()) console.log(`REQUEST ${key}`);
for (const key of requested.keys()) {
  if (payloads[key] === undefined) fail(`the frontend requested ${key}, which was never captured`);
}

if (problems.length) {
  for (const p of problems) console.log(`FAIL ${p}`);
  process.exit(1);
}
console.log(`smoke: ${app.SPEC.views.length} views rendered from ${Object.keys(payloads).length} live payloads ok`);
