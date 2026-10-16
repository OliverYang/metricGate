# Constraint Digest 工程考量清单 + R3 前置接地实验预注册

> 目标：在 LLM 动手**之前**注入任务级约束摘要（constraint digest），把范式五支点的支点 1（前置接地）从"接口已有"推进到"实测有效"。
> 本文先列工程考量（每条附处理决策），再预注册 R3 实验设计（含可推翻假设），之后才动手实现。
> 依据：R2 实测架构类违规修复成本 6.59× 生成成本（[`../evaluations/evaluation-r2-repair-loop.md`](../evaluations/evaluation-r2-repair-loop.md)）——预防远便宜于修复；
> R1b 实测看见既有实现 = 100% 复用率（[`../evaluations/evaluation-reuse.md`](../evaluations/evaluation-reuse.md)）——digest 必须指向既有代码。

## 1. 工程考量清单（12 条）

| # | 考量 | 处理决策 |
|---|---|---|
| 1 | **任务级裁剪 vs 全量倾倒**：全量 policy 直接塞给 LLM 会让注意力稀释（YAML 正则也不是模型需要的语义） | digest 按**目标包**裁剪：只含该包适用的分层契约 + 全语言禁 API + 生效阈值 + 该包复用指引。目标 ≤80 行 / ~1K token |
| 2 | **确定性生成**：摘要若由 LLM 编写则不可审计、不可复现 | digest 路径**零 LLM 调用**：每行由 policy/arch/symbols/deps 确定性导出；同输入逐字节同输出（value-proof R5 治理要求的可门禁性） |
| 3 | **非标输入兼容矩阵**：真实仓库的 policy 形态千差万别 | 显式定义降级：无 policy→内置默认+标注"未配置"；无 arch 层→省略分层节；无禁 API→"（无）"；未知包→告警+全仓摘要；Java→同逻辑换语言参数；policy 里未知规则类型→跳过不崩 |
| 4 | **豁免精度**：把不适用于目标包的禁令也塞进去 = 教 agent 无视摘要 | except_files/allowed_files 参与 digest 计算：目标包全部文件豁免某禁令时，该行**不出现**（防 FP 疲劳，R2 附带发现） |
| 5 | **复用指引的确定性来源**：R1b 证明"看见既有实现"是最高价值信息 | 目标包导出符号清单（internal/symbols 索引，确定性排序，封顶 30 个）。注意：refs.TopReferenced 不存在于真实代码（那是 R1 任务 T08 让 agent 新写的函数），不得依赖 |
| 6 | **语言双栈**：Go/Java 阈值、禁令、包名归一化都不同 | `-lang` 参数 + 文件探测；Java 包名经 archFileOf 同款斜杠归一化；阈值走 EffectiveThresholds（policy 只能抬高内置地板，摘要必须报**生效值**而非 policy 原文） |
| 7 | **预算与截断次序**：超大仓库的符号清单可能爆预算 | 截断优先级：红线/禁 API/分层契约**永不截**；复用符号清单封顶；deps 白名单只列计数+根前缀；输出 used/est_tokens |
| 8 | **全程审计与溯源**：用户硬要求 | `-format json` 每行带 provenance（`.metricgate.yaml#banned_api.go[2]` / `builtin:metrics.cyclomatic` / `symbols:internal/engine`）；R3 harness 逐任务留档实际注入的 digest，报告可复算 |
| 9 | **不制造绕闸门手册**：摘要被注入提示词，可能被当作逃逸指南 | digest 只陈述约束与合法替代（"用 gitBytes() 不许用 os/exec"），不含"如何让检查器看不见"类信息；验收仍由 check 闸门独立判定，摘要无门禁权 |
| 10 | **只读与成本**：目标仓可能是只读试点项目 | digest 零写盘、零 store 交互、毫秒级；对 /tmp 只读沙箱安全 |
| 11 | **CLI/MCP 一致性**：双入口容易漂移（getReviewBundle 注释里的教训） | 本期只做 CLI `metricgate constraints digest`；MCP 工具化（复用同一 internal/digest 函数）列为 P1，函数签名现在就按双入口设计 |
| 12 | **文本形态为 LLM 优化**：受众是模型不是人 | 稳定节标题 + 每行一个"禁止 X / 必须 Y / 复用 Z"动宾结构，与验收规约 v1 的红线词汇对齐；不暴露 YAML 键名 |

