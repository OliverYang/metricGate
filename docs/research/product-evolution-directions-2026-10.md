# metricGate 产品演进方向调研（短期 0–2 个迭代 / 中期 3–6 个迭代）

> **定位**：本文只回答一件事——在 v1.5 已交付、v2（HTTP/gRPC 服务 + context generator）尚未开工的当口，
> 接下来 2 个迭代该做什么、3–6 个迭代该往哪走。
>
> **方法**：所有"现状事实"来自本仓库的一次可复现命令或一行代码定位；所有"外部格局"带检索日期；
> 所有"价值判断"回指 [claims.md](../claims.md) 的 claim 台账，不新造价值主张。
> **本文不改 roadmap**：[docs/roadmap.md](../roadmap.md:183-189) 明确 `v2/v3 规划不变`，本文是它的输入而非替代。

---

## 0. 摘要：一句话结论与三条主张

**结论**：短期（0–2 迭代）不要做新能力，做**闸门自身可信度**与**分发面**；中期（3–6 迭代）把
"确定性约束层"从"本仓库的护城河"变成"别人 harness 的默认开关"。

三条主张及支撑证据：

| # | 主张 | 证据强度 |
|---|---|---|
| S1 | **闸门速度与 store 健康度双重失效**：`-diff` 因两处 git 子进程扇出而慢（实测 282 次进程，逐文件 diff 99 + 每路径重解析 worktree root 172）；store 无保留策略已涨到 2.19 GB 并把整条命令拖慢；而 pre-commit hook 跑的正是 `-diff` | 四轮实测 + 代码定位（两个独立根因；扇出已在 2026-10-16 修掉：282→8 进程，见 S1） |
| S2 | **分发面只剩一个缺口**：harness 的 post-edit hook 已在 [docs/workflow.md](../workflow.md:106) 写好却未发货（`hooks/` 下只有 pre-commit） | 文档 × 文件系统对照 |
| S3 | **外部竞品从"空白"变成"拥挤"**：positioning.md:264（HEAD 版）冻结于 2024-09-28 的"市场空白"论断已过期，差异化必须改到"事实层"而非"空白层"重讲 | 外部检索（2026-10-16），五家逐条一级来源见下节 S3；**工作区已改写为 v2.0（含对照表）** |

---

## 1. 方法与证据分级

### 1.1 分级

| 级 | 含义 | 本文用法 |
|---|---|---|
| **E1 实测** | 本仓一次可复现命令的输出，命令在文中给出 | 可支撑优先级排序 |
| **E2 定位** | 代码行定位（文件:行），说明机制而非解释动机 | 可支撑改法判断 |
| **E3 文档** | 仓库内既有文档的记载 | 作为事实引用，不重复验证 |
| **E4 外部** | 2026-10 检索到的第三方数据 | 只用于论证"问题真实存在"，不用于证明 metricGate 有效 |
| **E5 类比** | 从外部做法推演的选项 | 显式标注为待验证 |

### 1.2 三条不越界

1. **不用 benchmark 分数证明方向**（[value-proof.md](../strategy/value-proof.md) 陷阱清单第 1 条）。
2. **所有 E4 数据都只回答"问题是否真实"**，metricGate 是否解决它由 [claims.md](../claims.md) 台账回答——
   台账里 ❌ 的三条（#1 检出多于竞品 / #2 减少幻觉 / #3 省探索 token）本文绝不引用为论据。
3. **能力论证会过期**：凡涉外部格局，一律带检索月份。

---

## 2. 现状盘点：先确认"我们到底站在哪"

### 2.1 已经站住的部分（E1/E3，不复述能力清单，只标结构性事实）

- **v1.5 已全部落地**：[docs/backlog-v1x.md](../backlog-v1x.md) 中 iter 1–18 全 ✅，README.md:497-499 的 v0/v1/v1+ 三行全 ✅，
  roadmap.md 的"审美回路补全"5 个迭代全 ✅，[five-properties-report.md](../iterations/five-properties-report.md) 五性全部闭环（唯一 ⚠️ 是"建议采纳率"未度量）。
- **闭环形态已存在而非设想**：`.metricgate/mcp-calls.jsonl` 360 条、15 session；`metricgate calls` 输出
  intercept 48% / converge 45% / clean-first-try 2 项（E1）。这是 claims #15（闸门真的被 agent 调用了、finding 真的被修了）
  的**第一个真实数据点**——虽然是单仓库 dogfooding，不是多仓库样本。
- **价值主张的三句话已锁定**：[claims.md](../claims.md) 的"现在能对外说的三句话"（知道你的私有规则 / 把"我照做了"变成可检事实 /
  看见一次改动对整个依赖图做了什么）。本文所有方向都必须落回这三句之一，否则不做。

### 2.2 文档自身的不一致（E1，先修号再谈新增）

| 位置 | 记载 | 实际 | 处置 |
|---|---|---|---|
| README.md:69 | "19 CLI subcommands" | `metricgate --help` 列 **28 个动词**；`cmd/metricgate/main.go` 的 `var commands` 是 **29 键**——28 与 29 的差**只来自 `rules-gaps` 这一个键**：它是 `rules gaps` 的别名，help 不单列一行（不是「同一动词多形态」：多形态会让行数变大而不是键数，按行抽动词不去重会得到 37） | ✅ **已改为 "28 CLI subcommands"**（2026-10-16，取 `--help` 口径：用户可自行数的数） |
| docs/capabilities.md:261 | "20 个子命令" | 同上 | ✅ **已改为 28**，与 README 对齐 |
| docs/capabilities.md:304 | "11 个工具" | `cmd/metricgate-mcp/tools.go` 注册 12 个（check_code 196 次调用可证它们在跑） | ✅ **已改为 12** |
| README.md:500 v2 | "HTTP/gRPC service" | 但 `metricgate cockpit serve` 已是一个 loopback HTTP 服务（`cmd/metricgate/cockpit_cmd.go` 拒绝非 loopback） | ✅ **v2 已改为 "Remote HTTP/gRPC service (SDK)"**（2026-10-16） |

> 这几处是**最便宜的信任债**：一个要卖"确定性"的工具，自己的 README 数字对不上。
> **✅ 四行已全部修完（2026-10-16）**：口径数字直接由 `go run ./cmd/metricgate --help`（去重后 28 个动词）与
> `cmd/metricgate-mcp/tools.go` 的注册表（12 个）得出，不是人工估的。
> 注意 help 里有 6 个动词因多形态而重复成行（api/arch/check/cockpit/reachability/rules），
> 所以口径必须说清是"去重后的动词数"。

