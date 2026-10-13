# 迭代 14 · 策略指纹落库（`runs.policy_digest`：一条 finding 的前提是「哪份策略」）

日期：2026-10-13（goal mv6c7b1e-rnog4y task-1，v0.2 排期表迭代 14）

## 为什么做这个

`docs/research/llm-loop-traceability.md` 与 `docs/research/team-gate-capabilities.md`
（2026-10-13 两份调研）都把同一件事指认为前置缺口：**运行历史存了 finding，却没存
产生它的策略版本**。

历史库里 837 条 run、每条的 finding 都在，但「这个 cyclomatic 20 是 warn 10 还是
error 15 判出来的」无法回答。后果有两层，都不是理论上的：

| 已付的成本 | 被丢掉的部分 |
| --- | --- |
| `internal/bundle/bundle.go` 已经有 `PolicyDigest(cfg)`（SHA-256），并且 bundle 产物一直在带它 | 指纹只活在 bundle 里，`check` / SARIF / `history.db` 全部不带 |
| `runs` 表存了 `tool_version`、`git_commit`、`project_root` | 唯独没有「规则集版本」——而只有它会随组织策略变化 |
| `-diff` 数值 delta 棘轮（迭代 10）已经用 base 快照做跨修订比较 | 若策略同时改了，「数值变了」无法区分是代码变还是阈值变 |

策略指纹是所有后续归因（`extends` 继承链、`trace_id` 会话链）的公共键，因此排在
v0.2 排期表的 X0：没有它，后面两项拿什么对齐都不清楚。

## 交付物

| 交付 | 位置 | 说明 |
| --- | --- | --- |
| `runs.policy_digest` 可空列 + 幂等迁移 | `internal/store/store.go:105-138` | `migrateRunsPolicyDigest`：先查 `PRAGMA table_info(runs)`，列已存在则直接返回；否则 `ALTER TABLE … ADD COLUMN`。历史库（568MB、837 runs）可原样打开，历史行留 `''`，**不回填**——无法知道历史 run 当时用的哪份策略，猜一个比空着更糟 |
| `RunMeta{PolicyDigest, TraceID}` + `SaveRunFull` | `internal/store/store.go:324-341` | 可选 provenance 的载体；`SaveRun` / `SaveRunForProject` 保留旧签名并转调 `SaveRunFull(…, RunMeta{})`，所以既有调用方与行为逐字节不变 |
| `SaveRunFull` 落三张表 | `internal/store/store.go:341-380` | `runs`（policy_digest + trace_id）、`findings`（trace_id）、`metric_snapshots`（trace_id）——只落 runs 会让「哪个工作单元产出这条 finding」对 finding 表不可答 |
| `PolicyDigest` / `PolicySourceLabel` 单一定义 | `internal/bundle/bundle.go:505-531` | 指纹散在两处就会分叉；store 与两个报告格式都从 here 取 |
| store 三个测试 | `internal/store/provenance_test.go` | 见「单测」 |

**为什么指纹放 `runs` 而不是单独的策略版本表**：策略版本表要回答「哪几个版本存在」，
而当前的问题只有一个——「这条 finding 是哪份策略判的」。一列 SHA-256 就答完；
策略版本表是一个待真实用户提出「我想对比两个策略版本」时再做的表，提前建是过度设计。

## 关键设计决策

**1. 输出只增不删，且只在有值时出现。** `internal/report/text.go` 的
`Policy{Digest, Source}` 作为 `*Policy`（`omitempty`）挂在 Envelope 上；SARIF 的
`run.properties` 加 `policyDigest` / `policySource`。空值时整个字段不出现——
不给机器消费者凭空加一个新键，也不让「没有策略」看起来像「策略为空」。

**2. `Source` 不是附带品。** 同一个 digest 可能来自组织策略或本仓策略，
`source` 区分这两者（`bundle.PolicySourceLabel`）。没有它，「阈值来自 org」和
「阈值来自本仓」在机器可读输出里不可分——这正是 `extends` 之后必须能分开的事。

