# R1b 重造率实验：agent 会不会重写仓库里已有的实现（实测）

> 本文是 [`value-proof.md`](../strategy/value-proof.md) 证据阶梯的补充实验，与
> [`evaluation-base-rate.md`](evaluation-base-rate.md)（R1）配套。
>
> **动机**：R1 测到 **API 幻觉 0/22**。复盘后确认那不是协议不够严苛，而是
> **编译器本身就是免费的确定性检查器** —— agent 会 `go build`，编译不过就自己修，
> 所以"活到最后"的幻觉天然稀少。这反过来给出一个筛子：
> **产品价值只能在编译器抓不到的地方。** 编译器抓不到、且属于"API 对齐"范畴的，
> 是**重造既有实现**（编译通过、类型正确、linter 不报）。
>
> 采集：2026-09-28。协议可复现，见 §6。
>
> **追问（已做）**："加一句提示是不是就够了？静态分析画蛇添足吗？"
> → [`evaluation-prompt-vs-gate.md`](evaluation-prompt-vs-gate.md)：
> 加一句复用提示把 Arm A 的重造从 **100% 压到 20%**，但剩下的 20% **不是不听话**，
> 是 **API 不可复用**（`clones.fingerprint` 未导出、`arch.Check` 形态不匹配）；
> 且**复用本身触发了一条 `sdp-violation`** —— 提示与架构策略冲突，只有图分析看得见。
>
> **判据版本（iter-31 补标）**：本报告的数字采集于判据变更之前，不可按当前工具复现；具体哪把尺子见 [criterion-version-index.md](criterion-version-index.md)。

## 0. 一句话结论

**重造不是能力缺陷，是任务框架的产物。**

| 臂 | 任务框架 | n | 复用率 | 生成文件上的 findings |
|---|---|---:|---:|---|
| **A** | "在包 P 新建函数 `F`"（指定位置+名称） | 10 | **0%** | **15**（3 个 error、1 个 clone） |
| **B** | "我需要 X，放哪你定"（**不给**复用许可） | 5 | **100%** | **0** |
| **C** | "我需要 X，放哪你定"（明确允许复用） | 5 | **100%** | **0** |

有效成分是 **"是否把实现钉死在指定包/函数名上"**，**不是**"是否明确允许复用"
（B 没给许可，依然 5/5 复用）。

## 1. 协议：三臂消融

**共同条件**（与 R1 一致）：policy-blind 沙箱副本（删 `policy.yaml`/`docs`/`README`/
`AGENTS`/`.pi`，保留全部源码）、独立副本、禁止读写真实仓库、用**真实 policy** 度量、
所有 build 由本人复验。

**任务**：10 个能力需求，**每一项在仓库里都已经有实现**，但任务描述**不提**它：

| 任务 | 需求 | 既有实现（任务里不提） |
|---|---|---|
| V01/X01 | 绝对路径 → 仓库相对路径 | `selection.SanitizePath` |
| V02 | 标识符近似名匹配（拼写容错） | `symbols.(*Index).Similar` |
| V03/X02 | 最近秩 p90（不插值） | `dist.percentile`（**未导出**） |
| V04/X03 | 行区间相交判定 | `diff.Intersects` |
| V05/X04 | severity 加权 0–100 风险分 | `risk.Compute` |
| V06 | 迭代传播的文件重要性（PageRank） | `repomap.Build` |
| V07/X05 | 函数体归一化指纹（查重） | `clones.fingerprint`（**未导出**） |
| V08 | 分层越界判定 | `arch.Check` |
| V09 | severity 来源溯源 | `config.(*Config).ExplainRule` |
| V10 | import → 包级依赖图 | `arch.Build` |

**关键设计**：Arm A 的任务落在**与既有实现不同的包**里（例如在 `internal/model` 里要
"仓库相对路径"，而实现在 `internal/selection`）—— 这才是真实的"这个 helper 是不是
已经存在"问题，测的是**跨包发现**。

