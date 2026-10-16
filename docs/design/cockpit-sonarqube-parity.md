# Cockpit 复刻 SonarQube 交互与界面：调研分析与实施方案

**状态**：调研结论 + 复刻方案提案，尚未实现。

调研日期：2026-10-14。目标：在**本地工具**前提下复刻 SonarQube 的交互与界面（不是复刻它的架构）。

关联：竞品定位见 [`../strategy/competitor-analysis.md`](../strategy/competitor-analysis.md)；同类"外部参考整理"文体见 [`open-codereview-reference.md`](open-codereview-reference.md)。

## 0. 一句话结论

**不需要找"类似的开源产品"，SonarQube 的前端本体就是开源的**：`SonarSource/sonarqube-webapp`（LGPL-3.0，TypeScript + React 19，最后提交 2026-10-14，活跃）。它是 SonarQube Community Build 的完整 Web UI 源码，约 5.5 MB TS/CSS，逐页逐组件对应我们想复刻的交互。后端 `SonarSource/sonarqube`（LGPL-3.0，Java）同期开源。

所以本次调研有两件事：

1. **参考源确定**：以 `sonarqube-webapp` 为唯一权威交互参考（读结构、抄交互语义，不搬运代码，见 §6 许可证）。
2. **本地化改造方案**：把它的服务端依赖剥掉，接到 metricGate 已有的 `runs / findings / metric_snapshots / accept 账本 / policy` 上。

## 1. 参考源清单（按契合度排序）

| 仓库 | 星数 | 语言/栈 | 许可证 | 与本项目的契合点 | 不适配点 |
|---|---|---|---|---|---|
| `SonarSource/sonarqube-webapp` | 12（2024-12 才拆库） | React 19 + Vite + NX + Tailwind(`sw-` 前缀) + d3 | LGPL-3.0 | **SonarQube 官方 UI 全量源码**。Issues/Overview/Coding Rules/Quality Gates/Activity/Code 逐页组件树齐全；含 `.gitar/review/*.md` 设计规范与 `AGENTS.md`（可直接喂 agent） | 5.5 MB、NX monorepo、Node 24 + yarn 4；数据层是 `@tanstack/react-query` + REST，全部假设有服务器 |
| `@sonarsource/echoes-react` | npm 包 | React + Mantine + Radix + CSS 设计令牌 | LGPL-3.0 | SonarSource 官方设计系统（Echoes）。design token 表 `--echoes-*`，是"审美在线"的机器可读来源 | 依赖 React 19 / Mantine / emotion，与零依赖 embed 前端冲突 |
| `SonarSource/sonarqube` | 11.1k | Java | LGPL-3.0 | `server/sonar-webserver-webapi` 是 Web API 的事实定义（facet、分页、bulk change 的参数形状）；`sonar-webserver-pushapi` 定义实时推送 | 体量与我们的单二进制完全不是一回事，只读语义不读实现 |
| `SonarSource/sonarlint-vscode` | 628 | TS/CSS | LGPL-3.0 | **"没有服务器时如何呈现同一套问题语义"** 的官方答案：IDE 本地分析、本地问题面板、状态流转。对我们最有形态参考价值 | VSCode 扩展宿主，不是独立 Web UI |
| `DependencyTrack/frontend` + `dependency-track` | 178 / 4.3k | Vue 3 / Java | Apache-2.0 | 同一类"扫描结果分诊台"：左侧 facet 过滤 + 右侧问题表 + 策略门禁 + 历史趋势。**Apache-2.0，可搬运比 LGPL 干净** | 领域是 SCA 依赖漏洞，非代码审美；界面密度低于 SonarQube |
| `DefectDojo/django-DefectDojo` | 5.0k | Python/Django + BS5 | BSD-3 | 多扫描器结果归一（engagement / finding / severity / 状态机 reopen·false-positive）。**状态流转语义最完整**，与 metricGate 的 accept 账本同构 | 审美是典型 Django-Bootstrap，不是我们要的界面语言 |
| `google/perfetto` | 6.7k | C++ + TS UI | Apache-2.0 | 纯前端 trace viewer（打包后本地打开，无服务器）——**"本地优先 + 重交互 trace"的最佳工程范式**，对我们 trace_attrs 视图有参考价值 | 领域是系统 trace，时间轴/泳道，不呈现质量规则 |
| `JetBrains/Qodana` | 402（仅 docs） | — | Apache-2.0 | qodana.sarif 输出格式；报告形态参考 | **报告 UI 未开源**，不能当交互源 |

