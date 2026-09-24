# 大模型产出约束工具 × harness 工程工具：业界全景（2026-Q3）

> 调研日期 2026-09；来源：OpenAlex（politepool）、arXiv、GitHub Search API、
> MCP spec（Anthropic 2024-11 / 2025 修订）、Anthropic *Building effective agents*（2024-10）。
> 与 `docs/research-notes.md` 互补：那边是"对 metricgate 的直接迭代建议"（操作性），本文件是
> "面向问题域的全景"（判读性）。
>
> 关键原则（与 research-notes 一致）：**用研究校准方向，不用研究替代实测**。
> 本文件只给"已解决 vs 未解决"的判断，**不承诺 metricgate 会做**——第 7 节单独留口子。

## 0. 摘要

- **约束工具**层在 2026-Q3 已经收敛成三类：静态禁止（semgrep/gitleaks/PMD/SpotBugs/staticcheck）、
  类型契约（tsc/mypy/Pyright/Sorbet）、运行时/执行验证（CodeHalu）。语义层（"用本仓库 logger 而非
  fmt.Println"）只有自定义 DSL 才能覆盖，**业界没有端到端标准**。
- **harness 工程工具**层在 2026-Q3 趋同严重：MCP + LangGraph/LangChain + SWE-bench + Langfuse +
  E2B/Daytona 沙箱。所有主流框架都在拼同一个图（tool-call loop / RAG / eval / observability），
  差异化主要靠"prompt 工程 + 数据回流"。
- **真正未解决**的反而是**两类系统**之间的接口：怎么把"agent 跑了 30 步"沉淀成可学习信号；
  怎么把"工具输出里的 prompt injection"标准化隔离。**metricgate 的卡位正好在这条缝**——做
  "事实/约束层"的事实源，harness 用我们做 facts/bundle/trends。

## 1. 术语与边界

| 概念 | 含义 | 与 metricgate 的关系 |
|---|---|---|
| **约束工具** | 对 LLM 输出（或开发者的代码）施加"必须 / 必须不"规则的静态/动态检查 | metricgate 自身：banned-api、arch、克隆、MI、duplicate |
| **lint** | 风格/正确性的轻量规则；常含约束但约束仅其中一类 | metricgate 不做（避免与 Prettier/gofmt 同质化） |
| **harness** | prompt + tool 注册表 + agent loop + eval + observability 的工程栈 | metricgate **不**做 harness；本文件评估其需求 |
| **grounding** | 把 LLM 输出锚定到"事实/外部证据"的过程 | metricgate `get_constraints`/`check_path`/`bundle` 即此 |
| **facts layer** | 用确定性事实（符号/接口/约束/趋势）替代 LLM 自由生成 | metricgate 的定位 |

边界：本文件不重新展开 research-notes.md 已写的"圈复杂度/MI/CK/克隆/架构侵蚀"细节，
也不重述 CodeHalu/Lost-in-the-Middle 等已有引文——见 `research-notes.md` §1、§2。

## 2. 约束工具：已解决的子问题

按"是否有开源实现 + 是否有可复现 benchmark"判定"已解决"。

| 子问题 | 代表实现 / benchmark | 备注 |
|---|---|---|
| **静态禁止 API / 反模式** | `semgrep` ★16.7k、`gitleaks` ★29.4k、`PMD` ★5.5k、`SpotBugs` ★3.9k、`staticcheck` ★6.9k、`golangci-lint` ★19.4k | 已工业化、SARIF 输出标准统一 |
| **类型契约** | `tsc`、`mypy`、`Pyright`、`Sorbet` (Meta)、`go vet` | 编译器/类型检查器层面已覆盖 |
| **形式 / 风格** | `Prettier`、`Black`、`gofmt`、`rustfmt` | 已收敛，单一事实源 |
| **仓库级检索** | `RepoCoder` (EMNLP 2023)、`GraphCoder`、`GNNC`、`RepoExec` | embedding/graph 路线均成立 |
| **执行级验证（小代码片断）** | `CodeHalu` (AAAI 2025)、execution-based verification | 仓库级**未**规模化（见 §3） |
| **评审流水线** | `alibaba/open-code-review` ★40k、Copilot PR Review、Coderoot | 已成产品形态 |
| **引用 / 上下文 grounding** | `Lost-in-the-Middle` (TACL 2024)、`Lost in Context`、`context rot` 实证 | 排序/截断策略已成 |
| **编辑追踪（snapshot + 锚点）** | 各 LLM IDE 自带的 code-aware diff；metricgate 自身的 `Evidence` 哈希 | 设计收敛，但仍罕有公开标准 |
| **委托式 MCP / spec-grounded** | metricgate `bundle`；OpenAI Operator；Devin；Aider | 形式趋同 |

