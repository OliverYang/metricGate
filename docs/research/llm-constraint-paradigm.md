# LLM 生成代码的约束新范式

> 前置阅读：[llm-code-constraint-research.md](llm-code-constraint-research.md)（9 个论文方向 + 采纳决策）、[../evaluations/evaluation-base-rate.md](../evaluations/evaluation-base-rate.md)（R1 实测）。
> 本文是调研的收束：把 9 个方向收拢成一个可工程化的范式，并给出每一步的真实命令与数据契约。配套的条款级规约见 [../constraints/llm-code-acceptance-spec-v1.md](../constraints/llm-code-acceptance-spec-v1.md)。

## 0. TL;DR

旧范式的约束是**一份文档**：写给人看，靠人记住，靠 CR 和 CI 事后拦截。
新范式的约束是**一个闭环**：规约可执行（policy.yaml），接地进上下文（getConstraints），生成后确定性验收（check -diff -scorecard，0 token 毫秒级），失败以结构化 JSON 回灌生成循环让 agent 自修，违规统计再回流演进来一轮 policy。

一句话：**约束的执行主体，从"人（记忆 + CR）"转移到"生成循环本身"。**

## 1. 为什么旧范式失效

三个结构性变化，两个有本地实测，一个有行业数据：

1. **生成成本趋零，约束的"拦截价值"重算。** 代码不再稀缺，稀缺的是"对"的代码。返工的边际成本降到接近零，但**没人发现**的失败成本不变 —— 约束的价值从"防止写错"变成"快速证伪"。
2. **LLM 失败是系统性的、可分类的。** 不是人的粗心（风格 typo），而是四类系统性偏差（见 §3）。R1 实测：policy-blind agent 7/22 = 31.8% 的任务至少违反一条仓库约束，error 级 9.1%；度量阈值类 18.2% 全部 error 级。
3. **CR 与 CI 是旧节拍，喂不起新产能。** GitClear 2025（36,894 开发者）：copy/paste 行数增速超 2024 年预测 6 个百分点，2024 年是有史以来第一次 copy/paste 超过 moved lines（重构活动的代理指标），缺陷率随 AI 采用上升。生成速度提高了一个量级，审查带宽没变 —— 瓶颈从"写"移到了"验"。

结论：约束必须前移到生成循环里，且验收必须是机器可执行的数字，不能是自然语言评论。

## 2. 新范式：五个支点

每个支点 = 一个论文方向（或多个）的工程化落地，全部映射到 metricGate 已有能力：

| # | 支点 | 治什么 | 论文方向 | metricGate 落地 | 状态 |
|---|---|---|---|---|---|
| 1 | **前置接地** Grounding | 类1 不认识仓库 | 方向6（RepoCoder/CodePlan）+ R1b 实证 | CLI `constraints digest`（任务级约束摘要，确定性生成，均价 441 token）+ MCP `getConstraints` / `getRepoMap` / `findSymbols` | ✅ 已实测（R3：31.8%→4.5%） |
| 2 | **生成侧 rails** | 类2 过度工程 + 类3 伪完成的输入侧 | 方向7（NeMo Guardrails 2310.10501） | `preset: agentic`：lint 类放松（function-length 80/160 等），伪完成信号收紧（empty-block/unknown-dependency = error，todo-comment 启用 = warning），banned-api = error | ✅ 本期已落地 |
| 3 | **确定性验收** Acceptance | 全部四类的出口判据 | 方向8（GitClear 质量退化度量） | `check -diff -scorecard`：四类失败 → 10 个计数器（见 §4 契约）；0 token，毫秒级 | ✅ 本期已落地 |
| 4 | **结构化修复回路** Repair | 闭环的"修"半边 | 方向1 采纳为核心（InferFix 2303.07263 / Self-Debug 2304.05128 / CodeCureAgent 2509.11787） | findings JSON 就是机器可读的修复指令；scorecard JSON 是验收数字；循环直到达标（§4 协议） | ✅ 通道已备，R2 实验排期 |
| 5 | **度量与演进** Evolution | 约束系统自身的迭代 | 方向8 + 约束识别三层架构（[../constraints/constraint-mvp-design.md](../constraints/constraint-mvp-design.md) Layer 3） | scorecard 时间序列（SQLite trend）、违规聚类 → FP 审计 → policy 调优；R2 量化闭环价值 | ⚠️ 部分已有 |

支撑关系：1 治"输入不知道"，2 治"生成本身"，3 治"出口判定"，4 治"失败后怎么办"，5 治"规约从哪来、往哪去"。**1–4 组成单次闭环，5 让闭环跨会话进化。**

