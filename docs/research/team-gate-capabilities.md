# 团队级门禁能力调研（2026-10-13）

> 本文只记录结论、证据与设计方案，不代表授权实现。
> 复现基线：HEAD `85d71e7`，工作区未提交态；`go build -o /tmp/mg-self ./cmd/metricgate`。
> 所有数字均为实测，标注「未测」的是本机缺少样本、只能按机制论证的项。

## 0. 结论摘要

**判断：团队功能与工具能力不冲突，因为它俩不在同一层。**

已实现的是**判定层**（一个仓库内，代码→判定→出口，全部本地完成）。团队场景要补的是**团队层**——
策略怎么下发、趋势怎么汇、让步算谁的、成品怎么拿到手、命中怎么找到 owner。
团队层搬运的是判定结果，不生产判定；判定仍然发生在每个仓库的 CI 里。

| 层 | 内容 | 现状 |
|---|---|---|
| L-A 判定层 | 规则/度量/架构/增量/ concede 机制 | **已完整**，且是相对竞品的真差异 |
| L-B 团队层 | 策略分发 · 趋势汇聚 · 身份审计 · 成品分发 · owner 联动 | **缺 L1/L2/L4/L5；L3 已交付一半**（`metricgate suppressions` + accept ledger 已存在，但 owner 是自由文本） |
| L-C 中心服务层 | 上传代码/判定到服务端 | **明确不做**（`docs/capabilities.md:312`「不做服务端」） |

团队层五条建议的优先级（依据 = 对团队的阻塞程度 × 实现成本）：

| # | 方向 | 阻塞什么 | 预估成本 | 是否动判定语义 |
|---|---|---|---|---|
| L1 | 组织策略继承 | N 个仓库的策略漂移；改一条红线要开 N 个 PR | 中（~250 行 + 一处加载链改造） | 否 |
| L4 | 成品分发 | 没人能装上；action 需要 Go 工具链 | 中（4 个 workflow/yaml + 文档，零 Go） | 否 |
| L3 | 让步身份化 | 让步的 owner 无身份背书，审计不可执行 | 小（~80 行） | 否 |
| L5 | CODEOWNERS 联动 | 命中无法路由到责任团队 | 小（~120 行，只读文件） | 否 |
| L2 | 趋势汇聚 | 团队债的收敛/恶化不可见 | 大（需先定共享模型） | 否 |

**五条全部不动判定语义**——这是本次调研最重要的结论：团队层可以在完全不碰"什么算违规"的前提下补齐。

---

## 1. 定位：团队层必须是「本地判定的搬运与聚合」

这一节是后面五条设计的共同前提，先说清楚，否则每条都会走偏。

一次门禁判定有三个不可让渡的性质，全部由"本地判定"保证：

1. **确定性**：同一份代码 + 同一份策略 = 同一个结论。中心服务一旦引入"服务端策略版本"或"服务端二次判定"，可复现性就没了。
2. **零网络**：`internal/` 与 `cmd/` 无任何网络客户端（grep 命中只有 cockpit 的静态服务）。代码不出机器是护城河，也是和 SonarQube/CodeRabbit/Qodo 的唯一区别。
3. **无信任成本**：门禁判定不需要信任任何第三方；中心化判定等于要求团队信任服务商。

因此团队层的形状只能是：

```
组织策略 ──(git)──► 仓库本地 policy.yaml ──► 本地 CI 判定 ──► findings ──► 汇总视图
      下发                              生产                      搬运
```

**下发走 git**（不是 HTTP），**汇总只搬运结论**（不是代码）。这条约束决定了 L1 用 submodule/文件包含而非 URL include，L2 用共享文件/artifact 而非服务端入库。

---

## 2. L1 · 组织策略继承

### 2.1 现状证据

- `internal/config/config.go:276-277`：`Config` 已带 `SourceKind`、`SourcePath`、`ExplicitKeys`（`yaml:"-"`），`LoadWithSource(data, source, sourcePath)` 在 `:298`，`collectYAMLKeys` 在 `:321-323`。** provenance（这个键是显式写的还是默认的）已经存在**。
- `preset.go` 的 `presetWins`/`markPreset`（`:197-301`）已经是"两个来源合并 + 谁赢记下来"的完整实现，previously 用于 builtin ↔ preset。
- 但 grep `include|extends|base_policy|inherit` 在 `internal/config/` **零命中**（只有 explain.go 的"文件是否被 include"无关命中）。
- 唯一加载路径：`cmd/metricgate/main.go:102 loadPolicy(path)`，单文件。
- `Config` 顶层字段固定为 Clones/Version/Preset/Gate/Exclude/Languages/Dependencies/Architecture/Store——**没有承载"另一份策略"的字段**。

