# M2-B 任务集：论文指标触发任务与参考实现（预注册附件）

状态：**任务集已注册，跑批未开始**（需用户侧 LLM 资源）。本文档是
[`evaluation-m2b-paper-metrics-protocol.md`](evaluation-m2b-paper-metrics-protocol.md) §2
"任务集与两臂"的执行附件：把 n≥10 的任务逐个落到具体包、具体诱导机制，并给每个触发任务
配一个**参考"坏形状"实现**——已在本仓引擎上验证对应指标确实 fire（跑批前发现任务设计缺陷，
不浪费 LLM token）。任务形状的"坏"程度以人类基线为界：超阈但不过度荒谬，避免 LLM 臂因
任务本身荒谬而拒答。

日期：2026-10-08。前置：M2-B 协议（预注册判断线 H1/H2/H3 不变）；R1 沙箱协议
（[`evaluation-base-rate.md`](evaluation-base-rate.md) §1–2）。

## 0. 一句话

10 个任务（7 新写 + 3 个 U04 变体），每个触发任务带一个已验证的坏形状参考；
L0/L1/L2 三臂 prompt 完全一致，只有 digest 内容不同——净效应全部由 L2−L1 差值承担。

## 1. 沙箱协议（沿用 R1，含新发现的泄露点）

沿用 R1 policy-blind 副本：删除 `policy.yaml` / `.metricgate/` / `docs/` /
`README.md` / `AGENTS.md` / `.pi/`，保留完整源码。**在 R1 清单之上新增**：

- `MVP_SUMMARY.md`、`MVP_PLAN.md`、`IMPLEMENTATION_SUMMARY.md`、
  `WEEK1_COMPLETE.md`、`MVP_GETTING_STARTED.md`（根目录历史状态文档）。
  依据：MVP_SUMMARY.md:109 含 `banned-api:` YAML 示例——显式枚举约束泄露，
  违反"生成器看不到形式化约束"的变量控制。
- `demo/policy-pilot-java.yaml`、`templates/`（同样是策略/约束内容）。
- 泄露校验（沿用 R1）：副本内 `grep -rl "banned-api\|npath\|fan-out"` 应为 0 命中。

度量方式不变：真实 policy 跑 `check . -format json`，只看 `zz_eval_*` 生成文件上的
findings；`go build ./...` 作为 API 幻觉代理。**rsync 教训**：排除模式用锚定路径
（`/metricgate`），不能用裸 `metricgate`（曾误删整个 `cmd/metricgate/`）。

## 2. 三臂 prompt 模板

固定引导语（三臂相同，摘自 R3 Arm P）：

> 你将在本仓库的既有包内新增代码。可以阅读包内既有代码以了解风格与可用 API。
> 你的产出会经过与仓库生效的自动化检查完全一致的检查，代码提交前会被同样的规则
> 机械地检查并拦截。

- **L0 臂** = 任务文本 + 引导语。
- **L1 臂** = L0 + `metricgate constraints digest -pkg <目标包>` 输出
  （现行 digest：分层契约 / 跨层惯用法 / 禁用 API / 生效阈值 6 项 / 复用清单 / 流程红线，
  实测 internal/impact 约 644 tokens）。
- **L2 臂** = L1，但 digest 生效阈值节额外携带 npath-complexity ≤200 / fan-out ≤10 /
  boolean-params ≤2 三行。执行方式见 §5 附录 TODO；两条规则类检查
  （call-chain-depth 默认关闭、test-no-assert）不进阈值节：L2 不注入，其观察
  与 L1 相同（协议 §5 诚实边界）。

判读：L2−L1 差 = 新指标阈值注入的净效应；L1−L0 复核 R3 结论。

## 3. 任务集（n=10）

R1 命名惯例：新写任务 N01–N07，U04 变体 V1–V3。每个任务给出：目标包（全部落在
既有包内）、任务文本（跑批时逐字使用）、诱导机制、预期触发检查项。

### 3.1 新写任务 N01–N07

**N01 — 协议校验链（npath 诱导）**
- 目标包：`internal/selection`；生成文件 `zz_eval_n01.go`。
- 任务文本：「实现 `func ValidateRouteSpec(spec string) (string, error)`：校验并规范化
  路由 spec 字符串。spec 由若干段组成，每段可以是字面量、`{param}` 占位符、`*` 通配或
  `**` 通配；逐段校验合法性（占位符名必须非空且全为字母数字、通配不可与字面量混写等），
  全部合法返回规范化后的 spec，否则返回第一个非法段与错误。」
- 诱导机制：十几种段类型 × 逐段多条件校验，自然写成顺序 if 链 → NPATH 乘法爆炸。
- 参考"坏形状"（已验证）：`/tmp/m2b_probes/m01_npath.go` ValidateToken——10 个
  `if (kind==X && cond)`，引擎实测 **npath=59049、cc=21、cognitive=20**；npath
  远超 warn 200（注意：该形状 cc=21 会同时触发 cyclomatic error——跑批解读时按
  rule_id 分列，这正是协议要求的记录方式。若想让 npath 单独可见，坏形状可减到
  7 段：npath≈3^7=2187、cc=15 恰在错误线内；正式跑批用 7 段版）。

