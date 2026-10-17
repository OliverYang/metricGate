# metricGate 文档索引

docs/ 按主题分 6 个目录，根目录只保留 4 份核心产品文档。各目录内文档互不重复、按"设计 → 执行 → 结论"排列。

---

## 根目录 · 核心产品文档

| 文档 | 说明 |
|---|---|
| [capabilities.md](capabilities.md) | **能力清单（汇总）**：metricGate 现有能力的权威清单，按约束族 A–F 组织，含能力边界（AST-only、无 autofix 等） |
| [claims.md](claims.md) | **价值主张台账**：每条对外 claim 的证据来源与验证方式，表述以本文件为单一事实源 |
| [workflow.md](workflow.md) | **Agent 循环工作流**：把闸门接进 coding agent 循环的三个接口（pre-generation 约束 / post-edit 校验 / review bundle） |
| [metrics.md](metrics.md) | **度量定义参考**：内置度量的口径、阈值与输出说明 |

## [pilot/](pilot/README.md) — 规范文档 → policy → 真实项目检查（MVP 试点）

端到端验证"团队非标规范能否转化为可执行门禁"的全流程记录，入口见 [pilot/README.md](pilot/README.md)。

| 文档 | 说明 |
|---|---|
| [alibaba-manual-research.md](pilot/alibaba-manual-research.md) | 《阿里巴巴 Java 开发手册》43 条抽样逐条标注可检查性（✅17 / ⚠️13 / ❌13）与 P0–P2 优先级——试点的规范源头 |
| [biz-spec-01-命名与注释规范.md](pilot/biz-spec-01-命名与注释规范.md) | 模拟"扫描门户团队研发规范 01"（试点输入物料），条款 NAMING/COMMENT |
| [biz-spec-02-异常与日志规范.md](pilot/biz-spec-02-异常与日志规范.md) | 模拟团队研发规范 02，条款 LOG/EXC |
| [biz-spec-03-并发与性能规范.md](pilot/biz-spec-03-并发与性能规范.md) | 模拟团队研发规范 03，条款 CONC/PERF |
| [biz-spec-04-工程分层与依赖规范.md](pilot/biz-spec-04-工程分层与依赖规范.md) | 模拟团队研发规范 04，条款 ARCH/DEP/SEC |
| [conversion-map.md](pilot/conversion-map.md) | **40 条规范条款 → policy 规则逐条转化对照**（✅15 / ⚠️5 / ❌20）+ 6 条工程教训；工具能力空隙清单，试点最有价值产出 |
| [pilot-report-scan-portal.md](pilot/pilot-report-scan-portal.md) | 试点夜间总结：真实项目 563 error / 523 warning 结果分析、误报修正过程、6 项产品建议、复审清单 |

## [strategy/](strategy/) — 产品定位与市场

| 文档 | 说明 |
|---|---|
| [positioning.md](strategy/positioning.md) | **现行定位（v2.0）**：三轴结构性事实 + 规则归属，含 2026-10 五家同格竞品表与六项不做 |
| [value-proof.md](strategy/value-proof.md) | "本地 · 0 token · 前后校验"为什么有价值，以及用什么实验证明 |
| [competitor-analysis.md](strategy/competitor-analysis.md) | 竞品调研总结（v1.0、2024-09-28，**已被 positioning.md v2.0 取代**，仅存档） |
| [competitor-research.md](strategy/competitor-research.md) | 同级别竞品能力调研与吸收清单（2026-09） |
| [competitor-research-summary.md](strategy/competitor-research-summary.md) | 竞品调研总结（另一版归纳，与 competitor-analysis.md 内容相近；**同样已被取代**，仅存档） |

## [constraints/](constraints/) — 约束识别 MVP 工作流

从团队访谈/文档中收集约束、转化为规则的 MVP 方法论与周计划记录。