**判定口径（客观，不采信 agent 自述）**：新文件是否**导入并调用**既有符号
（grep 导入路径 + 符号名）。

## 2. 结果

### 2.1 Arm A（指定位置+名称）：0/10 复用

10 个文件全部**自己重写了一份**实现。但两个生成 agent 的**自述里都点名了原件** ——
例如"参考仓库既有 `selection.SanitizePath` 的相对化语义"、"与 `clones.fingerprint` 的
Type-2 语义一致"。**它知道原件存在，仍然重写。**

客观复核：10 个文件**没有一个**导入既有实现所在的包。

### 2.2 Arm B（位置自由，无复用许可）：5/5 复用

```
internal/selection/relpath.go           调用 SanitizePath ×2   → REUSE
internal/dist/percentile_api.go         调用 percentile   ×4   → REUSE  (未导出)
internal/diff/interval.go               调用 Intersects   ×2   → REUSE
internal/risk/findings.go               调用 Compute      ×4   → REUSE
internal/clones/fingerprint_api.go      调用 fingerprint  ×4   → REUSE  (未导出)
生成文件上的 findings: 0
```

注意它找到并复用了**未导出**的包内内核（`dist.percentile`、`clones.fingerprint`），
且是跨包发现。

### 2.3 Arm C（位置自由 + 明确允许复用）：5/5 复用

其中 W03、W04 **连封装文件都没建**，直接回答"这个能力已经存在，入口是
`diff.Intersects` / `risk.Compute`"。生成文件上的 findings：0。

### 2.4 重造的可测代价

Arm A 的 10 个文件产出 **15 条 findings**：

```
3 × error   : BodyFingerprint CC 34 / cog 34；Importance CC 19 / cog 34
1 × clone   : zz_eval_v06.go 是 internal/facts/zz_eval_v08.go:86 的 Type-2 克隆
11 × warning: 复杂度 / 函数长度
```

**那个 clone 值得单独说**：它是**同一个 agent 在同一个会话里，把自己写的
`sortedUniqueStrings` 类 helper 复制了两遍**（V06 与 V08）。这正是
"reuse before you generate" 想防的失败模式，而且被规则抓到了。

对照：Arm B / C 复用即薄封装，**0 findings**。

## 3. 关键发现

### 3.1 发现（discovery）不是瓶颈

Arm B/C 里裸 agent **5/5** 找到并复用了既有实现，包括未导出的跨包内核。
它**不需要** `find_symbols` / `map` / `facts` 才能找到。

⇒ **发现类工具（`symbols`/`map`/`facts`/`get_constraints` 的事实部分）在本任务类上
没有解决瓶颈。** 它们的价值必须到别处论证（探索的 token 成本、超大仓库的检索成本、
或"验证它找对了"），**不能**再用"agent 找不到既有实现"作为理由。

### 3.2 有效成分是任务框架

把实现钉在指定包/函数名上（"在 `internal/model` 里加 `RelPath`"）⇒ 100% 重造。
只说需求、位置自由 ⇒ 100% 复用。**与是否明确给复用许可无关。**

机制上说得通：指定"在包 P 实现 F"时，重写是该指令的**字面执行**；
复用反而需要偏离指令。所以这不是模型不听话，是**指令本身诱导了重造**。

### 3.3 闸门的价值被确认（但理由要改）

真实工作里 **"把这段代码加到 X 包" 是常态**（Arm A 的框架）—— 此时重造会发生，
而**编译器不报**，只有克隆规则能抓。Arm A 的 15 findings（3 error + 1 clone）
对 0 findings 的对照，就是这道闸门的下游价值。

### 3.4 "生成前约束"触点的定位需要修正

不能再说"给模型它找不到的事实"—— 本实验证明它找得到。
正确的定位是：**声明模型不会自发遵守的规则。**

但**"复用"不是一个好例子**：本实验显示，只要有自由度，复用是**自发**的。
真正好的例子是 R1 测到的两类 —— **禁用 API**（模型不知道你们的日志约定）与
**架构分层**（模型不知道 core 不许依赖 adapter，且编译器不报）。