### 2.3 关键度量：闸门自身的健康度（E1）

| 观测 | 值 | 含义 |
|---|---|---|
| `check .`（整树，脏） | 第一轮 1.36–1.88 s；第二轮 78.03 s；**第三轮（扇出修后）48.16 s / 关 store 35.29–51.66 s** | 见 S1：`-diff` 两处扇出 + store 膨胀是两个独立因素 |
| `check . -diff`（同一脏树） | 第一轮 **12.6–15.8 s**；第二轮 76.12 s；**第三轮 79.51 s（改前）→ 27.59–51.96 s（批量后）** | 见 S1（含 shim 计数 282→9） |
| `bundle .` 默认 6000 单位 | `budget_exhausted`（需要 ≥ 11.2 M 字符才装得下） | review bundle 在真实仓库**开箱即坏** |
| `check . -format json` | files 284 / findings 356–357 / metrics 22009 | 自检规模 |
| `.metricgate/accepted.yaml` | 1 条：fan-out, `cmd/metricgate-mcp/main.go:35`, 2026-12-31 过期 | 让步系统本身健康 |
| `explain .`（选文件、不跑规则） | 1.59 s | 证明 78 s 的退化发生在规则执行 + 写库侧，不在解析/选文件侧 |
| history.db | 0.79 GB → **2.19 GB**（两轮间 +0.56 GB），1018 runs / 93097 findings / 8.7M snapshots，无保留策略；**第三轮实测 runs 1238 / snapshots 18,541,775 / findings 1,172,412，`.metricgate/` 整目录 2.3 GB** | 见 S1 / D4：它已从背景债升级为污染其他度量的活缺陷 |
| pre-commit 跑的 `check . -diff` | 全树基线 35–51 s ⇒ 提交前 ≤3 s **不可达**；scoped `check <目录> -diff` 额外开销第四轮实测中位数 **docs +1.65 / internal +1.95 / cmd +2.05 s** | 见 S1 修订版判据 ③；由此长出"pre-commit 改 scoped check 根"新候选 |

---

## 3. 短期方向（0–2 个迭代）

原则：**短期不新增能力族，只消灭"自伤"**。理由见 §5 的反证一节——在闸门自身有几个已知口径不一致、
分发面有一个已写好未发货的缺口时，新能力的边际价值低于修可信度。

### S1 `-diff` 性能：把闸门从"想用而不敢常开"变成"随手可开"（E1 + E2，最高优先级）

**现状事实（E1，两轮采集，须连读）**

第一轮（脏树 105 个变更路径）：

```
./metricgate check .           1.36 s / 1.88 s
./metricgate check . -diff    12.56 s / 14.35 s / 15.80 s
```

第二轮复测（2026-10-16 晚，`.metricgate/history.db` 已涨到 2.1 GB、脏树 149 个变更路径）:

```
./metricgate check .           78.03 s
./metricgate check . -diff     76.12 s   ← 倍差消失
```

**这个自相矛盾本身就是 diagnose 材料，不能择一去报**。两轮的共同点与差异：

| | 第一轮 | 第二轮 | 变动因素 |
|---|---|---|---|
| `check .` | 1.36–1.88 s | **78.03 s** | — |
| `check . -diff` | 12.56–15.80 s | 76.12 s | — |
| 倍差 | 8–10× | **≈1×** | — |
| `.metricgate/history.db` | 0.79 GB | 2.19 GB | +0.56 GB（`metric_snapshots` 8.7M 行级，`runs.archived` 只隐藏不回收）|
| 脏路径数 | 105 | 149 | +44 |

**根因不唯一，且第二轮暴露了第二个独立缺陷**。`explain .`（只选文件、不跑规则）= 1.59 s，证明解析与选文件没退化；78 s 花在**规则执行 + 写库**上。

> **本节原先写的"先修 D4 再修 S1，否则 S1 的收益测不出来"已被第三轮实测反证**（见下）：
> 关掉 store 后 `check . -diff` = 90.45 / 98.27 s，比**开着** store 的 79.51 s 更慢 ⇒ `-diff` 的倍差与 `history.db` 大小无关。
> store 只影响**绝对基线**（D4 仍要做），不是 S1 的前置条件，两件事可并行。

**诊断过程**（排除了三个错误假设）：

| 假设 | 检验 | 结论 |
|---|---|---|
| arch 闸门慢 | `arch diff` = 1.49 s；去掉 `-arch-base` 无变化 | ❌ 否 |
| git 本身慢 | 整树一次 `git diff HEAD` = 0.10–0.12 s；74 次逐文件 `git diff HEAD -- <f>` = 5.51 s | 部分↯ |
| 仓库太大 | /tmp/mgclean（`git archive HEAD` 干净树）touch N 个文件：1→1.74 s / 5→2.00 s / 20→4.49 s / 60→9.91 s | ✅ 随**变更文件数**线性涨，与仓库规模无关 |

**根因（E2）**：`internal/git/changes.go:90` `fillHunks` 对每个变更文件起一次 `git diff` 子进程
（`gitBytes(root, patchArgs(req, changePaths(*c))...)`，见 changes.go:99），N 个变更文件 = N 个进程。
这才是「(1) 慢、(2) 与仓库大小无关」的共同解释。

> 注：`internal/git` 是全仓唯一被允许的 `os/exec` 边界（[capabilities.md](../capabilities.md) §11），
> 所以"少起进程"本身也是尊重既定架构的修法，不是绕开它。

**三个可选修法**

| 方案 | 做法 | 预期 | 风险 |
|---|---|---|---|
| **A 批量化（推荐）** | `fillHunks` 从按文件起进程改为一次 `git diff` 全量再按文件切分 | 进程数 74→1，按 /tmp/mgclean 曲线预估 60 文件从 ~9.9 s 降到 ~2 s | 需处理 binary/rename/short-header；`patchArgs` 逐文件语义要合并 |
| B 缓存 hunks | parsed-hunks 按 (base, path, blobhash) 缓存 | 重复 commit/rerun 收益大 | 引入缓存失效问题，短期不划算 |
| C 异步并发 | 保留 per-file 调用但 goroutine 并行 | 治标，进程数不变 | 破坏字节确定性输出顺序的风险 |

**第三轮实测（2026-10-16，E1）：把"慢"拆成"进程数 × 单进程固定成本"**

用 PATH shim 计数（`/tmp/mgshim/git` 记录一行调用参数再 `exec /usr/bin/git "$@"`）跑 `check . -diff`，
脏树 = 99 tracked + 74 untracked = 173 路径：

