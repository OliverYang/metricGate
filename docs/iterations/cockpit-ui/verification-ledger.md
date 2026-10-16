# Cockpit SonarQube 对齐 · 独立验证台账

**goal `mv9rvxfs-ws5fu1`**（独立验证 CU-00..CU-04 并修复验证暴露的缺口）
建账时间 2026-10-16 00:55。基线：`bash scripts/cockpit-observe.sh fast` 退出 0，
`run #1002`，`gate: PASSED (fail_on=error)`，`0 errors / 13 warnings`，`go test ./internal/cockpit/...` 15.163s。

## 怎么读这张表

- **列「重跑」**：真实执行过的命令 + 退出码 + 关键输出行。没跑过的写 `—`，不允许留空（空档只能出现在
  「后端立项」或「需决策」两类，且在备注里点名原因）。
- **列「反向验证」**：`有(出处)` = 之前的 goal 已记录过一次变异实验；`无` = 本次补做；`n/a` = 该条不是
  断言而是产出物（文档/图）。
- **证据路径**：截图批次目录、测试文件:行、或本文件内的段落锚。

## 断言清单来源（不是凭空列的）

| 来源 | 路径 | 内容 |
| --- | --- | --- |
| 契约标准 | `docs/iterations/cockpit-ui/iter-0{0..4}-*.md` 的「成功标准」节 | 5 个 CU 共 27 条标准 |
| 前端契约测试 | `internal/cockpit/web_contract_test.go` | 16 个 Test（14 个既有 + 本 goal 新建 `TestNoColourLiteralsOutsideTokens` / `TestShellHasNoSecondNav`） |
| 离线渲染断言 | `internal/cockpit/web/render_all.mjs` | 82 处 `problems.push`（9 视图 × 3 scope × 2 模式 + 4 tab + 7 路由往返 + 表完整性 + 源码窗口 + 各视图专有块） |
| 真实载荷冒烟 | `internal/cockpit/web/smoke.mjs` | 89 处 `fail(`（含 L8 tripwire、三栏布局、tab URL 状态、gate 裕度/语言段） |
| 后端回归 | `internal/cockpit/gate_api_test.go` | 5 个 gate 用例 |

## CU-00 · 外壳 + Issues 列表（`iter-00-shell-and-tokens.md`）

**t2 重跑（2026-10-16 00:56）**：
```
go test ./internal/cockpit/ -run '<14 个契约测试名>' -count=1 -v   →  exit=0，`--- PASS` 计数 14
cd internal/cockpit/web && node render_all.mjs                    →  exit=0，9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok
```

| # | 契约标准 | 断言 / 落点 | 重跑 | 反向验证 | 证据 |
| --- | --- | --- | --- | --- | --- |
| 00-1 | smoke fetch stub 早于 `await import("./app.js")` | `smoke.mjs:192`（stub 在 `:166`） | PASS（`fast` 的 live smoke 段通过） | **有（t3 补）**：把 `const app = await import(...)` 挪到 `globalThis.fetch = ...` 之前 → `FAIL projects: 0 cards for 1 projects`（首个 scope 复用被 park 的 rejected promise） | `smoke.mjs:166` vs `:192`，注释块 `:178-191` |
| 00-2 | `style.css` 语义令牌表：`:root` 外 0 颜色字面量 | **`TestNoColourLiteralsOutsideTokens`（t3 新建）** | PASS（`:root` 两块 `:30-163` / `:172-192`，块外字面量 0） | **有（t3 补）**：`.gate-meta-item` 塞 `background: #ff00ff` → `style.css uses 1 colour literal(s) outside a :root token block (first: #ff00ff)` | `web_contract_test.go` 新测试；原先只有 `awk` 手工命令 |
| 00-2b | 无未声明 CSS 变量 | `TestCSSVariablesAreDefined` | PASS（`declared 121 / used 102 / undeclared 0`） | **有（t3 补）**：`.gate-meta-item` 用 `var(--e-color-that-nobody-declared)` → `uses var(...) but never declares it; the rule silently does nothing` | `web_contract_test.go:215`；多出的 1 个使用点是 CU-03 的 `.gate-meta` 系列 |
| 00-3 | 外壳两行网格、rail 是 9 视图唯一入口、每视图恰一个 active | `TestSidebarViewsExist` + `TestAppShellMountPointIsInMarkup` + `smoke.mjs:583` | PASS ×3 | **有（t3 补）**：`paintRail` 里让 `projects` 恒 active → `FAIL nav: view overview marks 2 rail entries active`（overview/issues/gate 各一条） | `smoke.mjs:582-583`；`paintRail`(app.js:2404) |
| 00-4 | 删除 stage 内 `workspaceNav` | **`TestShellHasNoSecondNav`（t3 新建）** | PASS（app.js 无该标识；`index.html` 侧栏外 0 个 `nav.view` 按钮） | **有（t3 补）**：恢复 `const workspaceNav = () => ""` → `app.js declares workspaceNav again; the rail must stay the only navigation or two lists disagree about the current view` | 新测试双查：源码无该名 + 标记里 nav 按钮全在 `<aside class="sidebar">` 内 |
| 00-5 | `fileBase()` 用于 file-group 头与 `.issue-detail-loc` | `grep -n fileBase(app.js` → 5 处：`733`(issue-loc) `765`(file-group 头) `1124`(issue-detail-loc) `1203`(源码窗口) `1261`(详情 meta) | PASS + **smoke.mjs:410-437 两侧断言（t3 新建）** | **有（t3 补，两个变异）**：① `title` 改用 `fileBase()` → `FAIL issues: no card carries the full path ... in a title`；② 可见文本改印全路径 → `FAIL issues: the card shows the full path as visible text (...), which is what fileBase() exists to prevent`（第一个变异暴露出只守 title 一侧，已补可见文本一侧） | `smoke.mjs:410-437`；驱动数据取真实载荷的绝对路径，不是夹具 |
| 00-6 | facet 计数来自全量、点击才收窄 | `smoke.mjs:361-390` facet 口径断言组 | PASS（`facetCache`(app.js:411) 缓存全窗口计数，筛选后不清缓存） | **有（t3 补）**：`facets: facetCache(result)`（去掉 `refreshFacets` 三元）→ `FAIL issues: the facet counts moved with the filter — drew 1,1,2,1, full window is 1,1,2,1,1` | `smoke.mjs:361-390`；`app.js:452` |
| 00-7 | `activityEvents` 不再用 `\|\| dashboard?.runs` 兜底 | `activityEvents`(app.js:1784) | PASS：已改走 `runsInScope(s).runs` + `callsInScope(s)`，无兜底 | **有（t3 补）**：空 scope 时回退到各 project 的 runs → `FAIL activity (unassigned): a run with a project identity ("运行 #1") was drawn under the unassigned scope` | `smoke.mjs:515-531`（注释明写「keeps L3 from coming back」）；`app.js:1784-1789` |
| 00-8 | UI 评审约束文件 | `docs/constraints/cockpit-ui-review.md` | 存在（5022B，mtime 00:08） | n/a | 本次实测 |
| 00-9 | 截图 issues-list × 3 scope 判定 | `/tmp/sq/sq-issues.png` | **PASS**：`/tmp/shots/metricGate-1792084867/`（7.5/6.1/4.5s，0 failed，`err=` 全空，105814/103889/105133 bytes，`groups=1`） | n/a | 逐区块表已写入 `iter-00-shell-and-tokens.md`「t4 复拍」段；新发现 L10 扩展（`/api/issues` 同样不吃 scope） |