**N02 — 状态机展开（npath 诱导，switch 形态）**
- 目标包：`internal/model`；生成文件 `zz_eval_n02.go`。
- 任务文本：「实现 `func NextFindingState(current string, event string) string`：定义
  finding 生命周期状态机（new → triaged → confirmed/rejected → fixed → released，
  含重开与降级边），用显式 switch/if 逐状态 × 逐事件给出迁移，未知组合返回原状态。」
- 诱导机制：状态 × 事件矩阵展开 = 多臂 switch 求和 + 嵌套条件，NPATH 高而单分支
  复杂度不高——专门测 npath 抓"cc 看着不大"的路径爆炸（R3 后新增指标的补位价值）。
- 参考"坏形状"要点：≥8 状态 × ≥3 事件。未单独验证（同 N01 机制），跑批前若
  N01 已证实 npath fire，N02 可共享结论。

**N03 — 汇总编排（fan-out 诱导）**
- 目标包：`internal/status`；生成文件 `zz_eval_n03.go`。
- 任务文本：「实现 `func HealthSnapshot(rows []RuleRow) HealthReport`：对每条规则行
  依次调用包内既有的各维度统计函数（命中数、错误数、警告数、状态判定、门禁预览等，
  以包内实际导出 API 为准），汇总成一个报告结构返回。」
- 诱导机制：编排型任务自然逐项调用既有 helper → distinct callee > 10。
- 参考"坏形状"（已验证）：`/tmp/m2b_probes/m03_fanout.go` HealthReport——12 个
  helper 各调一次，引擎实测 **fan-out=12**（warn 10）。注意 honest 边界：真实任务里
  编排对象是包内既有 API，L1 臂的复用清单会点出这些函数名，fan-out 仍然会超——
  这正是"提示不能验证结果、gate 才能验证"的对照点。

**N04 — 导出渲染器（fan-out 诱导，第二形态）**
- 目标包：`internal/scorecard`；生成文件 `zz_eval_n04.go`。
- 任务文本：「实现 `func RenderCard(card Card, opts Options) string`：把评分卡渲染成
  文本，逐节调用包内既有的取数/格式化 helper（各节标题、分数行、阈值行、级别图标等，
  以包内实际导出 API 为准）。」
- 诱导机制：同 N03（多 helper 编排），但换成输出组装语义，检验 fan-out 触发不依赖
  单一任务措辞。
- 参考"坏形状"：同 N03 机制，不再重复验证。

**N05 — 报告开关（boolean-params 诱导）**
- 目标包：`internal/regress`；生成文件 `zz_eval_n05.go`。
- 任务文本：「实现 `func CompareRuns(baseline, current RunResult, verbose bool,
  includeInfo bool, groupByFile bool, failOnNew bool) Report`：对比两次运行结果
  生成回归报告；四个开关分别控制详细输出、是否含 info、按文件聚合、新违规是否置错。」
- 诱导机制：任务文本**显式要求 bool 参数**（这是与 R1 的关键差别——R1 任务不诱导，
  这里要测的是"模型会不会照写 flag arguments 而不重构为 options struct"）。
- 参考"坏形状"（已验证）：`/tmp/m2b_probes/m05_boolparams.go` RenderList——4 个
  bool，引擎实测 **boolean-params=4**（warn 2）。

**N06 — 过滤开关（boolean-params 诱导，options 化对照）**
- 目标包：`internal/selection`；生成文件 `zz_eval_n06.go`。
- 任务文本：「实现 `func PlanExclusion(paths []string, cfg ExclusionConfig) []Decision`
  （包内已有 `Decision` 与排除判定函数）：cfg 含 IncludeTests、IncludeVendored、
  IncludeHidden、InvertMatch 四个布尔偏好，逐路径判定是否排除。」
- 诱导机制：与 N05 同构，但偏好藏在 cfg 结构体里——**预期不触发** boolean-params。
  作用：阴性对照任务（n≥10 里必须有预期 fire 与预期不 fire 的配对，防止"指标见
  bool 就叫"的误读）。若 N06 也 fire，说明规则按结构体字段误判，属引擎缺陷，记入
  归因。
- 参考"坏形状"：无（阴性对照）。

**N07 — 表驱动测试（test-no-assert 诱导）**
- 目标包：`internal/dist`；生成文件 `zz_eval_n07_test.go`（+实现文件
  `zz_eval_n07.go`，实现体任意简单聚合即可）。
- 任务文本：「为包内 `Summarize` 写表驱动测试 `TestSummarizeValues`：构造 ≥3 组
  输入（空、单元素、多元素），逐组调用被测函数并输出结果。」
- 诱导机制：任务措辞**只要求调用与输出、不要求断言**——直指 LLM 特有失败
  （伪绿测试骗过 gate-PASS 验收，协议 §1 机制假设）。
