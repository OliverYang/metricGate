/*
 * metricGate cockpit — single-file frontend, zero build step.
 *
 * The file is organised in eight fixed sections (S1..S8 below). The order is a
 * layering rule, not a habit: S1..S7 are pure (tokens, utils, state, api,
 * router, components, views) and S8 is the only section with side effects. A
 * Go test asserts the sections exist in this order, so the file stays navigable
 * as it grows. See docs/design/cockpit-single-file-tech.md.
 */

//! S1 · TOKENS — constants, severity/grade maps, copy. No behaviour.

/** Spec contract from the server (Go: internal/webspec). */
import spec from "./spec.json" with { type: "json" };

export const SPEC = spec;

/** Severity -> the CSS class and label used everywhere a severity shows. */
export const SEVERITY = {
  error:   { key: "error",   label: "Error",   weight: 0 },
  warning: { key: "warning", label: "Warning", weight: 1 },
  info:    { key: "info",    label: "Info",    weight: 2 },
};

export const STATUS = {
  open:           { key: "open",           label: "未处理",  tone: "muted" },
  confirmed:      { key: "confirmed",      label: "已确认",  tone: "blue" },
  false_positive:{ key: "false_positive", label: "误报",    tone: "amber" },
  accepted:       { key: "accepted",       label: "已接受",  tone: "mint" },
};

/** SORT_LABELS names each order in the operator's language. */
export const SORT_LABELS = {
  severity: "严重度", file: "文件", newest: "最新", rule: "规则",
};

/**
 * PHASES names the four moments of the gate loop. The cockpit's whole reason
 * to exist is that these happen in order, so the labels are a token table
 * rather than a string in one template.
 */
export const PHASES = {
  before:  { key: "before",  label: "生成前", note: "读取约束与查找符号" },
  explore: { key: "explore", label: "探索",   note: "理解仓库与影响范围" },
  verify:  { key: "verify",  label: "产出后验证", note: "检查代码与门禁结果" },
  review:  { key: "review",  label: "复核",   note: "整理 review bundle" },
  other:   { key: "other",   label: "其他",   note: "未归类的调用" },
};

export const phaseLabel = (phase) => (PHASES[phase] || PHASES.other).label;

/** Preferences live in one place so the keys never drift between views. */
export const PREFS = {
  historyMode: "metricgate.cockpit.historyMode",
  metric: "metricgate.cockpit.metricTrend",
};

/** WORKSPACE_ONLY is the subset of the contract's views that needs a scope. */
const WORKSPACE_ONLY = ["overview", "calls", "runs", "policy"];

/**
 * WORKSPACE_VIEWS are the tabs that only mean something inside a scope. They
 * belong to the same sidebar entry as the picker, because entering a project
 * is the action that makes them exist.
 *
 * It is derived from the Go spec rather than restated: the restatement was once
 * a page behind, naming a view that had been merged away and omitting the one
 * that replaced it, so the rail highlighted nothing on the page being shown.
 * spec.views is the list that already has to be right.
 */
export const WORKSPACE_VIEWS = new Set(spec.views.filter((v) => WORKSPACE_ONLY.includes(v)));

/** localStorage is only available in a browser; S8 guards boot on this too. */
const storage = () => {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
};

export const readPref = (key, fallback) => storage()?.getItem(key) || fallback;

export const writePref = (key, value) => storage()?.setItem(key, value);

//! S2 · UTILS — pure string/format helpers. No DOM, no fetch.

export const escapeHTML = (value) =>
  String(value ?? "").replace(/[&<>"']/g, (c) => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
  ));

/**
 * Tagged template that escapes every interpolated value by default.
 *
 * The cockpit renders server data into HTML strings. Doing that with plain
 * `${}` means every interpolation site has to remember to escape, and the one
 * that forgets is a stored-XSS bug in a tool an operator runs on their own
 * machine. This helper inverts the default: values are escaped unless wrapped
 * in raw().
 */
export const raw = (value) => ({ __raw: true, value: String(value) });

export const html = (strings, ...values) => {
  let out = "";
  strings.forEach((chunk, i) => {
    out += chunk;
    if (i >= values.length) return;
    out += interpolate(values[i]);
  });
  return raw(out);
};

/**
 * join concatenates the fragments a map() produced and stays a safe fragment.
 *
 * Array#join is the trap in this design: fragments survive interpolation (which
 * understands them), but `.map(fn).join("")` stringifies each fragment *first*
 * and every list silently renders as literal "[object Object]". The
 * interpolation is right, the join is wrong — so the join gets its own helper
 * and the Go contract test greps for stray `.join("")` in the templates.
 *
 * The return value is raw rather than a bare string for the second half of the
 * same trap: a plain string interpolated into an html`` template is *escaped*,
 * so a join that returns a string renders the page as visible markup source
 * instead of elements. One helper owns both failure modes.
 */
export const join = (fragments) => raw(interpolate(fragments));

/** list is join with the intent spelled out: it maps, then joins. */
export const list = (items, render) => join(items.map(render));

/** interpolate escapes anything that is not an already-safe raw fragment. */
const interpolate = (value) => {
  if (value == null || value === false) return "";
  if (Array.isArray(value)) return value.map(interpolate).join("");
  if (value && value.__raw) return value.value;
  return escapeHTML(value);
};

export const formatTime = (value) => {
  if (!value || value.startsWith("0001-")) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false });
};

/**
 * shortDate renders an RFC3339 day as `Mon D` without a locale library: the
 * ledger's expiry is a date, not a moment, and the raw string is what the
 * operator wrote in accepted.yaml.
 */
export const shortDate = (value) => {
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return String(value || "");
  return `${d.getMonth() + 1} 月 ${d.getDate()} 日`;
};

/** "3 days ago" for the relative-age column. */
/** relativeTime is "3 days ago" for the relative-age column. */
export const relativeTime = (value) => {
  if (!value) return "";
  const then = new Date(value).getTime();
  if (Number.isNaN(then)) return "";
  const seconds = Math.max(0, (Date.now() - then) / 1000);
  const units = [
    ["年", 31536000], ["个月", 2592000], ["天", 86400], ["小时", 3600], ["分钟", 60],
  ];
  for (const [label, size] of units) {
    const n = Math.floor(seconds / size);
    if (n >= 1) return `${n} ${label}前`;
  }
  return "刚刚";
};

export const percent = (value, denominator) =>
  denominator ? `${Math.round((value / denominator) * 100)}%` : "—";

/**
 * field reads a value under either spelling of the same key.
 *
 * Go serialises these payloads as camelCase, but the call log is a persisted
 * record and older rows spell the same field differently. A reader that only
 * knows one spelling renders a column of dashes and blames the backend.
 */
export const field = (value, key) =>
  value?.[key] ?? value?.[key.toUpperCase()] ?? value?.[key[0].toUpperCase() + key.slice(1)];

export const severityOf = (value) => SEVERITY[String(value || "").toLowerCase()] || {
  key: String(value || "unknown"), label: String(value || "未知"), weight: 9,
};

export const statusOf = (value) => STATUS[String(value || "open")] || STATUS.open;

//! S3 · STORE — the single source of truth. UI code never mutates it directly.

const listeners = new Set();

/**
 * state holds everything the URL does not. URL-facing state (view, filters,
 * sort, page) lives in the hash and is folded back in by the router.
 */
export const state = {
  view: "projects",
  // issues keys the payload exactly as /api/issues spells it, including the
  // `issues` field name: renaming a key on the way in is the drift this whole
  // file's contract exists to prevent, and it made the list read a key that
  // nothing ever wrote.
  issues: { issues: [], total: 0, page: 1, pageSize: 50, totalPages: 1, severities: {}, statuses: {}, sources: {}, rules: [], window: "" },
  // limit is how many runs the issues list spans, not how many rows a page
  // holds. The default is 1 on purpose: a store with 37k findings answers
  // "the newest run's open findings" and nothing wider, so the first screen is
  // the one the operator asked for and the widen control is always on screen.
  limit: 1,
  // facets keeps the counts from a read that carried no filter. The server
  // counts severities and statuses before the filters and rules after them
  // (countRules(filtered), internal/store/issues_query.go), so a response to a
  // filtered request cannot answer "what would I get if I also picked this
  // rule" — which is the only question the rail exists to answer.
  facets: null,
  gate: null,
  measures: null,
  selected: new Set(),
  openFingerprint: null,
  // loading is what the refresh control reads: a local tool over a store that
  // can hold thousands of runs needs to say "still reading" rather than appear
  // frozen on a page that is merely waiting.
  loading: false,
  error: "",
  logPath: "",
  // The aggregated view: project list, call trace, runs and the ledger. It is
  // one request because it is one aggregate; everything the operator drills
  // into (issues, gate, measures) is a per-view request instead.
  dashboard: null,
  policy: null,
  // rules is the /api/rules catalogue for the live scope. It is null until a
  // read succeeds so the page can tell "no catalogue" from "not asked yet" —
  // the difference between the fallback view and a blank one.
  rules: null,
  // policyDraft is the operator's unsaved edit. It lives in state because a
  // repaint rebuilds the textarea from the server's copy of the document, and
  // without this the edit is discarded by any unrelated refresh.
  policyDraft: null,
  source: null,
  issueTab: "where",
  // workspace scope: a project root, or "" for the unassigned records.
  scope: null,
  callFilters: { query: "", phase: "", status: "", trace: "" },
  historyMode: "active",
  // limit and status are the two defaults that decide what the issues view opens
  // on. status is "open" for the same reason limit is 1: a store with 37k
  // findings over 600 runs answers "what did the last run leave unhandled", and
  // a first screen of already-closed findings is the list nobody asked for.
  // ROUTE_KEYS keeps both shareable, and a link that spells either one out wins
  // over these — this is only what a bare reload lands on.
  status: "open",
  metricChoice: "",
  // rulesSort is the Rules table's column and direction. The effective-key list
  // is the whole policy's keys — 138 of them in this repository — so a table
  // locked to the server's alphabetical order cannot answer "which of these is
  // not from this file" without reading every row. One {key, dir} pair rather
  // than one field per column, because a second click on another column should
  // replace the order, not add to it.
  rulesSort: { key: "key", dir: "asc" },
  runSelection: new Set(),
  editedAt: "",
};

export const getState = () => state;

/**
 * RELOAD_KEYS are the state fields whose change needs the server's answer.
 *
 * These are exactly the parameters /api/issues reads. Repainting from the page
 * already in memory would show a filter the operator did not select — the list
 * is paginated, so "warning" on page 3 is not a subset of what we have.
 */
const RELOAD_KEYS = ["view", "severity", "status", "rule", "file", "query", "sort", "page", "scope", "limit"];

let reloadTimer = 0;

/**
 * set replaces part of the state, mirrors it into the URL, repaints, and — when
 * the change is one the server has to answer — re-reads the view's data.
 *
 * The URL write lives here rather than at each call site because every one of
 * these patches is shareable state: a filter that only exists in memory cannot
 * be pasted into a review thread, and the back button would leave the view while
 * keeping its filters. opts.delay exists for the search box, where a request per
 * keystroke is typing lag, not diligence.
 */
export function set(patch, opts = {}) {
  // A patch that changes nothing is dropped. The hashchange listener re-reads
  // the URL the write below just produced, so without this the round trip would
  // paint and re-fetch a view that is already showing exactly this state.
  if (!Object.entries(patch).some(([key, value]) => state[key] !== value)) return;
  const viewBefore = state.view;
  Object.assign(state, patch);
  // A view or scope change is a step the back button should be able to undo; a
  // filter change refines the page the operator is already standing on.
  const navigated = Boolean(opts.navigate) || Boolean(patch.view)
    || patch.scope !== undefined || viewBefore !== state.view;
  writeRoute(navigated);
  listeners.forEach((fn) => fn(state));
  if (!RELOAD_KEYS.some((key) => key in patch)) return;
  if (opts.delay) {
    clearTimeout(reloadTimer);
    reloadTimer = setTimeout(rerender, opts.delay);
    return;
  }
  rerender();
}

/**
 * writeRoute mirrors the URL-facing state into the hash.
 *
 * It compares before writing for two reasons: an unconditional write from set()
 * would re-fire the hashchange listener that itself calls set(), and the
 * difference between assigning the hash and replaceState is the difference
 * between a history entry and a silent refinement.
 */
function writeRoute(navigated) {
  if (typeof window === "undefined") return;
  const hash = `#/${encodeRoute(state)}`;
  if (window.location.hash === hash) return;
  if (navigated) window.location.hash = hash;
  else window.history.replaceState(null, "", hash);
}

/**
 * record writes the state without announcing it.
 *
 * set() is the rule and this is the single exception, for one reason: a
 * keystroke in the policy editor has to be stored (see policyDraft) but must not
 * repaint, because replacing the stage mid-keystroke also destroys an in-progress
 * IME composition — which is how a Chinese comment in a config file silently
 * loses the text being typed.
 */
export function record(patch) {
  Object.assign(state, patch);
}

export function subscribe(fn) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/**
 * routeChange applies a hash the browser handed us: paint, re-read, no write
 * back to the URL.
 *
 * It is a separate entry point rather than a flag on set() because going the
 * other way — assigning the state and letting set() write the hash — produces a
 * second hashchange event for the URL we just read.
 */
export function routeChange(patch) {
  Object.assign(state, patch);
  listeners.forEach((fn) => fn(state));
  rerender();
}

/** toggle in the multi-select set (bulk change). */
export function toggleSelected(fingerprint) {
  if (state.selected.has(fingerprint)) state.selected.delete(fingerprint);
  else state.selected.add(fingerprint);
  set({ selected: new Set(state.selected) });
}

export function clearSelected() {
  set({ selected: new Set() });
}

//! S4 · API — fetch, in-flight de-duplication, error normalisation.

/**
 * inFlight collapses concurrent requests for the same key into one promise.
 *
 * Clicking three facet chips quickly otherwise fires three identical requests,
 * and the response that arrives last is not necessarily the most recent one —
 * which leaves the list showing a filter combination nobody selected. One
 * promise per key makes that impossible.
 */
const inFlight = new Map();

/** READ_TIMEOUT_MS caps one read of the local store.
 *
 * A store holding thousands of runs means a slow query is real, not a bug; a
 * store that never answers is. Without a cap the cockpit sat on "读取本地数据…"
 * forever with a disabled refresh button and no error banner, which is
 * indistinguishable from a page that finished reading and found nothing.
 */
const READ_TIMEOUT_MS = 15000;

/** timeoutError names the one failure the abort signal produces. */
const timeoutError = (name) => (name === "TimeoutError" || name === "AbortError"
  ? new Error(`接口超时（>${Math.round(READ_TIMEOUT_MS / 1000)}s）`)
  : null);

/**
 * scopeParam spells the live scope the way the server's endpoints read it.
 *
 * The three states are distinct and the empty string is one of them: null is
 * "every project" (all), "" is "the unassigned records" (unassigned) and a root
 * is that project. api() drops empty-string parameters, so "unassigned" has to
 * be spelled out rather than passed through — otherwise the unassigned page
 * silently asked for the global aggregate instead of its own records.
 */
export const scopeParam = (s) => (s.scope === "" ? "unassigned" : s.scope || "all");

export async function api(path, params = {}) {
  // globalThis, not window: the fetch layer is what a test needs to exercise
  // against the server's real payloads, and a reference to `window` makes it
  // unloadable anywhere that is not a browser. The URL is still absolute-ready.
  const url = new URL(path, globalThis.location?.origin || "http://127.0.0.1");
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== null && value !== "") url.searchParams.set(key, value);
  }
  const cacheKey = url.toString();
  if (inFlight.has(cacheKey)) return inFlight.get(cacheKey);
  // AbortSignal.timeout, not a hand-rolled timer: this signal is per-request, so
  // a read that finished in time cannot be aborted by a later one's timer, and
  // it needs no cleanup for the timed-out case.
  const signal = AbortSignal.timeout(READ_TIMEOUT_MS);
  const pending = fetch(cacheKey, { headers: { Accept: "application/json" }, signal })
    .catch((error) => { throw timeoutError(error?.name) || error; })
    .then(async (response) => {
      // The abort lands during the body read as often as during the connect, so
      // the timeout is re-checked here rather than only on the fetch promise.
      try {
        const text = await response.text();
        let body = null;
        try {
          body = text ? JSON.parse(text) : null;
        } catch {
          body = null;
        }
        if (!response.ok) {
          throw new Error((body && (body.error || body.message)) || `请求失败 (${response.status})`);
        }
        return body;
      } catch (error) {
        throw timeoutError(error?.name) || error;
      }
    })
    // The in-flight entry is removed on the failure path too: a rejected read
    // that stayed cached would make every retry return the same rejection
    // instead of asking the server again.
    .finally(() => inFlight.delete(cacheKey));
  inFlight.set(cacheKey, pending);
  return pending;
}

/** patchIssue records one triage decision. */
export async function patchIssue(decision) {
  const response = await fetch("/api/issues/state", {
    method: "PATCH",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify(decision),
  });
  if (!response.ok) {
    const text = await response.text();
    throw new Error(text || `请求失败 (${response.status})`);
  }
  return response.json();
}

/**
 * facetCache copies the three facet fields out of a response.
 *
 * It is a copy rather than the response itself because the payload also carries
 * the page's rows, and holding a page in the cache would date the rail by the
 * page it came with.
 */
export const facetCache = (result) => ({
  severities: result.severities || {},
  statuses: result.statuses || {},
  rules: result.rules || [],
});

/**
 * unfilteredRead answers whether one issues request still describes the whole
 * window for the rail's severities and statuses.
 *
 * It lives out here because it is the rail's cache rule, not the page's load
 * rule. The server counts severities and statuses *before* its filters
 * (facetsOf(candidates) runs ahead of applyIssueFilters), so severity and
 * status chips do not narrow those numbers and must not invalidate the cache;
 * only rule, file and query do. The rules facet is counted after the filters,
 * so fetchIssues keeps the cached rules instead of overwriting them from a
 * narrowed read. Keeping it as a predicate keeps fetchIssues to the shape of a
 * request.
 */
export const unfilteredRead = (filters) =>
  !(filters.rule || filters.file || filters.query);

/**
 * nextFacets decides what the rail's cache becomes after one issues read.
 *
 * It is a separate function because the rule has three inputs that are easy to
 * collapse wrongly in place: whether this read may refresh the cache at all
 * (unfilteredRead), whether it still describes the window the cache holds
 * (a scope or limit change is a new window and invalidates every count), and
 * the cached rules, which must survive a narrowed read because the server
 * counts rules *after* its filters.
 */
const nextFacets = (prev, result, refresh) => {
  if (!prev || !refresh) return prev || facetCache(result);
  const sameWindow = (state.issues.window || "") === (result.window || "");
  return sameWindow ? { ...facetCache(result), rules: prev.rules } : facetCache(result);
};

