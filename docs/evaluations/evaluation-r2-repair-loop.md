# R2 修复回路实验：把 findings+scorecard JSON 喂回给 agent，违规能不能被修掉、代价多大（实测）

> 本文是 [`value-proof.md`](../strategy/value-proof.md) 证据阶梯 **R2 级**的实测报告，
> 也是 [`llm-constraint-paradigm.md`](../research/llm-constraint-paradigm.md) **支点 4（结构化修复回路）**的量化验证。
> R1 证明了"闸门会真实拦下违规"（基率 31.8%）；R2 回答剩下的半环：
> **被拦下之后，机器可读的 findings JSON 是否足以驱动 agent 自修到验收达标？修一轮的代价是什么？**
>
> 数据采集：2026-09-29，单机，配对设计。协议可复现，命令见 §8。
>
> **判据版本（iter-31 补标）**：本报告的数字采集于判据变更之前，不可按当前工具复现；具体哪把尺子见 [criterion-version-index.md](criterion-version-index.md)。

## 0. 一句话结论

**闭环成立**：Arm A 复现违规率 7/22（31.8%，与 R1 完全一致）；
Arm B 对这 7 个任务的终态只喂 findings+scorecard JSON（不给原始任务描述），
**7/7 在 ≤3 轮内达到验收规约 v1 的达标判据**（gate PASS + 五项红线字段全 0 + thresholdBreaches=0），
其中 6 个任务 **1 轮**修复成功，仅架构类任务 U04 用了 2 轮。
修复总代价 ≈171.6 万 token（约为这 7 个任务生成成本的 1.72 倍）。
防糊弄审计：修复前 23 个函数 **0 丢失**，且 7 个任务 LOC 全部增长——修复方式是"拆函数降复杂度/引端口"而非删代码。

## 1. 协议：配对设计

同一个模型（Cliproxyapi `z-ai/glm-5.3-flash`，与 R1 相同）、同一批 22 个任务、同一个 policy-blind 沙箱（白名单复制 go.mod/go.sum/cmd/internal + git 基线）：

```
Arm A（重跑基线）     Arm B（配对修复，从 A 臂终态出发）
  22 任务裸生成   →    只对违规的 7 个任务：
  离线全量度量          check -diff -base HEAD -regress -scorecard -format json
  → 违规 7/22          findings JSON（剔 metrics）+ scorecard JSON 回灌【全新】会话
  (31.8% = R1 复现)    修复 ≤3 轮，终止 = 规约 v1 达标判据
```

三个关键设计决定：

1. **修复会话不带原始任务描述**——prompt 里只有 findings JSON + scorecard JSON + 5 条约束（只许改 zz_eval_* 文件 / 六字段归零 / 严禁删功能空壳化 / 可读仓库代码 / 完成即止）。这是刻意测"纯 JSON 通道的充分性"：如果连任务上下文都没有也能修对，通道本身就是合格的修复指令格式（范式文档 §2 支点 4 的核心主张）。
2. **每轮全新会话**——不带上一轮记忆，修复所需信息必须全部来自 JSON。
3. **终止判据即规约 v1 达标判据**：gate exit=0，且 scorecard 的 structureViolations / dependencyViolations / bannedApiCalls / metricRegressions / parseErrors = 0，且 thresholdBreaches = 0。

## 2. Arm A：基率复验

7/22 = **31.8%**，与 R1 逐类吻合；仅 T04↔T11 一处互换（R1 中 T04 违规、T11 干净，本次相反），U04/U06/U07/U08/U09/U10 六个任务精确复现。这说明 R1 的基率不是单次抽样噪声，而是该模型在该任务集上的稳定行为。

