# 迭代 17 · 把审计结果路由到人（CODEOWNERS 归属列）+ trends 可用性（-out / -since / -project）

日期：2026-10-13（v0.2 需求排期的 T3 + T4，ref docs/research/v02-requirement-schedule.md:60-61）

## 为什么做这个

两条线共同的前提是：**工具的答案已经足够准，但没有人能把答案送到正确的收件箱。**

**T3 — 审计结果没有归属。** `rules gaps` 能列出「哪些规则没被 policy 武装」，
`reachability` 能列出删除候选项，`impact` 能算爆炸半径——但三个动词都停在
「谁运行谁看」。一个仓库的真实约束不是「没人知道」，而是「知道的人不负责，
负责的人不知道」。CODEOWNERS 早就写在 `.github/` 里，metricgate 却一次都没读过，
于是每次审计都要人工再问一句「这部分归谁」。

代价的对照很清楚：规则已经有一整套文件路径词汇
（`internal/rules/banned.go` 的 `fileMatches`：精确 / 前缀 / 后缀 / 整路径 glob /
基准名 glob / `**/` 任意深度），CODEOWNERS 是同一套语义的另一种写法——**已经付了
两遍成本**（一次给规则，一次给 Git），却没有任何一条通路把两遍用上。

**T4 — trends 只是 stdout 的一句话。** `trends` 之前只有 `-config/-runs/-format/-metrics`
四个旗标：报表打到终端，`head` 一下就没；窗口固定为「最近 20 条」，
无法按时间范围或按项目过滤。后果是「这个仓库最近是不是变好了」这个问题，
只能在 CI 里重定向，且**多仓库共用同一个 history.db 时（Cockpit 的项目选择器就是
这个场景）无法单独看一个仓库**——`.metricgate/history.db` 里存着所有项目，
report 却是全部项目的合数。

## 交付物

| 交付 | 位置 | 说明 |
| --- | --- | --- |
| CODEOWNERS 解析 | `internal/owners/owners.go` | `DefaultPaths = [".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"]`；`Find(root)` / `ParseFile(text, rel)` / `OwnerOf` / `OwnersOf` / `Label` / `UnknownShare` / `Owners` / `Empty` / `Describe`；`Unknown = "unknown"` 常量 |
| 归属列（reachability） | `cmd/metricgate/reachability_cmd.go` | `-owners` 旗标：`printReachabilityWithOwners` 每行加 owner 列 + 一行「谁都没认领」计数 |
| 归属列（impact） | `cmd/metricgate/impact_cmd.go` | `-owners` 旗标：`printAffectedByOwner` 把受影响文件按 owner 分组，未匹配显式 `unknown` 组 |
| 归属分组（rules gaps） | `cmd/metricgate/rules_gaps.go` + `internal/policygap/ownership.go` | `-owners` 旗标 + `Report.Owners []OwnerGroup` / `Report.Unattributed`；无 CODEOWNERS 时明说「无法路由」，而不是打印空节 |
| 解析测试 | `internal/owners/owners_test.go` | 16 个用例（见下） |
| 归属分组测试 | `cmd/metricgate/rules_gaps_test.go` | `TestRulesGapsRoutesByOwner` / `TestRulesGapsWithoutCodeownersDoesNotRoute` |
| store 窗口 | `internal/store/window.go` | `Window{Limit, Since, ProjectRoot}` + `runFilter()` / `metricFilter()` / `String()` |
| store 查询收敛 | `internal/store/store.go` | `Report(limit)`→`ReportIn(Window)`、`SeverityTrend`/`RuleFrequency`→`…In(Window)`、`LatestRunsIn`、`ReportIn` 填 `TrendReport.Window` |
| 指标趋势收敛 | `internal/store/metritrend.go` | `MetricTrend(limit)`→`MetricTrendIn(Window)`，runs 子查询先 `runFilter()` 再 `LIMIT` |
| CLI 窗口 | `cmd/metricgate/trends.go` | `-out`（写文件）+ `-since`（RFC3339 / 裸日期 / 日期+时间，本地时区）+ `-project`（仓库根或仓库名后缀） |
| 项目根解析 | `internal/store/store.go` `ProjectRoot` | 新增 canonical 比较：macOS `/tmp/x` 与记录的 `/private/tmp/x` 必须相等，不能把正确路径当「不存在」 |
| 单测 | `internal/store/store_test.go` | `TestProjectRootResolvesASymlinkedSpelling` / `TestProjectRootRefusesAnUnknownRoot` |

