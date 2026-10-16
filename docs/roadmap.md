# metricGate 路线图：审美回路补全（迭代计划）

> 本文是"审美回路缺口分析"的落地文档。核心论点：AI 代码的"AI 味"一半是可测量的
> 结构问题（复杂度、重复、越界），而工具的深层价值是**项目的审美记忆**——
> 人"住过、疼过、改过"的判断被固化为 policy/账本/趋势，agent 每次开工前读
> digest 等于补上"逐渐认识"的过程。当前代码库的回路骨架已在，但有 4 个断点，
> 本计划按迭代补全，并**每次迭代用 metricGate 开发 metricGate（自吃狗粮）**，
> 验证工具的可介入、可审计、可度量、可回溯、可观察（下称"五性"）是否真正闭环。

## 一、回路四阶段与断点现状

| 回路阶段 | 对应机制 | 现状 | 断点 |
|---|---|---|---|
| ① 前馈：断言→下一次生成 | get_constraints digest · presets · provenance | ✅ 基本闭环 | — |
| ② 度量留存：住进去→哪里疼 | trends · hotspots · store | ⚠️ 半闭环 | **A** |
| ③ 疼→断言固化：修正判断 | inline ignore · .metricgate.yaml | ❌ 断得最狠 | **B、C** |
| ④ 实验：产品纠正人的预期 | regress -diff · scorecard | ⚠️ 只有单次 diff 视角 | **A** |

### 断点 A：metric 维度趋势缺失（可度量/可回溯的洞）

- 证据：`internal/store/store.go:308-315` — `TrendPoint` 只有
  `StartedAt/Errors/Warnings/Infos`，severity 序列而已；
  `metric_snapshots` 表（`internal/store/store.go:52-59`）每次 run 都存
  13k+ 快照（file/scope/metric/value/warn_threshold/err_threshold），
  **数据在库里，查询面没做**——没有 `metrics` 维度的时间序列视图。
- 后果：无法回答"上个月收紧 cognitive 阈值，这个月复杂度真的降了吗"。
  阈值调整（审美决策）变成不可检验的信仰。
- 补全 → 迭代 3。

### 断点 B：让步无账本（可审计的洞）

- 证据：`internal/engine/suppress.go:28-31` — `suppression{start,end,rules}`，
  inline `metricgate: ignore` 指令是**匿名的**：谁 ignore 的、为什么、
  到什么时候，全无记录。全仓 grep 无 accepted.yaml / accept CLI。
- 后果："这个 warning 我认了"和禁令同等重要（都是审美判断），但它散落在
  代码注释里：不可审计（谁让的步？）、不可过期（认一次永久豁免）、
  不可汇总（cockpit 看不到让步面有多大）。
- 补全 → 迭代 1。

### 断点 C：新维度写不进 policy（回路里最致命的一格）

- 证据：`internal/rules/rules.go:261+` — `ForLanguage` 只返回内置规则包；
  无任何自定义规则机制。试点实证（`docs/pilot/conversion-map.md`）：
  40 条团队规范 13 条未转化，其中 11 条卡在"AST 模式明确但内置没有"
  （NAMING-01~06 命名规约、EXC-03 finally-return、EXC-02 catch 类型…）。
- 后果："人发现新的疼痛维度"是工程不能自动化的那一格——而那一格现在
  焊死了：人发现了"命名不合规范很疼"，却没有任何格子把它写进 policy。
- 补全 → 迭代 2。

### 附加缺口（不属回路断点，但同根：接入摩擦与修复建议）

- layer-violation 无修复建议：试点 132 条违规 77 条同根因（类放错包），
  工具判对但让人自己悟怎么修（`docs/pilot/pilot-report-scan-portal.md` §3/§4.2）。
- 依赖白名单半自动生成缺失：试点接入最费时的手工步骤（§4.5）。
- 测试代码不是一等公民：测试函数长/参数多被正常度量误伤，只能 exclude（§4.6）。
- 补全 → 迭代 4、5。

## 二、迭代计划（按回路完整性排序，每迭代 = 功能 + 狗粮验证 + 迭代日志）

> 狗粮协议：每次迭代开发完成后，用**当前版本的 metricGate** 检查本次迭代
> 产生的代码（check . -diff），并回答五性问题——
> 1. **可介入**：gate 是否在本次开发中被触达？（MCP preamble / check -diff / pre-commit）
> 2. **可审计**：本次的每个"接受/豁免/禁用"决策是否有据可查？
> 3. **可度量**：本次改动的质量变化是否被数字捕获（而非凭感觉）？
> 4. **可回溯**：出问题时能否定位到具体 file:line + 规则 + 当时阈值？
> 5. **可观察**：cockpit/trends 是否能看到本次迭代留下的痕迹？
> 任何一性答"否"即为断点：先补断点再进入下一迭代。日志写入
> `docs/iterations/iter-N-<slug>.md`。

