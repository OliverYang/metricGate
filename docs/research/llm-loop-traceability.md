# LLM 会话链路可追溯性调研（2026-10-13）

> 本文只记录结论、证据与需求草案，不代表授权实现。
> 复现基线：HEAD `85d71e7`，工作区未提交态；`go build -o /tmp/mg-self ./cmd/metricgate`。
> 所有数字均为实测；本机缺少样本的项标注「未测」。

## 0. 结论摘要

**判断：值得做，而且要单独排一轮，但它不是"再加一类数据"，而是"给已有数据一根轴线"。**

MetricGate 现在手里已经有这条链路上的五份中间数据，问题是它们**各说各话、没有共同的键**：

| 链路环节 | 现有中间数据 | 已有维度 | 缺的关键维 |
|---|---|---|---|
| 输入（提示词/上下文） | `bundle` 信封 | `PolicyInfo.Digest`（sha256）、`schemaVersion`、`findingSchema` | 提示词指纹、模型、宿主 |
| 策略 | `policy.yaml` | 全文 | **指纹没进 store/SARIF/JSON**（只在 bundle 里） |
| 工具介入 | `.metricgate/mcp-calls.jsonl` | 14 字段（session/tool/phase/target/verdict/errors…） | trace id、模型 |
| 判定结果 | `history.db` | runs 11 列 + findings 10 列 + snapshots 7 列 | **trace id、policy digest** |
| 让步 | `accepted.yaml` + `suppressions` | rule/file/line/owner/reason/expires | 无法按 trace 归因 |

**核心结论：判定侧的四个环节都已经在留痕，缺的是一根 `trace_id` 把它们串起来，以及"LLM 侧元数据"（模型、提示词指纹、宿主）这三个目前完全没记的维度。**

一旦串起来，有五个问题是现在**结构上答不出、串起来后一行查询就能答**的（见 §5），而这三个问题恰好覆盖 claims.md 里长期缺证据的 #15/#16/#17。

**优先级**（按"是否是其他方向的前置"排，不按工作量）：

| 期 | 内容 | 为什么这个顺序 | 预估 |
|---|---|---|---|
| **X0** | **policy digest 落库 + 进输出** | 团队级策略继承（L1）的前提，也是本方向的前提——没有它，趋势曲线分不清"代码变好"和"阈值改了" | ~60 行 |
| **X1** | **trace_id 贯通四张表 + MCP Record** | 一切组合查询的前提；纯增量、默认空、不改判定 | ~150 行 |
| **X2** | 轻量 `trace_attrs` KV（白名单键） | 让"模型/提示词指纹"这类快变维度不改 schema | ~80 行 |
| **X3** | 组合报表 verb（`trace`/`trends -trace`/cockpit 过滤） | 数据齐了才有意义 | ~120 行 |
| **X4** | LLM 侧元数据自动采集 | 依赖宿主配合（env/CLI 传参），**放最后** | 待宿主形态定 |

---

## 1. 定位：为什么这是 MetricGate 顺手能做的独特资产

三个别人没有的条件，凑在一起才成立：

1. **判定是确定性的**。同一代码 + 同一策略 = 同一结论，所以"换模型/换提示词/换策略"造成的差异**可以归因**。用 LLM 做判定的产品做不到这一点——它们的基线本身在漂移。
2. **判定已经发生在前置/后置两个位置**。`calllog` 的 `Phase` 把 before/verify/review/explore 分开记（`internal/calllog/calllog.go:43-60`，`toolPhases` 是 total map，新工具落 `other` 而不是消失）。这是"工具介入了没有、介入在哪个环节"的现成答案。
3. **所有数据都已在本地**。`mcp-calls.jsonl` 的包注释写明三条约束：**不记源码、只在本地、绝不打断工具调用**。trace 必须继承同一条边界。

反过来说，**最大风险是把它做成"LLM 质量平台"**——那会立刻撞上 `docs/capabilities.md:312`「不做服务端」和确定性护城河。§6 的约束就是防这个。

---

## 2. 五份中间数据的实测清单