| 计数项 | 数值 |
|---|---|
| git 子进程总数 | **282**；同树 `check .`（不带 `-diff`）只有 **3** ⇒ 282 全属 `-diff` 路径 |
| 其中逐文件 `diff … -- <单个路径>` | **99** ← `internal/git/changes.go:99` 的 `fillHunks` |
| 其中 `rev-parse --show-toplevel` | **172** ← **第二处扇出**，此前未发现 |
| 墙钟 | `check .` 48.16 s / `check . -diff` **79.51 s**（差 31.35 s） |

单条 git 命令的固定成本（树副本上各取三次最小值）：全量 `diff` 0.40 s、`diff --name-status -z` 0.28 s、
`ls-files --others` 0.24 s、`merge-base` 0.21 s、`rev-parse --show-toplevel` 0.29 s、`ls-tree` 0.26 s、
`archive` 0.28 s、`rev-parse --short HEAD` 0.91 s ⇒ **每个 git 往返 ≈0.21–0.4 s**。
据此直接计时：99 次逐文件 diff = **13.97 s**（60 次 = 12.18 s），100 次 `show-toplevel` = **12.39 s**（⇒172 次 ≈21 s），
两者合计 ≈35 s ≈ 整个倍差。**倍差的全部来源是进程 spawn，不是解析、不是规则、不是 store。**
（环境：Apple Git 2.39.5 / macOS。固定成本随机器变，但"进程数"这一项跨机器可比，才是本节目的。）

第二处扇出定位到 `git.RepoRelative(checkRoot, path)`：它每次都 `Root(checkRoot)`（一次 `rev-parse --show-toplevel`）
再 `checkBaseDir`，而调用方对**每个变更路径**各调一次 ⇒ 173 路径 = 172 次。写这一节时调用方是
`cmd/metricgate/regress_cmd.go:80 repoRelativePaths`；本 goal 期间该函数被并发重构搬到
`internal/baseline/baseline.go:165`（扇出形态不变，两处修复都已跟进）。

**修法 A 已落地（同一 goal 内，E1 已验）**

| 改动 | 落点 | 效果 |
|---|---|---|
| 一次全量 patch + 按 `diff --git ` 切 section，用 `--name-status -z` 的**未引号化权威路径**做归属 | `attachHunks` internal/git/changes.go:87、`assignPatchHunks` internal/git/patch.go:18、`splitPatchSections` patch.go:64 | 逐文件 diff 99→1 |
| `patchArgs` 固定 `--src-prefix=a/ --dst-prefix=b/` | internal/git/changes.go:225 | 用户 config 的 `diff.mnemonicprefix` / `diff.noprefix` 会把 header 变成 `c/f.txt w/f.txt` / 裸路径 ⇒ 不固定就**静默丢 hunk**（测试 `TestLoadChangesIgnoresUserDiffConfig` 钉住） |
| `RepoRelativizer` 一次解析两套坐标系，`RepoRelative` 公开签名与"不在 worktree 内返回 false"语义不变 | internal/git/git.go:102 / :121 | `show-toplevel` 172→1 |
| `Root()` 加 per-process memoization，**只缓存非空答案**（miss 不缓存） | internal/git/git.go:188 | 长驻进程（MCP server、cockpit gateWatcher）同目录不再重复起进程；代价是 worktree 移动后报告旧路径 |

结果：**git 子进程 282 → 9 →（第四轮消 merge-base）8**（2 `show-toplevel` + 1 name-status + 1 patch + 1 `ls-files` + 1 `ls-tree` + 1 `archive` + 1 `rev-parse --short HEAD`）。
**第四轮再消 2 次 `merge-base`（2026-10-16，同一 goal 内）**：`ResolveBase`（internal/git/git.go:31）在
base 已经是 `HEAD` 时直接返回、不起 `git merge-base HEAD HEAD` —— HEAD 与 HEAD 的 merge-base 就是 HEAD，
这一跳只是花 0.21–0.4 s 重算一个手里已有的值；而它在一次 `-diff` 里被调两次（本函数 +
`internal/baseline/baseline.go:97` 基线快照）。非 HEAD 的 base（`-base origin/main`）行为不变，
失败仍回退原始 ref；测试 `TestResolveBaseShortCircuitsForHead`（internal/git/git_test.go）钉住短路边，
并把原 `TestResolveBaseFallsBackOutsideRepo` 的输入从 "HEAD" 改成 "main"（否则新短路会意外接管该回退测试）。
改前/改后输出逐字节对比：关 store 的树副本上两份 JSON（各 9,420,151 bytes）**完全相同**，stderr 也相同。
语义保持的三条边界已逐个测过：binary 文件仍"无 hunk"、R100 纯改名仍 nil hunks、C-引号化路径
（`back\slash` / `quo"te` / `tab<TAB>name`）切分后与 name-status 的未引号化路径一致。
全树 A/B（树副本 171 脏路径，HEAD 逐文件版 vs 批量版交替两轮）：83.7 vs 51.96 s = **1.61×**、
61.68 vs 27.59 s = **2.24×**（省 31.7 / 34.1 s）。

**验收口径（修订版，E1）** — 原三条有两条在本 goal 实测下口径本身不成立，按实测改写并写明理由：