### 迭代 1 · `metricgate accept` 审美让步账本（P0，最轻）

**功能**：`.metricgate/accepted.yaml`（有主 owner / 有理由 reason /
有过期日 expires / 可解除）；`metricgate accept` CLI 添加与解除；
check -diff 读取账本，warning 级 finding 区分 `accepted`（账本内且未过期）
与 `open`；cockpit 展示让步记录与过期提醒。

**为什么最轻先做**：它是"项目有记忆"叙事里唯一还缺的实体；且立刻能吃
狗粮——本仓自身就有 3 个预先存在 fan-out warning
（`cmd/metricgate-mcp/calllog_test.go:43/176`、`cmd/metricgate-mcp/main.go:35`）
正好做第一批账本条目。

> **状态：✅ 已完成**（2026-10-11）。交付与五性核对见
> [docs/iterations/iter-1-accept-ledger.md](iterations/iter-1-accept-ledger.md)。
> 亮点：gate 抓住了新写的 cmdAccept（cognitive 26/cyc 17 双超），拆分后清零；
> 3 条预存 warning 已入账本，`check . -diff` PASSED。遗留：MCP preamble 未接账本。

**狗粮验证清单**：
- [ ] 用 accept 记录上述 3 条 warning（含真实理由：预先存在/HEAD 已有）
- [ ] check -diff 输出正确区分 accepted/open
- [ ] 迭代日志：五性核对 + 开发过程中 gate 实际拦截记录

**闭环判据**：warning 不再二选一（修 vs 全局关规则），让步有主有期有理。

### 迭代 2 · 自定义规则 DSL 最小版（P0，最重杠杆）

**功能**：.metricgate.yaml 支持命名模式断言规则——标识符正则 × 节点类型 ×
包上下文（试点 P0 结论：一条 DSL 吃掉 6 条命名条款）。最小面：
`patterns:` 规则族（match/where/message/severity），引擎加载进规则包。

**狗粮验证清单**：
- [ ] ≥2 条真实 DSL 规则（如：测试函数须 Test 前缀；禁止 XxxHelper 上帝类命名）
- [ ] /tmp demo 验证命中与不命中
- [ ] 本仓自身 check . -diff 在 DSL 约束下跑通
- [ ] 迭代日志：五性核对（重点：**可介入**——新维度发现后到 policy 生效的时长）

**闭环判据**：发现新审美维度 → 写进 policy → gate 生效，全程 ≤1 天。

> **状态：✅ 已完成**（2026-10-11）。交付与五性核对见
> [docs/iterations/iter-2-dsl.md](iterations/iter-2-dsl.md)。
> 亮点：gate 抓住了新写的 dslRule（cyc 20），拆分后清零；狗粮规则
> go-test-func-naming 本仓零误报；/tmp/dsldemo 命中+不命中+ratchet 三段式
> 验证。遗留：MCP get_constraints digest 未展示 custom_rules 清单。

**狗粮验证清单**：
- [x] ≥2 条真实 DSL 规则（go-test-func-naming 狗粮 + 单测覆盖 import 禁令/Python 标识符）
- [x] /tmp demo 验证命中与不命中（/tmp/dsldemo：testlowercase 命中、TestProper 不命中、-diff 下新违规命中）
- [x] 本仓自身 check . -diff 在 DSL 约束下跑通（PASSED，custom 规则 0 findings）
- [x] 迭代日志：五性核对（可介入：从语义误用到 e2e 抓住修正再到本仓生效，单次会话内完成，远低于 ≤1 天判据）

### 迭代 3 · metric 维度趋势与阈值实验视图（P1）

**功能**：基于已有 `metric_snapshots` 表新增聚合查询（按 run×metric 的
p50/p90/max 序列）；`metricgate trends` 与 cockpit 增加 metric 维度视图；
能输出"阈值变更前后对比"。

**狗粮验证清单**：
- [ ] 本仓制造一次真实阈值调整：policy 修改 → 两次 check → trend 对比输出
- [ ] 迭代日志：五性核对（重点：**可度量**——审美决策是否变成可检验实验）

**闭环判据**：回答"这次阈值收紧值不值"有数据，不靠感觉。

