# CU 系列 · 五个 goal 契约（可直接粘进 `/goal`）

来源：原 goal `mv8sv3z4-g9daec`（paused）的 task-0..task-7，经 goal `mv97sv26-rrxp4j`
（CU-00 收口）跑完 task-0..6 后**CU-00 已 closed**。以下每段都是**自洽目标契约**：
Objective / Success criteria / Boundaries / Constraints / Verification / If blocked。

共同约束（每段都含，免得单独跑时丢）：零构建 4 文件 `go:embed`；`app.js` 的
`//! S1 TOKENS .. S8 BOOT` 顺序固定、`addEventListener` 只在 S8、事件走 ACTIONS 委派、
`html\`\` 默认转义、片段走 `raw()/join()/list()`；UI 文案中文、`index.html` 保持 `lang="zh-CN"`；
AGENTS.md 闸门循环强制（写前 `get_constraints`/`find_symbols`，每段产出后 `check_code`，
收尾 `check_path`，`.metricgate/mcp-calls.jsonl` 不删不绕）；Go 改动后 `go build ./...` 再
`go test ./internal/<包>/...`；LGPL 纪律：不搬 sonarqube-webapp 代码与 l10n 文案。
共同验证入口：`bash scripts/cockpit-observe.sh fast`。
If blocked（共同默认）：停止并问用户。

---

## CU-00（父 · 已 closed，可作其它四段的历史证据）

**Objective**：收口 cockpit 外壳/令牌/Issues 列表（原 task-0..4）的视觉证据，并修掉三个会让
其它四个 goal 截图失真的前端缺陷。

**Success criteria**
1. `fileGroup` 头（app.js:663）与 `.issue-detail-loc`（939）都走 `fileBase()`，完整路径留在 `title`（L1）。
2. `facetPanel`（1468）计数来自全量、点击才收窄；global scope 下左栏计数与主列表一致（L2）。
3. `activityEvents`（1524）删掉 `|| dashboard?.runs` 兜底（L3）。
4. `/tmp/shots/v4` 已有 6 张图逐张判定并写入 `docs/iterations/cockpit-ui/iter-00-shell-and-tokens.md` 末尾。
5. `cockpit-observe.sh fast` 全绿；令牌态守住：121 declared / 0 未声明、`:root`(1–214) 外颜色字面量 0。

**Boundaries**：in scope = `internal/cockpit/web/{app.js,style.css,index.html,smoke.mjs,render_all.mjs}`
+ 本迭代文档。out of scope = 后端载荷/端点、其余四屏的视觉重构（CU-01..04）。

**状态（CU-00 closed，2026-10-15）**：5 条 success criteria 全部达成。L1/L2 由
`/tmp/shots/metricGate-1792064021/issues-list-global.png`、`issues-list-unassigned.png` 证，
L3 由 `activity-global.png` 证（判定小节见 `iter-00-shell-and-tokens.md` 的「重拍复核（task-5）」与
「CU-00 收口（task-6）」两节）。`fast` 全绿、`check_path(diff_only)` = PASS errors=0。

**Verification contract**：`fast` 全绿（含 13 个契约测试）+ 上面 4 项 grep/截图证据 +
`check_code(app.js)` 无 error 且无**新增** warning。「warnings ≤7」这一条最终没有达成：12 条里
`set`/`api`/`fetchIssues`/`summarizeCalls`/`showCall`/`boot`/`paintHeader` 的复杂度与依赖项
都是 CU-00 之前就存在的，本 goal 不靠拆函数凑数（拆 `summarizeCalls`/`showCall`/`boot` 要连同
S1–S8 分层契约一起评审，属 CU-04）。

---

## CU-01 · Summary / 项目概览（已 closed，2026-10-15）

**Objective**：把 `VIEWS.overview` / `workspaceOverview` / `metricCards` /
`metricTrendPanel` / `measureCard` 对齐到 SonarQube 的 measure tile + 结论优先布局。

**Success criteria**：见 `iter-01-summary.md` 第「成功标准」四条 —— 评级块标签/大字数值/A–E 方框/
阈值带四要素同单元；概览取数只走 `scopeAggregate(s)`；三区主次清晰（单卡 ≤8 色）；
`overview` × 3 scope 截图对 `/tmp/sq/sq-summary.png` 判定。

**Boundaries**：不改后端 measures 载荷；不新增 webspec 外的视图；A–E 算法口径属 parity §4.3，本 goal 只投影。

**状态（CU-01 closed，2026-10-15）**：四条成功标准全部达成并可复算。
截图证据 = `/tmp/shots/metricGate-1792077462/` 三张（overview-global / overview-project /
overview-unassigned），判定小节见 `iter-01-summary.md`「截图判定（task-7）」与「CU-01 收口（task-8）」。
收口实测：`fast` 全绿（go build 干净、`go test ./internal/cockpit/...` ok 0 失败、闸门
`0 errors, 13 warnings, 413 metric snapshots` / `run #994` / `gate: PASSED`）；
`check_path(diff_only)` = `PASS errors=0 warnings=12 hidden=1`，warning 合计正好等于基线 13，无新增。