## 关键设计决策（含理由）

**1. owner 列是 opt-in（`-owners`），不加默认列。** 理由是两条相反的：
- 加为默认：一个没有 CODEOWNERS 的仓库每行都打 `unknown`，把一个可选的
  路由信息变成一片噪音，读者学会忽略这一列；
- 不加：已经写了 CODEOWNERS 的仓库（GitHub 早已推荐）永远不知道 metricgate
  支持它。
opt-in 让两种仓库都得到正确答案，代价只是多打一个旗标。**JSON 输出不加 owner
字段**：机器消费方自己就能做归属，把它塞进 schema 会让 `-owners` 从「路由视图」
变成「输出格式变更」。

**2. last-match-wins，与 git 语义一致。** `internal/owners/owners.go` 的
`OwnerOf` 遍历所有规则取最后一个匹配，因此 `*.go @all` 在前、
`internal/store/ @storage` 在后的写法按 Git 的惯例生效（宽的在前、
具体的在后覆盖）。这与 `fileMatches` 的「任一命中即真」不同——CODEOWNERS 是
**路由表**而不是过滤条件，语义必须跟 GitHub 保持一致，否则同一个文件在两个
系统里归属不同。

**3. 反转行 `!pattern` 被明确拒绝，而不是静默忽略。** `parseLine` 遇到 `!`
开头的行返回可读错误。理由：静态单行读无法 honor 一条反转（反转的语义依赖
它在文件中的位置与后续行），静默丢掉会让「我以为排除了」变成一次静默错误的
路由。宁可直接说「不支持」，也不要一条看似生效的否认。

**4. 未匹配显式 `unknown`，且给出总数。** 三个动词都遵循同一条纪律：owner 列的
价值不在于匹配了多少，而在于**没匹配的那部分有多少**。一张覆盖率为 0 的路由表和
一个缺失的 CODEOWNERS 是同一个 gap——只有数字能说出来。所以
`reachability -owners` 打「owned by nobody: N of M」、`impact -owners` 把
unowned 单独成组、`rules gaps -owners` 在没有 CODEOWNERS 时说
「no CODEOWNERS in this tree — N rule(s) cannot be routed」。

**5. `rules gaps` 的 owner 分组只路由「真的报了 finding 的规则」。** 一个安静的
规则没有位置，把它派给某个 owner 是猜；所以 `Report.Unattributed` 单独计
「catalogued 但没开火」的规则数，并在有 CODEOWNERS 时也打出来。
静默规则不是缺失的 owner，是没话说的规则——这两件事不能混。

**6. trends 三个旗标都收敛到同一个 `store.Window`。** `-since`、`-project`、
`-runs` 不是三个过滤，而是一个窗口的三个维度；SQL 侧在 INNER 查询里
`WHERE started_at >= ? AND project_root = ?` 再 `LIMIT`，因此「最近 20 条**
在这个项目内**」和「最近 20 条里**这个项目**的」不会混——这是「窗口」和
「过滤」的区别，也是 `.metricgate/history.db` 多项目共用时唯一的正确读法。

**7. `-project` 解析失败必须报错，不能变成字面过滤。** 拼错的 `-project` 若被
当成字面前提，会给出「0 runs」——而库里明明有几百条。这个 0 读起来是
「没有发生过工作」，而不是「没有这个项目」。所以 `ProjectRoot` 先精确匹配、
再 canonical 匹配（symlink 等价）、再按路径后缀匹配（仓库名），全部失败就
列出所有已记录的根，让拒绝本身可行动。