**t3 结论（2026-10-16 01:1x）**：8 条可变异的标准全部补上反向验证，**其中 2 条原先根本没有断言守着**（00-2 颜色字面量、00-4 `workspaceNav`）——两者在台账里都只有一次性 `grep`/`awk` 命令，而手工命令不会让构建变红。已新建 `TestNoColourLiteralsOutsideTokens` 与 `TestShellHasNoSecondNav` 补上，并各做一次变异确认能 FAIL。
另：00-5 的首版断言只守 `title` 一侧，变异②（可见文本改印全路径）未被抓到，已补「可见文本必须是短名」的第二侧。
新测试后全量：`go test ./internal/cockpit/... -count=1` → `ok 9.146s`。

## CU-01 · Summary / 项目概览（`iter-01-summary.md`）

**t5 重跑（2026-10-16 01:3x）**：
```
cd internal/cockpit/web && node render_all.mjs        →  exit=0，9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok
go test ./internal/cockpit/ -run 'TestCockpitRendersThePayloadsTheServerActuallySends' -count=1  →  ok 2.045s（live smoke 段）
bash scripts/cockpit-observe.sh quick                 →  exit=0，render_all + 定向契约测试（ok 0.669s）
```
**t5 新增断言（缺口）**：标准 1 与标准 5 在 t5 之前**没有任何断言守着** ——
① 度量卡：`smoke.mjs:407` 只查「有 measures 就出现 `measure-card`」这一个串，`render_all.mjs` 里对度量卡零断言。
一个只画第一张卡的循环、一张丢了 A–E 方框或阈值行的卡，两者都过。已补：
`render_all.mjs`（夹具侧，按 `fixtures.measures.measures.length` 计数 + 每张卡查 `class="measure-grade grade-<grade>"`、
`warn N · err N` 阈值行、`N 个位置超阈`）；`smoke.mjs`（真实载荷侧，9 张 vs 载荷条数 + 逐卡 grade 框与阈值行，
夹具只有 1 条而真实载荷有 9 条，只靠夹具测不出「只画第一张」）。
② 趋势口径：`trendScope`(app.js:1486) 的三分支文案在 render_all 里零断言。已补三 scope 文案断言
（未归属 scope 必须含「未归属」；picker 必须含「未归属」——载荷里 `ReportUnassigned` 是唯一全局报告，
把它说成合并全量就是 L3 同款谎；project scope **不得**含「未归属」——它有自己的序列）。