/** fetchIssues loads one page for the current filter set. */
export async function fetchIssues(filters) {
  set({ loading: true, error: "" });
  const refreshFacets = unfilteredRead(filters);
  try {
    const result = await api("/api/issues", {
      limit: filters.limit || 1, page: filters.page || 1, pageSize: 50,
      severity: filters.severity, status: filters.status,
      rule: filters.rule, file: filters.file, q: filters.query, sort: filters.sort,
      // The scope is sent, not filtered here: the list is paginated, so a
      // client-side filter would keep page sizes and totals for a wider scope
      // than the one the operator is standing in.
      scope: scopeParam(state),
    });
    set({
      issues: {
        issues: result.issues || [], total: result.total || 0,
        page: result.page || 1, pageSize: result.pageSize || 50,
        totalPages: result.totalPages || 1,
        severities: result.severities || {}, statuses: result.statuses || {},
        sources: result.sources || {}, rules: result.rules || [],
        // window is the scope the server actually queried. It is shown rather
        // than assumed because the two can differ: the list spans the runs the
        // window covers, not the workspace the breadcrumb names, and a number
        // that looks project-scoped while it is not is the worst kind of lie a
        // local audit tool can tell.
        window: result.window || "",
      },
      // The rail's cache is per window: nextFacets owns that rule.
      facets: nextFacets(state.facets, result, refreshFacets),
      loading: false,
    });
  } catch (error) {
    set({ loading: false, error: error.message });
  }
}

/**
 * fetchDashboard loads the aggregate the landing pages read: projects, calls,
 * runs, trends and the concession ledger.
 *
 * It is still one request on purpose — it is one aggregate of one local store,
 * and splitting it would mean several payloads that each recompute the same
 * grouping. The per-view endpoints exist for the interactive parts (issues,
 * gate, measures) where a filter has to hit the database, not this copy.
 */
export async function fetchDashboard() {
  set({ loading: true, error: "" });
  try {
    const data = await api("/api/dashboard");
    // One set() rather than two direct writes plus a notify: S3 is the single
    // source of truth, and a field assigned around set() leaves the subscribers
    // reading a state no one announced — which is only ever noticed by the one
    // view that happened to repaint at the wrong moment.
    set({ dashboard: data, logPath: data.logPath || "日志未启用", loading: false });
    return data;
  } catch (error) {
    set({ loading: false, error: error.message });
    return null;
  }
}

/** fetchPolicy reads the discovery chain, the document and the provenance. */
export async function fetchPolicy() {
  try {
    set({ policy: await api("/api/policy", { scope: scopeParam(state) }) });
  } catch (error) {
    set({ error: error.message });
  }
}

/**
 * fetchRules loads the rule catalogue: one row per rule per language, with the
 * value actually in force and why it exists.
 *
 * It is a separate endpoint from /api/policy because the two answer different
 * questions: policy is "what does this file say", rules is "what will the
 * engine do and why". The catalogue is built from rules.ForLanguage ×
 * config.ExplainRule, so it is policy-only and never reads the history store.
 *
 * The error lands in state.error rather than in a field of its own on purpose:
 * the effective-key table below it is the fallback view, and one error banner
 * covers whichever read failed.
 */
export async function fetchRules() {
  try {
    set({ rules: await api("/api/rules", { scope: scopeParam(state) }) });
  } catch (error) {
    set({ error: error.message });
  }
}

export async function fetchGate() {
  try {
    set({ gate: await api("/api/gate", { scope: scopeParam(state) }) });
  } catch (error) {
    set({ error: error.message });
  }
}

export async function fetchMeasures() {
  try {
    set({ measures: await api("/api/measures", { scope: scopeParam(state) }) });
  } catch (error) {
    set({ error: error.message });
  }
}

/**
 * fetchSource reads the code window for one fingerprint. It is the only call
 * in the cockpit that causes the server to read the operator's source, so it
 * is made per open issue and not per list render.
 */
export async function fetchSource(fingerprint) {
  try {
    set({ source: await api("/api/source", { fingerprint }) });
  } catch (error) {
    // A missing window is a state, not a failure: the file may have moved
    // since the run. The tab says so rather than showing an empty box.
    set({ source: { reason: error.message } });
  }
}

/**
 * ensureSource loads the code window for the open issue, and only when the
 * "where" tab is the one on screen.
 *
 * It exists because the detail pane became restorable: a shared link now
 * carries the open fingerprint and the tab, so the restore path needs the same
 * read a click does, and putting it here keeps that decision in one place
 * instead of three (a click, a restored hash, and boot each grew their own
 * copy of it).
 *
 * The two guards are both load-bearing. The tab guard is the reason a link to
 * the "why" tab costs no request: the other three tabs are painted from the row
 * already in memory, and reading the operator's source for a tab that will not
 * show it is the one read this file must never spend by accident. The
 * fingerprint guard makes a re-click of "where" free, and it deliberately does
 * not treat a failed read as cached: fetchSource records a failure as
 * `{reason}` with no fingerprint, so the next click tries again rather than
 * pinning a moved file as permanently unreadable.
 */
export async function ensureSource() {
  const fingerprint = state.openFingerprint;
  if (!fingerprint || state.issueTab !== "where") return;
  if (state.source && state.source.fingerprint === fingerprint) return;
  await fetchSource(fingerprint);
}

//! S5 · ROUTER — the shareable state is the URL, and nothing else.

/**
 * CALL_PARAM_KEYS is the one place that maps a hash parameter onto a
 * call-filter key. Both directions of the router read it, so adding a filter
 * cannot leave the link showing an empty table because encode forgot a name.
 *
 * Two of the four are namespaced, and both namespacing is load-bearing.
 * `callStatus` because "status" also names an issue verdict; `cq` because "q"
 * also names the issue search. Sharing a parameter means whichever list the
 * operator last touched owns the other's box: one pasted link shows two
 * filters nobody set, and clearing one silently clears the other.
 */
export const CALL_PARAM_KEYS = [
  ["cq", "query"], ["phase", "phase"], ["callStatus", "status"], ["trace", "trace"],
];

/**
 * ROUTE_KEYS is the whole URL-facing state as a table: parameter name, the
 * state field it owns, and the value that means "leave it out".
 *
 * The router used to be a branch per field, and every added filter made both
 * functions cross the complexity threshold — the gate flagged the encoder and
 * the decoder on the same day. With a table, both directions are one loop over
 * the same rows and adding a filter is a row, not a new path.
 */
export const ROUTE_KEYS = [
  ["view", "view", "projects"],
  ["scope", "scope", null],
  ["severity", "severity", ""],
  ["status", "status", ""],
  ["rule", "rule", ""],
  ["file", "file", ""],
  ["q", "query", ""],
  ["sort", "sort", "severity"],
  // limit joins the shareable state because it changes which runs the server
  // queries: 1 is the convergent default, so it is the value left out of the
  // URL and an old link with no limit now lands on the narrow window.
  ["limit", "limit", 1],
  ["history", "historyMode", "active"],
  ["page", "page", 1],
  // The open issue and its tab are shareable state too, and leaving them out
  // was the one place the URL did not describe the screen: "the issue I am
  // looking at, on the tab I left it on" is exactly what a review thread
  // links to. They are two rows rather than a composite "open/where" value
  // because the tab is meaningless without the issue and the issue is useful
  // without the tab — and a composite would need a parser, which is the
  // per-field branch this table exists to avoid.
  ["open", "openFingerprint", null],
  ["tab", "issueTab", "where"],
];

/**
 * The hash holds only what is worth sharing or re-entering: the view, the
 * workspace scope, and everything that changes what a list shows.
 *
 * UI-local state (expanded sections, hover, an open dialog) deliberately stays
 * out: it would make every click rewrite the URL and break the back button
 * for a change nobody wants to undo.
 */
export function encodeRoute() {
  const params = new URLSearchParams();
  // The argument is the state being encoded, not the module singleton: the
  // singleton is still the default while the operator is on another view, and
  // routing through it would write the view they left instead of the one they
  // are on.
  const route = arguments.length ? arguments[0] : state;
  const f = route.callFilters || {};
  for (const [param, key] of CALL_PARAM_KEYS) {
    if (f[key]) params.set(param, f[key]);
  }
  // One loop, one row per shareable value. Omitting the default is what keeps a
  // bare reload on the same page without every click rewriting the URL.
  for (const [param, key, blank] of ROUTE_KEYS) {
    const value = route[key];
    // Only the row's own blank is omitted. The old blanket "empty string is
    // never worth writing" also dropped scope="", which *is* a state: the
    // unassigned records. That workspace could be entered but not shared,
    // reloaded, or stepped back out of.
    if (value !== blank && value !== undefined && value !== null) params.set(param, String(value));
  }
  return params.toString();
}

/**
 * decodeRoute maps a hash onto a state patch. Missing keys mean "keep what you
 * had", which is what makes a bare reload land on the same view.
 */
