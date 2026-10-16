# 策略完备性审计（`metricgate rules gaps`）

## 它回答什么问题

`check` 的输出只有发现清单。发现清单回答不了这个问题：

> 这次跑干净了，是代码干净，还是策略太薄？

一个从未声明 `architecture.layers` 的仓库，`layer-violation` 永远报 0。一个
声明了但阈值高到没人越过的仓库，也报 0。一个真的没有越界的仓库，还是报 0。
三者的 `check` 输出一模一样，但含义完全相反——而其中只有第三种是好事。

`rules gaps` 把"0 条发现"拆开，按规则逐条给出沉默的原因。

## 用法

```bash
metricgate rules gaps [path] [-config .metricgate.yaml] [-format text|json]
```

只读、确定性（同一棵树两次审计输出逐字节相同）、不写文件、不发网络。
跑一次完整的 `check`，所以代价与 `check` 相当，不是"免费的元数据查询"。

## 六种状态

| 状态 | 含义 | 该怎么反应 |
|---|---|---|
| `firing` | 有发现 | 正常处理 |
| `armed` | 已武装、适用、没找到问题 | 不用动 |
| `report_only` | 启用着，但 warn/error 都是 0，结构上不可能产生发现 | 要么给阈值，要么承认它是报表项 |
| `disabled` | 策略显式关掉了 | **一个决定，不是洞** |
| `undeclared` | 策略从未声明武装它的那一节 | **这才是要补的洞** |
| `flag_only` | 只有加 flag 才跑，`check` 对它什么也没说 | 需要单独跑 |
| `no_files` | 没有该语言的文件 | 不适用 |

`disabled` 和 `undeclared` 经常一起出现，且要求相反的动作：前者是"我们知道，
不要"，后者是"我们不知道它没开"。把它们都塞进"0 条发现"里，就是工具悄悄
失去大部分价值、却每次都报 PASS 的方式。

## 真实输出：一个薄策略的仓库

两文件、两包、`.metricgate.yaml` 只有 `version: 1`：

```
policy gaps: . — 2 files, 2 packages, 22 rules catalogued, 1 firing

not armed — these rules cannot fire until the policy declares them:
  forbidden-edge           declare architecture.forbid
  import-cycle             declare architecture.layers or architecture.forbid
  layer-violation          declare architecture.layers

needs a flag — a plain `check` says nothing about them:
  metric-regression        only runs with check -diff -regress

report-only — enabled but no threshold set, so they can never fire:
  maintainability          no threshold set (warn=0, error=0)

firing:
  unknown-dependency       1 finding(s)

no files of their language:
  cbo                      no scanned java files
  ...
  wmc                      no scanned java files

armed and clean — no action (10 rules)

2 package(s) match no layer, so no layer rule protects them:
  internal/api, internal/store
```

这个仓库 `check` 只报 1 条发现。审计说的是：三条分层规则根本没开火的可能，
七个度量里六个靠内置阈值在跑（`maintainability` 例外，它默认就是报表项），
两个包不在任何分层里。

## 真实输出：本仓库

```
policy gaps: . — 152 files, 34 packages, 27 rules catalogued, 8 firing

needs a flag — a plain `check` says nothing about them:
  metric-regression        only runs with check -diff -regress

report-only — enabled but no threshold set, so they can never fire:
  maintainability          no threshold set (warn=0, error=0)

switched off — a decision, not a gap:
  todo-comment             languages.go.rules.todo-comment.enabled: false

firing:
  banned-os-exec           3 finding(s)
  cognitive-complexity     21 finding(s)
  cyclomatic-complexity    24 finding(s)
  file-length              5 finding(s)
  function-clone           1 finding(s)
  function-length         10 finding(s)
  params-count             2 finding(s)
  sdp-violation            1 finding(s)

armed and clean — no action (8 rules)
```

外加分层覆盖：

```
10 package(s) match no layer, so no layer rule protects them:
  internal/apisurface, internal/bundle, internal/calllog, internal/clonedrift,
  internal/clones, internal/gittest, internal/hotspots, internal/parsetest,
  internal/policygap, internal/symbols
```