**Verification contract**：`fast` 全绿 + allow-list 断言（`s.dashboard` 在 overview 段内恰好命中 3 处且各自带注释，反向证明有效）+ 3 张图对 `/tmp/sq/sq-summary.png` 判定。

**状态（CU-01 closed，2026-10-15，含深夜复核）**：五条全部达成并可复算。
截图证据 = `/tmp/shots/metricGate-1792077462/` 三张（overview-global / overview-project /
overview-unassigned），判定小节见 `iter-01-summary.md`「截图判定（task-7）」与「CU-01 收口（task-8）」。
收口实测：`fast` 全绿（go build 干净、`go test ./internal/cockpit/...` ok 0 失败、闸门
`0 errors, 13 warnings, 413 metric snapshots` / `run #999` / `gate: PASSED`）；
`check_path(diff_only)` = `PASS errors=0 warnings=12 hidden=1`，12 条与下午逐条同源，无新增。

allow-list 的**两半各守一端**（task-3 契约要求 render_all + smoke 各一份，首轮只落了 render_all，
深夜补齐）：`render_all.mjs:311-350` 读 app.js **源码**（守「有没有多出第 4 处读」）；
`smoke.mjs:413-440` 读**真实载荷**（守「`ProjectSummary` 是否已长出 `accepted`/`metricTrend`/`summary`」）。
后者不能用前者代替：render_all 只看手写夹具，Go 结构体加字段时夹具不会跟着变。两半的反向验证均按预期 FAIL。

**后端遗留（CU-01 判定时新增，前端不可自修）**：

- **L8**：项目级载荷缺 `accepted` / `metricTrend` / `summary.skipped`。`ProjectSummary`
  （internal/cockpit/dashboard.go:62-77）只有 `runs / archivedRuns / trend / archivedTrend /
  findings / archivedFindings`，这三项只存在于顶层 `Dashboard`（:52/:46/:42）。概览段这三处
  继续读顶层且每处带注释；要消除只能由后端补项目级字段。
- **L9**：`dashboard.calls` 缺归属。40 条调用 **0 条带 `projectRoot`**（task-7 截图判定实测），
  项目 scope 的「调用总数」「验证拦截率」两张卡结构性为空。修点在记录侧（写调用日志时带上项目路径），
  前端补不出项目级调用，配合账本同理。


---

## CU-02 · Issue 详情四 tab + 代码窗口

**Objective**：四 tab 与代码窗口对齐 `/tmp/sq/sq-issue-detail.png`，并且**不臆造后端没有的字段**。

