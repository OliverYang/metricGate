# Phase B 设计文档：确定性包/文件级约束与省 token 能力（设计评审版）

> 本文档是 Phase B 实现的**合同**：先评审定案，后按文档逐步实现。
> 状态标记：✔ 已实现并通过测试；⬜ 未实现。
> 每轮评审记录了提案 → 反方质询（5 项判据）→ 裁定。反方判据固定为：
> **F 公式与文献一致性**、**I 集成成本（行数/侵入面）**、**T token 开销**（产物是否进 prompt、多大）、
> **FP 误报风险**（在真实仓库首跑即验证）、**D 狗粮风险**（自家仓库会不会自爆）。

## 0. 方法论与文献依据

| 能力 | 文献/工程依据 | 核心公式或机制 |
|---|---|---|
| 包耦合度 | Robert C. Martin, *OO Design Quality Metrics* (1994)；*Agile Software Development* (2002) Ch. 20-21 | Ca、Ce、I=Ce/(Ca+Ce)、A、D=dist; SDP 依赖方向 |
| 圈复杂度 | Thomas McCabe (1976), *IEEE TSE* | V(G)=E−N+2P（我们已有函数级实现，Phase B 复用不重做） |
| Halstead | Maurice Halstead (1977), *Elements of Software Science* | V=($\eta_1$+$\eta_2$)·log₂($\eta_1$+$\eta_2$) + ... 简化为体积项，见 B-2 |
| 可维护性指数 | Paul Oman & Jack Hagemeister (1992)；Microsoft VS *Maintainability Index* (2007, scale-restored) | MI = max(0,(171−5.2·lnV−0.23·CC−16.2·lnLOC)·100/171) |
| 重复代码 | PMD CPD；Baker (1992) *Parameterized Duplication*；Johnson (1994) winnowing, *SIGMOD* | k-gram 指纹 + 最小窗口保证；Phase B 用函数级 Jaccard 近似（见评审轮 4 取舍） |
| 变更热点 | Michael Feathers (2004) 学术页 "Hotspot Analysis"；Adam Tornhill, *Your Code as a Crime Scene* (2015) | churn(复杂度) 气泡分析；git log --numstat 乘复杂度 |
| API 兼容 | Google eng-practices "small CLs"/API review；SemVer v2.0.0 spec | 导出面快照 diff：removed/changed 签名=breaking，added=safe |
| token 经济 | 项目立项原则（constraint pack ≈ 4 char/token 实测） | 所有新增 context 产物按"预算内行数 + 预算降级"接入现有 render ladder |

非目标（本轮不做，理由见评审）：类型系统分析（集成领地，Phase B-6 仅留 ingest 适配器接口）、
自然语言→规则、跨仓库解析、IDE 插件。

## 1. 评审轮 1 —— 提案池筛选

**提案池**（11 项）：包耦合度量 Martin、MI、API surface diff、winnowing 部分克隆、
churn 热点、循环复杂度（已有）、CyclomaticDX 式认知复杂度（已有）、test coverage 集成、
semgrep 集成、golangci-lint ingest、PMD ingest。

**反方质询（要点）**：
- coverage/semgrep/golangci-lint/PMD 属集成适配器，不产生新方法论，且依赖外部二进制（CI 环境约束），
  无法在本轮做**自包含的量化对比** → 移入 Phase B-6（ingest 框架），本轮只定接口形状。
- 循环/认知复杂度已有（Phase A 前完成），重做无增量 → 排除。
- 其余 5 项均为：纯树/文本可达、公式有文献、阈值可闸门化、产物可进工单 → **入选 B-1..B-5**。

**裁定**：B-1 Martin 包度量、B-2 MI、B-3 API surface diff、B-4 部分克隆、B-5 churn 热点。
B-6 ingest 框架只做接口设计（不实现，见轮 5）。

## 2. 评审轮 2 —— B-1 Martin 包度量：公式与架构放置

**提案**：对 Phase A-2 的包依赖图（`internal/arch.Graph`）直接计算：
- Ca（afferent，被依赖数）、Ce（efferent，依赖数）；
- I (instability) = Ce/(Ca+Ce)，0..1；
- A (abstractness)：包内抽象类型数/总类型数（Java：interface+abstract class；Go：interface）；
- D (distance) = |A + I − 1|（主序列距离）；
- SDP (Stable Dependencies Principle)：I(从) > I(到) 的边为违规（依赖应指向更稳的包）。