| # | 契约标准 | 断言 / 落点 | 重跑 | 反向验证 | 证据 |
| --- | --- | --- | --- | --- | --- |
| 01-1 | `measureCard` = measure tile（标签/大字/A–E/阈值带一行） | `measureCard`(app.js:970) + **`render_all.mjs:371-395`（t5 新建）** + **`smoke.mjs:408-431`（t5 新建）** | PASS ×3（render_all 夹具 1 卡；smoke 真实载荷 **9 卡**，`grade-A` ×9 + 阈值行逐条对上） | **有（t5 补）**：删掉 `<span class="measure-grade grade-${measure.grade}">` → render_all `FAIL overview: measure "cyclomatic-complexity" did not render its grade box (grade-B)` + smoke `FAIL overview: measure "cyclomatic-complexity" rendered without its grade box (grade-A)`（真实载荷侧连报 3 条，9 条中前 3 条都被点名） | `app.js:970-977`；断言两半守不同输入（夹具 1 条 vs 载荷 9 条） |
| 01-2 | run/finding/trend 取数经 `scopeAggregate`(1021) | render_all allow-list（源码侧，`render_all.mjs:315-356`） | PASS（`metricCards`(1313)..`VIEWS.policy`(1600) 段内 `s.dashboard` 命中集合恰等于 ALLOWED 三行） | **有（t6 补）**：`metricCards` 内塞 `const bogus = s.dashboard.totallyNewField;` → `FAIL overview scope: app.js reads [...4 行...] off s.dashboard, expected exactly [...3 行...]` + `FAIL overview scope: app.js:1314 reads s.dashboard with no L8 comment above it` | allow-list 是「源码侧」守「有没有多出第 4 处读」；与 01-3 载荷侧合起来才是完整的 L8 契约 |
| 01-3 | 项目级不存在的 3 字段保留顶层读 + L8 注释 | smoke L8 tripwire（载荷侧，`smoke.mjs:441-470`） | PASS（真实载荷 `projects[0]` 无 `accepted`/`metricTrend`/`summary` 三键） | **有（t6 补）**：给 `/tmp/shots-payloads.json` 副本的 `projects[0]` 注入 `accepted: []` + `metricTrend` → `FAIL overview: ProjectSummary now carries accepted, metricTrend (internal/cockpit/dashboard.go:62-77) — L8 is fixed, so the top-level reads in app.js must become scope-aware` | 载荷侧守「Go 结构体是否已长出这三字段」；夹具不会跟着 Go 结构体变，所以这一半不能用 01-2 代替 |
| 01-4 | 概览三区主次清晰，单卡 ≤8 色 | 令牌纪律（`TestNoColourLiteralsOutsideTokens`）+ 截图 | PASS（令牌测试过；`measure-card` 的颜色只来自 `grade-A..grade-E` 一类） | n/a | 截图判定见 01-6 |
| **01-7** | **（t6 新发现，契约外）「质量运行」一卡必须与运行页列表同数** | `metricCards`(app.js:1313) + `render_all.mjs:396-414` + `smoke.mjs:411-427`（t6 新建） | **FAIL → 已修**：卡片读 `scopeAggregate(s).trend.runs`，列表读 `runsInScope(s).runs.length`，两把尺子。真实载荷直连实测：并集 **46** 条 vs `trend.runs` **20** —— 全局 scope 下概览写 20、运行页写 46，差 26 | **有（t6 补）**：改回 `agg.trend.runs` → render_all 报 6 条（`the 质量运行 card says 9 at scope=null history=active but the run list holds 2`），smoke 报 `the 质量运行 card says 0 but the run list holds 1` | `iter-01-summary.md`「t6 复拍与缺陷修复」段；根因 `dashboard.go:116-117`（`LatestUnassignedRuns(20)` 与 `ReportUnassigned(20)` 是两次查询） |
| 01-5 | 趋势口径如实（global = 未归属序列） | `trendScope`(app.js:1486) + **`render_all.mjs:396-410`（t5 新建）** | PASS（三 scope 文案：`scope=null`→「未归属序列 · …」、`scope=""`→「未归属运行的趋势…（服务端只发未归属序列，不发送合并全量）」、project→「趋势：… 的运行」） | **有（t5 补）**：picker 分支改成 `` `全局趋势…` `` → `FAIL overview: the picker's trend caption does not say the series it draws is the store's unassigned one` | `app.js:1480-1492` 的注释明写 `ReportUnassigned (dashboard.go:114)` 是唯一全局报告 |
| 01-6 | 截图 overview × 3 scope 判定 | `/tmp/sq/sq-summary.png` | **PASS**：`/tmp/shots/metricGate-1792086381/`（13.3/7.9/6.9s，0 failed，`err=` 全空，152183/155594/160095 bytes） | n/a | 逐张判定 + 差异表写入 `iter-01-summary.md`「t6 复拍与缺陷修复」段 |

## CU-02 · Issue 详情四 tab（`iter-02-issue-detail.md`）

**重跑（t7，2026-10-16 02:0x）**：`go test ./internal/cockpit/ -run '<14 个契约测试名>' -count=1 -v` → exit=0，`--- PASS` 计数 14（含 `TestCockpitRendersThePayloadsTheServerActuallySends` 1.78s）；`node render_all.mjs` → `9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok`。

| # | 契约标准 | 断言 / 落点 | 重跑 | 反向验证 | 证据 |
| --- | --- | --- | --- | --- | --- |
| 02-1 | `.issue-detail-loc` 与 file-group 头走 `fileBase()` | `app.js:740`(issue-loc) `772`(file-group 头) `1131`(issue-detail-loc) `1210`(源码窗口) `1268`(详情 meta)，5 处全带 `title` | PASS | **有（t8 补）**：`1131` 改成 `${issue.file}` → 首跑绿（列表侧 L1 断言看不见详情 pane），补切片断言后 `FAIL issue.select: the detail location line does not show the short name "engine.go:7"` | `smoke.mjs` L1 块 + 新详情切片断言；`iter-02` t8 段 |
| 02-2 | `issueTab` 进 `ROUTE_KEYS`，刷新/分享不丢 | `ROUTE_KEYS` 尾部 `["open","openFingerprint",null]` / `["tab","issueTab","where"]`；`render_all.mjs:556` 两条 `#/view=issues&open=…`、`…&tab=activity`；`render_all.mjs:566-572` 往返比对；smoke `tab=why` | PASS | **有（t8）**：摘掉 `["tab", …]` 行 → `FAIL ensureSource: the 原因 tab is not in the URL (view=issues&open=ec57886ee62b8384), so a shared link loses it`（render_all 仍绿，说明这一条只有 smoke 守得住） | `iter-02` t8 段 |
| 02-3 | 代码窗口对齐 / 锚点高亮 / `truncated` 提示 / 隐私说明 | `smoke.mjs:701` 的 `source-line`；**新增** `render_all.mjs` 源码窗口块（锚点行计数 === 1、`source-anchor-note` 存在、`truncated` 两侧对称） | PASS | **有（4 次，t14 补）** | `iter-02` t8 段图 |
| 02-4 | 详情区无装饰性 inline style | `grep -c 'style="'` → **5 处**（`789` flow-track 条宽、`806-809` bar-stack/bar-error/warning/info 柱高，全数据驱动）；详情段 1100–1290 **0 处** | PASS（比契约更好：契约记的 `app.js:1541/1542` 已不存在） | n/a | `iter-02` |
| 02-5 | 三栏布局 `has-detail` | `smoke.mjs:648-655` 出现/消失/复原三段 | PASS | **有（t8）**：摘掉条件类 → `FAIL issue.select: an open issue did not switch the layout to three columns` | `iter-02` t8 段 |
| 02-6 | `ensureSource` 两 guard（tab / fingerprint） | `smoke.mjs:664-693`；新增 `let requestCount` 按**调用次数**计数 | PASS | **有（t8 补）**：拆 tab guard → 首跑绿（被 fingerprint guard 遮住），补 `record({source:null})` + 计数断言后 `FAIL …the 原因 tab read the operator's source…`；删 fingerprint guard → 首跑绿，补断言后 `FAIL …already loaded re-read the file` | `iter-02` t8 段 |
| 02-7 | 截图 detail-{where,why,activity,more} 判定 | `/tmp/sq/sq-issue-detail.png` | **PASS**：`/tmp/shots/metricGate-1792087773/`（15.2/7.7/6.9/7.0s，0 failed，`err=` 全空，308601/237325/246112/226355 bytes，`detail=` 8010/1796/1904/1733） | n/a | 逐区块判定表写入 `iter-02` t8 段 |

