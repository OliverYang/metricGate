# 迭代 12 · 闭环易用性：三处「静默丢弃」与让步系统的可见性

日期：2026-10-13（goal mv5elcyh-fwcf0l，任务 2–7）

## 为什么做这个

上一轮迭代（11）补的是「看得见」的能力缺口。这一轮的起点是一次闭环调研
（`docs/research/closed-loop-usability-audit.md`，48 条发现，零产品代码改动），
问题不在能力而在**闭环的每一跳都在静默丢东西**：

| 已付的成本 | 被丢掉的部分 |
|---|---|
| `Handler(logPath, dbPath)` 收到 dbPath | `dashboardAPI` 只拿到 logPath，store 永不打开 → `/api/dashboard` 恒 `runs:0 / projects:[]` HTTP 200 |
| MCP handler 返回 typed `Out` | wrapGate 在 `res == nil` 分支自建 `Content`，MCP SDK v1.8.0「仅当 Content 未设置才填 Out JSON」的规则被绕开 → preamble 顶替 payload |
| `accept add` 校验 owner/reason/expiry | 唯独不校验严重度 → error 级条目写进账本、打印成功、下一次 check 原样报错 |

第三种最危险：它和另外两种是同一个失败形态 —— **一个参数或输出通道被接受、
然后被静默替换，而框架的默认填充恰好是断的**。这类缺陷在输出上与正确行为
完全同形，只能靠「在已知期望的 fixture 上端到端重跑」抓到，与九语言拉齐时的
「静默零」同一族。

## 交付物

| 交付 | 位置 | 说明 |
|---|---|---|
| P0-A cockpit 读库 | `internal/cockpit/dashboard.go` | `dashboardAPI` 改用 Handler 传入的 dbPath，store 真正打开 |
| P0-A 回归 | `internal/cockpit/cockpit_test.go:153` | `TestHandlerDashboardReadsRunHistory`：HTTP `/api/dashboard` 断言 `runs > 0` |
| P0-B 载荷保活 | `cmd/metricgate-mcp/gate.go` | wrapGate `res == nil` 分支自己 `json.Marshal(out)`，`Content = [preamble, payload]`；marshal 失败退回 `nil, out, err` 让 SDK 自己填 |
| P0-B 回归 | `cmd/metricgate-mcp/gate_test.go` | `TestWrapGateKeepsTheHandlerPayload`：preamble 与 payload 两块共存 |
| P0-C 错误级拒绝 | `cmd/metricgate/accept_cmd.go` | `findingSeverity` / `loadPolicyQuiet`；`acceptAdd` 保存前查严重度，error 级 `fail(...)` 且不落账 |
| P0-C 反馈 | 同上 | 文案指出「ledger 不能让步 error」+「要么修，要么改 `configPath` 里的阈值/严重度再跑」 |
| task-5 失败指引 | `cmd/metricgate/main.go`（后在 `cmd/metricgate/check_cmd.go`） | `gateNextSteps` 六行 fix/suppress/concede/adjust/hidden；`enforceGate` FAILED 时打印，不自动执行任何一步 |
| task-5 hidden 展开 | `internal/engine/filter.go` | `SplitSummary` 拆出 `DiffSplit{RunSummary, Kept, PreExisting, Unchanged}`；`FilterSummary` / `FilterSummaryWithBase` 改为委托 |
| task-5 hidden 输出 | `cmd/metricgate/main.go`（后在 `cmd/metricgate/check_cmd.go`） | `-hidden` flag + `listHiddenFindings`，逐条打印 hidden 与 unchanged 两组 |
| task-6 trends 文案 | `cmd/metricgate/trends.go` | note 前先 `storeRunCount`，按真实 run 数分别输出「库里还有 N 条」/「 trend 库为空」 |
| task-6 JSON 裁决 | `internal/report/text.go` | `Verdict` / `VerdictOf` / `Envelope` / `JSONEnvelope`；`enforceGate` 与 JSON 共用同一份算术 |
| task-6 让步审计 | `internal/engine/suppress.go` + `cmd/metricgate/accept_cmd.go` | `Directive` / `engine.Directives`（抑制指令带 reason）；`suppressions` 新 verb 统一列出 inline directives + ledger（owner/reason/expires/stale） |
| check 管线归属 | `cmd/metricgate/check_cmd.go` | 新增：check 旗标/`-diff` 过滤与 add-on/report/store/verdict 从 main.go 迁出，main.go 回到 498 行 |
| task-6 指路 | `cmd/metricgate/rules_gaps.go` | `printSuppressed` 文案改为「per-rule count；`suppressions` lists each site and reason」并指向 ledger |