1. ~~`check . -diff` ≤ 2.5× `check .`~~ → **`-diff` 相对 `check .` 的额外开销 ≤2 s（scoped 口径的中位数）**。倍差判据作废：整树基线本身
   35–51 s（关 store 三次实测 35.29 / 42.25 / 51.66 s，机器噪声 ±30%），倍差读数不可靠。
   **扇出修完（9 进程）时的实测**：额外开销 cmd +2.52 / internal +2.28 / docs +2.27 s（树副本各 3 次中位数），
   当时据此写过“原 ≤2 s 在架构内不可达”。**该结论已被第四轮推翻**：消掉 2 次 `merge-base` 后（8 进程）
   重测同一树副本（docs 5 对 / cmd 3 对 / internal 3 对，交替计时取中位数）：
   **docs +1.65 s（区间 1.42–1.88）、cmd +2.05 s（1.82–2.54）、internal +1.95 s（1.00–2.81）** ⇒ **原 ≤2 s 判据可达**，
   不必改口径。残留的 ≈1.5–2 s 是 8 次 git 往返 × ≈0.2 s 的 spawn 成本，最贵的单次是
   `rev-parse --short HEAD`（0.91 s，来由：`cmd/metricgate/main.go:187 gitCommit()` 为 store 记 commit）。
   诚实的注脚：全树计时**不具备分辨力**——收口时机器高负载下重测三对 = +10.93 / −176.13 / −12.2 s（基线本身
   75→231 s 拖动）；之前三对 = +0.93 / +8.42 / +1.36 s。⇒ 判据只能在 scoped 口径上判。
   **判据的负载敏感性（收口后重测，2026-10-16 16:45）**：机器 load average 18.6（8 CPU，多个 pi/node 进程在跑）时
   同一批 scoped 重测 5 对中位数 → `internal/traceview`（0 脏文件）+0.87 s、`internal/git`（3 脏）+2.25 s、
   `cmd/metricgate-mcp`（5 脏）+2.52 s；同时单条 `git rev-parse --show-toplevel` 的往返成本量到 **0.188 s**。
   ⇒ ≤2 s 只在低负载下稳定成立，**真正与负载无关的不变量是进程数**：shim 重测全树 `check . -diff` = **8 次**，
   scoped `check internal/traceview|internal/git|cmd -diff` = **6 / 7 / 7 次**（name-status 恒 1 次、patch 恒 1 次，
   不随脏文件数 0→22 增长）。写入文档时把 ≤2 s 读作「低负载下的中位数目标」，硬判据用进程数。
   可继续消的候选（收益递减，列 iter-19）：基线快照与变更集共用一次 merge-base 结果（传已解析的 sha）、
   `gitCommit()` 改读 `HEAD` 的松散引用或缓存。
2. 五个连续 `check . -diff -format json` **字节完全相同**（claims #10 不容许用并发换确定性松动）——
   **必须关 store 比**，或逐叶子排除 `/run/runId`：store 开启时唯一差异是 run 自增 id（实测 1264 vs 1265），与判定无关。
   树副本 5 连跑 md5 **全同**（本轮值 `e76279a5c3a6a80f9e71e3fb6420dd2f`，扇出修完那轮值 `ac7186523d7106075e3957ad18a3136b`；
   具体值跟树快照绑定，**判据是同值×5、不是某个固定 md5**）⇒ 通过；HEAD 逐文件版与批量版的 JSON body 叶差 **0**，
   `-hidden` / `-regress` 文本输出逐字节相同；消 merge-base 前后两份 JSON（各 9,420,151 bytes）**逐字节相同**。
3. ~~pre-commit hook 在 149-path 脏树上 ≤3 s~~ → **scoped 口径：`check <目录> -diff` 的额外开销 ≤2 s（低负载中位数），
   硬判据用进程数不随脏文件数增长（全树 8 / scoped 6–7）**（同 ①，docs/internal 中位数达线，cmd +2.05 s 贴着阈值且在噪声内）。
   原口径不可达：`hooks/pre-commit:50` 跑 `exec "$bin" check . -diff $archflags` 是**全树**，而全树 `check .`
   基线 35–51 s 属解析 + 规则执行，`-diff` 修不掉。⇒ 由此**长出一条新候选**：pre-commit 改为只查将提交的路径
   （以 `git diff --name-only --cached` 作 check 根），那才是"提交前 3 s"的真正前提，列 iter-19 之后。

> 分目录改前实测（关 store，供对照）：internal 6.03 s / cmd 2.39 s / docs 2.92 s。

### S2 post-edit hook 发货：把一个已写完的分发面真正发出去（E1）

**现状事实**：[docs/workflow.md](../workflow.md:106-137) 已经给出完整方案——`hooks/post-edit.sh`
加一份 Claude Code `PostToolUse` (`Edit|Write`) 的 `settings.json` 块，并且诚实记录了语义边界
（`PostToolUse` **不能**阻止已发生的编辑，`exit 2` 的作用是把 stderr 送到模型眼前）。
但 `ls hooks/` 只有一个 `pre-commit`。`find . -name 'post-edit*'` 为空。

**为什么短期做（E4 + E5）**：2026-10 检索显示，四家主流 agent harness（Claude Code / Cursor / OpenAI Codex / 以及第三方汇总）
都把 **hooks 作为确定性控制的规范入口**，官方与社区表述收敛为同一句："Guidance is probabilistic; hooks are deterministic"——
CLAUDE.md 是请求、hooks 是保证。Cursor 甚至能直接加载 Claude Code 的 hook 配置（Third-Party Hooks）。
也就是说：**写一个 hook 脚本，可以同时打到两个 harness**，这是本仓性价比最高的一次分发投入。

**做法**：发货 workflow.md 里的草图，并且做对三件小事：

1. `hooks/post-edit.sh` 必须是**纯 shell 入口**（workflow.md 已这样要求），内部调 `$METRICGATE check "$file" -diff`；
   只报**当前文件 + error 级**，避免把整仓 563 个 warning 灌进模型上下文（这是 pilot 报告记录的教训）。
2. 输出必须短：一行一个 finding + 规则 id + 修复建议（rulegen.Verify 已有 `use` 字段渲染，见 internal/rulegen/rulegen.go:37）。
3. 与 MCP 路径**同一判定内核**（`engine.FilterSummary`，见 cmd/metricgate-mcp/checks.go:85），不许 hook 另写一套过滤规则。

**验收口径**：一份 `settings.json` 样例 + `hooks/post-edit.sh` 进仓；用 Claude Code 编一次
`internal/...` 下的真实修改，观察 finding 是否以 <2 s 延迟出现在模型眼前，且**不重复报告与该文件无关的历史欠账**
（这条由 S1 的 `-diff` churn 过滤保证，所以 S1 必须先于或同批于 S2）。

### S3 文档信任债与 `version` 真相（E1，与 S1/S2 同批的低成本项）

