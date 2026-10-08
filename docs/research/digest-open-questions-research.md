# digest 待解问题定向文献调研

- 日期：2026-10-08
- 状态：调研完成，结论已吸收进优先级判断
- 背景：digest（阶段 2 规则→提示词注入）待解问题分两类——验证类（B 类惯用法防不住、1.74× 生成成本膨胀未归因、新阈值净效应待跑批）与扩展类（任务条件化注入、规则类检查进提示词）。本文回答三个定向调研问题，为 P2「任务条件化」和 P0「M2-B/A 臂重跑」提供文献地基。
- 方法：三轮 web 检索 + 原文抓取（Exa/OpenAI/Brave 渠道部分 429/无 key，AutoGuide 经 arxiv 定位）。诚实边界：全部为摘要级精读 + 一篇实践文，未逐篇复现实验；ACM 两个 DOI 因 403 仅识别标题。

## TL;DR

1. **任务条件化注入有成熟学术先例**（AutoGuide, NeurIPS 2024）：条件化 guideline + 按当前状态选择注入，已在 web navigation 验证显著优于全量基线。机制与本项目「按任务选规则」同构——差异只在匹配器：AutoGuide 用 LLM 判断适用性，我们应保持确定性匹配。这把「任务条件化」从自创功能变成了有先例支撑的工程决策。
2. **1.74× 成本归因的关键文献警告：同任务 token 波动可达 30×**（arxiv 2604.22750）。单次跑批的 token 对比不可靠，A 臂重跑必须多 run 取中位数——这直接改写 M2 附录的实验设计，是本次调研最有操作价值的发现。
3. **B 类惯用法提示的文献支持分两层**：学术层（Modular Imperative, LMPL'25）支持"内置具体指标 + 实时反馈"有效，但尚无"一次性生成时结构提示能否防住 B 类违规"的直接测量——这正是 M2 主协议要填的空。
4. Hexagonal 架构实践文（notes.muthu.co）给出 B 类惯用法提示的措辞方向：不是解释规则，而是给出**可检查的最小合规路径**（定义 port → 外层实现 → 组装根注入），与 WP1 已落地的惯用法节一致，可互引佐证。

---

## 问题一：任务→相关规则的检索怎么做（避免 LLM 挑规则破坏确定性）

### 核心先例：AutoGuide (NeurIPS 2024, arxiv 2403.08978)

「AutoGuide: Automated Generation and Selection of Context-Aware Guidelines for Large Language Model Agents」

- **机制**：从离线 agent 轨迹自动生成 context-aware guidelines；每条 guideline 用简洁自然语言表达且遵循**条件化结构**——明确描述其适用的上下文（"当处于状态 X 时，做 Y"）。
- **选择**：注入时按 agent 当前决策状态检索**相关**的 guidelines，而非全量注入；论文把"相关知识的提供"作为对 demonstration-based ICL 局限的克服。
- **效果**：在复杂基准（含真实世界 web navigation）上显著优于竞争基线。

### 与 metricGate 的同构与差异

| | AutoGuide | metricGate 应取的形态 |
|---|---|---|
| 规则来源 | 离线 agent 轨迹 → LLM 生成 guidelines | policy.yaml 编译产物（已存在） |
| 条件化 | 每条带自然语言适用条件 | 每条已有**结构化**适用条件：规则 × 包/层 + 阈值 × 包 |
| 匹配器 | LLM 判断 guideline 是否适用当前状态 | **确定性匹配：任务文本关键词 × 规则的 match 范围 × 目标包** |
| 注入 | 相关 guidelines 注入 prompt | 相关规则行注入 digest 对应节 |

**对项目的直接指导**：
1. AutoGuide 证明「条件化 + 选择性注入」优于全量注入——这是任务条件化的直接学术先例，P2 从"自创想法"升级为"有先例的工程决策"。
2. **匹配器保持确定性**是我们与 AutoGuide 的本质差异，也是必须守住的红线：digest 的零 LLM 立场（extract/digest 全链路无 LLM）不能因为任务条件化被破坏。AutoGuide 用 LLM 做适用性判断，代价是选择过程不确定、不可复现——违反 claims #10（byte-identical 可复现）。我们的匹配器应是：任务文本 → 确定性关键词映射 → 规则/包子集。
3. 现成数据源已存在：非标提取（internal/extract）时每条规则已带**编号+原文条款**，任务条件化所需"规则的条件结构"在转换管线里天然具备。

---

## 问题二：1.74× 生成成本膨胀的归因实验设计

### 核心先例：arxiv 2604.22750（agent token 消耗首个系统研究）

SWE-bench Verified，8 个前沿模型的轨迹分析。与 A 臂重跑设计直接相关的发现：

- **同任务 token 高度随机：同一任务的多次 run 总 token 可差 30×**——单次 run 的 token 对比在统计上无意义，森林（30× 方差）会淹没树木（1.74× 信号）。
- **更高 token ≠ 更高准确率**：准确率常在中等成本处达峰，高成本段饱和。
- agentic 任务 token 消耗是 code chat 的 1000×，**input token（而非 output）主导总成本**——修正认知：digest 均价 441 tok 是 input 侧小头，膨胀更可能来自 in-loop 探索（U09 30 次 API 调用）而非 digest 本身。
- 模型间 token 效率差异巨大（同任务 Kimi-K2/Claude-Sonnet-4.5 比 GPT-5 平均多耗 150 万+）；模型无法预测自身 token 消耗（相关性最高 0.39）。

