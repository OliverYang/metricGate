# 迭代 11 · impact 传递闭包 + 可达性报告（把「谁在用」和「没人在用」分别补上）

日期：2026-10-12（goal mv407zjq-jab3l6，计划 A+B）

## 为什么做这个

上一轮（迭代 10）用与 codex review 的对拍把 `-diff` 的**纵向**信号修准了：
「这个 scope 有没有变差」。对拍同时暴露了它看不到的另一半 —— **横向**信号：
一个函数变差之后，谁依赖它。

先量化了「现在缺多少」。在 gitee-scan-2.0-portal（1190 个文件、
80858 条 call ref、4411 个有调用者的符号）上做单跳 vs 二跳闭包对比：

```
单跳 vs depth-2：80.2%（3538/4411）的符号 depth-2 看得更多
受影响文件均值：hop1 3.4 个 → depth2 30.7 个（9.0×）
```

也就是说原先 `impact -q X` 的回答里，五分之一强的情况是**漏报**，而不是
「准」。这和九语言拉齐时抓到的「静默零」是同一种失败形态：没报不等于没问题。

反向的另一面同理：没人引用的符号，`impact` 之外没有任何出口。于是本轮两件：

| 计划 | 内容 | 定性 |
|---|---|---|
| A | `impact -depth N`：反向传递闭包 + 三层可信度 + 宽度上限 | 新能力，默认行为不变 |
| B | `reachability`：无人引用候选 + 无测试触达，**只做报告，不进闸门** | 新出口，零门禁风险 |

## 交付物

| 交付 | 位置 | 说明 |
|---|---|---|
| 传递闭包 | `internal/impact/impact.go` | 重写；`Compute` 变成 `ComputeWith` 的薄封装，默认输出逐字节不变 |
| 选项与层 | 同上 | `Options{Depth, MaxAffectedFiles}`；`Reach{Name, Depth, Tier, Files}`；`TierResolved/Ambiguous/External` |
| CLI 入参 | `cmd/metricgate/impact_cmd.go` | `-depth` / `-max-affected` 两个 flag + 分层渲染 `printReach` |
| MCP 入参 | `cmd/metricgate-mcp/main.go` | `impactIn` 增加 `Depth` / `MaxAffected`，与 CLI 同一函数，不会漂移 |
| 可达性分析 | `internal/reachability/reachability.go` | 新包：`Collect(files, uses, opts) Report` |
| 可达性 CLI | `cmd/metricgate/reachability_cmd.go` | 新 verb + dispatch + usage 帮助行 |
| 策略感知收集 | `internal/facts/symbols.go` | `CollectSelectedWithRefs`：带 policy `exclude` 的 facts + 双索引一遍走 |
| 单测 | `internal/impact/impact_test.go`、`internal/reachability/reachability_test.go` | 6 + 6 个 |

## 关键设计决策

**默认深度 1，且字段用 `omitempty`** —— 新旧输出必须逐字节相同，
否则所有既有脚本和 LLM 调用方的解析会静默错位。`Depth`/`Reach`/
`HiddenAffectedFiles` 只在 `Depth > 1` 时出现。

**可信度分层而不是一个深度数字** —— 上一轮实测：callee 名字唯一解析率只有
14.5%（bugbye-parser）/ 6.2%（metricGate），其余是 0 定义或多定义。所以每个
reach 条目带 tier：1 定义 = resolved，多定义 = ambiguous，0 定义 = external。
一个 `-depth 3` 打印出的名字里有三分之二可能是巧合同名的，
这个比例必须写在输出里，而不是藏在文档里。

**宽度上限卡在 level() 内部** —— 一层展开时发现文件数超限就让整层停掉
（`stopped = true; return nil`），而不是「先做完再截断」。上一轮实测 13.1% 的
符号会爆（最深的 341 个受影响文件），不设限就是 DOS 自己。默认 25 个文件，
与 `gatecheck.DefaultMaxAffectedFiles` 同值，语义一致。

**种子名写入 visited** —— 环（A 调 B、B 调 A）不会把种子重新加回来。

**`reachability` 是报告不是闸门** —— 一个没有 AST 引用的符号，可能是死代码，
也可能是动态派达的。测出来的数（metricGate 3%、bugbye-parser 19%、
portal 5%）在没有覆盖率数据校准前，进闸门就是制造误报墙。

**report 类 verb 读 policy `exclude`** —— `facts.CollectWithRefs` 故意「全收」，
那是给 `constraints digest` 这类要全局视角的调用用的。但一个**报告数字**
（无人引用多少个、占百分之几）如果混进了沙箱副本，分母和分子同时被污染，
而这个污染只发生在有 `.data` 的仓库上 —— 正是最需要它的仓库。所以
impact / reachability 改用 `CollectSelectedWithRefs`，与 gate 用同一套
selection，「报告的范围」和「闸门的范围」从此是同一件事。

