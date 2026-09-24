# 研究与工程实践参考（用于指导 metricgate 迭代）

本文件把**外部一手来源**（高引论文 + 活跃开源项目）映射到 metricgate 的具体迭代/优化动作。
检索时间 2026-09，来源：OpenAlex（politepool，含被引数）、arXiv API、GitHub Search API。
被引数为 OpenAlex 计数，**不代表质量**，只用于排序“哪些工作在领域里被反复引用”。

> 使用原则：**用研究校准方向，不用研究替代实测**。凡涉及“LLM 生成质量/成本”的结论，
> 仍以 `docs/ab-experiment.md` 的真实 A/B 为准；本文件只给“值得做/不值得做”的判据。

## 0. 一句话结论

metricgate 的三大赌注——(1) **事实/约束的 grounding 能减少幻觉**、(2) **确定性检查要“可操作、低噪”**、
(3) **上下文要按重要度排序并控制在预算内**——都能在近年文献里找到直接支撑；
同时文献也明确指出**类型/数据流分析不应自研**（印证既有的 `ingest` 决策）。

## 1. 已落地能力 ↔ 学术根基（用来“锁死”设计不漂移）

| 能力 | 关键文献（年 / 被引） | 说明 |
|---|---|---|
| 圈复杂度 | McCabe, *A Complexity Measure*, IEEE TSE **1976** / **5923**（doi 10.1109/tse.1976.233837） | 阈值度量的经典定义 |
| Halstead / MI | Halstead, *Elements of Software Science* **1977** / **2344**；*Automated Source Code Maintainability Index*, J. Softw. Maint. **1997** / 74（doi 10.1002/(sici)1096-908x(199705)9:3<127） | 我们的 MI 是**函数级 scale-restored 变体**（已在 evaluation-phase-b 诚实标注）；文献原版是文件级 |
| CK 类度量（WMC/NOM/LCOM/CBO/DIT） | Chidamber & Kemerer, *A metrics suite for object oriented design*, IEEE TSE **1994** / **5691**（doi 10.1109/32.295895） | LCOM 默认 advisory 的做法与“LCOM 作为 smell 预测力弱”的后续讨论一致 |
| 分层/模块化（architecture policy） | Parnas, *On the criteria to be used in decomposing systems into modules*, CACM **1972** / **4715**（doi 10.1145/361598.361623） | 信息隐藏 = 我们 `forbid`/`layers` 的理论基础 |
| 克隆检测（Type-2 指纹） | *Comparison and evaluation of code clone detection techniques and tools*, Sci. Comput. Program. **2009** / **1008**（doi 10.1016/j.scico.2009.02.007）；CCFinder **2002** / **1737** | token/归一化指纹属主流路线；clones 分 Type-1..4，我们做 Type-2 |
| 架构侵蚀 | *Controlling software architecture erosion: A survey*, JSS **2011** / **181**（doi 10.1016/j.jss.2011.07.036） | conformance + drift 长期化 → 对应“趋势库”尚未启用 |
| Churn×MI 热点 | Nagappan & Ball, *Use of relative code churn measures to predict system defect density*, ICSE **2005** / **763**（doi 10.1145/1062455.1062514） | 直接支撑 `hotspots`(churn × debt) 的构造 |

## 2. 近年 LLM×Code 文献 → 对 metricgate 方向的直接验证（最重要）

| 主题 | 文献（年 / 被引） | 对 metricgate 的意义 |
|---|---|---|
| **代码幻觉** | *CodeHalu: Investigating Code Hallucinations in LLMs via Execution-based Verification*（AAAI **2025**，被引 17；arXiv 2405.00253）；*CodeMirage* **2024**；*CoHalLo* **2025**；*Refusing the Impossible: A Taxonomy and Benchmark for Code Hallucination* **2026** | **最强外部验证**：幻觉是公认问题且需**执行/事实级校验**。metricgate 的 facts/symbols/banned-api 正是“事实层”，与执行验证互补（我们做静态确定性、零 token） |
| **API 误用** | *Reassessing Java Security API Misuse in Current LLMs* **2026**；*Database-Augmented RAG for Automated Repair of REST API Misuses* **2026** | 支撑 `banned-api` 数据化与“符号索引=防误用”；提示可扩展 **required-api**（必须用某 API，如 logger） |
| **生成代码安全** | *Asleep at the Keyboard? Assessing the Security of GitHub Copilot's Code Contributions* **2021** | 约 40% 生成片段含漏洞 → 确定性安全约束（禁用 API / secrets）价值明确 |
| **仓库级生成** | RepoCoder（EMNLP **2023** / 151）；*On the Impacts of Contexts on Repository-Level Code Generation* **2024** | 支撑 facts/symbol 索引作为**grounded context**；上下文质量 > 长度 |
| **长上下文** | *Lost in the Middle: How Language Models Use Long Contexts*, TACL **2024** / **1312**（doi 10.1162/tacl_a_00638） | **关键**：模型对“中间”内容利用差 → 我们 pack 的 **required-first + priority 排序** 方向正确；应保证**硬约束与当前 diff 靠近开头** |
| **LLM 代码评审** | *Evaluating Large Language Models for Code Review* **2025**；*SGCR: A Specification-Grounded Framework for Trustworthy LLM Code Review* **2025** | 支撑 `bundle` 的“**spec-grounded** 委托”定位：把 policy 约束交给宿主 agent，而不是让 LLM 凭感觉评 |
| **静态分析价值/噪声** | *On the value of static analysis for fault detection*, IEEE TSE **2006** / **286**（doi 10.1109/tse.2006.38） | 支撑“低噪、可操作”纪律：`metricgate: ignore` + diff-only 降低噪声 |
| **类型推断** | *Deep learning type inference*, FSE **2018** / **189** | 类型/数据流是独立研究线 → 印证“**不自研类型分析、走 ingest 集成**”的决策 |

