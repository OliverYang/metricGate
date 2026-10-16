# 迭代 CU-02 · Issue 详情四 tab + 代码窗口对齐（goal）

父：CU-00 ｜依赖：CU-00 L1（`fileBase()` 未覆盖的两处路径）
参考：`/tmp/sq/sq-issue-detail.png`（1381×1076）

## 为什么做这个

详情区是 issue 分诊的手感核心。现状已有四 tab 与代码窗口，但三处与 SonarQube 不同：
**位置 tab 的文件头**没有可复制的全路径、没有「共 N 行 / 锚点 L」的稳定一行，
**原因 tab** 的 meta grid 用 inline style；
**履历 tab** 把每次 run 摊成表格但没有状态流转时间线。

## 先讲清一件事：不要造假

SonarQube 详情有 `Effort: 15min`、`Assignee`、`tags cwe/symbolic-execution` 三样。
我们的后端载荷**没有这些字段**（`internal/store/issues_query.go:36-68` 的 IssueRow 只有
fingerprint/ruleId/severity/source/file/行列/message/firstSeen/lastSeen/occurrences/
runIds/lastRunId/status/statusReason/statusOwner/fromLedger/anchor；
`internal/cockpit/source_api.go:29-41` 的 SourceView 只有 fingerprint/file/language/
起止行/totalLines/anchorLine/anchorStart/anchorEnd/lines/truncated/reason，
**没有 project / branch**）。

决策沿用 parity 文档 **D3**：不臆造 effort 分钟数。要显示就显示**能解释的代价档位**
（由 `anchor` 精度 + 度量超标幅度 + 被放行频次推出），且必须写明是推断。
本项目边界是「只渲染服务端现有载荷」，任何需要后端加字段的项 → 另立项，不写占位假数据。

## 落点

| 组件 | app.js | 备注 |
|---|---|---|
| `issueDetail` | 1017 | 头部（eyebrow `ISSUE <fp>` / h2 消息 / `.issue-detail-loc` / actions） |
| `ISSUE_TABS` `issueTabBar` | 1083 / 1088 | 位置/原因/履历/详情 |
| `issueWhere` | 1100 | 代码窗口 + 锚点高亮 + 锚点行内消息 |
| `issueWhy` | 1128 | meta grid + 消息 |
| `issueActivity` | 1149 | 运行列表 + 状态出处（原为三列表格，见「收口」） |
| `issueMore` | 1163 | 指纹/位置/让步账本提示 |
| `ensureSource` | 532 | 只有 `where` tab 读源码：tab 是 URL 状态，恢复链接不该白读一次 |
| `ROUTE_KEYS` `open`/`tab` | 583 / 584 | `openFingerprint`、`issueTab` 进路由，表驱动无 per-field 分支 |

样式：`.issue-detail*`（`.issue-detail-title` `.issue-detail-loc` `.issue-detail-actions`）、
`.tab-bar`、`.source-view` `.source-head` `.source-lines` `.source-line.is-anchor`。

## 成功标准

1. **L1 收口**：`.issue-detail-loc` 与 file-group 头都走 `fileBase()`，完整绝对路径留在 `title`。
2. 四 tab 切换经 `ACTIONS["issue.tab"]`，`issueTab` 进 `ROUTE_KEYS`，刷新/分享后 tab 不丢
   （`encodeRoute`/`decodeRoute` 同表驱动，加字段=加一行；不得写 per-field 分支，上一版因此撞复杂度阈值）。
3. 代码窗口：行号列 + 内容列对齐不换行；锚点行为 warning-weak 底 + 3px inset；
   `truncated`/`reason` 有可见提示；隐私说明一行（只读被命中的文件、固定 ±15 行）。
4. 详情区**无装饰性 inline style**：全文件仅 7 处 `style="`，其中 927–1080 段为 0（守住不回退）；
   680/697–700 是数据驱动的条宽/柱高（`phaseBar`/`trendColumn`，合理保留），
   1437–1438 是 `policyPane` 的装饰性 margin/padding/border —— 那两行属 CU-04 清理，不在本 goal。
5. 截图：`detail-where / detail-why / detail-activity / detail-more` 四张（已出，需复核）
   对 `/tmp/sq/sq-issue-detail.png` 逐区块判定，采纳/不采纳都写结论。

