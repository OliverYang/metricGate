# 迭代 6 · git 历史共变（change coupling / temporal coupling）

日期：2026-10-12（goal mv407zjq-jab3l6 task-t1）

## 为什么做这个

`internal/arch` 问的是「这条依赖方向合法吗」——它读 import 图。但**腐蚀发生在 import 图之前**：两个文件没有任何依赖关系，却每次都被一起改。分层规则此刻仍然全绿，边界在实践上已经没了。

这正是 backlog 候选 1 记录的能力洞：**补的是非语言洞（9 语言全覆盖），不是语言洞**，且天然适配 `-diff` ratchet（只对本次改动里的对报）。

## 交付物

| 交付 | 位置 | 说明 |
|---|---|---|
| 统一历史数据源 | `internal/git/history.go` | `History(dir, since string) ([]Commit, error)`；`Commit{Files []FileStat}`；`FileStat{Path, Added, Deleted, Binary}`；`IsShallow(dir) bool`。**一次 `git log --numstat` 同时供 churn 与 coupling 使用**，两个视图不可能对"什么变了"产生分歧 |
| 共变分析 | `internal/coupling/coupling.go` | `Analyze(commits, Options) Report`；`Pair{A,B,Count,Layers[2],CrossLayer}`；`Report{Pairs,SkippedSweeps,SkippedFiles,Shallow}`；`Options.CrossLayerOnly`（门禁只看跨层，CLI 看全量）；`MatchFile`（`**` 感知的文件 glob）；`PackageOf`、`LayerResolver(layers)`、`Summarize` |
| 热点重构 | `internal/hotspots/hotspots.go` | 删掉自己的 `exec` 与 `parseNumstat`，改走 `git.History` + 新 `churnOf`；新增 `Options.AllowShallow`；浅克隆默认拒绝 |
| 门禁 | `internal/engine/coupling.go` | `RuleIDCoupling = "change-coupling"`；`(*Engine).CouplingEnabled()` / `CouplingSince()`；`(*Engine).CouplingFindings(root, map[string][]diff.Range, history CouplingHistory) []model.Finding`；`CouplingHistory` 端口（`IsShallow() bool` + `Commits() ([]coupling.Commit, error)`）；`couplingRuleMeta(cfg)` 进 SARIF 目录 |
| 策略 | `internal/config/config.go` | `ArchConfig.CouplingExclude []string`（yaml `coupling_exclude`）；`ArchConfig.Coupling CouplingGate`（yaml `coupling`，字段 `Enabled/MinCount/Since/MaxCommitFiles/Severity`）；`(*Config).CouplingSeverity()` 默认 warning |
| CLI | `cmd/metricgate/coupling_cmd.go` + `main.go`/`usage.go` | `metricgate coupling [path]`（`-since -max -min-count -max-commit-files -format -allow-shallow -config`）；`gitCouplingHistory` 适配器把 `internal/git` 接到 `engine.CouplingHistory` 端口；`-diff` 管线在 `diffExtras` 里追加 `applyCouplingGate` |

## 关键设计决策（含理由）
- **阈值是 `-max-commit-files` 不是硬编码。** 默认 12：pilot 仓实测 median files/commit = 1，12 远高于正常提交、远低于整仓 sweep。超阈值的 commit 整条丢弃并计入 `SkippedSweeps`。
- **`CrossLayer` 要求两侧都有层且不同。** 未归属文件是"未知"不是"越界"——否则任何根目录文件都会伪装成边界穿越。
- **`-diff` 只报两侧都在变更集里的对。** 门禁的单位是"这次改动"，仓库里其它既存耦合不是这次的问题；这也让检查天然是 `-diff` 形状。
- **浅克隆显式拒绝，且要出声。** `hotspots.Compute` 报错退出；`coupling` 命令报错退出；`-diff` 门禁在 stderr 打印 `coupling: skipped — shallow clone, git history is truncated (the gate would undercount)`。**沉默的 0 findings 会被读成"没有耦合"**，而真正能显示它的历史恰好缺失。
- **`arch.MatchesAny` 不能用于文件。** 它是包名匹配器（segment 语义，见 `internal/arch/pattern.go`）。第一版直接复用它导致 `**/*.sum` 完全不生效（`TestAnalyzeExcludesGeneratedAndMirroredFiles` 当场抓到）；改为 `internal/coupling` 内的 `MatchFile`（`**` 递归 + `path.Match` 每段）。
- **方言镜像不进硬编码默认值。** pilot 仓 top-1/2/5 是 `mysql/mapper/*.xml` ↔ `pgsql/mapper/*.xml` ↔ `dmsql/`。这是**项目特定**噪音，靠 `architecture.coupling_exclude` 表达（实测加 `**/resources/*sql/**` 后 570 个文件被忽略、真信号上浮）。默认只收 lock/生成/`application-*.yml` 镜像这类通用噪音。
- **`coupling` 包不 import `internal/config`**，用 `LayerOf func(string) string` 注入 + CLI 侧 `coupling.LayerResolver(cfg.Architecture.Layers)`；保持与 hotspots 同级的薄适配层风格。
- **`engine` 不 import `internal/git`（端口在 CLI 侧接线）。** 本仓分层规则是 `core` 不得依赖 `adapter`，而 `internal/git` 属于 adapter（它 shell out 到进程，core 的定位是纯库）。第一版直接在 `engine.CouplingFindings` 里调 `git.History`，自检当场报 `internal/engine/coupling.go:22 layer-violation (core may not depend on adapter: internal/git)`；改为声明 `engine.CouplingHistory` 端口，由 `cmd/metricgate/coupling_cmd.go` 的 `gitCouplingHistory` 实现并注入（测试侧用 `couplingHistory`）。
- **`CrossLayerOnly` 让 CLI 与门禁共用一份分析。** 门禁只关心边界穿越（`Options.CrossLayerOnly = true`），`coupling` 命令要看全量排序（跨层只是标注）。用选项而不是两套调用路径，保证两边对「哪对算跨层」的判断永远一致。
- **`FileStat.Binary`**：`--numstat` 的二进制行是 `-\t-\tpath`，无法 parse 成数字。第一版把这类行整行丢掉，导致"只改图片的 commit"消失；现在保留为 `Binary: true` 但不计 churn（热点评分语义不变）。