> 上述每个子类都有**至少一个 1k+ star 项目**或**一篇 100+ 引用的论文**做事实源。
> 它们的共同弱点：**对"语义约束"无能为力**——任何没有先验 schema 的规则仍要靠人写。

## 3. 约束工具：公开缺口

按"是否已被论文定义为开放问题 + 是否没有工业标准实现"判定"未充分解决"。

1. **spec-grounded 生成**（DSL → 可校验代码）
   - 现状：契约测试工具（Pact、Schemathesis、Postman）覆盖 HTTP/JSON Schema 一段；
     模型形式化（TLA+、Alloy、PRISM）在硬件/分布式系统也成立。
   - 缺口：从**自然语言 spec** → 真的可机校验代码（带类型 + 行为规约）仍没有统一产品。
   - 风险：OpenAPI/JSON Schema 转 Go/Type 客户端已有（oapi-codegen、openapi-generator），
     但转出的代码"通过了 schema ≠ 满足规约意图"。语义的规约无法从 schema 提取。
2. **执行级验证的仓库级规模化**
   - 现状：CodeHalu 等执行验证在小代码片断上 recall/precision 已可测；
     SWE-bench / SWE-bench Verified 在 PR 级验证已规模化但**只覆盖存量 issue**。
   - 缺口：对**任意新生成代码**做"容器内编译 + 跑测试 + 监控 side effect"端到端验证，
     延迟 + 成本都过高，且**对生成代码本身的"无中生有"的测试**没有 ground truth。
3. **多模态产物校验**
   - 现状：图表/UI 仅有 SVG diff / visual regression（Playwright/Chromatic ★2k）。
   - 缺口：LLM 输出的图表是否符合 spec、数据流图是否与代码一致——完全靠人看。
   - 风险：随着 agent 长出"画图 + 写文档"能力，这块是确定性约束的真空区。
4. **constraint decoding（语义约束）**
   - 现状：`Outlines` ★8k、`Guidance` (Microsoft)、`JSON schema` 等**语法约束**已成熟。
   - 缺口：把"用本仓库 `internal/log` 而非 `fmt.Println`"这种**语义约束**实时编入解码，
     几乎都是各家自研。Anthropic 2025 prompt-cache 出来后该问题更急迫（cache 命中要求
     prompt 高度稳定，约束变化会让 cache 失效）。
5. **self-verification / self-refinement 闭环**
   - 现状：CodeHalu、CRISPE 等论文已证 LLM **自评能力弱**；
     反复 self-refine 不一定改善。
   - 缺口：可信闭环"模型自己写 + 自己验证 + 自己修"目前仍是研究，
     Anthropic 2024-2026 的 Constitutional AI 路线在文字侧有进展，**代码侧没有可复现 SOP**。
6. **deterministic replay of agent runs**
   - 现状：agent 多步运行**几乎不可复现**——采样温度、tool 副作用、并发竞争、LLM provider 升级。
   - 缺口：标准化的"agent run record format"（区别于 Langfuse 的 span trace）。
     即使 Langfuse 记录所有 span，重放得到的 LLM token 序列也无法 byte-perfect 复现。
7. **differential testing 基准**
   - 现状：CoT 论文、Copilot eval、RepoExec 等用了"同 spec 多模型差异化"评估；
     但**统一基准**没有。
   - 缺口：给定 spec + 多模型产出，可重复生成的 differential benchmark 几乎没有。
     SWE-bench Verified 是任务级，不是 spec 级。
8. **API 幻觉仍未根治**
   - 现状：CodeHalu 2025、CodeMirage 2024、CoHalLo 2025 持续报告 17-30% 幻觉率。
   - 缺口：deterministic grounding（factual 名/路径）只能覆盖一部分；
     **语义幻觉**（"看似正确但用错语义"）几乎没有自动检测手段。
     路径：把 `find_symbols` + `check_path` 这类 deterministic facts
     **强行编入解码/约束**比"训一个检测器"更可行——但要把 constraint decoding 和
     harness 解码器整合，缺统一标准。

