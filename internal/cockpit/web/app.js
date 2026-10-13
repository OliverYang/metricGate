const root = document.documentElement;
const byId = (id) => document.getElementById(id);
let dashboard = null;
let activeProject = null;
let currentView = "projects";
let showUnassigned = false;

const escapeHTML = (value) => String(value ?? "").replace(/[&<>"']/g, (char) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[char]));
const field = (value, key) => value?.[key] ?? value?.[key.toUpperCase()] ?? value?.[key[0].toUpperCase() + key.slice(1)];
const formatTime = (value) => {
  if (!value || value.startsWith("0001-")) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false });
};
const percent = (value, denominator) => denominator ? `${Math.round(value * 100)}%` : "—";
const badge = (value) => {
  const status = String(value || "unknown").toLowerCase();
  const klass = status.includes("pass") ? "pass" : status.includes("error") ? "error" : "flagged";
  return `<span class="badge ${klass}">${escapeHTML(value || "未知")}</span>`;
};

async function loadDashboard() {
  const refresh = byId("refreshBtn");
  refresh.disabled = true;
  refresh.textContent = "读取中…";
  try {
    const response = await fetch("/api/dashboard", { headers: { Accept: "application/json" } });
    if (!response.ok) throw new Error(`读取失败 (${response.status})`);
    dashboard = await response.json();
    renderDashboard(dashboard);
    byId("errorBanner").hidden = true;
  } catch (error) {
    byId("errorBanner").textContent = `${error.message}。请确认本地服务仍在运行后重试。`;
    byId("errorBanner").hidden = false;
  } finally {
    refresh.disabled = false;
    refresh.textContent = "↻ 刷新数据";
  }
}

function renderDashboard(data) {
  const projects = data.projects || [];
  const priorRoot = activeProject;
  activeProject = projects.some((project) => project.root === priorRoot) ? priorRoot : null;
  byId("logPath").textContent = data.logPath || "日志未启用";
  byId("updatedAt").textContent = `更新于 ${formatTime(data.generatedAt)}`;
  byId("projectCount").textContent = `${projects.length} 个可验证仓库`;
  byId("unassignedCount").textContent = data.unassignedCalls || 0;
  byId("projectList").innerHTML = projects.length ? projects.map((project) => `
    <button class="project-card" data-project="${escapeHTML(project.root)}"><span class="project-icon">⌘</span><span class="project-copy"><strong>${escapeHTML(project.name)}</strong><small>${escapeHTML(project.root)}</small></span><span class="project-stats"><b>${Number(project.calls) || 0}</b><small>次调用 · ${Number(project.flagged) || 0} 次未通过</small></span><span class="project-latest">最近活动<br>${escapeHTML(formatTime(project.latestActivity))}</span><span class="project-arrow">↗</span></button>`).join("") : '<div class="empty-state"><span class="empty-icon">⌘</span><strong>暂未识别到项目</strong><small>仅当调用定位包含可验证的绝对 Git 仓库路径时，才会列为项目。</small></div>';
  byId("projectList").querySelectorAll("[data-project]").forEach((button) => button.addEventListener("click", () => openProject(button.dataset.project)));
  renderAccepted(data.accepted || []);
  renderCalls(data.calls || []);
  renderRuns(data.runs || [], data.findings || []);
  if (activeProject) {
    currentView = "overview";
    showUnassigned = false;
  } else {
    currentView = "projects";
  }
  renderView();
}

function projectCalls() {
  const calls = dashboard?.calls || [];
  if (showUnassigned) return calls.filter((call) => !call.projectRoot);
  if (activeProject) return calls.filter((call) => call.projectRoot === activeProject);
  return calls;
}

// renderView switches the visible panes for the current view state and fills
// the workspace panes when one is active. The per-concern rendering lives in
// the helpers below so this stays a dispatcher.
function renderView() {
  const project = (dashboard?.projects || []).find((item) => item.root === activeProject);
  const inWorkspace = Boolean(project) || showUnassigned;
  updateViewVisibility(project, inWorkspace);
  updateBreadcrumbs(project);
  updateNavSelection(project);
  if (currentView === "policy") {
    if (!policy) loadPolicy();
    return;
  }
  if (inWorkspace) renderWorkspace(project);
}