**Success criteria**：见 `iter-02-issue-detail.md` 五条 —— L1 路径收口、`issueTab` 进 `ROUTE_KEYS`
（刷新/分享不丢 tab）、代码窗口行号列对齐 + 锚点高亮 + `truncated` 可见 + 隐私一行、
927–1080 段无 inline style、4 张 detail 图逐区块判定。
effort/assignee/tags 无载荷依据 → 按 D3 不造假；要显示就显示可解释的代价档位并标明推断。

**Boundaries**：不改 `/api/source` 隐私边界（`sourceWindowLines = 15`、`sourceMaxBytes = 512*1024`、
只读被 finding 点名的文件）；不引 highlight.js；不搬 l10n 文案。

**Verification contract**：`fast` 全绿 + `smoke.mjs` 的 `issue.select` + `fetchSource` 往返后断言 `source-line` 通过 +
4 张图 + warnings 不新增。

**状态（CU-02 closed，2026-10-15）**：五条全部达成。契约里的旧锚点已失效，现值：`issueDetail`(1112)
`ISSUE_TABS`(1178) `issueWhere`(1195) `issueActivity`(1244) `ensureSource`(544)。
最大实质改动是**详情 pane 提为列表右侧第三栏**（`.issues-layout.has-detail`），锚点行行内重复
问题消息已采纳；**履历 tab 去表格**（载荷无逐次时间戳，画时间线就是造假）。判定表见
`iter-02-issue-detail.md`「收口」段。

---

## CU-03 · 质量门禁页

**Objective**：`VIEWS.gate`(1565) + `gateBanner`(770) 改成结论优先：状态块在上、条件表作证据、
每行给裕度，并区分「无数据」与「通过」。

**Success criteria**：见 `iter-03-gate.md` 五条。**契约里两处错已在收口时就地订正**：（一）L6 写「Go 载荷没有
`runId`」是错的 —— `internal/cockpit/gate_api.go` 的 `GateView` 本来就有 `RunID`/`Commit`/`StartedAt`，缺的是
`/api/measures`；所以 `gateMeta` 在 gate 页把四个事实收成一行。（二）「不改 Go 侧条件投影」被本 goal 破了：
`GateCondition` 新增 `Language` 字段 —— 三语言 policy 吐 25 行同度量而载荷无一列说明哪行管哪个文件，
这是缺字段不是展示问题；改动局限在投影，不碰 `gateVerdict` 的判定语义。

**Boundaries**：**收口时已破** —— 见下「Success criteria」里的（二）。原意是不动 Go 侧投影，实际
为了把「哪行管哪个文件」说清楚而给 `GateCondition` 加了 `Language`；判定语义（`gateVerdict`）未动。
阈值来源仍只显 `c.source`，前端不推断。

**Verification contract**：`fast` 全绿 + `TestViewTableMatchesSpec`/`TestSidebarViewsExist` 通过 +
`gate` × 3 scope 截图（此前三屏从未拍过）。

**状态（CU-03 closed，2026-10-15）**：五条全部达成（goal `mv9lfuxc-km80w6`，8/8，审计被用户跳过）。现锚点：
`gateBanner`(891) `gateHeadroom`(917) `gateMeta`(945) `gateConditionRows`(1834) `VIEWS.gate`(1862)。三张图
`/tmp/shots/metricGate-1792077383/`（1600x2700，需 `MG_SHOT_HEIGHT`；默认 1100px 看不见注脚）。实测：
`go test ./internal/cockpit/...` ok 16.950s；新增 `gate_api_test.go` 5 用例 + 三个变异反向验证全被抓到。
**三条遗留**：L9 `gateVerdict` 完全不看 `metric_snapshots`（PASSED 与表内「发现问题」并存是真实语义）；
`RunMeasures` 只 `GROUP BY metric` 所以实测值不分语言；**门禁页的 scope 切换是无效控件**（`/api/gate` 不接受
scope，三张图只差面包屑）。判定表见 `iter-03-gate.md`（169 行）。

---

## CU-04 · Activity / Rules / Policy + UI 评审规则进闸门（终批）