| 数据 | 位置/形态 | 实测维度 | 缺 |
|---|---|---|---|
| MCP 调用 | `.metricgate/mcp-calls.jsonl`，JSONL，0600 | `Time/Session/Tool/Phase/Target/ProjectID/ProjectPath/ProjectName/MS/Findings/Errors/Warnings/Verdict/Err`（`internal/calllog/calllog.go:99-113`，共 14 字段） | `TraceID`、`Model` |
| 运行 | `history.db` `runs` | `id/started_at/duration_ms/tool_version/git_commit/files/error_count/warning_count/info_count/project_root/archived`（11 列） | `trace_id`、`policy_digest` |
| 命中 | `history.db` `findings` | `run_id/rule_id/severity/source/file/start_line/start_column/end_line/end_column/message`（10 列） | `trace_id` |
| 度量 | `history.db` `metric_snapshots` | `run_id/file/scope/metric/value/warn_threshold/err_threshold`（7 列） | `trace_id` |
| 让步 | `.metricgate/accepted.yaml` | `rule/file/line/owner/reason/expires`（`internal/accept/accept.go` `Entry`） | 无法按 trace 归因 |
| 策略指纹 | `bundle.PolicyInfo.Digest` | `sha256:<hex>` + `Source`（`internal/bundle/bundle.go:510-511`） | **只活在 bundle 里**，`store`/SARIF/JSON 都拿不到 |

**一个已经存在但没人看见的键**：`cmd/metricgate/check_cmd.go:406` 调 `id, err := st.SaveRunForProject(...)`——**run id 已经落库，但没有任何输出暴露它**。agent 或人拿到 findings 时，无法指回"这是哪次运行"。X1 顺手把它带出来即可，成本几乎为零。

**另一条坐标系裂缝**：`mcp-calls.jsonl` 的 `session` 是**进程级**（callLogger 的 session 标识，一个进程一个），而 `runs.id` 是**check 级**。一次会话里通常有多次 check，所以 session → run 是一对多，无法互相替代，只能靠第三把键（trace）桥接。

---

## 3. 三个根因

### T1 · 没有贯穿链路的键

session（进程级）、run id（check 级，未输出）、project_root（仓库级）、git_commit（提交级）四把键各管一段，**没有任何一把能同时回答"这次 LLM 会话/这个 PR/这次任务"**。组合报表只能做到 today×趋势，做不到"这个 trace 的判定如何随模型/策略变化"。

### T2 · LLM 侧三个维度完全缺席

- **模型**：`reviewBundleIn`（`cmd/metricgate-mcp/main.go:124-132`）只有 Path/Base/From/To/Commit/MaxChars/MaxTokens/IncludeSource——**输入侧完全没有模型、宿主、提示词的位置**。
- **提示词指纹**：`bundle` 算了 policy 的 sha256（`:510-511`），但**提示词/任务描述的指纹不存在**。于是"提示词改了以后质量有没有变"没有基线。
- **宿主/harness**：不知道是 CLI agent、IDE 还是 MCP 客户端，无法区分"工具介入"与"人工提交"。

### T3 · 报表维度是预置的，不能组合

`trends` 是固定聚合（时间 × 规则），cockpit 是固定视图。用户问"按模型 × 策略 × 阶段切一遍"时，只能自己导出 JSON 在外面对。X3 的目标是让组合发生在工具内、以同一个确定性口径产出。

---

## 4. 需求草案

### 4.1 trace ID（X1）——定义与传播

**定义**：一个字符串，覆盖"一次 LLM 会话 / 一次 PR / 一次任务"这个粒度。**不是** run id、**不是** session。

**生成规则**（关键是"可空 + 优先外来"）：

```
1. 若环境变量 METRICGATE_TRACE_ID 非空 → 用它（宿主/ harness 已有时，不自造）
2. 否则若 MCP session 存在 → 派生一个并固定到进程生命周期内
3. 否则留空
```

理由：不自造第二套 trace 协议。现成 harness 若带 W3C traceparent / OTLP，直接复用比新定义更有互操作性；不带的场景（纯 CLI）退化为空，**空 trace 不影响任何功能**。

**必须一起定的四条规则**：

| 规则 | 内容 |
|---|---|
| 可空 | 没有 trace 时一切照旧，不报错、不留空行、不改判定 |
| 单向可见 | trace 只出现在**输出**（JSON/SARIF/trends/cockpit），不作为输入参与判定 |
| 不跨仓 | trace 只在本地解释，不上传、不跨机器复制后继续沿用 |
| 长度上限 | 截断（如 128 字符），防止一行垃圾污染 JSONL |

**传播路径**（四处都要，少一处就断链）：