## CU-03 · 质量门禁页（`iter-03-gate.md`）

**重跑（t9，2026-10-16 02:2x）**：`go test ./internal/cockpit/ -run 'TestGate|TestLanguageRank' -count=1 -v` → exit=0，`--- PASS` 计数 **6**（五用例 + `TestGateViewReportsTheRunVerdictUnderFailOn`，0.994s）；`node render_all.mjs` → `9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok`。

**t9 补的断言缺口（重要）**：CU-03 的四条前端标准（03-1..03-4）此前**只有 render_all 夹具侧断言**，smoke 侧对 gate 页零断言（`grep -n 'base("gate")' smoke.mjs` 原先无命中）。夹具只有 1 条 condition，而真实载荷有 **25 条跨三语言段且 `hasData` 全 false** —— 一个只画第一组、或整列丢掉、或把无数据画成绿「通过」的表格都能让夹具断言继续绿。已在 `smoke.mjs` 新增真实载荷侧的 gate 断言块（约 `:556-608`）：25 条 vs 裕度单元格数、语言段序列必须等于载荷的 `go/java/javascript`、`class="badge unknown"` 计数必须等于 25 条、页面上不得出现 `class="badge pass"`、必须有 `class="cell-unknown">无数据`、不得出现 `>—</td>`、`gate-meta-item` 必须 4 个、必须含「最差单文件」。

| # | 契约标准 | 断言 / 落点 | 重跑 | 反向验证 | 证据 |
| --- | --- | --- | --- | --- | --- |
| 03-1 | `gateBanner` 三态（passed/failed/unknown） | `smoke.mjs:525-545`（三态映射 + passed/failed glyph 不同 + `gateBanner(null)` 为 unknown）；`app.js:891` 附近 `tone`/`glyph`/`reason` | PASS | **有（t10）**：`tone` 改成 `status === "failed" ? "failed" : "passed"` → `FAIL gate banner: status "pending" rendered as is-passed, not is-unknown` | `iter-03` t10 段 |
| 03-2 | 条件表裕度列（`gateHeadroom`，无数据单独一档） | `render_all.mjs:105-160`（4 用例 + 单元格计数 + badge pass/unknown 分档 + `cell-unknown`）；**t9 新增** `smoke.mjs` 25 条 vs 单元格数 + `badge unknown` 计数 | PASS | **有（t10）**：裕度单元格换成裸数字 → `FAIL gate: 25 conditions in the payload but 0 headroom cells in the table` | `iter-03` t10 段 |
| 03-3 | `fail_on`/`runId`/`commit`/`startedAt` 收敛一行（`gateMeta`） | `render_all.mjs:161-199`（6 断言：4 项齐 / 只有 runId 时 1 项且无 fail_on / `{}` 与 `null` 都空 / 无 `<code></code>` 与 `>—<`）；**t9 新增** smoke 侧 `gate-meta-item === 4` | PASS | **有（`/tmp/rv4`）**：把 runId/commit 两个 push 改成 `if (false)` → `FAIL gate meta: four facts in the payload drew 2 items` | `iter-03` |
| 03-4 | 「最差单文件」口径注脚独立成块（移出 `.table-wrap`） | `render_all.mjs:188-205`（3 断言：页面含该串 / `.table-wrap` 切片内不含 / `class="panel-foot metric-convention"`）；**t9 新增** smoke 侧同串存在 | PASS | **有（`/tmp/rv5`、`/tmp/rv6`）**：注脚放回 wrap 内 → `FAIL gate: the convention note is inside .table-wrap, so it scrolls with the columns` | `iter-03` |
| 03-5 | 条件按语言分组、一语言一表头 | `internal/cockpit/gate_api_test.go` 五用例（`TestGateConditionsGroupByLanguageInRankOrder` 等，行 92/120/152/188/214）+ `render_all.mjs:207-250`（3 组断言 + 无 language 载荷 0 表头）；**t9 新增** smoke 侧语言段序列 = 载荷 `go/java/javascript` | PASS | **有（`/tmp/rvgate` 三变异）**：去 `errAt==0` 回退 / 合并跨语言同度量行 / 无数据当实测 0，三变异全被抓到 | `iter-03` |
| 03-6 | 截图 gate × 3 scope 判定 | `/tmp/sq/sq-summary.png` | **PASS**：`/tmp/shots/metricGate-1792088792/`（7.5/5.2/5.3s，0 failed，`err=` 全空，287058/284589/285649 bytes，`MG_SHOT_HEIGHT=2700`） | n/a | 逐区块判定表 + 三图同构结论写入 `iter-03` t10 段 |