### 2.2 缺口

每个仓库一份完整 `policy.yaml`。组织级红线（"全公司禁 `System.out.println`"、"服务必须分层"）无处安放，导致：

- N 仓库 = N 份策略漂移，且漂移不可见（没有"我的策略和组织基线差在哪"的命令）。
- 新仓库 onboarding 要从零抄策略。
- 组织改一条红线要开 N 个 PR，且每个 PR 的 diff 噪声淹没真正的代码变更。

### 2.3 设计方案：`extends:` + 差异段 + 来源标注

```yaml
# policy.yaml（仓库侧，只写差异）
version: 1
extends: ./org-policy/policy.yaml      # 组织基线（git submodule / 同仓公共目录）

languages:
  go:
    metrics:
      cyclomatic-complexity: {warn: 12, error: 18}   # 本仓库更严
```

合并规则（直接复用 preset 的既有机制，不新造）：

| 维度 | 规则 |
|---|---|
| 标量阈值 | 本仓库显式值赢；未写则继承基线 |
| 列表（bans/layers/exclude） | **并集**，不是覆盖（组织禁的必须一直在） |
| 结构（languages.go） | 深合并到叶子键 |
| `ExplicitKeys` | 合并后重新计算，保留"这个值是显式写的"标注 |
| `SourceKind` | 从 `policy_file` 变成 `policy_file+extends`，并在 `rules explain`/`constraints digest` 里显示（**来源透明是 ledger 之外的第二个可审计性支点**）|

三个必须一起做的配套，否则会制造新问题：

1. **策略可复现**：`store.runs` 当前列是 `id/started_at/duration_ms/tool_version/git_commit/files/*_count/project_root/archived`——**没有策略指纹**。继承后同一个代码在不同基线下结论不同，趋势曲线就不可信。必须加 `policy_digest` 列（值已有：bundle 里就在算 policy sha256），并允许 `-format json`/SARIF 里带出。
2. **基线变更要重 gate**：`.github/workflows/metricgate-pr.yml` 的 `paths` 已含 `policy.yaml`，需扩展为"policy.yaml 或基线指针（submodule 指针 / 基线目录内任何文件）变更 → 全量重 gate"。否则组织改基线时存量代码不会重新判。
3. **禁止静默失效**：基线文件缺失/解析失败必须显式报错（`extends: 打不开`），不能退化成"只用本仓库策略"——静默降级会让一条组织红线无声消失，属于本次调研反复出现的「silent zero」同类。

**排除的方案**：URL include（破零网络）、服务端策略下发（破无服务端）、通配符覆盖（`!` 取消语义）——都不要。

### 2.4 成本与风险

- 改动集中在 `internal/config`（core，加载链）与 `cmd/metricgate/main.go loadPolicy`，约 250 行；`preset` 的合并机制可复用大部分。
- 主要风险是**合并语义的边角**（列表并集 vs 覆盖、深层级 languages 表）。缓解：给每个合并规则配一个单测，并在 `rules explain` 输出继承来源。
- 自检风险：`internal/config` 属 core，其自身要过 `check . -diff`。

---

## 3. L2 · 趋势与 gate 状态的团队可见性

### 3.1 现状证据（实测）

- `.metricgate/history.db` = **568,053,760 字节（568 MB）**（`ls -la`，2026-10-13 12:35；同一文件本会话早期为 350 MB，**在持续增长**）。
- `internal/store/store.go:68 Open(path)`：单文件 SQLite（`modernc.org/sqlite` 纯 Go），无 pragma、无 WAL、无连接池配置、无保留策略。
- 三张表 + 三个索引；`migrateRunsProjectRoot` / `migrateRunsArchived` 是**原地 ALTER**（为兼容旧库），说明 schema 演进会一直背着历史包袱。
- grep `retain|prune|DELETE FROM runs|vacuum` 在 store 与 cmd **零命中**——没有任何保留/清理机制。
- `trends` 已有 `-format json`（`cmd/metricgate/trends.go:86`、`:118` 两处 `json.MarshalIndent`）——**导出 seam 已经存在**。
- `cockpit serve` 硬编码 loopback（`cockpit_cmd.go:41-42 isLoopbackAddress`，非 loopback 直接 `fail`），设计上就不共享。
- `runs` 表已有 `project_root`（可空，iter-7 加的），`store.ProjectRoots()` 已能列出所有项目根——**按项目拆分的趋势查询能力已具备**。