## 关键设计决策

**P0-A 修在 Handler，不修在 NewDashboard** —— 进程内探针已经证明
`cockpit.NewDashboard(logPath, <绝对 db>)` 返回 `projects:1 / runs:3`，
即 store 层是好的；坏的只是 HTTP 路径上 dbPath 被丢成 `""`。所以改动是
一行接线，不是一次重写。

**P0-B 让 preamble precede payload 而非 replace** —— 修法不是「脏 gate 时
不插 preamble」（那会让 agent 在最需要裁决的那一次看不到裁决），而是把
`json.Marshal(out)` 的结果作为第二块追加。marshal 失败时宁可丢 preamble
也不吞 payload（返回 `nil, out, err` 让 SDK 自己填）。

**P0-C 修在 check 侧，不修在 `acceptAdd`** —— `acceptAdd` 拿不到严重度
（严重度由 policy 的阈值决定，而它只被传入账本路径）。所以在保存前用
同一份 policy 跑一次单文件分析，用 `findingSeverity` 反查该 rule+行的实际
严重度；`loadPolicyQuiet` 不重复打「no policy file」note，否则 accept 每次
调用都是噪音。错误级直接拒绝，而不是写进去再在 check 时提醒——**能第一秒
说清的事不要留给下一次运行**。

**task-5 的 next steps 只打印不执行** —— 闸门不知道用户想修还是想让步，
任何自动执行都是越权。六行把四条合法路径（fix / suppress / concede /
adjust threshold）加 `-hidden` 全部列全，选择权留给操作者。

**task-6 的 suppressions 是统一视图，不是第三个 concession 系统** ——
inline directive 无 owner、无 expiry，ledger 有 owner/reason/expiry 但只对
warning 生效。两者互补但互不可见，所以一个 verb 同时列两边，并互相指路
（`rules gaps` → `suppressions`，directive 段 → ledger 段）。

## 实测证据

P0-A（`/tmp/onboard`，history DB 里 runs=6 / findings=8）：

```
修复前  GET /api/dashboard → {"runs":0,"projects":[],...} HTTP 200
修复后  同库同请求        → runs > 0、projects 列出 /private/tmp/onboard
```

P0-B（`/tmp/mg-mcp` stdio 探针，cwd=/tmp/onboard，脏 gate）：

```
get_impact        139 字符（只有 preamble） → 351 字符（preamble + 完整 JSON）
get_constraints   139 → 1562
check_path        139 → 338（早前「check_path 免疫」的判断是错的：
                  checks.go:69-93 返回 nil,out,nil，正是会被劫持的形状）
check_code        569 字符（自建 Content，免疫）
模拟 agent 循环 6/6：每轮「改 src/big.ts → 调 get_impact」，6/6 拿到完整载荷
9 个工具在脏 gate 下均 blocks=2 preamble=True payload=True
```

P0-C（`/tmp/tpdir` fixture，policy `nesting-depth {warn:2,error:4}`）：

```
accept add nesting-depth src/big.ts -line 1 -owner me -reason "legacy parser"
  → rc=2，文案说明 ledger 不能让步 error + 指向 policy 改阈值
  → 账本未写入（后续 accept list 为空）
warning 级（unknown-dependency）→ 正常让步，rc=0
```

task-5（`/tmp/onboard`）：

```
gate: FAILED (fail_on=error, 1 findings)
  fix it, or:
  suppress — add `metricgate: ignore <rule> [reason]` above the finding's line
  concede  — metricgate accept add <rule> <file> -line N -owner X -reason Y
  adjust   — change the threshold/severity in policy.yaml and re-run
  inspect  — metricgate suppressions lists every directive and ledger entry
  hidden   — re-run with -diff -hidden to see what the gate hid

check . -diff -hidden
  → src/big.ts:1: [error] nesting-depth — nesting-depth of "parse" is 5
     (threshold error 4)
```

task-6（`/tmp/onboard`，库里 6 条 run）：

