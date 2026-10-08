# 同级别竞品能力调研与吸收清单（2026-09）

调研对象：与 metricgate 同层或相邻的**确定性代码分析 / 约束 / 上下文供给**产品。
目标：守住「LLM 生成代码的确定性约束层」主线，找出**可吸收进本产品**的能力，并给出下一步指导。

检索时间 2026-09；外部数据来自官方文档 / README / 发布说明（GitHub、产品文档站）。
本文件是**方向校准**，不承诺效果；效果仍以 `docs/ab-experiment.md` 的真实 A/B 为准。
**该方向为何有价值、以及用什么证据（按什么顺序）证明它，见 [`docs/value-proof.md`](value-proof.md)。**

---

## 0. 结论摘要（TL;DR）

1. **主线赌注被市场验证了。** SonarQube 2026.4 推出 **"Sonar way for Agentic AI" 质量门** + agentic-security 规则族 +
   架构管理；ast-metrics 直接把 MCP server 定位成"给 AI agent 架构感知"。**"给 agent 的确定性约束层"已是公认需求**。
2. **但"更多指标"这条路已经走不通。** SonarQube 把架构管理 + AI 门变成**随扫描免费附带**；ast-metrics 覆盖 7 语言、
   HTML 报告、bus factor、测试质量、社区检测、baseline、review，且同样有 MCP。比覆盖面必输。
3. **metricgate 唯一的护城河是通道，不是指标**：本地零配置、零 token、毫秒级、MCP-first、
   **spec-grounded bundle**、**可机器校验的 evidence anchor**、**policy-as-data**，
   以及**面向"生成循环"的即时反馈**（每次编辑后 `check_code`）。
4. **最该补的不是新度量，而是三件贴合 agent 工作流的事**：
   - **引用索引 + 影响面**（改这个符号会破坏什么）——当前只有定义索引，无调用图；
   - **回归检测（worsened vs base）**——当前 diff-only 是绝对阈值，不是"变差了"；
   - **agentic gate 预设 + 新增依赖风险**——正面回应 SonarQube 的进场，但以"本地零配置"差异化。
5. **明确不碰**：类型/数据流/taint（CodeQL/Semgrep/Joern 领域）、向量检索、LSP 重命名、LLM 评审编排、
   HTML dashboard 服务端。这些要么属集成路线（Phase B），要么属宿主侧。

---

## 1. 主线定义与筛选器（先立规矩，再看竞品）

metricgate 的主线（README 原文）：**本地、毫秒级、零 token 回答"这段代码满足我们的结构约束吗"**，
是 LLM 评审工具缺的确定性层；同一个 policy 既驱动闸门，也驱动生成约束。

吸收任何能力前，过三道筛（沿用 `docs/roadmap-research.md` §2）：

1. **树语法可达 + 输出要进统一 findings / context / bundle** → 可自研（边际成本是一次遍历）。
2. **需要类型系统 / 数据流 / 全程序分析** → 永远不重造（集成或不做）。
3. **必须服务三条 LLM 触点之一**：生成前约束（context/constraints）、生成后校验（check_code/check_path）、
   委派评审（bundle）。**不服务任何触点的能力，哪怕竞品有，也不做。**

> 这三条同时是"拒绝清单"：竞品能力强 ≠ 该做。CodeQL 有 498 条安全查询，我们一条都不抄。

---

## 2. 竞品全景：五个层次

按"离 metricgate 主线的距离"分层。**只有 L1/L2 是同层竞争，L3 是集成对象，L4 是最像的邻居，L5 是宿主（互补）**。

### L1 确定性度量引擎（同层）