**8. `-since` 接受三种写法，本地时区解析。** RFC3339、裸日期（2026-10-01）、
日期+时间（2026-10-01 09:30:00）。裸时间戳不带时区是最常见的输入，
要求人补 `Z` 是让人去猜工具。解析失败的信息给出今天日期作例子，
因为它通常是操作员此刻唯一想输入的形态。

## 实测证据

**CODEOWNERS 归属列**（`/tmp/cotest`，一个 Go 树 + CODEOWNERS
`* @all / *.go @gophers / internal/store/ @storage / docs/** @docs-team`）：

- `reachability . -owners`：两个导出未引用函数各得一行，末列 `@gophers`；
  首行 `owners: CODEOWNERS: 4 rule(s) — 0 of 1 candidate file(s) owned by nobody (unknown)`。
- 去掉 `-owners` 输出逐字节一致（owner 列与统计行都不出现）——opt-in 的
  代价被这条实测钉死。
- `impact . -q helperTwo -owners`：affected files 节从裸路径变成
  `@gophers (1):` + 缩进路径；未认领时是
  `unknown: 1 of 1 file(s) — nobody has claimed them`。

**rules gaps 按 owner 分组**（真实仓库，自建 `.github/CODEOWNERS`）：`by owner:`
节把开火规则按 owner 分列，每列给出 finding 数与规则 id 列表；
`Unattributed` 把 catalogued-but-silent 的规则数单独报出。

**trends 窗口**（`/tmp/trdemo2`，3 次 `check` 后）：

- `-out report.json -format json` → `wrote trends to report.json (last 20 run(s); 510 bytes)`，
  文件内容与 stdout 形态一致；`-out report.txt` 同样 259 bytes。
- `-since` 四种写法（`2020-01-01`、`2026-10-13`、`2026-10-13 00:00:00`、
  `2026-10-13T00:00:00Z`）都解析，且都是同一条 run。
- `-project /tmp/trdemo2` 被**拒绝**（记录的是 `/private/tmp/trdemo2`），
  报错列出已记录根——这是本迭代抓到的 macOS symlink 陷阱，修复后同一命令返回
  `"runs": 2`。
- 多项目一致性：在真实仓库上 `trends -project metricGate` 返回 20 runs，
  与手工 `stats` 导出的 run 数口径一致（同一 db、同一窗口语义）。
- 自建 `.metricgate/history.db` 之外的路径不受影响：`-project` 未传时
  `window` 字段是 `last 20 run(s)`，与 JSON 的 `runs` 计数一致。

## 单测

| 测试 | 位置 | 断言 |
| --- | --- | --- |
| `TestParseSkipsCommentsAndBlankLines` … `TestPatternIsNormalisedForStorage`（16 个） | `internal/owners/owners_test.go` | 注释/空行跳过；last-match-wins；清空行回退到早先匹配；前导 `/` 锚定；模式词汇（`*`/`**`/`前缀 **/`/`/` 后缀/glob）；精确文件；`!` 反转被拒绝；`unknown` 显式；`UnknownShare` 计路由缺口；`Owners` 去重排序；`OwnersOf` 返回整列；`.github/` 优先 |
| `TestRulesGapsRoutesByOwner` | `cmd/metricgate/rules_gaps_test.go` | CODEOWNERS 认领的文件里开火的规则出现在该 owner 组下，finding 数 > 0 |
| `TestRulesGapsWithoutCodeownersDoesNotRoute` | 同上 | 无 CODEOWNERS → `Owners` 为空且 `Unattributed` = catalogued 数（不说谎） |
| `TestProjectRootResolvesASymlinkedSpelling` | `internal/store/store_test.go` | 同一目录的非 canonical 写法解析到同一记录 |
| `TestProjectRootRefusesAnUnknownRoot` | 同上 | 未知根报错并列出已记录根（不是静默 0 runs） |
| 既有全量回归 | `go test ./internal/... ./cmd/...` | 除预先存在的 `internal/digest TestBuildTaskFilteredBans` 外全绿 |