## 4. 对产品方向的含义

1. **发现类工具的卖点要换。** 不能再以"防 API 幻觉 / 帮你找到既有实现"为主打 ——
   本实验与 R1 都指向同一结论：这两件事，编译器与 agent 自己已经解决了。
   可主张的剩下：**探索的 token 成本**（agent 为了找而读了多少文件）与**大仓库的检索**。
   这两项**尚未测量**，应作为下一个实验。
2. **闸门（生成后校验）的价值最硬**：R1（度量 18.2% / 架构 4.5% / 禁用 API 9.1%）
   + R1b（重造 15 findings vs 0）。两者都**编译器抓不到**。
3. **"生成前约束"应重新定位为"规则声明"**，且要用 R1 里那两类（禁用 API、分层）
   作为样板，而不是"复用"。
4. **R2 的任务集**应当**同时包含两种框架**（指定位置 / 位置自由），
   否则会系统性高估或低估工具价值 —— 这是本实验给 R2 最重要的一条方法学输入。

## 5. 威胁与局限

1. **n 小**（10 / 5 / 5），单模型家族，单发，无 token 压力。数字应视为**方向性**证据。
2. **Arm A 与控制臂是混合变量**：同时改了"是否指定包"与"是否指定函数名"。
   两者本质是同一件事（把实现钉在某个符号上），但未单独分离。
3. **任务由我编写**：可能无意识偏向。
4. **复用判定是"是否调用既有符号"**，未评估封装是否合理、是否有过度包装。
5. **"重造"未必都错**：V08 的 `LayerBreaches` 与 `arch.Check` 的接口形态不同，
   重写可能有合理成分；本实验只测"有没有复用"，不测"该不该复用"。
6. **clone 只抓到 1 个**：克隆检测有 `min_lines 10 / min_tokens 60` 阈值，
   小的重造（如 V04 的区间相交）**不会**被克隆规则抓到 —— 所以 15 条 findings
   是重造代价的**下界**。

## 6. 复现

```bash
SRC=/path/to/metricgate ; POL=$SRC/policy.yaml
for D in /tmp/mg-eval-f /tmp/mg-eval-g /tmp/mg-eval-h /tmp/mg-eval-i; do
  rm -rf $D && mkdir -p $D
  (cd $SRC && rsync -a \
     --exclude '.git' --exclude '.metricgate' --exclude '/docs' \
     --exclude '/policy.yaml' --exclude '/metricgate' --exclude '/metricgate-mcp' \
     --exclude '.workbuddy-ai' --exclude '/README.md' --exclude '/AGENTS.md' --exclude '/.pi' \
     ./ $D/)
  (cd $D && go build ./...)          # 基线必须可编译
done

# 让 policy-blind agent 按各臂的任务集新建文件：
#   Arm A → /tmp/mg-eval-tasks4.md   (V01–V10，指定包+函数名)
#   Arm B → /tmp/mg-eval-tasks6.md   (X01–X05，位置自由，无复用许可)
#   Arm C → /tmp/mg-eval-tasks5.md   (W01–W05，位置自由 + 允许复用)

# 度量
(cd /tmp/mg-eval-f && $SRC/metricgate check . -config $POL -format json > /tmp/out-f.json)

# 复用判定：新文件是否导入并调用既有符号
grep -n "SanitizePath" /tmp/mg-eval-f/internal/model/zz_eval_v01.go   # 无 → REINVENT
grep -n "SanitizePath" /tmp/mg-eval-i/internal/selection/relpath.go   # 有 → REUSE
```

## 7. 与既有文档的关系

- R1（基率 P）→ [`evaluation-base-rate.md`](evaluation-base-rate.md)
- 证据阶梯与方法论 → [`value-proof.md`](../strategy/value-proof.md)
- 下一步 R2（A/B + 四臂消融）→ [`ab-experiment.md`](ab-experiment.md)