**跳过多定义名** —— 同名有多个定义时，这个定义无法被证明无人引用
（被引用的可能是另一个）。宁可不报，不猜。

**测试触达用共享谓词** —— `testFileSet` 用 `internal/sources.IsTestPath`，
不私有复制一份，否则「什么是测试」会出现两套答案。

## 实测证据

真实仓库 dogfood（`/tmp/mg-self`，policy.yaml；MetricGate 只统计仓库自身，`.data` 副本已排除）：

第一次跑的时候，MetricGate 自己的数字是 `4595 defs / 174 unref / 2270 untested`。
Grep 复核样本候选时发现 20 个来自 `.data/m2b-raw/...` 的沙箱副本 ——
`facts.CollectWithRefs` 不读 policy 的 `exclude`，把仓库里别人跑的产物一起
收进了索引。修法不是给 reachability 开特例，而是让 report 类 verb 走
`CollectSelectedWithRefs`（与 gate 同一套 selection）。修完后同一个仓库：

```
仓库                            defs   unref   比例   untested
MetricGate（默认 exported=true）  2471    511    20%     1232
MetricGate（-exported=false）     2471    608    25%     1232
bugbye-parser                    861     172    19%      657
gitee-scan-2.0-portal          12179     678     5%     3902
```

数字变了不是因为「发现了更多死代码」，而是因为分母终于对：2471 是仓库
自身的定义数，4595 里那 2124 个是 `.data` 副本。511 个里有 500 个在
`_test.go` —— 测试函数没有任何调用者，这是它们的正常形态，读者需要自己
分辨这一点（报告没有替人判断）。`-exported=false` 的 608 个里有 107 个
非测试项，抽样 grep 复核：未被引用即无 AST 调用点，属实。

A 侧，bugbye-parser 的 `BaseToolReport`（迭代 9 已修复它能被看见，但只到单跳）：

```
默认（depth 1）   1 def / 1 call / 36 type use / 25 affected files
-depth 2 -max-affected -1
                 1 def / 1 call / 36 type use / 40 affected files
                 depth: 2 — reached 35 further name(s)
                 无 cap 提示（-1 = 不限）
-depth 2（默认 cap 25）
                 note: 12 affected file(s) past the width cap were not expanded
```

`-depth 3` 只打印 depth-2 的条目：第 2 层就触顶停住，不会静默继续走。

MetricGate 自身的 `impact -q Compute -depth 2 .`：

```
6 定义 / 51 调用 / 25 个受影响文件（触顶）
depth: 2 — reached 9 further name(s)
note: name-level result unreliable: the seed has 6 definitions, so the
      closure mixes unrelated symbols; 4 affected file(s) past the width cap
      were not expanded
```

—— 种子有 6 个定义时，闭包把 6 个同名函数混在一起，这句话必须出现在输出里。

默认路径（`impact -q ComputeWith .`，不带新 flag）：1 定义 / 3 调用 /
3 个受影响文件，**没有 depth 行、没有 reach 段**，与迭代 10 的输出逐字节相同。

## 单测

impact（7 个）：
- `TestComputeDefaultDepthIsUnchanged` —— `Options{}` 与 `Options{Depth:1}`
  都与 `Compute` DeepEqual，单跳不带 Depth/Reach
- `TestComputeWithDepthReachesCallersOfCallers` —— main→entry→leaf 链条，
  depth 2 只 reach 到 main，affectedFiles == [a.go, c.go]
- `TestComputeWithDepthTiersEveryReachedName` —— 1/2/0 定义 →
  resolved/ambiguous/external
- `TestComputeWithDepthStopsAtTheWidthCap` —— Depth 4 + cap 1 只留 1 个文件且
  note 非空；cap -1 不隐藏；默认 cap 不动 3 文件链
- `TestComputeWithDepthLabelsAnAmbiguousSeed` —— 2 定义 → note 含 '2 definitions'
- `TestComputeWithDepthReportsPerHopFiles` —— `Reach.Files` 是本跳自己的调用点文件（顶层 scope 为空，而 `affectedFiles` 仍覆盖全部跳），见下一节

facts 收集层 + reachability（7 个）：
- `TestCollectReportsUnreferencedAndUntested`（含 `TestReached == Definitions - UntestedCount`）
- `TestExportedOnlyNarrowsCandidates`
- `TestCollectSkipsAmbiguousNames`
- `TestCollectCapsListsButNotCounts`（列表截断，计数不截）
- `TestCollectOnEmptyTree`
- `TestDefsFlatteningIsHonest`（members 会作为独立定义出现）