export function decodeRoute(hash) {
  const params = new URLSearchParams(String(hash || "").replace(/^#\/?/, ""));
  const patch = {};
  for (const [param, key] of ROUTE_KEYS) {
    if (params.has(param)) {
      // limit is a count, like page: "limit=60" that arrived as the string
      // "60" would be sent back to the server as a string and compared against
      // the number 1 to decide whether to re-read.
      patch[key] = key === "page" || key === "limit" ? Number(params.get(param)) || 1 : params.get(param);
    }
  }
  // The call-trace filters sit beside the shared ones rather than inside them:
  // "status" names both an issue verdict and a call verdict, and only
  // namespacing the second keeps a shared link from overwriting the first.
  const filters = {};
  for (const [param, key] of CALL_PARAM_KEYS) {
    if (params.has(param)) filters[key] = params.get(param);
  }
  if (Object.keys(filters).length) patch.callFilters = filters;
  return patch;
}

//! S6 · COMPONENTS — pure functions from data to HTML. One screen each.

/**
 * fileBase is the last path segment. A finding's file is a worktree-absolute
 * path, and forty characters of /var/folders/... in a row label pushes the line
 * number — the only part that locates the issue — off the card. The full path
 * stays in the title attribute, so nothing is hidden, it is just not shouted.
 */
export const fileBase = (path) => String(path || "").split(/[\\/]/).pop() || String(path || "");

/**
 * pathDir is the two segments above the file, with a leading ellipsis when the
 * path goes deeper. fileBase alone cannot tell two worktrees apart — both have
 * their own engine.go — and the group heading is the only place that names the
 * file, so it carries just enough ancestry to disambiguate without repeating
 * the forty-character temp prefix that pushed the name off screen.
 */
export const pathDir = (path) => {
  const segments = String(path || "").split(/[\\/]/);
  // "a/b/c.go" and "/a/b/" both end in a separator or a name: dropping the last
  // piece by index, not by filter, keeps the parent list honest about depth.
  if (segments.length < 2) return "";
  const parents = segments.slice(0, -1).filter(Boolean);
  if (!parents.length) return "";
  const near = parents.slice(-2).join("/");
  return parents.length > 2 ? `…/${near}/` : `${near}/`;
};

export const severityBadge = (severity) => {
  const meta = severityOf(severity);
  return html`<span class="badge sev-${meta.key}" title="严重度 ${meta.label}">${meta.label}</span>`;
};

export const statusTag = (status) => {
  const meta = statusOf(status);
  return html`<span class="status-tag tone-${meta.tone}">${meta.label}</span>`;
};

/**
 * badge renders one verdict. A state nobody named still gets a badge: a blank
 * cell reads as a rendering bug, a grey "未知" reads as data.
 */
export const badge = (value) => {
  const text = String(value || "未知");
  const status = text.toLowerCase();
  // "无数据" is its own tone, not a warning and not a pass. The gate's status
  // column used to give an unmeasured condition the same green "通过" a
  // measured one gets, which is the exact confusion this table exists to
  // prevent: a threshold nobody measured has not been met.
  const tone = text === "无数据" ? "unknown"
    : status.includes("pass") || status === "通过" ? "pass"
    : text === "接近阈值" || status.includes("warn") ? "flagged"
    : status.includes("error") || text === "发现问题" ? "error"
    : "flagged";
  return html`<span class="badge ${tone}">${text}</span>`;
};

/**
 * issueCard is the Issues list's unit of work.
 *
 * The card carries the identity the triage buttons act on (data-fingerprint),
 * so a click on a card and a click on a verdict dropdown are addressed to the
 * same finding without either knowing about the other.
 *
 * The layout is SonarQube's issue row: the message is the headline, the rule id
 * sits beside it, and the second line carries the verdict chips, the location
 * and the history. Severity and status are chips rather than a coloured border,
 * because a border cannot be read by anyone who cannot see the colour.
 */
export const issueCard = (issue) => html`
  <article class="issue-card${state.openFingerprint === issue.fingerprint ? " is-selected" : ""}"
    data-action="issue.select" data-fingerprint=${issue.fingerprint}>
    <label class="issue-pick"><input type="checkbox" data-action="issue.toggle" data-fingerprint=${issue.fingerprint}
      ${state.selected.has(issue.fingerprint) ? raw("checked") : ""} aria-label="选择此问题"></label>
    <div class="issue-card-body">
      <header class="issue-card-head">
        <p class="issue-message">${issue.message}</p>
        <code class="issue-rule">${issue.ruleId}</code>
      </header>
      <footer class="issue-card-foot">
        ${severityBadge(issue.severity)}
        ${statusTag(issue.status)}
        <span class="issue-loc" title="${issue.file}:${issue.startLine}">${fileBase(issue.file)}:${issue.startLine}</span>
        <span class="issue-meta">${relativeTime(issue.lastSeen)} · 见过 ${issue.occurrences} 次</span>
        ${statusSelect(issue)}
      </footer>
    </div>
  </article>`;

/**
 * groupByFile splits one page of issues into SonarQube's file groups. The file
 * is the unit an operator actually works on — nobody fixes "issue #3", they fix
 * one file — so the grouping is the list's structure rather than a filter on it.
 * Order is first appearance, so the server's sort still decides what comes
 * first; grouping only decides what sits together.
 */
export const groupByFile = (issues) => {
  const order = [];
  const byFile = new Map();
  for (const issue of issues) {
    const path = issue.file || "（未定位）";
    if (!byFile.has(path)) {
      byFile.set(path, []);
      order.push(path);
    }
    byFile.get(path).push(issue);
  }
  return order.map((path) => ({ path, issues: byFile.get(path) }));
};

/** fileGroup is one file heading plus the issues found in it. */
export const fileGroup = (group) => html`
  <section class="file-group">
    <header class="file-group-head">
      <code title="${group.path}"><span class="path-dir">${pathDir(group.path)}</span>${fileBase(group.path)}</code>
      <span class="subtle">${group.issues.length} 个问题</span>
    </header>
    ${list(group.issues, issueCard)}
  </section>`;

/**
 * issueRow is the Issues table's unit of work.
 *
 * It carries the same identity issueCard does — one row per finding, the row
 * selects it, the checkbox bulk-selects it, the dropdown sets its verdict — but
 * renders as a <tr> because the list is scanned by column. On a page with 421
 * findings the question is almost always "which files have the worst of rule
 * X", and that is a question about a column: a card puts the same seven facts
 * in a different order on every row, which makes the column impossible to read.
 *
 * The message is the only cell allowed to grow; every other one is sized by the
 * stylesheet so the columns line up down the page. The location keeps both
 * halves of the L1 contract — the short name is what is visible, the absolute
 * path stays in the title.
 */
export const issueRow = (issue) => html`
  <tr class="issue-row${state.openFingerprint === issue.fingerprint ? " is-selected" : ""}"
    data-action="issue.select" data-fingerprint=${issue.fingerprint}>
    <td class="col-pick"><input type="checkbox" class="issue-pick" data-action="issue.toggle" data-fingerprint=${issue.fingerprint}
      ${state.selected.has(issue.fingerprint) ? raw("checked") : ""} aria-label="选择此问题"></td>
    <td class="col-sev">${severityBadge(issue.severity)}</td>
    <td class="col-rule"><code class="issue-rule">${issue.ruleId}</code></td>
    <td class="col-msg" title="${issue.message}">${issue.message}</td>
    <td class="col-loc"><span class="issue-loc" title="${issue.file}:${issue.startLine}">${fileBase(issue.file)}:${issue.startLine}</span></td>
    <td class="col-age"><span class="issue-meta">${relativeTime(issue.lastSeen)} · 见过 ${issue.occurrences} 次</span></td>
    <td class="col-status">${statusSelect(issue)}</td>
  </tr>`;

/**
 * issueTable is the list itself: a header that names the columns, then one row
 * per finding.
 *
 * The header is drawn even when the list is empty, because the empty state is a
 * <td> inside the table — a filter that matches nothing should leave the shape
 * of the answer on screen, not replace it with a sentence.
 */
export const issueTable = (rows) => html`
  <div class="table-wrap">
    <table class="issues-table">
      <thead>
        <tr>
          <th class="col-pick"></th>
          <th class="col-sev">严重度</th>
          <th class="col-rule">规则</th>
          <th class="col-msg">问题</th>
          <th class="col-loc">位置</th>
          <th class="col-age">引入</th>
          <th class="col-status">状态</th>
        </tr>
      </thead>
      <tbody>${rows.length ? list(rows, issueRow) : html`<tr><td class="table-empty" colspan="7">没有匹配的问题</td></tr>`}</tbody>
    </table>
  </div>`;

/** phaseBar is one step of the gate-loop flow, sized against the busiest step. */
export const phaseBar = (key, counts) => {
  const phase = PHASES[key] || PHASES.other;
  const steps = Object.keys(PHASES).map((k) => Number(counts[k]) || 0);
  const max = Math.max(1, ...steps);
  const count = Number(counts[key]) || 0;
  return html`
    <div class="flow-step">
      <div class="flow-index">0${Object.keys(PHASES).indexOf(key) + 1}</div>
      <div class="flow-copy"><strong>${phase.label}</strong><small>${phase.note}</small></div>
      <div class="flow-count">${count}<small>次</small></div>
      <div class="flow-track"><i style="width:${Math.round((count / max) * 100)}%"></i></div>
    </div>`;
};

/**
 * trendColumn is one bar of the run-to-run severity chart. The stack is
 * proportional inside itself so the error share of a small run is still
 * readable next to a big one.
 */
export const trendColumn = (point, max) => {
  const errors = Number(field(point, "errors")) || 0;
  const warnings = Number(field(point, "warnings")) || 0;
  const infos = Number(field(point, "infos")) || 0;
  const total = errors + warnings + infos;
  const when = field(point, "startedAt") || "";
  return html`
    <div class="trend-column" title="${formatTime(when)} · errors ${errors}, warnings ${warnings}, info ${infos}">
      <div class="bar-stack" style="height:${Math.max(4, Math.round((total / max) * 100))}%">
        <i class="bar-error" style="height:${total ? (errors / total) * 100 : 0}%"></i>
        <i class="bar-warning" style="height:${total ? (warnings / total) * 100 : 0}%"></i>
        <i class="bar-info" style="height:${total ? (infos / total) * 100 : 0}%"></i>
      </div>
      <small>${String(when).slice(5, 10)}</small>
    </div>`;
};

/** callRow is one row of the local call trace. */
export const callRow = (call) => {
  const state = field(call, "error") ? "ERROR" : field(call, "verdict") || "已记录";
  return html`
    <tr>
      <td class="time-cell">${formatTime(field(call, "time"))}</td>
      <td><strong>${field(call, "tool") || "—"}</strong></td>
      <td><span class="phase-tag">${phaseLabel(field(call, "phase"))}</span></td>
      <td class="target-cell" title="${field(call, "target") || ""}">${field(call, "target") || "—"}</td>
      <td>${Number(field(call, "ms")) || 0}<span class="muted"> ms</span></td>
      <td>${badge(state)}</td>
      <td><button class="text-button" data-action="call.detail" data-time="${field(call, "time") || ""}" data-tool="${field(call, "tool") || ""}">详情 ↗</button></td>
    </tr>`;
};

/** runRow is one stored run, selectable for archive/restore. */
export const runRow = (run, findings, archived) => {
  const id = field(run, "id");
  const errorCount = Number(field(run, "errorCount")) || 0;
  const warningCount = Number(field(run, "warningCount")) || Number(field(run, "warnings")) || 0;
  const count = findings.filter((entry) => Number(field(entry, "runId")) === Number(id)).length;
  return html`
    <article class="run-row">
      <label class="run-select"><input type="checkbox" data-action="run.select" data-run="${id}" ${state.runSelection.has(String(id)) ? raw("checked") : ""} aria-label="选择运行 #${id}"></label>
      <div class="run-status ${errorCount ? "has-errors" : "is-clean"}">${errorCount ? "!" : "✓"}</div>
      <div class="run-main">
        <div class="run-title"><strong>运行 #${id}</strong>${badge(errorCount ? "发现问题" : "通过")}
          <span class="run-commit">${field(run, "gitCommit") || "本地工作区"}</span>${archived ? html`<span class="badge flagged">已归档</span>` : ""}</div>
        <div class="run-meta">${formatTime(field(run, "startedAt"))}<span>·</span>${field(run, "files") || 0} 个文件<span>·</span>${field(run, "durationMs") || 0} ms</div>
      </div>
      <div class="run-counts"><span class="error-text">${errorCount} errors</span><span class="warning-text">${warningCount} warnings</span><span>${count} 条发现</span></div>
      <button class="text-button" data-action="run.findings" data-run="${id}">查看发现 ↗</button>
    </article>`;
};

/**
 * metricStep flags a row whose threshold moved since the previous run.
 *
 * A tightening decision is only checkable if the step in the threshold is
 * visible next to the distribution — otherwise a drop in violations could just
 * mean the code changed. The class draws the line; the tooltip says why.
 */
export const metricStep = (point, previous) => {
  // errorThreshold, not errThreshold: the store spells it out, and reading a
  // key that is always undefined compares undefined to undefined, so a
  // tightening of the *error* band — the one that fails a build — showed no
  // step at all while the warn band did.
  const changed = point.warnThreshold !== (previous || {}).warnThreshold
    || point.errorThreshold !== (previous || {}).errorThreshold;
  return changed ? "metric-step" : "";
};

/**
 * acceptedEntry is one row of the concession ledger: one line that can be
 * scanned for what was waived, where, who signed it and when it lapses.
 * SonarQube's ledger is a list of such rows, not a paragraph each.
 */
export const acceptedEntry = (entry) => {
  const expired = entry.expires && new Date(entry.expires) < new Date();
  return html`
    <div class="accepted-item">
      <div class="accepted-who">
        ${expired ? html`<span class="tag stale">STALE</span>` : html`<span class="tag ok">ACCEPTED</span>`}
        <code>${entry.rule}</code>
        <strong>${entry.file}:${entry.line}</strong>
      </div>
      <div class="accepted-meta">
        <span class="accepted-owner">${entry.owner}</span>
        <span class="accepted-reason" title="${entry.reason}">${entry.reason}</span>
        <span class="accepted-expires">${entry.expires ? shortDate(entry.expires) : "长期有效"}</span>
      </div>
    </div>`;
};
/**
 * gateBanner is the Quality Gate verdict. It is the page-level conclusion, so
 * it is one line and it is loud — the condition table below is its evidence.
 *
 * The three states are not two states plus an edge case: "unknown" is what the
 * backend reports when no stored run exists (`internal/cockpit/gate_api.go:99`
 * — "history store is disabled; no gate verdict is available"), and rendering
 * it green would turn "never ran here" into "passed". It gets its own glyph
 * and its own sentence, not the passed banner with a different word.
 */
/**
 * conditionId is the anchor a condition row answers to.
 *
 * Stable and derived from the row's own identity (language + measure) rather
 * than from a list index, because the table is sorted by language rank: adding
 * a language shifts positions but not identities, and a jump that lands on the
 * wrong row is worse than no jump at all.
 */
export const conditionId = (c) => `cond-${c && c.language ? c.language : "all"}-${c && c.measure ? c.measure : "unknown"}`;

/**
 * gateLocateTarget picks the row a click on the banner should land on: the
 * condition that actually triggered the verdict, not the top of the table.
 *
 * Pure and exported so the wiring can be asserted without a DOM — render_all
 * checks that the banner's target is a row id the table really renders. It
 * returns the first failing condition, then the first one merely in the
 * warning zone, and nothing when neither exists: a PASSED banner has no row to
 * blame, and a button that jumps to an arbitrary row would invent one.
 */
export const gateLocateTarget = (gate) => {
  const conditions = (gate && gate.conditions) || [];
  const pick = conditions.find((c) => c.failing) || conditions.find((c) => c.warning);
  return pick ? conditionId(pick) : "";
};

export const gateBanner = (gate) => {
  if (!gate) return html`<div class="gate-banner is-unknown">门禁状态未知</div>`;
  const tone = gate.status === "passed" ? "passed" : gate.status === "failed" ? "failed" : "unknown";
  const label = tone === "passed" ? "PASSED" : tone === "failed" ? "FAILED" : "UNKNOWN";
  const glyph = tone === "passed" ? "✓" : tone === "failed" ? "✕" : "?";
  const reason = gate.reason || (tone === "unknown" ? "没有可用的运行记录，无法给出结论" : "");
  // A verdict from another project root is not this project's verdict. The
  // banner still shows the run, because the checker's answer is real, but it
  // says whose run it is — otherwise an empty project inherits a green PASSED
  // from a neighbour and the page reads as "your repository passed".
  const foreign = gate.foreign
    ? html`<span class="badge flagged gate-foreign">该运行不属于当前项目，仅供参考</span>`
    : "";
  const target = gateLocateTarget(gate);
  return html`
    <div class="gate-banner is-${tone}">
      <span class="gate-glyph" aria-hidden="true">${glyph}</span>
      <strong>${label}</strong>
      <span class="gate-reason">${reason}</span>
      ${gate.runId ? html`<span class="gate-run">运行 #${gate.runId}</span>` : ""}
      ${foreign}
      ${target ? html`<button type="button" class="gate-locate" data-action="gate.locate" data-target="${target}">定位到触发的条件行</button>` : ""}
    </div>`;
};

/**
 * gateHeadroom is how much room a condition has left, in the threshold's own
 * unit. The table used to show only the threshold and the measured value, so a
 * condition sitting at 14/15 and one at 1/15 read the same; the margin is the
 * number that says whether the next commit is the one that fails the gate.
 *
 * `hasData === false` is its own tier, not a margin of zero. The engine reports
 * it when it had nothing to measure (`internal/cockpit/gate_api.go`), and
 * drawing that as "0" or "—" makes an unmeasured condition look like a
 * comfortable pass — the one mistake this table exists to prevent.
 */
export const gateHeadroom = (c) => {
  if (!c || !c.hasData) return { text: "无数据", tone: "unknown" };
  // Every projected condition is "≤", so the margin is threshold minus
  // measured. A different operator needs different arithmetic, so it is
  // reported rather than guessed at.
  if (c.operator && c.operator !== "≤") return { text: "—", tone: "unknown" };
  const value = Number(c.value);
  const current = Number(c.current);
  if (!Number.isFinite(value) || !Number.isFinite(current)) return { text: "无数据", tone: "unknown" };
  const margin = value - current;
  if (margin < 0) return { text: `超出 ${Math.abs(margin)}`, tone: "over" };
  if (margin === 0) return { text: "恰好压线", tone: "at" };
  return { text: `余 ${margin}`, tone: "under" };
};

/**
 * gateMeta is the provenance of the verdict: which policy bound it, which run
 * produced it, and when. The page used to carry only `fail_on: error · 运行 #1`
 * as a trailing string in the panel head, which answered "what bound this" and
 * nothing else — a verdict with no commit and no timestamp cannot be argued
 * with, because there is no way to find the run it describes.
 *
 * Each part is rendered only when the payload carries it. L6 is why: the same
 * three facts are wanted on the summary and detail pages, their payloads do not
 * have them (`internal/cockpit/gate_api.go:78-83` is the only one that does),
 * and a placeholder is worse than an absence — it makes a missing field look
 * like a rendered one.
 */
export const gateMeta = (gate) => {
  if (!gate) return "";
  const parts = [];
  if (gate.failOn) parts.push(html`<span class="gate-meta-item">fail_on <code>${gate.failOn}</code></span>`);
  if (gate.runId) parts.push(html`<span class="gate-meta-item">运行 <code>#${gate.runId}</code></span>`);
  if (gate.commit) parts.push(html`<span class="gate-meta-item">提交 <code>${String(gate.commit).slice(0, 12)}</code></span>`);
  if (gate.startedAt) {
    // The payload's timestamp is RFC3339 in UTC; the reader is looking at a
    // local clock, so it is converted rather than printed as-is.
    const when = new Date(gate.startedAt);
    const text = Number.isNaN(when.getTime()) ? gate.startedAt : when.toLocaleString();
    parts.push(html`<span class="gate-meta-item">${text}</span>`);
  }
  if (!parts.length) return "";
  return html`<span class="gate-meta">${join(parts)}</span>`;
};

/** measureCard grades one Summary measure. */
export const measureCard = (measure) => html`
  <article class="measure-card">
    <div class="measure-head"><span class="measure-label">${measure.label}</span>
      <span class="measure-grade grade-${measure.grade}">${measure.grade}</span></div>
    <div class="measure-value">${measure.value}</div>
    <div class="measure-bands">warn ${measure.warnThreshold} · err ${measure.errThreshold}</div>
    ${measure.exceeded > 0 ? html`<div class="measure-exceeded">${measure.exceeded} 个位置超阈</div>` : ""}
  </article>`;

/**
 * facetRow is one filter chip with its count.
 *
 * The label is a parameter rather than the value because the two differ for
 * severities and statuses ("error" is the URL value, "Error" is what a Chinese
 * UI shows) and only the value is what a click has to send back.
 *
 * is-active is the visual highlight and aria-current is the same fact for a
 * screen reader. Both are set here, in the one place that knows which facet is
 * live: before, the state was carried by a background tint alone, which a
 * screen reader cannot report, so "which filter is applied" was answerable
 * only by looking at the page. aria-current rather than aria-pressed because
 * the facets are single-select — facet.toggle clears the kind when the same
 * value is clicked again.
 */
export const facetRow = (kind, value, label, count, active) => html`
  <button type="button" class="facet-row${active ? " is-active" : ""}"
          aria-current=${active ? "true" : ""}
          data-action="facet.toggle" data-kind=${kind} data-value=${value}>
    <span class="facet-label">${label}</span>
    <span class="facet-count">${count}</span>
  </button>`;

/**
 * loadError is the "this read failed" card. It stands in for the view's content
 * rather than sitting beside it: an empty list rendered next to an error tells
 * the operator "there is nothing here", and the difference between "no issues"
 * and "the store could not be read" is the one a triage decision must not
 * confuse. The banner already names the failure; this is where the retry lives.
 */
export const loadError = (s) => html`
  <div class="panel load-error">
    <div class="load-error-head"><span class="load-error-glyph" aria-hidden="true">!</span><strong>数据读取失败</strong></div>
    <p class="load-error-reason">${s.error}</p>
    <button type="button" class="button primary" data-action="view.retry">重试</button>
  </div>`;

/**
 * loadPending is the "this read has not come back yet" card, and it is the same
 * idea as loadError one step earlier.
 *
 * Without it a view draws its empty state while the request is still in flight,
 * because `loading` was never consulted — only the header label read it. On this
 * store that is not a hypothetical: `/api/dashboard` is 74MB and takes ~13s, and
 * the runs page rendered "0 条 / 还没有运行记录 / 日志未启用" for that whole
 * window. "There is nothing here" and "I have not heard back" are different
 * facts, and this cockpit exists to keep them apart — the same reason
 * loadError exists rather than an empty list beside a failed read.
 */
export const loadPending = (s) => (s.loading
  ? html`<div class="panel load-pending"><span class="load-pending-glyph" aria-hidden="true">…</span><strong>正在读取本地数据…</strong><p class="subtle">数据量较大时可能需要十几秒。</p></div>`
  : "");

//! S7 · VIEWS — one object per route: what it fetches and how it renders.

export const VIEWS = {};

/** projectOf returns the scope's project record, when the scope is a project. */
export const projectOf = (s) => (s.dashboard?.projects || []).find((p) => p.root === s.scope) || null;

/**
 * callsInScope is the call trace for the live scope. The unassigned records
 * are kept as their own scope rather than folded into a project: a call that
 * cannot be proven to belong to a worktree would otherwise be attributed to
 * the neighbouring project that happens to share a path prefix.
 */
export const callsInScope = (s) => {
  const calls = s.dashboard?.calls || [];
  if (s.scope === "") return calls.filter((call) => !field(call, "projectRoot"));
  if (s.scope) return calls.filter((call) => field(call, "projectRoot") === s.scope);
  return calls;
};

/**
 * scopeAggregate is the record the run history of the live scope is read from.
 *
 * The dashboard's own runs are the unassigned ones, so a global view cannot just
 * read the top level: it has to union every project's runs with them. Deduping
 * by id matters — a run is attached to exactly one project, but the same store
 * row is listed under both views when a root is renamed.
 */
export const scopeAggregate = (s) => {
  const d = s.dashboard || null;
  if (!d) return null;
  if (s.scope === "") return d;
  if (s.scope) return projectOf(s);
  const archived = s.historyMode === "archived";
  const key = archived ? "archivedRuns" : "runs";
  const fkey = archived ? "archivedFindings" : "findings";
  const seen = new Set();
  const runs = [];
  const findings = [];
  for (const row of [...(d[key] || []), ...(d.projects || []).flatMap((p) => p[key] || [])]) {
    const id = field(row, "id");
    if (id !== undefined && id !== null && seen.has(id)) continue;
    if (id !== undefined && id !== null) seen.add(id);
    runs.push(row);
  }
  for (const row of [...(d[fkey] || []), ...(d.projects || []).flatMap((p) => p[fkey] || [])]) {
    findings.push(row);
  }
  runs.sort((a, b) => String(field(b, "startedAt") || "").localeCompare(String(field(a, "startedAt") || "")));
  return { ...d, [key]: runs, [fkey]: findings };
};

/** runsInScope is the runs+findings pair for the live scope and history mode. */
export const runsInScope = (s) => {
  const scope = scopeAggregate(s);
  if (!scope) return { runs: [], findings: [] };
  const archived = s.historyMode === "archived";
  return {
    runs: (archived ? field(scope, "archivedRuns") : field(scope, "runs")) || [],
    findings: (archived ? field(scope, "archivedFindings") : field(scope, "findings")) || [],
  };
};

/**
 * unassignedRunCount is the count of runs with no project identity in the
 * payload's own history mode.
 *
 * It exists because the picker's total is a union (scopeAggregate above mixes
 * the store's runs in with every project's), and a total alone told the
 * operator nothing about how much of it could not be attributed to a
 * worktree — which is the number that decides whether the workspace picture is
 * trustworthy at all. It reads the same records the union reads, so it cannot
 * disagree with the card's value.
 */
export const unassignedRunCount = (s) => {
  const d = s.dashboard;
  if (!d) return 0;
  // The payload carries the real count; the records are a page of at most 20,
  // so reading the list length would report the page size as the population
  // and make a 598-run history look like a rounding error.
  const counted = s.historyMode === "archived" ? field(d, "archivedUnassignedRuns") : field(d, "unassignedRuns");
  if (typeof counted === "number") return counted;
  const runs = s.historyMode === "archived" ? field(d, "archivedRuns") : field(d, "runs");
  return (runs || []).length;
};

VIEWS.projects = {
  title: "项目总览",
  subtitle: "从本地调用记录识别仓库；无法验证归属的数据单独保留。",
  load: () => fetchDashboard(),
  render: (s) => html`<section id="projectOverview" class="project-overview">
    <div class="section-heading">
      <div><p class="eyebrow">WORKSPACES</p><h2>识别到的项目</h2></div>
      <p class="subtle">${(s.dashboard?.projects || []).length} 个可验证仓库 · ${s.dashboard?.unassignedCalls || 0} 条未归属调用</p>
    </div>
    <div id="projectList" class="project-list">
      ${(s.dashboard?.projects || []).length
      ? list(s.dashboard?.projects || [], projectCard)
      : s.loading ? loadPending(s)
      : html`<div class="empty-state"><span class="empty-icon">⌘</span><strong>暂未识别到项目</strong><small>仅当调用定位包含可验证的绝对 Git 仓库路径时，才会列为项目。</small></div>`}
    </div>
    ${(s.dashboard?.unassignedCalls || 0) > 0 ? unassignedCard(s) : ""}
  </section>`,
};

/**
 * WIDEN_LIMIT is the one wider window the chips offer. It is not
 * defaultWindowLimit's value by coincidence: it is the same window the cockpit
 * sent before the narrow default existed, so "放宽" restores exactly what the
 * old first screen showed rather than inventing a second vocabulary.
 */
const WIDEN_LIMIT = 60;

/**
 * issuesWindow names the window the list spans.
 *
 * The server's own wording is the fact of record (it is the query it ran), so
 * it wins whenever it is present. It is derived locally only when the payload
 * carries none — an older build, or a response from a build that predates the
 * field — because the two filter controls on screen already decide the window
 * and a blank line there reads as "unknown" rather than "narrow".
 */
const issuesWindow = (s) => s.issues.window || localWindowLabel(s);

const localWindowLabel = (s) => {
  const runs = (Number(s.limit) || 1) === 1 ? "最新 1 次运行" : `最近 ${Number(s.limit) || 1} 次运行`;
  const verdict = s.status === "" ? "全部状态" : `${statusOf(s.status).label}`;
  return `${runs} · ${verdict}`;
};

/**
 * convergeChips is the narrow/widen strip above the issue list.
 *
 * The narrow default answers "what did the last run find that I have not dealt
 * with", which is a list of single digits. The 37k-finding window is one click
 * away and stays one click away at every scroll position: a widening control
 * that only appears when the list is empty is a control nobody finds while
 * there is data on screen.
 *
 * It reuses facetRow's classes rather than adding a chip style, because these
 * are the same widget — a labelled toggle with the active one tinted.
 *
 * "清除筛选" clears every filter the strip controls, and is active whenever
 * none of them is in force. It used to hardcode `false` and reset only limit
 * and status, so clicking it with a rule selected left that rule on, dropped
 * both lit chips and lit "放宽" instead — a strip claiming nothing was filtered
 * while the list was still filtered. The active state here means "you are on
 * the widened baseline", not "click to undo".
 */
const convergeChips = (s) => {
  const baseline = Number(s.limit) === WIDEN_LIMIT && s.status === ""
    && !s.severity && !s.rule && !s.file && !s.query;
  return html`
  <div class="facet-group converge">
    <h3>范围</h3>
    ${convergeRow("只看最新 1 次运行", `data-action="converge.set" data-limit="1"`, Number(s.limit) === 1)}
    ${convergeRow(`放宽到最近 ${WIDEN_LIMIT} 次运行`, `data-action="converge.set" data-limit="${WIDEN_LIMIT}"`, Number(s.limit) === WIDEN_LIMIT)}
    ${convergeRow("只看未处理", `data-action="converge.set" data-status="open"`, s.status === "open")}
    ${convergeRow("清除筛选", `data-action="converge.clear"`, baseline)}
  </div>`;
};

/**
 * convergeRow draws one chip of the narrow/widen strip.
 *
 * The attributes arrive as one literal string and go in through raw(), not as
 * a dataset object joined into the template: html`` escapes every interpolated
 * value, so a built string lands as data-limit=&quot;1&quot; — an attribute the
 * DOM never sees and a chip that silently does nothing. A literal keeps the
 * action name greppable too, which is what the contract test reads.
 *
 * active is the state the chip *is*, not what it does: 清除筛选 restores the
 * pre-convergence read (limit=60, any status), which is the same state
 * 放宽到最近 60 次运行 can be in, so marking it active lit two chips at once
 * and left the strip unable to say where the operator stood.
 */
const convergeRow = (label, attrs, active) => html`
  <button type="button" class="facet-row${active ? " is-active" : ""}" ${raw(attrs)}>
    <span class="facet-label">${label}</span>
  </button>`;

VIEWS.issues = {
  title: "问题分诊",
  subtitle: "跨运行去重后的问题列表，可逐个流转状态。",
  load: async (filters) => fetchIssues(filters),
  // The three-column shape is SonarQube's, and the reason is that the detail
  // has to be readable *without* losing the list: stacked below, reading the
  // code window scrolls the very cards you are comparing away. `has-detail`
  // is an explicit class rather than `:has()` so the grid is decided by the
  // same state the pane renders from, and so a test can assert it.
  render: (s) => html`
    <div class="view-stack">
      <div class="issues-layout${s.openFingerprint ? " has-detail" : ""}">
        <aside class="issues-sidebar">
          ${facetPanel(s)}
          <div class="facet-group">
            <h3>排序</h3>
            <select class="button" data-action="sort.set" aria-label="排序方式">
              ${spec.sorts.map((key) => html`<option value=${key} ${s.sort === key ? raw("selected") : ""}>按 ${SORT_LABELS[key] || key} 排序</option>`)}
            </select>
          </div>
        </aside>
        <div class="issues-main">
          ${bulkBar(s)}
          ${convergeChips(s)}
          ${s.error || s.loading ? "" : html`<header class="issues-head">
            <span class="issues-count">${s.issues.total} 个问题</span>
            <span class="subtle">第 ${s.issues.page} / ${s.issues.totalPages} 页</span>
            ${issuesWindow(s) ? html`<span class="issues-window" title="服务端实际查询的范围">范围：${issuesWindow(s)}</span>` : ""}
          </header>`}
          <div class="issue-list">
            ${s.error ? loadError(s)
            : s.loading && !s.issues.issues.length ? loadPending(s)
            : issueTable(s.issues.issues)}
          </div>
          ${pager(s)}
        </div>
        ${issueDetail(s)}
      </div>
    </div>`,
};

/**
 * issueDetail is the four-tab pane for the selected issue. SonarQube's split
 * of one issue into where/why/activity/more is worth keeping because each tab
 * answers a different question, and shoving all four into one column makes the
 * operator read past the answer they wanted.
 *
 * It is a drawer, not a dialog: it stays in the page's own grid so the table it
 * describes keeps scrolling beside it. The close control is the same
 * `dialog.close` action the modal uses — the action clears openFingerprint, and
 * the drawer is drawn from that state, so there is one way to un-select a row
 * rather than two.
 */
const issueDetail = (s) => {
  if (!s.openFingerprint) return "";
  const issue = s.issues.issues.find((row) => row.fingerprint === s.openFingerprint);
  if (!issue) {
    return html`<aside class="panel issue-detail">
      <header class="issue-detail-head">
        <div class="issue-detail-title"><h2>该问题不在当前页</h2></div>
        <div class="issue-detail-actions"><button class="icon-button" data-action="issue.close" aria-label="关闭详情">×</button></div>
      </header>
      <p class="empty">翻页或清除筛选后重试。</p>
    </aside>`;
  }
  return html`
    <aside class="panel issue-detail">
      <header class="issue-detail-head">
        <div class="issue-detail-title">
          <p class="eyebrow">ISSUE ${issue.fingerprint}</p>
          <h2>${issue.message}</h2>
          <p class="issue-detail-loc"><code title="${issue.file}">${fileBase(issue.file)}:${issue.startLine}</code>
            <span class="issue-rule">${issue.ruleId}</span></p>
        </div>
        <div class="issue-detail-actions">${severityBadge(issue.severity)}${statusTag(issue.status)}
          <button class="icon-button" data-action="issue.close" aria-label="关闭详情">×</button></div>
      </header>
      ${issueTabBar(issue, s.issueTab)}
      ${s.issueTab === "where" ? issueWhere(issue, s.source)
      : s.issueTab === "why" ? issueWhy(issue)
      : s.issueTab === "activity" ? issueActivity(issue)
      : issueMore(issue)}
    </aside>`;
};

/**
 * bulkBar is the multi-select tray. It only appears when something is
 * selected: an always-visible tray with disabled buttons is a way of saying
 * "this feature exists" without letting anyone use it.
 */
const bulkBar = (s) => {
  if (!s.selected.size) return "";
  return html`
    <div class="bulk-bar">
      <span>已选 ${s.selected.size} 个问题</span>
      ${list(Object.entries(STATUS).filter(([key]) => key !== "open"), ([key, meta]) => html`
        <button class="button" data-action="bulk.status" data-status=${key} data-reason="bulk change">${meta.label}</button>`)}
      <button class="button ghost" data-action="bulk.clear">取消选择</button>
    </div>`;
};

/** pager steps through the server-side pages. */
const pager = (s) => {
  if (s.issues.totalPages <= 1) return "";
  return html`
    <nav class="pager" aria-label="分页">
      <button class="button" data-action="page.set" data-page="${s.issues.page - 1}" ${s.issues.page <= 1 ? raw("disabled") : ""}>上一页</button>
      <span class="subtle">${s.issues.page} / ${s.issues.totalPages}</span>
      <button class="button" data-action="page.set" data-page="${s.issues.page + 1}" ${s.issues.page >= s.issues.totalPages ? raw("disabled") : ""}>下一页</button>
    </nav>`;
};

/**
 * statusSelect is the per-issue verdict dropdown on a card.
 *
 * `data-status` carries the verdict already in force so the stylesheet can tint
 * the control: an untinted select next to a "已接受" badge reads as a verdict
 * nobody has reached, and the control that changes it is the one place the
 * current state must be visible before it is touched. The tone words are the
 * same ones statusTag uses, from STATUS.tone — one spelling per verdict.
 */
export const statusSelect = (issue) => html`
  <select class="issue-status tone-${statusOf(issue.status).tone}"
          data-action="issue.status" data-fingerprint=${issue.fingerprint}
          data-status=${issue.status} aria-label="流转状态">
    ${list(Object.entries(STATUS), ([key, meta]) => html`
      <option value=${key} ${issue.status === key ? raw("selected") : ""}>${meta.label}</option>`)}
  </select>`;

/**
 * issueTabs is the four-tab split SonarQube uses for one issue, and the reason
 * it works: the question changes per tab, so each tab answers exactly one.
 * Where is the anchor, Why does the rule fire, What happened to it over runs,
 * and everything the rule knows about itself.
 */
export const ISSUE_TABS = [
  ["where", "位置"], ["why", "原因"], ["activity", "履历"], ["more", "详情"],
];

/** issueTabBar renders the tab strip for the open issue. */
export const issueTabBar = (issue, active) => html`
  <nav class="tab-bar" role="tablist">
    ${list(ISSUE_TABS, ([key, label]) => html`
      <button class="tab${active === key ? " active" : ""}" role="tab" aria-selected=${active === key}
        data-action="issue.tab" data-tab=${key}>${label}</button>`)}
  </nav>`;

/**
 * issueWhere is the code window. It is the one place the cockpit shows source,
 * and it is only ever the file a finding names, around the anchor — never a
 * tree, never a search (docs/design/cockpit-sonarqube-parity.md §4.4).
 */
export const issueWhere = (issue, source) => {
  if (!source || source.reason) {
    return html`<div class="empty-state"><span class="empty-icon">▤</span>
      <strong>代码窗口不可用</strong><small>${source?.reason || "该问题没有可读取的文件路径。"}</small></div>`;
  }
  // An empty window is its own state, not an empty file: the read succeeded
  // and the file simply has no lines around the anchor. Falling through drew an
  // empty <ol> with "第 0-0 行 / 共 0 行", which reads as "this file is empty"
  // rather than "this window has nothing to show".
  if (!source.lines || !source.lines.length) {
    return html`<div class="empty-state"><span class="empty-icon">▤</span>
      <strong>代码窗口为空</strong><small>${source.file ? `${fileBase(source.file)} 在锚点附近没有可显示的行（文件可能已变化）。` : "该问题没有可读取的文件路径。"}</small></div>`;
  }
  return html`
    <div class="source-view">
      <header class="source-head">
        <code title="${source.file}">${fileBase(source.file)}</code>
        <span class="subtle">第 ${source.startLine}-${source.endLine} 行 / 共 ${source.totalLines} 行 · 锚点 ${source.anchorLine}${source.truncated ? " · 已截断" : ""}</span>
      </header>
      <ol class="source-lines">
        ${list(source.lines, (line) => html`
          <li class="source-line${line.number === source.anchorLine ? " is-anchor" : ""}${line.class ? ` hl-${line.class}` : ""}">
            <span class="source-no">${line.number}</span>
            <span class="source-text">${line.text || " "}</span>
            ${line.number === source.anchorLine ? html`<span class="source-anchor-note">${issue.message}</span>` : ""}
          </li>`)}
      </ol>
      <p class="privacy-note">只读取被本条问题命中的文件；窗口固定为锚点前后各 15 行。</p>
    </div>`;
};

/**
 * issueWhy states the rule and the evidence that fired it. The message itself
 * lives in the pane's header, where SonarQube puts it, so this tab answers the
 * next question instead of repeating the first one.
 */
export const issueWhy = (issue) => html`
  <div class="detail-grid">
    <div><dt>规则</dt><dd>${issue.ruleId}</dd></div>
    <div><dt>来源</dt><dd>${issue.source || "—"}</dd></div>
    <div><dt>严重度</dt><dd>${severityOf(issue.severity).label}</dd></div>
    <div><dt>锚点精度</dt><dd>${issue.anchor === "exact" ? "精确字节区间" : issue.anchor === "line_only" ? "仅行号" : "未定位"}</dd></div>
  </div>
  <p class="issue-message">${issue.message}</p>
  <p class="subtle">${issue.anchor === "exact"
    ? "锚点为精确字节区间：代码一旦改动，指纹自然失效，状态会诚实地重开。"
    : "锚点仅为行号：行号发生位移即视为不同问题，这是诚实但粗糙的退让。"}</p>`;

/**
 * issueActivity is the history tab. It used to be a table with one row per run
 * whose "结论" column said the same sentence on every row and whose "理由" column
 * repeated the issue's current status reason — a table is for rows that differ,
 * so this said nothing three times. The payload has no per-run timestamp and no
 * status-change event (internal/store/issues_query.go:36-68), so a real timeline
 * would be invented: what stays is the run list as one line and the status block
 * with its provenance, which are both facts the store does have.
 */
export const issueActivity = (issue) => html`
  <div class="activity-runs">
    <dt>出现于</dt>
    <dd>${issue.occurrences} 个运行：<span class="run-ids">${list(issue.runIds || [], (id) => html`<code>#${id}</code>`)}</span></dd>
  </div>
  <div class="detail-grid detail-grid-single">
    <div><dt>首次出现</dt><dd>${formatTime(issue.firstSeen)}</dd></div>
    <div><dt>最近出现</dt><dd>${formatTime(issue.lastSeen)}</dd></div>
    <div><dt>当前状态</dt><dd>${statusOf(issue.status).label}${issue.statusReason ? ` · ${issue.statusReason}` : ""}</dd></div>
    <div><dt>状态归属</dt><dd>${issue.statusOwner || "未署名"}${issue.fromLedger ? " · 来自让步账本" : " · 来自本次运行记录"}</dd></div>
  </div>
  <p class="subtle">载荷里没有逐次运行的时间戳，也没有状态变更事件，所以这里不画时间线；需要它就得先在后端记状态变更。</p>`;

/** issueMore is the raw record: everything not worth a tab of its own. */
export const issueMore = (issue) => html`
  <div class="detail-grid">
    <div><dt>指纹</dt><dd>${issue.fingerprint}</dd></div>
    <div><dt>文件</dt><dd title="${issue.file}">${fileBase(issue.file)}</dd></div>
    <div><dt>位置</dt><dd>${issue.startLine}:${issue.startColumn} - ${issue.endLine}:${issue.endColumn}</dd></div>
    <div><dt>最近运行</dt><dd>#${issue.lastRunId}</dd></div>
  </div>
  ${issue.fromLedger ? html`<p class="unassigned-notice">此状态来自让步账本（accepted.yaml），owner: ${issue.statusOwner || "未署名"}。</p>` : ""}`;

/**
 * projectState is the three-state read of one project record.
 *
 * The three states are `hasCalls` / `runsOnly` / `empty`, and the split exists
 * because a card that shows only calls and flagged made six worktrees look
 * identical: one with 600 runs and nothing attached to it read exactly like one
 * that had never been scanned. `runsOnly` is the state where the operator's
 * next action is a scan; `empty` is the state where the cockpit genuinely has
 * nothing to say about the worktree.
 *
 * runTotal and issueOpen come from the project record itself (§3.2). Number(x)
 * || 0 rather than `x`: a dashboard build without the two fields still has to
 * render, and an absent field must read as 0 rather than NaN.
 */
export const projectState = (p) => {
  const calls = Number(p && p.calls) || 0;
  const runTotal = Number(p && p.runTotal) || 0;
  const issueOpen = Number(p && p.issueOpen) || 0;
  if (calls > 0) return { key: "hasCalls", calls, runTotal, issueOpen };
  if (runTotal > 0) return { key: "runsOnly", calls, runTotal, issueOpen };
  return { key: "empty", calls, runTotal, issueOpen };
};

/**
 * projectEntries are the two doors offered on a card that has no calls.
 *
 * Neither reuses `scope.open`, and that is load-bearing rather than stylistic:
 * smoke.mjs counts scope.open occurrences to count project cards, so a second
 * scope.open inside a card would inflate that count. They are named for what
 * they do instead — open this project's run history, or tell me the command
 * that would produce data.
 */
const projectEntries = (project) => html`
  <span class="project-entries">
    <button type="button" class="text-button" data-action="project.runs" data-scope=${project.root}>查看运行历史</button>
    <button type="button" class="text-button" data-action="project.help" data-root=${project.root}>去跑一次检查</button>
  </span>`;

/**
 * projectCard is the landing list's unit: one verifiable worktree with the
 * numbers that decide which one to open, and — when there are no calls — the
 * two doors out of the dead end.
 */
export const projectCard = (project) => {
  const st = projectState(project);
  const secondary = st.key === "hasCalls" ? ""
    : st.key === "runsOnly"
      ? html`<span class="project-stats"><b>${st.runTotal}</b><small>次运行 · ${st.issueOpen} 个未处理问题</small></span>`
      : html`<span class="project-stats"><b>—</b><small>没有可验证的调用，也没有检查运行</small></span>`;
  return html`
  <span class="project-card tone-${st.key}" data-action="scope.open" data-scope=${project.root}>
    <span class="project-icon">⌘</span>
    <span class="project-copy"><strong>${project.name}</strong><small>${project.root}</small></span>
    ${secondary}
    <span class="project-latest">最近活动<br>${formatTime(project.latestActivity)}</span>
    <span class="project-arrow">↗</span>
    ${st.key === "hasCalls" ? "" : projectEntries(project)}
  </span>`;
};

/** unassignedCard is the door to the records with no provable worktree. */
export const unassignedCard = (s) => html`
  <button class="unassigned-card${s.scope === "" ? " selected" : ""}" data-action="scope.open" data-scope="">
    <strong>${s.dashboard?.unassignedCalls || 0}</strong>
    <span>条调用无法验证归属 →</span>
    <span>它们不混入任何项目，单独成页，避免把旁邻仓库的数字算到你头上。</span>
  </button>`;

/**
 * breadcrumbs names the live scope and is the way back to the picker.
 *
 * It is shell chrome, not view content: SonarQube puts the project path in the
 * header strip, where it survives a scroll and reads identically on every page.
 * Making it the shell's job means one implementation instead of eight views
 * each remembering to include it — and the wrapper stays in index.html, where
 * the element is, so this returns only its content.
 */
/**
 * scopeLabel names the live scope in the breadcrumb, in four states that are
 * each a different fact.
 *
 * The old two-state version wrote "未归属记录" for *every* unmatched scope, so
 * "unknown project" and "the records with no project" read identically — and
 * the unassigned records' own scope ("") never reached the branch at all,
 * because an empty string is falsy like null. Only "the project is not in the
 * payload" ever hit that text, so it was wrong for the one scope that owned it.
 *
 * Each state now says which one it is: "" is the unassigned records, a root
 * that matches is that project, a root that matches nothing after the
 * aggregate has loaded is an unknown project (a deleted worktree, a shared
 * link to a path this machine never saw), and a scope whose aggregate is not
 * loaded yet names the path it was given.
 *
 * That last state used to read "读取中…", which is not a name: most views never
 * load the aggregate — only the picker and the overview do — so deep-linking to
 * another page's issues left the breadcrumb saying "reading…" for as long as
 * the tab stayed open. The path is not a guess: it is the token the operator
 * asked for, and its last segment is what the picker would have called it.
 */
export const scopeLabel = (s) => {
  if (s.scope === "") return "未归属记录";
  if (!s.scope) return "全部项目";
  const project = projectOf(s);
  if (project) return project.name;
  if (!s.dashboard) return pathName(s.scope);
  return "未知项目";
};

/**
 * pathName is the trailing segment of a scope path — the label the project
 * picker would show for it — with the filesystem root kept as itself so a
 * scope of "/" reads as "/" rather than as an empty string.
 */
export const pathName = (scope) => {
  const trimmed = String(scope || "").replace(/[/\\]+$/, "");
  if (trimmed === "") return "/";
  const parts = trimmed.split(/[/\\]/);
  return parts[parts.length - 1] || trimmed;
};

export const breadcrumbs = (s) => {
  // The gate is `!== null`, not truthiness: the unassigned scope is the empty
  // string, which is falsy, so a truthy test dropped the label for exactly the
  // one scope whose name the operator most needs to see — the page looked like
  // the global picker while showing a filtered record set.
  return html`<button data-action="scope.clear">项目总览</button>
    ${s.scope !== null ? html`<span>›</span><strong>${scopeLabel(s)}</strong>` : ""}`;
};

/** metricCards are the four headline numbers of a workspace. */
export const metricCards = (s) => {
  const summary = summarizeCalls(callsInScope(s));
  const project = projectOf(s);
  // 「质量运行」一卡与运行历史同源：都数 scopeAggregate 并集里的运行条数
  // （app.js:1021），historyMode 决定数活跃还是归档。
  //
  // 以前这里读的是 `agg.trend.runs`，而那个数字来自另一个查询（全局 scope 下是
  // 未归属运行数，项目 scope 下是该项目的窗口计数），与列表里的条数可以不等。
  // 同一屏上「质量运行 4」和「1 条运行」各说各话，是操作员无法自行解决的矛盾。
  const { runs } = runsInScope(s);
  const runCount = runs.length;
  // “工具错误”一卡的 note 只有顶层 Summary.skipped（dashboard.go:42）：
  // ProjectSummary 没有该字段，前端不补一个（后端遗留 L8）。
  const skipped = field(field(s.dashboard, "summary") || {}, "skipped");
  return html`
  <section class="metrics-grid">
    <article class="metric-card"><p class="metric-label">调用总数</p><p class="metric-value">${summary.total}</p><p class="metric-note">${summary.total ? `${summary.sessions} 个会话 · ${formatTime(summary.first)} — ${formatTime(summary.last)}` : "尚无可归属的调用记录"}</p></article>
    <article class="metric-card accent"><p class="metric-label">验证拦截率</p><p class="metric-value">${percent(summary.verifyFlagged, summary.verifyCalls)}</p><p class="metric-note">${summary.verifyCalls ? `${summary.verifyFlagged} / ${summary.verifyCalls} 次验证发现问题` : "暂无验证调用"}</p></article>
    <article class="metric-card"><p class="metric-label">工具错误</p><p class="metric-value">${summary.toolErrors}</p><p class="metric-note">${skipped ? `${skipped} 条损坏日志已跳过` : "调用日志仅保存在本机"}</p></article>
    <article class="metric-card"><p class="metric-label">质量运行</p><p class="metric-value">${runCount}</p><p class="metric-note">${s.historyMode === "archived" ? "已归档运行" : s.scope === "" ? "未归属运行" : s.scope ? `${project?.name || "—"} · 带项目身份的运行` : `全部项目与未归属记录（含 ${unassignedRunCount(s)} 条未归属运行）`}</p></article>
  </section>`;
};

/**
 * summarizeCalls folds the call trace into the numbers the cards show. It is
 * exported because it is the honest answer to "what did this scope do", and
 * both the cards and the tests want the same folding.
 */
export function summarizeCalls(calls) {
  const byPhase = {};
  let verifyCalls = 0;
  let verifyFlagged = 0;
  let toolErrors = 0;
  const sessions = new Set();
  const times = [];
  for (const call of calls) {
    const phase = field(call, "phase") || "other";
    byPhase[phase] = (byPhase[phase] || 0) + 1;
    if (phase === "verify") {
      verifyCalls++;
      const verdict = String(field(call, "verdict") || "").toLowerCase();
      if (verdict && !verdict.startsWith("pass")) verifyFlagged++;
    }
    if (field(call, "error")) toolErrors++;
    if (field(call, "session")) sessions.add(field(call, "session"));
    if (field(call, "time")) times.push(field(call, "time"));
  }
  times.sort();
  return { byPhase, verifyCalls, verifyFlagged, toolErrors, sessions: sessions.size, total: calls.length, first: times[0], last: times.at(-1) };
}

/**
 * callMatches applies the search box and the three dropdowns to one record.
 * The search reads every field the table shows, so a query for a verdict or a
 * target path finds it without the operator guessing which column it lives in.
 */
export function callMatches(call, filters) {
  const text = [
    field(call, "tool"), field(call, "target"), field(call, "error"),
    field(call, "verdict"), field(call, "phase"),
  ].join(" ").toLowerCase();
  return (!filters.query || text.includes(filters.query))
    && (!filters.phase || field(call, "phase") === filters.phase)
    && (!filters.status || callState(call) === filters.status)
    && (!filters.trace || field(call, "traceId") === filters.trace);
}

/** callState classifies a record for the status filter and its badge. */
export const callState = (call) => {
  if (field(call, "error")) return "error";
  const verdict = String(field(call, "verdict") || "").toLowerCase();
  if (verdict.startsWith("pass")) return "pass";
  return field(call, "verdict") ? "flagged" : "";
};

/**
 * visibleCalls filters the scope's trace and keeps it newest-first, capped at
 * the documented 100 rows. Capping in the renderer rather than the API keeps
 * the count honest: "312 条 · 显示最近 100 条" is a fact, a silent 100 is a lie.
 */
export const visibleCalls = (s, cap = 100) => {
  const filtered = callsInScope(s).filter((call) => callMatches(call, s.callFilters));
  return { all: filtered, shown: filtered.slice(0, cap).reverse() };
};

/** traceOptions lists the trace ids on hand for the filter dropdown. */
export const traceOptions = (s) => [...new Set(callsInScope(s).map((c) => field(c, "traceId")).filter(Boolean))].sort();

VIEWS.calls = {
  title: "工具活动",
  subtitle: "本地调用轨迹：每一次约束查询、验证与复核。",
  load: () => fetchDashboard(),
  render: (s) => {
    const { all, shown } = visibleCalls(s);
    const traces = traceOptions(s);
    return html`
    <div class="view-stack">
      <section class="panel">
        <div class="panel-head">
          <div><p class="eyebrow">LOCAL TRACE</p><h2>调用轨迹</h2></div>
          <span class="subtle">${all.length} 条${all.length > shown.length ? ` · 显示最近 ${shown.length} 条` : ""}</span>
        </div>
        <div class="filters">
          <span class="search"><input type="search" placeholder="搜索工具、定位、结论…" value="${s.callFilters.query}" data-action="calls.query" aria-label="搜索调用"></span>
          <select data-action="calls.filter" data-filter="phase" aria-label="阶段筛选">
            <option value="">全部阶段</option>
            ${Object.entries(PHASES).map(([key, p]) => html`<option value=${key} ${s.callFilters.phase === key ? raw("selected") : ""}>${p.label}</option>`)}
          </select>
          <select data-action="calls.filter" data-filter="status" aria-label="结论筛选">
            <option value="">全部结论</option>
            <option value="pass" ${s.callFilters.status === "pass" ? raw("selected") : ""}>通过</option>
            <option value="flagged" ${s.callFilters.status === "flagged" ? raw("selected") : ""}>未通过</option>
            <option value="error" ${s.callFilters.status === "error" ? raw("selected") : ""}>错误</option>
          </select>
          <select data-action="calls.filter" data-filter="trace" aria-label="trace 筛选">
            <option value="">全部 trace</option>
            ${traces.map((t) => html`<option value=${t} ${s.callFilters.trace === t ? raw("selected") : ""}>${t}</option>`)}
          </select>
        </div>
        <div class="table-wrap">
          <table>
            <thead><tr><th>时间</th><th>工具</th><th>阶段</th><th>定位</th><th>耗时</th><th>结论</th><th></th></tr></thead>
            <tbody id="callsTable">
              ${shown.length ? list(shown, callRow)
                : s.error ? html`<tr><td colspan="7" class="table-empty load-error-cell">数据读取失败 · ${s.error} · <button type="button" class="text-button" data-action="view.retry">重试</button></td></tr>`
                : html`<tr><td colspan="7" class="table-empty">没有匹配的调用记录</td></tr>`}
            </tbody>
          </table>
        </div>
        <p class="privacy-note">此日志记录工具调用元数据，不包含传入的源码。</p>
      </section>
    </div>`;
  },
};

/**
 * runsHeading names the scope the run history is drawn from, because the
 * global page unions every project's runs with the store's unattributed ones
 * (scopeAggregate above) and a title that says only "active runs" hides which
 * of the two the operator is looking at.
 */
const runsHeading = (s) => {
  if (s.scope === "") return "未归属运行";
  if (s.scope) return `运行历史 · ${projectOf(s)?.name || "项目"}`;
  return "运行历史（全部项目 + 未归属）";
};

/** runsScopeLabel is the secondary line: active vs archived, and whose runs. */
const runsScopeLabel = (s) => {
  if (s.scope === "") return "未归属运行（无项目路径）";
  if (s.scope) return `${projectOf(s)?.name || "项目"} · 带项目身份的运行`;
  return `全部项目与未归属记录（含 ${unassignedRunCount(s)} 条未归属运行）`;
};

VIEWS.runs = {
  title: "运行历史",
  subtitle: "历次检查的运行、发现与归档状态。",
  load: () => fetchDashboard(),
  render: (s) => {
    const { runs, findings } = runsInScope(s);
    const project = projectOf(s);
    return html`
    <div class="view-stack">
      <section class="panel runs-panel">
        <div class="panel-head">
          <div><p class="eyebrow">RUN HISTORY</p><h2>${runsHeading(s)}</h2>
            <p class="panel-subtitle">${s.historyMode === "archived" ? "已归档运行" : "活跃运行"} · ${runsScopeLabel(s)}</p>
          </div>
          <span class="subtle" id="historyCount">${runs.length} 条${s.historyMode === "archived" && runs.length === 100 ? " · 显示最近 100 条" : ""}</span>
        </div>
        <p class="unassigned-notice" ${project || s.scope === "" ? "" : raw("hidden")}>
          ${project ? "只显示带项目身份的新运行；早期运行没有记录项目路径，仍留在「未归属记录」里。"
          : s.scope === "" ? `这些运行缺少项目路径，无法可靠地归入某个项目，因此单独列在这里。共 ${unassignedRunCount(s)} 条。`
          : `其中 ${unassignedRunCount(s)} 条运行缺少项目路径，无法归入任何项目，仍留在「未归属运行」里。`}
        </p>
        <div class="history-toolbar">
          <div class="history-tabs">
            <button class="button${s.historyMode === "active" ? " active" : ""}" data-action="history.mode" data-mode="active">活跃</button>
            <button class="button${s.historyMode === "archived" ? " active" : ""}" data-action="history.mode" data-mode="archived">已归档</button>
          </div>
          <span class="subtle" id="runsScopeNote" ${s.scope ? "" : raw("hidden")}>${runsScopeLabel(s)}</span>
          <button class="button primary" data-action="runs.archive" ${s.historyMode === "archived" || !s.runSelection.size ? raw("disabled") : ""}>归档所选</button>
          <button class="button primary" data-action="runs.restore" ${s.historyMode === "archived" && s.runSelection.size ? "" : raw("disabled")}>恢复所选</button>
        </div>
        <div class="runs-list" id="runsList">
          ${runs.length ? list(runs, (run) => runRow(run, findings, Boolean(field(run, "archived"))))
          : s.error ? loadError(s)
          : s.loading ? loadPending(s)
          : html`<div class="empty-state run-empty"><span class="empty-icon">◷</span><strong>还没有运行记录</strong><small>运行 metricgate check 后，每次检查都会落到这里，带项目的按项目分开。</small></div>`}
        </div>
      </section>
    </div>`;
  },
};

/**
 * trendScope states where the trend series comes from, because the server has
 * no merged series to send: ReportUnassigned (dashboard.go:114) is the only
 * global report, so a global view is honestly labelled "unassigned runs"
 * instead of being passed off as the whole history. A project scope names its
 * own series, and the label says which one is drawn.
 */
const trendScope = (s, series) => {
  const window = field(series, "window");
  if (s.scope === "") return `未归属运行的趋势${window ? ` · ${window}` : ""}（服务端只发未归属序列，不发送合并全量）`;
  if (s.scope) return `趋势：${projectOf(s)?.name || "项目"} 的运行`;
  return `未归属序列${window ? ` · ${window}` : ""}`;
};

/** trendEmptyNote explains the empty trend for the live scope only. */
const trendEmptyNote = (s) => (s.scope
  ? "该项目在所选时间窗内还没有带项目身份的运行。"
  : "历史运行未记录项目路径，仅能作为未归属趋势查看。");

/**
 * overview renders the workspace landing: the four headline numbers, the
 * gate-loop flow, the severity trend, and the concession ledger. Everything
 * here is a read of the one aggregate, which is why this view has no filter
 * controls — there is nothing to filter yet at this altitude.
 */
const workspaceOverview = (s) => {
  const summary = summarizeCalls(callsInScope(s));
  const series = (field(scopeAggregate(s), "trend")) || {};
  const points = field(series, "series") || [];
  const max = Math.max(1, ...points.map((p) => Number(field(p, "errors")) + Number(field(p, "warnings")) + Number(field(p, "infos"))));
  // 让步账本只有顶层载荷有：ProjectSummary（dashboard.go:62-77）没有 accepted 字段，
  // 项目级要显示它必须有后端新字段，本迭代不造数据（后端遗留 L8）。
  const accepted = s.dashboard?.accepted || [];
  return html`
  <div class="view-stack">
    ${metricCards(s)}
    <div class="layout-grid">
      <section class="panel">
        <div class="panel-head"><div><p class="eyebrow">GATE LOOP</p><h2>闸门循环</h2></div><span class="live-badge"><i></i>本地记录</span></div>
        <div class="flow">${list(Object.keys(PHASES), (key) => phaseBar(key, summary.byPhase))}</div>
      </section>
      <section class="panel">
        <div class="panel-head"><div><p class="eyebrow">SEVERITY TREND</p><h2>运行趋势</h2></div><span class="subtle">${field(series, "runs") || 0} 次运行</span></div>
        ${points.length
        ? html`<div class="trend-chart">${list(points, (p) => trendColumn(p, max))}</div>
            <div class="legend"><span><i class="dot error"></i>error</span><span><i class="dot warning"></i>warning</span><span><i class="dot info"></i>info</span></div>`
        : html`<div class="empty-state"><span class="empty-icon">⌁</span><strong>还没有运行趋势</strong><small>${trendEmptyNote(s)}</small></div>`}
        <p class="facet-note">${trendScope(s, series)}</p>
      </section>
    </div>
    <section class="panel accepted-panel" id="acceptedPanel" ${accepted.length ? "" : raw("hidden")}>
      <div class="panel-head"><div><p class="eyebrow">CONCESSION LEDGER</p><h2>让步账本</h2></div><span class="subtle" id="acceptedCount">${accepted.length} 条让步</span></div>
      <div class="accepted-list" id="acceptedList">${list(accepted, acceptedEntry)}</div>
    </section>
    ${metricTrendPanel(s)}
  </div>`;
};

/**
 * metricTrendPanel is the experiment half of the loop: per-run metric
 * distributions with the threshold that was in force. A tightening decision
 * is only checkable when the step in the threshold sits next to the
 * distribution — otherwise falling violations could just mean the code moved.
 */
const metricTrendPanel = (s) => {
  // 度量实验序列同样只有顶层载荷有：ProjectSummary（dashboard.go:62-77）没有 metricTrend，
  // 项目级要显示它必须有后端新字段，本迭代不造数据（后端遗留 L8）。
  const mt = s.dashboard?.metricTrend || { series: {} };
  const names = Object.keys(mt.series || {}).sort();
  if (!names.length) {
    return html`<section class="panel metric-trend-panel">
      <div class="panel-head"><div><p class="eyebrow">METRIC EXPERIMENT</p><h2>度量实验</h2></div></div>
      <div class="empty-state"><span class="empty-icon">⌗</span><strong>还没有度量快照</strong><small>运行 metricgate check（store.enabled）后这里显示每次运行的度量分布。</small></div>
    </section>`;
  }
  // The picker remembers its choice across refreshes; a refresh must not snap
  // the panel back to the first metric, which would hide the comparison the
  // operator opened it for.
  const choice = names.includes(s.metricChoice) ? s.metricChoice : names[0];
  const points = mt.series[choice] || [];
  return html`
    <section class="panel metric-trend-panel">
      <div class="panel-head">
        <div><p class="eyebrow">METRIC EXPERIMENT</p><h2>度量实验</h2></div>
        <select class="button" data-action="metric.choose" aria-label="选择度量">
          ${names.map((n) => html`<option value=${n} ${n === choice ? raw("selected") : ""}>${n}</option>`)}
        </select>
      </div>
      <div class="table-wrap">
        <table>
          <thead><tr><th>运行</th><th>时间</th><th>p50</th><th>p90</th><th>max</th><th>样本</th><th>warn 阈值</th><th>超阈</th></tr></thead>
          <tbody id="metricTrendBody">
            ${list(points, (p, i) => html`<tr class="${metricStep(p, points[i - 1])}"><td>#${p.runId}</td><td>${String(p.startedAt || "").slice(5, 16)}</td><td>${p.p50}</td><td>${p.p90}</td><td>${p.max}</td><td>${p.samples}</td><td>${p.warnThreshold}</td><td>${p.violated}</td></tr>`)}
          </tbody>
        </table>
      </div>
    </section>`;
};

// There is one workspace landing, and it is called overview. It used to be two
// views — overview (the local loop) and summary (gate + measures) — which meant
// a shared URL could name either, the rail marked one active while the tab bar
// marked the other, and "概览" was two different pages depending on how you
// arrived. SonarQube's project Overview is one page: verdict, then measures.
VIEWS.overview = {
  title: "项目概览",
  subtitle: "门禁结论、评级度量、闸门循环与让步。",
  load: async () => {
    await Promise.all([fetchDashboard(), fetchGate(), fetchMeasures()]);
  },
  render: (s) => html`
    <div class="view-stack">
      ${gateBanner(s.gate)}
      ${emptyProjectPanel(s)}
      <section class="measures-grid">
        ${list(s.measures?.measures || [], measureCard)}
      </section>
      ${s.measures?.foreign
        ? html`<p class="subtle measure-foreign">以上度量来自另一次运行的快照（不属于当前项目），不是本项目的结论。</p>`
        : ""}
      ${s.measures?.reason ? html`<p class="subtle">${s.measures.reason}</p>` : ""}
    </div>
    ${workspaceOverview(s)}`,
};

/**
 * emptyProjectPanel is the guidance for a project with no runs at all.
 *
 * A project scope is not the global one, so this panel is the only thing that
 * answers "why is this page empty" for it: the panels below show what the
 * workspace holds, which is nothing, and reading zeros there without being told
 * the worktree has never been scanned is the dead end this panel closes. It
 * reads the project's own runTotal rather than the top-level dashboard field,
 * because the dashboard's runs are the store's unattributed ones — a global
 * count shown on a project page is exactly the wrong number.
 */
const emptyProjectPanel = (s) => {
  if (!s.scope || s.scope === "") return "";
  const project = projectOf(s);
  if (!project) return "";
  const st = projectState(project);
  if (st.runTotal > 0) return "";
  return html`
  <section class="panel">
    <div class="panel-head"><div><p class="eyebrow">NO RUNS YET</p><h2>该项目暂无质量运行</h2></div></div>
    <p class="subtle">${st.calls > 0
      ? "该仓库有调用记录，但还没有一次带项目身份的检查运行，所以下面没有可核对的结论。"
      : "该仓库既没有可归属的调用记录，也没有检查运行，所以下面没有可展示的结论。"}</p>
    <p class="project-entries">
      <button type="button" class="button" data-action="project.runs" data-scope=${project.root}>查看运行历史</button>
      <button type="button" class="button" data-action="project.help" data-root=${project.root}>去跑一次检查</button>
    </p>
  </section>`;
};

VIEWS.policy = {
  title: "扫描配置",
  subtitle: "配置发现链、当前生效的文档与每个键的出处；保存后立即参与后续检查。",
  load: () => fetchPolicy(),
  render: (s) => policyPane(s),
};

/**
 * policyPane is the configuration surface. The raw document is the editing
 * area and the parsed structure is the navigation, because "what would I
 * type" and "what does it mean" are different questions with the same answer
 * behind them. Provenance is not decoration: a threshold whose origin is
 * "builtin" is a different claim than one this file wrote.
 */
const policyPane = (s) => {
  const p = s.policy;
  if (!p) return html`<div class="empty-state"><strong>读取配置中…</strong></div>`;
  const tried = new Set(p.tried || []);
  // draft is the unsaved edit, if any; the textarea and the dirty hint both read
  // it so a repaint cannot show a document the operator did not last type.
  const draft = s.policyDraft !== null && s.policyDraft !== (p.content || "");
  return html`
  <div class="view-stack">
    <section class="panel policy-pane">
      <div class="panel-head">
        <div><p class="eyebrow">DISCOVERY</p><h2>${p.found ? p.name : "使用内置默认值"}</h2></div>
        <span class="subtle">${p.found ? p.path : "未找到配置文件"}</span>
      </div>
      <p class="policy-intro">发现顺序自上而下，命中的那一份生效；其余候选未被读取。保存写回同一路径，因此界面改动不可能与下一次检查不一致。</p>
      <div class="policy-layout">
        <aside class="policy-side">
          <div>
            <h3>发现链</h3>
            <div class="policy-chain">
              ${list(p.candidates || [], (c) => {
                const inForce = p.found && c === p.name;
                const mark = inForce ? "◉" : tried.has(c) ? "○" : "·";
                const klass = inForce ? "in-force" : tried.has(c) ? "missed" : "";
                const suffix = inForce ? " · 生效" : tried.has(c) ? " · 未命中" : "";
                return html`<div class="policy-chain-step ${klass}"><span class="mark">${mark}</span><span>${c}${suffix}</span></div>`;
              }) || html`<div class="empty">—</div>`}
            </div>
          </div>
          <div>
            <h3>分节</h3>
            <div class="policy-sections">
              ${list(p.sections || [], (sec) => html`<button type="button" class="policy-section-chip" data-action="policy.jump" data-line="${Number(sec.line) || 1}">${sec.key}</button>`)
              || html`<span class="subtle">无可解析的分节</span>`}
            </div>
          </div>
        </aside>
        <div class="policy-editor">
          <div class="policy-editor-bar">
            <span class="policy-file-label">${p.found ? p.name : "policy.yaml"}</span>
            <span class="policy-hint" ${p.error ? "" : raw("hidden")}>${p.error}</span>
            <span class="policy-dirty" id="policyDirty" ${draft ? "" : raw("hidden")}>有未保存的改动</span>
            <span class="policy-editor-actions">
              <button class="button" data-action="policy.reload">重新读取</button>
              <button class="button primary" data-action="policy.save" ${p.found ? "" : raw("disabled")}>保存配置</button>
            </span>
          </div>
          <textarea class="policy-textarea" id="policyContent" spellcheck="false" data-action="policy.edit">${s.policyDraft ?? p.content ?? ""}</textarea>
          <p class="subtle">${p.modTime ? `磁盘版本 ${formatTime(p.modTime)}` : p.found ? "读取时间未知" : "（不存在）"}</p>
        </div>
      </div>
      <div class="policy-keys-panel panel">
        <div class="panel-head"><div><p class="eyebrow">EFFECTIVE</p><h2>生效键与出处</h2></div><span class="subtle">${(p.keys || []).length ? `${p.keys.length} 个已显式设置的键` : "全部为内置默认值"}</span></div>
        <div class="table-wrap">
          <table>
            <thead><tr><th>键</th><th>取值来源</th></tr></thead>
            <tbody>
              ${(p.keys || []).length
              ? list(p.keys || [], (k) => html`<tr><td>${k.key}</td><td>${policySourceLabel(k)}</td></tr>`)
              : html`<tr><td colspan="2" class="table-empty">该文档没有显式设置任何键，全部沿用内置默认值。</td></tr>`}
            </tbody>
          </table>
        </div>
        <p class="privacy-note">取值列不显示实际数值：服务端的 entry 只带 key 与来源（policyKeys → {Key, Source}），没有解析出的值；真值在左侧的磁盘版本文本框里，那一份才是会被写回的内容。</p>
      </div>
    </section>
  </div>`;
};

/** policySourceLabel keeps an inherited value visibly unlike a written one. */
const policySourceLabel = (entry) => {
  if (!entry) return "—";
  if (entry.source === "policy_file") return "本文件";
  if (entry.source === "builtin") return "内置默认值";
  // An empty source is "nobody said", not a source whose name is blank: the
  // fallback used to render the bare prefix "来自" with nothing after it.
  if (!entry.source) return "—";
  return `来自 ${entry.source}`;
};

/**
 * facetCounts returns the numbers the rail draws.
 *
 * The preference is a rule, not a fallback: the cached unfiltered counts first,
 * because those are the only ones that survive a chip click; the current
 * response only when no unfiltered read has happened yet, which is the deep
 * link case. Callers must not read s.issues.severities directly.
 */
export const facetCounts = (s) => (s.facets && Object.keys(s.facets.severities || {}).length
  ? s.facets
  : { severities: s.issues.severities, statuses: s.issues.statuses, rules: s.issues.rules || [] });

/**
 * facetNote names the universe the rail's counts describe — never implied.
 *
 * On a failed read the note stops qualifying the counts and says there are
 * none: the rail below is empty either way, and "全量计数" over an empty rail
 * reads as "the store holds zero findings" rather than "the read did not
 * happen".
 */
const facetNote = (s) => {
  if (s.error) return html`<p class="facet-note load-error-cell">读取失败：无法提供计数</p>`;
  return html`<p class="facet-note">${s.facets
    ? "全量计数，不随筛选变化"
    : (s.severity || s.status || s.rule || s.file || s.query
      ? "尚无全量计数：清除筛选后重新读取" : "全量计数")}</p>`;
};

/**
 * facetEmpty is the rail's stand-in for an empty or unread group.
 *
 * "暂无数据" and "读取失败" answer "why is this group blank" differently, and
 * only one of them is fixable by retrying. Splitting the two here rather than
 * at each of the four groups keeps them from drifting into their own wording of
 * the same distinction.
 */
const facetEmpty = (s) => html`<p class="subtle load-error-cell">${s.error ? "读取失败" : "暂无数据"}</p>`;

/**
 * facetPanel is the left filter rail.
 *
 * Clicking a facet must not make the other facets' counts collapse toward the
 * selection, because then the operator cannot see what else is available and has
 * to clear a filter to find out. The server only honours that promise for
 * severities, statuses and sources: the rules facet is counted after the
 * filters. The rail therefore draws from the cached unfiltered read (facetCounts)
 * and says which universe its numbers describe (facetNote) instead of letting a
 * filtered response pass itself off as the whole.
 */
const facetPanel = (s) => {
  const facets = facetCounts(s);
  return html`
  ${facetNote(s)}
  <div class="facet-group">
    <h3>搜索</h3>
    <span class="search"><input type="search" id="issueSearch" placeholder="消息、规则、文件…"
      value="${s.query || ""}" data-action="issues.query" aria-label="搜索问题"></span>
  </div>
  <div class="facet-group">
    <h3>严重度</h3>
    ${!s.error && Object.keys(facets.severities).length
      ? list(Object.entries(facets.severities), ([value, count]) => facetRow("severity", value, `${severityOf(value).label}`, count, s.severity === value))
      : facetEmpty(s)}
  </div>
  <div class="facet-group">
    <h3>状态</h3>
    ${!s.error && Object.keys(facets.statuses).length
      ? list(Object.entries(facets.statuses), ([value, count]) =>
          facetRow("status", value, statusOf(value).label, count, s.status === value))
      : facetEmpty(s)}
  </div>
  <div class="facet-group">
    <h3>规则</h3>
    ${!s.error && (facets.rules || []).length
      ? list((facets.rules || []).slice(0, 12), (r) => facetRow("rule", r.RuleID, r.RuleID, r.Count, s.rule === r.RuleID))
      : facetEmpty(s)}
  </div>`;
};

/**
 * activity fuses the two event streams the cockpit has: stored runs and local
 * calls. Mixing them is the point — the same conversation produced both, and
 * a scan that follows a flagged verify call is the story the loop is for.
 */
VIEWS.activity = {
  title: "活动",
  subtitle: "运行、调用与配置改动的混排时间线。",
  load: () => fetchDashboard(),
  render: (s) => {
    const events = activityEvents(s);
    // The picker is the one scope with no series of its own: the server sends
    // an unassigned series and a per-project series, never a merged one, so a
    // chart drawn here would show one scope's history under another's name.
    const series = (s.scope === "" ? field(s.dashboard, "trend") : projectOf(s)?.trend) || {};
    const points = field(series, "series") || [];
    const max = Math.max(1, ...points.map((p) => Number(field(p, "errors")) + Number(field(p, "warnings")) + Number(field(p, "infos"))));
    return html`
    <div class="view-stack">
      ${points.length ? html`<section class="panel">
        <div class="panel-head"><div><p class="eyebrow">HISTORY</p><h2>运行趋势</h2></div><span class="subtle">${field(series, "runs") || 0} 次运行 · ${field(series, "window") || ""}</span></div>
        <p class="chart-caption">每根柱一次运行，单位是 finding 条数（error / warning / info 堆叠）；纵轴峰值 <strong>${max}</strong>，横轴是运行日期。</p>
        <div class="trend-chart" role="img" aria-label="运行趋势：每根柱一次运行的 finding 条数，纵轴峰值 ${max}">${list(points, (p) => trendColumn(p, max))}</div>
        <div class="legend"><span><i class="dot error"></i>error</span><span><i class="dot warning"></i>warning</span><span><i class="dot info"></i>info</span><span class="legend-scale">峰值 ${max}</span></div>
      </section>` : ""}
      <section class="panel">
        <div class="panel-head"><div><p class="eyebrow">TIMELINE</p><h2>事件时间线</h2></div><span class="subtle">${events.length} 个事件</span></div>
        ${events.length
        ? html`<div class="timeline">${list(events, activityRow)}</div>`
        : s.error ? loadError(s)
        : html`<div class="empty-state"><span class="empty-icon">◷</span><strong>还没有事件</strong><small>运行 metricgate check 或产生一次调用后，这里按时间倒序混排两类事件。</small></div>`}
      </section>
    </div>`;
  },
};

/**
 * activityEvents merges runs and calls into one newest-first list.
 *
 * The runs come from runsInScope, so "unassigned", "project" and "picker"
 * mean the same thing here as they do in the run history. The falling back to
 * dashboard.runs that this replaced meant one run could appear under two
 * breadcrumbs at once: a project scope whose own list was short borrowed the
 * store's unassigned runs, which carry no project identity.
 */
export function activityEvents(s) {
  const runs = runsInScope(s).runs;
  const events = [...runs.map(runEvent), ...callsInScope(s).map(callEvent)];
  // Newest first, capped: the timeline is a headline, not an archive. The cap
  // keeps a long-running local log from building one enormous document.
  return events.sort((a, b) => String(b.at).localeCompare(String(a.at))).slice(0, 200);
}

/** runEvent projects one stored run into the timeline's shape. */
const runEvent = (run) => ({
  at: field(run, "startedAt") || "", kind: "run",
  title: `运行 #${field(run, "id")}`,
  tone: Number(field(run, "errorCount")) ? "error" : "pass",
  note: `${field(run, "files") || 0} 个文件 · ${field(run, "errorCount") || 0} errors · ${field(run, "warningCount") || 0} warnings · ${field(run, "durationMs") || 0} ms`,
});

/** callEvent projects one local call into the same shape. */
const callEvent = (call) => ({
  at: field(call, "time") || "", kind: "call",
  title: field(call, "tool") || "—",
  tone: field(call, "error") ? "error" : "info",
  note: `${phaseLabel(field(call, "phase"))} · ${field(call, "target") || "—"}`,
});

/** activityRow is one entry of the fused timeline. */
export const activityRow = (event) => html`
  <article class="timeline-row tone-${event.tone}">
    <span class="timeline-kind">${event.kind === "run" ? "◷" : "◆"}</span>
    <div class="timeline-body">
      <strong>${event.title}</strong>
      <small>${event.note}</small>
    </div>
    <time class="timeline-when">${relativeTime(event.at)}</time>
  </article>`;

/**
 * gateConditionRows turns the condition list into table rows, one header per
 * language.
 *
 * The payload repeats every measure once per configured language
 * (`internal/cockpit/gate_api.go` gateConditions loops configLangs), so a
 * three-language policy produced three identical "Cyclomatic complexity ≤ 15"
 * rows and nothing in the table said which one covered which file — or which
 * threshold the row was judging against. The header is what makes the row
 * legible; the rows are not merged, because the same measure with two bands is
 * two conditions and collapsing them would hide one.
 *
 * A condition with no language renders ungrouped rather than under an empty
 * header: an older server's payload is a state, and a nameless group is worse
 * than no grouping.
 */
export const gateConditionRows = (conditions) => {
  const rows = [];
  let group = null;
  conditions.forEach((c) => {
    if (c.language && c.language !== group) {
      group = c.language;
      rows.push(html`<tr class="gate-lang-row"><th scope="rowgroup" colspan="7"><span class="gate-lang">${c.language}</span></th></tr>`);
    }
    rows.push(html`
      <tr id="${conditionId(c)}">
        <td>${c.label || c.measure}</td>
        <td>${c.operator}</td>
        <td>${c.value}</td>
        <td>${c.hasData ? c.current : html`<span class="cell-unknown">无数据</span>`}</td>
        <td><span class="headroom headroom-${gateHeadroom(c).tone}">${gateHeadroom(c).text}</span></td>
        <td>${c.source || "内置"}</td>
        <td>${c.failing ? badge("发现问题") : c.warning ? badge("接近阈值") : c.hasData ? badge("通过") : badge("无数据")}</td>
      </tr>`);
  });
  return join(rows);
};

/**
 * gate projects the policy's thresholds into the condition table the checker
 * would actually enforce, next to the value the last run measured. "warn" is
 * shown as a condition too, so an operator can see a band they are about to
 * cross before it starts failing the build.
 */
VIEWS.gate = {
  title: "质量门禁",
  subtitle: "当前生效的条件与最近一次运行的实测值。",
  load: async () => {
    await Promise.all([fetchGate(), fetchDashboard()]);
  },
  render: (s) => html`
    <div class="view-stack">
      ${gateBanner(s.gate)}
      <section class="panel">
        <div class="panel-head">
          <div><p class="eyebrow">CONDITIONS</p><h2>门禁条件</h2></div>
          <span class="subtle">${gateMeta(s.gate)}</span>
        </div>
        <div class="table-wrap">
          <table>
            <thead><tr><th>度量</th><th>操作</th><th>阈值</th><th>实测（最差）</th><th>裕度</th><th>阈值来源</th><th>状态</th></tr></thead>
            <tbody>
              ${(s.gate?.conditions || []).length
              ? gateConditionRows(s.gate?.conditions || [])
              : s.error ? html`<tr><td colspan="7" class="table-empty load-error-cell">数据读取失败 · ${s.error} · <button type="button" class="text-button" data-action="view.retry">重试</button></td></tr>`
              : html`<tr><td colspan="7" class="table-empty">没有可投影为条件的阈值；policy 未配置任何度量。</td></tr>`}
            </tbody>
          </table>
        </div>
        <p class="panel-foot metric-convention">阈值为「最差单文件」口径：引擎在任一文件越过 error 线时判失败，平均值不参与判定。实测列按语言分别取数：同一度量在 go 与 java 下显示的是各自语言自己的最差单文件值，某语言本次运行没有该度量的快照时如实显示「无数据」，不借用其它语言的值。状态列区分三态：越线为「发现问题」，落在 warning 与 error 线之间为「接近阈值」，未测量为「无数据」。上方结论只数 findings 的 error 级，本表越线不会让它变红：表回答的是「下一次改动会不会越线」，结论回答的是「这次改动有没有产生 error」。</p>
      </section>
    </div>`,
};

/**
 * rules lists the effective rules with the reason each exists. A rule without
 * a stated reason is a rule nobody can review, so the table shows a dash
 * instead of hiding the column: the gap is the finding.
 */
VIEWS.rules = {
  title: "规则",
  subtitle: "生效规则、存在理由与出处。",
  // Both reads: the catalogue is the table, the effective keys are the fallback
  // that stays reachable when /api/rules cannot answer (older server, disabled
  // store, a scope with no policy file). They are two requests because they are
  // two answers, not one payload twice.
  load: async () => {
    await Promise.all([fetchRules(), fetchPolicy()]);
  },
  render: (s) => html`
    <div class="view-stack">
      ${rulesCatalogue(s)}
      ${policyKeysSection(s)}
    </div>`,
};

/**
 * RULE_REASONS is the machine-code to Chinese mapping for /api/rules' reason.
 *
 * The backend returns the code rather than the sentence because it is the same
 * vocabulary config.ExplainRule and the policy files already use, and it is not
 * this file's job to guess a second set of codes. An unknown code renders as
 * itself: a made-up translation of a code nobody has seen is a claim about the
 * engine that cannot be checked.
 */
const RULE_REASONS = {
  configured: "策略文件里显式配置",
  builtin: "内置默认，未在策略中覆盖",
  language_not_configured: "该语言未在策略中启用",
  custom_rule: "自定义 DSL 规则",
  rule_not_found: "目录中有、策略未识别（需排查）",
};

const ruleReason = (reason) => RULE_REASONS[String(reason || "")] || String(reason || "—");

/** ruleToggle spells the on/off state as a word rather than a bare boolean. */
const ruleToggle = (rule) => (rule.enabled ? "启用" : "关闭");

/**
 * rulesCatalogue is the primary table: what the engine will do, and why.
 *
 * It answers the question the effective-key table could not — "why does this
 * rule exist" — because the catalogue pairs each rule with the reason
 * ExplainRule gave and the thresholds actually in force, both of which the
 * policy file alone does not contain.
 */
const rulesCatalogue = (s) => {
  const rules = s.rules?.rules || [];
  const ordered = rulesOrderBy(rules, s.rulesSort);
  return html`
  <section class="panel">
    <div class="panel-head"><div><p class="eyebrow">RULE CATALOGUE</p><h2>规则目录</h2></div>
      <span class="subtle">${s.rules?.policyPath ? `策略：${s.rules.policyPath}` : (s.rules?.languages || []).length ? `${(s.rules.languages || []).length} 种语言` : ""}</span></div>
    ${rules.length
    ? html`<div class="table-wrap">
        <table class="rules-table">
          <thead><tr>${rulesHead(s.rulesSort, "language", "语言")}${rulesHead(s.rulesSort, "id", "规则")}${rulesHead(s.rulesSort, "title", "说明")}${rulesHead(s.rulesSort, "severity", "严重度")}${rulesHead(s.rulesSort, "warn", "warn")}${rulesHead(s.rulesSort, "error", "error")}${rulesHead(s.rulesSort, "policySource", "生效来源")}${rulesHead(s.rulesSort, "enabled", "开关")}${rulesHead(s.rulesSort, "reason", "存在理由")}</tr></thead>
          <tbody>
            ${list(ordered, (r) => html`<tr>
              <td>${r.language}</td><td>${r.id}</td><td>${r.title || "—"}</td>
              <td>${severityOf(r.severity).label}</td>
              <td>${thresholdCell(r, "warn")}</td><td>${thresholdCell(r, "error")}</td>
              <td>${ruleSourceLabel(r)}</td><td>${ruleToggle(r)}</td>
              <td title="${r.reason || ""}">${ruleReason(r.reason)}${r.note ? ` · ${r.note}` : ""}</td>
            </tr>`)}
          </tbody>
        </table>
      </div>`
    : s.error ? html`<div class="load-error-cell">规则目录读取失败 · ${s.error} · <button type="button" class="text-button" data-action="view.retry">重试</button></div>`
    : html`<p class="subtle">该 scope 没有可列出的规则（策略未启用任何语言）。</p>`}
  </section>`;
};

/** thresholdCell shows a band the rule carries, and a dash when it has none. */
/**
 * thresholdCell reads the warn/error band a rule carries.
 *
 * The value is flat on the wire (`warn` / `error`), not nested under a
 * `thresholds` object: reading `rule.thresholds` found nothing and drew "—" for
 * every row, which happened to look right for boolean rules but would have
 * blanked a metric's real number too. Zero still draws "—" because the policy
 * treats 0 as "unset, use the default" rather than as a threshold of zero
 * (internal/config/config.go:154-157).
 */
const thresholdCell = (rule, band) => {
  const value = rule[band] !== undefined && rule[band] !== null ? rule[band]
    : rule.thresholds ? rule.thresholds[band] : undefined;
  return value === undefined || value === null || value === "" || value === 0 ? "—" : String(value);
};

/** ruleSourceLabel names where the value in force came from. */
const ruleSourceLabel = (rule) => policySourceLabel({ source: rule.policySource });

/**
 * rulesOrderBy sorts the catalogue the same way the effective-key table is
 * sorted, so the header arrows mean one thing across both tables.
 *
 * The cell is derived rather than read: warn/error are threshold keys and
 * enabled is a boolean, and the operator clicks the column they can see.
 */
const rulesOrderBy = (rules, sort) => {
  const cell = (r) => (sort.key === "warn" || sort.key === "error" ? thresholdCell(r, sort.key)
    : sort.key === "severity" ? severityOf(r.severity).label
    : sort.key === "policySource" ? ruleSourceLabel(r)
    : sort.key === "reason" ? ruleReason(r.reason)
    : sort.key === "enabled" ? ruleToggle(r)
    : r[sort.key] || "");
  const dir = sort.dir === "desc" ? -1 : 1;
  return [...rules].sort((a, b) => String(cell(a)).localeCompare(String(cell(b)), "zh") * dir);
};

/**
 * policyKeysSection is the effective-key table, demoted to a collapsed
 * secondary region.
 *
 * It stays because it answers a question the catalogue does not: "what did this
 * file actually write", including metric thresholds that are configuration
 * rather than rules. It is folded rather than beside the catalogue because two
 * tables of 138 and 200 rows side by side is a page nobody reads, and because
 * it is the fallback view when /api/rules cannot answer.
 */
const policyKeysSection = (s) => {
  const keys = rulesOrder(s.policy?.keys || [], s.rulesSort);
  return html`
  <details class="panel policy-keys-details">
    <summary>生效配置键（${(s.policy?.keys || []).length}）</summary>
    <div class="table-wrap">
      <table class="rules-table">
        <thead><tr>${rulesHead(s.rulesSort, "key", "键（度量配置）")}${rulesHead(s.rulesSort, "value", "取值（不可读）")}${rulesHead(s.rulesSort, "source", "出处")}</tr></thead>
        <tbody>
          ${keys.length
          ? list(keys, (k) => html`<tr><td>${k.key}</td><td>${k.value || "—"}</td><td>${policySourceLabel(k)}</td></tr>`)
          : s.error ? html`<tr><td colspan="3" class="table-empty load-error-cell">数据读取失败 · ${s.error} · <button type="button" class="text-button" data-action="view.retry">重试</button></td></tr>`
          : html`<tr><td colspan="3" class="table-empty">policy 未显式配置任何键；全部使用内置默认值。</td></tr>`}
        </tbody>
      </table>
    </div>
    <p class="privacy-note">「取值（不可读）」一列没有数值不是数据丢失：服务端只发键名与出处（policyKeys → {Key, Source}），不发送解析后的值。实际生效的数值要看磁盘上的那份文档（见「扫描配置」页的文本框），那里才是检查引擎读取的内容。</p>
  </details>`;
};

/**
 * rulesOrder sorts the effective-key list for the Rules table.
 *
 * "取值" and "出处" are derived, not stored on the key: the value is a display
 * string that is empty for every key today (see L10) and the source label goes
 * through policySourceLabel. Sorting the rendered cell rather than the field
 * means the column the operator clicks is the column that reorders.
 */
const rulesOrder = (keys, sort) => {
  const cell = (k) => (sort.key === "value" ? (k.value || "")
    : sort.key === "source" ? policySourceLabel(k) : k.key);
  const dir = sort.dir === "desc" ? -1 : 1;
  return [...keys].sort((a, b) => String(cell(a)).localeCompare(String(cell(b)), "zh") * dir);
};

/**
 * rulesHead is one sortable column header.
 *
 * aria-sort is the contract, not the arrow glyph: a screen reader has no use
 * for "↑", and a header that says "sorted" while the rows are not ordered by it
 * is the same lie either way. The arrow is for the reader who does not trust
 * an attribute.
 */
const rulesHead = (sort, key, label) => {
  const active = sort.key === key;
  const arrow = active ? (sort.dir === "asc" ? "↑" : "↓") : "";
  return html`<th aria-sort="${active ? (sort.dir === "asc" ? "ascending" : "descending") : "none"}"
    ><button type="button" class="th-sort" data-action="rules.sort" data-key="${key}"
      ><span>${label}</span><i class="sort-arrow">${arrow || "↕"}</i></button></th>`;
};

//! S8 · BOOT — the only section with side effects.

/**
 * ACTIONS is the one place UI events are wired. Every [data-action] element in
 * a template is handled here; a template naming an action the table lacks fails
 * the Go test rather than silently doing nothing on click.
 */
// Exported so the dev render driver can assert against the real table instead
// of a restated copy: a copy of this list can only ever confirm what its author
// remembered, which is exactly how "issue.tab" went unnoticed while every other
// action was checked.
export const ACTIONS = {};

/**
 * view.retry re-reads the view after a failed or timed-out load.
 *
 * It is the escape hatch the error cards name, and it is a fresh load rather
 * than a repaint of what is already in memory: a repaint would repaint the
 * same empty list that failed to answer, with no new request behind it.
 */
ACTIONS["view.retry"] = () => rerender();

/**
 * gate.locate jumps from the verdict to the row that caused it.
 *
 * The banner said FAILED with a reason in words, and the table below held the
 * number, but nothing joined them: an operator had to read the reason, guess
 * which measure it named, then scan a table that is grouped by language for
 * the right one. The scroll is to the row, not to the table, and the row is
 * marked as well as scrolled to — a highlight is what makes it obvious the
 * page moved because a button was pressed rather than because the list
 * re-rendered.
 *
 * The class is temporary by design: it marks where the jump landed. A
 * permanent tint would become part of the table's normal appearance, and the
 * next reader would not know whether it meant anything.
 */
let locateTimer = 0;
ACTIONS["gate.locate"] = (el) => {
  const row = document.getElementById(el.dataset.target || "");
  if (!row) return;
  row.scrollIntoView({ block: "center", behavior: "smooth" });
  row.classList.add("is-located");
  clearTimeout(locateTimer);
  locateTimer = setTimeout(() => row.classList.remove("is-located"), 2600);
};

ACTIONS["issue.select"] = (el) => {
  set({ openFingerprint: el.dataset.fingerprint });
  set({ selected: new Set([el.dataset.fingerprint]) });
  // Through ensureSource, not fetchSource: selecting a row while the operator
  // is reading "why" or "履历" must not read their source behind the pane that
  // is actually on screen. Switching to "where" fetches it then.
  ensureSource();
};

/**
 * issue.close un-picks the row, which is what empties the drawer.
 *
 * It is a separate action from dialog.close even though both end up closing
 * something: the dialog is a DOM element with its own open state, while the
 * drawer is drawn from openFingerprint. Routing the drawer's × through the
 * dialog's action would work only as long as closeDialog() happens to also
 * clear the fingerprint — and it does not.
 */
ACTIONS["issue.close"] = () => set({ openFingerprint: null });

/**
 * toast reports a write that has already happened, with its undo.
 *
 * The status writes are the cockpit's only destructive actions and they used
 * to be silent: a PATCH that succeeded repainted the card and nothing said
 * "已确认" had just been applied to an issue, or how to take it back. The undo
 * is another PATCH to the previous value rather than a local revert, because
 * the verdict is read-time state (StatusOverride, internal/store) — there is no
 * stored row to roll back, and a write-back of the old value is what actually
 * un-applies it.
 *
 * One toast per decision, never one per issue: a bulk change to 40 findings
 * needs one message with the count and one undo, not 40 overlapping popups.
 */
let toastTimer = 0;

function toast(message, undo) {
  const host = document.getElementById("toast");
  if (!host) return;
  const label = message;
  const text = document.getElementById("toastText");
  const button = document.getElementById("toastUndo");
  if (text) text.textContent = label;
  if (button) {
    button.hidden = !undo;
    button.onclick = undo ? () => { host.hidden = true; undo(); } : null;
  }
  host.hidden = false;
  // A toast that never leaves is a second status line competing with the banner,
  // so it clears itself. The timer is kept so a rapid second write replaces the
  // first one's countdown rather than queueing behind it.
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { host.hidden = true; }, 6000);
}

