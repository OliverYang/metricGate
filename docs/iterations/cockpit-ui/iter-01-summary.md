# 迭代 CU-01 · Summary / 项目概览页对齐（goal）

父：CU-00（`iter-00-shell-and-tokens.md`）｜依赖：CU-00 L2（facet 计数口径）

## 为什么做这个

概览页是 SonarQube 的「一眼看懂项目健康度」入口，我们现在的三处不像：
度量卡网格有 A–E 方框但**没有把评级和阈值带绑在同一个视觉单元上**；
闸门循环 `phaseBar` 与 `trendColumn` 挤在同一 layout-grid 里，主次不分；
让步账本 `acceptedEntry` 是纯文字列表，SonarQube 里同类「谁在什么时候放行了什么」
是以条目 + 过期时间呈现的。用户 m00001 的原话是「UI 还是挺丑的，不保留原样式」，
这一屏是观感权重最高的一屏。

## 落点（CU-01 校准，2026-10-15 —— CU-00 之后行号全部漂移，以本节为准）

> **本节已过期（2026-10-15 深夜注）**：下表是 CU-01 开工时实测的，此后 CU-02 / CU-03 各自又让锚点
> 漂了约 +120 行。**当前权威表在 `README.md` 的 CU 总览表**（`app.js` 2488 行那棵树）。
> 本节保留不删，因为 task-0 的验收证据就是这份逐条 `grep -n` 输出，改掉就变成事后编造。

| 组件 | app.js | style.css |
|---|---|---|
| `acceptedEntry` | 844 | `.accepted-panel` 892 / `.accepted-list` 893 / `.accepted-item` 894–895 |
| `gateBanner` | 855 | `.gate-banner` 846 / `.is-passed` 859 / `.is-failed` 860 / `.is-unknown` 861 |
| `measureCard` | 868 | `.measures-grid` 864 / `.measure-grade` 868–883 / `.measure-bands` 885–887 |
| `facetRow` | 884 | — |
| `callsInScope` | 904 | — |
| `scopeAggregate` | 919 | — |
| `runsInScope` | 944 | — |
| `metricCards` | 1200 | `.metrics-grid` 794 |
| `summarizeCalls` | 1217 | — |
| `workspaceOverview` | 1365 | `.layout-grid` 802–803 / `.flow` 806 / `.trend-chart` 829 |
| `metricTrendPanel` | 1402 | — |
| `VIEWS.overview` | 1440 | — |

app.js 现 **2322 行**（CU-00 收口时 2277，其后 +45；warn 2000 / error 4000）。

## 成功标准（第 2 条口径已修正，见下方「标准 2 的可执行口径」）

1. 度量卡 = SonarQube 的 measure tile：标签 / 大字数值 / A–E 方框 / 阈值带一行；
   `bands` 与 `exceeded` 不在卡片里换行打架。
2. `scopeAggregate(s)`（919）成为概览 run/finding/trend 三类取数的唯一入口；
   `s.dashboard` 与 `projectOf(s)` 二选一的 ad-hoc 写法在概览段清除（校验口径见下）。
3. 概览三个区（评级块 / 闸门循环 / 让步账本）有清晰主次：单卡内 ≤8 个颜色（闸门纪律）。
4. 截图：`overview` 在 3 个 scope（global / `/Users/mac/Downloads/metricGate` / `""` 未归属）
   全部出图并与 `/tmp/sq/sq-summary.png` 逐区块比对，差异结论写进本文件末尾。

### 标准 2 的可执行口径（CU-01 goal 定）

原来的写法「`scopeAggregate` 成为概览唯一取数入口」+「`grep -n 's\.dashboard' app.js` 在概览段为空」
**不能按字面执行**，因为后端载荷里项目级根本没有这三个字段：
`ProjectSummary`（internal/cockpit/dashboard.go:62-77）只有
`runs / archivedRuns / trend / archivedTrend / findings / archivedFindings`，而
`Accepted`（:52）、`MetricTrend`（:46）、`Summary`（:42）只存在于顶层 `Dashboard`。
按字面做到「概览段 `s.dashboard` 归零」，只能由前端凭空造出项目级让步账本与度量实验 —— 违反 D3 不造假。

