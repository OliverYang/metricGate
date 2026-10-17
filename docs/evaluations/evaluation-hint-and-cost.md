# 提示强度、探索成本与 SDP 冲突：三个后续实验

> 承接 [`evaluation-prompt-vs-gate.md`](evaluation-prompt-vs-gate.md)（Arm D）。
> 三个待验证的问题：
> ① 更含糊的提示（"别重复造轮子"）还有 ~80% 效果吗？
> ② 发现类工具的"探索成本"优势到底有多大？
> ③ `sdp-violation` 与"复用"指令的冲突，该怎么在策略里调和？
>
> 采集：2026-09-28。协议与原始数据可复现，见 §5。
>
> **判据版本（iter-31 补标）**：本报告的数字采集于判据变更之前，不可按当前工具复现；具体哪把尺子见 [criterion-version-index.md](criterion-version-index.md)。

## 0. 一句话结论

| 点 | 结论 |
|---|---|
| ① | **含糊提示的总分与明确提示相同（都 7/10 严格判定），但失败的题目不一样** —— 其中一次失败是"在文档注释里点名了 `internal/repomap`，然后自己重写了 PageRank"。提示率不是提示的稳定属性。 |
| ② | **"探索成本"这个价值点站不住**。发现一份实现只要 ~4 KB（`map` 3932 B + 目标文件），但 R1b 已证明**不用任何工具也能 5/5 复用** —— 瓶颈从来不是"找不到"。真正省下的是**验证成本**，不是发现成本。 |
| ③ | **冲突的真实成因不是 SDP 太严，而是本仓库的层声明有洞**：6 个真实包不在任何层里。把 `apisurface` 补进 `core` 后，那条含糊的 SDP 警告立刻变成精确的 `layer-violation`。 |

## 1. 协议与判定口径

- **任务集**：R1b Arm A 的 V01–V10（指定目标包 + 函数名，10 项功能仓库里都已有实现）。
- **复用判定**：客观 grep —— 生成文件是否**同时**（a）导入既有实现所在包、（b）出现既有符号名。
  这比 Arm D 报告里的口径**更严**：只复用了权重常量而没调 `risk.Compute` 的 V05 判为重造。
  报告里同时给出严格值与文档原口径（把 V05 算部分复用）以便对齐。
- **findings 判定**：`metricgate check . -config <repo policy>`，只统计生成文件（`zz_eval_*`）上的 findings。
- **臂的定义**：

| 臂 | 沙箱 | 提示词 |
|---|---|---|
| A | `/tmp/mg-eval-f`、`-g` | 无复用提示 |
| D | `/tmp/mg-eval-j`、`-k` | "先确认仓库里是否已经有实现…如果有，请直接复用（可以加一层薄封装），不要重新实现一份" |
| **E** | `/tmp/mg-eval-l`、`-m` | **"注意不要重复造轮子。"**（仅此一句） |

## 2. 点①：含糊提示的分数一样，但**失败模式不同**

| 臂 | 提示 | 严格复用 | 原口径 | 生成文件 findings |
|---|---|---:|---:|---:|
| A | 无 | 0/10 | 0/10 | 15 |
| D | 明确（点名"先确认…请直接复用"） | **7/10** | **8/10** | **4** |
| **E** | **"不要重复造轮子"** | **7/10** | **8/10** | **5** |

总分对齐：**含糊提示没有变差**。所以"提示措辞的精细度"不是那个有效成分
（R1b 已经指出有效成分是**任务框定**：说"在包 P 新建函数 F"就必然重造，说"我需要 X，放哪你定"就必然复用）。

但**逐题对照**显示两臂的失败不是同一批：

| 任务 | 既有实现 | Arm D | Arm E | 性质 |
|---|---|---|---|---|
| V05 | `risk` 权重 | 部分（只用常量） | 部分（只用常量） | 任务框定模糊 |
| **V06** | `repomap.Build`（**已导出**） | 复用 | **重造** | **点名但不用** |
| **V07** | `clones.fingerprint`（**未导出**） | 重造 | 重造 | **真·结构性** |
| **V08** | `arch.Check` | **重造**（137 行） | **复用**（88 行） | 只是适配器成本 |