**排除项**（搜索过程中确认不合适）：aixigo/energyimpact 插件（2★，领域不同）、`qualor`/`polyscan`/`lens`/`Vett` 等自称 "SonarQube alternative" 的新仓库（0–2★，无 UI 沉淀）、`GrimoireLab`（工程分析，非质量门禁）、`ReviewBoard`（人工评审，非扫描）、`semgrep`（引擎，无 UI）、`grafana`（通用可视化，7.7k★，过重且领域无关）。

### 1.1 sonarqube-webapp 的可用结构（本次调研实测）

```text
apps/sq-server/src/main/js/apps/<feature>/
  components/     页面组件（IssuesApp.tsx / IssuesList.tsx / IssueDetails.tsx / BulkChangeModal.tsx）
  sidebar/        facet 过滤器（StandardFacet / SoftwareQualityFacet / … 每 facet 一文件）
  hooks/ utils/ routes.tsx  styles.css
  __tests__/      *-it.tsx 交互测试 —— 这是"交互契约"的可执行文档
libs/
  shared/                 与后端无关的通用逻辑
  sq-server-commons/      api/ queries/ design-system/ l10n/ hooks/
  feature-dashboards/     可编辑仪表盘（widget 网格）
  feature-quality-gate-history/
```

三个可以直接借鉴的东西：

1. **`.gitar/review/*.md`**：design-system / general-design / module-boundaries / conventions 四份评审规则，是 SonarQube UI 的设计约束明文（例如"优先语义化组件 prop，禁止裸 Tailwind 上色""颜色只走 `cssVar` 设计令牌"）。可按同样文体写进 `docs/constraints/`，喂给我们的闸门循环。
2. **`.agents/skills/echoes-components/SKILL.md`**：他们把设计系统写成 agent skill——先查 `reference/components/*/index.d.ts`，设计令牌从 `design-tokens-*.json.d.ts` 推断 `--echoes-*` 变量。**这正是 metricGate"约束前馈给 agent"的同构做法**，可作为 `get_constraints` 的文体参考。
3. **`__tests__/*-it.tsx`**：IssuesApp-Mode / IssuesApp-Filtering / IssueApp-RuleDescription / IssuesSourceViewer 等交互测试。复刻交互时，"要复刻什么"可以直接从这些测试名反推成验收清单。

## 2. 概念映射：SonarQube → metricGate 现有数据