所以本迭代改为：

- **收敛部分**：run / finding / trend 三类读一律走 `scopeAggregate(s)`（919）；
  `workspaceOverview`（1365）的 series 与 `metricCards`（1200）的「质量运行」同口径；
  `projectOf(s)` 不再被概览段用来取数。
- **保留部分（带注释 + 遗留）**：3 处项目级载荷不存在的字段继续读顶层，且每处必须带一行注释：
  `metricCards` 的 `s.dashboard.summary.skipped`（1207）、
  `workspaceOverview` 的 `s.dashboard.accepted`（1371）、
  `metricTrendPanel` 的 `s.dashboard.metricTrend`（1403）。
- **新增后端遗留 L8**：项目级 accepted / metricTrend / summary.skipped 载荷缺失（详见下方遗留表）。
- **校验口径**：grep 清零改为 render_all.mjs / smoke.mjs 里的 **allow-list 断言** ——
  概览段内 `s.dashboard` 命中集合必须恰好等于上述 3 处，且各自带注释，多一处即 FAIL。

## 边界

只改上述组件与其样式。**不改**后端 measures 载荷；不新增 webspec 之外的视图；
不引 highlight.js、不引任何构建链。评级 A–E 的**算法口径**属 parity 文档 §4.3，
不在本 goal 里改阈值来源（阈值来自 policy，前端只投影）。

## 验证口径

每任务 `bash scripts/cockpit-observe.sh quick` 退出 0 + 定向证据；收尾 `fast` 一次。
标准 2 的校验用 **allow-list 断言**（render_all.mjs / smoke.mjs）而不是 grep：
概览段内 `s.dashboard` 命中集合必须恰好等于 `metricCards:1207` / `workspaceOverview:1371` /
`metricTrendPanel:1403` 三处且各自带注释，多一处即 FAIL（反向证明见 task-3）。
**常跑档 = `quick`（≈4.2s）**：`node --check` + `node render_all.mjs` + 定向 `go test ./internal/cockpit/`；
**全量档 = `fast`（仅在收尾）**：`go build ./...` + 全量 `go test ./internal/cockpit/...` + 全树闸门。

## 截图判定（task-7，2026-10-15）

批次 `/tmp/shots/metricGate-1792077462`（CDP runner，一次 Chrome 三张，锁由批自己持有）：
`overview-global` 152930 B / 20.3s、`overview-project` 149395 B / 13.0s、
`overview-unassigned` 157571 B / 13.3s，`0 failed`。三张 harness title 全部 `err=`（空串）、
`cards=0 detail=0 groups=0`，即 fetcher 未报错且 `state.loading` 已收敛后才截图。
对照基准 `/tmp/sq/sq-summary.png`。

> 早一版批次 `metricGate-1792076319` **作废重拍**：拍摄期间另一个 agent 的
> `TestZZDumpShotsPayloads` 刷新了 `/tmp/shots-payloads.json`（stat 22:55:56 → 23:06:37），
> 导致图与后续复算的数字来自两个不同载荷快照。判定小节只引用单一快照。

### 逐张判定