两处值得单独说：

**V06 —— 提示做不到的那件事的完美样本。** Arm E 生成的 93 行里，文件头注释写着：

> The propagation **mirrors the fixed-iteration PageRank used by the repo-map ranking
> (`internal/repomap`)**: 30 iterations at damping 0.85…

它**准确描述了既有实现**，然后自己又写了一遍，还拆成 3 个函数。
这与 Arm A 的"报告里点名了原件却仍然重写"是同一种失效：**提示是请求，不是事实**。
`repomap.Build` 是导出的、可直接调用的 —— 没有任何结构性理由重造。

**V08 —— 需要修正 Arm D 报告里的一个过强结论。**
`evaluation-prompt-vs-gate.md` §3 把 V08 列为"既有 API 不可复用（形态不匹配）"。
Arm E 证伪了这一点：它写了约 60 行适配（把 `[][2]string` 边表转成 `arch.Graph` + `arch.Policy`），
调用了 `arch.Check`，88 行完成同一件事。所以 V08 是**适配器成本**，不是不可复用。

⇒ 修正后的残余失败构成：**1 项真结构性（V07 未导出）+ 1 项适配成本（V08）+ 1 项任务框定模糊（V05）
+ 1 项点名不用（V06）**。四类里只有一类是"工具/API 的锅"。

## 3. 点②：探索成本 —— 这个价值点**站不住**

先给数字。仓库规模（排除 `testdata/`）：

| 口径 | 文件 | 字节 |
|---|---:|---:|
| 生产代码（`_test.go` 除外） | 84 | 437 778（428 KB） |
| 含测试 | 137 | 648 031（633 KB） |

发现路径的输出体积：

| 命令 | 输出字节 | 回答的问题 |
|---|---:|---|
| `metricgate map .` | 3 932 | 该读哪些文件（PageRank 排序） |
| `metricgate context internal/selection` | 3 054 | 这个包有什么、约束是什么 |
| `metricgate symbols . -q SanitizePath` | 91 | 这个名字在哪 |
| `metricgate symbols . -q fingerprint` | 115 | 同上（未导出符号也能查到） |

而"读进脑子里"的成本（agent 必须打开的文件）：

| 文件 | 字节 |
|---|---:|
| `internal/diff/diff.go` | 908 |
| `internal/selection/selection.go` | 4 947 |
| `internal/risk/risk.go` | 6 073 |
| `internal/symbols/index.go` | 6 256 |
| `internal/arch/check.go` | 7 189 |
| `internal/clones/clones.go` | 7 766 |
| `internal/repomap/repomap.go` | 10 651 |

所以对比是：**盲读全仓 428 KB vs 用工具定位到 1–2 个文件 ~5–12 KB，约 1–3%。**

**但这个对比是无效的**，因为 R1b 已经测出：Arm B（位置自由、无提示、**无任何工具**）5/5 复用，
Arm C 同样 5/5。也就是说 —— **agent 本来就不需要"发现类工具"来找到既有实现。**
它自己 grep、自己读包，成本本来就不高；真正的成本不在"找"，而在别处。

**那"别处"是什么？** 把 Arm D/E 的失败逐个拆开看就很清楚：

| 失效 | 一个 `grep`/`check` 能否抓到 | 需要读多少代码才能发现 |
|---|---|---|
| V06 点名不用 | ✅ 一条 grep（`repomap.Build` 是否出现） | 0 |
| V07 未导出只能重写 | ✅ `check` 报 2 个 error | 0（是被动发现） |
| V08 适配成本 | ⚠️ 需要人判断"这算不算复用" | ~7 KB（`arch/check.go`） |
| 复用引入的 SDP 边 | ✅ **只有图分析能报** | 全图（单文件读不出来） |

