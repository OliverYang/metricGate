# 判据版本索引：每篇评估报告的数字是哪把尺子量出来的（iter-31 P0-2）

日期：2026-10-18　状态：**已补标**（本文不引入新实验，只让旧数字可被追问）
上游：[iter-30 §6](../iterations/iter-30-recalibration-and-benchmark-scope.md)（P0-2 需求来源）、
[evaluation-iter29-remediation.md](evaluation-iter29-remediation.md) §7（副作用记录）

## §0 为什么需要这一页

iter-29 同时改了两处**判据**（TS/JS 模块级箭头函数从不可见到可量、`test-no-assert`
认「拿到 `*testing.T` 的 helper」），iter-30 又改了一次（`Benchmark*` 不再被要求断言）。
改完之后，`docs/evaluations/` 下的老报告并没有失效——它们的结论多半仍然成立——但
**它们的数字不再能用现在的工具复现**，而没有任何一行告诉读者这件事。

一件事：本页不改任何老结论，只给每篇报告钉上「它的数字来自哪个 commit 的工具链」，
并列出那之后有哪些判据变了。引用老数字的人因此知道自己引的是哪把尺子。

> **一条教训写在这里，因为它已经造成过一次错误结论**（iter-30）：跨版本重放时，
> **policy 与二进制必须同时回到那个 commit**。当时只回退了二进制、policy 用了晚 5 天的
> 186 行版本，重跑数字全面偏高、多出 5 个归档里根本没有的规则族，一度得出「归档不可复现」
> 的错误结论——差一点把一份可信数据标成坏数据。所以本页每个条目都同时记 **工具链 commit
> 与 policy 版本**，缺一个就不算标定。

## §1 判据变更时间线（`main` 上的 commit）

| commit | 日期 | 改了什么判据 | 影响哪些老数字 |
|---|---|---|---|
| `73fd85d` | 2026-09-23 | **v0.1.0 基线**（另一个根，之前的历史由 `7ada898` 等 19 个 commit 构成） | — |
| `7ada898` | 2026-09-24 | near-clone 检测上线（Phase B-4） | Phase B 的 `function-clone` 数 |
| `7686857` | 2026-09-24 | evidence anchor 上线（Phase D4） | 早期报告的 anchorStatus 口径 |
| `becb5ea` | 2026-09-23 | MCP stdio server | `evaluation.md`「0.1.0 @ becb5ea+」自述 |
| `7ea7318` | 2026-10-14 | 语言对等campaign + 依赖/耦合可达性 | 语言相关的度量与规则数 |
| `8f78d0c` | 2026-10-08 | 引擎重构 + `test-no-assert` 的 Go 侧实现落地 | M2-B 与 replay 的 tna 数 |
| `1140909` | 2026-10-08 | tna 任务集 + 已验证 bad-shape 探针 | 同上 |
| `59e0a24` | 2026-10-16 | call-chain-depth 跨语言化 + SQL/VB/Verilog 适配器 | call-chain 数、多语言度量 |
| `760f498` | 2026-10-18 | **TS/JS 模块级箭头函数成为函数单元** | 一切 TS/JS 的函数级度量（见下） |
| `ecd57b5` | 2026-10-18 | **tna 认「拿到 `*testing.T` 的 helper」** | 语料 55→19、本仓 8→0 |
| `d2bcf9b` | 2026-10-18 | **tna 排除 `Benchmark*`** | 语料 19→5、dev-cockpit 233→215 |

## §2 逐篇：采集日期 → 当时 main → 之后变过的判据

采集日期取文档自述；「当时 main」=`git rev-list -1 --before=<日期 23:59> main`；
「之后变过」= 表 §1 中在该 commit 之后落地的判据。**这个判定是机械的，可重跑**：

```bash
python3 /tmp/crit.py   # 遍历 docs/evaluations/*.md 的采集日期，输出 base + 之后判据
```