### 3.2 缺口

三个独立的洞：

1. **没有共享模型**：SQLite 单文件锁在一台机器；568MB 也不适合进 git。
2. **没有保留策略**：每次 `check` 都写一条 run，无上限。团队级使用时（CI 每次 PR 一条）增长速度是单人使用的数十倍。
3. **策略指纹缺失**（见 2.3 配套 1）：趋势曲线无法区分"代码变好了"和"阈值改了"。

### 3.3 设计方案

分两步，先做能立刻产生团队价值的那一步：

**L2-a（立即可做）：导出 → 团队已有看板**

- `trends -format json` 已经是完整结构（`TrendReport` + `MetricTrend`），**补一个 `-out` + `-since` + `-project`** 就能让 CI 定期把 JSON 推到 artifact/对象存储，由 Grafana/内部看板消费。
- 成本约 40 行，零新依赖，不碰存储模型。
- 这是"搬运结论"而非"共享判定"的典型形态，完全符合 §1 约束。

**L2-b（需要先决策）：共享库 or 聚合器**

三个候选，各有代价：

| 候选 | 做法 | 代价 |
|---|---|---|
| 共享文件 | `store.path` 指向 NFS/共享盘，`trends` 直接读 | 需 WAL + busy_timeout（当前都没配），并发写要处理 `database is locked`；跨机房不行 |
| 聚合器 | 一个极小的本地服务收 findings JSON 写库 | 引入一个新进程 = 新的运维面，且要想清楚"它是否算服务端"——**只收结论不收代码，仍在 §1 边界内**，但需要显式声明 |
| 分布外置 | 每个 CI 把 run 作为 artifact 上传，周任务聚合 | 无并发问题，但延迟以天计 |

建议顺序：L2-a → 评估聚合器 → 最后才考虑共享文件。**共享文件看起来最简单，其实并发正确性最贵。**

**保留策略**必须与 L2-b 同期定，否则 568MB 会变成 5.6GB。可选：按 run 数上限 + 按时间窗口 + "只归档已归档运行"三选一，默认按时间窗口（团队关心的是近期曲线，远古 run 的边际价值趋零）。**默认值必须保守（比如保留 90 天）并可通过 policy.yaml 覆盖。**

---

## 4. L3 · 身份与让步审计（**已交付一半**）

### 4.1 现状证据（实测，与上一轮汇报不同，这里已经有货）

- **accept ledger 存在且校验严格**：`internal/accept/accept.go:162 Validate()` 强制 rule/file/line>0/**owner**/reason，expires 必须是 RFC3339。注释原话：`an anonymous concession defeats the ledger`。
- **`metricgate suppressions` verb 已存在**：`cmd/metricgate/accept_cmd.go:328 cmdSuppressions` + `:406 printSuppressions`，已有测试 `accept_cmd_test.go:170 TestSuppressionsReportCountsWhyAnonymous`——`metricgate: ignore` 指令从"只有计数"变成了"可逐条列出的审计面"。（这是闭环可用性迭代的交付物，调研时容易漏掉。）
- 当前 ledger 只有 1 条：`rule: fan-out / file: cmd/metricgate-mcp/main.go / line: 35 / owner: roadmap-iter1 / expires: 2026-12-31`。
- **owner 是自由文本**：Validate 只查非空，不校验它对应任何真实身份。
- **git 作者身份完全没被读取**：grep `user.email|--author|user.name` 在 `internal/`+`cmd/` 只命中 `internal/gittest/gittest.go:27-28`（测试夹具设假身份）。生产代码从未读过 git author。
- 让步在 gate 输出里可见（`text.go` 的 `[accepted by ledger]` tag + "Accepted warnings" 段），**但 ledger 只对 warning 生效**（`accept_cmd.go` annotateAccepted 跳过非 warning，P0-C 已在 `accept add` 入口显式拒绝 error 级）。