> **状态：✅ 已完成**（2026-10-11）。交付与五性核对见
> [docs/iterations/iter-3-metric-trend.md](iterations/iter-3-metric-trend.md)。
> 亮点：真实阈值实验 15→10（violated 29→152，分布持平）；gate 抓住
> MetricTrend 与测试函数复杂度各 1 次；自研 DSL 的 exclude-only 语义 bug
> 被自家狗粮规则当天抓住并修复（跨迭代联动）。

**狗粮验证清单**：
- [x] 本仓制造一次真实阈值调整：cognitive warn 15→10 → 两次 check → trends -metrics 对比输出（#523 vs #524）
- [x] 迭代日志：五性核对（可度量：violated 阶梯 + 分布不变性测试；结论：维持 15 是数据支持的决定）

### 迭代 4 · layer 违规的结构修复建议（P1）

**功能**：layer-violation finding 附带修复建议（类搬家/包归属建议），
覆盖试点 77/132 同根因场景；架构图谱已有（`internal/arch/`），建议生成
基于包级依赖方向。

**狗粮验证清单**：
- [ ] /tmp 多包 demo 触发违规 → 建议文本可执行
- [ ] 迭代日志：五性核对（重点：**可回溯**——finding→建议→行动链路）

**闭环判据**：违规报告从"列表"变"行动"。

> **状态：✅ 已完成**（2026-10-11）。交付与五性核对见
> [docs/iterations/iter-4-layer-advise.md](iterations/iter-4-layer-advise.md)。
> 亮点：双分支建议（fan-in 层数 ≥2 → 搬家 / 否则 → 改声明）覆盖试点
> 77/132 根因；/tmp/layerdemo 两形态建议均验证可执行；gate 抓住
> suggestFix 不可达分支与测试 fixture 认知错误各 1 次。

**狗粮验证清单**：
- [x] /tmp 多包 demo 触发违规 → 两形态建议文本均可直接执行（搬家+声明层 → PASSED；加 may_depend_on → PASSED）
- [x] 迭代日志：五性核对（可回溯 ✅：建议随 finding 存 history.db；可度量 ⚠️ 采纳率无追踪，记入遗留）

### 迭代 5 · deps scan + 测试一等公民（P2，推广摩擦清除）

**功能**：`metricgate deps scan` 从 import 面生成 dependencies.allow 草案
（复用 internal/deps 九语言 manifest 解析）；测试文件（_test.go/test_*.py
等）适用独立度量基线（长函数/多参数豁免），不再靠 exclude。

**狗粮验证清单**：
- [x] 本仓跑 deps scan，对照现有 policy 依赖清单核对一致性（13 distinct / 0 uncovered by 30 declared，见 docs/iterations/iter-5-deps-scan.md）
- [x] check . -diff 全绿（测试基线生效后：test_metrics.fan-out 取代 2 条 calllog_test.go 逐行让步，PASSED）
- [x] 迭代日志：五性核对总结论（docs/iterations/iter-5-deps-scan.md，全部 ✅）

**闭环判据**：新用户接入依赖清单从小时到分钟。

### 收尾 · 五性总结报告 ✅ 已完成

汇总 5 次迭代日志，输出最终核对报告：每性给出证据（机制 + 迭代日志引用）
与仍存在的边界。此报告即"审美回路工程化"的验收物。
→ [docs/iterations/five-properties-report.md](iterations/five-properties-report.md)（五性全部闭环成立；唯一 ⚠️ 建议采纳率未度量，已记遗留）

## 三、明确不做（本计划边界）

- 不做 autofix（CI 里的 agent 自动修复）——已决策后置，保持零凭据卖点
- 不做语义级检查（COMMENT-04 中文注释质量、EXC-04 异常流程控制等 13 条
  ❌ 中的语义类）——试点已定性不可自动化
- 不改 v2/v3 既有规划（HTTP service / LLM adapter / skeleton）——本计划
  是 v1.x 的补全，与其正交

## 四、与既有文档的关系

- 缺口证据：`docs/pilot/conversion-map.md`（40 条转化对照 + 空隙清单）、
  `docs/pilot/pilot-report-scan-portal.md` §4（6 项产品建议）
- 价值叙事：`docs/claims.md`（台账 #5/6/7/8 与本计划直接相关）
- 迭代日志（本计划产生）：`docs/iterations/`

## 五、后续方向候选（v1.x backlog）

本计划的 5 次迭代已全部完成。之后的候选方向（含实测证据、必做护栏、已决定搁置项）
另行记录于 → [docs/backlog-v1x.md](backlog-v1x.md)。
本计划边界见上节「明确不做」，backlog 与其不冲突。

