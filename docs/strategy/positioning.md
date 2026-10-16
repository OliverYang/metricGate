# MetricGate 市场定位与差异化分析

## 🎯 核心结论

**MetricGate 不是传统的 CI 工具，而是"LLM 时代的确定性约束层"**。

差异化不能建立在"没人做"上——v1.0 就是这么写的，而那个论断已经过期。它能建立在**三轴上的结构性事实**：
一次检查的**频率**、**成本**、**确定性**。这三轴是事实陈述，不随竞品的功能表移动。

| 轴 | 我们的位置 | 可反证的口径 |
|---|---|---|
| **频率** | 行级增量；pre-commit / harness hook / MCP / CI 共用同一判定内核 | 一次 `check -diff` 的墙钟时间 |
| **成本** | 纯本地、0 token、无常驻服务、零环境依赖 | 同一次检查的 token 开销与网络依赖 |
| **确定性** | byte-deterministic，finding 可复算 | 5 次运行 JSON 字节相同 |

而真正还空着的一格不是这三轴本身（见下节：2026-10 外部检索显示三轴已被逐条对上），
是**规则的归属**：下节五家的规则集是**产品的**，不是**你的**。把**组织私有约定**（不是通用质量规则）
做成同一套 policy、同一判定内核、多个消费面——这是当前唯一没被逐条占住的定位（claims #5/#12）。

四项能力仍然成立，但只能作为"我们已就位的能力"来讲，不能作为"市场上还没有"来讲：

1. ✅ **LLM 生成代码的实时验证**
2. ✅ **团队非标约束的自动化工具**（← 唯一仍空着的一格）
3. ✅ **增量感知的架构检查**
4. ✅ **0 Token 成本的实时反馈**

---

## 🌐 2026-10 外部检索：同格竞品已经出现（取代"市场空白"论断）

> **检索日期 2026-10-16，证据级别 E4（外部一级来源，逐句取回原文）**。逐句取证与引用链见
> [产品演进方向调研 §4.6](../research/product-evolution-directions-2026-10.md)，本节是它的落地改写。
> v1.0（2024-09-28 冻结）把"没有同格竞品"当卖点，该论断**已被推翻**——同格不是空白，
> 而是已经出现了五个能逐条对上我们卖点的产品。（本节即核心结论里"规则归属"论证的证据来源。）