> **状态（CU-04 closed，2026-10-16）**：7 条成功标准全部达成。证据截图在
> `/tmp/shots/metricGate-1792082899`（activity × 3 scope）与 `/tmp/shots/metricGate-1792082995`
> （rules × 3 + policy × 3），两批 `0 failed`、harness title 全 `err=` 空；`bash scripts/cockpit-observe.sh fast`
> 收口一次为 `0 errors, 13 warnings, 413 metric snapshots` / run #1003 / `gate: PASSED`；
> `check_path('internal/cockpit/web', diff_only:true)` = `verdict=PASS errors=0 warnings=12 files=3 hidden=1`
> （12 条 warning 与 CU-01 收口时同一基线，无新增）。
> 收口时发现的后端缺口 **L10**：`internal/cockpit/policy_api.go:237 policyKeys()` 写死 `Value: ""`，
> 使 Rules 页与 Policy 页 EFFECTIVE 表「取值」列恒为 `—`；前端拿到空串别无选择，故判为后端投影
> 缺口，不在本段前端范围内修。

**Objective**：三页对齐，并新建 `docs/constraints/cockpit-ui-review.md`，最后做全树收尾。

**Success criteria**：见 `iter-04-activity-rules-policy.md` 七条 —— L3 生效、趋势图有刻度、
Rules 变目录（reason + 出处，缺 reason 显示 `—` 并说明是缺口）、清掉 1437–1438 的装饰性 inline style、
facet 口径推广、UI 评审规则成文、9 张截图判定。

**Boundaries**：不改 `/api/policy` 读写协议与 same-origin；本 goal 只写规则文件，不改 `.metricgate.yaml`
的门禁绑定。

**Verification contract**：`fast` 全绿 + `grep -c 'style="' app.js` 由 7 降到 5 + 9 张图 +
终批 `go build ./...` 干净、`go test ./internal/cockpit/...` 0 失败、
`check_path('internal/cockpit/web', diff_only:true)` 无 error 级 finding。

## CU-00 closed 后的并行结论（task-6 写入）

**CU-01..04 现在可以独立开跑，但必须先声明「谁写」。** 事实依据：

- CU-00 的所有改动都在 `internal/cockpit/web/{app.js,style.css,index.html,smoke.mjs,render_all.mjs}`
  与 `docs/iterations/cockpit-ui/`，已合入工作树、`fast` 全绿、`check_path(diff_only)` PASS errors=0，
  闸门 PASSED (fail_on=error)。前端不再有任何会让截图失真的已知缺陷。
- 观测入口已共享且串行化：`scripts/cockpit-observe.sh shots "" <名字,...>` 走 PID 目录锁，
  `cdp_shoot.mjs` 一批一个 Chrome、单张 5–11s。四个 goal 可以**同时读**截图，
  但不能**同时拍**（同一 harness 目录 `/tmp/shots/` 会被覆盖）。
- 四个子 goal 的落点是不同视图（CU-01 Summary、CU-02 Issue 详情、CU-03 门禁、CU-04 活动/规则/配置
  + UI 评审规则），但**共用同一份 app.js/style.css**，所以「一个 goal 一个 managed worktree」
  不是可选项而是默认；同树并行会让 CU-00 修复的三个缺陷以新的形态复发（并发写同一段模板）。
- 每个 goal 的收益顺序：CU-02（详情四 tab + 代码窗，改动最局部，最容易先拿到一张可采纳的图）
  → CU-03 → CU-01 → CU-04（CU-04 依赖前三者提出的规则，必须最后批）。

## CU-02 / CU-03 closed 后的收尾状态