| 项 | 现状 | 修法 |
|---|---|---|
| §2.2 的 4 处数字漂移 | **已改完（2026-10-16 E1 实测）**：README.md:69 与 docs/capabilities.md:261 已写 28 个子命令、:304 已写 12 个 MCP 工具，与 `--help`（37 行动词行、去重 28）和 `grep -cE 'Name:\s+"[a-z_]+"' cmd/metricgate-mcp/tools.go`（12）一致 | 剩余：把 README 的 roadmap 表行号口径固定（v0 :497 / v1 :498 / v1+ :499 / v2 :500 / v3 :501） |
| `toolVersion` 对外说"0.1.0"，而 git 已经是 v1.5 之后（iter-18） | 实测 `./metricgate version` → `metricgate 0.1.0 5c1595c79c62+dirty`；`cmd/metricgate/main.go:36` 是 `var toolVersion = "0.1.0"`（:29 只是注释；改成 `var` 就是为了可被 `-ldflags -X` 注入），**但全仓 grep `ldflags` 在 .github / scripts / Makefile 均无命中** ⇒ 注入点存在、没有发布管道用它 | 在 CI/release 工作流里真正注入 `-X main.toolVersion=$(git describe)`，或把 version 输出改成以 commit 为准并明说"未发版" |
| ~~`cockpit update` 拿一个不存在的域名做检查~~ **已修**（原引 `internal/cockpit/cockpit.go:30 DefaultFeedURL = https://releases.metricgate.dev/latest.json`、实测 `no such host`） | 现在 `DefaultFeedURL` 已从代码里删除，`internal/cockpit/cockpit.go:28-34` 只剩状态常量（含 `StateNotEnabled = "not_enabled"`）；实测 `./metricgate cockpit update -format json -timeout 8s` → `"state": "not_enabled"`, `"error": "no release feed configured; pass -feed URL"` | 本项降级为：把"要不要有 update feed"做成显式产品决策（现在默认是"无"，对外界等于"不宣称可升级"），并把 §8 附录里的复现命令说明同步到新语义 |

---

## 4. 中期方向（3–6 个迭代）

原则：中期每一支都回指 claims #5/#6/#8/#9（同一套价值主张，不同切面），
且**优先补"规则表达力"而不是"规则数量"**——原因见 D1 的实测证据。

### D1 标识符层面的自定义规则（P0，把无法表达的团队规范变可表达）

**证据（E3，且为真实项目实测数据）**：[pilot-report-scan-portal.md](../pilot/pilot-report-scan-portal.md) 与
[conversion-map.md](../pilot/conversion-map.md) 记录了同一批 40 条 Java 规范的真实转化结果：✅15 / ⚠️5 / ❌20。
其中**13 条 ❌ 集中在少数几个机制缺口**，且高度重叠：NAMING-01~06（类名/方法名/常量/词缀/POJO 布尔前缀/层内方法前缀）、
COMMENT-01/02（Javadoc）、EXC-02（catch 参数类型）、EXC-03（finally return）、LOG-04（占位符）——conversion-map.md 明说
"NAMING-01~06 六条命名条款卡在同一个缺口：需要标识符风格检查器；内置无"。

**为什么它是 P0 而不是别的**：

- 它是"**一个机制吃 6 条条款**"的杠杆点；`constraints rule`（cmd/metricgate/constraints_cmd.go:124）已经把
  Spec → 反例/正例 fixture → 引擎真跑 → PASS/FAIL + 可合并 policy 片段这条"LLM 提案、引擎裁决"的链路做出来了
  （内链在 [rule-integration-interface.md](../constraints/rule-integration-interface.md)），
  但它能表达的 Spec 目前**只覆盖 ban/call/import/constructor 一族**（internal/rulegen/rulegen.go:32-38 的 Spec 字段）。
  也就是说"裁决器"已存在，缺的是**被裁决的规则类别**。
- 它直接放大 claims #5（执行组织约定）：约定不在你的 pretrain 里，这是唯一不会随模型变强而贬值的主张（claims #12）。

**建议机制**（最小可用）：`identifier` 规则族 = `节点类型 × name 正则 × package/layer 上下文`。
- 节点类型：class / method / field / parameter / constant / test_func；
- 上下文：所在层名 / 包名前缀（复用已有的 layer 匹配）；
- 正反例 fixture 沿用 `constraints rule` 的 `-bad`/`-good` 契约，**没有反例的规则不可信**这条不变。

**验收口径**：用 biz-spec-01 的 NAMING-01~06 写 6 条真实 rule spec，每条 ≥1 bad + ≥1 good fixture，
`metricgate constraints rule` 全 PASS 且合并进 policy 后 `check` 能在真实 Java 文件上命中；
误报率用 pilot 的 FP 审计口径（抽样 12 处归因）。

### D2 `-diff` 的价值棘轮对齐 + 遗留项清理

**现状**：`docs/backlog-v1x.md:301` 与 `:308` 两次记录同一项遗留——MCP `check_path -diff_only` 没有用 value-delta 棘轮，
与 CLI 的 `-regress` 不一致。代码层面同位可见（**本文初稿写的行号已过时，以下为 2026-10-16 复核值**）：
CLI 侧原先 `cmd/metricgate/regress_cmd.go:57` 用 `git.ArchivePaths` 做基线归档再跑第二遍引擎，
MCP 侧原先 `cmd/metricgate-mcp/checks.go:85` 只用 `engine.FilterSummary`（无基线值）。

> **【D2 已完成 2026-10-16，迭代日志占 `iter-20` 序号】**上述推定已坐实：基线取数抽成共享包 `internal/baseline`，
> CLI 走 `cmd/metricgate/check_cmd.go:166 baseline.Split`，**MCP 走 `cmd/metricgate-mcp/checks.go:84 baseline.Split`**
> ——同一函数、同一 value-delta 口径；`regress_cmd.go` 已缩到只剩 `applyRegressions`（131→35 行）。
> 两入口对测已写成**可执行测试**（`cmd/metricgate-mcp/parity_diff_test.go`：真编译两个二进制 + 真 stdio 握手，
> 覆盖 `root="."` 与子目录两种形状），并做了反向验证（摘掉 MCP 基线分支 → 测试报红）。
> 同一脏树实测：MCP 由 **3 条收到 1 条**，与 CLI 全等（多的 2 条是值未变的 fan-out）。
> ⇒ 本节**不再是候选**；`docs/backlog-v1x.md:301` / `:308` 已标 ✅。详 [iter-20 日志](../iterations/iter-20-mcp-cli-ratchet-parity.md)。
> 顺带挖到一个既有缺陷（不是两入口分叉，两侧同退化）：**子目录 root 跑 `-diff` 时 value-delta 静默全保留**，
> 因为 `engine.Run` 的 file 键相对 run 根、基线键相对工作树根，只在两者重合时才生效——见该日志 §五 / 遗留第 1 条。

**后果**：同一个"变得更糟但未过阈"的发现（claims #7 的核心），CLI 报、MCP 不报——而 agent 走的是 MCP。
这是"闸门在两个入口给出不同答案"，比性能问题更伤可信度。

**做法**：让 MCP 的 `-diff_only` 复刻 CLI 的 value-delta 口径（以 `metric_snapshots` 前后差值为判据，而不是只看是否触阈）。
这一个改动同时消掉 backlog 里两处 ⬜。