/**
 * issue.status records one triage decision. The decision is written to the
 * server before the copy in memory changes, so a rejected write (no reason,
 * wrong origin) leaves the card showing the verdict that is actually in force.
 */
ACTIONS["issue.status"] = async (el) => {
  // The value comes from a <select>, so it is el.value rather than a data
  // attribute: reading a dataset key the element never carried sent an empty
  // verdict to the server, which rejected it — and the card snapped back.
  const decision = { fingerprint: el.dataset.fingerprint, status: el.value };
  const reason = (el.dataset.reason || "").trim();
  if (reason) decision.reason = reason;
  try {
    await patchIssue(decision);
    set({ error: "" });
    // The previous verdict is what the card showed before this write, so it is
    // read from the list rather than from the select (whose value is already
    // the new one) — undo has to know where to go back to.
    const before = state.issues.issues.find((row) => row.fingerprint === decision.fingerprint)?.status;
    toast(`已将 #${decision.fingerprint} 标记为「${statusOf(decision.status).label}」`, () => {
      patchIssue({ fingerprint: decision.fingerprint, status: before || "open", reason: "undo" })
        .then(rerender)
        .catch((error) => set({ error: `撤销失败：${error.message}` }));
    });
    await rerender();
  } catch (error) {
    set({ error: `状态写入失败：${error.message}` });
  }
};