⇒ 工具真正省下的是**验证成本**（把"我复用了"从自述变成事实，成本≈1 条 grep），
以及**二阶后果的可见性**（复用改变了整张依赖图，任何单文件视角都看不见）。
"零 token 帮你省下探索"这个卖点，本次数据**不支持**。

**附带观察**：`bundle` 在预算不足时**拒绝输出**而不是静默截断 ——
本仓库当前工作区 diff 超过 6000 单位，直接报
`budget_exhausted: required constraints and diffs need more than 6000 units` 并 `rc=2`。
用 `-commit HEAD -max-chars 200000` 可得 18 873 字节的包（其中 `constraints` 1 299 字节）。
对"交给 LLM 审查"这条链路，**显式失败**比"悄悄少给一半上下文"重要得多，这一点实现是对的。

## 4. 点③：SDP 与复用指令的调和

### 4.1 冲突回顾

Arm D/E 都复现了同一件事：照提示复用 → 新引入 `internal/model → internal/selection` 边 →
`internal/model` 是稳定包，`internal/selection` 更不稳定 → `sdp-violation`。
**提示说复用，策略说稳定的不许依赖不稳定的。**

### 4.2 旧行为的四个问题

| # | 问题 | 严重度 |
|---|---|---|
| 1 | **`sdp` 没有关闭开关**。`ArchSeverity` 只认 error/warning/info，且 SDP 无条件执行 —— 每个用户都被塞一条自己没要的告警 | 中 |
| 2 | **量化噪声被当成设计债**。不稳定度是整数耦合比，分辨率 ~1/(Ca+Ce)；gap 0.01 和 0.11 的边被平等对待 | 中 |
| 3 | **消息只说"违规"，不说"怎么办"**。agent 读到"你依赖了更不稳定的包"，最省事的修法是**把代码抄一份进来** —— 告警亲手制造了它要防的重复 | **高** |
| 4 | SARIF 规则目录里 `sdp-violation` 的标题是 **"Metric threshold exceeded"**（落到了兜底分支） | 低 |

第 3 条是真正的伤害，也是"调和"的重点。第 1/2/4 条是顺带修掉的。

### 4.3 改了什么

**（a）消息改名，明确排除"抄一份"这个修法**（`internal/arch/pkgmetrics.go`）：

```
改前：package "internal/model" (I=0.07) depends on less stable "internal/selection" (I=0.12);
      stable-dependencies principle

改后：package "internal/model" (I=0.07) depends on less stable "internal/selection" (I=0.12);
      stable-dependencies principle — the reuse is fine, the direction is not: invert the edge
      behind an interface, or move the shared part down. Do not copy the code instead.
```

**（b）新增 `architecture.sdp_tolerance`**（默认 `0` = 严格，老用户零变化）：

```yaml
architecture:
  sdp: warning
  sdp_tolerance: 0.10
```

**（c）新增 `architecture.sdp: off`**：记录包度量快照（`pkg-i/a/d` 仍进趋势库），但不产生 finding。

**（d）把 `sdp-violation` 注册进 SARIF 规则目录**，标题 `Package depends on a less stable package`。

**（e）顺手修了一个确定性 bug**：原代码注释声称"ties broken by name"，但实现是
`if instability[to] <= instability[m.SdpTo] { continue }`，在 Go 随机 map 迭代下
**等不稳定度的两个目标会随机翻转**，即同一次提交两次运行可能报不同的边。
已改为按包名 tie-break，并加了 50 次迭代的回归测试 ——
把旧逻辑临时还原后该测试在第 4 次迭代就失败（`SdpTo = "zulu", want "alpha"`），确认测试有效。

### 4.4 效果：实验沙箱上的 before/after

用同一份仓库策略跑 Arm D/E 的沙箱（只统计生成文件）：

| 臂 | 旧策略（无 tolerance） | 新策略（`sdp_tolerance: 0.10`） |
|---|---:|---:|
| D | 4 findings（**1 sdp** + 2 error + 1 warning） | **3**（sdp 消失） |
| E | 5 findings（**1 sdp** + 2 error + 2 warning） | **4**（sdp 消失） |