> **实现选了比"复刻"更硬的口径**：不写第二套，而是把 CLI 那一套抽到 `internal/baseline`，两侧各留一次 `baseline.Split`。
> 理由：两个入口对"什么算已存在"只能有一份代码；否则下一轮改动只会碰到其中一份，分叉会长回来。

**其余遗留项按"是否伤信任"排序**（均出自 [backlog-v1x.md](../backlog-v1x.md:300-323) 的 ⬜ 清单）：
1. `metricgate policy show` CLI 动词 + MCP policy 工具（agent 在改 policy 前得能读到生效配置——这是 claims #5 的前提）；
2. 让步系统问责化（inline directive 加 owner/reason/expiry + 过期告警）：让步是"允许破例"，没有过期日就等于无限期；
3. `internal/owners` 不认自定义 CODEOWNERS location（GitHub 官方支持该能力，我们却在默认路径上静默返回无 owner）；
4. `.bak` 无轮转上限（每次保存追加一份，是 cockroach 型问题，一次修完一劳永逸）。

### D3 规则可解释性的下一跳：把"为什么没报"变成产品能力（呼应 claims #6/#9）

claims #6（跨文件依赖倒置，编译器看不见）和 #9（一次改动对整图的影响）都依赖"全局视角"，
而 agents/人类都只在单文件视角工作。现有 `impact` / `reachability` / `map` 已经把图算出来了，
但 [backlog-v1x.md](../backlog-v1x.md) 记录 blast-radius 还没进闸门（impact-scope 加 tier 过滤）。

**方向**：让 `-diff` 报出"这次改动把哪些文件拖过了它们的阈值"（二阶后果），并与 M2/M2-B 已测出的
B 类架构失效模式（claims #18 ⚠️，3/3 有效对全部防住、最严口径 3/5 达线）合流成一类"架构波及"告警。

**验收口径**：构造/复用 `evaluation-m2-arch-results.md` 里的一个 B 类任务，`check -diff` 报出该改动的**波及文件清单**，
且清单与 `impact -q` 的 tier 过滤结果一致（同一内核，不重写）。

### D4 存储与保留（从被动项升级为 P1：它正在污染其他度量）

`.metricgate/history.db` 两轮采集 = **0.79 GB → 2.19 GB**（`du -sh .metricgate`），1018 runs / 8.7M snapshots，且无保留策略
（`runs.archived` 只隐藏不回收）。**本轮最关键的发现**：store 膨胀不是背景噪声，它会**直接改变其他度量的读数**——
第一轮 `check .` 1.36–1.88 s，第二轮同一命令 78.03 s，而 `explain .`（不跑规则、只选文件）仍是 1.59 s。
也就是说退化发生在规则执行 + 写库这一侧。

方向：按 run 数/天数的窗口保留 + `VACUUM` 或归档导出。**与 S1 无依赖关系**——S1 原先写的"前置依赖"已被反证
（关 store 后 `-diff` 90.45 / 98.27 s，比开 store 更慢 ⇒ 倍差不来自 store）。D4 仍要做，但它修的是**绝对基线**
（`check .` 35–51 s 的抖动）与磁盘占用（`.metricgate/` 现 2.3 GB），不再是 S1 的排期前提，可并行。

### D5 上下文生成器的质量而非存在性

`bundle` 已存在但默认预算在真实仓库上开箱即坏（2.3：需要 ≥ 11.2 M 字符装下"required constraints and diffs"）。
但方向不是单纯调大预算：`internal/facts/budget.go` 已实现 required/priority 两级准入与
`OrderHeader/Constraints/Diff/Map/Facts/Reuse/Symbols/Source` 的排序契约（该文件注释明确说这是为了对抗
Lost-in-the-Middle）。真正的问题是：**默认预算选错会让 required 项直接失败**——`budget_exhausted`
时它连 diff 都不给你。方向：要么默认预算按仓库规模自适应，要么在 required 超额时改为**降级交付**
（先给 diff + constraints，附 omissions 清单），把"失败"变成"部分成功 + 明示缺口"。

### D6 strategy 文档重写：从"市场空白"改到"三轴结构性事实"（S3 的落地动作）

**这一节补的是本文开头三条主张里 S3 一直缺的证据。** 前几版竞品文档把"没有同格竞品"当卖点，
2026-10 的检索已经把它推翻——**同格不是空白，而是已经出现了五个能逐条对上我们卖点的产品**。
以下是逐条取回一级来源后的对照（检索日期 2026-10-16，E4）：