### 4.2 缺口

`owner: roadmap-iter1` 是一个**标签而非身份**。团队场景下三个具体后果：

1. 无人能验证"批这条让步的人是否有权批"。
2. 让步到期无人处理：`accept.IsStale()`/`Expired()` 在生产 `cmd/` 代码里**从未被调用**（grep 只命中测试），过期让步只在用户手动 `accept list` 时可见。
3. 无法回答"这个仓库的让步集中在谁/哪个团队"——这恰是团队治理最常问的问题。

### 4.3 设计方案

**L3-a（小，~80 行）：owner 身份化**

- `accept add` 默认从 git 取作者：`git config user.email`（经 `internal/git`，不新增 exec 点），`-owner` 仍可覆盖但记录 `owner_source: git|explicit`。
- ledger 条目加 `owner` 之外的可选 `team` 字段（从 `user.email` 的域名段推断，如 `@infra.example.com` → `infra`），`suppressions`/`accept list` 输出按 team 分组统计。
- **不做登录/ RBAC/ token**——那是 §10 明确不做的中心服务面。

**L3-b（小）：过期让步主动告警**

- `check` 结束时若有 stale 让步，打印 `note: N accepted concession(s) expired and now count as open: <rule> <file>:<line>`。
- `metricgate suppressions` 增加 `-stale-only`。
- 这一步只是把已经算好的 `IsStale()` 接到输出上，**零新语义**。

**L3-c（中，与 L5 合并）：让步归属校验**

- 若仓库有 CODEOWNERS，`accept add` 时若当前 author 不是目标文件的 owner，打印 `note: <file> is owned by @<team>; this concession is recorded under your name anyway`（**提示而非阻断**——阻断会把工具变成流程警察，且离线场景下 CODEOWNERS 可能不可得）。

---

## 5. L4 · 成品分发（团队能不能装上）

### 5.1 现状证据（实测）

- `git remote -v` **为空**——仓库没有远端，任何"发布"动作都还不具备物理前提。
- `cmd/metricgate/main.go:27 const toolVersion = "0.1.0"`——版本是硬编码常量，无 ldflags 注入机制。
- README **无 install 段**：Quick start 直接是 `go build -o metricgate ./cmd/metricgate`。
- 已有 CI 资产：`.github/workflows/{metricgate.yml, metricgate-pr.yml, metricgate-schedule.yml}` + `.github/actions/metricgate-check/action.yml`（composite）。
- composite action 的 "Build the gate" 步骤是 `go build -o "$RUNNER_TEMP/metricgate" ./cmd/metricgate`——**要求调用方 CI 里有 Go 工具链**，且需要 checkout 整个仓库源码。
- PR workflow 已规范：`paths` 覆盖 9+1 语言与 policy.yaml，`permissions` 只声明 `checks: write` + `pull-requests: write`（最小权限用得对）。
- action inputs：base/path/policy/go-version/sarif/max-cognitive/max-cyclomatic；outputs findings-json/sarif-file/errors/warnings。

### 5.2 缺口

| SaaS/团队必需 | 现状 | 后果 |
|---|---|---|
| 安装入口 | 只有 `go build` | 非 Go 团队装不上；本工具主要面向多语言仓库的使用者，**这个矛盾最刺眼** |
| 预编译二进制 | 无 release workflow、无 tag | 无法 `uses: metricgate/check-action@v1`（composite action 必须同仓或用 tag 引用） |
| 版本注入 | 硬编码 0.1.0 | 无法从二进制判断 CI 里跑的是哪一版；`-format json`/SARIF 不带构建版本，出问题无法归因 |
| Marketplace 上架 | action.yml 已写好但未发布 | 团队无法 `uses:` 一键接入 |
| MCP 目录 | Smithery/mcp.so/glama.ai 均未提交 | agent 用户发现不了 |
| 升级提示 | 无 | 团队会长期停留在旧版，新红线不到位 |

### 5.3 设计方案

按对团队的价值排序，全部是 CI/文档工作，**零 Go 改动**（除版本注入）：