## 2. digest 节结构（设计定稿）

```
【任务约束摘要 — 目标包 <pkg>（<lang>）】
■ 分层契约        本包属于 <layer> 层；允许依赖 <targets>；禁止 <forbid 模式>；（启用时）禁止循环依赖
■ 禁用 API/导入    禁止 <kind:match> — <reason>（逐条，豁免过滤后）
■ 依赖白名单       import 限 std + <self> + <allow 根前缀计数与列表>
■ 生效阈值        圈复杂度 ≤<w>/<e>、认知复杂度 ≤…、函数 ≤… 行、参数 ≤…、嵌套 ≤…（超出即拆辅助函数）
■ 复用既有实现     本包已有：<导出符号清单，封顶 30>——动手前先查，禁止重造
■ 流程红线        不留 TODO/panic 空壳；不留空块；（启用时）TODO 注释计警告
```

## 3. R3 前置接地实验预注册（2026-09-29，动手前锁定）

**设计**：配对 P 臂。同一 22 任务集、同一沙箱模板（/tmp/r2-sandbox 白名单复制 + git 基线）、同一模型（Cliproxyapi `z-ai/glm-5.3-flash`）、同一度量协议（`check . -config /tmp/r2-.metricgate.yaml -format json`，zz_eval 过滤，R1 分类器）。

- **Arm P**：生成提示 = 原任务文本 + 固定引导语 + **本任务目标包的 digest 文本**（harness 留档每份注入内容）。
- 对照基线：Arm A 31.8%（7/22，R1 与 R2 两次独立复现）。
- **P+B 复合实践**：P 臂仍违规的任务 → 接 R2 修复回路（findings+scorecard JSON 回灌，≤3 轮）——这是"生成前约束 + 生成后修复"的完整工程形态，也是最终要报告的数字。

**预注册假设与端点**（沿用 ab-experiment.md 的经济端点）：

- H1：P 臂首过违规率 < 31.8%，目标 <10%；残余违规集中在 digest 表达力之外的类别（如任务诱导的 stdout 打印用了 Fprintf 变体——前缀匹配天生看不见）。
- H2：digest 注入成本 ≤1K token/任务；经济端点 = 每千 token 违规避免数 = (0.318 − p_P) × 22 ÷ 每任务总 token（digest + 生成 + 修复），必须显著大于 0。
- H3（P+B 复合）：终端违规率 = 0（与 R2 一致），且**修复 token 总量 < R2 的 171.6 万**——因为一部分违规被预防、未发生的违规不需要修。

**推翻/调整/放弃判据**（预注册，防事后合理化）：

- 推翻：P 臂违规率 ≥ 基线（digest 无效或干扰）。
- 调整：违规率下降但经济端点 ≤0（预防成本 > 修复节省）→ 缩减 digest 体积重跑。
- 放弃：多次迭代后端点仍 ≤0 → 支点 1 记"无效"，投入保留在支点 4（修复回路）。

**已知风险与对照**：

- 污染：同一模型跑过 R1/R2，见过同批任务。缓解：与 R2 同款配对全新会话（两侧都无跨臂记忆）；残余差异归因时注明该风险。
- 提示 vs 事实的归因（硬约束 3）：P 臂测的是**注入**（prompt 层），其效果上限受"提示只能管意图"限制；P+B 复合才是完整实践，单独 P 臂数字不得用于贬低或夸大闸门价值。
- 框定混淆（硬约束 1）：22 任务集本身同时含 pinned/free 两种框定，沿用不改动。

## 4. 实现边界

- 新增 `internal/digest`（纯函数、可测）+ CLI 命令 `constraints digest`（注册进 commands 表）+ `internal/digest/digest_test.go`（含兼容矩阵降级用例）。
- 不改 check/闸门语义；不动 policy schema；MCP 工具化留 P1。
- 每步过 `go build ./...` + `go test ./internal/...`，新命令先 fixture 烟测（对已知违例验证会命中、会分级）再上真实任务——Java 试点教训（规约 v1 同款纪律）。

---
*撰写：2026-09-29。工程事实核实：getConstraints 现状输出文件事实包（cmd/metricgate-mcp/main.go:89）；BanConfig.Reason 字段（internal/config/config.go）；arch.Layer/Policy（internal/arch/check.go:23/:37）；DepsConfig（internal/config/config.go:124）；commands 注册表（cmd/metricgate/main.go:36）。*