// updateViewVisibility toggles the panes that match the current view state.
function updateViewVisibility(project, inWorkspace) {
  byId("projectOverview").hidden = inWorkspace;
  byId("workspaceNav").hidden = !inWorkspace;
  byId("workspaceNav").querySelector('[data-view="overview"]').hidden = showUnassigned;
  byId("workspaceNav").querySelector('[data-view="runs"]').textContent = showUnassigned ? "未归属运行" : "质量运行";
  byId("overviewPane").hidden = !project || currentView !== "overview";
  byId("callsPane").hidden = !inWorkspace || currentView !== "calls";
  byId("runsPane").hidden = !inWorkspace || currentView !== "runs";
  byId("runsNotice").hidden = !inWorkspace || currentView !== "runs";
  byId("policyPane").hidden = currentView !== "policy";
}

// updateBreadcrumbs renders the breadcrumb trail, page title and subtitle.
function updateBreadcrumbs(project) {
  byId("breadcrumbs").innerHTML = breadcrumbTrail(project);
  byId("pageTitle").textContent = pageTitleText(project);
  byId("pageSubtitle").textContent = pageSubtitleText(project);
}

// breadcrumbTrail names the current scope: the config view stands alone, a
// project workspace nests its view, and 未归属 has no project to name.
function breadcrumbTrail(project) {
  if (currentView === "policy") return '<button data-view="projects">项目总览</button><span>›</span><strong>扫描配置</strong>';
  if (project) return `<button data-view="projects">项目总览</button><span>›</span><strong>${escapeHTML(project.name)}</strong><span>›</span><span>${viewLabel()}</span>`;
  if (showUnassigned) return '<button data-view="projects">项目总览</button><span>›</span><strong>未归属记录</strong>';
  return '<strong>项目总览</strong>';
}

// pageTitleText is the h1 for the current scope.
function pageTitleText(project) {
  if (currentView === "policy") return "扫描配置";
  if (project) return project.name;
  return showUnassigned ? "未归属记录" : "项目总览";
}

// pageSubtitleText explains the current scope in one line, because a bare
// path or a bare project name tells the reader nothing about why the numbers
// below it are what they are.
function pageSubtitleText(project) {
  if (currentView === "policy") return "配置发现链、当前生效的文档与每个键的出处；保存后立即参与后续检查。";
  if (project) return project.root;
  if (showUnassigned) return "这些调用缺少可验证的 Git 仓库路径，未被混入任何项目。";
  return "从本地调用记录识别仓库；无法验证归属的数据会单独保留。";
}

// viewLabel names the current workspace view for the breadcrumb tail.
function viewLabel() {
  if (currentView === "overview") return "概览";
  if (currentView === "runs") return "质量运行";
  if (currentView === "policy") return "扫描配置";
  return "工具活动";
}

// updateNavSelection syncs the selected state of the sidebar and workspace nav.
function updateNavSelection(project) {
  byId("unassignedButton").classList.toggle("selected", showUnassigned);
  document.querySelectorAll(".nav-item[data-view], .workspace-nav [data-view]").forEach((button) => button.classList.toggle("active", button.dataset.view === currentView && (button.closest(".workspace-nav") || !project)));
}

// renderWorkspace fills the workspace panes for the active project or the
// unassigned scope: summary cards, flow, trend, calls and the runs history.
function renderWorkspace(project) {
  const calls = projectCalls();
  const summary = summarizeCalls(calls);
  renderSummaryCards(project, summary);
  renderFlow(summary.byPhase);
  renderTrend(showUnassigned ? dashboard.trend?.series || [] : project?.trend?.series || []);
  renderMetricTrend(dashboard.metricTrend);
  renderCalls(calls);
  if (currentView === "runs") renderHistory(project);
}

// renderSummaryCards fills the workspace overview cards.
function renderSummaryCards(project, summary) {
  byId("logPath").textContent = dashboard.logPath || "日志未启用";
  byId("totalCalls").textContent = summary.calls;
  byId("rangeText").textContent = summary.calls ? `${summary.sessions} 个 MCP 会话 · ${formatTime(summary.first)} — ${formatTime(summary.last)}` : "尚无可归属的调用记录";
  byId("interceptRate").textContent = percent(summary.verifyFlagged, summary.verifyCalls);
  byId("interceptNote").textContent = summary.verifyCalls ? `${summary.verifyFlagged} / ${summary.verifyCalls} 次验证发现问题` : "暂无验证调用";
  byId("convergeRate").textContent = "—";
  byId("convergeNote").textContent = "收敛率按全局会话统计，无法安全拆分到项目";
  byId("toolErrors").textContent = summary.toolErrors;
  byId("skippedText").textContent = dashboard.summary?.skipped ? `${dashboard.summary.skipped} 条损坏日志已跳过` : "调用日志仅保存在本机";
  byId("runCount").textContent = showUnassigned ? `${dashboard.trend?.runs || (dashboard.runs || []).length} 次未归属运行` : `${project?.trend?.runs || 0} 次质量运行`;
}