### 2.1 为什么修复回路放在 agent 层而不是解码层

方向4（constrained decoding：Picard/Synchromesh/GCD）被明确拒绝：需要进入推理栈，与"模型无关的本地闸门"冲突，且已有越狱反例（2606.11817）。工程等价物是把同一约束放到 agent 循环里 —— generate → check → repair → re-check。这保留了 NeMo Guardrails 的"rails"思想，但 rails 落在**代码层**（AST 事实），不落在 prompt 层（可被绕过）或解码层（需模型栈）。

## 3. 四类失败模式 → 约束映射（附实测基率）

这是整个范式的骨架：约束配置按失败类组织，而不是按 lint 传统分类。

| 失败类 | R1 实测基率 | 约束手段（规则/工具） | 落点支点 |
|---|---|---|---|
| 1 不认识仓库（幻觉依赖、无视分层、重造已有实现） | 分层 4.5% 自然发生；幻觉依赖 0%（任务集产物，非结论）；重造 100%→0% 由 R1b 证明是措辞产物 | `unknown-dependency` error、`layer-violation` error、near-clone（重造检测）、`banned-api`、接地上下文 | 1+3 |
| 2 过度工程（防御性嵌套、复杂度膨胀） | **18.2%，最高产，全部 error 级** | cyclomatic/cognitive/nesting/params 阈值 = error | 2+3 |
| 3 伪完成（stub、TODO、panic("not implemented")、空块） | todo/空块常规出现 | `empty-block` error、`todo-comment` warning（agentic 启用）、stubs 计数（scorecard `incompleteWork`） | 2+3 |
| 4 破坏稳定面（API 破坏、影响面失控、度量悄悄变差） | 未直接测量（R1 任务无此诱导） | `api diff` 破坏性变更闸门、`-regress` 度量回归、`getImpact` 影响面、`testsTouched` 计数 | 3 |

R1b/prompt-vs-gate 的位置：它们证明**接地（支点1）决定重造率**（钉死包名+函数名 → 0% 复用；放开 → 100%），且**提示解决不了的尾部由闸门兜底**（一句复用提示把重造 10/10 → 2/10，剩下的 2 例是 prompt 结构上抓不住、静态分析抓得住的）。提示与闸门是互补层，不是替代关系。

## 4. 工程协议（真实命令与数据契约）

单次闭环的完整协议，全部命令已在仓库可跑：

```
┌─ 0 前置：注入约束与仓库事实（MCP: getConstraints / getRepoMap / findSymbols）
├─ 1 生成：agent 按 agentic 档语义产出代码
├─ 2 验收：./metricgate check -diff -base HEAD -regress -scorecard
│         （-format json 时 findings JSON 走 stdout，scorecard JSON 走 stderr，
│           stdout 保持机器可读 —— 两条通道互不污染）
├─ 3 判定：scorecard 红线计数全 0 → 通过；否则 → 4
├─ 4 修复：agent 读 findings JSON + scorecard JSON，定向修复，回到 2
└─ 5 追加：改导出面时跑 api diff 闸门
   ./metricgate api snapshot -out F    （基线快照）
   ./metricgate api diff -base F [path]（破坏性变更 = 阻断）
```

**scorecard JSON 契约**（internal/scorecard，-format json 时输出到 stderr）：

| 字段 | 含义 | 对应失败类 |
|---|---|---|
| `changedFiles` / `changedLines` | diff 规模 | — |
| `testsTouched` | 是否触碰测试文件 | 类4 |
| `structureViolations` | layer-violation / import-cycle / sdp-violation / forbid* | 类1 |
| `dependencyViolations` | unknown-dependency | 类1 |
| `bannedApiCalls` | banned-* 前缀 | 类1 |
| `thresholdBreaches` | 度量阈值（不含回归） | 类2 |
| `metricRegressions` | 相对 base 变差（-regress） | 类4 |
| `clonedCode` | function-clone / near-clone / clone-drift | 类1（重造） |
| `incompleteWork` | todo-comment + diff 内 stubs（empty-body / panic-not-implemented） | 类3 |
| `parseErrors` | 解析失败（容错解析的诚实信号） | — |
| `topRules[].{rule,count}` | Top 违规规则 | 全部 |

**达标判据**：红线 = `structureViolations`、`dependencyViolations`、`bannedApiCalls`、`metricRegressions`、`parseErrors` 全 0；提示级 = `incompleteWork`（每条须带 issue 引用，上限 1）、`thresholdBreaches`、`clonedCode`（需人工判断是否合理复用变体）。条款级定义见[验收规约 v1](../constraints/llm-code-acceptance-spec-v1.md)。