## 边界

不改 `/api/source` 的隐私边界（`sourceWindowLines = 15`、`sourceMaxBytes = 512*1024`、
只允许被 finding 点名的文件、无遍历无搜索）；不加语法高亮依赖（仅关键字/注释/字符串三档）；
不搬 SonarQube 的 l10n 文案，文案中文（LGPL 纪律：只抄结构与交互语义）。

## 验证口径

`cockpit-observe.sh fast` 全绿 + `smoke.mjs:454`（issue.select + fetchSource 往返后断言 `source-line` 存在）
通过 + 4 张 detail 图 + `check_code(app.js)` warnings 不新增（原写「≤7」，CU-00 收口已订正为「不新增」，
理由见 `iter-00-shell-and-tokens.md:127`：12 条全为既有项，拆函数凑数字属 CU-04）。

## 收口（2026-10-15）

### 逐条判定

1. **L1 收口——达成**。`issueDetail`(1029)、`issueWhere`(1108)、`issueMore`(1166) 与 file-group 头(748)、
   卡片 `.issue-loc`(716) 均走 `fileBase()`，完整绝对路径只在 `title`。
2. **tab 进路由——达成**。`ROUTE_KEYS` 新增两行（`["open", …]` `["tab", …]`），`encodeRoute`/`decodeRoute`/`ensureSource`
   全表驱动，无 per-field 分支。`smoke.mjs` 新增 5 条断言：tab 切换不花源码读、tab 进 URL、粘贴链接恢复
   到对的 tab、恢复后不白读、回到 `where` 才补读。
3. **代码窗口——达成并加强**（见下「采纳」c）。
4. **详情区无装饰性 inline style——达成**。全文件仍 7 处 `style="`：765/782-785 是 `phaseBar`/`trendColumn`
   的数据驱动宽度/高度（合理），1541/1542 是 `policyPane`（CU-04）；详情区 1017–1170 段 **0 处**。
5. **四张图逐区块判定**：见下。

### 对 `/tmp/sq/sq-issue-detail.png` 的逐区块判定

| 参考图区块 | 判定 | 理由 |
|---|---|---|
| **a** tab 标签是问句（“Where is the issue?”） | **采纳语义、不改文案** | 问句式确实把 tab 从“分类”变成“提问”，但本仓文案中文且由 `ISSUE_TABS` 一处定义，改成“问题在哪？”/“为什么会报？”收益不抵改动面，且 tab 文案不是 LGPL 问题、是一致性问题。保留“位置/原因/履历/详情”。 |
| **b** 详情在列表**右侧**（三栏） | **采纳（本轮最大改动）** | 之详：代码窗口很高，堆在列表下方时，读它就把你正在对比的那几张卡片滚出屏幕——而“对比”正是分诊页唯一要干的事。实现：`VIEWS.issues` 把 `issueDetail` 从 `.issues-main` 内提到 `.issues-layout` 直接子级，有 open 时加 `has-detail` 类；`style.css:948` 三栏 `14rem / minmax(23rem,1.15fr) / minmax(21rem,1fr)`，详情栏 `position: sticky` + `max-height` 限高内滚，≤1100px 堆回单栏并解除 sticky（否则 `max-height` 会裁切而不是滚页）。不用 `:has()`：网格应由渲染 pane 的同一个状态决定，且可测。 |
| **c** 锚点行**行内**重复问题消息 | **采纳** | 新 `.source-anchor-note`（`app.js:1116`）：锚点行下方一格黄框写 `issue.message`。没有它，代码窗口里高亮了一行却要滚回头部才知道那行错在哪。只复用载荷已有的 `issue.message`，不需后端加字段。 |
| 右上 `Software qualities impacted: Maintainability Low` / `Code attribute: Consistency` | **不采纳（无数据）** | `IssueRow` 没有这两组字段（`internal/store/issues_query.go:36-68`），按 D3 不臆造。需后端先做 rule→质量属性映射，另立项。 |
| `Effort: 15min` / `Assignee` / `tags cwe` | **不采纳（无数据）** | 同上，契约里已写明。 |
| `Open in IDE` / `See all issues in this file` | **本轮不做** | 前者要后端知道 IDE 协议（无载荷）；后者是列表侧能力，属 CU-00 已定的 file-group 范围，不归本 goal。 |