// renderHistory renders the runs pane for the current history mode and scope.
function renderHistory(project) {
  const scope = showUnassigned ? dashboard : project;
  const [runs, findings] = historyPair(scope);
  updateRunsNotice(project);
  updateRunsScopeNote(project);
  byId("historyCount").textContent = historyCountText(project);
  updateArchiveControls();
  renderRuns(runs, findings);
}

// historyPair returns the runs+findings pair matching the current scope and
// history mode.
function historyPair(scope) {
  const active = historyMode === "active";
  return active ? [activeRunsOf(scope), activeFindingsOf(scope)] : [archivedRunsOf(scope), archivedFindingsOf(scope)];
}

// updateRunsNotice explains what the current runs view does and does not own.
function updateRunsNotice(project) {
  if (project) byId("runsNotice").textContent = "只显示带项目身份的新运行；早期运行没有记录项目路径，仍留在「未归属记录」里。";
  else if (showUnassigned) byId("runsNotice").textContent = "这些运行缺少项目路径，无法可靠地归入某个项目，因此单独列在这里。";
}

// updateRunsScopeNote names what the current runs view actually covers. The
// old hardcoded "历史运行尚未记录项目身份" read as "there is nothing here"
// even when the list below it was full, which is the misleading part.
function updateRunsScopeNote(project) {
  const note = byId("runsScopeNote");
  if (!note) return;
  note.textContent = project
    ? `${project.name} · 带项目身份的运行`
    : showUnassigned ? "未归属运行（无项目路径）" : "";
}

// historyCountText describes the size of the active/archived history view.
function historyCountText(project) {
  const activeCount = showUnassigned ? dashboard?.runs?.length || 0 : project?.runs?.length || 0;
  const archivedCount = showUnassigned ? dashboard?.archivedRuns?.length || 0 : project?.archivedRuns?.length || 0;
  if (historyMode === "active") return `${activeCount} 条活跃记录`;
  return `${archivedCount} 条已归档记录${archivedCount === 100 ? " · 显示最近 100 条" : ""}`;
}

function openProject(rootPath) {
  activeProject = rootPath;
  showUnassigned = false;
  currentView = "overview";
  renderView();
}

function openView(view) {
  if (view === "projects") {
    activeProject = null;
    showUnassigned = false;
    currentView = "projects";
  } else if (view === "unassigned") {
    activeProject = null;
    showUnassigned = true;
    currentView = "calls";
  } else {
    currentView = view;
    if (view !== "runs" && view !== "calls") showUnassigned = false;
  }
  renderView();
  if (view === "runs") byId("runsPane").scrollIntoView({ behavior: "smooth", block: "start" });
  if (view === "calls" || view === "unassigned") byId("callsPane").scrollIntoView({ behavior: "smooth", block: "start" });
}

function summarizeCalls(calls) {
  const byPhase = {};
  let verifyCalls = 0;
  let verifyFlagged = 0;
  let toolErrors = 0;
  const sessions = new Set();
  const times = [];
  for (const call of calls) {
    byPhase[call.phase] = (byPhase[call.phase] || 0) + 1;
    if (call.phase === "verify") {
      verifyCalls++;
      if (call.verdict && !String(call.verdict).toLowerCase().startsWith("pass")) verifyFlagged++;
    }
    if (call.error) toolErrors++;
    if (call.session) sessions.add(call.session);
    if (call.time) times.push(call.time);
  }
  times.sort();
  return { byPhase, verifyCalls, verifyFlagged, toolErrors, sessions: sessions.size, first: times[0], last: times.at(-1) };
}

