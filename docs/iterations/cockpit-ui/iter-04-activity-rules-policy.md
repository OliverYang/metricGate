# 迭代 CU-04 · Activity / Rules / Policy 三页对齐 + UI 评审规则进闸门（goal）

父：CU-00 ｜依赖：CU-00 L3（`activityEvents` 的兜底必须先修，否则时间线截图不可信）
参考：`/tmp/sq/sq-activity.png`、`/tmp/sq/sq-issues.png`（规则表口径）

## 为什么做这个

三页共用 `s.policy` / `s.dashboard`，改在一处最省：一次 `fetchPolicy` + 一次 `fetchDashboard`
往返就能拍齐。但它们现在各自有硬缺陷：

- **Activity**（`VIEWS.activity` 1497）：时间线只有 run + call 两类事件，
  `activityEvents`（1524）的 `(scope==="" ? dashboard.runs : projectOf(s)?.runs) || dashboard?.runs`
  兜底让「未归属」视图泄漏全部运行（L3）。趋势柱 `trendColumn`（689）用 inline `style="height:…%"`
  是数据驱动，可保留；但柱图没有刻度与区间标签，读不出量级。
- **Rules**（`VIEWS.rules` 1607）：只列 `languages.*` 键的「键/取值/来源」，
  parity 文档 §3.5 要求的是**规则目录**（每条规则的存在理由 reason 与出处）——
  载荷里有 `p.keys`，缺的是投影，不是数据。表头没有排序，规则多了没法看。
- **Policy**（`VIEWS.policy` 1372 → `policyPane` 1386）：发现链 aside + editor + EFFECTIVE 表三段已成型，
  但 1437–1438 两处**装饰性 inline style**（`margin:16px 0 0;padding:0;border:0` / `padding:16px 2px 10px`）
  违反令牌化纪律 —— 这是全文件 7 处 `style="` 里唯一不合法的两处（其余是数据驱动的条宽/柱高）。

## 成功标准

1. `activityEvents` 只返回当前 scope 的事件，「未归属」不再兜底成全部；时间线一行 = 类型图标 + 主体 +
   右侧相对时间，与 SonarQube 的 activity 行同构。
2. 趋势图有可解释的刻度：至少标出峰值与单位（runs / findings），空态文案说明「还没有事件」。
3. Rules 页变成目录：每条规则显示 reason + 出处，缺 reason 显示 `—` 并**说明这是缺口**（不隐藏列）。
4. Policy 页清掉 1437–1438 的装饰性 inline style，改走类名；发现链 ◉/○/· 三态与「生效/未命中」后缀在浅色彩带上可读。
5. `facetPanel`（1468）与 CU-00 L2 的联动结论推广到这三页： facet/筛选计数口径 = 全量，点击才收窄。
6. **新建 `docs/constraints/cockpit-ui-review.md`**（.gitar 式 UI 评审规则，L5）：
   单内页 ≤8 色、rail 是 9 视图唯一入口、facet 计数口径、`:root` 外无颜色字面量、
   非数据驱动不得用 inline style、禁抄 SonarQube 代码与 l10n 文案。
7. 截图：`activity / rules / policy` × 3 scope 出图并与参考图判定（此前这三屏从未拍过）。

## 边界

不改 `/api/policy` 的读写协议与 same-origin 校验；`policyDraft`/`#policyDirty` 的编辑语义保持不变；
不新增 webspec 之外的视图。UI 评审规则文件**只写规则**，不在本 goal 里改 `.metricgate.yaml` 的门禁绑定。

## 验证口径

`cockpit-observe.sh fast` 全绿 + `grep -c 'style="' app.js` 从 7 降到 5（只余数据驱动的
680/697/698/699/700）+ 9 张截图（三页 × 三 scope）+ 终批 `check_path('internal/cockpit/web', diff_only:true)`
无 error 级 finding。

## 截图判定（task-7，2026-10-16）

批次：单批 9 张 `/tmp/shots/metricGate-1792086542`（activity-global 121480 / activity-project 117006 /
activity-unassigned 108202 / rules-global 144794 / rules-project 147330 / rules-unassigned 145742 /
policy-global 183246 / policy-project 185985 / policy-unassigned 184427 B），
`batch done: 9 shots, 0 failed`，harness title 全为 `READY <view> scope=<…> err=` 空串，
`cards=0 detail=0 groups=0`（三页无卡片、无详情、无文件分组，与视图语义一致）。

这批是**审计退回后的最终取证**：最终树 `app.js` 写于 01:45:14、`style.css` 01:26:45，
9 张截图 mtime 01:49:19–01:49:55，**全部晚于最终写入**；harness 副本 `app.js`/`style.css`
与当前树 sha256 一致（b56129e978 / 8846e1365b），因此截图取证的就是最终代码。
task-2 的排序表头在 `rules-global.png` 上可直接看到（`键（度量配置）↑`，其余两列中性 `↕`）。
早于此的 `/tmp/shots/metricGate-1792082899`、`-1792082995` 是审计前分批拍摄、跨多个写入点，
**已作废，不作为本判定的证据**。