| 图 | 判定 | 依据（可复算，非观感） |
|---|---|---|
| `overview-global` | **采纳** | measure tile 恒 **9** 张（`cyclomatic-complexity C 13 · warn 10 · err 15 · 6 个位置超阈` … `boolean-params A 0 · warn 2 · err 4`），与 sq-summary 的「指标名 + 大字值 + 评级色块 + 阈值」四元组一一对应；4 张 metric-card 与 `summarizeCalls(callsInScope(s))` 离线复算逐字相符：`调用总数 40`（2 个会话 · 10/10 17:48:03 — 20:50:00）、`验证拦截率 67%`（12 / 18 次验证发现问题）、`工具错误 5`、`质量运行 20 · 全部项目与未归属记录`；趋势口径行如实写「未归属序列 · last 20 run(s), unassigned runs」 |
| `overview-project` | **采纳** | tile 同为 9 张；`质量运行 20 · metricGate · 带项目身份的运行`；趋势行「趋势：metricGate 的运行」，**不冒充分配合全量**（`ProjectSummary.trend` 是单项目序列）；`调用总数 0 · 尚无可归属的调用记录`、`验证拦截率 — · 暂无验证调用`（不是 0%，避免把「无数据」画成「拦截率 0%」） |
| `overview-unassigned` | **采纳** | `调用总数 40`、`验证拦截率 67%`、`质量运行 20 · 未归属运行`；趋势行带括注「服务端只发未归属序列，不发送合并全量」，即标准 5 的如实口径 |

### 逐区块对 sq-summary 的差异

| sq-summary 区块 | 我们的实现 | 结论 |
|---|---|---|
| Quality Gate 大横幅（Passed + 条件列表） | `gateBanner` 置于页首 | 对齐 |
| New Code / Overall Code 双 tab | 未实现 | **有意不做**：我们没有 New Code period，服务端也不发，硬造 tab 违反 D3 不造假。已记 L8 |
| measure 卡网格（评级色块 + 环比箭头） | 9 张 tile：标签 / A–E 方框 / 大字值 / 阈值带 + 超阈计数 | 对齐；**环比箭头不做**：需历史快照差，本 goal 只投影当前值 |
| 下方分布表 | 改为 `metricTrendPanel`（p50/p90/max 随阈值变化） | 增益项，parity §3.1 明确这是我们有而 SonarQube 没有的 |
| CoB / Overall Code 分区标题 | 三区改为「评级块 / 闸门循环 / 让步账本」 | 收窄但主次清晰，符合标准 3 |
| Effort / Assignee / Tags / ALM 徽章 | 不渲染 | payload 无对应字段，D3 不造假 |

### 复算证据（不依赖观感）

- measure tile 恒 9 张、metric-card 恒 4 张，三 scope 一致（离线渲染 `VIEWS.overview` 逐字比对）。
- `:root`(1–214) 外颜色字面量 **0**：`awk 'NR>214' style.css \| grep -E '#[0-9a-fA-F]{3,8}\|rgba?\('` 无输出，守住标准 3。
- measure tile 的颜色只来自 `grade-A..grade-E` 五个类，单卡最多 1 个颜色类（评级方框），≤8 的上限远未触顶。
- `metricCards(s)` 的取数是 `summarizeCalls(callsInScope(s))`，不是顶层 `s.dashboard.summary`；
  `s.dashboard` 在概览段只剩 3 处 allow-list 命中（skipped / accepted / metricTrend），每处均带注释，
  与标准 2 的可执行口径一致。

### 拍判过程中发现并记录的遗留

- **L9（新）**：`dashboard.calls` 只在记录侧写入 `projectRoot` 时才带项目身份。本批载荷 40 条调用
  **0 条带 `projectRoot`**，因此项目 scope 的调用两张卡结构性为空（`0 · 尚无可归属的调用记录`）。
  这不是渲染缺陷（`callsInScope` 过滤正确），而是**归属缺口**：要修在记录侧（调用日志写入时带上项目路径），
  前端补不出项目级调用。配合账本同理。记入 goal-contracts 的后端遗留表。

## CU-01 收口（task-8，2026-10-15）

> **本节数字是 2026-10-15 下午 15:37 快照。** 深夜 CU-02 / CU-03 收口后又重跑过一次同口径，
> 结论未变但 run 号与 file-length 会漂（每次编辑 app.js 都会变），所以数字不作为 invariants 引用。

对比利与闸门状态如下（全量档 `fast` 只跑这一次）：

