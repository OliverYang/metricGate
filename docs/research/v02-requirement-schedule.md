# v0.2 需求总排期（一条主线，六个迭代）

> 汇总自 `docs/research/team-gate-capabilities.md`（团队层 T1–T6）与 `docs/research/llm-loop-traceability.md`（链路层 X0–X4）。
> 本文只排期与定序，不代表授权实现细节；每个迭代开工时仍按 AGENTS.md 三触点走。
> 基准：HEAD `85d71e7`，工作区 148 条未提交。

## 0. 为什么是一条主线

两份调研表面是两个方向，实测后发现**它们共享同一根轴线，且互相是对方的前置**：

| 需求 | 归属 | 依赖 |
|---|---|---|
| X0 policy digest 落库 | 链路层 | **无** |
| T5 组织策略继承（`extends:`） | 团队层 | **X0**（继承后必须有指纹，否则趋势曲线分不清"代码变好"和"阈值改了"） |
| X1–X3 trace + 组合报表 | 链路层 | X0（同一条归因链） |
| T1 分发 / T2 让渡身份 / T3 CODEOWNERS / T4 trends 可用性 | 团队层 | 独立 |

所以：**X0 与 T5 合并成一个迭代，X1–X3 一个迭代，T2/T3/T4 各自独立，T1 随时可做。**

## 1. 六个迭代（按依赖与"是否为别人前置"排序）

### 迭代 14 · X0 policy digest + T5 组织策略继承
**为什么最先**：唯一一个同时是两个方向前置的项；也是唯一有回归风险的项，早做早上测试。

| 内容 | 判据 |
|---|---|
| `runs.policy_digest` 列 + 幂等迁移（照 `migrateRunsArchived` 既有写法） | 打开 568MB 旧库正常，新列可空 |
| digest 算法复用 `internal/bundle/bundle.go:510-511`，**两处必须 byte 级一致** | 同一 policy.yaml 在 bundle 与 store 得到同一 hex |
| `-format json` / SARIF run properties 带出 digest | 机器消费方不再重算 |
| `extends:` 单继承 + 唯一 `package_policy` per key | 循环/歧义报错，不静默取其一 |
| 继承来源标注 `SourceKind/SourcePath` | 复用 `preset.go:197-301` 已验证的双源合并+溯源机制，不新造 |

依赖：无。风险：**中**（T5 是唯一会改变"哪条策略生效"的一项，需要 `config_test.go` + `preset_test.go` 补继承矩阵）。
规模：~310 行。

### 迭代 15 · X1 trace_id 贯通
**为什么第二**：唯一能让五份已有数据突然开始互相说话的一步，且纯增量、零回归风险。

| 内容 | 判据 |
|---|---|
| `METRICGATE_TRACE_ID` env → 4 处传播（calllog `Record.TraceID`、`runs`/`findings`/`metric_snapshots`、bundle 信封、`reviewBundleIn` 回显） | 设置 env 后四处同一 id |
| **顺手补 `run id` 输出**（`check_cmd.go:406` 已落库但从未暴露） | JSON/SARIF 带 `runId`，findings 可指回运行 |
| 空 trace 是合法状态 | 不设置 env 时行为与现在 byte 级一致（这条是验收主项） |

**坐标系警告（必须在设计里写死）**：calllog 的 `session` 是进程级、`runs.id` 是 check 级，一对多——trace 是第三把键，**不能替代任何一个**。
依赖：无。风险：低。规模：~150 行。

### 迭代 16 · T2 让渡身份（git 作者）+ 让步审计
**为什么第三**：`IsStale()`/`Expired()` 至今**从未被生产代码调用**，过期让步只在用户手敲 `accept list` 时可见——这是规范性缺口，不是功能缺口。

| 内容 | 判据 |
|---|---|
| `accept add` 从 git 取作者作默认 owner，`-owner` 仍可覆盖 | repo 内一条命令即可让渡，无需手打 `-owner me` |
| 过期让步在 gate 输出里显式提示（调用既有的 `IsStale`） | 过期即计入 open，不再静默 |
| `suppressions` 报表扩展到 ledger 统一视图 | 两种让步机制一个视图 |

依赖：无（与 X1 可并行，但错开做避免同一批 `main.go`/`accept_cmd.go` 冲突）。
风险：低。规模：~120 行。

### 迭代 17 · T3 CODEOWNERS + T4 trends 可用性
**两者都不碰判定，且都是"读已有数据的新出口"**，适合合成一个迭代。

| 内容 | 判据 |
|---|---|
| 解析 `CODEOWNERS` + `.github/CODEOWNERS`，复用 `banned.go:429 fileMatches` 词汇 | 与 policy glob 同源，不引入第二套匹配 |
| `reachability` / `impact` 输出归属列；`rules gaps` 按 owner 分组 | 未匹配到 owner 的记录显式标 unknown，不猜 |
| `trends -out <file>` / `-since` / `-project` | 报表能落地、能按项目切（`runs.project_root` 已在 store.go:468） |

依赖：无（T3 建议排在 T2 后，让 owner 语义先稳定）。
风险：低。规模：~160 行。

### 迭代 18 · X2 trace_attrs + X3 组合报表
**为什么最后**：收益完全依赖 X1 落地；也是四个里唯一"给用户新查询面"的一项。