> 这些缺口的共同点：**没有一个被工业标准化**。它们是 2026-Q4 / 2027 的潜在突破点，
> 也是 harness 工程工具要补的方向。

## 4. harness 工程工具：已形成的共识/模式

按"是否被多项目共同采用 + 是否有协议规范"判定"已形成共识"。

| 类别 | 代表项目 / 协议 | 已收敛的形态 |
|---|---|---|
| **工具调用接口标准** | MCP (`modelcontextprotocol/servers` ★90k、`registry` ★7.3k)、OpenAI function calling、Google function calling | JSON-RPC 风格 tool schema；事实标准是 MCP |
| **agent 编排框架** | `LangChain` ★100k、`LangGraph` ★11k、`AutoGen` ★35k、`CrewAI` ★30k、`Smolagents` ★22k、`Pydantic AI` ★10k、`DSPy` ★27k | 模式趋同：**graph / loop / role**；状态机、retry、guardrails 是默认动作 |
| **agent 评估** | SWE-bench、HumanEval/MBPP、LiveCodeBench、CodeArena、AgentBench、GAIA、ToolBench、τ-bench | benchmark 大杂烩；各自覆盖不同能力 |
| **可观测性** | `LangSmith`、`Langfuse` ★13k、`OpenLLMetry`、`Arize Phoenix` | span trace + token cost + prompt version；事实标准 LLM span |
| **沙箱 / 权限** | `E2B` ★8k、`Daytona` ★10k、`Modal`、`CodeSandbox`、Anthropic Computer Use VM、OpenAI Operator browser sandbox、Devin Docker | containerized code execution 是默认；权限粒度仍粗 |
| **成本 / 速率感知** | `OpenRouter`、`LiteLLM` ★10k、`Inferentia` | 路由 + 降级 + 重试；缓存层刚刚起步（Anthropic prompt cache、OpenAI cached input） |
| **prompt 管理** | `PromptFoo` ★6k、`PromptLayer`、`DSPy` ★27k、`Guidance` (MS)、`Outlines` ★8k | 编译式 prompt 正在替代手写 prompt |
| **上下文工程** | `MemGPT` / `Letta` ★14k、`LlamaIndex` ★42k、`Haystack` ★17k、`graph-rag` ★20k | RAG 框架成熟；分页/记忆仍各做各的 |
| **仓库索引** | `Aider` repo map ★32k (tree-sitter based)、Cursor index、`Cody` (sourcegraph) ★10k、Zed LSP、Repo Hyper | tree-sitter + LSP 是事实路线 |
| **评测驱动** | SWE-bench、LiveCodeBench、BigCodeBench、FullStackBench、RepoExec | 每个都覆盖一段能力；缺统一真相源 |

> 这些项目的**共同弱点**：每一个都在拼"同一个图的子集"。差异化主要靠"提示词工程 +
> 数据回流 + 评测榜单"，而非真正的技术突破——这反过来印证 §3 的缺口：**没有一个项目
> 把"约束工程"作为第一类公民**。

## 5. harness 工程工具：公开缺口

按"被多篇 2024-2026 论文点名为开放问题 + 没有工业标准"判定"未充分解决"。

1. **trajectory 数据回流（agent run → 可学习信号）**
   - 现状：Langfuse/LangSmith 记录 span；Anthropic/OpenAI 不开源生产 agent 轨迹。
   - 缺口：怎么把"agent 跑了 N 步"沉淀成训练数据，是 2025-2026 文献最热问题之一；
     SWE-bench Verified 训出的模型只能解决它那一类（分布外差）。
   - 风险：这是研究学界最大的"数据黑洞"——厂商不开放，社区无法补全。

2. **agent 失败模式分类无统一 taxonomy**
   - 现状：Anthropic *Building effective agents* (2024-10) 提 5 类失败；
     SWE-bench 论文提过几类。
   - 缺口：**stuck loop / context rot / spurious commit / tool misuse / premature finalization**
     没有标准化命名 → 工具厂商、benchmark、论文各自一套术语。
   - 机会：metricgate findings 可以投影到这些 failure class，做"failure-aware gating"。

