# 迭代 18 · trace 属性（X2）与组合报表（X3）

日期：2026-10-14（v0.2 需求排期的 T5/X2 + X3，ref docs/research/v02-requirement-schedule.md:62-66）

## 为什么做这个

迭代 15 让 `trace_id` 贯通了四张表（calllog / runs / findings / metric_snapshots），
迭代 14 让 `policy_digest` 落库。但两份调研都点出同一个后续缺口：**键有了，
可切分的维度还没有，能一次问出跨表答案的入口也没有。**

**X2 — 模型/提示词这类维度变化太快，不该占固定列。**
`internal/store/store.go` 的 `runs` 表每加一个列都是一次 migration + 三处查询
改动。而"今天记 model、下个月要 prompt_template_id"正是 LLM 侧维度的常态。
调研给的设计是 `trace_attrs(trace_id, key, value)` KV + **键白名单**——
白名单是为了防止 KV 表变成垃圾场：谁都能写任意键，报表就不可信。

**X3 — 五份数据在，但没有任何入口把它们按 trace 串起来。**
`docs/research/llm-loop-traceability.md` §5 列了五个从此可回答的问题，其中最值钱的是
Q3：**"工具介入（先 get_constraints 后 check）是否提高首过率？"** ——它直接命中
claims.md 里 #15「闸门在真实 agent 循环里被调用/findings 被修掉」这个长期 ⏳。
Q3 答不出不是因为没数据，是**数据和判定不在一条链上**：`phase` 有、`verdict` 有，
但缺一个把"一次任务"的全部调用与运行归到一起的键——而这个键（trace_id）迭代 15
已经造好了。本迭代就是把它用起来。

## 交付物

| 交付 | 位置 | 说明 |
| --- | --- | --- |
| trace 属性词汇表 | `internal/trace/trace.go` | `Env`（`$METRICGATE_TRACE_ATTRS`）/ `MaxValueLen`=256 / `Attr` / `Keys()` / `Allowed()` / `Parse()`；未知键与畸形项**丢弃并计数**；值超长按 rune 边界截断 |
| trace_attrs 表 | `internal/store/store.go`（schema） | `(trace_id, key, value)` 复合主键 + `idx_trace_attrs_kv(key,value)`；`saveTraceAttrs` upsert（一个 trace 跨多 run，最新值胜） |
| trace 存储 API | `internal/store/trace_store.go` | `Attr` / `TraceAttrs` / `Traces` / `ResolveTraceAttr` / `TraceFilter` / `ResolveTraceFilters`；`queryStrings` 抽出单列查询样板（顺带消掉 3 处克隆） |
| 读取 API 拆分 | `internal/store/read.go` | 原 `store.go` 的读侧（Report/RunRecord/LatestRuns/ProjectRoot/…）整体移出，把 `store.go` 从 1048 行压回 425 行（原 884） |
| Window trace 维度 | `internal/store/window.go` | `Window.TraceIDs`：nil = 每条 trace；非 nil（含空）= 仅这些 trace；空集走 `1 = 0` 而不是非法 SQL `IN ()` |
| trace 全景（共享） | `internal/traceview/traceview.go` | `Collect(storePath, logPath, traceID)` 把 runs+findings+attrs+calls 汇成一个 `View`，CLI 与 MCP 复用 |
| `metricgate trace` verb | `cmd/metricgate/trace_cmd.go` | `trace <traceID> [path]`，text + JSON；totals 含 verify 计数 |
| trends trace 过滤 | `cmd/metricgate/trends.go` | `-trace` / `-model` / `-prompt-digest`，三者并集；无匹配显式 `no matching trace` 而非静默放宽 |
| check 采集属性 | `cmd/metricgate/check_cmd.go` | `traceAttrs()` 读 env → `RunMeta.Attrs`；丢弃时 stderr 提示允许键 |
| MCP `get_trace` | `cmd/metricgate-mcp/tools.go` | 复用 `traceview.Collect`；`get_trends` 加 `trace`/`model`/`prompt_digest` 参数 |
| MCP 工具分阶段 | `cmd/metricgate-mcp/tools.go` | `registerTools` 拆成 `registerLoopTools` + `registerInsightTools`（原 51 行触 function-length） |
| calllog 阶段分类 | `internal/calllog/calllog.go` | `get_trace` → `PhaseExplore` |
| Cockpit trace 过滤 | `internal/cockpit/web/{index.html,app.js}` | 调用视图加 trace 下拉（从在手的调用动态生成，选中态在刷新后保留）；详情弹窗显示 trace id |
| 单测 | `internal/trace/trace_test.go`、`internal/store/trace_test.go`、`internal/traceview/traceview_test.go`、`cmd/metricgate/trends_test.go`、`cmd/metricgate-mcp/trace_test.go` | 见下表 |