## 实测证据

**pilot 仓 `/Users/mac/Downloads/main/gitee-scan-2.0-portal`（5636 commits，非浅克隆）**

- 与 backlog 记录对得上：`-min-count 8` 得到 **230** 对；同一脚本（不排 lock/镜像）得 **240** 对 —— 正是候选 1 记录的 240。差值 10 对来自 `package-lock.json` 之类被默认排除。
- `-since "1.year ago" -max 15` 耗时 **1.28s**（`git log --numstat` 全量 4.50s 的同一份数据）。
- 真信号样例（加 `coupling_exclude` 后上浮）：
  - `BugSecurityStyleIndexDao.java ↔ es/impl/BugSecurityStyleIndexDaoImpl.java` 13×（接口↔实现，AST 图上无直接边）
  - `OpenApiMsController.java ↔ OpenApiMsServiceImpl.java` 12×
  - `ModuleServiceImpl.java ↔ resources/mysql/mapper/scan/ModuleMapper.xml` 13×（**Java↔XML 跨语言耦合**）
- 噪音样例（被默认过滤）：`mysql/mapper/scan/RuleMapper.xml ↔ pgsql/mapper/scan/RuleMapper.xml` 30×、`application-dev.yml ↔ application-poc.yml`。

**本仓（metricGate，76 commits）**：`-min-count 2` 出现 `internal/engine/engine.go <-> internal/facts/render.go [cross-layer: core -> adapter]` 3× —— 跨层共变真实存在于本仓（core 调 adapter 渲染层），与 `arch` 的 SDP 告警同源但视角不同。

**浅克隆护栏**：`git clone --depth 5` 后 `coupling` 退出 2 并给出可操作提示；`-allow-shallow` 时在 stderr 打印 `warning: shallow clone — co-change counts are undercounted` 并正常出结果。

**门禁端到端（/tmp/coupgate，7 commits 的跨层对）**：`check . -diff -base HEAD` 输出 `coupling: 1 cross-layer co-change pair(s) in this change` + `cmd/cli.go:1: [error] change-coupling (changed together in 7 commits across the cli -> core boundary ...)`，gate FAILED、**退出码 1**；把 `architecture.coupling.enabled` 去掉后同一改动不再出现该 finding（0 处提及）。

## 单测

- `internal/coupling/coupling_test.go`（17 个）：成对计数、无序对、sweep 丢弃与计数、默认+项目排除、min-count 下限、同 commit 重复路径不自我配对、排序（count 降序后路径升序）、Max 截断、CrossLayer 四种组合（跨层/同层/一侧未归属/两侧未归属）、无 resolver 时不打标、**`CrossLayerOnly` 只留边界穿越**、`PackageOf`、resolver 首个匹配优先、`Summarize` 打标格式。
- `internal/engine/coupling_test.go`（8 个，**纯 fake，不建真实 git 仓，每个 0.2s**）：跨层对进变更集报 1 条、只改一侧不报、策略未启用不报、未接端口不报、同层不报、**浅克隆不报**、低于 `min_count` 不报、`couplingRuleMeta` 仅在启用时进目录。
- `internal/git/history_test.go`（6 个）：按 commit 分组（含纯二进制 commit）、空块丢弃、畸形 stat 行矩阵、真实小仓 `History`（最新在前、行数正确）、非 git 目录报错、`IsShallow` 在完整仓为 false。
- `internal/hotspots/hotspots_test.go`：`TestParseNumstat` → `TestChurnOfFoldsCommitStats`（改走 `git.Commit` 结构）。

## 五性核对

| 性 | 结论 |
|---|---|
| 可介入 | ✅ 腐蚀在"分层规则仍全绿"时就能被抓到；`coupling` 命令让人在看第一眼排序后再决定是否开闸 |
| 可审计 | ✅ 每条 finding 带共变次数、两侧层名、两侧全路径；被丢弃的 sweep/生成文件都有计数（`SkippedSweeps`/`SkippedFiles`） |
| 可度量 | ✅ `-format json` 给 CI/agent；本仓跨层共变 3× 可直接复算；pilot 仓 230 对与 backlog 记录的 240 对可对齐验证 |
| 可回溯 | ✅ 与 `hotspots` 共用 `git.History` 一次数据源；排除项是 policy 声明而非硬编码；浅克隆拒绝而非静默偏小 |
| 可观察 | ✅ `-diff` 输出 `coupling: N cross-layer co-change pair(s)`；浅克隆打印跳过原因 |

## 遗留

- rename 未跟随（`--no-renames`）：改名会让共变计数断掉，也让 churn 把删除路径单独计。策略待定。
- 共变对目前是**报告/门禁**，不落库；历史趋势（某对耦合是否在加剧）未做。
- `SkippedFiles` 只统计"在提交里且被排除"的文件数，不区分具体命中哪条规则。
- 跨仓共变（多仓 monorepo 之外的场景）不在范围内。