function renderFlow(phases) {
  const steps = [
    ["before", "生成前", "读取约束与查找符号"],
    ["explore", "探索", "理解仓库与影响范围"],
    ["verify", "产出后验证", "检查代码与门禁结果"],
    ["review", "复核", "整理 review bundle"],
  ];
  const max = Math.max(1, ...steps.map(([key]) => phases[key] || 0));
  byId("flow").innerHTML = steps.map(([key, title, note], index) => `
    <div class="flow-step"><div class="flow-index">0${index + 1}</div><div class="flow-copy"><strong>${title}</strong><small>${note}</small></div>
    <div class="flow-count">${phases[key] || 0}<small>次</small></div><div class="flow-track"><i style="width:${Math.round((phases[key] || 0) / max * 100)}%"></i></div></div>`).join("") || "<p class='empty'>暂无阶段数据</p>";
}

function renderTrend(series) {
  const chart = byId("trendChart");
  if (!series.length) {
    chart.innerHTML = '<div class="empty-state"><span class="empty-icon">⌁</span><strong>还没有运行趋势</strong><small>历史运行未记录项目路径，仅能作为全局趋势查看。</small></div>';
    return;
  }
  const max = Math.max(1, ...series.map((point) => Number(field(point, "errors") || 0) + Number(field(point, "warnings") || 0) + Number(field(point, "infos") || 0)));
  chart.innerHTML = series.map((point) => {
    const errors = Number(field(point, "errors") || 0);
    const warnings = Number(field(point, "warnings") || 0);
    const infos = Number(field(point, "infos") || 0);
    const total = errors + warnings + infos;
    const height = Math.max(4, total / max * 100);
    const pointTime = field(point, "startedAt");
    return `<div class="trend-column" title="${escapeHTML(formatTime(pointTime))} · errors ${errors}, warnings ${warnings}, info ${infos}"><div class="bar-stack" style="height:${height}%"><i class="bar-error" style="height:${total ? errors / total * 100 : 0}%"></i><i class="bar-warning" style="height:${total ? warnings / total * 100 : 0}%"></i><i class="bar-info" style="height:${total ? infos / total * 100 : 0}%"></i></div><small>${escapeHTML(String(pointTime || "").slice(5, 10))}</small></div>`;
  }).join("");
}

// callState classifies a call record for filtering and badge display.
function callState(call) {
  if (call.error) return "error";
  const verdict = String(call.verdict || "").toLowerCase();
  if (verdict.startsWith("pass")) return "pass";
  return call.verdict ? "flagged" : "";
}

// callMatches applies the search and filter controls to one call record.
function callMatches(call, query, phase, status) {
  const text = [call.tool, call.target, call.error, call.verdict, call.phase].join(" ").toLowerCase();
  return (!query || text.includes(query)) && (!phase || call.phase === phase) && (!status || callState(call) === status);
}

// callRow renders one table row for a call record.
function callRow(call, index) {
  const state = call.error ? "ERROR" : call.verdict || "已记录";
  return `<tr><td class="time-cell">${escapeHTML(formatTime(call.time))}</td><td><strong>${escapeHTML(call.tool)}</strong></td><td><span class="phase-tag">${escapeHTML(phaseName(call.phase))}</span></td><td class="target-cell" title="${escapeHTML(call.target)}">${escapeHTML(call.target || "—")}</td><td>${Number(call.ms) || 0}<span class="muted"> ms</span></td><td>${badge(state)}</td><td><button class="text-button" data-call="${index}">详情 ↗</button></td></tr>`;
}

function renderCalls(calls) {
  const query = byId("searchInput").value.trim().toLowerCase();
  const phase = byId("phaseFilter").value;
  const status = byId("statusFilter").value;
  const filtered = calls.filter((call) => callMatches(call, query, phase, status)).slice().reverse();
  const limited = filtered.slice(0, 100);
  byId("resultCount").textContent = `${filtered.length} 条${filtered.length > 100 ? " · 显示最近 100 条" : ""}`;
  byId("callsTable").innerHTML = limited.length ? limited.map((call) => callRow(call, calls.indexOf(call))).join("") : '<tr><td colspan="7" class="table-empty">没有匹配的调用记录</td></tr>';
  byId("callsTable").querySelectorAll("[data-call]").forEach((button) => button.addEventListener("click", () => showCall(calls[Number(button.dataset.call)])));
}

function phaseName(phase) {
  return ({ before: "生成前", explore: "探索", verify: "验证", review: "复核", other: "其他" })[phase] || phase || "其他";
}