### 一处反向修正：履历 tab 从表格改成叙述

原 `issueActivity` 是三列表格（运行/结论/理由），每行“结论”列都是同一句「报告了此问题」，“理由”列
重复当前状态的 `statusReason`——**表格是为不同的行服务的**，这里等于把一句话说了三遍。载荷里既没有
逐次运行的时间戳、也没有状态变更事件，所以**不画时间线**（画了就是臆造）。现在：一行「出现于 N 个运行：#1 #4」
+ 单列 `detail-grid`（首次/最近/当前状态含 reason/状态归属含出处），并明写为什么没有时间线、要它得先在后端记状态变更。

### 实测

- `cockpit-observe.sh quick`：`render_all: 9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok`；定向契约测试 ok。
- `go build ./...` 干净；`go test ./internal/cockpit/...` `ok 9.671s`（0 失败）；`gofmt -l internal/ cmd/` 空。
- `check_code(app.js)`：`errors=0 warnings=12`，12 条与 CU-00 收口时同一批（`set` fan-out 11、`api` 12/14、`fetchIssues` 13、
  `summarizeCalls` 16/11、`showCall` 13、`boot` fan-out 17 + function-length 52、`paintHeader` 12、`./spec.json` unknown-dependency、
  file-length），**无新增项**；`boot` fan-out 16→17 是同一个已有告警的数值变动，不是新告警。
  `check_code(smoke.mjs)` = PASS 0/0。app.js 现 2341 行（warn 2000 / error 4000）。
- 图：`/tmp/shots/metricGate-1792070551/detail-{where,why,activity,more}.png`（最后一批 activity/more 在
  `/tmp/shots/metricGate-1792070921/`）。

## t8 独立验证（2026-10-16 02:1x，goal `mv9rvxfs-ws5fu1`）

### 五个变异，全部 FAIL（贴原文）

副本 `/tmp/rv-cu02/`（`app.js.orig` md5 `28f15179fd1bff9ad253683cd6866a8a`、`smoke.mjs.orig` md5 `9c4d06223cbdeca52766d6481c4e28aa`），每次改完 `go test ./internal/cockpit/ -run TestCockpitRendersThePayloadsTheServerActuallySends -count=1`，跑完 `cp` 还原并核对 md5。

| 变异 | 改法 | 结果 |
|---|---|---|
| 02-M1 | `issues-layout${s.openFingerprint ? " has-detail" : ""}` → `issues-layout` | `--- FAIL: TestCockpitRendersThePayloadsTheServerActuallySends (2.80s)` + `FAIL issue.select: an open issue did not switch the layout to three columns` |
| 02-M2 | `ensureSource` 里 `if (!fingerprint \|\| state.issueTab !== "where") return;` → `if (!fingerprint) return;` | 首次**没有** FAIL（见下），补断言后 `FAIL ensureSource: switching to the 原因 tab read the operator's source, which that tab never shows` |
| 02-M3 | 删掉 `if (state.source && state.source.fingerprint === fingerprint) return;` | 首次**没有** FAIL，补断言后 `FAIL ensureSource: asking for an issue whose code window is already loaded re-read the file` |
| 02-M4 | `.issue-detail-loc` 的 `${fileBase(issue.file)}` → `${issue.file}` | 首次**没有** FAIL，补断言后 `FAIL issue.select: the detail location line does not show the short name "engine.go:7"` |
| 02-M5 | 从 `ROUTE_KEYS` 摘掉 `["tab", "issueTab", "where"],` | `FAIL ensureSource: the 原因 tab is not in the URL (view=issues&open=ec57886ee62b8384), so a shared link loses it` |

### 三个变异暴露的三处断言空洞（本轮补上，这是 t8 的主要产出）

M2/M3/M4 第一次跑**都是绿的** —— 断言看着在守，实际恒真：