**3. store 的两个入口都不改签名。** `SaveRunForProject` 被迭代 5/6/12 的测试与
Cockpit 依赖，改签名会牵动一片与本次无关的调用点；加 `SaveRunFull` 是新入口，
旧入口保底。

## 实测证据

| 场景 | 命令 | 结果 |
| --- | --- | --- |
| 单文件 check 带指纹 | `/tmp/mg-self check . -config policy.yaml -format json` | `policy: {digest: sha256:0ca6b4…, source: policy.yaml}` 顶层出现 |
| history.db 落库 | 同一命令后查 `runs` | 最新 run 带 `policy_digest`；全库 837 runs 中仅 13 条带 digest（本轮写入），历史行 `''` |
| SARIF 带指纹 | `-sarif out.sarif` | `run.properties.policyDigest` 与 JSON 信封一致 |
| bundle 与 store 不分叉 | `bundle -commit HEAD` vs `check` 输出 | 两边 digest 逐字节相同（同一函数） |
| 迁移幂等 | 连续两次 `store.Open` 同一 568MB 库 | 第二次不改 schema、不报错，`runs` 行数不变 |

## 单测

| 测试 | 钉住什么 |
| --- | --- |
| `TestSaveRunFullCarriesPolicyDigestAndTrace` | 三张表都拿到 digest/trace（`runs` 读回字段，`findings`/`metric_snapshots` 用 SQL 计数断言） |
| `TestSaveRunWithoutProvenanceLeavesItEmpty` | `RunMeta{}` 的空值是空串而非哨兵值——旧入口行为逐字节不变 |
| `TestOpenMigratesLegacyStoreWithoutProvenance` | 手工 `ALTER TABLE … DROP COLUMN` 造出「没有这些列的旧库」，重开必须成功且可写新行 |

## 五性核对

| 性 | 状态 | 证据 |
| --- | --- | --- |
| 可介入 | ✅ | 指纹是输出里读得到的值，不需要翻 DB：`check -format json` 顶层 `policy.digest` |
| 可审计 | ✅ | digest + source 一对：既知道「哪份策略」也知道「从哪来」 |
| 可度量 | ✅ | 全库 837 runs 中带 digest 的条数可直接 `SELECT COUNT`（当前 13，随使用增长） |
| 可回溯 | ✅ | finding → run 行 → policy_digest → 对应 policy.yaml 内容 |
| 可观察 | ✅ | SARIF `run.properties` 与 JSON 信封同源，上传后指纹不丢 |

## 阈值纪律

本轮自检 `internal/metrics/metrics.go` 的 `fanOut`（认知 17 /  cyclomatic 15 /
npath 576）是在九语言拉齐中引入的旧账，本轮清理时顺手拆成
`callSpellingFor` + `callsOf` + `isCallTarget`（详见迭代 9 日志「阈值纪律」），
未新增任何 accept-ledger 让步。

## 验证

- `gofmt -l internal/ cmd/` 空
- `go build ./...` OK；`go build -o /tmp/mg-self ./cmd/metricgate` OK
- `go test ./internal/store/ ./internal/bundle/ ./internal/report/ ./internal/config/ -count=1` 全 ok
- `git diff --check` 干净
- 回归基线：`go test ./...` 仅 `internal/digest` `TestBuildTaskFilteredBans`
  一个既有失败（`internal/digest/digest.go` 是用户先前未提交的改动，与本迭代无关）

## 遗留

- `findings` / `metric_snapshots` 的 `trace_id` 已在同一批迁移里加好，但本轮只由
  `SaveRunFull` 写入；`getReviewBundle` 等只读路径不写库，无影响。
- 历史 837 runs 的 `policy_digest` 永久为空（不回填），趋势视图跨策略版本比较时
  必须容忍空 digest，不能当作 sha256 缺失数据。