## 5. 与 9 个研究方向的映射

| 方向 | 决策 | 归属支点 |
|---|---|---|
| 1 静态分析反馈修复回路（InferFix/Self-Debug/CodeCureAgent/PredicateFix/nullability） | **采纳核心** | 4 |
| 2 best-of-N 重排序（Coder[Reviewer]：似然偏好退化解 → 需外部判别器；2408.13976 执行反馈） | 改编 P1（check --rank / MCP rank_candidates，metricGate 当无执行判别器） | 3 的扩展 |
| 3 API/包幻觉（USENIX Sec 2025 slopsquatting 2406.10279） | 已采纳（unknown-dependency）；P2 加 pom.xml/go.mod 清单校验 | 1+3 |
| 4 受限解码（GCD 系；2606.11817 越狱反例） | **拒绝**（需推理栈，与模型无关闸门冲突） | — |
| 5 形式规约（SpecGen/ClassInvGen/Clover/AlphaVerus） | 思想采纳：policy.yaml = 轻量规约，试点流 = 低成本实现 | 5 |
| 6 仓库级接地（RepoCoder/CodePlan） | 已有（四个 MCP 工具） | 1 |
| 7 guardrails（NeMo 2310.10501） | **采纳本期**（agentic 档 = 代码层 rails） | 2 |
| 8 质量退化度量（GitClear 2025） | **采纳本期**（scorecard 依据） | 3+5 |
| 9 静态+LLM 混合评审（2502.06633） | 改编 P1（bundle + scorecard 摘要页） | 3 的扩展 |

## 6. 诚实边界

- **纯语义正确性（公式对不对、意图符不符合）静态不可判** —— 那是测试与人类评审的领地。新范式不做这个主张。
- **业务轴 = 声明式规则合规率**：团队把业务约束写进 policy，闸门给出合规数字。规约覆盖不到的约束，v1 明确标 ❌ 待建，不假装覆盖。
- R1 的幻觉依赖 0% 是任务集的产物，不是"LLM 不幻觉"的结论；类1 的依赖约束仍然全开。
- scorecard 是**报告**，不改门禁语义（gate 仍由 policy 的 fail_on 决定）；达标判据是团队消费 scorecard 的建议阈值，不是内置强制。

## 7. 竞品对照：为什么这是差异化

- NeMo Guardrails / Guardrails AI：rails 在 **prompt/LLM 层**，可被绕过、需挂模型栈；metricGate 的 rails 在**代码层**（AST 事实），模型无关、0 token、毫秒级。
- SonarQube / 传统质量门：分钟级 CI 事后拦截，无增量、无架构约束、无接地上下文；metricGate 是**生成循环内**的毫秒级验收 + 前置接地。
- 四个市场缺口（[positioning](../strategy/positioning.md)）中，本范式直接命中两个：LLM 生成代码验证、增量感知架构检查。R1/R1b/prompt-vs-gate 三级实验证据链是竞品没有的。

## 8. 下一步

1. ~~R2 修复回路实验~~ **✅ 已完成（2026-09-29）**：基率复验 31.8%，7/7 修复达标（6×1 轮 + U09×2 轮），修复 token 1.72× 生成成本，0 功能丢失；报告见 [../evaluations/evaluation-r2-repair-loop.md](../evaluations/evaluation-r2-repair-loop.md)。支点 4 的量化证据已闭环。
2. ~~R3 前置接地实验~~ **✅ 已完成（2026-09-29）**：`constraints digest` 命令落地 + 22 任务实测——首过违规 31.8%→4.5%，P+B 复合终态 0/22；报告见 [../evaluations/evaluation-r3-grounding.md](../evaluations/evaluation-r3-grounding.md)。支点 1 的量化证据已闭环。
3. P1：`check --rank`（候选实现无执行排序）+ scorecard 摘要页进 bundle（方向 2/9 落地）。
4. P2：pom.xml/go.mod → `dependencies.allow` 清单生成（方向 3 补全）；架构类 digest 增强仍排队：结构惯用法提示（收 U04 型架构漏网）。ban 级替代写法已提前落地（2026-09-30）：banned-api 新增 `use:` 字段，findings 与 digest 自动渲染“替代写法：…”，事故卡正例栏直接贯通。
5. 用[验收规约 v1](../constraints/llm-code-acceptance-spec-v1.md) 作为规范输入跑第二次端到端试点，试点结论回流修订 v2。

---
*撰写：2026-09-29。工程事实核实：scorecard JSON 字段、api snapshot/diff 用法均从源码与 main.go 用法行核实；实验数据引用自 docs/evaluations/ 实测文档。*
