# CU 拆分总览 · Cockpit SonarQube 对齐（5 个 goal）

父 goal：**CU-00** `iter-00-shell-and-tokens.md`（收尾 task-0..4 + 三个会让截图失真的缺陷）
子 goal：**CU-01..CU-04**，各对应 SonarQube 的一屏，互不重叠。

| goal | 屏幕 | 主要文件区域（app.js 行号 / style.css 选择器） | 依赖 |
|---|---|---|---|
| CU-00 | Issues 列表 + 外壳遗留（**已收口 2026-10-15**） | `issueCard`(839) `fileGroup`(881) `facetPanel`(2344) `activityEvents`(2411) / `.file-group*` 1471 `.issue-card*` 504 `.facet-*` 1384 | — |
| CU-01 | Summary / 项目概览（**已收口 2026-10-15**） | `measureCard`(1209) `acceptedEntry`(1075) `scopeAggregate`(1299) `trendScope`(2049) `workspaceOverview`(2067) `metricTrendPanel`(2107) `VIEWS.overview`(2147) / `.layout-grid` 1123 `.measures-grid` 1251 `.measure-*` 504 `.accepted-*` 505 | CU-00 L2；遗留 L8/L9（后端载荷） |
| CU-02 | Issue 详情四 tab + 代码窗口（**已收口 2026-10-15**） | `issueDetail`(1505) `ISSUE_TABS`(1587) `issueTabBar`(1592) `issueWhere`(1604) `issueWhy`(1640) `issueActivity`(1661) `issueMore`(1675) `ensureSource`(653) / `.issues-layout` 1355 `.issue-detail*` 1371 `.tab-bar` 1558 `.source-*` 506 | CU-00 L1 |
| CU-03 | 质量门禁页（**已收口 2026-10-15**） | `gateBanner`(1127) `gateHeadroom`(1163) `gateMeta`(1191) `gateConditionRows`(2470) `VIEWS.gate`(2498) / `.headroom` 781 `.gate-lang` 801 `.panel-foot` 986 `.metric-convention` 993 `.gate-banner*` 825；后端 `internal/cockpit/gate_api.go` + `gate_api_test.go` | CU-01（评级块口径） |
| CU-04 | Activity / Rules / Policy（**已收口 2026-10-16**） | `VIEWS.policy`(2201) `policyPane`(2215) `VIEWS.activity`(2379) `activityRow`(2444) `VIEWS.rules`(2533) `rulesOrder`(2634) `rulesHead`(2698) / `.th-sort` 732 `.trend-chart` 1150 `.chart-caption` 1177 `.timeline` 1599 `.policy-keys-panel` 1677 | CU-00 L3 |
| CU-05 | 表格化 + 右侧抽屉（**已收口 2026-10-17**） | `issueRow`(905) `issueTable`(926) `trendColumn`(969) `sameDaySeries`(991) `runRow`(1020) `runTable`(1040) `ratingCard`(1825) `VIEWS.issues`(1452) `VIEWS.runs`(2001) `workspaceOverview`(2067) `VIEWS.overview`(2147) / `.issues-layout` 1355 `.issues-table` 1448 `.runs-table` 1307 `.overview-head` 1238 `.rating-card` 1239 `.gate-banner` 825 | CU-00..CU-04 |

**行号是移动靶**：上表为 2026-10-17 CU-05 收口后的实测值，`app.js` 3456 行 / `style.css` 1744 行。
上一版（2026-10-16 独立验证 goal）记的是 2559 / 1411 —— CU-05 把 issues 与 runs 从卡片流改成表格、
新增 `ratingCard` 与 `sameDaySeries`，CU-00..CU-04 的锚点全部后移 150–900 行。
**开工前重导一次，别照抄**：

```bash
cd internal/cockpit/web && grep -n "export const measureCard\|const workspaceOverview\|VIEWS.activity = " app.js
```

**两条断言各守一端，不要合并**：`render_all.mjs` 的 overview allow-list 读的是 **app.js 源码**
（管「有没有多出第 4 处 `s.dashboard` 读」），`smoke.mjs` 的 L8 tripwire 读的是 **真实载荷**
（管「`ProjectSummary` 是不是已经长出 `accepted`/`metricTrend`/`summary`」）。后者不能用 render_all 代替：
render_all 只看手写夹具，Go 结构体加字段时夹具不会跟着变，检查会继续绿着而缺陷已经回来。
两边都做过反向验证（源码侧塞第 4 处 / 载荷侧给 project 加字段），均按预期 FAIL。

## 为什么不并行跑在同一棵树

AGENTS.md 的闸门循环是**有状态**的：`check_path` 写 `.metricgate/history.db`（当前 804 MB），
`pre-commit` 自动跑 `check -diff`。两个 writer 同树时，A 的 `-diff` 口径会包含 B 未完成的半成品，
gate 结论对双方都不可信。本仓已有实例：10:49 `internal/store/store.go` 因并发编辑短暂出现
`method Store.LatestRunsIn already declared` 打断 `go build ./...`，几分钟后自愈。

**规则**：同树只允许 1 个 writer；要真并行就一个 goal 一个 managed worktree，
CU-00 收口前其余四个只做读侧（截图 + 差异清单，不落码）。

## 共享观测点

`bash scripts/cockpit-observe.sh <quick|fast|serve|shots|status>` —— 每个 goal 的 fast 含义必须一致。
截图是唯一昂贵的观测点（Chrome 启动 13s+），`shots` 内置全局锁 `/tmp/shots/.shoot.lock`，并行 goal 排队。
每步的默认验证是 `quick`（实测 4.16–9.6s）；`fast` 与 `check_path` 是**收口级**观测，一批步骤后跑一次，
口径见 `AGENTS.md` 的「步骤预算」节。`shots` 的第二参数是 worktree root（空串＝当前树），第三参数才是视图名列表。