| 竞品 | 它自己怎么说（原文关键句） | 对上的 metricGate 卖点 | 来源 |
|---|---|---|---|
| **SonarQube Vortex** | "performs Sonar's trusted **deterministic** analysis right inside your agent, **reusing the context from a previous CI analysis** to deliver results in record times" | 确定性 / 进 agent loop / 复用缓存降延迟 | [docs.sonarsource.com](https://docs.sonarsource.com/agent-centric-development-cycle/inside-your-agent-the-agentic-loop/sonar-vortex-analysis) |
| **SonarQube Claude Code 插件**（Anthropic marketplace） | "integrates SonarQube's security and code quality analysis directly into the Claude Code terminal environment for **real-time verification**"；"blocks over 450 secret patterns **before content enters the LLM context**" | 分发面（harness 集成）/ 上下文前置过滤 | [sonarsource.com](https://www.sonarsource.com/blog/now-available-sonarqube-plugin-for-claude-code/) |
| **Verificate Gate** | "**17 deterministic reality gates** ... The gates hold **veto power the model can't override**"；自述目标含 mock/placeholder、hallucinated dependency、bypass attempts | 确定性 / 闸门不可被模型绕开 | [verificate.ai](https://verificate.ai/gate/) |
| **Vivari Guard** | "weighing **blast radius, transitive impact, and behavioral hotspots** among its signals, **with no LLM in the call**. Runs standalone, **local-first**" | claims #9 二阶后果 / 本地 / 0 token | [vivari.ai](https://vivari.ai/guard/) |
| **Trustabl Agent Analyzer** | "deterministic, **SDK-aware** static analysis to the agent layer"；"runs entirely on your machine: **no cloud scanner, no account, no code upload, no LLM**" | 本地 / 确定性 / SARIF 2.1.0 输出 | [trustabl.ai](https://www.trustabl.ai/products/) / [github.com/trustabl/agent-reliability-analyzer](https://github.com/trustabl/agent-reliability-analyzer)（原 `trustabl/trustabl` 已 301 改名，2026-10-16 复核） |

### 由此改写出来的三条打法

1. **不与 Sonar 打"分析深度"**。Vortex 明确复用 CI 缓存来压低延迟——说明"确定性分析 + 低延迟"这一格
   **已经有人占了**。metricGate 在这一格只剩"规则是你的私有约定"（claims #5/#12）与"零基础设施"两个差异。
2. **不与 Verificate 打"闸门权威"**。"17 个 gate 有 veto power" 与我们的 gate 定位同格，
   但它的输入是 **AI 的输出**、不是**仓库的代码**——我们报的是可复算的仓库事实。
3. **不与 Vivari / Trustabl 打"本地 + 0 token"**。两者都自述 local-first / no LLM；
   这一格的胜负由"多个消费面是否共享同一套 policy 与判定内核"决定，不由"是否本地"决定。

> **取证边界**：同轮检索还出现过 GateTest（CI quality gate，宣称在 19 个固定仓库上每夜实测精确率并公开漏报）
> 与 Kete/taniwha.ai（把规则抽成可查询事实、每次 tool call 前做 pre-flight gate）两家，
> 但本文**未逐条取回一级来源**，按 E4 规则不引用其细节、不列入上表。它们是下一轮检索的第一批目标。

---

## 📊 竞品分类与对比

### 第一类：传统代码质量门禁工具

| 工具 | SonarQube | CodeClimate |
|-----|-----------|------------|
| **定位** | 企业级平台 | SaaS 平台 |
| **优势** | 功能全面、可视化强 | 易用、配置简单 |
| **劣势** | 服务端部署、分钟级反馈、Token 高 | SaaS、黑盒、Token 高 |
| **能否替代 MetricGate** | ❌ 无架构约束、无增量 | ❌ 无架构约束、无增量 |

### 第二类：Linter 与静态检查工具

| 工具 | golangci-lint | ESLint | PMD/SpotBugs |
|-----|---------------|--------|--------------|
| **定位** | Go linter 聚合器 | JS linter | Java 静态分析 |
| **优势** | 速度快、配置简单 | 插件丰富 | 类型系统支持 |
| **劣势** | 全量扫描、无架构 | 仅 JS、无架构 | 仅 Java、无架构 |
| **能否替代 MetricGate** | ❌ 无架构约束、无增量 | ❌ 无架构约束、无增量 | ❌ 仅 Java、无增量 |

### 第三类：架构约束检查工具

| 工具 | ArchUnit | ADE |
|-----|----------|-----|
| **定位** | Java 架构测试库 | 架构决策 DSL |
| **优势** | 深度集成、类型系统 | DSL 定义规则 |
| **劣势** | **仅 Java、需编译、无法检查未完成代码** | 小众、无 CI 集成 |
| **能否替代 MetricGate** | ⚠️ 部分能力重叠，但局限明显 | ❌ 功能不完整 |

### 第四类：Policy as Code 工具

| 工具 | Policy Bot | Kodus | Semgrep |
|-----|-----------|-------|---------|
| **定位** | PR 审批策略 | AI 政策审查 | 安全规则管理 |
| **优势** | GitHub 深度集成 | AI 辅助、易写 | 企业级管理 |
| **劣势** | **非代码质量检查** | 云端 AI、Token 高 | 仅安全规则 |
| **能否替代 MetricGate** | ❌ 不检查代码内容 | ❌ 无架构约束 | ❌ 无业务语义 |

### 第五类：AI 代码审查工具

| 工具 | Open Code Review | Conveyor |
|-----|-----------------|----------|
| **定位** | AI 代码审查 | AI 项目编排 |
| **优势** | 多种语言支持 | 自然语言转任务 |
| **劣势** | 黑盒、Token 高、无确定性 | 项目生成工具、无 CI |
| **能否替代 MetricGate** | ❌ 黑盒、成本高 | ❌ 非代码审查 |

---

## 🔍 关键发现

### 现有工具的共性缺陷

> ⚠️ 下表是 v1.0 的判断，其中 **LLM 友好 / 实时性 / 本地化 / 成本** 四行已被
> ［2026-10 外部检索］一节的五家竞品自述直接竞争
> （Vortex 自述的 "record times"、Vivari 与 Trustabl 的 local-first / no LLM）。
> 仍然成立的是 **架构约束（语言无关分层）**、**业务语义（团队约束转化）**与**自进化**三行。

| 维度 | 现有工具现状 | MetricGate 解决方案 |
|-----|-------------|-------------------|
| **架构约束** | ❌ 仅有 ArchUnit(Java) | ✅ 语言无关的架构分层 |
| **增量检查** | ❌ 全量扫描 | ✅ 行级增量检查 |
| **LLM 友好** | ❌ 未优化 | ✅ 0 token、毫秒级 |
| **实时性** | ⚠️ 分钟级 | ✅ 毫秒级 |
| **本地化** | ⚠️ 多为 SaaS | ✅ 纯本地 |
| **业务语义** | ❌ 无 | ✅ 团队约束转化 |
| **自进化** | ❌ 静态规则 | ✅ 反馈驱动优化 |
| **成本** | ⚠️ Token 高 | ✅ 0 token |

### 能力缺口盘点（注意：缺口 ≠ 无人竞争）

> 下面四格是 v1.0 对"哪些能力没人做好"的判断。2026-10 检索后，**第 1 格与第 4 格已被
> ［2026-10 外部检索］一节的五家逐条对上**，
> 第 3 格被 Vivari 的"blast radius, transitive impact" 部分竞争，
> 对外只能作为"我们已就位的能力"讲，不能作为"市场上还没有"讲。**第 2 格（团队私有约束）仍空着。**

#### 1. LLM 时代的代码约束层
- **痛点**: 现有工具都是为"人类开发者"设计
- **机会**: 为 AI 生成的代码提供确定性验证
- **价值**: 防止 AI 幻觉、保证代码符合规范

#### 2. 团队非标约束的自动化工具
- **痛点**: 现有工具只有通用规则
- **机会**: 将团队的独特约束转化为可执行规则
- **价值**: 降低门槛、持续进化

#### 3. 增量感知的架构检查
- **痛点**: ArchUnit 等需要完整编译
- **机会**: AST 级别的增量架构检查
- **价值**: 实时反馈、支持未完成代码

#### 4. 0 Token 成本的实时反馈
- **痛点**: 所有 AI 工具都需要 Token
- **机会**: 纯本地、零成本、毫秒级
- **价值**: 高频次使用、低延迟体验

---

## 💡 MetricGate 的定位

### 不是替代，而是补充

```
┌─────────────────────────────────────────────────────────┐
│              Development & CI Flow                        │
└─────────────────────────────────────────────────────────┘
                          ↓
        ┌──────────────────────────────────────┐
        │  Pre-commit Hook                      │
        │  • metricGate check -diff            │ ← 快速、增量、本地
        │  • 只检查新增行                        │
        │  • 适合 LLM 生成场景                    │
        └──────────────────────────────────────┘
                          ↓
        ┌──────────────────────────────────────┐
        │  CI Pipeline                           │
        │  • metricGate check (full)           │ ← 完整检查
        │  • golangci-lint run                 │ ← 深度静态分析
        │  • SonarQube analysis                │ ← 长期趋势
        │  • ArchUnit tests                    │ ← 架构验证
        └──────────────────────────────────────┘
                          ↓
        ┌──────────────────────────────────────┐
        │  Nightly/Batch                        │
        │  • SonarQube deep scan               │ ← 深度扫描
        │  • Security scanning                 │ ← 安全审计
        │  • Metrics aggregation               │ ← 趋势分析
        └──────────────────────────────────────┘
```

**各司其职**（v1.0 写的"互不重叠"已被 2026-10 检索否证——重叠的是确定性/低延迟/本地三轴，不重叠的是规则归属）:
- **metricGate**: 实时、增量、架构、LLM 友好
- **golangci-lint**: 深度静态分析、类型检查
- **SonarQube**: 长期趋势、可视化、报告
- **ArchUnit**: 深度架构验证 (Java)

---

## 📊 详细对比矩阵

> ⚠️ **本表是 v1.0（2024-09-28）的内部功能判断，未经 2026-10 外部检索逐格复核。** 其中"实时反馈""LLM 友好"
> 两格已被 SonarQube Vortex 与 Claude Code 插件的自述直接竞争（见［2026-10 外部检索］一级来源），
> **不得作为"无人能做"的证据引用**；对外表述只讲三轴事实与规则归属。逐格复核列为后续项。

### 功能覆盖度

| 功能 | SonarQube | golangci-lint | ArchUnit | Policy Bot | MetricGate |
|-----|-----------|---------------|----------|-----------|------------|
| **静态分析** | ✅ | ✅ | ❌ | ❌ | ✅ |
| **架构约束** | ❌ | ❌ | ✅(Java) | ❌ | ✅ |
| **增量检查** | ❌ | ❌ | ❌ | ❌ | ✅ |
| **行级检查** | ❌ | ❌ | ❌ | ❌ | ✅ |
| **多语言** | ✅ | ❌ | ❌ | ✅ | ✅ |
| **实时反馈** | ❌ | ⚠️ | ❌ | ⚠️ | ✅ |
| **0 Token** | ❌ | ✅ | ✅ | ❌ | ✅ |
| **本地化** | ⚠️ | ✅ | ✅ | ✅ | ✅ |
| **LLM 友好** | ❌ | ❌ | ❌ | ❌ | ✅ |
| **团队约束** | ❌ | ❌ | ❌ | ❌ | ✅ |
| **自进化** | ❌ | ❌ | ❌ | ❌ | ✅ |
| **服务端** | ✅ | ❌ | ❌ | ✅ | ❌ |

### 性能指标

| 指标 | SonarQube | golangci-lint | ArchUnit | MetricGate |
|-----|-----------|---------------|----------|------------|
| **响应时间** | ~30s | ~5s | ~10s | ~0.3s |
| **增量速度** | N/A | ~1s | N/A | ~0.1s |
| **内存占用** | ~500MB | ~100MB | ~200MB | ~50MB |
| **Token 成本** | $ | $ | ¥ | ¥ |

---

## 🎯 市场机会评估

### 短期机会 (6-12 个月)

1. **LLM 代码审查市场**
   - 规模：快速增长
   - 竞争：**已有同格**（Sonar Vortex / Claude Code 插件、Verificate、Vivari、Trustabl，见［2026-10 外部检索］E4 对照表）
   - MetricGate 优势：确定性、0 Token、实时

2. **架构治理市场**
   - 规模：中等
   - 竞争：ArchUnit 主导但局限明显
   - MetricGate 优势：语言无关、增量检查

3. **团队定制规则市场**
   - 规模：新兴
   - 竞争：**仍空着**——［2026-10 外部检索］一节五家的规则集都是**产品的**，不是**团队的**
   - MetricGate 优势：约束转化机制

### 长期机会 (1-3 年)

1. **成为 LLM 时代的基础设施**
   - 愿景：所有 AI 生成代码的必经之路
   - 路径：建立标准、构建生态

2. **团队约束管理平台**
   - 愿景：让每个团队都能定义自己的约束
   - 路径：简化流程、共享模板

3. **企业级代码治理平台**
   - 愿景：整合现有工具，提供统一视图
   - 路径：开放 API、协同工作

---

## 📈 建议

### 产品定位

1. **明确差异化**: 不比"功能有无"，比**频率 × 成本 × 确定性**三轴上的结构性事实；定位收敛到"规则归属"这一格
2. **强调互补性**: 明确与现有工具的协同关系（但不再声称"互不重叠"）
3. **从小处切入**: 先解决"架构约束 + 增量检查"这个痛点

### 市场推广

1. **目标用户**: 
   - 使用 LLM 生成代码的团队
   - 有复杂架构约束的企业
   - 追求高效开发流程的组织

2. **核心价值主张**:
   - "为 AI 生成的代码提供确定性验证"
   - "将团队约束转化为可执行规则"
   - "0 Token、毫秒级的实时反馈"

3. **进入策略**:
   - 作为 pre-commit hook 引入
   - 与现有 CI 工具共存
   - 逐步扩展功能边界

### 技术路线

1. **保持轻量**: 坚持本地化、0 Token、毫秒级
2. **开放生态**: 提供 API、支持自定义扩展
3. **持续创新**: 在架构约束、增量检查、自进化方面领先

---

## 🚫 明确不做（取舍与反证）

> 本节引入自 [产品演进方向调研 §5](../research/product-evolution-directions-2026-10.md)。
> 定位文档只写"我们做什么"是不够的：竞争叙事已经变成"我们**不**做什么、以及什么条件下重开"，
> 否则每一格新能力都会被外部对照表里的一句竞品自述抵消掉。每一行都带**可反证的重开条件**。

| 不做 | 理由（可反证） | 反证条件（什么情况下重开） |
|---|---|---|
| **autofix / codemod** | roadmap.md:183 明确不做；且会把"判定"与"执行"耦合，破坏 claims #10 的字节确定性叙事 | "修复建议采纳率"被度量出来且 > 50%（five-properties 里唯一未闭环的指标） |
| **自建类型系统 / 数据流分析** | capabilities.md §11 既定边界；claim #2 已被证伪（编译器是免费确定性检查器，agent 会自修） | 出现轻量增量式方案（不是重造 javac/gopls） |
| **远程服务化（真 v2）** | 会破坏"本地、0 token、零环境依赖"的卖点（claims #11）；cockpit 已有 loopback 服务，市场叙事要重新划界 | 出现明确的团队级多仓需求（≥ 3 个外部仓库要求同仓治理） |
| **语义级 AI code review** | pilot 13 条 ❌ 语义条款明说不可自动化；外部 head-to-head（gitee-scan-2.0-portal，5 commits）显示 LLM 抓语义正确性、metricGate 抓复杂度/耦合欠账——**互补** | 只在想做"复杂度守卫"单一格、去抢 LLM 的格子时重开 |
| **调阈值以降低噪声** | claims #7 的实验结论是"需要数值棘轮"而非"放松标准"；放松标准会让闸门失去红线身份 | 无（iter-10 已证明 31 error → 16 的棘轮路径可行，不必放松标准） |
| **接入 hook 时改 hook 语义/权限模型** | 每个 harness 的 hook 权限模型不同（已知：Claude Code 的 `PreToolUse permissionDecision:"deny"` 对 MCP tool 不生效，issue #33106），去适配会陷进别人的 bug 周期 | 不使用 deny 语义：只用 `PostToolUse` + 自家 `check`，因此不随该 bug 周期重开 |

---

## 🏆 总结

### MetricGate 的独特价值

| 维度 | 现有工具 | MetricGate | 价值 |
|-----|---------|-----------|------|
| **架构约束** | ⚠️ 有限 | ✅ 完整 | 保证架构一致性 |
| **增量检查** | ❌ 无 | ✅ 行级 | 实时反馈、提升效率 |
| **LLM 场景** | ❌ 未优化 | ✅ 原生支持 | 防止 AI 幻觉 |
| **实时反馈** | ⚠️ 慢 | ✅ 毫秒级 | 无缝开发体验 |
| **团队定制** | ❌ 通用规则 | ✅ 约束转化 | 降低门槛 |
| **成本** | ⚠️ Token 高 | ✅ 0 token | 可持续使用 |

### 市场定位

**MetricGate 不靠"市场上还没人做"立足（那个论断已在 2026-10 被推翻），而是靠三轴上的结构性事实与规则归属：**
本地、0 token、毫秒级、byte-deterministic，把**组织私有约定**做成一套 policy、一个判定内核、多个消费面。

它与 SonarQube/golangci-lint/ArchUnit 等工具**在确定性 / 低延迟 / 本地三轴上有重叠**
（见［2026-10 外部检索］一节五家对照表），
在"规则属于谁"这一格上不重叠；协同关系仍成立，但不再描述为"互不重叠"。

---

*版本：v2.0*  
*最后更新：2026-10-16*  
*v2.0 变更*：按 [产品演进方向调研](../research/product-evolution-directions-2026-10.md) §4.6 / §5 改写差异化叙事——
把 v1.0（2024-09-28 冻结）的"填补市场空白"论断替换为"三轴结构性事实 + 规则归属"；新增五家同格竞品对照表
（带一级来源、检索日期与取证边界）和六项不做；并对 v1.0 的功能矩阵与"互不重叠"表述标注未经复核的边界。