对参考图（`/tmp/sq/sq-activity.png`、`/tmp/sq/sq-issues.png`）的差异与判定：

| 截图 | 判定 | 依据（可复算） |
|---|---|---|
| activity-global | **采纳** | 时间线按倒序混排 run/call 两类事件，右侧相对时间；趋势 panel 因 `dash.trend.series` 为空数组而不渲染（`activity:global` 的 chart caption 只在 `points.length` 时出现，属正当地空态） |
| activity-project | **采纳**（一处缺陷已修） | `scope=/…/alpha` 下时间线为该项目自己的 1 run + 2 call；新增 `chart-caption` 与 `legend-scale`（峰值 2）可见，即 task-3 的刻度行已在图上 |
| activity-unassigned | **采纳** | `scope=""` 下时间线只含未归属事件，无项目身份泄漏（L3 的 smoke 断言覆盖此性质） |
| rules-global / project / unassigned | **采纳**（附后端缺口） | 三张 beltcrumb 不同、表格内容一致，符合 `/api/policy` 无 scope 参数的预期；`138 条生效键` 与 `p.keys.length` 相符，但**取值列 138 行全为 `—`** |
| policy-global | **采纳** | 发现链三态（●生效 / ○未命中 / ·未试）与「生效键与出处」表、discovery aside + editor + EFFECTIVE 三段结构均在；task-1 清掉的两处装饰性 inline style 在图上已无对应偏移 |
| policy-project / unassigned | **采纳** | 同样仅 breadcrumb 不同，与 `/api/policy` 无 scope 一致 |

## t12 独立验证复拍（2026-10-16 02:5x）

批次 `/tmp/shots/metricGate-1792090315`（`MG_SHOT_HEIGHT=2700`），9 张
`activity-{global,project,unassigned}` / `rules-{…}` / `policy-{…}`，
`batch done: 9 shots, 0 failed`，日志 `err=` 全空、`cards=0 detail=0 groups=0`（三页无卡片无详情无分组，
与视图语义一致）。字节 112317–186096。拍前按 README「拍图前必做」重导真实载荷（7 endpoint / 147178 B，
`projects` 5 个，`metricGate` trend 20 点），避免 `TestZZDumpShotsPayloads` 的夹具载荷混进来。

**这批图与 task-7 那批的差别**：task-7 的 activity-project 峰值是 2（夹具只有 1 run），本批是 **378**
（真实 20 次运行），说明刻度读的是真实数据而不是夹具常量。

对 `/tmp/sq/sq-activity.png`（1500×875）的逐区块判定：

| 区块 | SonarQube | 我们 | 判定 |
|---|---|---|---|
| 时间线形态 | 左侧按日期分组（October 5, 2026 / 11:30 AM），每行图标 + 描述 | 平铺倒序，每行图标 + 主体 + 右侧相对时间 | **采纳（有意不同）**：载荷 `startedAt` 是 ISO 时间戳，分组可实现；但我们的行同时混排 run 与 call 两类事件，按日期分组会把「一次扫描 + 紧随其后的 verify 调用」这对因果关系拆到两天里。相对时间在「刚刚发生了什么」这个问题上更直接 |
| 趋势图位置 | 与时间线**左右并列**，图占右半屏 | 图在上、时间线在下（`view-stack`） | **采纳（有意不同）**：并列需要右半屏宽度，我们的 `.layout-grid` 在 1100px 断点下会塌成单列，届时图会压在时间线之上——堆叠是两种宽度下都成立的排法 |
| 图表类型 | 折线图 + 刻度轴（0/200/400/600/800）+ 年份轴 + 底部 brush 缩略图 | 堆叠柱 + `chart-caption`（峰值 378 + 单位）+ `legend-scale` | **部分采纳**：刻度与单位已采纳（task-3）；折线与 brush 不采纳——柱的堆叠本身表达 error/warning/info 构成，折线要丢这一维；brush 需要后端按时间窗口查询 |
| 筛选控件 | `Events filter` + `Date range filter` + `Graph type` 三个下拉 | 无 | **不采纳**：三者都要后端支持（`/api/dashboard` 不接受时间窗口或事件类型参数），前端加控件等于加一个点不动的按钮 |
| 图例 | `Issues` / `New Code` 两条系列 | error / warning / info 三个构成色 | **采纳（语义等价）**：SQ 分「总量 / 新增」，我们分「构成」；我们没有「新代码」这个基线概念（parity §4.3 未定） |

对 `/tmp/sq/sq-issues.png` 的 rules / policy 两页（SQ 没有对应页，取列表页的表格语言做参照）：