```
trends  修复前：'store.enabled is false ... will only see runs from prior
                enabled windows'（而此时库里有 6 条——文案是假的）
         修复后：'store.enabled is false in policy.yaml, so `check` stores
                nothing new — the database still holds 6 run(s) from earlier'
check -format json  顶层 keys = ['errors','files','findings','ignored',
                     'ignoredByRule','metrics','verdict','warnings']
         verdict = {'passed': false,'failOn': 'error','trips': 1,...}
         -diff 版 passed true / trips 0
suppressions → 1 directive（src/lib.ts:1-2 unknown-dependency，无 reason）
             + 1 ledger 条目（owner=me expires=never reason planned dep）
         json keys = ['directives','directives_without_reason','ledger',
                      'stale_ledger_entries']
```

## 单测

| 测试 | 位置 | 钉住什么 |
|---|---|---|
| `TestHandlerDashboardReadsRunHistory` | `internal/cockpit/cockpit_test.go:153` | HTTP 路径真的读库（P0-A 回归） |
| `TestWrapGateKeepsTheHandlerPayload` | `cmd/metricgate-mcp/gate_test.go` | preamble 与 payload 共存（P0-B 回归） |
| `TestAcceptAddRejectsErrorSeverity` | `cmd/metricgate/accept_cmd_test.go` | error 级拒绝且不落账（P0-C） |
| `TestGateNextStepsNamesEverySanctionedPath` | 同上 | 六行覆盖 fix/suppress/concede/adjust/hidden |
| `TestListHiddenFindingsPrintsBothDroppedGroups` | 同上 | hidden 与 unchanged 都打印 |
| `TestSplitSummaryReturnsTheGroupsNotJustTheSizes` | `internal/engine/filter_test.go` | 拆分给的是分组不是计数 |
| `TestVerdictCountsWhatTheGateCounts` | `internal/report/verdict_test.go` | per-finding 让步算术 + fail_on=error 对照 |
| `TestEnvelopeCarriesTalliesAndVerdict` | 同上 | envelope 同时带 errors/warnings/verdict |
| `TestVerdictReasonIsReadable` | 同上 | reason 文案人可读 |
| `TestSuppressionsReportCountsWhyAnonymous` | `cmd/metricgate/accept_cmd_test.go` | 无 reason 的 directive 被计数，Rules 空渲染成 `all` |
| `TestStaleLedgerIsCounted` | 同上 | 过去过期=1、未来过期=0、无 expires=0 |

## 五性核对

| 维度 | 结论 | 证据 |
|---|---|---|
| 可介入 | ✅ | 三处 P0 都是接线/反馈修正，无新分析逻辑；`-hidden`、`suppressions` 为新增出口，默认行为不变 |
| 可审计 | ✅ | `suppressions` 一个 verb 列出两种让步系统的全部站点与理由；stale 账本条目被计数 |
| 可度量 | ✅ | P0-B 有 139 vs 351 / 139 vs 1562 / 6-of-6 循环的字节级对比；JSON 现在带 errors/warnings/verdict |
| 可回溯 | ✅ | `VerdictOf` 被 `enforceGate` 与 JSON envelope 共用，打印行与机器字段不可能不一致 |
| 可观察 | ✅ | `-hidden` 让「闸门藏了什么」从不可见变成可列出；trends note 与真实 run 数一致 |

## 阈值纪律（本迭代被自检抓到 5 次，全部当场拆函数）

| 位置 | 问题 | 处理 |
|---|---|---|
| `internal/report/verdict_test.go` 初版 | 断言把已让步的 warning 也计入 trips | 改为先只放 conceded 条目断言 `trips==0`，再 append 一条未让步条目断言翻转，并加 fail_on=error 对照防常量 |
| `cmd/metricgate/rules_gaps.go` printSuppressed | 尾部 `fmt.Println` 带 `\n` 触发 go vet `redundant newline` | 去掉 `\n` |
| `cmd/metricgate/accept_cmd.go` suppressDirectives | cyclomatic 17（阈值 15）——walk 回调里叠了解释/读取/解析/相对路径四件事 | 拆出 `directiveDirSkipped` + `fileDirectives` |
| `cmd/metricgate/main.go` cmdCheck | cyclomatic 16（阈值 15）——旗标解析、add-on 守卫、ledger/arch/report 编排都在一个函数里 | 拆出 `checkFlags`/`newCheckFlags`、`checkAddOnOpts`、`runCheckAddOns` |
| `cmd/metricgate/main.go` file-length | 906 行（阈值 800）——check 管线全在 main.go | check 管线整体搬到新文件 `cmd/metricgate/check_cmd.go`（498 + 423 行） |

## 验证

- `gofmt -l cmd/ internal/` 干净
- `go build ./...` 通过；`go build -o /tmp/mg-self ./cmd/metricgate` 与
  `go build -o /tmp/mg-mcp ./cmd/metricgate-mcp` 均 OK