分层规则是**开着**的（`armed and clean`，不是 `undeclared`），但 34 个包里有
10 个不在任何分层声明里——所以那 10 个包的分层约束等于不存在。这是审计给出
的、发现清单给不出的结论。

## 三条设计约束

### 1. 目录只有一个口径

`engine.Catalog(cfg)` 是唯一的规则目录。在它之前存在两份：`rules explain list`
枚举 YAML 的 `languages.*.rules/metrics`，SARIF 用 `eng.Rules()`。前者漏掉了
**全部整图规则**——恰好是承载着剩余价值的那几条（架构、第二阶）。

现在 `rules explain list` 和 `rules gaps` 读同一份目录，并且补了 `[graph]` 段，
列出每条整图规则和武装它的策略键：

```
[graph]
  - layer-violation                    armed by architecture.layers
  - forbidden-edge                     armed by architecture.forbid
  - import-cycle                       armed by architecture.layers or architecture.forbid
  - sdp-violation                      armed by architecture.sdp
  - unknown-dependency                 armed by dependencies.allow / dependencies.self (or a go.mod)
  - function-clone                     armed by clones.enabled
  - metric-regression                  needs a flag: check -diff -regress
```

顺带修掉一个真 bug：度量规则从来不在目录里（SARIF 靠"不认识的 id 一律给个
通用标题"糊过去）。现在 `metrics.IDsFor(lang)` 提供语言→度量的单一来源，
`TestCatalogCoversEveryMeasuredMetric` 拿真实运行产出的快照对着它验，防止漂移。

### 2. 阈值用引擎的解析，不是策略原文

"这条度量规则能开火吗"取决于**生效**阈值：内置默认值打底，策略覆盖在上。
直接读策略会得到 0——于是一个策略里没提到、靠内置 15/25 在跑的
`cyclomatic-complexity` 会被判成"没有阈值、不可能开火"。

`engine.EffectiveThresholds` 现在是唯一的解析入口，引擎自己和审计都调它。

### 3. 一条规则一个结论

目录按"规则 × 语言"存条目，所以一个 Go 仓库会带 13 条永远不可能开火的 Java
条目；整图规则还会同时出现在目录和整图表里。审计按 id 合并：最"活"的状态
胜出（`firing > armed > report_only > disabled > undeclared > flag_only`），
发现数取该规则的总数，只有一种语言适用时才标语言。

不合并的直接后果是：一条在 Go 里开火的规则，会被同时报成"Java 也开火了"——
而这个仓库一个 Java 文件都没扫。

## 顺带给出的东西

- **hot rules**：某条度量在超过 25% 的测量对象上开火（且至少 3 条发现）。
  只对度量规则算，因为只有它有精确分母（每个测量值无论是否越界都记快照）。
  给 AST 规则编一个分母会得到一个"看起来像比率、其实不是"的数字。
  本仓库没有规则上榜——24/数千个函数不是"阈值错了"，是"代码里确实有 24 处"。
- **suppressed**：按规则细分的压制计数。一条被压制很多次的规则是团队投票反对
  的规则，策略要么改，要么这条规则该退役。
- **layers / unlayered**：每个声明的分层实际覆盖了哪些包，哪些包不在任何分层里。
  用的是 `arch.AssignLayers`，与检查器自己的匹配规则同源，审计不会和检查器
  在重叠 pattern 上给出不同答案。

## 与价值论证的关系

`docs/value-proof.md` 里"策略完备性是瓶颈"这一条，此前是推断。现在它是可测的：
`rules gaps` 给出每个仓库的未武装规则数、无分层包数、被压制次数。

仍未测量的是 R5（门是否真在 agent 循环里被调用）。那是接线层的事，见
`docs/workflow.md`。

## 复现

```bash
go install ./cmd/metricgate
metricgate rules gaps .
metricgate rules gaps . -format json
go test ./internal/policygap/... ./internal/engine/... ./cmd/metricgate/...
```