| 产品 | 语言 | 核心能力 | 与 metricgate 的关系 |
|---|---|---|---|
| **cccc**（Rust） | 20+（含 Go/Java/TS/Rust/Python/C/C++/Kotlin/Scala…） | 认知+圈复杂度；**共享 IR（`cccc-core`）+ 每语言适配器 crate**；**逐字节一致的缓存**（stat→git blob→content hash 三级验证）；**分布统计**（p50/p90/p95，非均值）；top-N 排名；阈值门禁 exit code；官方 GitHub Action + octocov | **架构同构**（我们的 NodeKindSet/IR 是同一模式）；缓存与分布统计是可吸收项；**无 MCP、无 AI、无架构/重复/约束**——纯度量 |
| **ast-metrics**（Go） | 7（Go/PHP/Python/Rust/Java/C#/TS） | 复杂度+MI+LOC+Halstead；**架构**（耦合、不稳定性、社区检测、禁止依赖）；**活动度量**（commit 历史、bus factor）；**测试质量**；**风险评分**（too bugged / too many responsibilities / too much coupling）；**baseline 快照**；**`review` 只报 new/worsened**；rulesets；报告 HTML/JSON/MD/SARIF/OpenMetrics；**MCP server 9 工具**；TUI；watcher | **覆盖面最广的直接竞品**。差在：非"约束层"（无 policy-as-data、无 evidence anchor、无 bundle、无生成前约束包）；强在：语言数、行为度量、报告、review 语义 |
| **SonarQube Server 2026.4** | 30+ | 规则引擎 + **质量门** + **新代码定义** + 重复检测 + **增量 taint**（Java/C#）+ **架构管理**（定义允许依赖、偏离报为标准 issue）+ **"Sonar way for Agentic AI" 门** + **agentic-security 规则族** + 门遵守率看板 | **最大的威胁与最强的验证**。它把"架构约束 + AI 专用门"做成**随扫描免费附带**；但它重（服务端、分钟级、需部署），与"本地毫秒零配置"不冲突 |

### L2 约束 / 架构专用工具（同层，窄）

| 产品 | 形态 | 能力 | 备注 |
|---|---|---|---|
| arch-go / go-arch-lint | Go CLI | 分层 / 禁止边 | 我们已超越（多语言 + policy 统一） |
| ArchUnit / Dependency-Cruiser / import-linter | Java / JS / Python 测试库 | 架构规则断言 | 语言绑定、需写测试代码 |
| CodeScene | SaaS | **行为分析**：hotspots、**change coupling（历史逻辑耦合）**、knowledge map、bus factor、CodeHealth、AI 重构 agent（ACE） | **change coupling 是我们完全没有的维度**（结构图看不出"总是一起改"） |

### L3 语义 / 深度分析引擎（集成对象，不自研）

| 产品 | 能力 | 我们的决策 |
|---|---|---|
| **CodeQL** | 语义引擎，代码→可查询数据库，Default suite **498 条安全查询**，跨仓 | 需要建库、分钟级、服务化 → **不采用**（roadmap 已定） |
| **Semgrep** | 模式匹配 + **interfile taint**（Pro，1.158 重写后快 75%），30+ 语言 | 集成候选（Phase B，可选） |
| **Joern / CPG** | **Code Property Graph = AST + CFG + PDG** | 数据流是独立研究线 → 集成，不重造 |
| SpotBugs / PMD / Infer | 字节码 / 源码级缺陷 | Phase B 适配器对象 |

### L4 代码索引 / 上下文供给（最像的邻居，MCP 侧直接竞争）

| 产品 | 索引形态 | 关键能力 | 对我们的启示 |
|---|---|---|---|
| **Sourcegraph** | SCIP（LSIF 后继） | 跨仓符号/引用/实现；**唯一支持跨仓追踪** | 重（部署 + 20–40 min 首次索引）→ 不做跨仓 |
| **CodeGraph** | 本地知识图谱（符号 + 调用边） | **一次调用给出调用路径 + blast radius**；文件变更自动同步；单仓、免费、本地 | **blast radius 是我们要补的**（我们只有包级 import 图） |
| **Serena** | LSP（40+ 语言） | **符号级操作**：find symbol / **find referencing symbols** / type hierarchy / find implementations / rename / replace symbol body / insert after symbol；memory system；YAML 配置 modes | **引用查询是我们要补的**；但 LSP 重命名/类型层次属 LSP 领域，AST 层不重造 |
| **aider repo map** | tree-sitter + **图排序（PageRank）** | 在 **token 预算**内选"最常被引用的关键符号"，只给签名不给全文 | **"重要性排序 + 预算"正是我们 context pack 缺的整仓视野** |
| CocoIndex Code | tree-sitter AST 分块 | 本地、token 省 70%；**检索为中心，不做调用图** | 分块思路我们已有（facts） |
| repomix / code2prompt | 全仓打包 | 打包成 AI 友好文件 + **token 计数** + 模板 | 我们是"预算内的约束子集"，比它更精 |