| 报告 | 采集日期 | 当时 main | 之后变过的判据 | 数字可否按现工具复现 |
|---|---|---|---|---|
| `evaluation.md` | 2025-09-30 | （早于 v0.1.0 根，commit 无从归属；文内自述 `0.1.0 @ becb5ea+`） | 全部 | ❌ 只作文物读；150/150 检出率这类结论仍有效，**具体条数不可复现** |
| `evaluation-phase-b.md` | 2025-10-06 周期 | `7ada898`（Phase B-4 落地处） | `760f498` `59e0a24` `7ea7318` | ❌ 「0 errors / 46 warnings / 2,606 snapshots」是当时树的值 |
| `evaluation-phase-d.md` | 2025-10-06 周期 | `7686857` | 同上 | ❌ 同左；anchorStatus 语义在 D4 之后未再改，这部分仍可比 |
| `evaluation-trends.md` | 2025-10-06 周期 | （同 Phase B/D 周期） | 同上 | ❌ 「69 warnings / 3595 snapshots」且**它测的正是稳定性**——重复跑一致不证明判据未变，别拿它当「现在也是 69」的证据 |
| `evaluation-base-rate.md` | 2026-09-27/28 | `7c3aa36` | **全部 7 项**（含 `760f498`/`ecd57b5`/`d2bcf9b`） | ❌ 31.8% 首过违规率仍是有效结论（端点/任务集同时变了，已在 §5 自述），但按规则拆分的计数不可复现 |
| `evaluation-reuse.md` | 2026-09-28 | `7c3aa36` | 同上 | ❌ 重造率结论有效；涉及函数单元的计数不可复现 |
| `evaluation-prompt-vs-gate.md` | 2026-09-28 | `7c3aa36` | 同上 | ❌ 同左 |
| `evaluation-hint-and-cost.md` | 2026-09-28 | `7c3aa36` | 同上 | ❌ 设计文档，无实测计数，影响最小 |
| `evaluation-r2-repair-loop.md` | 2026-09-29 | `7c3aa36` | 同上 | ❌ 7/7 收敛结论有效；`U04 layer-violation×2` 之类计数不可复现 |
| `evaluation-r3-grounding.md` | 2026-09-29 | `7c3aa36` | 同上 | ❌ 31.8%→4.5% 结论有效；按规则拆分不可复现 |
| `evaluation-m2b-paper-metrics-protocol.md` | 2026-10-07 | `7c3aa36` | `8f78d0c` `1140909` `760f498` `ecd57b5` `d2bcf9b` `59e0a24` `7ea7318` | ❌ 预注册文档，判断线仍是判断线；但 tna 当时尚无 Go 实现（见下） |
| `evaluation-m2b-taskset-paper-metrics.md` | 2026-10-08 | `82273e1` | `760f498` `ecd57b5` `d2bcf9b` `59e0a24` `7ea7318` | ⚠️ N07 的 bad-shape 夹具 iter-31 已用现判据复跑仍 fire；其余阈值类阈值未改，仍有效 |
| `evaluation-m2b-results.md` | 2026-10-09 | `86131a8` | `760f498` `ecd57b5` `d2bcf9b` `59e0a24` `7ea7318` | ⚠️ 「三臂 tna 均 0」用旧判据量得；范围已收窄（见该文档 §4.3 注） |
| `evaluation-m2-arch-results.md` | 2026-10-09 | `86131a8` | 同上 | ⚠️ 3/3 结论不受判据影响（layer-violation 判据未变）；`fan-out=14` 之类需按判据读 |
| `evaluation-commit-replay-protocol.md` | 2026-10-09 | `86131a8` | 同上 | ⚠️ 协议，判定线不变 |
| `evaluation-commit-replay-results.md` | 2026-10-09/10 | `85d71e7`–`d5cb01c` 区间 | 同上 | ⚠️ **判据不可归因到确切 commit**；时代最近复现 = `/tmp/mg-era-bin`@`d5cb01c` + 157 行 policy，已逐条复现归档。§2 表 13/18 行仍不可复现 |
| `evaluation-iter29-remediation.md` | 2026-10-18 | `c386b4a` | `d2bcf9b`（在其后） | ✅ before/after 都自带 commit 边界；§6.1 是「同树两把尺」的隔离测量 |
| `evaluation-iter30-tna-recalibration.md` | 2026-10-18 | `739aa11` | 无 | ✅ 当前判据 |
| `evaluation-ab-experiment.md` | 从未执行 | — | — | — 设计文档 |
| `ab-experiment` 之外的 `m2b-paper-metrics-protocol` | — | — | — | 同 protocol 行 |

> **读法**：❌ = 这个报告的数字只能用「当时的工具」复现，现在就当它是个带日期的快照；
> ⚠️ = 结论有效但引数字时必须连判据版本一起引；✅ = 与当前判据一致。
> 没有任何一篇被推翻——被改的是**引用它们的方式**。

## §3 三个必须一起读的组合

1. **R1/R2/R3 三条证据阶梯**（`base-rate` / `r2-repair-loop` / `r3-grounding`）：
   全部采集于 `7c3aa36`，同端点在 iter-30 之后已不可用（2api vs Cliproxyapi 的差别文档
   自己在 §5 写了）。它们支撑的是 `docs/strategy/value-proof.md` 的 P 与性价比结论，
   **不是任何一条规则的当前表现**。
2. **M2-B + M2 + commit-replay 三条**：都在 `8f78d0c`–`1140909` 与 `86131a8` 区间，
   共用一个反-registered 前提——`test-no-assert` 的 Go 实现在 `8f78d0c` 才落地，
   而且当时的判据既不会把 helper 算断言、也不会排除 benchmark。三条报告里凡涉及
   tna 的句子，一律按 iter-30 重标定后的口径读。
3. **iter-29 + iter-30 两条整改报告**：是唯一自带 before/after commit 边界的一对。
   引用它们的差值时必须连同 commit 对一起引，否则差值会被读成「同一把尺子量出的变化」。

## §4 本页不做什么

- **不做数字换算**。把老报告的 tna 计数按新判据折算一个「等效值」听起来有用，
  实际是在制造第四个口径——iter-30 已经有三个口径了（归档 / helper / 排除 benchmark）。
  该重跑就重跑，重跑不了就标不可比。
- **不改老报告正文**。只在文档头加「判据版本」注（已在 M2-B、replay、iter-29 三处落好）。
- **不宣称覆盖未列出的口径**。policy 文件版本、沙箱污染控制、端点型号这些都只影响
  臂间可比性，文档各自的 §5「诚实边界」已经覆盖，本页不再重复。

## §5 复现

```bash
cd /Users/mac/Downloads/metricGate
# 1. 每篇报告采集时的 main
git rev-list -1 --before="2026-09-28 23:59" main
# 2. 某判据 commit 是否在那之后
git merge-base --is-ancestor 7c3aa36 ecd57b5 && echo "after"
# 3. 两个 commit 之间哪些判据文件动过（内部一致性检查）
git diff --stat 7c3aa36 86131a8 -- internal/rules/ internal/metrics/
# 4. 时代正确的单 commit 复现（policy 与二进制同版本，iter-30 的教训）
git worktree add /tmp/mg-era d5cb01c && go -C /tmp/mg-era build -o /tmp/mg-era-bin ./cmd/metricgate
git show d5cb01c:policy.yaml > /tmp/policy-era.yaml
(cd /tmp/dev-cockpit && git checkout -q 396cd3cd8f && /tmp/mg-era-bin check . -config /tmp/policy-era.yaml -format json)
```