## CU-04 · Activity / Rules / Policy（`iter-04-activity-rules-policy.md`）

### t11 就绪性判定（2026-10-16 02:3x）：**已就绪**

三条独立证据，全部指向「开发已完成、可以开始验证」：

1. **开发 goal 已归档为 complete**：`.pi/goals/archived/goal_2026101602184181_mv9qakv4-an3tio.md`
   → `"status": "complete"`、`updatedAt 2026-10-15T18:18:41.908Z`（本机 02:18），
   8/8 tasks 全 complete（task-0 UI 评审规则 / task-1 清 policy inline style / task-2 Rules 目录 + 排序表头 /
   task-3 趋势刻度 / task-4 时间线行态与 L3 复核 / task-5 facet 口径 / task-6 9 张截图 / task-7 判定 + 终批验证）。
   `usage.tokensUsed 2889929`、`activeSeconds 6713`。
2. **契约文档有收口段**：`iter-04-activity-rules-policy.md`（220 行）含「截图判定（task-7）」与
   「CU-04 收口（task-7）」两节，七条成功标准逐条核对，末句 `**CU-04 closed。**`。
   另有「审计退回后新增」段记录 task-2 的排序表头是审计退回后补齐的（`rulesSort` state app.js:247 +
   `rulesOrder`(1954) + `rulesHead`(1969) + `ACTIONS["rules.sort"]`(2067)）。
3. **并行 writer 已停**：`.pi/goals/active_goal_*` 只剩本 goal（`mv9rvxfs-ws5fu1`，10/14）与
   两个无关 goal（`mv9sinup-46dj5a` paused 调研、`mv9tordp-52ny2c` SQL/Verilog/VB.NET 语言支持）；
   `.pi/goals/active_goal_2026101600034172_mv9qakv4-an3tio.md`（CU-04 的 writer）**已不在 active 目录**。
   台账里记录的 00:53 那次并发写入源已消失。

**结论**：走「就绪」分支 —— t12 只做验证（重跑 + 变异 + 截图判定），不需要手改。

契约 7 条标准见 `iter-04:22-33`。**goal `mv9qakv4-an3tio` 已 8/8 完成（2026-10-16）**。
终批 `bash scripts/cockpit-observe.sh fast` = `0 errors, 13 warnings, 413 metric snapshots` / run #1003 /
`gate: PASSED`；`check_path('internal/cockpit/web', diff_only:true)` = `verdict=PASS errors=0 warnings=12`。

task-2 的「表头可排序」经独立审计退回后补齐：`rulesSort` state(app.js:247) + `rulesOrder`(1954) +
`rulesHead`(1969) + `ACTIONS["rules.sort"]`(2067)，断言落在 `render_all.mjs` 的 rules 块
（aria-sort 三态 / 升降序互逆 / 派生列按单元格排 / 点按反向 / 点其它列替换）。

| # | 契约标准 | 断言 / 落点 | 重跑 | 反向验证 | 证据 |
| --- | --- | --- | --- | --- | --- |
| 04-1 | `activityEvents` 只返当前 scope；时间线一行同构 | `activityEvents`(app.js:1795) 走 `runsInScope(s).runs` + `callsInScope(s)`；`smoke.mjs:471-486` 逐 run 双向断言 + 行三段完整性断言 | t12 重跑 | 有(t12 变异：恢复 `dashboard.projects[].runs` 兜底 → smoke `FAIL activity (unassigned): a run with a project identity ("运行 #1") was drawn under the unassigned scope`) | 已达成 |
| 04-2 | 趋势图刻度（峰值 + 单位）+ 空态文案 | `chart-caption`(app.js:1772) `legend-scale`(app.js:1774)；空态「还没有事件」；**t12 新增** `render_all.mjs` 趋势块（caption/峰值/legend-scale/空态四断言） | t12 render_all | 有(t12 两处变异：删 caption+legend-scale → `the trend chart renders without its caption, so a bar has no stated unit` + `the legend does not repeat the chart's peak, so the axis has no scale in words`；峰值写 max+1 → `the caption does not state the peak (8) the bars are drawn against`；删空态 → `a scope with no runs and no calls renders no empty state`) | 已达成 + 断言补齐 |
| 04-3 | Rules 页目录化 + **表头可排序** | `VIEWS.rules` + `rulesOrder`(app.js:1958) + `rulesHead`(app.js:1973) + `ACTIONS["rules.sort"]`(2067)；`render_all.mjs` rules 块（aria-sort 三态 / 升降序互逆 / 派生列按单元格排 / 点按反向 / 点其它列替换 / **t12 新增** 箭头字形与 aria-sort 绑定） | t12 render_all | 有(t12 两处变异：`rulesOrder` 关排序 → 4 条 `ascending order starts at zzz.tolerance ... expected aaa.first` 等；箭头恒 `↑` → `the descending active column draws ↑, not ↓ (aria-sort and the glyph disagree)`) | 已达成 + 断言补齐 |
| 04-4 | Policy 页清掉装饰性 inline style；发现链三态可读 | `grep -c 'style="' app.js` = 5（全含 `${}` 插值）；`.policy-keys-panel`(style.css:1361) 承接原间距 | — | 有(规则5 判定命令报出 1658/1659) | 已达成 |
| 04-5 | facet 计数口径推广到三页 | `render_all.mjs:247-306` 缓存读画 41,7,44,4,33,12 vs 1,1,1；`smoke.mjs:362-405` 走真实请求路径 | — | 有(两侧) | 已达成 |
| 04-6 | 新建 `docs/constraints/cockpit-ui-review.md`（L5 规则） | 6 条规则各带可执行判定命令；规则 1/2/4/5/6 已实测 | — | 有(规则5 修复前正确报违规) | 已达成 |
| 04-7 | 截图 activity / rules / policy × 3 scope 判定 | 判定写入 `iter-04`「截图判定」节（9 张逐张采纳 + 依据 + 差异） | — | n/a | 已达成 |