**反方质询**：
- F：公式与 Martin 1994 一致 ✔。I 分母 Ca+Ce=0 时定义为 0（孤立包最稳）。
- I：`arch.Graph` 已有 `Packages()/Edges()`，`check.go` 已有 matcher 模式——新文件 `pkgmetrics.go` 纯函数，
  引擎照 architecture 桥接模式接（预计 <150 行桥接）。✔
- T：**关键裁定**——D 和 SDP 进**报告不进闸门**（异味/设计债，不是阈值违规）；policy 可配 `architecture.sdp: error|warning` 把 SDP 提升为闸门。
- FP：主序列 D 对"实用工具包"（I=1,A=0，比如 adapters）会报高 D——文档明确 D 是**信号不是错误**，
  默认只在 context 里给 top-3，不产生 finding。SDP 同理默认 warning。
- D：狗粮仓库是三层 cli/core/adapter——预期 I: cli≈1, core≈0，SDP 若报出 core→cli 类边即价值证明。

**裁定**：✔ 按提案实现；D 只报告；SDP 默认 warning 可升 error；Ca/Ce/I/A 全量进 metrics 快照（趋势库）。

## 3. 评审轮 3 —— B-2 可维护性指数：Halstead 简化与阈值

**提案**：文件级 MI，按 Oman/VS 公式（scale-restored）：
`MI = max(0, (171 − 5.2·ln(V) − 0.23·CC − 16.2·ln(LOC)) × 100/171)`
- V 用 Halstead 体积：V = N·log₂(η)，η=η1+η2（distinct 运算符+操作数），N=总 token 数（按语言 token 归一化计数）。
- CC = 文件内函数圈复杂度之和（已有）；LOC = 文件行数（已有）。
- 阈值：VS 口径 <65 红档 → error；65..85 黄档 → warning。

**反方质询**：
- F：Halstead 完整版有 V/G/L/D/E 多个量，此处只用体积 V——**声明为"MI(Halstead-V) 变体"**，文档写明偏差
  （完整 E=V·D 对现代代码噪音大，学界已有批评，Sherer et al. 1990 实证支持只用体积）。
- I：Halstead 计数需要遍历叶子 token——`clones.fingerprint` 已做叶子遍历，但**不共享**（不同归一化）；
  在 metrics 包新写 `halstead.go`（~80 行）：具名叶子=操作数，匿名叶子文本=运算符。
- T：MI 是**单值/文件**，进 metrics 快照与 metrics 表；越限时 finding 一条/文件。省 token ✔。
- FP：ln(V) 在 V=0（空文件）时 NaN——**公式卫：V<1 取 1**；生成代码大文件（3.8MB fixture）注意性能：叶子计数 O(n) 可接受。
- D：预期自家仓库 go 文件 MI 大多 90+，报不出 error；若首跑报出低 MI 文件，核实是否真该修。

**裁定**：✔ 按"MI(Halstead-V) 变体"实现，阈值 65/85 可覆盖。
**【狗粮修正，实现后追加】**：文件级 ln(LOC) 项在真实文件规模（100-800 行）下数学性压制其他项，
65/85 档位不可达（400 行文件 MI 上限≈44）——预注册的 FP 预案触发。修正为**函数级 MI**
（VS code-metrics 的实际口径），文件报告最差函数并锚定其上；公式与手算锚点不变。
进一步狗粮实测：本仓库健康函数 MI 集中在 55-65，65/85 档位即使函数级也过紧（0.23·CC 项惩罚
正常分支），故默认**只报告不闸门**（metrics 表新增 MI 列 + 快照进趋势库），policy 可显式开启。
这是"阈值类进闸门、异味类只报告"纪律的应用。

## 4. 评审轮 4 —— B-3 API surface diff 与 B-4 部分克隆（机制取舍）

**B-3 API surface diff 提案**：
- `metricgate api snapshot -o api.json`：从 facts 导出面快照（exported 符号+签名，Go 大写/Java public）。
- `metricgate api diff -base api.json`：removed / signature-changed / visibility-lowered → **breaking**（error）；
  added → safe（报告）。
- 文档依据：SemVer（major 版本变化应对应 breaking）；Google 代码评审准则。
- **反方**：T——API 报告全量导出面可能大；裁定：diff 输出**只列变更条目**（不是全量表），
  快照文件不进 prompt；context 产物零增加。
- 裁定 ✔。

**B-4 部分克隆提案（两案对比）**：
- 方案 A（winnowing 全量）：按 Johnson 1994，k-gram+窗口下采样，做子句级部分克隆。代价：新算法 + 归一化调参，
  误报面大，估 2-3d。