### L5 LLM 评审 / Agent（宿主侧，互补而非竞争）

| 产品 | 能力 | 关系 |
|---|---|---|
| **Greptile** | codebase graph + agent swarm + **confidence score** + 序列图 + 测试生成 + `.greptile/rules` 自定义规则 + 从评论学习 | 宿主侧；**它需要 facts provider**，正是我们的位置 |
| CodeRabbit / Qodo / Sourcery / Graphite / Ellipsis | LLM PR 评审 | 同上；非确定性、按 seat 收费 |
| **Open Code Review**（alibaba） | 确定性脚手架 + 宿主推理的**委托模式** | 已在 `docs/open-codereview-reference.md` 深度对照，本文件不重复 |

---

## 3. 能力矩阵：谁有什么，我们缺什么

「✅有 / ⚠️部分 / ❌无」。metricgate 列为**代码核实**结果（非文档推断）。

| 能力 | cccc | ast-metrics | SonarQube | CodeScene | CodeGraph/Serena | **metricgate 现状** |
|---|:--:|:--:|:--:|:--:|:--:|---|
| 圈/认知复杂度 | ✅ | ✅ | ✅ | ✅ | ❌ | ✅ 已有 |
| MI / Halstead | ❌ | ✅ | ✅ | ✅ | ❌ | ✅ 已有 |
| Java CK 类度量 | ❌ | ⚠️ | ✅ | ✅ | ❌ | ✅ 已有 |
| **包级耦合 Ca/Ce/I/A/D + SDP** | ❌ | ✅ | ✅ | ⚠️ | ❌ | ✅ **已有**（`arch/pkgmetrics.go`） |
| 架构分层 / 禁止边 / 循环 | ❌ | ✅ | ✅ | ⚠️ | ❌ | ✅ 已有 |
| Type-2 克隆 + diff 闸门 + clone-drift | ❌ | ⚠️ | ✅ | ⚠️ | ❌ | ✅ 已有（**差异化**） |
| 符号**定义**索引 + 近似名 | ❌ | ⚠️ | ⚠️ | ❌ | ✅ | ✅ 已有 |
| **符号引用 / 调用图 / 影响面** | ❌ | ❌ | ⚠️ | ⚠️ | ✅ | ❌ **缺** |
| **回归检测（worsened vs base）** | ❌ | ✅ | ✅ | ✅ | ❌ | ⚠️ **缺**（只有绝对阈值 diff） |
| **仓库重要性地图（图排序 + 预算）** | ❌ | ❌ | ❌ | ❌ | ✅(aider) | ❌ **缺** |
| 社区检测 / 架构漂移 | ❌ | ✅ | ⚠️ | ⚠️ | ❌ | ❌ 缺 |
| **change coupling（历史逻辑耦合）** | ❌ | ⚠️ | ❌ | ✅ | ❌ | ❌ **缺** |
| bus factor / 知识分布 | ❌ | ✅ | ❌ | ✅ | ❌ | ⚠️ 只有 churn×MI 热点 |
| 测试质量 / 隔离 | ❌ | ✅ | ⚠️ | ⚠️ | ❌ | ❌ 缺 |
| 风险聚合评分 | ❌ | ✅ | ⚠️ | ✅ | ⚠️ | ❌ 缺 |
| **agentic gate 预设** | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ **缺** |
| **新增依赖 / 幻觉包风险** | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ **缺** |
| baseline 快照 | ❌ | ✅ | ✅ | ✅ | ❌ | ⚠️ 有趋势库，无 baseline |
| 分布统计（p90/p95）+ top-N | ✅ | ⚠️ | ✅ | ✅ | ❌ | ⚠️ 有热点 top-N，无分布 |
| **解析结果缓存** | ✅ | ⚠️ | ✅ | ✅ | ✅ | ❌ 缺（roadmap Phase C） |
| 多格式报告（HTML/MD/OpenMetrics） | ❌ | ✅ | ✅ | ✅ | ❌ | ⚠️ SARIF/JSON/text |
| MCP server | ❌ | ✅ 9 工具 | ⚠️ | ❌ | ✅ | ✅ 6 工具 |
| **约束包（预算 + 排序）** | ❌ | ❌ | ❌ | ❌ | ⚠️ | ✅ 已有（**差异化**） |
| **委派 bundle + evidence anchor** | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ 已有（**唯一**） |
| 类型/数据流/taint | ❌ | ❌ | ✅ | ❌ | ⚠️ | ❌ 不做（集成） |