**审计退回后新增（2026-10-16）**：task-2「表头可排序」补齐（`rulesSort` state app.js:247 +
`rulesOrder` 1954 / `rulesHead` 1969 / `ACTIONS["rules.sort"]` 2067 + style.css `.th-sort` 593），
断言 6 条在 `render_all.mjs` 的 rules 块。截图用最终树单批重拍：`/tmp/shots/metricGate-1792086542`
9 张全部 `0 failed`、mtime 01:49:19–01:49:55 晚于最终写入 app.js 01:45:14 / style.css 01:26:45，
harness 副本与当前树 sha256 一致。早于其的 `-1792082899`/`-1792082995`（跨写入点分批）已作废。

## 跨屏一致性（t13）

重跑：`go test ./internal/cockpit/ -count=1 -v -run 'TestEventListenersAreCentralised|TestEveryTemplateActionIsHandled|TestEveryHandledActionIsReachable|TestKeyboardShortcutsYieldToTyping|TestCSSVariablesAreDefined|TestViewTableMatchesSpec'`
→ `--- PASS` 6 条、`ok metricgate/internal/cockpit 0.574s`、exit=0；`bash scripts/cockpit-observe.sh quick`
→ `render_all: 9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok` + `ok 0.852s`（5.9s）。

| # | 项目 | 断言 | 重跑 | 反向验证 | 证据 |
| --- | --- | --- | --- | --- | --- |
| X-1 | 路由往返 + 表完整性 | `render_all.mjs` 新增 `EXPECTED_ROUTE_PARAMS`/`EXPECTED_CALL_PARAMS` 独立副本 + 逐行往返 | PASS | **有（12+2 次，见下）** | `render_all.mjs` router 块；`ROUTE_HASHES` 7 条 |
| X-2 | 事件只在 S8 注册（≤6） | `TestEventListenersAreCentralised` | PASS（实测 5 处：click/change/input/keydown/hashchange） | 有 | `web_contract_test.go:138` |
| X-3 | 每个模板 action 都有 handler | `TestEveryTemplateActionIsHandled` | PASS | 有 | `web_contract_test.go:93` |
| X-4 | 每个 handler 都可到达 | `TestEveryHandledActionIsReachable` | PASS | 有 | `web_contract_test.go`（无独立文案，与 X-3 联动） |
| X-5 | 快捷键在输入框里让位 | `TestKeyboardShortcutsYieldToTyping` | PASS | 有 | `web_contract_test.go:115` |
| X-6 | CSS 变量全部有声明 | `TestCSSVariablesAreDefined` | PASS | 有 | `web_contract_test.go:226` |
| X-7 | 视图表与 spec.json 一致 | `TestViewTableMatchesSpec` | PASS（9 = 9） | 有 | `web_contract_test.go:289/299` |
| X-8 | S1..S8 分层顺序 | `TestAppJSSectionsAppearInLayeringOrder` | PASS | 有 | `web_contract_test.go:47` |
| X-9 | 片段不拼进裸字符串 | `TestFragmentsAreNotJoinedToBareStrings` + `TestMappedListsAreJoinedFragments` | PASS | 有（两半） | `web_contract_test.go:381/179` |
| X-10 | 模板插值走 html`` | `TestTemplateInterpolationGoesThroughHTML`（`innerHTML =` 恰好 1 次） | PASS | 有 | `web_contract_test.go:149` |

### X-1 的断言空洞（本轮最重要发现，已修）
**原断言是自指的**：路由往返由 `app.ROUTE_KEYS` 驱动遍历，删掉表里任意一行，两边读的是同一张更短的表，循环与自己一致 → 全绿。实测把 `severity` / `sort` / `page` / `tab` / `rule` / `open` / `history` / `q` / `file` / `status` / `scope` / `view` **十二行逐一删除，render_all 十二次全部报 ok**（`9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok`，exit=0）——即「筛选条件、排序、页码从 URL 里消失、分享链接丢状态」这一整类回归无人守。

修法（`internal/cockpit/web/render_all.mjs`）：加一份**独立于表的手写契约副本** `EXPECTED_ROUTE_PARAMS = [view scope severity status rule file q sort history page open tab]` 与 `EXPECTED_CALL_PARAMS = [cq phase callStatus trace]`，双向比对（表缺 → 「no longer carries，该状态无法分享或重载」；表多 → 「carries X, which this contract does not list」），另加逐行「取样值必须到达 hash 并原样回来」的往返。

修后 12 行逐一删除全部 FAIL，例：
```
FAIL router: ROUTE_KEYS no longer carries "severity", so that state can no longer be shared or reloaded
FAIL router: ROUTE_KEYS no longer carries "tab", so that state can no longer be shared or reloaded
```
新增一行 `["zzz","metricChoice",""]` → `FAIL router: ROUTE_KEYS carries "zzz", which this contract does not list; add it above if it is really shareable state`。
`CALL_PARAM_KEYS` 同法：删 `cq` / `trace` → `FAIL router: CALL_PARAM_KEYS no longer carries "cq", so that call filter can no longer be shared`。