/** issue.toggle adds or removes one card from the bulk-change set. */
ACTIONS["issue.toggle"] = (el) => toggleSelected(el.dataset.fingerprint);

/**
 * issue.tab switches the detail pane's tab. Only the "where" tab needs the
 * server, so the others are painted from the row already in memory — an extra
 * round trip per tab click would be latency for no new information.
 */
ACTIONS["issue.tab"] = (el) => {
  set({ issueTab: el.dataset.tab });
  ensureSource();
};

/** bulk.status applies one verdict to every selected issue. */
ACTIONS["bulk.status"] = async (el) => {
  const status = el.dataset.status;
  const fingerprints = [...state.selected];
  if (!fingerprints.length) return;
  // Each selection's previous verdict, captured before the writes: one undo has
  // to restore the whole selection, and a value reused across findings would
  // silently re-write every one of them to the same verdict.
  const before = new Map(
    state.issues.issues
      .filter((row) => fingerprints.includes(row.fingerprint))
      .map((row) => [row.fingerprint, row.status]),
  );
  try {
    for (const fingerprint of fingerprints) {
      await patchIssue({ fingerprint, status, reason: "bulk change" });
    }
    set({ selected: new Set(), error: "" });
    // One toast for the whole batch, with a count rather than a list: the tray
    // already showed which findings were selected, and 40 findings is one
    // decision, not 40.
    toast(`已将 ${fingerprints.length} 条标记为「${statusOf(status).label}」`, () => {
      Promise.all(fingerprints.map((fingerprint) => patchIssue({
        fingerprint,
        status: before.get(fingerprint) || "open",
        reason: "undo",
      })))
        .then(rerender)
        .catch((error) => set({ error: `撤销失败：${error.message}` }));
    });
    await rerender();
  } catch (error) {
    set({ error: `批量流转失败：${error.message}` });
  }
};