---

## 4. 关键判断

### 4.1 市场在替我们验证，但也在替我们收窄窗口

- SonarQube 2026.4 的 "Agentic AI 质量门" 明确写了它的逻辑：**agent 生成的代码"失败方式不同"**——
  新依赖风险、安全缺口、可靠性问题加速引入；因此门要"对安全/可靠性/新依赖更严，对次要维护性更宽"。
  **这正是 metricgate 该给的预设**，只是它做成了服务端，我们该做成 `policy.yaml` 里的一个 preset。
- ast-metrics 的 MCP 宣传语是"给 AI agent 架构感知"——**同一条定位**。它的 `review`（只报 new/worsened）
  已经是我们 diff-only 的上位替代（见 4.3）。

### 4.2 我们的护城河必须收窄到"通道"，不是"指标"

竞品可以一夜之间加指标（SonarQube 就加了架构管理）。**加不了的是**：

- **零 token + 毫秒 + 本地**（SonarQube 是分钟级服务端；CodeQL 要建库）；
- **同一份 policy 同时驱动"生成前约束"和"生成后闸门"**（竞品没有把约束喂给 prompt 的能力）；
- **bundle：把变更集变成可审计的评审任务包，自己不调 LLM**（CodeScene/Greptile 都要上传代码）；
- **evidence anchor：可机器校验的定位证据**（编辑后 hash 变即判定失效）——**调研中没有第二个产品做这个**。

### 4.3 三个"我们确实落后"的点（诚实记录）

1. **引用/影响面**：symbols 只有定义（`Build/Lookup/Similar`），arch 只有**包级** import 图。
   回答不了 agent 最常问的"改这个签名会破坏谁"。CodeGraph / Serena 都做得到。
2. **回归语义**：`FilterSummary` 只按**变更行相交**保留 findings，比较对象是**阈值**而非**基线**。
   一个函数复杂度 8→9（未越阈值）**不会报**；ast-metrics 的 `review` 会。这削弱了"你碰了你就负责"的说服力。
3. **整仓视野**：`context` 是"给定路径的约束包"，缺 aider repomap 式的**重要性排序**——
   agent 拿到约束却不知道该先看哪个文件。

---

## 5. 可吸收能力清单（按优先级）

**判定口径**：V=价值，C=成本（人日），合主线=是否服务三条 LLM 触点。

### P0 — 强化主线，纯 AST 可达，最高杠杆

| # | 能力 | 做什么 | V | C | 合主线 | 参考 |
|---|---|---|---|---|---|---|
| **P0-1** | **符号引用索引 + 影响面** | 从"定义索引"升级为"定义 + 引用"；`metricgate impact <symbol>` 与 MCP `get_impact`：返回定义点、所有引用点、受影响文件、相关测试、被触碰的架构层 | 极高 | 3–5d | 生成后校验 + 委派 | CodeGraph blast radius / Serena find referencing symbols |
| **P0-2** | **回归检测（worsened vs base）** | `check -diff --base` 增加"指标变差"判定：复用 `git.Archive` 取 base 快照，比较同名函数/类的度量与 finding 严重度；变差即报，即使未越阈值 | 高 | 2–3d | 生成后校验 | ast-metrics `review` |
| **P0-3** | **agentic gate 预设** | policy 内置 `preset: agentic`：对 banned-api / 新增依赖 / error 处理更严，对纯风格类度量更宽；`metricgate init --preset agentic` | 高 | 1–2d | 生成前约束 + 闸门 | SonarQube "Sonar way for Agentic AI" |

### P1 — 扩展事实层表达力（仍 AST 可达）