### 其余 9 条变异原文
| 变异 | 失败原文 |
| --- | --- |
| X-2 在 S7 前加 2 个 `addEventListener` | `app.js registers 7 listeners; events should be delegated through S8's table` |
| X-3 把 `data-action="bulk.clear"` 改成 `orphan.nobody` | `data-action="orphan.nobody" is rendered but never handled; it would be a dead control` |
| X-4 只重命名 `ACTIONS["bulk.clear"]`（模板不动） | 同时 FAIL 两条：X-3 报 `data-action="bulk.clear" is rendered but never handled` + `TestEveryHandledActionIsReachable` FAIL |
| X-5 `typingInAField(event)` → `false` | `the keydown handler does not check for typing; shortcuts would fire inside inputs` |
| X-6 `.gate-meta-item` 加 `color: var(--e-color-nobody-declared)` | `style.css uses var(--e-color-nobody-declared) but never declares it; the rule silently does nothing` |
| X-7 加 `VIEWS.ghostview` | `the frontend has 10 views ([ghostview projects issues …]), the spec declares 9` + `VIEWS.ghostview is not in webspec.Views(); it is unreachable from a shared link` |
| X-8 调换 `//! S6` 与 `//! S7` | `section "S7 · VIEWS" appears out of order; S55 must not be imported by an earlier one` |
| X-9a `join = (f) => raw(interpolate(f))` → `interpolate(f)` | `join() no longer returns a raw fragment; every list() would be double-escaped on insert` |
| X-9b 新增 `xs.map(x => html`<b>${x}</b>`).join("")` | `web/app.js:996 joins a template result directly (…); use list() so fragments are not stringified into [object Object]` |
| X-10 新增 `document.body.innerHTML = "x"` | `innerHTML is assigned 2 times; exactly one place (render) should own the DOM write` |

### 自己踩的两个假绿（记下来免得重复）
- X-4 首版注入 `export const VIEWS = {};` 后追加，但真实声明形态是 `VIEWS.issues = {`（`export const VIEWS = {}` 只是空壳）→ 变异没生效，测试绿。改在 `export const VIEWS = {};` 之后追加 `VIEWS.ghostview = {…}` 才 FAIL。**注入点必须按真实语法形态选，不能按印象。**
- X-5 首版只加 1 个 listener，而当时已有 5 个、阈值是 `>6` → 6 不越线，测试绿。加 2 个才 FAIL。**变异要越过阈值，不是「加一点」。**

### 结论
X-1..X-10 十条全部有断言、有重跑、有反向验证。其中 X-1 原本是**恒真断言**（本轮修复），X-4/X-9/X-10 的断言形态与名字不符（分别实际守「模板 action 双向一致」「join 返回 raw」「innerHTML 唯一写点」），台账已按实际语义登记，避免以后照名字找错地方。

## 遗留（后端立项 / 需决策）

| id | 内容 | 性质 | 出处 |
| --- | --- | --- | --- |
| L6 | `/api/measures` 缺 `runId`/`commit`/`startedAt`（gate 载荷有） | 后端立项 | `iter-03:20` |
| L8 | 项目级载荷缺 `accepted`/`metricTrend`/`summary.skipped` | 后端立项 | `iter-01:47-67` |
| L9 | `gateVerdict` 完全不看 `metric_snapshots`（PASSED 与表内越线并存） | 语义待定 | `iter-03:88-98` |
| L10 | `/api/gate` **与 `/api/issues`** 都不接受 scope 参数（两者都走 `projectScopeRoot()` = 服务启动目录）→ 门禁页 scope 切换是无效控件；**问题分诊页更严重：全局列表与未归属列表显示同一份项目数字且面板未声明** | 后端立项 | `iter-03:99-110`、`iter-00` t4 段 |
| L11 | 逐语言实测值：`RunMeasures` SQL 只 `GROUP BY metric` | 后端立项 | `iter-03` |
| **V-1** | ~~02-3 的锚点类名（`is-anchor`）与「已截断」文案没有断言~~ **已修（t14）** | 已补断言 | `iter-02` 02-3 行 |

## 进度

- [x] t1 建台账骨架 + 基线复现（`run #1002`）
- [x] t2 CU-00 重跑（14 契约测试 + render_all）
- [x] t3 CU-00 变异（8 条全部反向验证；新建 2 个契约测试补上原先只有手工命令的两条）
- [x] t4 CU-00 截图判定（3 张；新发现 `/api/issues` 同样不吃 scope → L10 扩展）
- [x] t5 CU-01 重跑（补上标准 1 与标准 5 两处零断言缺口：度量卡两半 + 趋势文案三分支）
- [x] t6 CU-01 变异 + 截图判定（4 个变异全 FAIL；新发现真缺陷 01-7：「质量运行」卡片与运行页列表不同源，已修 + 两半断言 + 反向验证）
- [x] t7 CU-02 重跑（14 契约测试 + render_all + smoke；行号再漂移一轮；02-3/02-4 无既有反向验证，02-2/02-5/02-6 的反向验证留给 t8）
- [x] t8 CU-02 变异 + 截图判定（5 个变异全 FAIL；其中 M2/M3/M4 首跑绿 → 补三处断言空洞：requestCount 调用计数、fingerprint guard 断言、详情 loc 切片断言）
- [x] t9 CU-03 重跑（gate_api_test 6 用例 PASS + render_all + **补上 smoke 侧 gate 页零断言缺口**：25 条真实条件 vs 夹具 1 条，新增裕度单元格计数 / 语言段序列 / badge unknown 计数 / 无 pass badge / 无 em-dash / meta 4 项 / 最差单文件）
- [x] t10 CU-03 变异 + 截图判定（5 个变异全 FAIL：gateBand 回退 / 合并跨语言行 / 无数据当 0 / banner unknown 当 passed / 删裕度列；三张图 0 failed 且逐区块判定入 iter-03；**抓到 harness 真问题：`TestZZDumpShotsPayloads` 会覆盖 `/tmp/shots-payloads.json`**，已写入规避顺序）
- [x] t11 CU-04 就绪性判定：**已就绪**（开发 goal `mv9qakv4-an3tio` 已归档 complete 8/8；`iter-04` 有「截图判定」+「CU-04 收口」两节且末句 `CU-04 closed`；CU-04 的 writer 已不在 `.pi/goals/active_goal_*`）→ t12 走纯验证分支
- [x] t12 CU-04 验证（就绪分支，纯验证）：重跑 04-1 走 `smoke.mjs` L3 断言、04-4 `grep -c 'style="'` = 5（全数据驱动）、04-5 facet 两侧断言、04-6 6 条规则文件在位；**补两处零断言缺口**——04-2 趋势刻度（`render_all.mjs` 新增活动页趋势块）与 04-3 排序箭头（`thStates` 同时抓字形）；7 个变异全 FAIL；9 张图 `/tmp/shots/metricGate-1792090315`（0 failed，峰值 378 真实值）判定写入 `iter-04` 新增「t12 独立验证复拍」段
- [x] t13 跨屏一致性验证（X-1..X-10 十条重跑 + 14 次变异全 FAIL；**发现并修复 X-1 的自指断言空洞**——路由往返遍历 `ROUTE_KEYS` 自身，删掉任意一行仍全绿，实测 12 行逐一删除 12 次全 ok；新增 `EXPECTED_ROUTE_PARAMS`/`EXPECTED_CALL_PARAMS` 独立副本后 14 次全 FAIL）
- [x] t14 收口：`fast` 全绿（`run #1088`，`0 errors / 13 warnings`，gate PASSED，`go test ./internal/cockpit/...` 6.738s）+ `check_path('internal/cockpit/web', diff_only:true)` → `PASS files=3 errors=0 warnings=12 hidden=1`（= 基线，未超）+ 补上 V-1（源码窗口锚点/截断断言，4 次变异全 FAIL）