- `node --check internal/cockpit/web/app.js` ok；`node render_all.mjs` →
  `render_all: 9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok`。
- `go build ./...` 干净；`go test ./internal/cockpit/...` → `ok metricgate/internal/cockpit (cached)`，0 失败。
- 闸门（全树）`summary: 0 errors, 13 warnings, 0 notes, 413 metric snapshots`，
  `run #994`，`gate: PASSED (fail_on=error)`。
- `check_path('internal/cockpit/web', diff_only:true)` → `verdict=PASS errors=0 warnings=12 hidden=1`。
  warning 合计 13，**正好等于基线**，无新增。清单：app.js 9 条
  （unknown-dependency ./spec.json @14、fan-out "set" 11 @268、cyclomatic "api" 12 @361、
  fan-out "api" 14 @361、cyclomatic "fetchIssues" 13 @429、cognitive "summarizeCalls" 16 @1330、
  cyclomatic "summarizeCalls" 11 @1330、cyclomatic "showCall" 13 @2130、fan-out "boot" 17 @2283、
  function-length "boot" 52 @2283、cyclomatic "paintHeader" 12 @2415）+ render_all.mjs 1 条
  （unknown-dependency ./app.js @12）+ hidden 1 条。
  这些都是 **CU-00 收口时就存在的既有项**，CU-01 没有引入新 finding；`summarizeCalls` /
  `showCall` / `boot` / `paintHeader` 的拆分属于 CU-04（要和 S1–S8 分层契约一起评，
  否则会为凑数字把语义切碎），本 goal 不动。

### 结论

CU-01 的四条成功标准均已满足并可复算：

1. `measureCard` = 标签 / A–E 方框 / 大字数值 / 阈值带（+ 超阈计数）同单元，9 张 tile 三 scope 一致。
2. run/finding/trend 三类读走 `scopeAggregate`；`s.dashboard` 在概览段只剩 3 处 allow-list 命中
   且每处带注释；L8/L9 两条后端遗留已记入 `goal-contracts.md`。
3. 三区主次清晰；`:root`(1–214) 外颜色字面量 0；单卡颜色类 ≤1（评级方框）。
4. `acceptedEntry` 条目化，STALE/ACCEPTED 徽标保留。
5. 趋势口径如实：global/未归属写明「未归属序列」，未归属 scope 带括注说明服务端不发合并序列。

**CU-01 closed。** 页内无已知会让截图失真的前端缺陷；剩余问题都是后端载荷缺口（L8/L9），
按契约不在本 goal 修。

### 深夜复核（CU-02 / CU-03 收口后，同一口径重跑）

三个 goal 依次动过 `app.js`，所以 CU-01 的收口数字需要重验而不是沿用。同口径结果：

- `bash scripts/cockpit-observe.sh fast` → `render_all: 9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok`；
  `go build ./...` 干净；`go test ./internal/cockpit/... -count=1` → `ok 15.120s`（0 失败）；
  闸门 `summary: 0 errors, 13 warnings`，`run #999`，`gate: PASSED (fail_on=error)`。
- `check_path('internal/cockpit/web', diff_only:true)` → `verdict=PASS errors=0 warnings=12 hidden=1`，
  12 条 finding 与下午**逐条同源**（app.js 14/268/361×2/429/1330×2/2130/2283×2/2415 + render_all.mjs 12）。
- 新增变化只有一条：`get_constraints` 报的 file-length 由 2488 → **2489**（`>2000` 的既有 warning，非新增）。

**结论：CU-01 的五条标准在两次收口之间没有被后续 goal 破坏**，L8/L9 仍未修（后端载荷，按契约不在本 goal）。

### 标准 2 的 smoke 等价检查（本轮补齐）

task-3 的契约要求「render_all.mjs 新增断言 + smoke.mjs 加等价检查」，但落地时只做了 render_all 那一半 ——
CU-01 收口时漏了。本轮补上，且**两半守的不是同一件事**（这一点在写之前并不明显）：