| 截图 | 判定 | 依据（可复算） |
|---|---|---|
| activity-global | **采纳** | `20 次运行 · last 20 run(s), unassigned runs` + 20 根柱；时间线 52 个事件，行内含 `运行 #id` / 文件数 / error / warning / 耗时 |
| activity-project | **采纳** | `20 次运行 · last 20 run(s), project /Users/mac/Downloads/metricGate`，峰值 **378**；6 个事件（项目自己的 run 序列） |
| activity-unassigned | **采纳** | 与 project 的**口径行不同**（`unassigned runs` vs `project …`）而**时间线行集不同**（52 vs 6 个事件）——这正是标准 1「只返当前 scope」的图上证据；两侧都不含对方的 run id |
| rules-{global,project,unassigned} | **采纳**（附后端缺口） | `138 条生效键`，表头 `键（度量配置）↑` / `取值 ↕` / `出处 ↕`（task-2 的排序表头）；三张仅面包屑不同，与 `/api/policy` 无 scope 一致；**取值列仍 138 行全 `—`** |
| policy-{global,project,unassigned} | **采纳** | 发现链三态（● `.metricgate.yaml · 生效` / ○ `.metricgate/config.yaml` / ○ `policy.yaml`）+ 分节芯片 + 编辑器 + `EFFECTIVE 138 个已显式设置的键` 四段结构齐全；task-1 清掉的两处装饰性 inline style 在图上无对应偏移 |

**t12 新增的两处断言**（原契约只有「已达成」，无反向验证记录）：

1. 04-2 趋势刻度：`render_all.mjs` 新增活动页趋势块——峰值由夹具数据算出（不写死），
   要求 `chart-caption` 里的 `纵轴峰值 <strong>N</strong>` 与 `legend-scale` 同时命中；空态用清空
   `runs`/`calls` 的 state 渲染并要求「还没有事件」。
2. 04-3 排序箭头：`render_all.mjs` 的 `thStates` 改为同时抓 `aria-sort` 与 `.sort-arrow` 字形，
   断言降序活动列画 `↓`、升序画 `↑`、非活动列画 `↕`。**这补的是 app.js 注释自己声称的性质**
   （「箭头给不信任属性的人看」），原先只断言了 `aria-sort`。

### 本次判定暴露的后端缺口（不在前端可修范围）

`internal/cockpit/policy_api.go:237 policyKeys()` 写死 `Value: ""`，注释说取值由前端
「compact display」却没有任何填充来源，导致 `/api/policy` 的 `keys[]` 全部
`value == ""`。Rules 页与 Policy 页 EFFECTIVE 表的「取值」列因此恒为 `—`——
这不是渲染缺陷：前端拿到空串只能显示占位。**记为 L10**，属后端投影缺口，修法是在
`policyKeys` 里按 key 走一次 `config` 的取值格式化（如 `fmt.Sprintf("%v", …)`）。
同理 `keys[]` 的 `explicit` 字段前端未使用（138 条全为 true，无区分度），暂不渲染。

### 与 SonarQube 的已知差异（有意不为）

- Rules 页不做「左 facet + 右详情」双栏：无 `/api/rules`（parity §4.2 P1-3），单表 + 缺口说明比空详情栏诚实。
- Activity 无 SonarQube 的「Custom filter / date range」浮层：本地工具无用户体系。
- 三页均不显示 Effort / Assignee / Tags：payload 无字段（D3 不造假）。

## CU-04 收口（task-7，2026-10-16）

七条成功标准逐条核对：

1. `activityEvents` 只返回当前 scope —— `activityEvents`(app.js:1784) 读 `runsInScope(s).runs`，
   `grep 'dashboard?.runs'` 只在 1780 行的注释里命中；smoke.mjs:471-486 的逐 run 断言双向保护。
2. 趋势图刻度已加：`chart-caption`（单位 + 纵轴峰值）+ `legend-scale`（峰值，右对齐）+
   `trend-chart` 的 `role="img"`/`aria-label`；空态沿用「还没有事件」。
3. Rules 成目录：列出全部 138 条生效键 + 取值 + 出处；缺 reason **不隐藏列**，而是顶部缺口说明段
   指明 `/api/rules` 无出口并给出仓内可查路径。契约原文要求「每条规则显示 reason + 出处」，
   但 payload 无规则目录，故按「缺 reason 显示 — 并说明这是缺口（不隐藏列）」执行。
4. 两处装饰性 inline style（原 1658/1659）已删，`grep -c 'style="' app.js` 从 7 降到 5。
5. facet 口径：`facetPanel` 经 `facetCounts` 取缓存全量，`facetNote` 声明口径；
   render_all.mjs:247-306 与 smoke.mjs:362-405 两侧断言。
6. `docs/constraints/cockpit-ui-review.md` 新建，六条规则均带判定命令。
7. 9 张截图拍齐并判定（见「截图判定」节）。

验证：`bash scripts/cockpit-observe.sh fast` 一次 → `0 errors, 13 warnings, 413 metric snapshots`、
run #1003、`gate: PASSED (fail_on=error)`；`check_path('internal/cockpit/web', diff_only:true)` →
`verdict=PASS errors=0 warnings=12 files=3 hidden=1`（12 条 warning 与 CU-01 收口同一基线）。

**CU-04 closed。**