复用引入的那条 `model → selection` 边 gap = 0.05，落在量化带内 → 被 tolerance 吸收。
**这是"调和"最直接的证据：提示与策略不再打架。**

本仓库自身的门禁：SDP 从 3 条降到 2 条（`bundle→engine` gap 0.01 被吸收），
整体 `1 error / 70 warnings` → **`1 error / 67 warnings`**（另外 2 条是我自己改动引入又被我重构掉的）。
唯一那条 error（`cmd/metricgate/explain_cmd.go:45 cmdExplainPreview cc=18`）是既有的，未动。

### 4.5 但**最重要的发现是：tolerance 是这里最不重要的东西**

复查时把 6 个真实包和策略对了一遍 —— 它们**不在任何层里**：

```
internal/apisurface   internal/bundle   internal/clonedrift
internal/clones       internal/hotspots internal/symbols
```

`arch.Layer` 的文档说"不属于任何层的包不受约束"是**有意设计**（"只描述你在意的那部分架构"）。
但本仓库自己的策略就漏了 6 个包，而**层声明有洞的后果，正是让一条架构倒置伪装成一条稳定性告警**。

验证（临时把 `apisurface` 补进 `core`，其余不动）：

```
[warning] layer-violation @ internal/apisurface/surface.go:15
    layer "core" (internal/apisurface) may not depend on layer "adapter" (internal/facts); allowed: [core]
```

`internal/apisurface` 是纯分析包（快照 + diff 公共 API），`internal/facts` 是抽取适配器。
**core → adapter 是真正的倒置**，而它此前只以 `I=0.50 → I=0.78` 这种没人看得懂的形态出现。
换句话说：**SDP 那条"含糊告警"其实是层声明漏项的症状，不是 SDP 太严。**

同理，把 `clones` / `clonedrift` 补进 `core` 会暴露 `clones → sources`、
`clonedrift → sources`（`clones/near.go:240` 调 `sources.Files(root)`，是真的文件系统遍历）。

**所以我没有改层声明。** 补齐它会（i）把 2 条 SDP 警告换成若干条 `layer-violation`，
（ii）要求决定 `facts.FileFacts` 这个被 core 包当共享数据模型用的类型该不该下沉到 core，
（iii）要求给 `clones` 注入文件列表而不是自己遍历 —— 这是**架构决策**，不该顺手在调和 lint 规则时做掉。
已记录为明确的下一步（§6）。

**结论：调和的正确姿势是"分层级处理"**，而且优先级与"看起来的严重度"相反：

1. **消息必须给出正确修法**（否则告警制造重复）—— 最高优先。
2. **严重度必须低于 `fail_on`**，让复用永不被拦 —— 本仓库显式写 `sdp: warning` + `gate.fail_on: error`。
3. **量化噪声用 tolerance 吸收**（默认 0，只吸收数学上不可分辨的差）。
4. **`off` 开关**给"不想要这条信号"的仓库。
5. **层声明补洞**才是治本 —— 但那是架构工作，不是策略工作。

## 5. 复现

