# 迭代 15 · `extends` 组织策略继承 + `trace_id` 贯通四张表（同一把键归因）

日期：2026-10-13（goal mv6c7b1e-rnog4y task-2 / task-3，v0.2 排期表迭代 15）

## 为什么做这个

两项是同一件事的两半：**让一次运行能被归因到一个具体的工作单元和一份具体的策略**。
分开做都不成立——只有 trace 没有 `extends`，会话链对不上「阈值其实来自组织策略」；
只有 `extends` 没有 trace，策略来源对了但「哪次 LLM 会话触发的」仍然不可答。

先有的能力已经付了成本却被丢掉：

| 已付的成本 | 被丢掉的部分 |
| --- | --- |
| `internal/config/config.go` 的 `SourceKind` / `SourcePath` / `ExplicitKeys`  provenance 三元组 | 只用于报告「这份值是自己写的还是预设备」，从未用于跨文档继承 |
| `internal/config/preset.go:197-301` 的 `presetWins` / `markPreset` | 已实现「两源合并 + 保留来源」，但只服务 preset，没服务组织策略 |
| `internal/store` 的 `runs` / `findings` / `metric_snapshots` 三张表 | 没有任何一列记录「这是哪个 trace 的产物」 |
| `internal/bundle` 已有 `RunInfo{Root, Mode, Base, Head, Commit}` 信封字段 | 没有 trace 字段 |
| MCP 会话本身已是进程级聚集体（`l.session`） | session 与 runs.id 是两把不同的键，session→run 一对多 |

`llm-loop-traceability.md` 给出的判定：**id 已经落库（`check_cmd.go` 的
`SaveRunForProject` 返回它）却从不出现在任何输出里**；而 `team-gate-capabilities.md`
指出的 L1 组织策略继承，实测 `grep include|extends|base_policy|inherit` 在
`internal/config` 下为零命中。

## 交付物

| 交付 | 位置 | 说明 |
| --- | --- | --- |
| `extends:` 单继承解析 | `internal/config/extends.go`（新文件，~200 行） | `MaxExtendsDepth = 10`；环检测给出完整链路报错；缺失父文档即时失败，不回退到静默的空策略 |
| 合并语义 | `extends.go` `mergeInto` | 子文档同键覆盖；**序列（list）整体替换而非追加**——两个源的阈值列表相加会产出任何一方都没写过的组合 |
| 来源标注 | `internal/config/config.go:427` `Config.Extends()` + `ExtendsSource` | 返回「继承链 + 每个父文档贡献了哪些键」，`rules explain` 因此能说出一个继承来的阈值出自哪份文件 |
| 单继承链上限与诊断 | `internal/config/explain.go:308-309` | `explain` 列出策略加载来源链 |
| `trace_id` 贯通 | `internal/store/store.go:140-160` `migrateRunsTraceID` | `runs` / `findings` / `metric_snapshots` 三张表各加可空 `trace_id`（幂等迁移，与 `policy_digest` 同批） |
| `trace_id` 四个写入/带出点 | `cmd/metricgate/check_cmd.go:434`（check）、`internal/bundle/bundle.go:208`（bundle 信封）、`internal/report/{text,sarif}.go`（两种输出）、`cmd/metricgate-mcp/{calllog,main}.go`（MCP 调用记录 + `reviewBundleIn` 回显） | 读 `$METRICGATE_TRACE_ID`；未设置时一切为空，**输出逐字节不变** |
| `runId` 输出 | `internal/report/text.go` `JSONEnvelopeWithRun` + `internal/report/sarif.go` `SARIFWithRun`，`cmd/metricgate/check_cmd.go` `storedRunID()` | store 返回的 row id 首次进入机器可读输出，回答「这次裁决对应哪一行」 |
| extends 测试 + provenance 测试 | `internal/config/extends_test.go`（已有，6 个用例）、`internal/store/provenance_test.go`（新增，3 个用例） | 见「单测」 |

## 关键设计决策

**1. 单继承，且只做文件路径。** 不做多继承、不做 URL include、不做 glob。
多继承的冲突消解（菱形 / 覆盖顺序）是一个需要真实组织场景才能定语义的设计，
现在猜出来的规则会在第一次真实使用时变成约束。环检测与深度上限只是让这个
受限能力**失败得响亮**：一个继承成环的策略必须在加载时报错，而不是悄悄用一半。

**2. trace 只有四个落点，且都不是判定输入。** `trace_id` 参与归因与展示，
**不参与任何判定**：`internal/engine` 一次都没引用它（`grep trace internal/engine`
零命中）。一条 trace 不会让 finding 变多变少，只让 finding 可归属。这是
「判定语义不变」这条硬约束的落地方式。

**3. 未设置 env 时逐字节一致。** 这是本轮反复验证的一条：`run` 对象整体
`omitempty`（`internal/report/text.go` 的 `Run *Run`），空 trace / 空 digest /
无 store 时连键都不出现。否则会有大量自动 diff 工具把空字段当成 schema 变更。

**4. session 与 trace 是两个键，不合并。** `session` 是进程级的（一次 MCP server
进程），`trace_id` 是工作单元级的（一次 LLM 任务）。session→run 是一对多，
强行合成一把键会丢掉「同一会话做了两件事」这个事实。两者并存写入 calllog。