| 内容 | 判据 |
|---|---|
| `trace_attrs(trace_id, key, value)` KV + 键白名单（model/prompt_digest/harness/agent/branch/tool_invocations） | 未知键丢弃**并计数**（同 DSL 纪律），值截断 256 |
| `metricgate trace <id>` 单 trace 全景 | 一次调用串起工具介入(phase)×检出×让步×运行 |
| `trends -trace/-model/-prompt-digest` | 与手工导出 JSON 结果一致 |
| cockpit trace 过滤框 | 复用既有 runs/projects 视图 |

依赖：X1。风险：中（KV 表易滥用，靠白名单压）。
规模：~200 行。

> **状态：✅ 已完成**（2026-10-14）。交付与五性核对见
> [docs/iterations/iter-18-trace-attrs-and-report.md](../iterations/iter-18-trace-attrs-and-report.md)。
> 亮点：X2 白名单丢弃有计数、X3 四入口同源（CLI `trace` / MCP `get_trace` /
> `trends -trace|-model|-prompt-digest` / Cockpit 下拉）；`Window.TraceIDs` 用
> nil-vs-空 区分"全部 trace"与"无匹配"，`trends -model nope` 明确回答 0 runs。
> 自检抓到 14 条新 warning 全部当场修掉，并顺带消掉 store.go 的 file-length error。
> 遗留：X4（宿主设 env，工具不探测）、Cockpit trace 过滤未覆盖 run 视图。

### 迭代 19 · T1 分发与版本（发布动作）
**为什么独立排**：它的主体是仓库外动作（remote、tag、Marketplace），代码量极小（`toolVersion` 改 ldflags），但**每一步都要外部确认**。

| 内容 | 判据 |
|---|---|
| `toolVersion` 从硬编码 `"0.1.0"`（`cmd/metricgate/main.go:27`）改 ldflags 注入 | `go build -ldflags "-X main.toolVersion=vX"` 生效 |
| README 补 install 段（当前 Quick start 直接从 `go build` 开始，零安装说明） | 新人照 README 能装上 |
| 发版流水线 + v1 tag + Marketplace / MCP 目录 | 需用户确认远程与账号 |

依赖：无。风险：低（代码）/ 外部依赖（发布）。
规模：~30 行代码 + 外部动作。

## 2. 不做的（明确边界）

| 不做 | 理由 |
|---|---|
| 集中判定服务、上传代码/findings | 撞 `docs/capabilities.md`「不做服务端」+ 零网络护城河 |
| 记录提示词/模型输出**原文** | 只存指纹；与 `calllog` 三条约束同源 |
| trace 参与判定（限流/降级/差异化阈值） | 一旦进判定，同一份代码结论可变，确定性崩塌 |
| 固定列记录 LLM 维度 | schema 演进代价高于 KV 白名单 |
| Postgres / 登录 / RBAC / 多租户 | 团队层调研 §10 已否 |
| 服务端强制组织策略 | 组织约束在仓库里流通，不由服务端下发 |

## 3. 排期总表

| 迭代 | 主题 | 规模 | 依赖 | 风险 | 是否改判定语义 |
|---|---|---|---|---|---|
| 14 | X0 policy digest + T5 策略继承 | ~310 行 | — | 中 | **T5 会**（继承改变哪条策略生效） |
| 15 | X1 trace_id 贯通 + runId 输出 | ~150 行 | — | 低 | 否 |
| 16 | T2 让渡身份 + 让步审计 | ~120 行 | — | 低 | 否 |
| 17 | T3 CODEOWNERS + T4 trends | ~160 行 | — | 低 | 否 |
| 18 | X2/X3 trace 属性 + 组合报表 | ~200 行 | 15 | 中 | 否 |
| 19 | T1 分发与版本 | ~30 行 | — | 低 | 否 |

合计约 970 行 Go + 六个迭代日志。迭代 14 之后**每一项都不碰判定语义**，这是刻意的：把唯一有回归风险的项放在最前面，后面全是纯增量。

## 4. 每个迭代的固定收尾

1. `gofmt -l internal/ cmd/` 空
2. `go build ./...` 通过
3. `go test ./...` 仅允许既有失败 `internal/digest TestBuildTaskFilteredBans`（digest_test.go:389，用户既有脏工作）
4. `go run ./cmd/metricgate check . -diff -base HEAD -config policy.yaml` 通过（fail_on=error；`digest.go:377 banSection cyc 16` 与 `explain_cmd.go:45 cmdExplainPreview cyc 18` 是既有错误，仅在非 -diff 全量时可见）
5. `git diff --check` 干净
6. `docs/iterations/iter-N-*.md`（按 iter-13 体例：为什么做/交付物/设计决策/实测证据/单测/五性核对/阈值纪律/验证/遗留）
7. `docs/roadmap.md` §五 + `docs/backlog-v1x.md` 优先级表回填

## 5. 顺序可调的两处（需要你拍板）

1. **迭代 19（分发）可以随时提前**——它对外部确认的依赖高于对代码的依赖。如果你打算先解决"别人用不上"，它可以插到最前面。
2. **迭代 16 与 15 可交换**——两者文件重叠小，但都改 `main.go` 附近；我默认 15 先做（trace 是主线的轴线）。