## 补充修复：`Reach.Files` 的语义（C/C++ 也和其他语言一致）

第一版交付后按用户要求逐语言复核，发现 `Reach.Files` 有三种不同表现：
C/C++ 为空、其他语言带本跳文件、顶层 scope（`main`/`tmain`/`M`）为空。
根因不是 C/C++ 的 `field_expression` 太窄，而是**这个字段的语义没定清**：

```
更新的语义：Files = 这个名字「自己」的调用点所在文件，不是走走到这里的累积集合
```

顶层 scope（`main`、测试函数）没有任何调用者，所以它的 `Files` 天生是空的；
把它填成累积集合会让「闭包已经停止扩展」看起来像是真的，而其实没有。
C/C++ 与其余六种语言在同一语义下行为一致（受控 fixture `/tmp/xlang`，
9 语言 `entry→mid→leaf` + 顶层入口，逐语言断言）：

| 语言 | depth-2 reach | depth-2 `Files` | depth-3 顶层 |
|---|---|---|---|
| Python / TS / JS / C# / Rust / C++ / C / Go / Java | 上一层调用者 | 有文件 | 空（无调用者） |

新增回归测试 `TestComputeWithDepthReportsPerHopFiles` 钉住这个语义
（mid 带 `b.go`，top 为空，而 `affectedFiles` 仍覆盖两跳）。

## 五性核对

| 维度 | 结论 | 证据 |
|---|---|---|
| 可介入 | ✅ | 默认行为零变化；`-depth 2` 显式开启；reachability 不进闸门 |
| 可审计 | ✅ | tier 逐条标注；种子多定义与宽度上限都写进 note；截断数量 `HiddenAffectedFiles` 可见 |
| 可度量 | ✅ | 80.2% 符号 depth-2 看得更多；7×–9× 均值放大；3 个真实仓库的 unref 比例实测 |
| 可回溯 | ✅ | 每个 reach 名字都来自 `refs.Index` 的真实 `Enclosing` 字段，可 grep 复核 |
| 可观察 | ✅ | cap 剩余量、ambiguous 种子、untested 下界都在输出里，不留隐藏状态 |

## 阈值纪律（本迭代被自检抓到 4 次，全部当场拆函数）

| 位置 | 问题 | 处理 |
|---|---|---|
| `impact_cmd.go` cmdImpact | fan-out 14 | 抽出 `impactFlags` + `parseImpactFlags` |
| `impact_cmd.go` printImpact | cognitive 16 / cyclomatic 14 / npath 1296 | 拆 `printImpactJSON` / `printImpactTail` / `printReach` |
| `reachability.go` Collect | cyclomatic 11 | 拆 `unusedCandidates` / `untestedCandidates` |
| `impact.go` 三个 Type-2 clone | affectedFiles / enclosingNames / fileSet | 合并为泛型 `uniqueSorted[T]`（随后 `fileSet` 与 `affectedFiles` 又重复，再合为一个） |

## 验证

- `gofmt -l internal/ cmd/` 干净
- `go build ./...` 通过；`/tmp/mg-self` 已重建
- `go test ./internal/... ./cmd/... -count=1`：仅 pre-existing
  `internal/digest TestBuildTaskFilteredBans`（digest_test.go:389）失败
- 仓自检 `check . -diff -base HEAD -config policy.yaml`：0 errors / 104 warnings /
  17702 snapshots，1 条被 `metricgate:ignore` 抑制，gate PASSED rc=0
- 每个新文件单独走 MCP `check_code`：impact.go / impact_cmd.go /
  reachability.go / reachability_cmd.go / reachability_test.go 全部 0 errors 0 warnings

## 遗留

- ~~`affectedFiles` 与 `fileSet` 在泛型化后完全相同~~ —— 已合并为一个
  `affectedFiles`（`fileSet` 删除，`Reach.Files` 改调前者）。
- ~~reachability / impact 不读 policy `exclude`~~ —— 已修：report 类 verb 改走
  `facts.CollectSelectedWithRefs`，与 gate 同一套 selection。MCP `get_impact`
  与 `gatecheck` 的调用点仍用全收语义（它们要全局视角），这是有意的分工。
- `gatecheck` 的 impact-scope 仍保持单跳。它的 `DefaultMaxAffectedFiles` 是按
  单跳标定的，直接用深度 N 会让带爆炸符号的仓库几乎每个提交都失败。
  blast-radius 进闸门需要单独一轮，且必须带 tier 过滤。
- MCP `check_path -diff_only` 仍走纯行区间（迭代 10 遗留，本轮未触及）。
- 测试触达率是结构下界：portal 只有 0.1%，不是因为没测试，而是 HTTP
  mock 测试和名字级解析都看不见那些路径。这个数只能当排序信号，不能当覆盖率。