## 关键设计决策（含理由）

**1. 白名单是纪律，不是过滤器。** `internal/trace.Parse` 对未知键**丢弃并返回计数**，
而不是存下来等报表去发现。这与 DSL 对非法条目、`BansFromConfig` 对坏 ban 的做法同源：
一个拼错的键（`modle=`）必须是一个**可见的数字**，不能悄悄进库变成一条没人能按它分组的行。
check 侧把它打到 stderr（`note: N trace attribute(s) dropped …; allowed keys: …`），
因为"我设了 model 但没生效"必须在设置它的那次运行里说，不能等到查询时才发现。

**2. nil 与空 slice 是两句话。** `Window.TraceIDs` 用 nil 表示"每条 trace"，
非 nil（含空）表示"仅这些 trace"。这是本迭代最容易写错、也最值钱的一处：
`trends -model nope` 必须回答"0 runs（no matching trace）"，而不是悄悄放宽回全部 run——
后者是一个**静默错误的报告**，比报错更糟。`runScope` 对空集发 `1 = 0`，
因为字面 `trace_id IN ()` 是非法 SQL。

**3. 属性挂在 trace 上，不挂在 run 上。** 一个任务常有多次 `check`（不同文件、
不同时刻），它们共享同一份 model/prompt 指纹。把属性存在 `trace_attrs` 表按
trace_id 主键，第二次 run 是 upsert 而非重复插入——"最新断言胜"是符合直觉的语义。
属性只在有 trace id 时才写：没有 trace 的属性是无人能 join 回来的孤儿行。

**4. 组合报表的解析下沉到 store。** CLI 的 `resolveTraceScope` 与 MCP 的
`traceScope` 本是同一段逻辑，分处两个 main 包。与其复制，不如把
`ResolveTraceFilters` 放到 `internal/store`：union 语义、nil-vs-empty 区分、
白名单解析只有一份实现，两个 surface 不可能对同一个问题给出不同答案。

**5. `store.go` 的拆分是被自检逼出来的。** 本迭代把 `store.go` 从 884 行推到 1048 行，
触发了既有的 `file-length` error（阈值 800，迭代 17 日志里它是 859 行的遗留项）。
与其接受一次变差，不如按职责拆：trace 相关存储 → `trace_store.go`，
全部读侧 → `read.go`。结果 `store.go` 回到 425 行，且全量 error 从 3 个降到 2 个
（消掉了 `internal/store/store.go` file-length）。

**6. trace 不参与判定。** 与调研 §6 的红线一致：trace 只出现在输出侧。
`check` 的 verdict 仍只由 findings 与 `fail_on` 决定；属性采集失败（丢弃）只提示，
不改变结论。确定性没有被破坏。

## 实测证据（/tmp demo，真实 CLI + MCP）

**X2 采集与丢弃计数**（`METRICGATE_TRACE_ATTRS="model=gpt-5,harness=codex,modle=typo"`）：

```
note: 1 trace attribute(s) dropped (unknown key, empty value, or missing '=');
      allowed keys: agent, branch, harness, model, prompt_digest, tool_invocations
```

`modle=typo` 被丢弃且计数为 1，两个合法键入库。

**trace 全景**（同一次运行后，`metricgate trace trace-demo`）：