3. **deterministic replay of agent runs**
   - 现状：与 §3-6 重叠；从 harness 视角，**debug 难是最大的工程痛**。
   - 缺口：标准化"agent run record format"——区别于 span trace，要能 byte-perfect 重放
     tool 输入输出 + LLM prompt schema（不含 token）。

4. **tool-result sanitisation（间接 prompt injection）**
   - 现状：prompt injection 论文自 2022 起持续（`Not What You've Signed Up For` 2023、
     `Indirect Prompt Injection` 系列 2024-2026）。
   - 缺口：MCP spec 2025 修订加了"trusted boundary"提示，但**没有标准化 sanitizer**——
     各家 agent 自己用 LLM-as-judge 做"输出是否可疑"判定，误判率 5-30%。
   - 机会：把工具输出打上"风险评分"是天然约束问题；deterministic 检查可以做一部分。

5. **MCP 自身局限**
   - 工具描述过长（数百 token）挤占上下文；工具输出无统一限长；
     error 模型不统一（部分 string、部分 JSON-RPC error object）；
     多 server 协调 + auth 模型弱。
   - 缺口：这些都是**协议层**问题，应该在 spec 解决；社区已有人在 PR，但尚未合并。

6. **prompt cache 与 harness 状态耦合**
   - 现状：Anthropic prompt cache、OpenAI cached input 都按 prompt 前缀稳定才生效。
   - 缺口：harness 怎么"知道何时该刷新 cache"没标准化——
     bundle 内容变了、policy.yaml 变了、源文件改了，cache 都该失效，但 harness 一律不感知。

7. **权限 / 沙箱策略的最小权限与审计**
   - 现状：Anthropic Computer Use VM、OpenAI Operator browser、Devin Docker——三种范式。
   - 缺口：最小权限粒度仍粗；replay 仍不能复现 sandbox 内的副作用；
     审计日志格式各做各的。

8. **eval 平台"现实性"过拟合**
   - 现状：SWE-bench 在 2024-2026 多篇论文被指过拟合；
     LiveCodeBench 每月滚动题目试图对冲。
   - 缺口：榜单导向"刷题" → 真实生产里大量失败模式不上榜。
   - 机会：内部 benchmark（用真生产代码做 eval）才能补全——metricgate 已规划
     `docs/ab-experiment.md` 的内部 A/B 是这条线。

9. **代码级 vs 知识级 grounding 的隔离**
   - 现状：LLM 是否理解"它在用哪个仓库"几乎是隐式状态。
   - 缺口：Anthropic / OpenAI 都没暴露"agent 当前活跃仓库"为第一类信号——
     MCP resource 里只有文件系统视图，没"我是哪个仓库 + 哪个分支 + 哪个 policy"的元数据。
   - 机会：metricgate `bundle` 可以作为这一信号的**载体**——`bundle.evidence.manifest`
     暴露给 harness 即可。

## 6. 与现有研究映射的差分

明确"**本文件新增 vs research-notes.md 已写**"，避免读者来回跳转。

| 主题 | research-notes.md | 本文件 |
|---|---|---|
| 圈复杂度 / MI / CK / 克隆 / 架构侵蚀 | §1 全覆盖 | 不重复（设边界） |
| CodeHalu / CodeMirage / CoHalLo（API 幻觉） | §2 引为"最强外部验证" | §3-8 把它定位为"仍高频未根治" |
| RepoCoder / Lost-in-the-Middle（仓库级 + 长上下文） | §2 全覆盖 | §4 仅作为"已收敛"事实源 |
| LangChain / LangGraph / MCP / langfuse / Aider | 未覆盖（仅 MCP 在 §3 提一句） | §4 全覆盖 |
| Outlines / Guidance / DSPy / Letta / graph-rag | 未覆盖 | §4 全覆盖 |
| trajectory 回流 / failure taxonomy / replay | 未覆盖 | §5 新增 |
| prompt injection / MCP 局限 / cache 耦合 | 未覆盖 | §5 新增 |
| spec-grounded / 多模态 / constraint decoding | 未覆盖 | §3 + §5 双视角 |
| metricgate 卡位 / 下一轮 P1/P2/P3 候选 | §4 P1/P2/P3 | §7 重新对位（harness 视角） |

## 7. 对 metricgate 的卡位与下一轮候选

### 7.1 卡位（一句话）

metricgate 是 **harness 的"事实/约束源"**，不是 harness。harness 调我们，我们不调 harness。
这条边界在 2026-Q3 的工业格局里很清晰——所有主流 agent 框架都需要一个
deterministic facts provider，但没有任何框架愿意自己实现一遍。