ACTIONS["bulk.clear"] = () => clearSelected();

ACTIONS["facet.toggle"] = (el) => {
  const { kind, value } = el.dataset;
  const patch = { page: 1 };
  patch[kind] = state[kind] === value ? "" : value;
  set(patch);
};

ACTIONS["sort.set"] = (el) => set({ sort: el.value, page: 1 });

/**
 * converge.set narrows or widens the issues window from the chip strip.
 *
 * The dataset carries whatever that chip owns — a limit, or a status — and the
 * row that is already in force is a no-op rather than a re-read: clicking
 * "只看最新 1 次运行" while that is the state should not spend a request.
 */
ACTIONS["converge.set"] = (el) => {
  const patch = { page: 1 };
  if (el.dataset.limit !== undefined) patch.limit = Number(el.dataset.limit) || 1;
  if (el.dataset.status !== undefined) patch.status = el.dataset.status;
  if (!Object.entries(patch).some(([key, value]) => state[key] !== value)) return;
  set(patch);
};

/**
 * converge.clear returns to the widened baseline: 60 runs, every status, and no
 * facet filter. It used to reset only limit and status, so a rule, severity,
 * file or search filter survived a button labelled "clear the filters".
 */
ACTIONS["converge.clear"] = () => set({
  limit: WIDEN_LIMIT, status: "", severity: "", rule: "", file: "", query: "", page: 1,
});