### v1.x 已落地

| 迭代 | 内容 | 日志 |
|---|---|---|
| 迭代 6 | git 历史共变（change coupling）：`metricgate coupling` + `-diff` 跨层共变门禁 | [docs/iterations/iter-6-coupling.md](iterations/iter-6-coupling.md) |
| 迭代 7 | facts 符号层补齐 7 语言（Py/TS/JS/C/C++/C#/Rust）：表驱动抽取器 + `declKinds` 补齐 | [docs/iterations/iter-7-facts-symbols.md](iterations/iter-7-facts-symbols.md) |
| 迭代 8 | 第二轮广度调研（9 方向 → 3 入选）的实现：继承/实现边、`custom_rules` 文件作用域、root 一致性 | [docs/iterations/iter-8-t6-three-directions.md](iterations/iter-8-t6-three-directions.md) |
| 迭代 9 | 九语言能力拉齐：`metricgate: ignore` 抑制、arch 依赖图、CK 类级度量、`banned-api`、gatecheck、rulegen、digest 语言塑形、context 约束节 | [docs/iterations/iter-9-language-parity.md](iterations/iter-9-language-parity.md) |
| 迭代 10 | `-diff` 棘轮改用数值 delta：类级指标数值未变不再重报（实测 5 提交 error 31 → 16） | [docs/iterations/iter-10-diff-value-delta.md](iterations/iter-10-diff-value-delta.md) |
| 迭代 11 | `impact -depth N` 传递闭包（三层可信度 + 宽度上限）与 `metricgate reachability` 无人引用/无测试触达报告 | [docs/iterations/iter-11-impact-reachability.md](iterations/iter-11-impact-reachability.md) |
| 迭代 12 | 闭环易用性：cockpit 读库接线、MCP preamble 不再吞载荷、accept 对 error 级当场拒绝、`-hidden` 展开、`suppressions` 让步统一视图、JSON 裁决字段 | [docs/iterations/iter-12-closed-loop-usability.md](iterations/iter-12-closed-loop-usability.md) |
| 迭代 13 | TSX 支持：`.tsx` 注册为独立语言（自有语法，TS 语法不带 JSX 节点），解析之上 13 层表全部与 TypeScript 共用 | [docs/iterations/iter-13-tsx.md](iterations/iter-13-tsx.md) |
| 迭代 14 | 策略指纹落库：`runs.policy_digest` + 幂等迁移，JSON 信封 / SARIF / bundle 同源带出，历史行不回填 | [docs/iterations/iter-14-policy-fingerprint.md](iterations/iter-14-policy-fingerprint.md) |
| 迭代 15 | 组织策略 `extends:` 单继承（环检测 + 深度上限 + 来源链）与 `trace_id` 贯通四张表（`$METRICGATE_TRACE_ID`，未设置时输出逐字节不变）+ `runId` 输出 | [docs/iterations/iter-15-extends-and-trace.md](iterations/iter-15-extends-and-trace.md) |
| 迭代 16 | 配置发现链（`.metricgate.yaml` → `.metricgate/config.yaml` → `policy.yaml`，就近停止 + 停在 Git 根）+ Cockpit 策略读写 API（发现链/分节/生效键与出处可查，yaml 校验后原子写） | [docs/iterations/iter-16-config-discovery.md](iterations/iter-16-config-discovery.md) |
| 迭代 17 | 审计结果路由到人：`internal/owners` 解析 CODEOWNERS（GitHub 同语义 last-match-wins），`reachability` / `impact` / `rules gaps` 加 `-owners` 归属列，未匹配显式 `unknown` + 计数；`trends -out / -since / -project` 收敛为 `store.Window`，`-project` canonical 解析（macOS symlink 等价） | [docs/iterations/iter-17-ownership-and-trend-window.md](iterations/iter-17-ownership-and-trend-window.md) |
| 迭代 18 | trace 属性（X2）与组合报表（X3）：`$METRICGATE_TRACE_ATTRS` 采集 + `trace_attrs` KV 白名单表（未知键丢弃并计数）；`internal/traceview` 把 runs/findings/attrs/calls 按 trace 汇成一个视图，`metricgate trace <id>`、MCP `get_trace`、`trends -trace/-model/-prompt-digest`、Cockpit trace 下拉四处同源；`Window.TraceIDs` 用 nil/空区分"全部 trace"与"无匹配" | [docs/iterations/iter-18-trace-attrs-and-report.md](iterations/iter-18-trace-attrs-and-report.md) |