---

## 收口（t14，2026-10-16）

### 收尾验证（原文）
```
bash scripts/cockpit-observe.sh fast
  == node --check ==
  == render_all … == 9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok
  == live smoke … == REQUEST /api/dashboard … (全部命中)
  == go build == （无输出）
  == go test ./internal/cockpit/... == ok metricgate/internal/cockpit 6.738s
  == gate == summary: 0 errors, 13 warnings, 0 notes, 413 metric snapshots
             run #1088 stored in .metricgate/history.db
             gate: PASSED (fail_on=error)
  real 0m51.899s
```
`check_path({path:'internal/cockpit/web', diff_only:true})` → `{"verdict":"PASS","files":3,"errors":0,"warnings":12,"hidden":1}`，12 条：`app.js` 14 / 275 / 368×2 / 436 / 1341×2 / 2201 / 2354×2 / 2486 + `render_all.mjs:12`。**与基线（app.js 12 + render_all.mjs 1 = 13）持平，未新增**（差异只是 diff 口径下 render_all 的那 1 条被 hidden 计入）。

### 本 goal 交付的修复（每条都有反向验证）
| # | 缺口 | 性质 | 修法 | 反向验证 |
| --- | --- | --- | --- | --- |
| 01-7 | 「质量运行」卡片读 `trend.runs`、运行页列表读 `runs.length` → 同屏两个数字（全局 scope 实测 20 vs 46） | **真缺陷** | `metricCards` 改走 `runsInScope(s)`；归档态 note 同步改 | render_all 6 条 + smoke 1 条 |
| V-1 | 源码窗口的 `is-anchor` / `source-anchor-note` / `· 已截断` 三处无断言 | 零断言 | `render_all.mjs` 新增源码窗口块（锚点计数 === 1、note 存在、截断两侧对称） | 4 次变异全 FAIL |
| X-1 | 路由往返遍历 `ROUTE_KEYS` 自身 → 删表里任意一行仍全绿（实测 12 行逐一删 12 次全 ok） | **恒真断言** | 新增独立副本 `EXPECTED_ROUTE_PARAMS` / `EXPECTED_CALL_PARAMS` + 双向比对 + 逐行往返 | 14 次变异全 FAIL |
| 00-2 / 00-4 | `:root` 外颜色字面量、`workspaceNav` 只有手工命令，无测试 | 零断言 | 新建 `TestNoColourLiteralsOutsideTokens` / `TestShellHasNoSecondNav` | 各 1 次变异 FAIL |
| 01-1 / 01-5 | 度量卡只断言「至少存在一张」；趋势文案三分支无断言 | 零断言 | render_all + smoke 两半按载荷条数逐卡校验 grade 框与阈值行；趋势文案三 scope 正向断言 | 2 次变异 FAIL |
| 04-2 / 04-3 | 活动页趋势刻度、规则表排序箭头无断言 | 零断言 | `render_all.mjs` 新增活动页趋势块与 `thStates` 字形断言 | 2 次变异 FAIL |

### 契约就地订正（口径与载荷不符，写回契约）
- `iter-03` 标准 3 原写「L6：Go 载荷没有 runId」→ **错**，`GateView` 有 `RunID`/`Commit`/`StartedAt`（`internal/cockpit/gate_api.go:88-90`），缺的是 `/api/measures`。已在契约里就地订正并写明理由。
- `iter-03` 原边界「不改 Go 侧条件投影」被本 goal 所破（加 `GateCondition.Language`），已写明理由。
- `iter-02` 验证口径「warnings ≤7」订正为「不新增」（基线 12）。

### 遗留（不属本 goal 范围，未修）
后端数据模型/端点类 **L6 / L8 / L9 / L10 / L11**（见上表），以及需决策项：A–E 评级算法口径（parity §4.3）、`.metricgate.yaml` 闸门绑定。以上均未伪造、未绕过，只记录。

### 结论
CU-00..CU-04 共 41 条契约标准 + 10 条跨屏项，**全部有断言、有重跑、有结论**；其中 7 处零断言/恒真断言已当场修掉并补反向验证；2 处契约写错已就地订正；5 项后端缺字段按纪律只记遗留。