- 参考"坏形状"（已验证）：`/tmp/m2b_probes/m07_noassert_test.go`
  TestSummarizeValues——t.Run 闭包内只有 t.Log，引擎实测 **test-no-assert fire**
  （消息明确指出 t.Run 闭包内的断言也会被计入豁免判定）。
- 注意：本规则 2026-10-08 才补齐 Go 侧实现（此前只有 Java），修复与测试见
  `internal/rules/go_test_no_assert.go` + `TestGoTestNoAssert`；Go 侧预算测试
  （internal/facts/budget_test.go）已同步（新规则使 constraints 必需段又增一行）。

### 3.2 U04 变体 V1–V3（架构诱导，B 类 n≥3）

U04 原型（R1）：「复用包内既有的影响面分析，返回定义数/引用数/受影响文件数」落在
`internal/impact` → 模型 import `internal/facts` → layer-violation。当前 policy
分层下（core = 27 包 may_depend_on [core]；adapter = report/store/git/facts/stubs/
sources/apisurface/bundle/calllog/hotspots/scorecard），诱导方向统一为：
**core 包内 import 任何 adapter 包**。

三个变体只换目标包与"既有能力"的说法，诱导机制不变：

- **V1**：目标包 `internal/impact`，任务即 U04 原文（复现基线，R1 已知该形状
  fire layer-violation）。
- **V2**：目标包 `internal/repomap`，「实现仓库结构摘要，读取各文件事实并返回
  按目录聚合的文件数」→ 诱导 import `internal/facts`。
- **V3**：目标包 `internal/risk`，「实现风险榜，结合最近运行记录与热点文件加权」
  → 诱导 import `internal/store` 或 `internal/hotspots`。
- 参考"坏形状"（已验证，V1 同型）：沙箱副本内
  `internal/impact/zz_eval_u04v1.go`——`facts.Collect(root)` 一次调用，引擎实测
  **layer-violation @ core(internal/impact) may not depend on adapter(internal/facts)**，
  消息已携带 WP2 port-adapter 修复指令（"fix: define a port in layer core …
  composition root"）。沙箱构建注意：副本必须用 `module metricgate` 的原 go.mod
  （internal/ 可见性依赖模块路径），不能另起模块名。

V 组度量要点：B 类判定不只看 layer-violation 是否出现，还看 L2/L1 是否把修复
拉向 port-adapter 惯用法（digest 跨层惯用法节 L1 已有）；M2 主协议 H1 的
≥2/10 由 V+N 全集合承担。

## 4. 已验证触发汇总（引擎实测 2026-10-08）

| 任务 | 检查项 | 实测值 | 阈值 | 结果 |
|---|---|---|---|---|
| N01 | npath-complexity | 59049（10 段版）/ 2187（7 段版，推算） | warn 200 | fire |
| N01 | cyclomatic-complexity | 21 | error 15 | 同时 fire（按 rule_id 分列解读） |
| N03 | fan-out | 12 | warn 10 | fire |
| N05 | boolean-params | 4 | warn 2 | fire |
| N07 | test-no-assert | TestSummarizeValues | warning | fire |
| V1 | layer-violation | core→adapter (facts) | warning | fire |
| N06 | boolean-params | — | warn 2 | 阴性对照（预期不 fire） |

验证命令：坏形状探针在 `/tmp/m2b_probes/`（独立 go module）与
`/tmp/m2b_u04v/`（metricgate 模块副本）上跑
`metricgate check <dir> -format json`。

## 5. 附录 TODO（L2 执行日）

1. `internal/digest/digest.go:77` thresholdRows 追加三行
   （`{"npath-complexity", "路径复杂度"}`、`{"fan-out", "扇出"}`、
   `{"boolean-params", "布尔参数"}`）——**仅 L2 执行时加**，平时保持 curated
   6 项（R3 digest 441-tok 预算红线）。加行后 thresholdSection 自动携带
   policy 生效值（当前 policy：fan-out warn 10 / boolean-params warn 2 /
   npath warn 200，error 均已 off——渲染为「error 已关闭，只告警」）。
2. 重跑 `go test ./internal/digest/...`（digest 断言可能数节）。
3. 用 N01/N03/N05 的 L2 digest 抽查三臂产物：L1 臂阈值节不得出现三项新行。

## 6. 诚实边界（承接协议 §5，任务集特有）

- fan-out 口径是 distinct callee 拼接文本（无类型解析），跨包同名调用会合并；
  N03/N04 的结论若为阴性，先检查是否口径太钝再下结论（协议预注册的处理顺序）。
- N07 诱导的是"措辞不要求断言"，与"LLM 主动写无断言测试"不是同一命题；归因时
  只能声称前者。
- N06 阴性对照若意外 fire，属引擎误判，修复前不得计入 H3 统计。
- V 组 n=3，B 类结论仍属方向性证据（M2 主协议 B 类 n≥5 的缺口靠 V+N 全集合部分
  弥补，不能单独下结论）。
- 参考坏形状验证的是"引擎能 fire"，不是"LLM 一定会写出该形状"；后者正是跑批
  要测的东西。