| 任务 | 违规类别 | findings | severity | 生成 token |
|---|---|---:|---|---:|
| T11 | C 度量 ×8（CC/cognitive/nesting） | 8 | 3 error | 187,585 |
| U04 | B 架构 ×2（layer-violation） | 2 | warning | 101,737 |
| U06 | C 度量 ×3 | 3 | warning | 301,125 |
| U07 | C 度量 ×1（CC） | 1 | warning | 17,350 |
| U08 | C 度量 ×3（cognitive/CC/nesting） | 3 | 1 error | 39,644 |
| U09 | A 禁 API ×1（banned-os-exec） | 1 | warning | 209,388 |
| U10 | C 度量 ×3 | 3 | 1 error | 140,715 |
| 合计 | C 18 / B 2 / A 1 | 21 | 5e+16w | 997,544 |

22 任务生成总 token 2,377,996。

## 3. Arm B：修复回路结果

**7/7 全部达标**。轮数分布：6×1 轮 + U09×2 轮。

| 任务 | 轮数 | 修复 token | 修复 calls | 终态 changedFiles | 功能丢失 |
|---|---:|---:|---:|---:|---|
| T11 | 1 | 298,032 | 15 | 1 | 0/12 函数 |
| U04 | 1 | 670,824 | 32 | 2 | 0/1 |
| U06 | 1 | 193,339 | 15 | 1 | 0/3 |
| U07 | 1 | 17,881 | 5 | 1 | 0/1 |
| U08 | 1 | 76,438 | 13 | 1 | 0/1 |
| U09 | 2 | 434,080 | 30 | 1 | 0/2 |
| U10 | 1 | 25,373 | 5 | 1 | 0/3 |
| 合计 | 1.14 均值 | **1,715,967** | 115 | — | **0/23** |

独立复核（对 7 个终态重跑完整协议，防 loop 记账 bug）：全部 build 通过、gate PASS、zz findings=0、红线+threshold 全 0——且 incompleteWork=0、clonedCode=0（这两项不在终止判据里，但也归了零，说明修复没有引入 TODO 类半成品或复制粘贴）。

## 4. 成本核算

- 修复 token / 生成 token（7 个违规任务）= 1,715,967 / 997,544 ≈ **1.72×**。
- 逐任务差异极大：U07 修复只花 17.9K（1.03×），U10 只花 25.4K（0.18×）；而 U04 花了 670.8K（**6.59×**）。
- 规律：**度量类违规（C 类）修复便宜**（拆函数、提前 return、收窄作用域，机械重构）；**架构类违规（B 类）修复昂贵**（U04 需要引入接口端口 + 组合根注入的结构性改造，见 §6）。这给范式文档支点 1（前置接地）提供了一个成本侧论据：架构违规预防的成本远低于事后修复。

## 5. 防糊弄审计

修复回路最大的风险是 agent 用"删功能/空壳化"骗过闸门。三层核查：

1. **函数级保留**：从 Arm A 会话 JSONL 重建 7 个任务修复前的原始产物，逐一比对修复后终态——**23 个函数 0 丢失**，全部任务 LOC 增长（T11 298→354、U04 38→92、U06 124→167、U07 58→70、U08 62→88、U09 65→72、U10 100→137），新增函数全是降低复杂度的拆分辅助函数。
2. **stub/TODO 逃逸手法**：终态 incompleteWork=0；粗扫发现的 5 个 TODO/FIXME 标记经逐一精查全部位于注释/字符串字面量内——是 T11（ClassifyStub 桩分类器）任务本身的判据文本（`"{ /* TODO */ }"`、`"unimplemented"` 等检测对象），非真实糊弄。
3. **协议合规**：终态 git status 确认 0 个既有 tracked 文件被改动，修复只动了 zz_eval_* 白名单内的文件。

## 6. 修复质量样本（两个高含金量案例）

**U04（B 架构，最贵也最能说明问题）**：违规是 core 包（internal/impact）直接 import 文件遍历相关的下层实现。修复没有用"把 import 挪进函数里"之类的把戏，而是引入了**端口-适配器**：core 定义 `TreeIndexer` 接口 + `SetTreeIndexer()` 组合根注入，文件遍历留在 adapter 侧文件，注释明确写出"composition root wires the port at startup"。这是教科书级的依赖倒置修复——闸门的 findings JSON 没说该怎么做，模型自己从分层约束推出了正确方向。

