# 一句提示 vs 静态分析：谁在防重复？（实测）

> 承接 [`evaluation-reuse.md`](evaluation-reuse.md)（R1b）。
> 用户提出的可证伪命题：**"测重复代码，只要加一句提示就行了？用静态分析反而画蛇添足？"**
>
> 采集：2026-09-28。协议可复现，见 §6。

## 0. 一句话结论

**提示承担了大部分"防重复"的工作（100% 重造 → 20%），但静态分析不是画蛇添足 ——
它做到了提示在结构上做不到的三件事，而这三件事本次全部实际发生了。**

| 臂 | 条件 | n | 复用率 | 生成文件 findings |
|---|---|---:|---:|---|
| A | 指定包+函数名，**无**提示 | 10 | **0%** | **15**（3 error + 1 clone） |
| **D** | 指定包+函数名，**加一句**复用提示 | 10 | **80%** | **4**（2 error + 2 warning） |

那句提示是：

> 注意：在动手实现任何一项之前，先确认仓库里是否已经有实现同样功能的函数或方法；
> 如果有，请直接复用（可以加一层薄封装），不要重新实现一份。

**它确实有效**：重造从 10/10 降到 2/10，findings 从 15 降到 4。

**但剩下的 2 个失败不是"模型不听话"** —— 见 §3。

## 1. 协议

沿用 R1b 的 Arm A 任务集（V01–V10，**指定目标包与函数名**，10 项功能在仓库里都已有实现），
唯一差别是 agent 提示词里加了上面那一句。判定仍为客观 grep：新文件是否**导入并调用**既有符号。

## 2. 结果：8/10 复用

| 任务 | 既有实现 | Arm A | Arm D |
|---|---|---|---|
| V01 路径规范化 | `selection.SanitizePath` | 重造 | **复用** |
| V02 近似名匹配 | `symbols.Index.Similar` | 重造 | **复用** |
| V03 最近秩 p90 | `dist.Summarize` | 重造 | **复用** |
| V04 区间相交 | `diff.Intersects` | 重造 | **复用** |
| V05 风险分 | `risk.DefaultOptions` | 重造 | **复用**（部分：权重复用，公式自写） |
| V06 PageRank | `repomap.Build` | 重造 | **复用** |
| **V07 函数体指纹** | `clones.fingerprint`（**未导出**） | 重造 | **重造（被迫）** |
| **V08 分层越界** | `arch.Check`（**形态不匹配**） | 重造 | **重造（被迫）** |
| V09 severity 溯源 | `config.ExplainRule`（需 `*Config`） | 重造 | 复用注册表 + 新判定 |
| V10 import→包图 | `arch.Build` | 重造 | **复用** |

生成文件上的 findings：**Arm A 15 → Arm D 4**（3 error + 1 clone → 2 error）。

## 3. 关键：剩下 2 个失败**不都是**结构性的

**（本节结论已被 [`evaluation-hint-and-cost.md`](evaluation-hint-and-cost.md) §2 修正：
V08 不是"不可复用"，只是适配器成本 —— Arm E 用约 60 行适配调到了 `arch.Check`。原文保留如下，
以免掩盖当时的推理。）**

两处重造中，一处由**既有 API 不可复用**造成，另一处 agent 主动说明了原因：

- **V07**：`clones.fingerprint` 在 `internal/clones/clones.go:146` 是**小写未导出**的
  （`func fingerprint(src []byte, body *sitter.Node) (uint64, int)`），
  从 `internal/metrics` 根本调不到。agent 只能自己写词法扫描器。
  **代价：65 行、CC 20 —— 2 个 error 级违规。这是真·结构性阻塞。**
- **V08**：`arch.Check` 要求 `arch.Graph` / `arch.Policy` 类型，采用"单层归属 + 包模式匹配"，
  与任务给的「包→层（可多层）」+「层→允许层」映射形态不匹配。
  **当时判为"形态不匹配"，但 Arm E 证伪了：写约 60 行适配即可复用。所以这是适配成本，不是阻塞。**

⇒ **这是关于"你自己 API 设计"的信号**：指纹内核该被导出（真阻塞），
`arch.Check` 该有一个轻量入口（只是易用性）。
提示给不出这个信号，只有检查会把"你不得不重写"这件事摆到台面上 —— 而且它是以
**2 个 error 级违规**的形式付费的。

## 4. 最意外的一点：**复用本身触发了一条违规**

Arm D 唯一的那条 warning：

```
[warning] sdp-violation @ internal/model/zz_eval_v01.go:3
  package "internal/model" (I=0.07) depends on less stable "internal/selection" (I=0.12);
  stable-dependencies principle
```

核实：真实仓库的 `internal/model` 只 import 标准库（`cmp`/`encoding/hex`/`sort`/`strings`/`time`），
**不依赖 `selection`**。这条 `model → selection` 边是 Arm D 照提示复用后**新引入**的。

⇒ **提示说"复用"，架构策略说"稳定的不许依赖不稳定的"，两者直接冲突。**
agent 完全照做了提示，却被另一条规则判违规。