**CU-00 / CU-01 / CU-02 / CU-03 均已 closed，只剩 CU-04**（Activity / Rules / Policy + UI 评审规则进闸门）。
CU-04 接手的树：`app.js` 2488 行 / `style.css` 1373 行 / `render_all.mjs` 401 行 / `smoke.mjs` 676 行；
`check_path('internal/cockpit/web', diff_only:true)` 基线是 **PASS errors=0 warnings=12**（不是 ≤7）。
`style="` 全文件仍 7 处，其中 `app.js:1658`/`1659`（grep 实测于 2488 行那棵树）是 `policyPane` 的装饰性
margin/padding/border —— 那就是 CU-04 要清的（契约原文「降到 5」即指这两条）；其余 5 处在 782/799–802，
是数据驱动的条宽柱高，合理保留。另三个已知坑：① 拍图前必须重导载荷（`python3 /tmp/shots/dump_payloads.py`）+ 重跑 `mk.py`，
否则拍到旧快照（CU-03 就因此白拍一批）；② `html``` 模板里不能出现反引号（`node --check` 报不出，
render_all 加载时才炸）；③ 同树确实有第二个 writer（CU-03 收口期间 `app.js` 自行变了两行），任何结论
前重跑一次 grep 确认落点。

## CU-01 closed 后的可开跑结论（task-8 写入）

CU-01 已 closed，CU-02 / CU-03 / CU-04 **当时即可开跑**（现只剩 CU-04），规则与 CU-00 closed 时相同：

- 一份工作树同时只允许一个 writer（闸门有状态，`check_path` 会写 `history.db`）。要真并行就
  「一个 goal 一个 managed worktree」；四个子 goal 共用同一份 `app.js`/`style.css`，串行是默认。
- 观测入口共享且已串行化：`bash scripts/cockpit-observe.sh shots "" <名字,...>`（PID 目录锁，
  `cdp_shoot.mjs` 一批一个 Chrome，单张 5–20s）。可以同时读截图，不能同时拍
  （`/tmp/shots/` harness 目录会被覆盖）。
- 建议顺序不变：**CU-02 → CU-03 → CU-01(已 closed) → CU-04**。CU-04 依赖前三个浮出来的规则，
  且 `summarizeCalls`/`showCall`/`boot`/`paintHeader` 的复杂度 warning 是 CU-00 之前就有的既有项，
  要在 CU-04 连同 S1–S8 分层契约一起评审，不拆散语义凑数字。
- CU-01 留下了两条**后端**遗留（前端不可自修）：L8 项目级缺 `accepted`/`metricTrend`/`summary.skipped`；
  L9 `dashboard.calls` 无 `projectRoot` 归属（40 条里 0 条），项目 scope 的调用两张卡结构性为空。
  CU-02/03/04 若在同一处撞上，直接复用这条记录，不要再排查一遍。

## CU-04 closed 后的结论（task-7 写入）

`docs/constraints/cockpit-ui-review.md` 已成文，六条规则全部带可执行判定命令并已实测。自 CU-04 起它是
`internal/cockpit/web/` 的评审依据：任何新的颜色类名要按规则 1 数，任何新的 `style="` 要按规则 5 过一遍
插值检查，任何新的 `s.dashboard.<field>` 读取要按 CU-01 的 allow-list 记 L 编号。

五段迭代全部 closed，剩下的都是**后端投影缺口**，前端不占位：

- **L6** `/api/issues` 行级缺 `runId`（Go 载荷加字段）；
- **L7** `/api/issues` 的 facet 口径不对称（`facetsOf(candidates)` 在过滤前数 severities/statuses/sources，
  而 `countRules(filtered)` 在过滤后数 rules，`internal/store/issues_query.go:178` 起）；
- **L8** `/api/dashboard` 的 `ProjectSummary` 只有 runs/archivedRuns/trend/archivedTrend/findings/archivedFindings，
  summary(:42)/accepted(:52)/metricTrend(:46) 只在顶层；
- **L9** `/api/dashboard` 的 calls 无 `projectRoot` 归属 → 项目 scope 的调用总数恒 0；
- **L10** `/api/policy` 的 `keys[].value` 恒为空串（`policyKeys` 写死 `Value: ""`，`internal/cockpit/policy_api.go:237`）。
