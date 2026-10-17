# M2-B 评测协议：论文指标阈值对 LLM 生成代码的指导意义（预注册）

状态：**协议已注册，未执行**（LLM 跑批未开始）。本文档在实验前写定判断线，防止事后挑数字。
日期：2026-10-07。前置：R1/R2/R3（同批 22 任务、单模型 glm-5.3-flash）已建立基线方法论。

> **判据版本（iter-31 补标）**：执行见 [evaluation-m2b-results.md](evaluation-m2b-results.md)（跑批 2026-10-09，
> 判据介于 `8f78d0c`–`429e7a8`）。`test-no-assert` 的判据此后改过两次：`ecd57b5`（拿到 `*testing.T` 的
> helper 即算断言）、`d2bcf9b`（只算 `Test*`，benchmark 不再被要求断言）。协议里凡把这条规则当**阈值/指标**
> 读的段落都是**分类错误**——它是规则非阈值，且判据已变；引用执行数请连同判据版本一起引。
>
> 全仓判据版本索引：[criterion-version-index.md](criterion-version-index.md)（每篇报告的采集日期 → 当时 main → 之后变过的判据）。

## 0. 一句话

R3 已证明复杂度阈值注入能预防 C 类违规（31.8%→4.5%）；本实验回答**新增的论文指标
（npath / fan-out / boolean-params / call-chain-depth / test-no-assert）对 LLM 代码
是否抓到真问题**——用两臂对照测量：阈值是否改变生成行为、修复回路是否一轮收敛。

## 1. 动机与已知证据

- **已测**：R3 C 类（cyclomatic/length/nesting）5/5 预防，且阈值数字改写实现形态
  （T11 298 行巨石 → 6 函数/122 行）。NPATH 是复杂度家族的补位项——agentic preset
  刻意放宽 function-length 后，NPATH 抓"每段看着不大"的路径爆炸。
- **机制假设、未测**：test-no-assert 针对 LLM 特有失败（伪绿测试骗过 gate-PASS 验收）；
  boolean-params / call-chain-depth 针对 LLM 从训练分布模仿的 API 形状坏习惯。
- **人类代码分布（本项目 2026-10-07，metric_snapshots 34173/12400/12400 条测量）**：
  npath p50=4 p90=48 p99=432 max=4752，超 warn 200 占 2.20%；
  fan-out p50=4 p90=9 max=20，超 warn 10 占 5.52%；
  boolean-params p50=0 max=4，超 warn 2 占 0.02%。
  → 观察期阈值配置合理：warn 在分布尾部之内，绝大多数代码不受扰。

## 2. 任务集与两臂

- **任务集**：B/C 类混合 n≥10（M2 原设计 B 类 n≥5 升级为混合集），从 R1 任务集
  抽 7 个新写任务 + 3 个 U04 变体（架构诱导）+ 覆盖四类新指标的可触发任务
  （大状态机/解析器诱导 NPATH；编排型任务诱导 fan-out；配置切换型任务诱导
  boolean-params；带测试产出的任务诱导 test-no-assert）。
- **Arm L0**（基线）：任务文本 + 固定引导语，无 digest。
- **Arm L1**（全量 digest）：现行 digest（6 项精选指标阈值 + 红线 + 复用清单）。
- **Arm L2**（+新指标）：L1 基础上 digest 的 threshold 节加入 npath/fan-out/
  boolean-params 行（call-chain-depth/test-no-assert 是规则非阈值，走红线节）。
- 判读：**L2−L1 的差 = 新指标阈值注入的净效应**；L1−L0 复核 R3 结论。

## 3. 预注册判断线

1. **H1（预防）**：L2 臂在新指标上的首过违规率 < L1 臂。判定：≥2/10 任务由新
   指标预防（首过 0 新指标违规，而 L1 同任务非 0）→ H1 证实；0/10 → 推翻
   （结论：这些阈值对 LLM 无指导意义，降级为报告型观察项）。
2. **H2（修复收敛）**：若 L1 臂有新指标违规，修复回路（findings JSON，≤3 轮）
   首轮收敛率 ≥ C 类历史水平（6/7 一轮过）。
3. **H3（特异性）**：LLM 代码新指标分布 vs §1 人类分布——npath 超 200 占比、
   fan-out 超 10 占比显著高于人类基线（≥2×）→ "抓到模型特有失败模式"成立；
   与人类分布无差异 → 阈值只是通用代码风格约束，不构成对 LLM 的特殊价值。
4. **完整性红线**（沿用 R3）：22/22 功能签名完整、零 tracked-file 改动、
   scorecard 五红线 0；anti-cheat 干净。

## 4. 度量与记录

- 每任务记录：首过 findings 按 rule_id/metric 分列；gen tokens；digest tokens；
  修复轮次与每轮 tokens；npath/fan-out/bool 实测值全量（trend 库自动存储）。
- 统计口径：n 小，只报描述统计+逐任务归因，不做显著性检验（沿用 R3 诚实边界）。

## 5. 执行前提与诚实边界

- 需要真实 LLM 跑批（单模型、第三轮同任务集污染对称）——无法由引擎侧单独完成。
- fan-out 口径是 distinct callee 拼接文本（无类型解析），跨包同名调用会合并；
  已在 docs/metrics.md 声明。若 L2 差异不显著，先检查是否口径太钝再下结论。
- call-chain-depth 默认关闭 fluent-builder 误报风险（本仓 0 命中）；测试产出任务
  若框架本身不写断言（如纯重构任务），test-no-assert 判读需人工核任务语义。

## 6. 复现

- 任务集与 prompt 模板：R1 任务集 + 本文 §2 构造规则。
- 人类分布基线：`sqlite3 .metricgate/history.db` metric_snapshots 表，查询见 §1。
- digest 生成：`metricgate constraints digest -pkg <pkg>`（L2 需 thresholdRows
  扩展，见下）。

## 附：待办（执行 L2 前的小改动）

- internal/digest/digest.go thresholdRows（6 项精选）是否加 npath/fan-out/
  boolean-params：**执行 L2 时加**（L1/L2 两臂差异即其净效应）；平时保持精选，
  避免 digest 预算膨胀（441 tok 均价的红线）。