- `render_all.mjs` 读 **app.js 源码**，守「overview 段里有没有多出第 4 处 `s.dashboard` 读」。
- `smoke.mjs` 读 **真实载荷**，守「`ProjectSummary` 是不是已经长出 `accepted`/`metricTrend`/`summary`」。
  后者不能用前者代替：render_all 只看手写夹具，Go 结构体加字段时夹具不会跟着变，检查会继续绿着而缺陷已经回来。

**一个自己造出来的假失败（值得记下）**：首版断言写的是 `if (!("accepted" in dash)) fail(...)`，
直接挂在真实载荷上 —— 因为 `Accepted []accept.Entry \`json:"accepted,omitempty"\``（`internal/cockpit/dashboard.go:52`）
和 `MetricTrend ... omitempty`（:46）在空账本时**根本不发这个键**，而 `Summary` 无 `omitempty` 所以一直在。
「键缺失」是正常状态而非缺陷；断言已改为只查「project 记录是否长出了这三个字段」+「顶层 summary 是否还在」。
教训：写「断言某字段存在」之前先看 Go tag，`omitempty` 会让存在性断言在空数据上误报。

反向验证两半都做了：源码侧塞入第 4 处 `s.dashboard` 读 → render_all 报
`app.js reads [...] off s.dashboard, expected exactly [...]` + `app.js:1316 reads s.dashboard with no L8 comment above it`；
删掉第 3 处的 L8 注释 → 报 `app.js:1314 reads s.dashboard with no L8 comment above it`；
载荷侧给 `projects[0]` 加 `accepted`/`metricTrend` → smoke 报
`ProjectSummary now carries accepted, metricTrend ... — L8 is fixed, so the top-level reads in app.js must become scope-aware`。
三次都按预期 FAIL，验后副本已删（`/tmp/rvallow`、`/tmp/rv-l8-payloads.json` 仅留作复现）。

## t6 复拍与缺陷修复（2026-10-16 01:4x，独立验证 goal `mv9rvxfs-ws5fu1`）

**批次**：`bash scripts/cockpit-observe.sh shots "" "overview,overview-global,overview-unassigned"` →
`/tmp/shots/metricGate-1792086381/`（13.3s / 7.9s / 6.9s，`batch done: 3 shots, 0 failed`，`err=` 全空，
bytes 152183 / 155594 / 160095）。拍前 `cp` 四个前端文件到 `/tmp/shots` + `python3 mk.py` + `python3 dump_payloads.py`
（载荷 `generatedAt 2026-10-15T17:45:59Z`），harness `app.js` md5 与仓内一致。

### 本轮修掉的真缺陷：「质量运行」一卡与运行页列表不同源