**任何提示都看不见这个后果** —— 它不是关于"这段代码"的局部属性，而是
"这次改动让整张依赖图变成了什么样"的**全局属性**。只有图分析能回答。

（顺带：这也暴露了本仓库策略的一个内部张力 —— "鼓励复用"与"稳定依赖原则"
在 `internal/model` 这类高稳定包上会打架。）

**已处理。** 复查发现真实成因不是 SDP 太严，而是**本仓库的层声明漏了 6 个包**：
把 `apisurface` 补进 `core` 后，同一条边立刻变成精确的
`layer-violation: layer "core" (internal/apisurface) may not depend on layer "adapter" (internal/facts)`。
调和方案（消息给出正确修法 + `sdp_tolerance` + `sdp: off` + 严重度低于 `fail_on`）
与逐项验证见 [`evaluation-hint-and-cost.md`](evaluation-hint-and-cost.md) §4。

## 5. 所以：提示与静态分析是**互补**，不是替代

| 层 | 机制 | 回答的问题 | 提示能否替代 |
|---|---|---|---|
| **意图层** | 提示 / `get_constraints` | 该做什么 | — |
| **事实层** | 检查 / 闸门 | **实际发生了什么** | **否** |

提示做不到、而本次全部实际发生的三件事：

1. **验证**。提示是**请求**，不是事实。Arm A 的 agent 在报告里点名了原件却仍然重写；
   Arm D 的 8/10 是我 grep 出来的，**不是 agent 说的**。没有检查，你无法区分
   "照做了"与"声称照做了"。
2. **暴露结构性不可复用**（§3）。它把"你的内核没导出"变成一条可执行的结论。
3. **抓二阶后果**（§4）。复用产生的依赖边、SDP 冲突 —— 全局属性，提示在定义上看不见。

**结论**：对"防重复"这个单一目标，提示确实承担了约 80% 的工作，静态分析的边际价值
从"阻止重复"转移到了**验证 + 暴露 API 可复用性缺口 + 抓全局后果**。
但它是**不可替代**的，不是画蛇添足 —— 因为这三件事恰好都是本次实测中真实发生的。

## 6. 威胁与局限

1. **n=10，单模型家族，单发**。80% 是点估计，置信区间宽。
2. **提示词是我写的**，措辞（"先确认…如果有请直接复用"）可能比真实用户提示更明确。
   **已补测**：更含糊的"别重复造轮子"总分相同（7/10 严格），但失败的题目不同 ——
   见 [`evaluation-hint-and-cost.md`](evaluation-hint-and-cost.md) §2。
3. **复用判定是"是否导入并调用既有符号"**，不评估封装是否合理。V05 的"部分复用"
   （只复用权重常量）被计为复用。
4. **V09 计为复用**，但它实际是"复用注册表 + 新写判定逻辑"，属混合形态。
5. **未测**：提示在**多轮/长会话**下是否仍有效；agent 是否会"声称复用"而不复用
   （本次 Arm D 未出现，Arm A 出现了"点名但不用"）。

## 7. 复现

```bash
SRC=/path/to/metricgate ; POL=$SRC/policy.yaml
for D in /tmp/mg-eval-j /tmp/mg-eval-k; do
  rm -rf $D && mkdir -p $D
  (cd $SRC && rsync -a \
     --exclude '.git' --exclude '.metricgate' --exclude '/docs' \
     --exclude '/policy.yaml' --exclude '/metricgate' --exclude '/metricgate-mcp' \
     --exclude '.workbuddy-ai' --exclude '/README.md' --exclude '/AGENTS.md' --exclude '/.pi' \
     ./ $D/)
  (cd $D && go build ./...)
done
# 任务集同 R1b Arm A：/tmp/mg-eval-tasks4.md（V01–V10，指定包+函数名）
# agent 提示词中额外加一句：
#   「注意：在动手实现任何一项之前，先确认仓库里是否已经有实现同样功能的函数或方法；
#     如果有，请直接复用（可以加一层薄封装），不要重新实现一份。」

(cd /tmp/mg-eval-j && $SRC/metricgate check . -config $POL -format json > /tmp/out-j.json)

# 客观复用判定
grep -n "SanitizePath" /tmp/mg-eval-j/internal/model/zz_eval_v01.go   # 有 → REUSE
grep -n "fingerprint"  /tmp/mg-eval-k/internal/metrics/zz_eval_v07.go # 只在注释里 → NEW
```

## 8. 与既有文档的关系

- R1（基率 P）→ [`evaluation-base-rate.md`](evaluation-base-rate.md)
- R1b（重造率，三臂消融）→ [`evaluation-reuse.md`](evaluation-reuse.md)
- 后续三点（含糊提示 / 探索成本 / SDP 调和）→ [`evaluation-hint-and-cost.md`](evaluation-hint-and-cost.md)
- 方法论与证据阶梯 → [`value-proof.md`](../strategy/value-proof.md)