## 拍图前必做：载荷会被测试覆盖（独立验证 goal 记录）

`internal/cockpit/zz_shots_test.go` 的 `TestZZDumpShotsPayloads` 会把**夹具仓库**（alpha 临时目录、
`runId 1`、25 条条件 `hasData` 全 false）写进 `/tmp/shots-payloads.json`，与
`python3 /tmp/shots/dump_payloads.py` 写的**真实 cockpit**（本仓、`runId 1054`、`hasData` 全 true）
抢同一个文件。**任何 `go test ./internal/cockpit/...` 都会把真实载荷换回夹具载荷**，
之后拍的图全是「无数据」那一版。

固定顺序（每次拍图前）：

```bash
python3 /tmp/shots/dump_payloads.py                 # 1. 先导真实载荷
cp internal/cockpit/web/{app.js,style.css,spec.json,fixtures.json} /tmp/shots/
cp internal/cockpit/web/index.html /tmp/shots/base.html
python3 /tmp/shots/mk.py                            # 2. 再烘进 index.html
MG_SHOT_HEIGHT=2700 bash scripts/cockpit-observe.sh shots "" "<names>"
```

先 `mk.py` 后导载荷会把旧载荷烘进 `index.html`，图与载荷对不上。

## 载荷尺寸陷阱（独立验证 goal 记录）

`/tmp/shots/dump_payloads.py` 会把每个项目的 `runs` 截到 6 条而 `trend` 不截（仍 20 点），
所以注入版页面里 `runs` 列表与 `trend.runs` 天然不同源。这不是缺陷，但意味着
**`/tmp/shots-payloads.json` 是采样载荷，不能用它验证「两个字段是否相等」这类不变量** ——
这类断言必须走 `smoke.mjs`（打真实端点）或 `render_all.mjs`（受控夹具）。

## 独立验证 goal 的产出（`mv9rvxfs-ws5fu1`，2026-10-16）

台账：`verification-ledger.md`（CU-00..CU-04 共 41 条契约标准 + 10 条跨屏项，每行都有断言/重跑/反向验证/证据）。
本轮 7 处零断言或恒真断言已当场修掉：

| 缺口 | 性质 | 反向验证 |
| --- | --- | --- |
| 01-7「质量运行」卡片与运行页列表不同源（全局 scope 实测 20 vs 46） | 真缺陷 | render_all 6 条 + smoke 1 条 |
| V-1 源码窗口 `is-anchor` / `source-anchor-note` / `· 已截断` 无断言 | 零断言 | 4 次变异全 FAIL |
| X-1 路由往返遍历 `ROUTE_KEYS` 自身 → 删任意一行仍全绿（实测 12 行全 ok） | **恒真断言** | 14 次变异全 FAIL |
| 00-2 `:root` 外颜色字面量只有手工命令 | 零断言 | 1 次变异 FAIL |
| 00-4 `workspaceNav` 只有手工 `grep` | 零断言 | 1 次变异 FAIL |
| 01-1 度量卡只断言「至少一张」 | 零断言 | 1 次变异 FAIL |
| 01-5 / 04-2 / 04-3 趋势文案、活动页刻度、排序箭头无断言 | 零断言 | 3 次变异 FAIL |

**写断言的教训（三条，都是本轮踩出来的）**
1. **断言不能遍历它要保护的表**：`for (const [k] of app.ROUTE_KEYS)` 在表被删行时两边同时变短，恒真。
   要保护一张表，得有一份独立写下的期望副本，双向比对。
2. **注入点按真实语法形态选**：`export const VIEWS = {}` 只是空壳，真实声明是 `VIEWS.issues = {`；
   往空壳后面加条目测不到东西，会得到假绿。
3. **变异要越过阈值**：`addEventListener` 上限是 6，当时已有 5 个，只加 1 个不越线；
   加 2 个才 FAIL。同理写「断言某字段存在」前先看 Go 的 `omitempty`（空账本时键根本不发）。

## PRD 迭代的记录（CU 之后，按 `docs/COCKPIT_产品设计文档.md` 的编号）

| 文档 | 对应 PRD | 状态 | 备注 |
| --- | --- | --- | --- |
| `iter-05-p0-availability-and-scope.md` | 迭代 1（P0，T1.0–T1.8） | 已收口 | goal `mvac0f32-nbtjxt` |
| `iter-06-iteration2-diff-baseline.md` | 迭代 2 前置勘察 | 差集基线 | D2 四条「已在位/真缺」逐条实测，含并发 writer 证据 |
| `iter-06-p2-guidance-and-honest-empty-states.md` | 迭代 3（P2） | 另一会话在写 | **与上一行撞编号 06**，非本文作者所为，留待整理 |
| `iter-07-iteration2-close.md` | 迭代 2（P1，T2.1–T2.4） | 已验收 | goal `mvacqkra-arplee`；截图与回验脚本在 `shots-iter2/` |

**这些文档的行号锚点同样是移动靶**：迭代 2 收口时主树 `app.js` 3305 行、`gate_api.go` `gateBand:276`、`source_api.go` `resolveSourcePath:130`（17:2x 实测，同一棵树仍有并发 writer，数字本身还会漂）。改过 `internal/cockpit/` 之后照抄即错，开工前用上面的 `grep -n` 重导。