1. **版本注入**（`:27` 常量 → `var toolVersion = "dev"` + `-ldflags "-X main.toolVersion=$TAG"`），并在 release workflow 里注入 tag。这是唯一需要动 Go 的一处，且让 `metricgate version` 变成可信信息。
2. **release workflow**：push tag `v*` 时交叉编译 5 平台（linux/darwin × amd64/arm64 + windows amd64）+ `checksums.txt` + 附 `policy.example.yaml`。
3. **composite action 升级为"可用 tag 引用"**：发布 `v1` tag 后 `uses: metricgate/check-action@v1` 即可；同时**保留源码构建路径**给想固定版本/内网镜像的团队（现在的形态对私有化其实是优点，别删）。
4. **预编译产物用于 CI**：action 增加一个 `install: release|build` 输入（默认 release 用下载的二进制，`build` 走现在的 go build），让非 Go 团队也能零工具链接入。
5. **README 安装段**：三种方式（下载 release / `go install` / composite action），并给出"内网离线"路径（`build` 输入）。
6. **MCP 目录提交清单**：Smithery / mcp.so / glama.ai 各需一份 manifest（名称、描述、install 命令、需要 `-config` 与 `-calllog` 的说明）。**这条只在前 5 条落地后才有意义**——目录里放一个装不上的包是负资产。

---

## 6. L5 · CODEOWNERS 联动

### 6.1 现状证据

- 仓库内**没有 CODEOWNERS 文件**（`find` 零命中），代码里零引用。
- 已有可复用的 glob 词表：`internal/rules/banned.go:429 fileMatches` / `:439 filePatternMatch`——支持精确路径、前缀（`cmd/app/`）、后缀（`_test.go`）、对 basename 与全路径各跑一次 glob、前导 `**/` 表示任意深度。语义已在 `filematch_test.go` 钉住。
- `check` 输出已带 root-relative 路径（iter-9 的 root 一致性工作的成果），与 CODEOWNERS 的路径同一坐标系。

### 6.2 设计方案（只读 + 提示，不进 gate）

- 新增 `internal/sources/codeowners.go`（或复用 `internal/sources`）：解析 CODEOWNERS/GitHub CODEOWNERS 语法（`path @team`，`*` 通配，后匹配优先），产出 `owner(dir/file) → team[]`。
- **只做两件事**：
  1. `check` 报告的命中行后追加 `  [owner: @infra]`——**纯展示**，让人知道该找谁。
  2. `check -diff` 的 gate 摘要里统计"本次新增命中中有多少落在非作者所属 team 的文件"，输出 `note: k of n new findings are in files owned by other teams (@infra: 2, @scan: 1)`。
- **不进 gate、不阻断**：owner 路由是流程问题，一旦变成 error 就会逼人改 CODEOWNERS 来绿灯，那是把度量靶子化。
- 附带收益：给 L3-a 的 team 推断、L1 的组织维度分组提供同一个解析器。

---

## 7. 横向约束（五条建议共同遵守，不可协商）

1. **判定留在本地**：团队层不下发判定、不携带代码出去。任何设计如果要求"把代码或 findings 发到远端服务"，直接否决。
2. **策略可复现**：任何"策略来源"的变更必须留下指纹（policy digest + 基线 commit sha），否则确定性声明失真。
3. **禁止静默降级**：基线缺失、CODEOWNERS 解析失败、store 打不开——都必须显式报错或显式 note，不允许退化成"看起来干净"。
4. **来源可审计**：每个生效值的来源（explicit / preset / extends / builtin）必须在 `rules explain` / `constraints digest` 的输出里可见，与 accept ledger 的 owner/reason/expiry 构成两条互补的审计链。
5. **不动判定语义**：L1–L5 全部只改"策略怎么来、结论怎么搬、责任怎么归因"，**不许**借着团队化的名义调阈值、加规则、改 -diff 语义。

---

## 8. 已具备的复用车件（做团队层时不要重造）