## 五性核对

| 维度 | 状态 | 证据 |
| --- | --- | --- |
| 可介入 | ✅ | 归属列让 `reachability`/`impact`/`rules gaps` 的结果能转给具体的人；trends 报表落盘可被任何下游接手 |
| 可审计 | ✅ | owner 依据是仓库里已有的 CODEOWNERS（GitHub 也读它），不是 metricgate 私有的第二份映射；未匹配显式 `unknown` + 计数 |
| 可度量 | ✅ | `Window` 让「最近 20 条在这项目内」成为可表达的查询；`Unattributed` 让「多少规则无法路由」成为一个数 |
| 可回溯 | ✅ | trends 落盘带 `window` 字段（last N / since / project），报告与口径一起保存 |
| 可观察 | ✅ | `-owners` 三处一致；`-out` 报告可进 CI 归档 |

## 阈值纪律（本迭代被自检抓到 3 次）

本迭代没有新增超标函数，但自检抓到过 3 次 fan-out warning，全部当场拆掉：

1. `cmdReachability` fan-out 11（>10）——把 JSON 输出与 owner 加载拆成
   `emitReachabilityJSON` 与 `ownerMapFor`。
2. `cmdRulesGaps` fan-out 11——旗标解析拆成 `parseGapsFlags` /
   `gapsFlags` 结构。
3. （`printAffectedByOwner` 的变量遮蔽 `owners_` 已被 gofmt 清理。）

最终 `check_code` 对 `rules_gaps.go` / `reachability_cmd.go` / `impact_cmd.go`
三个文件全部 PASS，0 error 0 warning。

## 验证

- `gofmt -l internal/ cmd/` 干净；`go build ./...` OK；`go vet ./...` 干净。
- `go test ./internal/owners/ ./internal/store/ ./internal/policygap/ ./cmd/... -count=1` 全 ok。
- `go test ./internal/... ./cmd/... -count=1` 只 FAIL 预先存在的
  `internal/digest TestBuildTaskFilteredBans`（digest_test.go:389）。
- MCP 三触点：`get_constraints`（policygap/owners/store 均为 core 层，
  `internal/owners` 无 cmd 依赖，符合 forbid internal/** -> cmd/**）→
  `check_code` 对 6 个改动文件全部 PASS → `check_path` 全树 0 error
  （3 个 error 全为预先存在：`cmd/metricgate/explain_cmd.go:45` cmdExplainPreview cyc 18、
  `internal/digest/digest.go:377` banSection cyc 16、`internal/store/store.go` file-length 859）。
- 仓自检 `check . -diff -arch-base ./arch.json`：PASS（arch.json 已 re-bless，
  新增 `internal/owners` 与两条边 `cmd/metricgate -> internal/owners`、
  `internal/policygap -> internal/owners`）。
- 调用记录持续落在 `.metricgate/mcp-calls.jsonl`，未删除。

## 遗留

1. **Cockpit 没有 owner 视图。** `-owners` 目前只有 CLI 三个动词；Web 侧
   的 `rules gaps` / reachability / impact 还是无归属视图。要做的是把同一份
   `owners.Map` 接到 Cockpit 的对应面板，工作量小但需要前端交互验证。
2. **`internal/owners` 只读静态单文件，不解析 Git 的 `CODEOWNERS` 继承规则中
   的「repo 之外 location」。** GitHub 允许把 CODEOWNERS 放在分支根之外；
   这里只认 `.github/`、根、`docs/` 三个位置（按 GitHub 的查找序），
   自定义位置不认。
3. **owner 列在 JSON 输出里不存在。** 机器消费方自己算归属；若后续发现有
   CI 场景想要 owner 维度，应新增独立字段而不是复用 owner 列。
4. **`-project` 的 suffix 匹配只按完整路径后缀。** 传 `metricGate` 能命中
   `/Users/mac/Downloads/metricGate`，但传大小写不同或带 `.git` 后缀的名字不——
   这是保守选择，宁可不猜。