1. **M2（tab guard）被 fingerprint guard 遮住**：断言只看 `requested.size`（URL 集合），而上一步刚载入过同一 fingerprint 的窗口，fingerprint guard 先返回，tab guard 从未被执行。修法两步：`app.record({ source: null })` 先丢掉缓存窗口，再把断言从「URL 集合」换成**调用计数**（`smoke.mjs` 新增 `let requestCount = 0`，在 fetch stub 里 `requestCount += 1`）——同一端点的第二次读对 `requested` Map 是隐形的，这正是「零读取」必须按请求次数而非新 URL 数的原因。
2. **M3（fingerprint guard）根本没有断言**：没有任何断言要求「窗口已在手时再问一次不重新读文件」。补在 `smoke.mjs` 回「位置」tab 之后：记 `requestCount`，连调两次 `ensureSource()`，计数必须不变。
3. **M4（`fileBase()`）只被列表侧断言覆盖**：`smoke.mjs:461-489` 的 L1 断言读的是 `base("issues")`（问题卡片），详情 pane 的 `.issue-detail-loc` 是另一个元素，删掉它的 `fileBase()` 列表检查照样绿。补：切出 `class="issue-detail-loc"` 到 `</p>` 的切片，两半都查（`title="<绝对路径>"` 可达 + 可见文本是短名 `<fileBase>:<line>`）。

### 四张图（`/tmp/shots/metricGate-1792087773/`）

`bash scripts/cockpit-observe.sh shots "" "detail-where,detail-why,detail-activity,detail-more"` →
15.2s / 7.7s / 6.9s / 7.0s，`batch done: 4 shots, 0 failed`，`err=` 全空，
bytes 308601 / 237325 / 246112 / 226355，`detail=` 字节数 8010 / 1796 / 1904 / 1733（四 tab 确实互不相同）。
拍前重导载荷（`python3 /tmp/shots/dump_payloads.py` → 7 endpoints，anchor `f0843fbe1dce6fec` / `internal/refs/clike.go:69`）。

对 `/tmp/sq/sq-issue-detail.png` 逐区块判定（与收口段的结论一致，本批图逐条复核）：

| 区块 | 判定 | 本批图的依据 |
|---|---|---|
| 面包屑 `项目总览 › metricGate` | 采纳 | 三栏顶部，与 SonarQube 同位置同层级 |
| 三栏（列表 + 详情并列） | 采纳 | 详情 pane 在右第三栏，`位置/原因/履历/详情` tab 条与列表卡片同屏可见 |
| tab 条四问句语义 | 采纳（文案保持中文四词） | `位置` 高亮态有下划线，切到 `原因/履历/详情` 各自内容不同（`detail=` 字节 8010→1796→1904→1733） |
| 代码窗口行号列 + 锚点行高亮 + 3px inset | 采纳 | `detail-where.png`：`clike.go 第 54-84 行 / 共 170 行 · 锚点 69 · 已截断`，69 行整行 warning-weak 底 |
| 锚点行行内重复消息 | 采纳 | 69 行内联 `fan-out of "walker.walkCS" is 11 (threshold warning 10)`，与 SonarQube 同款 |
| 隐私/截断提示 | 采纳 | `· 已截断` 在路径条右侧 |
| `Effort` / `Assignee` / `tags cwe` | 不采纳 | 载荷无字段（D3） |
| `Software qualities impacted` / `Code attribute` 面板 | 不采纳 | 需 rule→质量属性映射，属另立项 |
| `Open in IDE` / `See all issues in this file` / `Show previous-next few lines` | 不做 | 后端不知 IDE 协议；按文件聚合属 CU-00 file-group 范围 |
| 履历 tab 时间线 | **不画**（诚实） | `detail-activity.png` 明写「载荷里没有逐次运行的时间戳，也没有状态变更事件，所以这里不画时间线；需要它就得先在后端记状态变更」，只列 `出现于 1 个运行：#584` + 首次/最近出现 + 当前状态 + 状态归属「未署名 · 来自本次运行记录」 |

### 一处观察（非缺陷）

四张图里列表区都能看到 facet 数字很大（`11705 个问题`）而列表只画一页 —— 这是 `范围: last 60 run(s)` 窗口的累计去重计数与分页的正常关系，`全量计数，不随筛选变化` 的口径行已在左栏顶部声明。