**U09（A 禁 API，唯一 2 轮任务）**：第 1 轮修复被 go build 卡住（import os/exec 移除后的连锁编译错误），第 2 轮完成：`LogNameOnly` 改调本包既有的 `gitBytes(dir, args...)` 辅助（os/exec 在 changes.go 是合法驻地），保留了命令参数与错误包装 `git log: %w (is this a git repository?)`，`ChurnSince` 全功能承接。修复复用了仓库既有抽象——与 R1b 的"重造"发现形成呼应：闸门反馈也把模型推向了复用。

## 7. 产品发现（喂给支点 4 的工程改进）

1. **`-diff` 模式的 findings JSON 未过滤 metrics**：全量跑出 6,003 条 metrics 使 JSON 达 1.36MB——曾直接把 argv 传参撑爆（`Argument list too long`），且对 agent 是纯噪音（scorecard 已含 breach 汇总）。**建议 `-diff` JSON 默认剔除或裁剪 metrics 数组**（本实验用应用侧 slim 缓解）。
2. **scorecard JSON 混在 stderr 文本里**：需要从首个裸 `{` 起做括号配平截取。建议 `check -scorecard -format json` 时 stderr 的 scorecard 单独成行输出或提供 `-scorecard-out FILE`。
3. **regress 对全新文件零误报**：metricRegressions=0（无基线不回归），说明 `-regress` 可以安全地常开在修复回路协议里。

## 8. 复现命令

```bash
# Arm A（每任务独立沙箱 /tmp/r2-arm-a/run/$TASK，白名单复制 + git init + commit）
/tmp/r2-arm-a/run_task.sh T01 ... U10        # pi 裸生成 → go build → 离线度量 → token 记账

# Arm B（配对修复）
python3 /tmp/r2-arm-b/repair.py              # 协议采集 → findings+scorecard JSON 回灌 → ≤3 轮

# 度量协议（任一沙箱内）
metricgate check -diff -base HEAD -regress -scorecard -config /tmp/r2-policy.yaml -format json
```

会话 JSONL 含逐条 `usage.totalTokens`，token 记账可独立重算（本次两处独立口径一致）。

## 9. 诚实边界

- **单模型**：全部结论基于 glm-5.3-flash 一族；修复成功率 100% 不代表所有模型都能一轮达标。
- **样本小**：违规配对只有 7 对；类别分布（C 类 5 对、B 类 1 对、A 类 1 对）不均衡，架构类结论（贵、需结构性改造）目前 n=1。
- **通道充分性 ≠ 真实场景**：修复会话刻意不给任务上下文，真实场景里 agent 还有任务描述与记忆，效果只会更好；但"纯 JSON 可修"这一下界是本实验建立的。
- **达标判据只含静态可检字段**：功能等价性只能靠函数保留 + LOC 增长 + stub 扫描近似，语义等价（公式对不对）静态不可检——与范式文档 §6 的诚实边界一致。
- **C 类违规的"修复"是让指标过线**：拆函数确实改善了可维护性，但阈值的业务含义（哪个粒度算"太复杂"）本身是 policy 调优问题，由支点 5（度量演进）回答。

## 10. 证据阶梯定位

| 实验 | 回答的问题 | 结论 |
|---|---|---|
| R1 基率 | 闸门拦得住吗？ | 违规率 31.8%，error 级 9.1% |
| R1b 重造 | 接地决定什么？ | 任务框架决定 0% vs 100% 复用 |
| 提示 vs 闸门 | 提示能替代闸门吗？ | 不能——提示管意图，闸门管事实 |
| **R2 修复回路** | **拦下之后闭环转得动吗？** | **7/7 修复达标，1.72× token，0 功能丢失** |

至此范式文档五支点中"验收（支点 3）→ 修复（支点 4）"的量化证据链完整：
**findings JSON 就是修复指令**——不需要新的引擎能力，闸门产出即闭环输入。