- `go vet ./cmd/...` 干净
- `go test ./cmd/...` ok（cmd/metricgate 3.440s、cmd/metricgate-mcp 5.693s）；
  `go test ./internal/report/ ./internal/engine/ ./internal/accept/` ok
- `go test ./...`：仅 pre-existing `internal/digest TestBuildTaskFilteredBans`
  （digest_test.go:389）失败
- 仓自检 `check . -diff -base HEAD -config policy.yaml`：118 new / 173 hidden /
  10 unchanged dropped，summary 2 errors / 299 warnings / 18068 snapshots，
  gate FAILED——两个 error 均为预先存在（`cmd/metricgate/explain_cmd.go:45`
  `cmdExplainPreview` cyc 18、`internal/digest/digest.go:377` `banSection`
  cyc 16，HEAD worktree 同口径复现一致）
- `check . -diff -format json` 顶层 keys = errors/files/findings/ignored/
  ignoredByRule/metrics/verdict/warnings，verdict =
  `{'passed': false, 'failOn': 'error', 'trips': 2,
  'reason': '2 finding(s) at or above fail_on=error', 'accepted': 1,
  'suppressed': 1}`——与 stderr 的 gate 行同源同数
- `check . -diff -hidden` 列出 173 条 pre-existing 与 10 条 unchanged
- `git diff --check` 干净；temp probe（zz_peek / zz_peek2 / zz_stale /
  zz_verdict）已删除，`git status` 无 zz 残留

## 遗留

- **账本仍只对 warning 生效**（刻意设计：「error 意味着语义坏了，不是口味」）。
  本轮只是把这个边界从「静默 no-op」变成「`accept add` 当场拒绝并说明」。
- **inline directive 仍无 owner/expiry** —— `suppressions` 只是让它可见
  （含「N 条没写理由」的计数提示），没有给它加问责元数据。给 directive 加
  owner 会改变一个无状态语法的心智模型，需要单独一轮产品决策。
- **MCP `check_path -diff_only` 仍走纯行区间**（迭代 10 遗留，本轮未触及）：
  CLI 默认 value-delta，MCP 还是 line-range，两条路径的噪声水平仍不一致。
- **`suppressions` 的 directive 采集跳过 `.data`/testdata 等目录，但不读 policy
  的 `exclude`** —— 与 `CollectSelectedWithRefs` 的 selection 不是同一套，
  沙箱副本里的 directive 会被列出来。
- `rules gaps`（policygap）本身仍不审计让步系统，只是在文案上指向
  `suppressions`；早前想在 policygap 里加 `Concession` 的尝试已整体回退。

### 补充修复：`-diff` 闸门用了未过滤的 summary（回归）

iter-12 把 check 管线搬进 `check_cmd.go` 时丢了 `summary = runDiffMode(...)`
的赋值：`runCheckAddOns` 照旧算出正确的 split 并打印
`diff-only: base=HEAD, 0 new findings, 1 pre-existing hidden`，但紧接着的
闸门、控制台报告和 JSON envelope 拿到的都是**未过滤**的 summary。结果是
一次跑在相邻两行里自相矛盾——先告诉用户「这个 finding 是既存的，不算你的」，
再因为它把闸门判红。

实测复现（`/tmp/dbg2`：TS fixture + policy 让 cognitive 为 error、树干净）：

| | diff-only 行 | summary | gate |
| --- | --- | --- | --- |
| 修复前 | 0 new / 1 hidden | 1 errors | FAILED, rc=1 |
| 修复后 | 0 new / 1 hidden | 0 errors | PASSED, rc=0 |

反向验证同步钉住：往文件里追加一个真 error → 1 new / FAILED rc=1；
`checkout` 回干净树 → 0 new / PASSED rc=0。仓自检也随之从
`2 errors, gate FAILED` 变成 `0 errors, gate PASSED`——那 2 个 error
（`cmd/metricgate/explain_cmd.go:45`、`internal/digest/digest.go:377`）
本来就不是本次改动引入的，只是一直被这个回归挡在 hidden 集之外。

回归测试 `cmd/metricgate/check_cmd_test.go`
`TestRunCheckAddOnsReturnsTheFilteredSummary`（+ `TestRunCheckAddOnsWithoutDiffKeepsTheSummary`）
钉住 `runCheckAddOns` 的返回值必须被调用方采用。