```
trace trace-demo

attributes:
  harness            codex
  model              gpt-5

runs: 1   errors: 0   warnings: 0   notes: 0
calls: 0

runs (oldest first):
  #1  2026-10-14T05:18:56Z  E=0 W=0 I=0  files=1  policy=sha256:0771f267…
```

**trace 的跨表 join**（补一条 calllog 记录后，同一条命令）：

```
runs: 1   errors: 0   warnings: 0   notes: 0
calls: 2   verify calls: 1   flagged: 0   resolved: 0

calls (oldest first):
  05:00:00  get_constraints    before
  05:00:01  check_code         verify   PASS
```

同一个日志里 `traceId: other` 的那条调用被正确排除——join 按 trace 收窄，不是全量。

**trends 按模型切窗口**：

```
$ metricgate trends -model gpt-5
runs: 1 (last 20 run(s), trace trace-demo)

$ metricgate trends -model nope
no runs stored in this window (last 20 run(s), no matching trace) — widen it …
```

第二个是本迭代的验收主项：**没匹配 = 明确说"没有"，而不是悄悄给全部 run**。

**MCP 侧**：`get_trace`（`internal/traceview` 复用）与 `get_trends -model` 的
单测（`cmd/metricgate-mcp/trace_test.go`）通过；`get_trace` 复用同一个
`traceview.Collect`，因此 MCP 与 CLI 对同一 trace 的输出字段一致。

## 单测

| 测试 | 位置 | 断言 |
| --- | --- | --- |
| `TestParseKeepsWhitelistedKeys` | `internal/trace/trace_test.go` | 白名单键按 key 排序保留 |
| `TestParseDropsUnknownKeysAndCountsThem` | 同上 | 未知键丢弃且计数（`modle`/`temperature` → 2） |
| `TestParseDropsMalformedAndEmpty` | 同上 | 无 `=`、空 key、空 value 丢弃；纯空项跳过不计数 |
| `TestParseTruncatesLongValues` / `TestTruncateKeepsValidUTF8` | 同上 | 值截断到 256 且不切开多字节 rune |
| `TestLastWriteWinsWithoutDoubleCounting` | 同上 | 重复键取末值且只算一个 |
| `TestTraceAttrsRoundTrip` / `TestTraceAttrsUpsertAcrossRuns` | `internal/store/trace_test.go` | 属性读写；同 trace 二次 run upsert |
| `TestTraceAttrsNeedATrace` | 同上 | 无 trace id 时不写孤儿行 |
| `TestResolveTraceAttr` / `TestWindowTraceIDsScopesReports` | 同上 | 属性→trace 映射；非 nil 收窄、空集选 0、nil 选全部 |
| `TestCollectJoinsRunsAttrsAndCalls` / `TestCollectUnknownTraceIsEmptyNotError` | `internal/traceview/traceview_test.go` | 四表 join + 排除 decoy；未知 trace 返回非 nil 空列表 |
| `TestResolveTraceScope` / `TestTrendsTraceScopeFiltersTheReport` | `cmd/metricgate/trends_test.go` | 三旗标并集、nil-vs-empty；-model 收窄且无匹配返回 0 |
| `TestGetTraceReadsStoreAndLog` / `TestGetTraceRequiresID` / `TestGetTrendsScopesByModel` | `cmd/metricgate-mcp/trace_test.go` | MCP 接线；空 id 报错；MCP trends 与 CLI 同口径 |

## 五性核对

| 维度 | 状态 | 证据 |
| --- | --- | --- |
| 可介入 | ✅ | `get_trace` 让宿主在一次调用里拿到"这次任务做了什么"；`get_trends -model` 让"换模型后是否变差"变成一次查询 |
| 可审计 | ✅ | 属性只存白名单键的指纹类值（model/prompt_digest/harness…），不存提示词/模型输出/源码；未知键丢弃有计数 |
| 可度量 | ✅ | Q3 从"数据在但不在一条链上"变成一条命令：一次 trace 的 verify 调用数 / flagged / resolved 直接可见 |
| 可回溯 | ✅ | trace 全景把 run#id、policy digest、每条 finding 的 file:line、每条调用的 phase+verdict 并排展示 |
| 可观察 | ✅ | CLI `trace`、MCP `get_trace`、`trends -trace/-model/-prompt-digest`、Cockpit trace 下拉四处一致（同一 `traceview` / 同一 `Window`） |