## 3. 活跃开源项目（可借鉴/可对标；星数为 2026-09 GitHub 实测）

| 项目 | 星 | 语言 | 可借鉴点 / 差异 |
|---|---:|---|---|
| `alibaba/open-code-review` | 40267 | Go | 业界 OCR 参考：**委托模式**（确定性脚手架 + 宿主推理）——与 `bundle` 同向 |
| `modelcontextprotocol/servers` / `registry` | 90569 / 7282 | TS/Go | MCP 生态；我们已是 MCP server，可考虑上 registry 曝光 |
| `fe3dback/go-arch-lint` | 575 | Go | Go 架构 import 规则；我们对标其“分层/禁止边”，但更语言无关 |
| `uudashr/gocognit` | 493 | Go | 认知复杂度实现可对照我们的算法 |
| `mibk/dupl` | 370 | Go | token 级克隆检测；我们额外做 cross-language + diff-aware 闸门 |
| `microsoft/sarif-sdk` / `owenrumney/go-sarif` | 226 / 84 | C#/Go | SARIF 库；我们手写，仅在 schema 变动痛点出现时再考虑引入 |
| `Feysh-Group/corax-community` | 256 | Kotlin | Java 深度分析框架——**集成候选**（走 ingest，不进程内依赖） |
| `PhpCodeArcheology/PhpCodeArcheology` | 91 | PHP | “架构 + 可维护性，60+ 指标”的同类；提示我们指标面可继续扩 |
| 参考（路线图已列）：`golangci-lint` ★19.4k / `staticcheck` ★6.9k / `PMD` ★5.5k / `SpotBugs` ★3.9k / `gitleaks` ★29.4k / `semgrep` ★16.7k | — | 多语言 | 深度缺陷/安全 → **ingest 适配**，不重造 |

## 4. 由研究得出的**具体迭代建议**（按杠杆/成本排序）

**P1（高杠杆、低成本，建议紧接着做）**
1. **上下文排序显式化**（依据：Lost-in-the-Middle 2024）：在 `facts.PackItemsBudgeted` 之外，
   增加一条**契约级不变量**——硬约束与当前 diff 必须出现在 pack 前部；补测试断言首块是
   `constraints`+`diff:*`。当前实现已 required-first，但缺“显式保证 + 文档”。
2. **克隆“不一致修改”检测**（依据：*Consistent/Inconsistent Changes to Code Clones* 2007 / 217）：
   当用户改了克隆的一侧、另一侧未同步 → 报 `clone-drift`。这是**研究支撑的差异化能力**，
   且天然适配 diff-only 闸门。
3. **grounding 效果的微型实验**（依据：CodeHalu/CodeMirage）：在 `ab-experiment.md` 里把
   “API 幻觉率”列为**首要指标**，A/B 就是“有/无 facts+symbols”。这把未验证命题变成可测。

**P2（中等成本）**
4. **`required-api`（禁用 API 的对偶）**（依据：API-misuse 2026）：policy 增加“必须经过 logger /
   必须处理 error”的正向约束，补齐 A 约束族的表达力。
5. **架构侵蚀趋势**（依据：erosion survey 2011）：启用 store，记录 layering/forbidden 违规随时间的
   变化，输出“erosion trend”；这是把一次性闸门升级为“长期健康”的关键。
6. **可操作性与误报率实测**（依据：static analysis value 2006）：在评估里加入 FP/信噪比量化，
   为 severity/阈值调优提供数据（而非拍脑袋）。

**P2 中显式不做**

4'. `required-api`（must-call / must-handle-error）— 显式不做
    - 依据：与 `banned-api` 对偶的"必须经过 logger / 必须处理 error"等价于
      "代码覆盖式的强制日志/异常处理"静态分析（"log every error path"、
      "every service method must call metrics.Inc" 之类）。
    - 没有类型系统与数据流分析时，这类约束只能用形态匹配近似（如
      `log.Error` 在某祖先函数出现一次），狗粮价值弱（自家仓库违规可控，
      结论外推性差）。
    - 决策：按"类型/数据流/全程序分析不自研、走 ingest"纪律，交给
      `metricgate ingest` 适配 staticcheck / PMD / SpotBugs 等成熟规则，
      不在 metricgate 内重造。文献证据同 P3 第 1 条（type inference）。
    - 触发条件：未来若 metricgate 用户报告"必须经 logger / 必须处理 error"
      这类约束的高频需求，由 ingest 路线引入；本轮 metricgate 自身不加。

**P3（明确不做，引用为证）**

1. **类型/数据流/全程序分析**：文献（如 deep learning type inference）表明这是独立且深厚的方向 →
   坚持 `ingest` 集成（staticcheck/PMD/SpotBugs），不进程内重造。
2. **进程内 LLM 评审**：LLM code review 文献存在，但与"metricgate=确定性事实层、宿主负责推理"的
   定位冲突 → 不引入，交给宿主 agent。

## 5. 威胁与边界（诚实声明）

- **被引数≠质量**，且**近期 LLM 论文多为预印本**（CodeMirage/CoHalLo 等 2024–2026），尚未经充分同行复核；
  本文件只用它们校准方向，不作为结论证据。
- **基准会饱和**（SWE-bench 2024/2025 迅速被刷高）→ 不把任何单一 benchmark 当验收。
- OpenAlex/arXiv/GitHub 的检索是**抽样**，非系统综述；星数随时间变化。
- 本文件不含对 metricgate 自身效果的承诺；效果仍需 `docs/ab-experiment.md` 的 A/B 实测。