| 文档 | 说明 |
|---|---|
| [llm-code-acceptance-spec-v1.md](constraints/llm-code-acceptance-spec-v1.md) | **LLM 生成代码验收规约 v1**：按四类失败模式组织条款（LC-10x~40x），红线/提示分级 + scorecard 达标判据 + 豁免机制；下一轮试点的规范输入 |
| [constraint-conversion-sop.md](constraints/constraint-conversion-sop.md) | **非标规范→约束规则转换 SOP v1**：提取→三档转化→fixture 烟测→试点 FP 审计→接进 LLM 闭环五步，含引擎语义陷阱清单 |
| [incident-to-constraint-playbook.md](constraints/incident-to-constraint-playbook.md) | **事故报告→约束规则 Playbook（T1a）**：事故卡八字段模板 + 六步流程 + 4 例试点（CONC-07 新增落地、2 复现、1 缺口台账），事故输入自带 fixture 与优先级 |
| [supply-chain-feed.md](constraints/supply-chain-feed.md) | **供应链告警→门禁规则：外部 CLI 适配层**：scripts/osv2policy.sh 编排外部 osv-scanner → 适配器转换/查重/文本级合并（回读校验，绝不写输入）→ 独立 patch 人批；tripwire 语义与三条边界（版本盲/传递依赖/编译耦合）；外部工具保持外部，引擎与 CLI 不为具体外部格式长代码；Loop A 数据循环首个接线的触发器 |
| [rule-integration-interface.md](constraints/rule-integration-interface.md) | **规则集成接口：LLM 提案，引擎裁决**：`constraints rule`——LLM 按 policy 词汇出 Spec JSON + 反例/正例 fixture，引擎真跑验证（没有反例的规则不可信；其他规则命中不干扰判分），返回 PASS/FAIL + 可合并 policy 片段；节点全落盘可改/重跑/回退，集成者不读 metricgate 源码；合并永远是人批动作（SOP 4–5） |
| [constraint-mvp-design.md](constraints/constraint-mvp-design.md) | 约束识别 MVP 设计方案（收集 → 转化 → 反馈三段） |
| [constraint-inventory.md](constraints/constraint-inventory.md) | 约束清单（初版） |
| [constraint-inventory-final.md](constraints/constraint-inventory-final.md) | 约束清单（最终版） |
| [interview-questions.md](constraints/interview-questions.md) | 团队约束调研访谈问题清单 |
| [policy-gaps.md](constraints/policy-gaps.md) | 策略完备性审计（基于 `metricgate rules gaps`） |
| [week1-summary.md](constraints/week1-summary.md) | Week 1 约束调研总结 |
| [day2-3-summary.md](constraints/day2-3-summary.md) | Day 2–3 数据收集总结 |
| [week2-guide.md](constraints/week2-guide.md) | Week 2 规则实现指南 |

## [evaluations/](evaluations/) — 实验与评估

> **引用任何一条数字前先看** [evaluations/criterion-version-index.md](evaluations/criterion-version-index.md)：判据改过 7 次，
> 老报告的数字全部不可按当前工具复现。索引给出每篇报告的采集日期、当时的 main commit、之后变过哪些判据，
> 以及 ❌/⚠️/✅ 三档读法——没有一篇被推翻，被改的是引用它们的方式。