| 需要的能力 | 已有实现 | 位置 |
|---|---|---|
| 合并来源 + 记录谁赢 | `presetWins` / `markPreset` | `internal/config/preset.go:197-301` |
| 值是否显式设置 | `Explicit` / `ExplicitKeys` | `internal/config/config.go:355` |
| 策略内容指纹 | bundle 的 policy sha256 | `docs/` 实测 `08aa3604…` |
| 文件 glob 匹配 | `fileMatches` / `filePatternMatch` | `internal/rules/banned.go:429/:439` |
| 趋势 JSON 出口 | `trends -format json` | `cmd/metricgate/trends.go:86/:118` |
| 按项目查趋势 | `runs.project_root` + `ProjectRoots()` | `internal/store/store.go:444-459` |
| 让步校验与过期 | `Entry.Validate` / `IsStale` / `Expired` | `internal/accept/accept.go:162/:83/:140` |
| 抑制指令审计 | `metricgate suppressions` | `cmd/metricgate/accept_cmd.go:328` |
| 最小权限 CI | PR workflow permissions | `.github/workflows/metricgate-pr.yml` |
| 跨平台打包 | 无（要新建） | — |

---

## 9. 分期建议

| 期 | 内容 | 完成判据 | 预估 |
|---|---|---|---|
| **T1** | L4 分发（版本注入 + release workflow + action tag + README install + MCP 目录） | 新人能在 3 条命令内装上并在 CI 跑通 | 零 Go（除 ldflags），半天 |
| **T2** | L3 让步身份化 + 过期告警（L3-a/L3-b） | `accept add` 默认带 git author；`check` 提示 stale 让步 | ~80 行，半天 |
| **T3** | L5 CODEOWNERS 只读联动 | 命中显示 owner；diff 摘要统计跨 team 命中 | ~120 行，半天 |
| **T4** | L2-a trends 导出增强（`-out`/`-since`/`-project`） | CI 能定期产出可分包的 JSON | ~40 行，2 小时 |
| **T5** | L1 组织策略继承（`extends:` + 合并 + policy digest + 重 gate） | 组织改基线 → 全仓重 gate；`rules explain` 显示继承来源 | ~250 行，1-1.5 天 |
| **T6** | L2-b 共享模型 + 保留策略 | 团队趋势可见，DB 有上限 | 需先决策，未估 |

T1–T4 互不依赖、都不动判定语义，可作为一批连做；T5 单独一轮（它改动配置加载链，是唯一有回归风险的一项）；T6 依赖 T5 的 policy digest，否则趋势不可归因。

---

## 10. 明确不做

| 不做 | 理由 |
|---|---|
| 中心化判定服务 | 破坏零网络 + 无信任成本；`docs/capabilities.md:312` 已明确 |
| 上传代码/findings 到第三方 | 同上；这是与竞品唯一的区别 |
| 登录 / RBAC / token / 多租户 | 团队层不需要服务端；身份用 git author + CODEOWNERS 就够 |
| 组织级"必须"强制（服务端下发且本地不可覆盖） | 让仓库失去本地自治，且离线场景直接失效 |
| URL include / 远程策略拉取 | 破零网络；策略分发走 git submodule 或同仓公共目录 |
| 把 owner 未匹配做成 gate error | 度量靶子化；只做提示 |
| Postgres/MySQL 后端 | 现代 SQLite 纯 Go 已是当前定位下的最优解；换库意味着引入运维面 |

---

## 附：复现命令

```bash
cd /Users/mac/Downloads/metricGate
go build -o /tmp/mg-self ./cmd/metricgate

# L1 证据
grep -rn 'include\|extends\|base_policy\|inherit' --include='*.go' internal/config/ | grep -v _test
sed -n '270,330p' internal/config/config.go          # SourceKind / ExplicitKeys / LoadWithSource

# L2 证据
ls -la .metricgate/history.db                        # 568 MB，无保留策略
grep -n 'pragma\|WAL\|journal' internal/store/store.go   # 零命中
/tmp/mg-self trends -format json | head -30          # 导出 seam 已存在

# L3 证据
cat .metricgate/accepted.yaml                        # owner: roadmap-iter1（自由文本）
/tmp/mg-self accept list
/tmp/mg-self suppressions                            # iter-12 已交付的审计面
grep -rn 'user.email' --include='*.go' internal/ cmd/ | grep -v gittest   # 生产零命中

# L4 证据
git remote -v                                        # 空
grep -n 'toolVersion' cmd/metricgate/main.go | head -3   # 硬编码 0.1.0
grep -n 'install\|brew' README.md                    # 零命中

# L5 证据
find . -name CODEOWNERS -not -path './.git/*'        # 零命中
sed -n '429,450p' internal/rules/banned.go           # 可复用的 glob 词表
```