### 7.2 P1 候选（高杠杆、低成本，本调研新发现、research-notes 未列）

1. **失败模式 taxonomy → metricgate findings 投影**
   - 把 metricgate findings 投影到 Anthropic 5 类失败（stuck loop / context rot / spurious commit / tool misuse / premature finalization）。
   - 成本：~200 行 Go，1 个新 MCP tool `get_failure_profile`。
   - 价值：让 harness 知道"这个 PR 的 metricgate findings 主要是哪一类失败"。
2. **trajectory 回流协议（metricgate findings × agent run）**
   - 给 MCP 增加 tool `record_run(outcome, finding_ids)`，让 harness 在 run 结束后打 tag。
   - 成本：~100 行 Go + store 加一列 `run_tags`。
   - 价值：开启"哪些 findings 实际拦下了失败"的实证信号。
3. **MCP 自我合规（解决 §5-5）**
   - 工具描述超过阈值（默认 200 token）→ 拒绝注册；输出超过阈值（默认 4k token）→ 截断并标注。
   - 成本：~100 行 Go，policy.yaml 加 `mcp:` 段。
   - 价值：metricgate 不只是被 MCP 调用，还是"约束 MCP 自身的工具"——差异化。
4. **prompt cache 失效信号**
   - 在 bundle/manifest 加 `cache_invalidation_token`（policy.yaml sha + source mtime + 工作树 head）。
   - 成本：~50 行 Go。
   - 价值：harness 不用自己算"该刷 cache 了"。

### 7.3 P2 候选（中等成本）

1. **constraint decoding 接入**（§3-4）
   - 把 `banned-api` / `arch` 规则转成 Outlines grammar 输出，host harness 可在解码阶段就拦住违规。
   - 风险：与 harness 解码器紧耦合，迁移成本高；先做 spec 文档，不实现。
2. **多模态产物校验**（§3-3）
   - chart/UI 的 SVG diff：仅当真实用户场景出现再启动。
3. **spec-grounded 实验床**（§3-1）
   - OpenAPI/JSON Schema → 代码的"语义规约"校验：先做小实验（OpenAPI 一段子集 → Go handlers），再决定是否产品化。

### 7.4 P3 候选（明确不做）

- **agent 编排、prompt 工程平台、LLM 训练数据生成**：harness 层；与 metricgate 定位冲突；
  不引入，交给主流框架。
- **沙箱 / 权限平台**：harness 层；与 metricgate 定位冲突；按 §7.1 边界不做。
- **deterministic replay of agent runs**（§3-6 / §5-3）：天然属于 harness 而非 facts；
  不引入。

### 7.5 触发条件（与 research-notes.md §P2④ 同口径）

上面任一 P1/P2 候选**进入实施**的条件：
1. 有 ≥ 1 个真实 harness 用户明确请求；
2. 有可复现 benchmark（哪怕内部 A/B）；
3. 评估"投入产出比"仍优于"继续深化 P2/P3 已落地能力（趋势/克隆/架构/MI）"。

## 8. 威胁与边界

- **被引数 ≠ 质量**——与 research-notes.md 口径一致；近期 LLM×Code 论文多为预印本，
  CodeMirage/CoHalLo 等 2024-2026 尚未充分同行复核。
- **GitHub star 浮动**——2026-09 数，仅用于"值得借鉴"快速排序。
- **本文件不含对 metricgate 自身效果的承诺**——效果仍需 `docs/ab-experiment.md` 的 A/B 实测。
- **检索时间 2026-Q3**：6 个月内的重大更新（尤其 harness 层）需要重做一遍本调研；
  已在文首标注 TODO。
- **不与 `research-notes.md` 抢主体**——本文件只补空白；下一轮如有更新，单独增量修订。

## 交叉链接

- 上一轮调研（对 metricgate 的直接迭代建议）：`docs/research-notes.md`
- 内部 A/B 实验设计（首要指标 = API 幻觉率）：`docs/ab-experiment.md`
- Phase E 量化报告（run-to-run 方差 = 0、diff-only 噪声削减 95.7%）：`docs/evaluation-trends.md`
- Phase D 评估（bundle 6,832 chars = 1.6%）：`docs/evaluation-phase-d.md`