| 文档 | 说明 |
|---|---|
| [evaluation.md](evaluations/evaluation.md) | 确定性约束能力总评估（2025-09 基线） |
| [evaluation-phase-b.md](evaluations/evaluation-phase-b.md) | Phase B 评估：包级度量/MI/API diff/克隆/热点 |
| [evaluation-phase-d.md](evaluations/evaluation-phase-d.md) | Phase D/E 评估：preview / explain / token pack / delegate bundle |
| [evaluation-trends.md](evaluations/evaluation-trends.md) | run-to-run 稳定性 + diff-only 噪声比（量化评估） |
| [evaluation-base-rate.md](evaluations/evaluation-base-rate.md) | **R1 基率实验（实测）**：agent 生成代码违反本仓库约束的概率 |
| [evaluation-reuse.md](evaluations/evaluation-reuse.md) | **R1b 重造率实验（实测）**：agent 会不会重写仓库已有实现 |
| [evaluation-prompt-vs-gate.md](evaluations/evaluation-prompt-vs-gate.md) | **一句提示 vs 静态分析（实测）**：谁在防重复 |
| [evaluation-hint-and-cost.md](evaluations/evaluation-hint-and-cost.md) | 提示强度、探索成本与 SDP 冲突：三个后续实验设计 |
| [evaluation-r2-repair-loop.md](evaluations/evaluation-r2-repair-loop.md) | **R2 修复回路实验（实测）**：findings JSON 喂回 agent 自修，7/7 达标、1.72× token、0 功能丢失 |
| [evaluation-r3-grounding.md](evaluations/evaluation-r3-grounding.md) | **R3 前置接地实验（实测）**：constraints digest 注入，首过违规 31.8%→4.5%，P+B 复合终态 0/22 |
| [evaluation-m2b-paper-metrics-protocol.md](evaluations/evaluation-m2b-paper-metrics-protocol.md) | **M2-B 协议（预注册，已执行）**：论文指标阈值（npath/fan-out/boolean-params 等）对 LLM 代码的净效应三臂实验 + 人类代码分布基线 |
| [evaluation-m2b-taskset-paper-metrics.md](evaluations/evaluation-m2b-taskset-paper-metrics.md) | **M2-B 任务集（预注册附件，已验证触发）**：10 任务落包 + 坏形状参考实现（npath/fan-out/boolean-params/test-no-assert/layer-violation 全部引擎实测 fire）+ 沙箱泄露点补充清单 + 三臂 prompt 模板 |
| [evaluation-m2b-results.md](evaluations/evaluation-m2b-results.md) | **M2-B 执行报告（实测，30 run）**：H1 证伪（新阈值行注入净效应 0→digest 维持 curated）、H2 未触发、H3 部分成立（boolean-params 超阈率 90× 人类 = 模型特有；npath/fan-out 为通用门禁）；U04 未复现归因（包内先例救了 V1/V2）+ V3 L1 参数注入防住 B 类 |
| [evaluation-m2-arch-protocol.md](evaluations/evaluation-m2-arch-protocol.md) | **M2 主协议（B 类，预注册）**：5 个架构诱导任务 × L0/L1 两臂，判定 digest 惯用法/先例注入能否把 B 类预防拉到 ≥3/5（claims #18 判定实验） |
| [evaluation-m2-arch-results.md](evaluations/evaluation-m2-arch-results.md) | **M2 主协议执行报告（实测，10 run）**：H1-B 证实——3/3 有效对全部防住（最严口径 3/5 达线）；L1 agent 自发写 port+组装根注入（机制证据逐字）；claims #18 ⏳→⚠️ |
| [evaluation-commit-replay-protocol.md](evaluations/evaluation-commit-replay-protocol.md) | **提交级研判协议（预注册）**：真实 LLM 生成开源仓库（dev-cockpit Go + ace-ts TS）commit 级检出 + 挑拣纠正重放 + 误伤对照（H0），判定线 H2 ≥6/10 一轮收敛 |
| [evaluation-commit-replay-results.md](evaluations/evaluation-commit-replay-results.md) | **提交级研判执行报告（实测，dev-cockpit 289 commits）**：0/289 零检出 commit；error 清零 5/5；一轮收敛 1/5 证伪（真实仓库比合成任务难）；副作用 2 warning/0 error；test-no-assert 215/289（按 commit 计数）。⚠️ 两处已就地修正：215 已于 iter-30 用当前判据重算（含义变了：全部来自 1 个函数），§2 的「规则族触发总量」18 行里 13 行**不可从归档复现** |
| [evaluation-iter29-remediation.md](evaluations/evaluation-iter29-remediation.md) | **iter-29 五项整改实测**：TS 箭头函数从不可见到可量（同文件可维护性 19→51）、`test-no-assert` 误报 55→19（本仓 8→0）、规则中文目录到前端、运行历史记录触发命令（含脱敏）、harness 接线样例。本仓 422→431 warnings / 0 errors 不变 |
| [evaluation-iter30-tna-recalibration.md](evaluations/evaluation-iter30-tna-recalibration.md) | **iter-30 重标定（实测，289 commit × 双判据重放）**：215/289 在新旧判据下都成立且可从归档复现，但按独立事件只有 1 个函数（anchors 31→2→1，findings 1,783→233→215）；`Benchmark*` 已从 `test-no-assert` 排除（语料 19→5，0 条真阳丢失）；另附跨语言函数单元普查与旧报告 13 行不可复现的逐条对照 |
| [iter-30 迭代日志](iterations/iter-30-recalibration-and-benchmark-scope.md) | iter-30 全过程（重放口径的三个坑 / 215 的机制 / benchmark 摘出 / 普查的负面结论 / iter-31 排期与 `triaging` 的两条负结论） |
| [ab-experiment.md](evaluations/ab-experiment.md) | LLM 约束效果 A/B 实验设计（下一步核心验证） |
| [criterion-version-index.md](evaluations/criterion-version-index.md) | **判据版本索引（iter-31）**：每篇评估报告的数字来自哪把尺子——采集日期→当时 main→之后变过的 7 项判据，逐篇标 ❌ 不可复现 / ⚠️ 连判据版本一起引 / ✅ 当前判据；含「policy 与二进制必须同版本」这条用错误结论换来的前置 |