```
env METRICGATE_TRACE_ID
  ├─► cmd/metricgate-mcp/calllog.go  → Record.TraceID（新字段，omitempty）
  ├─► cmd/metricgate/check_cmd.go    → runs.trace_id / findings.trace_id / snapshots.trace_id
  ├─► internal/bundle                → 信封顶层（已有 SchemaVersion 旁边）
  └─► cmd/metricgate-mcp/main.go     → reviewBundleIn 增加可选 trace_id（回显，便于对齐）
```

### 4.2 policy digest（X0）——为什么必须第一个做

现在 `runs` 表**没有策略指纹**。后果不是"少一列"，而是：一旦策略继承（团队层 L1 的 `extends:`）落地，同一份代码在两条基线下结论不同，`trends` 曲线会把"阈值改动"画成"质量变化"。

- 复用 `bundle.PolicyInfo.Digest` 的算法（`:510-511`，sha256）+ `Source`。
- 落 `runs.policy_digest`，并在 `-format json` / SARIF 的 run/properties 里带出。
- 兼容旧库：可空列 + 幂等 `ALTER TABLE`（照 `migrateRunsArchived` 的既有写法）。

### 4.3 trace_attrs KV（X2）——为什么不用固定列

模型、提示词指纹、宿主这类维度**变化太快**：今天记 `model`，下个月要 `prompt_template_id`、`temperature`、`tool_count`。固定列每加一个都要 migration + 改三处查询。

设计：`trace_attrs(trace_id TEXT, key TEXT, value TEXT)`，**键白名单**（`model` / `prompt_digest` / `harness` / `agent` / `branch` / `tool_invocations`），值长度上限 256。

- 白名单是为了防止 KV 表变成垃圾场（谁都能往里写任意键，报表就不可信）。
- 未知键**丢弃并计数**，和 DSL 规则丢弃非法条目同一个纪律。

### 4.4 组合报表（X3）

三个入口，口径一致：

| 入口 | 形态 |
|---|---|
| `metricgate trace <id>` | 单 trace 全景：调用了哪些工具（按 phase）、哪些 check、结论、让步 |
| `metricgate trends -trace <id>` / `-model` / `-prompt-digest` | 在既有 TrendReport 上加过滤 |
| cockpit | trace 过滤框（复用现有 runs/projects 视图） |

**输出必须是表格 + JSON 两种**，因为用户要"不同维度组合"，组合结果最终会进 CI 或看板。

---

## 5. 五个从此可回答的问题（价值证明）

| # | 问题 | 需要的维度 | 现在为什么答不出 |
|---|---|---|---|
| Q1 | 提示词改了以后，error 率降了吗？ | `prompt_digest` × findings.severity | 提示词指纹不存在，无法分组 |
| Q2 | 从 A 模型换到 B 模型后，`fan-out`/`npath` 是否上升？ | `model` × metric | 模型维度不存在 |
| Q3 | 工具介入（先 get_constraints 后 check）是否提高首过率？ | `phase=verify` × `verdict` | phase 有、verdict 有，但**无法按 trace 归到"一次任务"**，session 与 run 是两把键 |
| Q4 | 复杂度上升是业务迭代带来的，还是 LLM 产出带来的？ | `git_commit` × churn × trace | commit 有，但分不清"谁的 commit" |
| Q5 | 策略收紧后，哪些 trace 的结论翻转了？ | `policy_digest` × trace | digest 只在 bundle 里，store 没有 |

Q3 是五个里最值钱的，因为它直接命中 claims.md 里 #15「闸门在真实 agent 循环里被调用/findings 被修掉」这个长期 ⏳——**不是没数据，是数据和判定不在一条链上**。

---

## 6. 约束（不可协商）

1. **不记提示词原文、不记模型输出、不记源码**——与 `internal/calllog/calllog.go` 的三条约束同源。指纹（sha256）可以，原文不行。
2. **trace 不参与判定**。它只出现在输出侧；一旦进判定，确定性就被破坏。
3. **只在本地**。没有远端端点，`mcp-calls.jsonl` 的注释把这个写死了（"no remote endpoint to enable later"）。
4. **键白名单 + 值上限**，未知键丢弃并计数。
5. **空 trace 是合法状态**，不允许为了凑数据制造空壳 trace。
6. **不动判定语义**——与团队层五条建议同一条红线。

---

## 7. 与其他方向的关系