| # | 能力 | 做什么 | V | C | 合主线 | 参考 |
|---|---|---|---|---|---|---|
| **P1-1** | **仓库重要性地图** | `metricgate map`：在 import/符号图上跑 PageRank，预算内输出"最该先读的符号/文件"；并入 context pack 作为 `OrderHeader` 之后的新分区 | 高 | 2–3d | 生成前约束 | aider repomap |
| **P1-2** | **新增依赖风险** | 新 import 不在 allowlist / lockfile / 标准库 → 报 `unknown-dependency`（agent 幻觉包、typosquat） | 高 | 1–2d | 生成后校验 | SonarQube 供应链风险 |
| **P1-3** | **风险聚合评分** | 把多指标 + findings 聚合成 per-file risk score（对齐 hotspots 排序），进 `hotspots` 与 `map` | 中 | 1–2d | 委派 | ast-metrics risk / Greptile confidence |
| **P1-4** | **MCP 补齐** | 暴露 `hotspots` / `trends` / `impact` / `map`，从 6 工具对齐到 ~10 | 中 | 1d | 全部 | ast-metrics 9 工具 |
| **P1-5** | **分布统计 + top-N** | 报告给 p50/p90/p95 + `--top N`（复杂度右偏，均值骗人） | 中 | 0.5–1d | 委派 | cccc |

### P2 — 需 git 历史 / 较重，价值高但成本中

| # | 能力 | 做什么 | V | C | 合主线 | 参考 |
|---|---|---|---|---|---|---|
| **P2-1** | **change coupling** | 两文件历史上总是一起改 → 隐藏逻辑耦合/架构风险（结构图看不见） | 高 | 3–5d | 委派 | CodeScene |
| **P2-2** | **baseline 快照** | 冻结今日违规，`lint` 只对新增失败（与 diff-only 互补：按任意时间点冻结） | 中高 | 2d | 闸门 | ast-metrics baseline |
| **P2-3** | **解析结果缓存** | 复用未变更文件的 parse/metrics/clone；验证链 stat→blob SHA→content hash；**逐字节一致**保证 | 中高 | 2–3d | 全部（性能） | cccc（roadmap Phase C 已列） |
| **P2-4** | **bus factor / 知识分布** | 作者 × 文件历史，关键人风险 | 中 | 2–3d | 委派 | ast-metrics / CodeScene |
| **P2-5** | **社区检测 + 架构漂移** | Louvain 找"自然模块"，对比声明分层 → 漂移 | 中 | 3–5d | 委派 | ast-metrics communities |
| **P2-6** | **gate 遵守率趋势** | 把趋势库升级为"门通过率/风险发布"治理证据 | 中 | 1–2d | 闸门 | SonarQube 遵守率看板 |

### P3 — 明确不做（引用为证）

| 不做 | 原因 |
|---|---|
| 类型/数据流/taint（CodeQL / Semgrep Pro / Joern CPG） | 独立且深厚的研究线；**走集成（Phase B），不重造**（`research-notes.md` §P3-1） |
| 向量检索 / 嵌入（Claude Context / CocoIndex） | 非确定性 + 高 token 成本，与"零 token"定位冲突 |
| LSP 级重命名 / 类型层次 / move / inline（Serena） | LSP 领域；AST 层模拟只会做出低配误报版 |
| LLM 评审编排 / 测试生成 / 序列图（Greptile / CodeRabbit） | 宿主侧职责，metricgate 不调 LLM（`research-notes.md` §P3-2） |
| HTML dashboard / 服务端 / 账号体系 | 与"本地轻量零配置"冲突（可做**静态** HTML 报告，不做服务） |
| 跨仓索引（Sourcegraph SCIP） | 部署重、首次索引分钟级；单仓已覆盖主场景 |

---

## 6. 下一步指导

**结论：先做 P0 三件，它们不是"加指标"，而是把主线从"能查约束"补成"能查影响、能判回归、能给 agent 专用门"。**

### 实施顺序（每步独立提交，以 `go build ./...` + `go test ./internal/<包>/...` + `metricgate check .` 验收）

1. **P0-2 回归检测**（先做，最便宜、直接补 diff-only 的语义漏洞）
   - 复用 `internal/git.Archive` + `internal/store`；比较对象从"阈值"升级为"基线"。
   - 验收：改动使某函数复杂度上升（即使未越阈值）→ 报 `metric-regression`；未改动文件不报；与 `-diff` 集合一致。
2. **P0-1 引用索引 + 影响面**（最高杠杆，主线核心缺口）
   - 扩 `internal/symbols` 为 定义+引用 双索引（Go/Java 的 call/selector 节点）；`impact` 组合 符号引用 + 包依赖图 + 相关测试文件。
   - 验收：`impact` 能列出引用点、受影响文件、跨层影响；对未知符号给 near-miss；MCP `get_impact` 与 CLI 同源。