## [research/](research/) — 调研笔记

| 文档 | 说明 |
|---|---|
| [research-notes.md](research/research-notes.md) | 研究与工程实践参考（指导 metricgate 迭代的主笔记） |
| [llm-code-constraint-research.md](research/llm-code-constraint-research.md) | **LLM 生成代码约束论文调研**：9 个方向 + 采纳/改编/拒绝决策（含 P0–P2 排期） |
| [llm-constraint-paradigm.md](research/llm-constraint-paradigm.md) | **约束新范式设计**：把 9 方向收拢为五支点闭环（接地→rails→验收→修复→演进），附真实命令与 scorecard JSON 契约 |
| [research-harness-and-constraints-2026.md](research/research-harness-and-constraints-2026.md) | 大模型产出约束工具 × harness 工程：业界全景（2026-Q3） |
| [roadmap-research.md](research/roadmap-research.md) | 确定性约束能力补全调研与行动方案（2025-09） |
| [digest-open-questions-research.md](research/digest-open-questions-research.md) | **digest 待解问题定向文献调研**：任务条件化注入先例（AutoGuide）+ 1.74× 成本归因实验设计（30× 方差警告 → k≥5 中位数 + 三桶模型）+ B 类惯用法提示文献支撑与空白 |
| [product-evolution-directions-2026-10.md](research/product-evolution-directions-2026-10.md) | **产品演进方向调研（2026-10）**：短期收敛闸门可信度与交付质量（iter-19 ~ iter-20），中期扩展规则表达力（iter-21 ~ iter-23），v2 决策输入。§7 排期表已按「已完成轮次 / 下一轮」重写两次，最新排期见 [iter-31 建议](research/product-evolution-directions-2026-10.md) §7.3 |

## [design/](design/) — 设计契约与方案

| 文档 | 说明 |
|---|---|
| [phase-b-design.md](design/phase-b-design.md) | Phase B 设计契约：确定性包/文件级约束与省 token 能力（B-1~B-5） |
| [goal-contract-p0-a-explain-preview.md](design/goal-contract-p0-a-explain-preview.md) | Goal 契约：P0-A `explain` 与 `check --preview` |
| [goal-contract-p1-a-token-aware-pack.md](design/goal-contract-p1-a-token-aware-pack.md) | Goal 契约：P1-A Token-aware Context Pack v2 |
| [goal-contract-p1-b-delegate-bundle.md](design/goal-contract-p1-b-delegate-bundle.md) | Goal 契约：P1-B Delegate Bundle / `metricgate bundle` |
| [metricgate-ux-bundle-design.md](design/metricgate-ux-bundle-design.md) | 可解释选择、预算 context 与宿主 Agent Bundle 技术方案 |
| [metricgate-open-codereview-optimization.md](design/metricgate-open-codereview-optimization.md) | 可用性优化方案（参考 Open Code Review） |
| [constraint-pipeline-design.md](design/constraint-pipeline-design.md) | **规约→约束原语→预检门禁→流程硬控四段流水线设计**：内置名单降为兑底，三缺口任务分解（转换工具面/DSL 原语/LC-403/404 闸门化）+ 价值主张红线 |
| [digest-engineering-checklist.md](design/digest-engineering-checklist.md) | Constraint Digest 工程考量清单（12 条，含非标输入兼容矩阵）+ R3 前置接地实验预注册 |
| [open-codereview-reference.md](design/open-codereview-reference.md) | Open Code Review 文档整理与工程设计参考 |
| [cockpit-sonarqube-parity.md](design/cockpit-sonarqube-parity.md) | Cockpit 复刻 SonarQube 交互与界面：参考源调研、概念映射、逐页方案、D1–D6 决策与 P0/P1/P2 分批 |
| [cockpit-single-file-tech.md](design/cockpit-single-file-tech.md) | Cockpit 单文件形态技术方案：8 段结构、事件委托、html 模板、store/路由、闸门补偿规则与实施顺序（承接 D4=C′） |

---

*整理时间：2026-09-29。文件移动已用 git mv 保留历史；全库交叉链接经 link-checker 校验通过。*