/**
 * rules.sort reorders the Rules table. A second click on the same column
 * reverses it, so "which of these is not from this file" is one click on 出处
 * rather than a read of 138 rows. Clicking a different column replaces the
 * order rather than adding a second one: two sort keys is a query, and the
 * operator did not ask for one.
 */
ACTIONS["rules.sort"] = (el) => {
  const key = el.dataset.key;
  set({ rulesSort: { key, dir: state.rulesSort.key === key && state.rulesSort.dir === "asc" ? "desc" : "asc" } });
};

ACTIONS["page.set"] = (el) => set({ page: Math.max(1, Number(el.dataset.page) || 1) });

/**
 * issues.query is the issue search box. It hits the server, unlike the call
 * trace's client-side filter: the issue list is already paginated, so searching
 * the visible page would answer a different question than the operator asked.
 */
ACTIONS["issues.query"] = (el) => {
  // Debounced: this box hits the server, and one request per keystroke on a
  // paginated list is typing lag rather than diligence.
  set({ query: el.value, page: 1 }, { delay: 250 });
};

ACTIONS["nav.view"] = (el) => set({ view: el.dataset.view, page: 1 });

/**
 * scope.open enters one project (or the unassigned records) and lands on its
 * overview. The picker has no meaningful scope, so entering one is what makes
 * the workspace tabs appear at all.
 */
ACTIONS["scope.open"] = (el) => {
  const scope = el.dataset.scope;
  set({ scope, view: scope === "" ? "calls" : "overview", page: 1 });
};

/** scope.clear returns to the project picker. */
ACTIONS["scope.clear"] = () => set({ scope: null, view: "projects" });

/**
 * project.runs opens the run history of the project a card belongs to.
 *
 * It is its own action rather than a second scope.open on the card: the cards
 * on the picker are counted by how many scope.open controls they carry, so a
 * second one inside a card would make the count disagree with the number of
 * projects on screen.
 */
ACTIONS["project.runs"] = (el) => set({ scope: el.dataset.scope, view: "runs", page: 1 });

/**
 * project.help copies the command that would produce data for this project.
 *
 * A card with no calls and no runs has nothing to open, so the useful next step
 * is the command itself. The clipboard is best-effort: an http:// origin, or a
 * browser that refuses the permission, has to still get the command — so the
 * failure path puts the same text in a toast rather than silently doing nothing.
 */