## 实测证据

| 场景 | 命令 | 结果 |
| --- | --- | --- |
| extends 生效 | `/tmp/extdemo`：`policy.yaml` 写 `extends: ./base.yaml` + `fail_on: warning`，`base.yaml` 写 typescript cyclomatic warn 1/error 2 | `check -format json` 顶层 `run: {extends: "base.yaml"}`，阈值按 base 生效、`fail_on` 被子文档覆盖为 warning |
| 指纹 + trace + runId 同框 | `METRICGATE_TRACE_ID=audit-trace-1 check . -config policy.yaml -format json` | 同一份 JSON 里 `policy.digest`、`run.traceId="audit-trace-1"`、`run.runId` 同时出现 |
| 未设 trace 时不变 | 不设 env 跑同一命令 | `run` 键整个不出现（对照组逐字节相同） |
| store 落库 | 同上后查 `.metricgate/history.db` | `runs` 最新行 `trace_id='audit-trace-1'`；`findings` 与 `metric_snapshots` 同 trace 行数与 run 的 finding/快照数一致 |
| MCP 调用记录 | `/tmp/mg-mcp` stdio 探针发 `get_review_bundle` | 响应顶层 `traceId` 回显；`.metricgate/mcp-calls.jsonl` 记录带 `traceId` 字段 |
| 历史行不受影响 | 全库统计 | `runs` 共 837 行，带 `trace_id` 的 1 行（本轮），历史行全部空——**只归因新运行** |

## 单测

| 测试 | 钉住什么 |
| --- | --- |
| `TestExtendsMergesChildOverParent` | 同键子覆盖父；序列整体替换不追加 |
| `TestExtendsBlankDocumentIsUntouched` | 没有 extends 的策略走原路径，对象身份不变 |
| `TestExtendsCyclesAreRejectedWithTheChain` | 环必须报错且报出完整链路，不能只报 "cycle" |
| `TestExtendsDepthLimit` | 超过 `MaxExtendsDepth` 明确失败 |
| `TestExtendsMissingParentFailsLoudly` | 父文档缺失即时失败，不回退空策略 |
| `TestSaveRunFullCarriesPolicyDigestAndTrace` | digest/trace 落三张表 |
| `TestSaveRunWithoutProvenanceLeavesItEmpty` | `RunMeta{}` 行为与旧入口逐字节一致 |
| `TestOpenMigratesLegacyStoreWithoutProvenance` | 旧库（无新列）可打开、迁移幂等、可写新行 |

## 五性核对

| 性 | 状态 | 证据 |
| --- | --- | --- |
| 可介入 | ✅ | 继承来的阈值在 `rules explain` 里标出父文档路径；trace 可在任何一处输出读到 |
| 可审计 | ✅ | 让步/阈值/裁决三者都能问到「哪份策略、哪个 trace」；`extends` 链与 digest 都在输出里 |
| 可度量 | ✅ | `SELECT COUNT(*) FROM runs WHERE trace_id != ''` 可统计已归因运行占比 |
| 可回溯 | ✅ | trace → session → runs.id → finding → policy_digest → policy.yaml，链条无断点 |
| 可观察 | ✅ | Cockpit 与 SARIF 拿同一把键；JSON 信封 top-level 可直接给消费方 |

## 阈值纪律

本轮自检抓到一次真实回归并当場拆掉：`cmd/metricgate-mcp/main.go`
`getReviewBundle` 为回显 trace 增加分支后 cyclomatic 达 16（阈值 15），成为新的
gate error。修复方式是抽出 `reviewBundleRequest(in)` 承担全部入参归一化
（路径默认、budget/counter 三路、mode 判定），主函数只剩 bundle.Build + 回显。
拆完自检 0 errors，无 accept-ledger 让步。

## 验证

- `gofmt -l internal/ cmd/` 空
- `go build ./...` OK；`go build -o /tmp/mg-self ./cmd/metricgate` 与 `-o /tmp/mg-mcp ./cmd/metricgate-mcp` OK
- 自检棘轮：`/tmp/mg-self check . -diff -base HEAD -config policy.yaml`
  → `summary: 0 errors, 140 warnings, 0 notes, 18484 metric snapshots`，
  `gate: PASSED (fail_on=error)`，EXIT=0
- `go test ./...` 仅 `internal/digest` `TestBuildTaskFilteredBans` 一个既有失败
- `git diff --check` 干净

## 遗留

- `trace_attrs`（模型名 / prompt digest / harness 等键值对）未做：需要先确定白名单
  与值长度上限，且依赖各 host 主动传入，记入下一轮排期。
- LLM 侧维度（模型、prompt digest）仍完全缺失：评审输入
  （`reviewBundleIn`）只有路径/base/commit/预算开关，这是 host 侧配合问题，非本工具
  可单方面补齐。
- MCP `check_path -diff_only` 仍用纯行范围棘轮（未接迭代 10 的数值 delta），
  与 CLI 的一致性欠账延续。
- 组织策略继承目前只解决「读」，组织侧统一推送/校验策略的机制未涉及
  （`team-gate-capabilities.md` §10 已明确不做中心化判定服务）。
