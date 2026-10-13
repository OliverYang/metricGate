# 迭代 10 · -diff 棘轮改用数值 delta（把「碰了」和「变差了」分开）

日期：2026-10-12（外部工具对拍实验的直接产物）

## 为什么做这个

和 `codex review` 做对拍时发现 `-diff` 棘轮会过度上报。选一个真实提交
（gitee-scan-2.0-portal `4b5ca7ead2`，给两个方法加了 3 行 null 保护）：

```
MetricGate: 14 new findings / 10 errors，gate FAILED
codex review: no findings（判定为纯防御性改动）
```

逐条对比提交前后的实际数值，10 个 error 里只有 7 个数值真的动了：

```
cbo of "OpenApiMsServiceImpl"   189 → 189   UNCHANGED
lcom of "OpenApiMsServiceImpl"  769 → 769   UNCHANGED
nom of "OpenApiMsServiceImpl"    54 →  54   UNCHANGED
wmc of "OpenApiMsServiceImpl"   478 → 482   WORSE
taskIssues cognitive             30 →  33   WORSE
taskIssues cyclomatic            17 →  19   WORSE
...
```

根因：`-diff` 判定「新 finding」的依据是**改动 hunk 落在 finding 的行区间内**，
而类级指标的行区间是整个类体（`OpenApiMsServiceImpl` 跨 L77–2868）。
改 L2136 的任意一行，整个类的五个 CK 值全部重新上报 —— 数值一字未变。

`-diff` 能回答「你碰的这块有没有超阈值」，回答不了「这个数字是不是你弄高的」。
前者的答案在存量债重的仓库里几乎是恒真的。

## 交付物

| 交付 | 位置 | 说明 |
|---|---|---|
| 数值 delta 过滤 | `internal/engine/filter.go` | 新增 `FilterSummaryWithBase` / `MetricKey` / `BaseValues` / `IndexBaseValues`；`FilterSummary` 变成 base 为空时的薄封装 |
| 部分归档 | `internal/git/git.go` | `ArchivePaths(dir, ref, paths)` 只归档改动涉及的文件；`treePaths` 用 `ls-tree` 预筛（`git archive` 遇到一个不存在的 pathspec 会整体 abort） |
| 坐标换算 | `internal/git/git.go` | `RepoRelative(checkRoot, path)` + 抽出 `checkBaseDir`（含 EvalSymlinks 规范化） |
| base 快照 | `cmd/metricgate/regress_cmd.go` | `touchedMetricSnapshot` 只对改动文件跑 base 分析 |
| 接入 | `cmd/metricgate/main.go` | `runDiffMode` 先取 base 值再过滤，header 增加 `, N unchanged metric(s) dropped` |
| 单测 | `internal/engine/filter_test.go`、`internal/git/changes_test.go` | 4 + 3 个 |

## 关键设计决策

**用「scope 名字」而不是行号做 base 的 join key** —— 一个函数下移 200 行
仍是同一个 scope，用行号会误判成新代码并保留。

**新 scope（base 里没有）一律保留** —— 新代码跨过阈值正是闸门存在的理由。
只有「base 里存在且数值未变」才丢。

**两个计数分开报** —— `pre-existing hidden`（没碰）和 `unchanged metric(s)
dropped`（碰了但没变差）是关于一次改动完全不同的事实，合并会把这次修的东西藏起来。

**没有 base 时降级而非报错** —— MCP `check_path` 走内存路径拿不到 base 树，
`base == nil` 时退回纯行区间语义。丢掉一条真实回归比多报一条未变的更糟。

**只归档改动的文件** —— 整树归档等于对上千个文件做第二次全量分析，只为回答
几个文件的问题。实测：整树 `-regress` 12.97s → 部分归档 9.40s（含全树主分析）。

## 五性核对

| 维度 | 结论 | 证据 |
|---|---|---|
| 可介入 | ✅ | 零配置改动；`-diff` 语义直接变准，无需用户做任何事 |
| 可审计 | ✅ | header 显式报出 `N unchanged metric(s) dropped`，被丢掉的数量可见 |
| 可度量 | ✅ | 5 个真实提交：error 31 → 16；其中 1 个提交 4 error → 0 |
| 可回溯 | ✅ | 每个保留的 finding 都对应 base 快照里的一个真实数值；`git archive` 保证比的是已提交状态 |
| 可观察 | ✅ | 对拍实验的 15 条 UNCHANGED 与本次丢掉的 15 条完全对应 |

## 实测证据

```
commit        新语义前 errors   新语义后 errors
4b5ca7ead2          10               7
16c289c48a           4               0
2ca4698cd4           9               3
6d3664373d           3               2
d2575a780f           5               4
合计                31              16
```

`16c289c48a` 从 4 error 变 0 error，与 codex review 的 "no findings" 一致。

## 单测

- `TestFilterSummary`（原有，验证无 base 时行为不变）
- `TestFilterSummaryDropsUnchangedMetricValues`（cbo 未变被丢，wmc/cognitive 上升被留）
- `TestFilterSummaryKeepsNewScopes`（base 里没有的 scope 保留）
- `TestFilterSummaryNilBaseKeepsEverything`（降级路径）
- `TestArchivePathsSkipsFilesAbsentAtRef`（新增文件不拖垮归档）
- `TestArchivePathsWithNoSurvivingPathsIsEmptyNotAnError`（全新增 → 空，非错误）
- `TestRepoRelativeMapsCheckRootOntoWorktree`（子目录 root 的坐标换算）

## 验证

- `gofmt -l internal/ cmd/` 干净
- `go build ./...` 通过
- `go test ./internal/... ./cmd/... -count=1`：仅 pre-existing
  `internal/digest TestBuildTaskFilteredBans`（digest_test.go:389）失败
- 仓自检 `check . -diff -base HEAD`：0 errors / 104 warnings / 17333 snapshots，gate PASSED
- 单文件检查 `filter.go` / `regress_cmd.go` / `changes_test.go`：0 errors 0 warnings

## 遗留

- 行区间判定仍然存在：一个**没碰**的 scope 如果数值上升，`-diff` 不会报它。
  这是刻意的（「你碰的才归你」），但意味着跨文件的连锁退化仍不可见。
- `-regress` 现在是冗余的：它的数值 delta 信号已经进了默认过滤，但保留因为它
  额外覆盖「没跨阈值但数值上升」的测量（`metric-regression`，warning 级）。
  两者共享 `regress.Compare` 的语义，长期看应合并。
- MCP `check_path -diff_only` 仍走纯行区间（内存路径无 base 树），因此 MCP 侧
  的噪声比 CLI 高。补齐需要 MCP 也做归档，成本是每次调用多一次 git 操作。
- base 分析用的是同一份 policy.yaml：如果本次改动同时改了阈值，比较的是新阈值
  下的旧代码，而不是旧阈值下的旧代码。未测。