```
X0 policy digest ──► 团队层 T5（组织策略继承） 的前置
                  └─► 本方向 X1 的前置（不然趋势不可归因）

X1 trace_id     ──► X2（attrs 挂在 trace 上）
                  └─► X3（报表按 trace 切）

团队层 L2（趋势汇聚）── 若做聚合器，trace 是它的天然维度键
```

**结论：X0 要和团队层的 T5 合并排期，X1–X3 自成一轮。** 拆开的原因：X0/X1 是纯增量且互相独立，X3 的收益完全依赖前两者落地。

---

## 8. 分期与成本

| 期 | 内容 | 完成判据 | 预估 | 风险 |
|---|---|---|---|---|
| X0 | policy digest 列 + 幂等迁移 + JSON/SARIF 带出 | 旧库打开正常；`-format json` 有 `policyDigest`；digest 与 bundle 一致 | ~60 行 | 低 |
| X1 | `trace_id` 贯通 4 处 + run id 输出 | 设置 env 后四处都带同一 id；不设置时行为与现在 byte 级一致 | ~150 行 | 低（纯增量） |
| X2 | `trace_attrs` KV + 白名单 | 未知键被丢弃且有计数；值超长被截断 | ~80 行 | 中（KV 表易滥用，靠白名单压） |
| X3 | 组合报表 | `trends -model X` 与手工导出一致 | ~120 行 | 低 |
| X4 | LLM 侧元数据采集 | 宿主不配合时优雅退化为空 | 待定 | **高**（依赖外部，可能只能做约定文档） |

X0–X3 合计约 400 行，全部是纯增量、不改判定语义、无新依赖。

---

## 9. 明确不做

| 不做 | 理由 |
|---|---|
| 记提示词/模型输出原文 | 隐私边界；只存指纹 |
| 自建 trace 协议（不用 W3C/OTLP 语义） | 生态已有标准，env 名对齐即可，别当发明者 |
| 模型质量评分 / 模型排行榜 | 越界成 LLM 评测平台；判定确定性是护城河不是评分器 |
| trace 上传/跨机聚合 | 与"零网络、不做服务端"直接冲突；跨机搬运只以导出文件形式 |
| 在判定里使用 trace（限流/降级） | 一旦进判定，同一份代码结论可变 |
| 固定列记录 LLM 维度 | schema 演进代价高于 KV |

---

## 10. 答用户的问题：追加还是先调研？

**已按"先调研"做完，这就是调研产物**（本文档）。结论是**追加为独立一轮，并且排期有依赖顺序**：

- **X0 必须和团队层的 T5 合并**（它是策略继承的前置，早做早省事）。
- **X1–X3 独立成一轮**，不依赖任何其他方向，可随时开工。
- **X4 移到"待宿主形态确认"**，不占排期——它可能最终只是一份约定文档。

如果只做一件，做 **X1**（trace 贯通）：它是唯一能让"已存在的五份数据"突然开始互相说话的一步，且风险最低。

---

## 附：复现命令

```bash
cd /Users/mac/Downloads/metricGate
go build -o /tmp/mg-self ./cmd/metricgate

# 五份中间数据的形状
head -1 .metricgate/mcp-calls.jsonl | python3 -m json.tool     # 14 字段，无 trace/model
sqlite3 .metricgate/history.db '.schema runs'                  # 无 trace_id / policy_digest
sed -n '99,113p' internal/calllog/calllog.go                    # Record 全字段
sed -n '/CREATE TABLE IF NOT EXISTS runs/,/^)/p' internal/store/store.go
sed -n '/CREATE TABLE IF NOT EXISTS findings/,/^)/p' internal/store/store.go
sed -n '/CREATE TABLE IF NOT EXISTS metric_snapshots/,/^)/p' internal/store/store.go

# policy digest 已存在但只在 bundle 里
sed -n '505,515p' internal/bundle/bundle.go                    # PolicyInfo{Digest: "sha256:"+hex}
grep -rn 'PolicyInfo' --include='*.go' internal/ cmd/ | grep -v _test

# run id 已落库但未输出
grep -n 'SaveRunForProject\|SaveRun(' cmd/metricgate/check_cmd.go
grep -rn 'SaveRunForProject(' --include='*.go' cmd/ | grep -v _test

# MCP 输入侧没有模型/提示词位置
sed -n '124,132p' cmd/metricgate-mcp/main.go                   # reviewBundleIn 全字段
```