| 竞品 | 它自己怎么说（原文关键句） | 对上的 metricGate 卖点 | 来源 |
|---|---|---|---|
| **SonarQube Vortex** | "performs Sonar's trusted **deterministic** analysis right inside your agent, **reusing the context from a previous CI analysis** to deliver results in record times" | 确定性 / 进 agent loop / 复用缓存降延迟 | [docs.sonarsource.com](https://docs.sonarsource.com/agent-centric-development-cycle/inside-your-agent-the-agentic-loop/sonar-vortex-analysis) |
| **SonarQube Claude Code 插件**（Anthropic marketplace） | "integrates SonarQube's security and code quality analysis directly into the Claude Code terminal environment for **real-time verification**"；"blocks over 450 secret patterns **before content enters the LLM context**" | 分发面（harness 集成）/ 上下文前置过滤 | [sonarsource.com](https://www.sonarsource.com/blog/now-available-sonarqube-plugin-for-claude-code/) |
| **Verificate Gate** | "**17 deterministic reality gates** ... The gates hold **veto power the model can't override**"；自述目标含 mock/placeholder、hallucinated dependency、bypass attempts | 确定性 / 闸门不可被模型绕开 | [verificate.ai](https://verificate.ai/gate/) |
| **Vivari Guard** | "weighing **blast radius, transitive impact, and behavioral hotspots** among its signals, **with no LLM in the call**. Runs standalone, **local-first**" | claims #9 二阶后果 / 本地 / 0 token | [vivari.ai](https://vivari.ai/guard/) |
| **Trustabl Agent Analyzer** | "deterministic, **SDK-aware** static analysis to the agent layer"；"runs entirely on your machine: **no cloud scanner, no account, no code upload, no LLM**" | 本地 / 确定性 / SARIF 2.1.0 输出 | [trustabl.ai](https://www.trustabl.ai/products/) / [github.com/trustabl/trustabl](https://github.com/trustabl/trustabl) |

**结论（改写法，不是改方向）**：

1. **不与 Sonar 打"分析深度"**。Vortex 明确复用 CI 缓存来压低延迟——说明"确定性分析 + 低延迟"这一格**已经有人占了**，
   metricGate 的差异只剩"规则是你的私有约定"（claims #5/#12）与"零基础设施"。
2. **不与 Verificate 打"闸门权威"**。"17 个 gate 有 veto power" 与我们的 gate 定位同格，但它的输入是 AI 输出、不是仓库代码。
3. **真正还空着的一格**：把**组织私有规则**（不是通用质量规则）做成同一套 policy、同一判定内核、多个消费面
   （pre-commit / harness hook / MCP / CI / cockpit）。这是上表五家都没在做的——它们的规则集是**产品的**，不是**你的**。
4. **可立即执行的短期动作**：把上表连同"六项不做"（§5）写进 strategy 文档，替换 positioning.md:264（HEAD 版）的空白论断。
   **✅ 已落地（2026-10-16 工作区，未提交）**：positioning.md 已升 v2.0、日期改为 2026-10-16，新增
   "2026-10 外部检索：同格竞品已经出现（取代\"市场空白\"论断）"一节（含五家一级来源表），
   并把 v1.0 的"互不重叠"表述标注为未经复核的边界。⇒ 本文这条从"待办"改为"已完成"，iter-23+ 里的 D6 只剩复核与补全。

> 取证边界：同轮检索中还出现过 GateTest（CI quality gate，宣称在 19 个固定仓库上每夜实测精确率并公开漏报）与
> Kete/taniwha.ai（把规则抽成可查询事实、在每次 tool call 前做 pre-flight gate）两家，但本文**未逐条取回一级来源**，
> 按 §1.2 的 E4 规则不引用其细节、不列入上表。它们是下一轮检索的第一批目标。

---

## 5. 明确不做（取舍与反证）

| 不做 | 理由（可反证） | 反证条件（什么情况下重开） |
|---|---|---|
| **autofix / codemod** | roadmap.md:183 明确不做；且会把"判定"与"执行"耦合，破坏 claims #10 的字节确定性叙事 | 只有当"修复建议采纳率"被度量出来且 >50%（five-properties 里唯一未闭环的指标） |
| **自建类型系统 / 数据流分析** | capabilities.md §11 既定边界；claim #2 已被证伪（编译器是免费确定性检查器，agent 会自修） | 除非出现轻量增量式方案（不是重造 javac/gopls） |
| **远程服务化（真 v2）** | 会破坏"本地、0 token、零环境依赖"的卖点（claims #11）；且 cockpit 已有 loopback 服务，市场叙事要重新划界 | 若出现明确的团队级多仓需求（≥3 个外部仓库要求同仓治理） |
| **语义级 AI code review** | pilot 13 条 ❌ 语义条款明说不可自动化；外部 head-to-head（gitee-scan-2.0-portal，5 commits）显示 LLM 抓的是语义正确性、metricGate 抓的是复杂度/耦合欠账——**互补** | 除非只想做"复杂度守卫"这一格，别去抢 LLM 的格子 |
| **调阈值以降低噪声** | claims #7 的实验结论是"需要数值棘轮"而非"放松标准"；放松标准会让闸门失去红线身份 | iter-10 已证明 31 error → 16 的棘轮路径可行 |
| **接入 hook 时改 hook 语义/权限模型** | 每个 harness 的 hook 权限模型不同（已知：Claude Code 的 `PreToolUse permissionDecision:"deny"` 对 MCP tool 不生效，issue #33106），去适配会陷进别人的 bug 周期 | 我们只用 `PostToolUse` + 自家 `check`，不依赖 deny 生效 |

---

## 6. 风险与反证

| 风险 | 量级 | 缓解 |
|---|---|---|
| S1 的批量化 `git diff` 重写破坏字节确定性 | ~~高影响~~ **已消**（树副本 5 连跑 md5 全同；与 HEAD 逐文件版 JSON body 叶差 0） | 遗留两条新风险：①patch header 形状依赖用户 git 的 prefix 配置 → 已用 `--src-prefix/--dst-prefix` 固定并加测试；②`Root()` memoization 在 worktree 移动后报告旧路径（只缓存非空答案，`git init` 之后可见） |
| "市场空白"叙事过期（S3） | 中影响、**已处置**（positioning.md 已升 v2.0 并换写三轴叙事 + 五家一级来源表，工作区未提交） | 剩余动作：提交这份改写，并逐条复核 v1.0 遗留的功能矩阵/性能表（SonarQube ~30 s / golangci-lint ~5 s / ArchUnit ~10 s / MetricGate ~0.3s、内存 500/100/200/50 MB 这类数字未标明口径与复测日期，属可信度尾巴） |
| 单仓库 dogfooding 证据被过度外推 | 高（叙事风险） | 所有从 mcp-calls.jsonl / history.db 得出的结论都注明"单仓库"，claim #15 保持 ⏳ 直到有跨仓库样本 |
| D1 标识符规则误报潮 | 中 | 强制 bad/good fixture 双契约（`constraints rule` 已强制 `-bad` 必填）；每种规则先 warning 观察期再提升 error |
| hook 方案在别的 harness 上不通用 | 中 | 先只发 Claude Code + Cursor（后者能直接读 Claude hook 配置）；Codex 侧的 control-effect 只对 sync hook 生效，先不接 |

---

## 7. 排期建议

> **2026-10-18 修订说明（必读）**：本节原表把未来编号预占到 `iter-23+`，但实际日志序号被
> `iter-24`（子目录 value-ratchet）、`iter-25`（存储保留）、`iter-26`（post-edit hook）、
> `iter-27`（人工交接阻塞项）、`iter-28`（cockpit 深色 + 保留落地）、`iter-29`（中期 backlog 清点）
> 消费掉了。**原表的 iter-21 / iter-22 / iter-23 三行从未作为迭代跑过**——它们的内容要么已在
> 别的迭代里顺带做完，要么仍未开始。所以下表不再用「迭代号」组织，而用「状态 + 下一轮」组织；
> 下一轮记作 `iter-30`。

### 7.1 原表逐行现状

| 原计划 | 内容 | 现状（实测） |
|---|---|---|
| iter-19 | ~~S1 `-diff` 批量化~~ | ✅ 已落地（282→8 进程） |
| iter-19 | D4 history.db 保留策略 | ✅ 已做（`iter-25` + `iter-28`：`retain` / `prune` / `-vacuum`，真实库 497.5 MiB → 121.5 MiB） |
| iter-19 | ~~S3 文档漂移~~ | ✅ 已改完 |
| iter-19 | `toolVersion` 的 ldflags 注入接发布管道 | ✅ 已完成（`.github/workflows/seal.yml:157` 的 `-ldflags "-X main.toolVersion=${version#v}"`） |
| iter-19 | **pre-commit 改 scoped check 根** | ❌ **仍未做**（`hooks/pre-commit:50` 是全树 `exec "$bin" check . -diff $archflags`），实测省 1.65–2.05 秒 |
| iter-20 | ~~S2 `settings.json` 样例发货~~ | ✅ **已发货**（`ec1455c`，`.claude/settings.json.example` + `hooks/post-edit_test.sh` 12 passed） |
| iter-21 | D1 标识符规则族（Java NAMING-01~06） | ❌ 未开始，仍是最大的一块 |
| iter-22 | ~~D2 MCP `-diff_only` 棘轮对齐~~ | ✅ 已完成（`iter-20` 日志 + `parity_diff_test.go`） |
| iter-22 | `policy show` verbs | ❌ 未开始（`cmd/metricgate/usage.go` 里**没有 `policy` 这个动词**，`main.go` 的 case 也没有） |
| iter-22 顺带 | 子目录 root 跑 `-diff` 时 value-delta 静默全保留 | ✅ 已定口径并实现（`iter-24` §2.1 选 C、§4 断言结清）。~~验收判据「`unchanged` 不再一个为 0」~~ 这说的是**修复前**的病灶形状，不是还欠的债 |
| iter-23+ | D3 blast-radius 进闸门 / D5 bundle 降级 / ~~D6 strategy 重写~~ | ⚠️ D6 已提交；D3/D5 未开始 |

### 7.2 本轮（iter-29）实际做完的五项

不依赖 CI 的待办已全部清掉，详见 [evaluation-iter29-remediation.md](../evaluations/evaluation-iter29-remediation.md)：

| 项 | commit | 类型 |
|---|---|---|
| TS/JS 模块级箭头函数算函数单元 | `760f498` | 度量正确性（同文件可维护性 19→51） |
| `test-no-assert` 认「拿到 `*testing.T` 的 helper」 | `ecd57b5` | 规则正确性（语料误报 55→19，本仓 8→0） |
| `.claude/settings.json.example` 发货 | `ec1455c` | 文档承诺兑现 |
| 规则目录中文到前端 | `f0b0a6e` | 可解释性（61 行全覆盖，7 个 id） |
| 运行历史记录触发命令 + 路径 | `c386b4a` | 可追溯性（含脱敏） |

### 7.3 iter-30 建议（下一轮）

| 优先级 | 内容 | 依赖 CI？ | 收口观测 |
|---|---|---|---|
| **P0** | **重跑 commit replay 标定 `test-no-assert`** —— 本轮改了判据，`evaluation-commit-replay-results.md` 的 215/289 与 `claims.md` #19 都成了旧判据数字 | 否 | 215 换成新判据数，或明标「旧判据上界」（已在两处就地标注） |
| **P0** | **C/C++/C# 等价失明排查** —— R1 修的是 JS 系，同类「kind-set 漏掉某种声明形态」在别的适配器里大概率还有 | 否 | 每语言一个「全 lambda / 全属性 / 全表达式体」fixture，FUNCS > 0 |
| P1 | `Benchmark*` 是否单开一档（R2 剩的 14/19 条都是 benchmark） | 否 | 定口径后本仓 + 语料各跑一次，写进 `docs/metrics.md` |
| P1 | 别名追踪（`tt := t`）—— R2 的已知精度下界 | 否 | 要么做，要么在规则文档明写不追 |
| P1 | D1 标识符规则族（原 iter-21，最大的一块未动） | 否 | `metricgate constraints rule` 6/6 PASS |
| P1 | `languageGroups` 有数据无消费者（R4 记下的观察） | 否 | 前端按语言分组，或删字段 |
| P2 | pre-commit scoped 根（原 iter-19 唯一遗留） | 否 | 同一脏树前后耗时中位数 |
| **等 CI** | P2 triage push 触发、P4 分支保护、T4.5 L8 | **是** | CI 停着时加的是不会执行的代码 |

> 排期逻辑改一句话：**先保证度量量到的东西是真的（P0 两项），再让闸门表达更多团队规范（D1）**。
> 原表那句「先快又一致」仍然对，但本轮发现了一个更前面的坑：**快而一致地量错，比慢更糟**。


---

## 8. 附：本文引用的可复现命令

```bash
# -diff 与整树对比（脏树，149 个变更路径）
time ./metricgate check .
time ./metricgate check . -diff

# bundle 默认预算是否开箱即坏
./metricgate bundle .

# CLI 动词数 / MCP 工具数（对 §2.2 的漂移）
./metricgate --help
grep -cE 'Name:\s+"[a-z_]+"' cmd/metricgate-mcp/tools.go

# 让步账本现状
cat .metricgate/accepted.yaml

# 版本真相与 update feed 现状（改后语义：默认无 feed，报 not_enabled）
./metricgate version
./metricgate cockpit update -format json -timeout 8s

# S1 的核心观测：git 子进程计数（改前 282，批量后 9，消 merge-base 后 8）
mkdir -p /tmp/mgshim && printf '#!/bin/bash\necho "$*" >> /tmp/mgshim/calls.log\nexec /usr/bin/git "$@"\n' > /tmp/mgshim/git && chmod +x /tmp/mgshim/git
: > /tmp/mgshim/calls.log; PATH=/tmp/mgshim:$PATH ./metricgate check . -diff >/dev/null
wc -l /tmp/mgshim/calls.log; sort /tmp/mgshim/calls.log | uniq -c | sort -rn | head

# 改后的落点（fillHunks 已被 attachHunks 取代）
grep -n 'func attachHunks' internal/git/changes.go
grep -n 'func splitPatchSections' internal/git/patch.go
grep -n 'rootCache' internal/git/git.go

# 字节确定性：必须在 store 关闭的树副本上比（开 store 时唯一差异是 /run/runId 自增）
rsync -a --exclude .metricgate/ --exclude node_modules/ ./ /tmp/mgfreeze/
cp .metricgate.yaml /tmp/mgfreeze/.frozen-nostore.yaml   # 把其中 store.enabled 改成 false
for i in 1 2 3 4 5; do (cd /tmp/mgfreeze && metricgate check . -diff -format json -config .frozen-nostore.yaml) | md5; done
```

---

*撰写时间：2026-10-16。本文为 v2 决策输入，不替代 [docs/roadmap.md](../roadmap.md)*