- 方案 B（函数级 Jaccard）：Type-2 指纹（A-3 已有）改进为**集合指纹**：函数体 = 归一化 token 集合（shingle），
  两函数共享 token 比例 J(A,B)=|A∩B|/|A∪B| ≥ 0.8 且行数 ≥10 → near-clone（warning，报告不闸门）。
  精确候选：先用 A-3 的 64 位 hash 桶分桶，只对同桶/相似桶做 Jaccard，避免 O(n²)。
- **反方**：F——方案 B 非 winnowing，是集合近似；**裁定为"文献妥协"**：真正部分克隆（局部复制粘贴）
  需要窗口算法，函数级 Jaccard 只抓"整函数近似重复"。**取 B 为 v1**（估计覆盖 LLM 生成场景的
  "改名+小改"复制），方案 A 记为未来工作（需误报数据支撑调参）。
**【实现注记】**：rare-shingle 倒排（postings ≤8 才进入配对）+ 每 anchor 上限 3 条，
报告由 `metricgate nearclones` 独立命令输出（不复用 engine per-file 管线——保留全程 shingle
集合需要全程持有 parse 树，内存不划算；且 B-4 裁定报告不进闸门）。狗粮首跑：13 对，
其中测试代码相似度 87-100%（测试样板是已知热点，符合 Tornhill 的观察），无生产代码误配对。
- T：near-clone 进报告与 context 复用区（每类一行）。
- 裁定 ✔ 方案 B。

## 5. 评审轮 5 —— B-5 churn 热点 + B-6 ingest 接口形状

**B-5 churn 提案**：
- `metricgate hotspots [path] [-since 3.months]`：`git log --numstat --since` 汇总每文件 added+deleted（churn），
  与 MI（B-2）联合输出 **churn × (101−MI) 热点分** = churn·(101−MI)/100（归一化 churn 后 0..100）。
- 文献：Feathers hotspot（churn×复杂度）；Tornhill（工业案例：热点 top-10 集中多数缺陷）。
- **反方**：需要 git 仓库（非 git 目录 → 命令报错退出 2，明确不硬造）；只报告不闸门（历史数据不是代码约束）；
  LLM 用途：告诉模型"这些文件高风险，改之前先读"→ context 可选区（-with-hotspots，默认关）。
- 裁定 ✔。

**B-6 ingest 接口**（只定形，不实现）：
`metricgate ingest --tool golangci-lint -- sarif.out`：读外部 SARIF/JSON → 归一化 Finding → 合并报告/趋势库。
本轮产出：CLI 参数与 SARIF 字段映射表（文档），不写代码。理由：无外部二进制则无自包含评估。

**总裁定**：实现 B-1..B-5；每个能力独立提交；每步 `go test ./...` + `metricgate check .` 狗粮。

## 6. 实现顺序与验收标准（对照本文档逐条验收）

| 项 | 文件 | 验收标准 |
|---|---|---|
| B-1 ✔ | internal/arch/pkgmetrics.go + engine 桥接 + 测试 | Ca/Ce/I/A/D/SDP 公式正确（单测精确值）；快照进 metrics；SDP 默认 warning；自家仓库跑通 |
| B-2 ✔ | internal/metrics/halstead.go + mi.go + 测试 | MI 公式对拍手算值；空文件/单行文件不 NaN；阈值 65/85；快照进 metrics |
| B-3 ✔ | cmd api snapshot/diff + facts 导出面 | removed/changed→error, added→报告；-base 读快照；自家快照 diff=空 |
| B-4 ✔ | internal/clones/jaccard.go + 测试 | 相似对 ≥0.8 报告；锚定在"较新"成员；不产生 error 级 finding |
| B-5 ✔ | internal/hotspots/hotspots.go + cmd hotspots | churn 分公式可对拍；非 git 目录退出 2；报告格式含 top-N |
| 评估 ✔ | docs/evaluation-phase-b.md | 量化：性能开销对比、token 开销对比、狗粮修复记录、阈值验证 |

## 7. 量化评估协议（预注册，防止事后挑数字）

1. **性能**：Go toolchain src（约 5600 文件）开启 B-1..B-5 前后各 3 次取中位数（wall clock）。
2. **token**：context pack 在 `internal/...` 上的字符数（≈/4 = token），B-4/B-1 产物加入前后对比；预算降级验证（6000 字符上限不超）。
3. **狗粮**：`metricgate check .` 首跑 findings 清单；每条的处置（真实修复/豁免+理由/阈值调整+理由）。
4. **公式对拍**：每个度量的手算用例（写在测试注释里），与实现值逐一对照。
5. **误报审查**：在自家仓库 + Go toolchain 采样 200 文件上人工核对 warning 是否值得开发者时间（诚实记录否决率）。