| SonarQube 概念 | metricGate 现状 | 差距 |
|---|---|---|
| Analysis（一次扫描快照） | `runs` 表：`started_at / duration_ms / tool_version / git_commit / files / error_count / warning_count / info_count / project_root / policy_digest / trace_id` | 基本齐。缺 `branch`（SonarQube 一等公民）——本地工具可先用 commit + `-diff` 口径替代 |
| Issue | `findings` 表（run_id + rule_id + severity + source + file + 行列 + message + trace_id）；`model.Finding` 另带 `Evidence{StartByte,EndByte,SnippetHash,AnchorStatus}` 与 `Accepted` | **`findings` 表没有主键、没有 status 列**。SonarQube 的状态机（Open/Accepted/False positive/Confirmed/Reopened）与评论无处落库 → §4.1 |
| Rule / Issue 详情"为什么" | `internal/rules.Rule{ID,Title,DefaultSeverity,Query}`；banned-api 有 `reason` / `use`；custom_rules 有 `reason` | 规则描述**已在源码/policy 里**，但没有可查询出口。缺 "More info"（外部文档链接）——本地工具不该联网，改为指向仓内文档路径 |
| Measure（组件级度量） | `metric_snapshots`：file/scope/metric/value/warn_threshold/err_threshold，13k+ 行/次 | 齐，且比 SonarQube 更细（带阈值）。缺的是**聚合与评级**（A–E），见 §4.3 |
| Quality Gate | `.metricgate.yaml` 的 `gate.fail_on` + 各 metric 的 warn/error 阈值 + `architecture.cycles/sdp` | 语义等价但形状不同：SonarQube 是"metric + 操作符 + 阈值"条件列表，我们是每 metric 双阈值。UI 需要一层**条件视图投影**（把阈值渲染成 `error > 0 ⇒ FAIL`），不改判定 |
| Quality Profile | `languages.<lang>.rules` 开关/降级 + `custom_rules` + `test_metrics` | 齐。现成 `rules gaps` / `explain` 已能出可解释视图 |
| New Code Period | `check -diff -base <ref>`（默认 HEAD）+ `internal/diff`；`regress`/`scorecard`/`-hidden` | 能力齐，**缺"周期"这个概念的持久化**：git ref / 时间窗在 UI 里要可选（我们已有 `store.Window`） |
| 技术债 / effort（分钟） | 无 | **数据不存在**。SonarQube 的 effort 来自规则内置常量。见 §5 决策 D3 |
| Assignee / 用户体系 | `accept.Entry{Owner}`（人名/迭代号）；CODEOWNERS（`internal/owners`） | 无用户体系。→ 决策 D2：用 CODEOWNERS + owner 字符串，**不引入登录** |
| Accept / False positive 工作流 | `.metricgate/accepted.yaml`：rule/file/line/**owner**/reason/**expires** —— 比 SonarQube 多了过期语义 | UI 侧只读展示。缺**写入路径**（在 cockpit 上点 Accept）→ §4.1 |
| Activity / 事件流 | `runs` + `trace_id` + `trace_attrs`；`mcp-calls.jsonl` 阶段（before/explore/verify/review） | 齐，且比 SonarQube 强（LLM 会话级溯源）。缺 UI 形态 |
| Component View（代码浏览 + 问题高亮） | `Finding.Evidence` 有 byte span + hash；本地直接读文件；无 SCM blame 依赖 | 可做，但**当前 dashboard 明确不返回源码**（`internal/cockpit/dashboard.go:71`）→ §4.4 |
| 评级 A–E | 无 | 需自定确定性算法（§4.3），不能抄 SonarQube 的（其阈值随版本变，属产品口径） |
| Portfolio / Application | `ProjectSummary` 多仓聚合（按 Git 根分组） | 等价，够用 |
| Webhooks / 通知 / 权限 / Marketplace / LDAP | — | **明确不复刻**（服务端专属），见 §5 D5 |

## 3. 逐页复刻方案（复刻什么 / 剥掉什么）

### 3.1 Summary（项目概览页）

- 复刻：Quality Gate 状态大横幅（Passed/Failed + 未达标条件列表）→ New Code / Overall Code 双 tab → measure 卡网格（每卡：指标名 + 值 + 评级色块 + 环比箭头）→ 下方分布表。
- 剥掉：ALM（Jira/GitHub PR）徽章、SonarLint 推广位、AI Code Status、付费 edition 门控、Issues in Sandbox。
- 本地增益：SonarQube 的图只能画 issue 数趋势，我们的 `metric_snapshots` 能画**复杂度分布 p50/p90/max 随阈值变化**（`MetricTrend` 已有），这是他们没有的。

### 3.2 Issues（问题分诊页）—— 优先级最高

- 复刻（`apps/issues/` 组件一一对应）：
  - 左 facet 栏：Status / Severity / Software Quality / Rule / Tags / Assignee / Scope / Creation date / Directory-file，**每 facet 带计数且计数随其它 facet 联动**（SonarQube 用 `facets` 一次返回）。
  - 右卡片列表：项目·文件路径 → 规则消息（可点）→ severity 下拉 → 状态下拉 → `L40 · 5min effort · 7 years ago` → 勾选框（Bulk）。
  - 顶部：总数 + 排序选择器（Priority / File name / Creation date，**选择记住**）+ Bulk Change。
  - 键盘：`↑↓` 选卡、`→` 进详情、`←` 返回、`f` 流转状态、`a` 指派、`m` 给我、`i` 改 severity、`c` 评论、`t` 改 tags、`Alt+↑↓` 多位置跳转。
- 剥掉：全局搜索 projects/branches、"My issues"（依赖登录）、服务端 New Code period 计算（本地用 `-diff` 预算结果落库）。

### 3.3 Issue 详情

- 复刻：四 tab —— Where is the issue?（代码视图 + primary/secondary location + flow）/ Why is this an issue?（规则说明）/ Activity（评论与状态历史）/ More info。三栏布局（左 issue 列表可滚、中代码、右"受影响的质量属性"）。
- 剥掉：Fix Suggestions（AI 修复建议，违反我们"不调用 LLM"的非目标）、Rule sandbox 标记、Jupyter 专用渲染。
- 替换：More info → 仓内文档链接（如 `docs/constraints/…`），零网络。

### 3.4 Activity / History

- 复刻：左分析时间线（每次 run 一事件：Version / Quality Gate change / Profile change / Definition change）+ 右侧 d3 折线（Graph type 选择、日期区间、Overall vs New Code 双线、底部缩放滑块）。
- 本地强化：我们的事件源不只是扫描——`mcp-calls.jsonl` 有 agent 的**三触点调用**（生成前/验证/复核）。左栏可以是 `scan / mcp-call / policy-change / accept-grant` 四类事件混排，右图叠加 accept 账本时间点。**"审美回路"可视化是 SonarQube 完全没有的叙事**。
- 剥掉：手动 Add event / Remove analysis（管理员功能）。

### 3.5 Rules 与 Quality Gate 配置页

- Rules 复刻：左 facet + 中列表 + 右详情（描述 / 参数 / 哪些 profile 激活了它 / 该规则的历史 issue 数）。等价物：内置规则包 + `custom_rules` DSL + `banned-api`。
- Quality Gate 复刻：条件表（metric + 操作符 + error 阈值 + 新建条件弹窗 + 生效项目列表）。**写回 `.metricgate.yaml` 必须走现有 `policy_write.go` 原子保存路径**，并继续呈现 provenance（哪个键来自哪个文件）——这是我们已比 SonarQube 多做的一步，别丢掉。

## 4. 要新建的东西（差距清单）

### 4.1 状态机落库（§3.2/§3.3 的前提）

`findings` 表无主键、无 status。加列不够——**同一 finding 跨 run 必须可追踪**，否则每次扫描后状态全部丢失。建议新增 `issue_states`，以**稳定指纹**为键（不依赖自增 id，兼容既有 additive-only schema 约束）：

```sql
CREATE TABLE IF NOT EXISTS issue_states (
  fingerprint TEXT PRIMARY KEY,      -- sha256(rule_id|file|message|anchor_scope)
  status      TEXT NOT NULL,         -- open|accepted|false_positive|confirmed|reopened
  owner       TEXT,                  -- CODEOWNERS 或字符串，无用户表
  comment     TEXT,
  decided_at  TEXT NOT NULL,
  expires     TEXT                   -- 沿用 accept.Entry 的过期语义
);
```

指纹必须与 `Evidence.AnchorStatus` 对齐：`exact` 用 byte span + snippet hash 校验（代码一改就自然失效 → 状态自动 reopen），`line_only` 退化为 line + rule + message。这与 `accept.Entry{rule,file,line,owner,reason,expires}` 语义合并，**避免"账本"和"状态"两套真相**。→ 落地前先决策 D1。

### 4.2 API 端点（现在只有 3 个）

现有 `/api/dashboard`（全量聚合）、`/api/runs/archive`、`/api/policy`。复刻分诊体验需要**服务端过滤 + 分面**，不能再让 `app.js` 客户端扫全表：

```text
GET   /api/issues         # 过滤(severity,status,rule,file,lang,window,newcode)+计数+排序+分页
GET   /api/issues/facets  # 与上分离，SonarQube 形状
PATCH /api/issues/state   # 单次状态流转，走 sameOrigin + 原子写
GET   /api/measures       # component-measures 等价：按 file/scope 聚合 + 评级
GET   /api/gate           # policy 阈值 → 条件列表投影 + 当前判定结果
GET   /api/rules          # 规则目录 + reason/use + 出处
GET   /api/source         # 按 finding 取代码窗口（见 §4.4 边界）
GET   /api/activity       # runs + calls + policy/accept 事件混排 + 趋势序列
```

**边界守住**：全部沿用 `withLocalAPIGuard`（loopback host 校验）+ `sameOrigin` + 严格 Content-Type + `MaxBytesReader` + `DisallowUnknownFields`（照 `decodeArchiveRequest` 的写法，`internal/cockpit/dashboard.go:338` 起）。写端点复用已有原子保存路径，不新造写文件逻辑。

### 4.3 评级算法（必须自己定义，且要可解释）

SonarQube 的 A–E 阈值是产品口径且随版本变。我们定义确定性规则并把公式显示在 UI 上，例如按**密度**：`debt_ratio = Σ weight(severity) / KLOC`，weight 取 `error=5, warning=1, info=0`（与 `gate.fail_on` 一致），A–E 断点写进 policy 而不是硬编码在前端。理由：符合"判定确定、阈值可调、出处可查"三条既有主张。

### 4.4 代码视图（有隐私/架构边界）

`internal/cockpit/dashboard.go:71` 明确"不暴露源码"。开 `/api/source` 要守住：

- 只返回**被 finding 命中的文件 + 窗口**（如 ±15 行），不返回目录树遍历，不做全文搜索；
- 从本地磁盘直读（loopback 场景下用户本来就能读自己的代码，不新增泄露面）；
- 留一条可回溯记录（读了哪些源码）；
- 高亮：不引 highlight.js 全量，按语言做**关键字/注释/字符串**三档极简着色（tree-sitter 在仓内，但浏览器侧不跑）。

### 4.5 前端工程形态（决策 D4）

现状 `app.js` 单文件 683 行 / 36 KB、零框架、`go:embed web/*`。复刻上述页面后组件数量约 40–60 个，**单文件不可维护**；但引入 React+NX+yarn4 会把"单二进制"变成"需要 Node 24 构建"。

## 5. 决策（需你选定，我再开工）

**D1 — 状态存哪：扩展 `accepted.yaml` 还是新建 `issue_states` 表？**

- A（推荐）**新建表，`accepted.yaml` 保留为导入源**：账本是文本、可 git、可 code review，这个优点不能丢；但跨 run 的状态与评论是高频写，放 SQLite 更合适。启动时一次性把 ledger 导入为 accepted 状态，写操作只写库 + 可选导出回 yaml（保持可评审性）。
- B 全部走 `accepted.yaml`：单一真相、可 git diff；代价是 UI 每次点状态都改文本文件，评论/历史难表达。

**D2 — 用户体系**：确定不做。Assignee 用 `owner` 字符串 + CODEOWNERS 建议值（`internal/owners` 已有路由能力）。UI 文案避免出现"用户/权限/登录"。

**D3 — effort（修复工时）**：SonarQube 的 "5min effort" 来自规则内置常量，我们没有。

- A（推荐）**不引入分钟数，换成"修复代价档位"**：由已有确定性信号给出——`Evidence.Anchor`（exact 更易定位）、metric 超标幅度 `(value-warn)/(err-warn)`、同规则在本仓的 accept 频率。UI 位置与文案照 SonarQube（`L40 · low effort · 3 days ago`），但底数是我们的度量，**不假装有工时数据**。
- B 按 rule_id 给常量表：省事，但等于凭空抄 SonarQube 的规则常量，对自定义规则无解。

**D4 — 前端构建形态**：

- A（推荐）**零构建多文件 ES 模块**：把 `web/` 拆成 `views/issues.js`、`components/facet.js` 等，靠 `<script type="module">` + 原生 DOM；`go:embed web/*` 不用改，无 Node 依赖，二进制仍是单文件。代价：没有 JSX，需要一层很小的 `h(tag, props, children)`。
- B 引入 Vite + Preact，构建产物再 embed：开发体验好、能直接对照 sonarqube-webapp 的组件写法；代价是仓库需要 `web/dist` 生成步骤 + 构建期网络（npm 安装）——**与"零网络"主张冲突**。
- C 原样继续单文件 `app.js`：只在 P0 范围内可行，做完 Issues + 详情 + Activity 会失控。

**D5 — 复刻范围**：SonarQube 的 `apps/` 有 30+ 功能目录（permissions/groups/users/marketplace/webhooks/system/maintenance/projectDump…）。**建议只做 8 个**：Projects、Summary、Issues、Issue 详情、Rules、Quality Gate、Activity、Code（组件视图）。其余明确标注"服务端能力，不复刻"，写进文档避免反复评估。

**D6 — 视觉基线**：SonarQube 是浅色高密度（Echoes 设计系统）。当前 cockpit 是深色（`--mint/--blue/--amber/--red`）。→ 复刻"布局与交互语义"，**保留我们自己的色板与深色基调**，只引入 design-token 的做法（变量表 + 语义命名）。理由：抄视觉会显得像仿品；抄 token 纪律才是抄到点上。

## 6. 许可证与"抄什么"

`sonarqube-webapp` / `echoes-react` 是 **LGPL-3.0**，metricGate 目前**仓内无 LICENSE 文件**（`git remote` 也未配置），所以没有"必须兼容"的既有义务。三条纪律：

1. **不搬运代码**：只读它的结构、命名与交互语义。LGPL 对"修改后分发"有传染约束，没必要为此背包袱。
2. **不搬运文案**：SonarQube 的 l10n 串（`libs/sq-server-commons/src/l10n/default.ts`）逐条不抄，我们自己写中文（本项目对话与 UI 均中文，`index.html` 已是 `lang="zh-CN"`）。
3. **可搬运的"方法"**：`.gitar/review/*.md` 与 `.agents/skills/*/SKILL.md` 是文档/规范文体，属"设计约束前馈给 agent"这一同构实践，可按本项目口径重写后引用出处。

## 7. 分批实施

| 批次 | 内容 | 依赖 | 规模 |
|---|---|---|---|
| P0-0 | `issue_states` 表 + 指纹算法 + 单测；`/api/issues` 过滤/分面/排序/分页；状态 PATCH | D1 | M（Go 为主，后面一切的前提） |
| P0-1 | Issues 页：左 facet 栏（计数联动）+ 右卡片列表 + 状态下拉 + 排序记忆 | P0-0 | L |
| P0-2 | Summary 页：Gate 横幅 + New/Overall 双 tab + measure 卡 + 评级 | §4.3 | M |
| P1-1 | Issue 详情四 tab + 代码视图（含 `/api/source` 边界） | §4.4 | L |
| P1-2 | Activity：事件时间线（scan + mcp-call + policy/accept）+ 折线与区间 | P0-0 | M |
| P1-3 | Rules 目录页（reason/use/出处）+ Quality Gate 条件视图 | §4.2 | M |
| P2-1 | Bulk Change（多选 + 批量流转）+ 键盘快捷键全套 | P0-1 | M |
| P2-2 | 可编辑仪表盘（`feature-dashboards` 式 widget 网格） | 全部 | L，可能不做 |
| P2-3 | 设计令牌化 + `.gitar` 式 UI 评审规范进闸门 | D6 | S |

建议先做 **P0-0 + P0-1**：这两步做完，"分诊 issue"这个 SonarQube 最核心的手感就立住了，后面的页面都是在同一套数据契约上加视图。

> 视觉重构这一支自 2026-10-15 起按屏拆成 CU-00..CU-04 五个 goal 并行迭代，入口与
> 契约见 [docs/iterations/cockpit-ui/](../iterations/cockpit-ui/README.md)（P0-1/P1-1/P2-3 的视图面落在那边，P0-0/P0-2 的存储与端点仍按本表推进）。

## 8. 风险

- **数据模型是真正的成本**：SonarQube 的 issue 有跨版本稳定 key，我们的 `findings` 没有。指纹设计错则状态会乱跳（同一问题每次扫描变新 id）。→ P0-0 先写性质测试：同代码重复扫描指纹不变；代码改动后 `exact` 锚点失效可观察。
- **一次全量 `/api/dashboard` 与"分面查询"共存**会造成两套聚合路径，违背"同一选择计划"的既有主张。→ 让 dashboard 端点逐步降级为概览专用，明细一律走 `/api/issues`。
- **P2-2 仪表盘**容易变成"什么都想做"的入口，与现有 cockpit 视图重复。→ 推迟到 P0/P1 稳定后再评估。
- 若选 D4-B（Vite+Preact），会把 Node/npm 引入构建链，**动摇"零网络单二进制"卖点**。除非明确接受，否则不选。
- 键盘快捷键与表单/下拉在同页共存易冲突（SonarQube 自身也有此类缺陷报告）。→ P2-1 需要输入态抑制规则。

## 9. 待你确认

1. D1–D6 的选择（尤其 **D4 前端形态** 和 **D5 范围**，直接决定工期与是否引入构建链）。
2. 起始批次是否按 **P0-0 + P0-1**。
3. 是否同时把 `.gitar/review/*.md` 的方法落地成 `docs/constraints/cockpit-ui-review.md`（给闸门循环消费，让 UI 改动也过 `check_code`）。

## 附：本次调研的可复核证据

- `api.github.com/repos/SonarSource/sonarqube-webapp`：created 2024-12-06，pushed 2026-10-14，license LGPL-3.0，size 91.3 MB（git），12★/14 fork。
- `GET /git/trees/master?recursive=1`：4417 条目；`apps/sq-server/src/main/js` 下 TS/TSX/CSS 合计约 5.5 MB；`apps/issues/` 组件 27 + sidebar facet 22 + `__tests__/` 交互测试 10。
- `package.json`：React 19.2 / Vite 8.1 / @tanstack/react-query 5.56 / d3-* / highlight.js 11 / Tailwind 3.4（`sw-` 前缀）/ `@sonarsource/echoes-react` 6.0；engines node ≥24.21；yarn 4.17。
- `@sonarsource/echoes-react` npm：6.0.0，LGPL-3.0，deps 含 @mantine/core、@radix-ui/*、sonner。
- 截图样本（本地 `/tmp/sq/`）：`sq-issues.png` `sq-summary.png` `sq-issue-detail.png` `sq-issue-activity.png` `sq-activity.png` `sq-accept.png` `sq-card.png`，来源 docs.sonarsource.com（2026.5 LTA）。
- metricGate 侧核对：`internal/cockpit/dashboard.go:71,284-287,398-408`（端点、不暴露源码、loopback 守卫）、`internal/store/store.go:26-73`（表结构，findings 无主键/无 status）、`internal/model/model.go:46-68`（Finding + Evidence + Accepted）、`internal/accept/accept.go:27-39`（Entry 含 owner/expires）、`.metricgate.yaml`（gate/metrics/architecture）。