async function changeArchiveState(archived) {
  const scope = runsScope();
  if (!dashboard || scope === null) return;
  const selected = [...byId("runsList").querySelectorAll('input[data-run-select]:checked')].map((input) => Number(input.value)).filter(Number.isSafeInteger);
  if (!selected.length) return;
  const action = archived ? "归档" : "恢复";
  if (!window.confirm(`确定${action}所选的 ${selected.length} 条运行记录？数据不会删除。`)) return;
  byId("archiveSelectedBtn").disabled = true;
  byId("restoreSelectedBtn").disabled = true;
  try {
    const response = await fetch("/api/runs/archive", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify({ runIds: selected, projectRoot: scope, archived })
    });
    if (!response.ok) throw new Error(`操作失败 (${response.status})`);
    await loadDashboard();
  } catch (error) {
    byId("errorBanner").textContent = `${action}失败：${error.message}`;
    byId("errorBanner").hidden = false;
    updateArchiveControls();
  }
}

function runsScope() {
  const project = (dashboard?.projects || []).find((item) => item.root === activeProject);
  if (!project && !showUnassigned) return null;
  return showUnassigned ? "" : activeProject;
}

// scopeData returns the named active/archived array for the current scope:
// a project workspace or the global unassigned view.
function scopeData(activeKey, archivedKey) {
  const project = (dashboard?.projects || []).find((item) => item.root === activeProject);
  const scope = showUnassigned ? dashboard : project;
  if (!scope) return [];
  return scope[historyMode === "active" ? activeKey : archivedKey] || [];
}
function activeRunsOf(scope) { return scope?.runs || []; }
function activeFindingsOf(scope) { return scope?.findings || []; }
function archivedRunsOf(scope) { return scope?.archivedRuns || []; }
function archivedFindingsOf(scope) { return scope?.archivedFindings || []; }

function updateArchiveControls() {
  const archived = historyMode === "archived";
  byId("activeRunsBtn").classList.toggle("active", !archived);
  byId("archivedRunsBtn").classList.toggle("active", archived);
  const count = byId("runsList").querySelectorAll('input[data-run-select]:checked').length;
  byId("archiveSelectedBtn").hidden = archived;
  byId("restoreSelectedBtn").hidden = !archived;
  byId("archiveSelectedBtn").disabled = archived || count === 0;
  byId("restoreSelectedBtn").disabled = !archived || count === 0;
}

let historyMode = "active";

function setHistoryMode(mode) {
  historyMode = mode;
  renderView();
}

function renderRuns(runs, findings) {
  const container = byId("runsList");
  byId("runsPane").classList.toggle("show-archived", historyMode === "archived");
  if (!runs.length) {
    container.innerHTML = '<div class="empty-state run-empty"><span class="empty-icon">◷</span><strong>暂无检查运行</strong><small>运行 metricgate check 并启用本地 store 后，结果会显示在这里。</small></div>';
    return;
  }
  const state = historyMode === "active" ? "active" : "archived";
  if (runs.some((run) => Boolean(field(run, "archived")) !== (state === "archived"))) throw new Error("run archive state mismatch");
  container.innerHTML = runs.map((run) => {
    const runId = field(run, "id");
    const errorCount = Number(field(run, "errorCount") || 0);
    const warningCount = Number(field(run, "warningCount") || field(run, "warnings") || 0);
    const state = errorCount ? "发现问题" : "通过";
    const count = findings.filter((entry) => Number(field(entry, "runId")) === Number(runId)).length;
    return `<article class="run-row"><label class="run-select"><input type="checkbox" data-run-select value="${escapeHTML(runId)}" aria-label="选择运行 #${escapeHTML(runId)}"></label><div class="run-status ${errorCount ? "has-errors" : "is-clean"}">${errorCount ? "!" : "✓"}</div><div class="run-main"><div class="run-title"><strong>运行 #${escapeHTML(runId)}</strong>${badge(state)}<span class="run-commit">${escapeHTML(field(run, "gitCommit") || "本地工作区")}</span>${field(run, "archived") ? '<span class="badge flagged">已归档</span>' : ""}</div><div class="run-meta">${escapeHTML(formatTime(field(run, "startedAt")))}<span>·</span>${escapeHTML(field(run, "files"))} 个文件<span>·</span>${escapeHTML(field(run, "durationMs"))} ms</div></div><div class="run-counts"><span class="error-text">${errorCount} errors</span><span class="warning-text">${warningCount} warnings</span><span>${count} 条发现</span></div><button class="text-button" data-run="${escapeHTML(runId)}">查看发现 ↗</button></article>`;
  }).join("");
  container.querySelectorAll("[data-run]").forEach((button) => button.addEventListener("click", () => showFindings(Number(button.dataset.run), findings)));
  container.querySelectorAll("[data-run-select]").forEach((input) => input.addEventListener("change", updateArchiveControls));
  updateArchiveControls();
}