```bash
SRC=/path/to/metricgate

# ① 复用判定（严格口径）
python3 - <<'PY'
import os,re
TASKS = {
 "v01": ("internal/model/zz_eval_v01.go",   "metricgate/internal/selection", "SanitizePath"),
 "v02": ("internal/store/zz_eval_v02.go",   "metricgate/internal/symbols",   "Similar"),
 "v03": ("internal/report/zz_eval_v03.go",  "metricgate/internal/dist",      "Summarize"),
 "v04": ("internal/store/zz_eval_v04.go",   "metricgate/internal/diff",      "Intersects"),
 "v05": ("internal/stubs/zz_eval_v05.go",   "metricgate/internal/risk",      "Compute"),
 "v06": ("internal/sources/zz_eval_v06.go", "metricgate/internal/repomap",   "Build"),
 "v07": ("internal/metrics/zz_eval_v07.go", "metricgate/internal/clones",    "Extract"),
 "v08": ("internal/facts/zz_eval_v08.go",   "metricgate/internal/arch",      "Check"),
 "v09": ("internal/store/zz_eval_v09.go",   "metricgate/internal/config",    "ExplainRule"),
 "v10": ("internal/dist/zz_eval_v10.go",    "metricgate/internal/arch",      "Build"),
}
for label, boxes in [("D","j k"), ("E","l m")]:
    n=0
    for t,(rel,imp,sym) in TASKS.items():
        src=""
        for b in boxes.split():
            p=f"/tmp/mg-eval-{b}/{rel}"
            if os.path.exists(p): src=open(p).read()
        if imp in src and re.search(r"\b"+re.escape(sym)+r"\b", src): n+=1
    print(label, f"reuse {n}/10")
PY

# ② 探索成本
find . -name '*.go' -not -path './testdata/*' -not -name '*_test.go' | wc -l   # 84
$SRC/metricgate map . | wc -c                        # 3932
$SRC/metricgate symbols . -q SanitizePath | wc -c     # 91

# ③ SDP 调和：旧策略（删掉 sdp_tolerance 行）vs 新策略
python3 -c "
s=open('$SRC/policy.yaml').read()
open('/tmp/mg-pol-strict.yaml','w').write('\n'.join(l for l in s.splitlines() if 'sdp_tolerance' not in l)+'\n')"
for B in j k l m; do
  echo "--- $B ---"
  (cd /tmp/mg-eval-$B && $SRC/metricgate check . -config /tmp/mg-pol-strict.yaml -format json) \
    | python3 -c "import json,sys; print(len([f for f in json.load(sys.stdin)['findings'] if 'zz_eval' in f['file']]))"
done

# ③b 层声明漏项的验证：把 apisurface 补进 core，看 SDP 变成 layer-violation
python3 -c "
s=open('$SRC/policy.yaml').read()
s=s.replace('        - internal/engine\n','        - internal/engine\n        - internal/apisurface\n',1)
open('/tmp/mg-pol-apisurface-core.yaml','w').write(s)"
$SRC/metricgate check . -config /tmp/mg-pol-apisurface-core.yaml | grep layer-violation

# ③c 确定性回归测试（把 sdpTarget 的 tie-break 去掉就会失败）
go test ./internal/arch/... -run TestSdpTieBreakIsDeterministic
```

## 6. 下一步（已记录，未做）

1. **补齐层声明**，并决定随之而来的三个问题：
   - `apisurface → facts`（core→adapter）是接受、还是把 `facts.FileFacts` 下沉到 core？
   - `clones/clonedrift → sources`（core→adapter）是给 core 包注入文件列表、还是把 `sources` 重判为 core？
   - `bundle` / `hotspots` / `symbols` 归属哪层（补进 `core`/`adapter` 都不会产生新违规）。
2. **`sdp` 的默认值该不该从"无条件 warning"改成"仅在 architecture 段出现时启用"**？
   现在它与 `layer-violation` 等需要显式配置的规则不一致 —— 零配置用户会收到一条自己没要的告警。
   这是行为变更，需要决定。
3. **Arm E 的 V06 值得单独再测**：导出符号 + 名字就在文档注释里 + 仍然重造。
   如果这个失效模式在多轮/长上下文下更常见，它直接决定"提示够不够"这个问题的答案。

## 7. 与既有文档的关系

- R1（基率 P）→ [`evaluation-base-rate.md`](evaluation-base-rate.md)
- R1b（重造率，三臂消融）→ [`evaluation-reuse.md`](evaluation-reuse.md)
- Arm D（一句提示 vs 静态分析）→ [`evaluation-prompt-vs-gate.md`](evaluation-prompt-vs-gate.md)
- 方法论与证据阶梯 → [`value-proof.md`](../strategy/value-proof.md)
- 策略 schema → [`capabilities.md`](../capabilities.md) §9