ACTIONS["project.help"] = (el) => {
  const command = `metricgate check ${el.dataset.root || "."}`;
  const done = () => toast(`已复制命令：${command}`);
  const fallback = () => toast(`剪贴板不可用，请手动执行：${command}`);
  if (!navigator.clipboard?.writeText) return fallback();
  navigator.clipboard.writeText(command).then(done).catch(fallback);
};

/**
 * calls.query is the search box. It re-renders from the copy already in
 * memory — going back to the server for every keystroke would make typing lag
 * on a repository with thousands of calls, and the data did not change.
 */
ACTIONS["calls.query"] = (el) => {
  const query = el.value;
  set({ callFilters: { ...state.callFilters, query } });
};

ACTIONS["calls.filter"] = (el) => {
  const key = el.dataset.filter;
  set({ callFilters: { ...state.callFilters, [key]: el.value } });
};

ACTIONS["call.detail"] = (el) => {
  const call = callsInScope(state).find(
    (c) => String(field(c, "time")) === el.dataset.time && String(field(c, "tool")) === el.dataset.tool,
  );
  if (call) showCall(call);
};

/** run.select keeps the archive set in state so a re-render cannot lose it. */
ACTIONS["run.select"] = (el) => {
  const next = new Set(state.runSelection);
  if (next.has(el.dataset.run)) next.delete(el.dataset.run);
  else next.add(el.dataset.run);
  set({ runSelection: next });
};

ACTIONS["run.findings"] = (el) => {
  const { findings } = runsInScope(state);
  showFindings(Number(el.dataset.run), findings);
};

ACTIONS["runs.archive"] = () => changeArchiveState(true);

ACTIONS["runs.restore"] = () => changeArchiveState(false);

ACTIONS["history.mode"] = (el) => {
  const mode = el.dataset.mode;
  writePref(PREFS.historyMode, mode);
  set({ historyMode: mode, runSelection: new Set() });
};

ACTIONS["metric.choose"] = (el) => {
  writePref(PREFS.metric, el.value);
  set({ metricChoice: el.value });
};

ACTIONS["policy.save"] = () => savePolicy();

ACTIONS["policy.reload"] = () => fetchPolicy();

/**
 * policy.edit stores the draft and updates the dirty hint in place.
 *
 * The hint is written straight to its span rather than through a repaint for the
 * reason record() documents. That is one textContent assignment, not a second
 * markup sink: the contract test counts innerHTML writes, and the escaping
 * template still owns every element the page builds.
 */
ACTIONS["policy.edit"] = (el) => {
  record({ policyDraft: el.value });
  const hint = document.getElementById("policyDirty");
  if (!hint) return;
  const dirty = el.value !== (state.policy?.content ?? "");
  hint.textContent = "有未保存的改动";
  hint.hidden = !dirty;
};

ACTIONS["policy.jump"] = (el) => jumpToPolicyLine(Number(el.dataset.line) || 1);

/**
 * With S7 being pure render functions, the detail dialog is the one place DOM
 * work survives: it is a modal, and a modal is not a view in the hash.
 * showCall/showFindings build its body from the same components the tables
 * use, so the dialog cannot drift from the list it was opened from.
 */
const detailDialog = () => document.getElementById("detailDialog");

/**
 * dialogBody paints the dialog and opens it. It writes through the same
 * writeInto sink the views use, so the escaping template stays the single
 * path into the document — a second raw string sink is where a hand-built
 * concat creeps in and stops escaping.
 */
function dialogBody(titleText, markup) {
  const dialog = detailDialog();
  const title = document.getElementById("detailTitle");
  const body = document.getElementById("detailBody");
  if (!dialog || !title || !body) return;
  title.textContent = titleText;
  writeInto(body, markup);
  dialog.showModal();
}

export function showCall(call) {
  const summary = [
    ["时间", formatTime(field(call, "time"))],
    ["阶段", phaseLabel(field(call, "phase"))],
    ["结果", field(call, "verdict") || (field(call, "error") ? "ERROR" : "已记录")],
    ["定位", field(call, "target") || "—"],
  ];
  const extra = [
    ["耗时", `${Number(field(call, "ms")) || 0} ms`],
    ["发现", `${field(call, "findings") || 0}（${field(call, "errors") || 0} errors · ${field(call, "warnings") || 0} warnings）`],
    ["会话", field(call, "session") || "—"],
    ["项目归属", field(call, "projectRoot") || "未归属"],
  ];
  if (field(call, "traceId")) extra.push(["trace", field(call, "traceId")]);
  if (field(call, "error")) extra.push(["错误", field(call, "error")]);
  dialogBody(field(call, "tool") || "调用详情", html`
    <dl class="detail-grid">${list(summary, ([k, v]) => detailCell(k, v))}</dl>
    <details class="extra-details"><summary>更多信息</summary>
      <dl class="detail-grid">${list(extra, ([k, v]) => detailCell(k, v))}</dl>
    </details>
    <p class="privacy-note">此日志记录工具调用元数据，不包含传入的源码。</p>`);
}

export function showFindings(runId, findings) {
  const items = findings.filter((entry) => Number(field(entry, "runId")) === runId);
  dialogBody(`运行 #${runId} · 发现列表`, items.length
    ? html`<div class="finding-list">${list(items, ({ item }) => html`
        <details class="finding"><summary>${badge(item.severity)} <strong>${item.ruleId}</strong> <span>${item.file}:${item.startLine}</span></summary>
        <p>${item.message}</p><small>${item.file}:${item.startLine}:${item.startColumn}</small></details>`)}</div>`
    : html`<p class="empty">这次运行没有保存发现。</p>`);
}

/** detailCell is one label/value pair of the dialog grid. */
const detailCell = (label, value) => html`<div><dt>${label}</dt><dd>${value}</dd></div>`;

export function closeDialog() {
  detailDialog()?.close();
}

/**
 * changeArchiveState archives or restores the selected runs. It goes through
 * the same endpoint the CLI writes, so an archive here cannot disagree with
 * what the next check reads.
 */
async function changeArchiveState(archived) {
  const scope = state.scope === "" ? "" : state.scope;
  if (scope === null || !state.runSelection.size) return;
  const runIds = [...state.runSelection].map(Number).filter(Number.isSafeInteger);
  if (!runIds.length) return;
  const action = archived ? "归档" : "恢复";
  if (!window.confirm(`确定${action}所选的 ${runIds.length} 条运行记录？数据不会删除。`)) return;
  try {
    const response = await fetch("/api/runs/archive", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify({ runIds, projectRoot: scope, archived }),
    });
    if (!response.ok) throw new Error(`操作失败 (${response.status})`);
    set({ runSelection: new Set(), error: "" });
    await fetchDashboard();
    rerender();
  } catch (error) {
    set({ error: `${action}失败：${error.message}` });
  }
}

/**
 * savePolicy writes the edited document back through the policy endpoint. The
 * server validates the YAML before touching disk, so a parse failure arrives
 * as an error message rather than a half-written file.
 */
async function savePolicy() {
  const area = document.getElementById("policyContent");
  if (!area || !state.policy?.found) return;
  try {
    // The scope rides on the write as well as the read. Without it the save
    // would be resolved against the server's own directory while the editor
    // showed another project's file, so an operator editing project A's policy
    // would silently rewrite the policy of the repository the server started in.
    const response = await fetch(`/api/policy?scope=${encodeURIComponent(scopeParam(state))}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify({ path: state.policy.path, content: area.value }),
    });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(payload.error || payload.message || `保存失败 (${response.status})`);
    set({ error: "", editedAt: new Date().toISOString() });
    // policyDraft is dropped on a successful save, so the editor repaints from
    // the server's copy rather than from the text the operator typed: the text
    // they see has to be the text that was written, and a stale draft keeps a
    // "有未保存的改动" hint on a document that is already on disk.
    await fetchPolicy();
    set({ policyDraft: null });
  } catch (error) {
    set({ error: `保存失败：${error.message}` });
  }
}

/**
 * jumpToPolicyLine moves the caret to a section and scrolls it into view, so a
 * section chip is real navigation rather than decoration. The scroll ratio is
 * computed from the line index because a textarea has no per-line geometry.
 */
function jumpToPolicyLine(line) {
  const area = document.getElementById("policyContent");
  if (!area) return;
  const lines = area.value.split("\n");
  const index = Math.min(Math.max(1, line), lines.length) - 1;
  let offset = 0;
  for (let i = 0; i < index; i++) offset += lines[i].length + 1;
  area.focus();
  area.setSelectionRange(offset, offset + (lines[index] || "").length);
  const ratio = lines.length > 1 ? index / (lines.length - 1) : 0;
  area.scrollTop = ratio * (area.scrollHeight - area.clientHeight);
}

/**
 * Detail dialog close button. The click handler below also closes on a
 * backdrop click, which is what a modal is expected to do.
 */
ACTIONS["dialog.close"] = () => closeDialog();

// refresh re-reads the spec first: a stale binary against a rebuilt UI is the
// one mismatch worth catching before painting anything.
ACTIONS["refresh"] = async () => {
  const served = await api("/api/spec");
  if (served.apiVersion !== SPEC.apiVersion) {
    set({ error: "界面与服务端版本不一致，请重新构建 metricgate。" });
    return;
  }
  await rerender();
};

/**
 * typingInAField suppresses the keyboard shortcuts while the operator is typing.
 * Without it, "f" inside the search box would start a state transition, which
 * is the bug every keyboard-driven triage UI eventually ships.
 */
const typingInAField = (event) =>
  event.target instanceof HTMLElement &&
  event.target.matches("input, textarea, select, [contenteditable]");

/**
 * controlEventMatches gives each control exactly one event that carries a
 * usable value.
 *
 * Delegating everything through click is the mistake this replaces: a select
 * fires click while it still holds the *old* value, so the sort dropdown and the
 * per-issue verdict dropdown would act on what the operator was leaving rather
 * than what they chose, and a search box fires click before anything is typed.
 * Every one of those controls looked wired because the handler exists.
 */
export const controlEventMatches = (el, type) => {
  switch (el.tagName) {
    case "SELECT": return type === "change";
    case "TEXTAREA": return type === "input";
    case "INPUT":
      return el.type === "checkbox" || el.type === "radio" ? type === "change" : type === "input";
    default: return type === "click";
  }
};

export function boot() {
  const root = document.getElementById("app-shell");
  if (!root) throw new Error("cockpit: #app-shell is missing from index.html");
  // delegate(type) is one dispatcher per event type over the same ACTIONS
  // table, so a control's wiring is still one data-action attribute and the
  // table stays the only place behaviour is registered.
  const delegate = (type) => (event) => {
    const el = event.target.closest("[data-action]");
    if (!el || !controlEventMatches(el, type)) return;
    const handler = ACTIONS[el.dataset.action];
    if (!handler) return;
    handler(el, event);
  };
  document.addEventListener("click", delegate("click"));
  document.addEventListener("change", delegate("change"));
  document.addEventListener("input", delegate("input"));
  document.addEventListener("keydown", (event) => {
    if (typingInAField(event)) return;
    const handler = KEYMAP[event.key];
    if (!handler) return;
    handler(event);
  });
  window.addEventListener("hashchange", () => {
    // Comparing against the hash our own state would produce is what tells a
    // self-write apart from a real navigation: set() has already applied the
    // values, so re-applying them would repaint and re-fetch a view that is
    // showing exactly this state. Anything else — back, forward, a pasted URL —
    // differs and has to be read.
    if (window.location.hash === `#/${encodeRoute(state)}`) return;
    routeChange(decodeRoute(window.location.hash));
    // A restored link that names the "where" tab owes the same read a click
    // does; a restored link to any other tab owes nothing. ensureSource decides
    // which, so the listener stays a two-line adapter.
    ensureSource();
  });
  subscribe(render);
  // Preferences are read before the URL, so a pasted link outranks what the
  // browser remembers while a bare reload keeps it. writePref had been the only
  // half that existed: the two choices were stored on every click and never
  // read back, so a refresh silently snapped the archived view and the metric
  // picker back to their defaults.
  Object.assign(state, {
    historyMode: readPref(PREFS.historyMode, state.historyMode),
    metricChoice: readPref(PREFS.metric, state.metricChoice),
  });
  Object.assign(state, decodeRoute(window.location.hash));
  // Boot restores through Object.assign rather than the hashchange listener, so
  // it needs the same call: a pasted link to an open issue's code window is the
  // first-load case, and it is the one a reviewer actually pastes.
  ensureSource();
  rerender();
}

/**
 * writeInto is the single place an HTML string reaches the document.
 *
 * Everything upstream produces an html`` fragment whose values are already
 * escaped; assigning .value (rather than the fragment itself, which would
 * serialise as an object) is the one operation that turns it into DOM. The
 * Go contract test counts these assignments, and the reason is escape
 * coverage: each additional raw sink is one more place a hand-written concat
 * could stop escaping.
 */
function writeInto(target, markup) {
  target.innerHTML = markup && markup.__raw ? markup.value : String(markup || "");
}

/**
 * render paints the active view. It is pure read + write: it never fetches.
 * Loading is driven by rerender so a state change and a data refresh cannot
 * race to paint different views.
 */
export function render(s) {
  const view = VIEWS[s.view] || VIEWS.projects;
  document.title = `${view.title} · metricGate Audit Cockpit`;
  const title = document.getElementById("pageTitle");
  const subtitle = document.getElementById("pageSubtitle");
  const stage = document.getElementById("viewStage");
  if (title) title.textContent = view.title;
  if (subtitle) subtitle.textContent = view.subtitle || "";
  if (!stage) return;
  paintNav(s);
  const markup = view.render ? view.render(s) : "";
  // value is the escaped string from the html template; assigning it as text
  // would double-escape, so the raw fragment is used. Caret and scroll offset
  // are saved and restored around the write: a search box that loses its caret
  // on every keystroke is the defect every string-template UI ships.
  const active = document.activeElement;
  const activeId = active?.id || "";
  const caret = active?.selectionStart ?? null;
  const scroll = stage.scrollTop;
  writeInto(stage, markup);
  stage.scrollTop = scroll;
  const restored = activeId ? document.getElementById(activeId) : null;
  restored?.focus?.();
  if (caret !== null) restored?.setSelectionRange?.(caret, caret);
}

/**
 * paintNav syncs everything outside the stage: the rail, the header, the banner.
 *
 * It is split by the half of the shell it writes, because one function that
 * touched nine elements was the largest in the file and read as a list of
 * unrelated `if (el)` blocks.
 */
function paintNav(s) {
  paintRail(s);
  paintHeader(s);
}

/** paintRail marks the active entry, folding workspace views into the project. */
function paintRail(s) {
  // The rail's own views, read from the buttons rather than hardcoded: a view
  // the rail already lists must never also fold into the project entry, or two
  // items light up for one page. That double-highlight is what a restated list
  // of "workspace views" eventually gets wrong, so the list is not restated.
  // The rail is split into two groups in the markup, so it is collected from
  // every nav-item rather than from one nav's id.
  const buttons = [...document.querySelectorAll(".nav-item[data-view]")];
  const railViews = new Set(buttons.map((button) => button.dataset.view));
  buttons.forEach((button) => {
    // A view the rail does not list still has to light up something: it is
    // reachable from inside a project, and leaving the rail dark made people
    // think the page had broken. The project entry is its home.
    const folded = button.dataset.view === "projects"
      && WORKSPACE_VIEWS.has(s.view) && !railViews.has(s.view);
    const active = button.dataset.view === s.view || folded;
    button.classList.toggle("active", active);
  });
}

/** paintHeader writes the bar above the content: crumb, timestamp, banner. */
function paintHeader(s) {
  const updated = document.getElementById("updatedAt");
  if (updated) {
    // The loading state is what the label reads first. Every fetch in this
    // cockpit already sets it, and nothing ever showed it, so a slow read of a
    // large store looked identical to a page that had stopped responding.
    updated.textContent = s.loading ? "读取本地数据…"
      : s.editedAt ? `配置已保存 ${formatTime(s.editedAt)}`
      : state.dashboard?.generatedAt ? `更新于 ${formatTime(state.dashboard.generatedAt)}`
      : "尚未同步";
  }
  const logPath = document.getElementById("logPath");
  if (logPath) logPath.textContent = s.logPath || "日志未启用";
  // The breadcrumb is header chrome, so it is painted with the rest of the
  // header. Outside a scope it is hidden rather than emptied: an element that
  // renders nothing in the header still reserves its line, and the title on the
  // picker sits lower than the title inside a project.
  const crumb = document.getElementById("shellCrumb");
  if (crumb) {
    writeInto(crumb, s.scope === null ? "" : breadcrumbs(s));
    crumb.hidden = s.scope === null;
  }
  // The error banner is the only place a failed request becomes visible, so it
  // is written here rather than per view. Without it the cockpit records an
  // error in state — a refused triage write, a save the policy rejected, a
  // dashboard that failed to read — and shows a page that simply did not change,
  // which reads as the click being ignored rather than as the failure it is.
  const banner = document.getElementById("errorBanner");
  if (banner) {
    banner.textContent = s.error || "";
    banner.hidden = !s.error;
  }
  // The refresh button is disabled while a read is in flight. The stylesheet
  // already styles :disabled with a wait cursor, so the affordance was designed
  // and never wired: a second click during a slow read queued a second load of
  // the same aggregate behind the first.
  const refresh = document.getElementById("refreshBtn");
  if (refresh) refresh.disabled = Boolean(s.loading);
}

/** rerender refreshes the view's data first, so the paint shows fresh state. */
export async function rerender() {
  const view = VIEWS[state.view];
  if (view && view.load) await view.load(filters(state));
  render(state);
}

/** filters is the filter set shared by the router and the issues view. */
export function filters(s) {
  return {
    severity: s.severity || "", status: s.status || "", rule: s.rule || "",
    file: s.file || "", query: s.query || "", sort: s.sort || "severity",
    page: s.page || 1,
    // limit is spread last and defaults to 1 rather than being folded into the
    // `s.limit || 1` idiom: it has to survive a `set({ status: "" })` patch,
    // which does not touch it, so the default lives here and in ROUTE_KEYS.
    limit: s.limit || 1,
  };
}

/** KEYMAP holds the Issues view shortcuts. */
const KEYMAP = {
  ArrowDown: () => moveSelection(1),
  ArrowUp: () => moveSelection(-1),
};

const moveSelection = (delta) => {
  const rows = state.issues.issues;
  if (!rows.length) return;
  const current = rows.findIndex((r) => r.fingerprint === state.openFingerprint);
  const next = Math.min(rows.length - 1, Math.max(0, current + delta));
  const row = rows[next];
  if (row) ACTIONS["issue.select"]({ dataset: { fingerprint: row.fingerprint } });
};

const isBrowser = typeof window !== "undefined" && document.getElementById("app-shell");

if (isBrowser) boot();