function showCall(call) {
  byId("detailTitle").textContent = call.tool || "调用详情";
  const summary = [["时间", formatTime(call.time)], ["阶段", phaseName(call.phase)], ["结果", call.verdict || (call.error ? "ERROR" : "已记录")], ["定位", call.target || "—"]];
  const extra = [["耗时", `${call.ms || 0} ms`], ["发现", `${call.findings || 0}（${call.errors || 0} errors · ${call.warnings || 0} warnings）`], ["会话", call.session || "—"], ["项目归属", call.projectRoot || "未归属"]];
  if (call.error) extra.push(["错误", call.error]);
  byId("detailBody").innerHTML = `<dl class="detail-grid">${summary.map(([label, value]) => `<div><dt>${escapeHTML(label)}</dt><dd>${escapeHTML(value)}</dd></div>`).join("")}</dl><details class="extra-details"><summary>更多信息</summary><dl class="detail-grid">${extra.map(([label, value]) => `<div><dt>${escapeHTML(label)}</dt><dd>${escapeHTML(value)}</dd></div>`).join("")}</dl></details><p class="privacy-note">此日志记录工具调用元数据，不包含传入的源码。</p>`;
  byId("detailDialog").showModal();
}

function showFindings(runId, findings) {
  const items = findings.filter((entry) => Number(field(entry, "runId")) === Number(runId));
  byId("detailTitle").textContent = `运行 #${runId} · 发现列表`;
  byId("detailBody").innerHTML = items.length ? `<div class="finding-list">${items.map(({ item }) => `<details class="finding"><summary>${badge(item.severity)} <strong>${escapeHTML(item.ruleId)}</strong> <span>${escapeHTML(item.file)}:${escapeHTML(item.startLine)}</span></summary><p>${escapeHTML(item.message)}</p><small>${escapeHTML(item.file)}:${escapeHTML(item.startLine)}:${escapeHTML(item.startColumn)}</small></details>`).join("")}</div>` : '<p class="empty">这次运行没有保存发现。</p>';
  byId("detailDialog").showModal();
}

document.querySelectorAll("[data-view]").forEach((button) => button.addEventListener("click", () => openView(button.dataset.view)));
byId("refreshBtn").addEventListener("click", loadDashboard);
byId("searchInput").addEventListener("input", () => dashboard && renderCalls(projectCalls()));
byId("phaseFilter").addEventListener("change", () => dashboard && renderCalls(projectCalls()));
byId("statusFilter").addEventListener("change", () => dashboard && renderCalls(projectCalls()));
byId("metricSelect").addEventListener("change", (e) => { metricTrendSelection = e.target.value; if (dashboard) renderMetricTrend(dashboard.metricTrend); });
byId("activeRunsBtn").addEventListener("click", () => setHistoryMode("active"));
byId("archivedRunsBtn").addEventListener("click", () => setHistoryMode("archived"));
byId("archiveSelectedBtn").addEventListener("click", () => changeArchiveState(true));
byId("restoreSelectedBtn").addEventListener("click", () => changeArchiveState(false));
byId("closeDialog").addEventListener("click", () => byId("detailDialog").close());
byId("policyContent").addEventListener("input", markPolicyDirty);
byId("policySaveBtn").addEventListener("click", savePolicy);
byId("policyReloadBtn").addEventListener("click", loadPolicy);
byId("detailDialog").addEventListener("click", (event) => { if (event.target === byId("detailDialog")) byId("detailDialog").close(); });
loadDashboard();

/* ------------------------------------------------------------------ *
 * Configuration (policy) view
 *
 * The policy file is the one input every other verb reads, so the cockpit
 * shows it instead of hiding it: the discovery chain (which file is in force
 * and which it beat), the raw document (the editing surface, comments and
 * all), and the effective keys with provenance (why a threshold is what it
 * is). Saving goes back through the same endpoint the CLI reads from, so an
 * edit here cannot disagree with the next check.
 * ------------------------------------------------------------------ */