### 对 A 臂重跑设计的直接改写（写进 M2 附录）

1. **对比单位改为 per-task 多 run 中位数**：k≥5 run/任务，报告中位数 + IQR；重尾分布下中位数比均值稳。原 1.74×（gen 4.38M vs 2.38M）是单次测量，在 30× 方差面前不能区分"digest 导致的额外探索"与"模型固有随机性"——重跑后应以中位数结论替代或标注置信。
2. **三桶归因模型**：把"1.74×"拆成三个机理不同的桶——
   - **input 常数项**：digest 注入文本本身（均价 441 tok，可精确计算）；
   - **in-loop 行为**：探索轮数、API 调用数、工具调用分布（calllog 已有阻截率/相位分布数据可对照）；
   - **output 重试**：early-death 重试与超时损耗（R3 已单列 251K）。
   三桶对应不同机理与不同对策：常数项是已知代价，行为桶才是真正要归因的对象。
3. 检验方式：对比两臂的 in-loop 行为数据（轮数/调用数）而非只看 token 总量——若行为分布无差异而 token 差异仍在，则差异主要来自随机性而非 digest 诱导。

---

## 问题三：B 类惯用法提示（结构修复指令）的有效性

### 学术层：Modular Imperative（LMPL'25）已覆盖的方向支持

此前 2026-10-07 精读过的 Modular Imperative (LMPL'25, DOI 10.1145/3759425.3763392) 已建立方向支持：LLM 需要"内置具体指标 + 运行时反馈"才能维持模块化（Rubric DSL 实验组 vs 无指导对照组），且指出抽象指令（"避免紧耦合"）无效而具体规则（"禁止访问 _ 前缀成员"）有效。

### 实践层：The Architecture is the Prompt（notes.muthu.co，2025-11）

- 核心论点：**"架构即提示词"**——与其用更长的 prompt 解释规则，不如让架构本身成为约束：边界清晰的结构让 AI"物理上"无法违反（缺依赖就编译不过），prompt 只需要小而准。
- 对 AI 协作的收益：结构强制 > prompt 解释；小而准的 prompt 优于长 prompt；port/adapter 明确接口降低幻觉。
- **与 WP1 惯用法节的一致性**：我们的惯用法节措辞（定义 port → 外层实现 → 组装根注入，禁止复制绕行）正是"给出可检查的最小合规路径"这一思路的落地——有检查机制背书的结构提示，比无检查背书的纯 prompt 主张更有力。
- 佐证 U04 机制不对称分析：B 类的解法知识（port-adapter）不在约束文本里时模型防不住；给了它解法知识的形状后，模型的实现路径被改写（R2 U04 两次自发明依赖倒置）。补充证据链：U04 修复过程（R2 两次自发明）+ WP1 惯用法前摄注入 + Hexagonal 实践文 = "给解法知识"方向的三点支撑。

### 空白点：直接测量仍缺位

- 现有文献支持"结构提示 + 检查背书"的方向，但**"一次性生成时结构提示能否把 B 类违规从防不住变成防得住"没有直接测量**——Modular Imperative 是编辑场景，2604.22750 是成本分析，AutoGuide 是导航决策质量。
- 这正是 M2 主协议预注册线（惯用法节 ≥3/5）要填的空：M2 是该空白的首个直接测量（或极少数之一）——写进 claims #18 的定位话术。

---

## 弱相关参考（识别但未深读）

- TOSEM 10.1145/3715908「Large Language Model-Aware In-Context Learning for Code Generation」（TOSEM 34(7) 2025）
- TOSEM 10.1145/3849809「LLM See, LLM Do: Enhancing Repository-Aware Code Generation in LLMs Through Logical Context and Usage Knowledge」
- arxiv 2512.24933「ADOPT: 多步 LLM 管线 prompt 联合优化」（textual gradient + Shapley 资源分配）——属"管线 prompt 优化"非"规则选择"
- 注：arxiv 2505.17453 为检索误抓（核物理论文），与 AutoGuide 无关，AutoGuide 正确 id 为 2403.08978。

## 对优先级判断的更新

1. **P0（M2-B 跑批 + A 臂重跑）价值更高了**：30× 方差的发现意味着原设计"单次对比"不足以支撑成本结论——k≥5 run 中位数设计让 P0 的证据强度上一档，是本次调研最直接的操作性收获。
2. **P2（任务条件化）从"自创功能"变成"有先例的工程决策"**：AutoGuide 同构 + 确定性匹配器红线。
3. **P3（规则类进红线节）设计确认**：等 M2-B 数据决定是否值得。
4. **claims #18 定位话术升级**：M2 主协议是"结构提示能否让一次性生成防住 B 类"的首个直接测量。
5. **三桶归因模型**（input 常数 / in-loop 行为 / output 重试）作为 1.74× 的归因框架写进 M2 附录。