## 阈值纪律（本迭代被自检抓到 14 次）

本迭代新写的代码首次 `check . -diff` 报出 **14 条 warning**，全部当场修掉，
再跑 `-diff` 归零（最后一次为 0 new findings）：

1. `registerTools` function-length 51 → 拆 `registerLoopTools`/`registerInsightTools`。
2. `Store.saveRun` fan-out 14 / npath 216 → 拆 `saveRunChildren`。
3. `Store.ProjectRoots`/`Traces`/`ResolveTraceAttr` 三处 function-clone → 抽 `queryStrings`。
4. `ResolveTraceFilters` cognitive 17 → 拆 `anyTraceFilter` / `resolveOneFilter`。
5. `resolveTraceScope`（CLI）cyc 11 / npath 216 → 逻辑下沉到 store，CLI 只做旗标映射。
6. `traceScope`（MCP）cyc 11 / npath 216 → 同上，删除重复实现。
7. `cmdTrace` fan-out 12 → 拆 `parseTraceFlags` / `traceFlags`。
8. `renderDashboard`（app.js）cyc 14 / fan-out 16 → 拆 `renderProjectList`/`projectCard`/`setLandingView`。
9. `showCall`（app.js）cyc 13 → 拆 `callDetailExtras`/`detailRows`。
10. `TestCollectJoinsRunsAttrsAndCalls` cyc 15 / npath 3456 → 拆 `seedTraceStore`/`assertTraceView`/…。
11. `assertTraceView` 残留 cyc 12 → 再拆 `assertTraceRuns`/`assertTraceCalls`。

顺带把 `internal/store/store.go` 的 file-length error（1048 → 425 行）消掉。

## 验证

- `gofmt -l internal/ cmd/` 干净；`go build ./...` OK。
- `go test ./internal/trace/... ./internal/traceview/... ./internal/store/... ./cmd/metricgate/... ./cmd/metricgate-mcp/... -count=1` 全 ok。
- `go test ./... -count=1` 只 FAIL 预先存在的 `internal/digest TestBuildTaskFilteredBans`
  （digest_test.go:389，用户既有脏工作）。
- 狗粮 gate：`check . -diff -base HEAD -config policy.yaml -arch-base arch.json` → PASSED，
  0 new findings；`arch diff -base arch.json .` → `no architecture drift`。
- 全量 `check .`：2 error，均为预先存在（`cmd/metricgate/explain_cmd.go:45` cmdExplainPreview cyc 18、
  `internal/digest/digest.go:377` banSection cyc 16）；本迭代新引入的 store file-length error 已消除。
- 分层声明：`policy.yaml` 的 core 层加 `internal/trace`、adapter 层加 `internal/traceview`；
  新增边 `cmd/metricgate -> internal/trace|traceview`、`cmd/metricgate-mcp -> internal/traceview`、
  `internal/traceview -> internal/calllog|store` 均符合声明。
- 调用记录持续落在 `.metricgate/mcp-calls.jsonl`，未删除。

## 遗留

1. **X4（LLM 侧元数据采集）仍是约定，不是功能。** 宿主必须在启动 MCP server 前
   设 `$METRICGATE_TRACE_ID` 与 `$METRICGATE_TRACE_ATTRS`；工具不主动探测模型。
   调研已把 X4 标为"待宿主形态确认"，本迭代未推进。
2. **Cockpit 的 trace 过滤只作用于调用视图，未作用于 run 历史视图。** runs 视图
   仍按 project 分组；按 trace 过滤 runs 需要 store 侧一个新查询，留待有真实需求时做。
3. **`tool_invocations` 键在白名单里但无生产者。** 它由宿主设置（它知道自己的
   工具调用数）；工具侧不臆造。若长期无生产者，应在白名单里删掉以免误导。
4. **属性值匹配是精确等值，无前缀/范围查询。** 与 `accept` 账本同样的保守选择：
   宁可精确命中，不做可能过度匹配的模糊查询。