let policy = null;
let policyDirty = false;
let policyLoadedPath = "";

async function loadPolicy() {
  try {
    const response = await fetch("/api/policy", { headers: { Accept: "application/json" } });
    if (!response.ok) throw new Error(`读取失败 (${response.status})`);
    policy = await response.json();
    policyLoadedPath = policy.path || "";
    policyDirty = false;
    renderPolicy();
    return true;
  } catch (error) {
    setPolicyError(`${error.message}。确认服务仍在运行后重试。`);
    return false;
  }
}

function setPolicyError(message) {
  const notice = byId("policyNotice");
  notice.textContent = message;
  notice.hidden = false;
}

function renderPolicy() {
  const notice = byId("policyNotice");
  notice.hidden = true;
  renderPolicyChain();
  renderPolicySections();
  renderPolicyEditor();
  renderPolicyKeys();
}

// renderPolicyChain shows the discovery order and marks the file in force.
function renderPolicyChain() {
  const found = policy?.found;
  const name = policy?.name || "";
  byId("policyStatus").textContent = found ? "已找到" : "使用内置默认值";
  byId("policyPathNote").textContent = found ? policy.path : "未找到配置文件";
  const candidates = policy?.candidates || [];
  const tried = new Set(policy?.tried || []);
  byId("policyChain").innerHTML = candidates.map((candidate) => {
    const inForce = found && candidate === name;
    const mark = inForce ? "◉" : tried.has(candidate) ? "○" : "·";
    const klass = inForce ? "in-force" : tried.has(candidate) ? "missed" : "";
    const suffix = inForce ? " · 生效" : tried.has(candidate) ? " · 未命中" : "";
    return `<div class="policy-chain-step ${klass}"><span class="mark">${mark}</span><span>${escapeHTML(candidate)}${suffix}</span></div>`;
  }).join("") || '<div class="empty">—</div>';
}

// renderPolicySections lists the top-level keys as jump chips.
function renderPolicySections() {
  const sections = policy?.sections || [];
  const box = byId("policySections");
  if (!sections.length) {
    box.innerHTML = '<span class="subtle">无可解析的分节</span>';
    return;
  }
  box.innerHTML = sections.map((section) => `<button type="button" class="policy-section-chip" data-line="${Number(section.line) || 1}">${escapeHTML(section.key)}</button>`).join("");
  box.querySelectorAll("[data-line]").forEach((chip) => chip.addEventListener("click", () => jumpToPolicyLine(Number(chip.dataset.line))));
}

// renderPolicyEditor fills the textarea with the document on disk and keeps
// the dirty flag meaningful: a save is only offered when the text differs
// from what was loaded.
function renderPolicyEditor() {
  const area = byId("policyContent");
  const content = policy?.content || "";
  area.value = content;
  area.disabled = !policy?.found && !policy?.content;
  byId("policyModTime").textContent = policy?.modTime ? `磁盘版本 ${formatTime(policy.modTime)}` : policy?.found ? "读取时间未知" : "（不存在）";
  byId("policyDirtyHint").hidden = true;
  byId("policySaveBtn").disabled = true;
  policyDirty = false;
}

// renderPolicyKeys lists the effective configuration with provenance.
function renderPolicyKeys() {
  const keys = policy?.keys || [];
  const body = byId("policyKeys");
  byId("policyKeys").previousElementSibling;
  const cap = document.querySelector(".policy-keys-panel .panel-head .subtle");
  if (cap) cap.textContent = keys.length ? `${keys.length} 个已显式设置的键` : "全部为内置默认值";
  body.innerHTML = keys.length
    ? keys.map((entry) => `<tr><td>${escapeHTML(entry.key)}</td><td>${escapeHTML(policySourceLabel(entry))}</td></tr>`).join("")
    : '<tr><td colspan="2" class="table-empty">该文档没有显式设置任何键，全部沿用内置默认值。</td></tr>';
}

// policySourceLabel names where a value came from, keeping an inherited
// value visibly different from one this file wrote.
function policySourceLabel(entry) {
  if (!entry) return "—";
  if (entry.source === "this policy") return "本文件";
  if (entry.source === "builtin") return "内置默认值";
  return `来自 ${entry.source}`;
}