**症状**：概览页「质量运行」写 **6**，同一份数据在「质量运行」页写 **6 条**——同一批载荷两个数字。
**根因**：卡片读 `field(field(scopeAggregate(s), "trend"), "runs")`，列表读 `runsInScope(s).runs.length`。
这是两个查询：`trend.runs` 来自 `reportForScope`（全局 scope 下是**未归属**运行数、项目 scope 下是该项目的窗口计数），
`runs` 来自 `latestRunsScoped`（`Limit:20` 的 `ORDER BY started_at DESC`）。
`internal/cockpit/dashboard.go:117` 的 `d.Trend = st.ReportUnassigned(20)` 与 `:116` 的 `d.Runs = st.LatestUnassignedRuns(20)`
本来就是两把尺子，而 `metricCards` 用了前者的数字去描述后者的列表。
**影响面（真实浏览器口径，直连 `/api/dashboard` 实测）**：顶层未归属 runs 20 条、各项目 runs 合计 26 条，
`scopeAggregate` 去重并集 **46** 条，而旧卡片读的 `trend.runs` 是 **20** ——
全局 scope 下卡片写 20、列表写 46，差 26。这不是采样夹具的产物，是当前生产载荷上的真实分叉。
项目 scope 下 `trend.runs` 与列表恰好都是 20（两边窗口都取满），所以那一侧是**潜伏**的，
换一份数据就会分叉。
**修法**：`internal/cockpit/web/app.js` `metricCards` 改读 `runsInScope(s).runs.length`，与运行页同一函数；
`historyMode === "archived"` 时卡片 note 写「已归档运行」（原先归档态下卡片仍写活跃态文案，是同一处逻辑的第二个毛病）。
**为什么之前没有断言**：`render_all.mjs` 与 `smoke.mjs` 对 `metric-card` **零断言**（`grep -c metric-card` 两边都是 0），
所以卡片读哪个字段没人管；`iter-01:100` 的「离线复算逐字相符」是人工比对，不是断言。
**已补断言（两半）**：
- `render_all.mjs`：三 scope × 两 historyMode，卡片值必须等于 `app.runsInScope(st).runs.length`（期望值由函数算，不写死数字）。
- `smoke.mjs`：真实载荷侧同样比对，用载荷自己算期望值。
**反向验证**：把 `metricCards` 改回 `agg.trend.runs` → render_all 报 6 条
`FAIL overview: the 质量运行 card says 9 at scope=null history=active but the run list holds 2`（夹具）；
smoke 报 `FAIL overview (global): the 质量运行 card says 0 but the run list holds 1`（真实载荷，顶层 trend.runs=0 而并集有 1 条）。

### 逐张判定（对照 `/tmp/sq/sq-summary.png` 1489×924）

| 图 | 判定 | 依据 |
|---|---|---|
| `overview.png`（project scope，`/Users/mac/Downloads/metricGate`） | **采纳** | 9 张 measure tile 带真实超阈计数（`Cyclomatic complexity D 1112 · warn 10 · err 15 · 109 个位置超阈`、`File length D 298040 · 29 个位置超阈` … `Boolean parameters B 1`），与 sq-summary 的「指标名 + 大字值 + 评级色块 + 阈值」四元组对齐；「质量运行 **6** · metricGate · 带项目身份的运行」与运行页 6 条一致（修复后）；趋势图 20 根柱、口径行「趋势：metricGate 的运行」 |
| `overview-global.png` | **采纳** | 卡片 `质量运行 24 · 全部项目与未归属记录`（修复后与并集一致）；趋势口径行如实写「未归属序列 · last 20 run(s), unassigned runs」——载荷顶层 `trend` 就是未归属序列，不冒充合并全量（标准 5） |
| `overview-unassigned.png` | **采纳** | `质量运行 12 · 未归属运行`；趋势行带括注「服务端只发未归属序列，不发送合并全量」 |

**对 sq-summary 的差异表**：与 `iter-01:103-111` 的判定一致（Quality Gate 横幅对齐；New Code/Overall Code 双 tab 有意不做，记 L8；环比箭头不做；Effort/Assignee/Tags 不渲染）。本批新增一条差异：
SonarQube 的 measure tile 有「环比箭头」，我们不做；但 SonarQube 的度量卡网格是**项目级聚合值**，
我们的是**最差单文件值**（`st.RunMeasures` 只 `GROUP BY metric`），这一点在 gate 页注脚里已写明，概览页未写。

### 载荷尺寸提醒（本轮踩到的坑）

`/tmp/shots/dump_payloads.py:82-86` 会把每个项目的 `runs` **截到 6 条**（`p["runs"] = rows(p.get("runs"), 6)`）
而 `trend` 不截（仍 20 点）。所以注入版页面里 `runs` 列表与 `trend.runs` 天然不同源，
这正是上面那个缺陷在图上显形的原因；真实浏览器直连 `/api/dashboard` 时两边都是 20。
**结论**：`/tmp/shots-payloads.json` 是**采样载荷**，不能用它验证「两个字段是否相等」这类不变量——
这类断言必须走 `smoke.mjs`（打真实端点）或 `render_all.mjs`（受控夹具）。