3. **P0-3 agentic gate 预设**（定位动作，成本最低）
   - `policy.yaml` 加 `preset:`，内置 `agentic`；`init --preset agentic` 生成。
   - 验收：预设可被 `rules explain` 追溯来源；默认 preset 行为与现状逐字节一致。
4. **P1-1 仓库地图 + P1-2 新增依赖风险**（补 context 的整仓视野与幻觉包防线）。
5. **P1-3/4/5**（评分、MCP 补齐、分布统计）——低成本批量收尾。
6. **P2 按数据决策**：先用真实 agent 会话证明"影响面/回归/地图"被调用、有价值，再决定 change coupling / baseline / 缓存。

### 与既有路线的对齐

- **Phase B（ingest 集成）仍按用户指示搁置**，但它与 P0 不冲突：P0 全在 AST 层，零外部依赖。
- **P0-1 完成后，Phase B 的"引用解析"部分（Java 深 receiver 等类型问题）才有对照基线**——先用 AST best-effort，再用 ingest 兜底。
- ~~**A/B 实验的"API 幻觉率"首要指标**，恰好能被 P0-1（引用索引）+ P1-2（新增依赖）直接改善~~
  —— **该建议的前提已作废**：R1 实测 API 幻觉 0/22（编译器免费兜底），
  首要指标已改为**每千 token 的违规避免数**，端点落在编译器抓不到的地方
  （度量阈值 / 架构分层 / 组织约定 / 重造）。P0-1 与 P1-2 仍然值得做，
  但理由是它们服务"生成前 grounding"这条知识论证，**不是**为了改善一个测不出信号的指标。
  见 [`claims.md`](../claims.md) #2 #6 #7、[`evaluation-base-rate.md`](../evaluations/evaluation-base-rate.md) §4.4。

### 风险与禁止事项

- **不为了"对齐竞品"而加指标**：每个新能力必须说清服务哪条 LLM 触点，否则进 P3。
- **引用索引必须带置信度**：AST 层无类型解析，动态调用/反射/接口分发会漏；输出要标注 `ast-only`，不谎报完整调用图。
- **回归检测不得改变既有 gate 语义**：默认关闭或仅 `-diff --base` 时启用，避免历史债务突然全红。
- **preset 不得静默放宽**：`agentic` 预设的每一处偏离默认值都要在 `rules explain` 里可见。

---

## 7. 来源索引

- cccc — <https://github.com/moznion/cccc>（共享 IR、逐字节一致缓存、分布统计、20+ 语言、无 AI/MCP）
- ast-metrics — <https://github.com/ast-metrics/ast-metrics>（7 语言、架构/活动/测试质量/风险、baseline、review、MCP 9 工具、HTML 报告）
- SonarQube Server 2026.4 — <https://www.sonarsource.com/products/sonarqube/whats-new/2026-4/>（Agentic AI 门、agentic-security 规则、架构管理、增量 taint、遵守率看板）
- CodeQL — <https://codeql.github.com/docs/codeql-overview/codeql-changelog/codeql-cli-2.27.1/>（Default suite 498 条安全查询）
- Semgrep Pro Engine — <https://semgrep.dev/products/pro-engine/>（interfile taint、30+ 语言）
- Joern / CPG — <https://docs.joern.io/code-property-graph/>（AST + CFG + PDG）
- CodeScene — <https://codescene.com/product/behavioral-code-analysis>（hotspots、change coupling、knowledge map、bus factor、CodeHealth、ACE）
- Code index MCP 对比 — <https://baeseokjae.github.io/posts/ai-coding-agent-code-index-mcp-comparison-2026/>（Sourcegraph / CodeGraph / Claude Context / CocoIndex）
- Serena — <https://github.com/oraios/serena>（LSP 符号级操作、引用查询、memory、40+ 语言）
- aider repo map — <https://aider.chat/docs/repomap.html>（tree-sitter + 图排序 + token 预算）
- Greptile — <https://www.greptile.com/agent>（codebase graph、agent swarm、confidence score、自定义规则）
- Open Code Review（内部对照）— `docs/open-codereview-reference.md` / `docs/metricgate-open-codereview-optimization.md`

> 注：本文件为**方向校准**，不含对 metricgate 自身效果的承诺；效果以 `docs/ab-experiment.md` 的 A/B 实测为准。