// jumpToPolicyLine moves the caret to a line and scrolls it into view, so a
// section chip is a real navigation rather than decoration.
function jumpToPolicyLine(line) {
  const area = byId("policyContent");
  const lines = area.value.split("\n");
  const index = Math.min(Math.max(1, line), lines.length) - 1;
  let offset = 0;
  for (let i = 0; i < index; i++) offset += lines[i].length + 1;
  area.focus();
  area.setSelectionRange(offset, offset + (lines[index] || "").length);
  const ratio = lines.length > 1 ? index / (lines.length - 1) : 0;
  area.scrollTop = ratio * (area.scrollHeight - area.clientHeight);
}

function markPolicyDirty() {
  policyDirty = byId("policyContent").value !== (policy?.content || "");
  byId("policyDirtyHint").hidden = !policyDirty;
  byId("policySaveBtn").disabled = !policyDirty;
}

async function savePolicy() {
  const button = byId("policySaveBtn");
  button.disabled = true;
  button.textContent = "保存中…";
  try {
    const response = await fetch("/api/policy", {
      method: "PUT",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify({ path: policyLoadedPath, content: byId("policyContent").value })
    });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(payload.error || payload.message || `保存失败 (${response.status})`);
    await loadPolicy();
    byId("updatedAt").textContent = `配置已保存 ${formatTime(new Date().toISOString())}`;
  } catch (error) {
    setPolicyError(`保存失败：${error.message}`);
    button.textContent = "保存配置";
    markPolicyDirty();
  }
}

function renderAccepted(entries) {
  const panel = byId("acceptedPanel");
  if (!panel) return;
  panel.hidden = !entries.length;
  byId("acceptedCount").textContent = entries.length ? `${entries.length} 条让步` : "—";
  byId("acceptedList").innerHTML = entries.map((entry) => {
    const expired = entry.expires && new Date(entry.expires) < new Date();
    const status = expired ? '<span class="tag stale">STALE</span>' : '<span class="tag ok">ACCEPTED</span>';
    return `<div class="accepted-item"><code>${escapeHTML(entry.rule)}</code> <strong>${escapeHTML(entry.file)}:${entry.line}</strong> ${status}<small>owner: ${escapeHTML(entry.owner)} · reason: ${escapeHTML(entry.reason)}${entry.expires ? ` · expires: ${escapeHTML(entry.expires)}` : ""}</small></div>`;
  }).join("");
}

// metricTrendSelection remembers the chosen metric across refreshes so the
// panel does not jump back to the first metric on every poll.
let metricTrendSelection = "";

// renderMetricTrend fills the metric experiment panel: a metric picker plus a
// per-run table (p50/p90/max, the thresholds then in force, violation count).
// A threshold change shows up as a step in the warn column; if the
// distribution itself moved too, p90/max move with it — that difference is
// what makes a tightening decision checkable.
function renderMetricTrend(mt) {
  const select = byId("metricSelect");
  const body = byId("metricTrendBody");
  const names = Object.keys(mt?.series || {}).sort();
  if (!names.length) {
    select.innerHTML = "";
    body.innerHTML = '<div class="empty-state"><strong>还没有度量快照</strong><small>运行 `metricgate check`（store.enabled）后这里显示每次运行的度量分布。</small></div>';
    return;
  }
  if (!names.includes(metricTrendSelection)) metricTrendSelection = names[0];
  if (select.dataset.built !== "1" || select.options.length !== names.length) {
    select.innerHTML = names.map((n) => `<option value="${escapeHTML(n)}">${escapeHTML(n)}</option>`).join("");
    select.dataset.built = "1";
  }
  select.value = metricTrendSelection;
  const points = mt.series[metricTrendSelection] || [];
  body.innerHTML = `<div class="table-wrap"><table><thead><tr><th>运行</th><th>时间</th><th>p50</th><th>p90</th><th>max</th><th>样本</th><th>warn 阈值</th><th>超阈</th></tr></thead><tbody>` +
    points.map((p) => {
      const changed = p.warnThreshold !== (mt.series[metricTrendSelection][points.indexOf(p) - 1] || {}).warnThreshold;
      return `<tr${changed ? ' class="metric-step"' : ""}><td>#${p.runId}</td><td>${escapeHTML(String(p.startedAt || "").slice(5, 16))}</td><td>${p.p50}</td><td>${p.p90}</td><td>${p.max}</td><td>${p.samples}</td><td>${p.warnThreshold}</td><td>${p.violated}</td></tr>`;
    }).join("") + "</tbody></table></div>";
}
