# 迭代 3 · 补引导与诚实空态（T3.1–T3.4 + T2.5/T2.6）

> 角色：PM（方案）｜依据：`docs/COCKPIT_产品验收报告.md` 迭代 3 节、`docs/COCKPIT_产品设计文档.md` §4.2/§4.3/§5.1/§5.3/§5.7/§六/§十 D3
> 前置：迭代 1（`iter-05-p0-availability-and-scope.md`）已收口，scope 契约与慢接口错误态可用
> 本轮口径：**只并入与"引导/空态"同主题的收口残留 T2.5、T2.6**；迭代 2 的门禁矛盾（T2.1–T2.4）与 source 窗口（T2.7/T2.8）不在本轮

## 0. 一句话目标

让"没有数据"和"数据太多"两种极端都不再是死路：空态说明为什么空并给下一步，海量默认收敛并给放宽入口，错误态不再和"0 个"同屏自相矛盾，规则页从"138 个配置键"变成能回答"这条规则为什么存在"的目录。

---

## 1. 病灶归并（4 个 P2 → 2 个根因）

| 根因 | 表现 | 涉及 |
|---|---|---|
| **C：数据口径只呈现"全量"和"该项目全history"两个极端** | issues 默认 3.7 万条不可翻；runs 全局页把"未归属"与"全部"混为一谈；项目卡片 6 张长得一样、进空项目是死路 | T3.1 T3.2 T3.3 |
| **D：状态呈现缺少"我不知道"这一档** | 请求失败时同屏显示"0 个问题 / 第 1/1 页 / 暂无数据"；服务端 `http.Error` 的正文被前端通用文案吞掉 | T2.5 T2.6 |

归并的意义：C 是一次数据契约改动（新增"最新 1 次运行"这一档 + 项目三态字段），D 是一处状态机改动（error 时抑制计数与 facet）。两者互不阻塞，可前后端并行。

---

## 2. 代码事实（本轮方案的落点依据，均已实测核对）

### 2.1 前端 `internal/cockpit/web/app.js`（现 2782 行）

- `RELOAD_KEYS` :261 = `["view","severity","status","rule","file","query","sort","page","scope"]` —— **新增任何影响取数的 URL 参数必须同时加进这里**，否则改了 hash 不重新取数。
- `ROUTE_KEYS` :626 起是路由双向表（参数名 / state 字段 / 省略值）；加参数 = 加一行，不要写分支（历史上分支版把 encoder/decoder 双双推过复杂度阈值）。
- `fetchIssues(filters)` :475 —— `limit: 60` **硬编码在 :480**；`scope: scopeParam(state)` :486。
- `VIEWS.issues.render` :1164 起 —— 计数行 :1180 `${s.issues.total} 个问题`、页码 :1181、范围行 :1182、列表 :1184（`s.error ? loadError(s) : ... : "没有匹配的问题"`）。**T2.5 的病灶就在 :1180–1182 与 :1184 的兄弟关系**：error 时上面三行照写 0。
- `facetPanel`（rules facet :1883）同样在 error 时显示"暂无数据"。
- `VIEWS.runs` :1582；`runsInScope` :1103 → `scopeAggregate` :1078：`s.scope === null`（全局）时把 **store 的未归属 runs 与每个项目的 runs 求并集**，:1605 `runsScopeNote` 已诚实写成"全部项目与未归属记录（含 N 条未归属运行）"，但 :1593 大标题仍是"活跃运行"、侧栏 :35（index.html）标签仍是"质量运行"。
- `projectCard` :1368 —— 只有 `calls`/`flagged`/`latestActivity` 三个数，**无 runs 维度**；`unassignedCard` :1378。
- `VIEWS.rules` :2048 —— 读 `s.policy.keys`（`policyKeys` 只发键名与出处，取值恒空，见 :1614/:1818 的 privacy-note），:2073 自我声明"规则目录缺口"。
- `api()` :396 起：`!response.ok` 时 :420 `throw new Error((body && (body.error || body.message)) || \`请求失败 (${response.status})\`)` —— **JSON 正文已经能透出；只有 `text` 不是 JSON 时 `body=null`（:414 catch）才退化成通用文案**。这决定了 T2.6 是"服务端改发 JSON"而不是"前端改解析"。
- 服务端非 JSON 错误正文的实据：`internal/cockpit/dashboard.go:333` `http.Error(w, "dashboard data unavailable: "+err.Error(), 500)`（纯文本）、:362 `"history store is disabled"`、:383/:388 `"invalid archive request"`。

### 2.2 后端

- `internal/cockpit/issues_api.go:102 parseIssueQuery` —— `limit` 允许 1–500（`defaultWindowLimit = 60` :136），**没有"最新 1 次运行"的专用参数**，但 `limit=1` 天然就是那一档（`windowFromParams` :147）。
- `internal/store/window.go:14-27 Window{Limit,Since,ProjectRoot,Unassigned,TraceIDs}`；`runScope(archiveMode)` :125 已能渲染 `project_root = ?` / 未归属 / `started_at >= ?`。
- `internal/cockpit/scope.go`：`scopeRunWindow(scope)` :243 = `store.Window{Limit:1, ProjectRoot, Unassigned}` —— **"scope 内最新 1 次运行"这个查询在后端已经存在**（gate/measures 在用），本轮只是把它交给 issues 与项目卡片。
- `internal/cockpit/dashboard.go:162 attachProjectRuns` :179 `st.LatestRunsForProject(20, project.Root)` —— **每项目 runs 截断在 20 条**，所以卡片上的"运行数"不能拿 `len(runs)` 冒充总数（要么加 `runTotal` 字段，要么文案写"最近 20 次"）。
- `internal/config/explain.go:175 (c *Config) ExplainRule(lang, ruleID string) RuleExplain` —— 返回 `{Enabled, Reason, Severity, Thresholds, PolicySource, Note}`；`Reason` 是**机器码词表**（`configured` / `language_not_configured` / `custom_rule` / `rule_not_found` / `builtin`…），**不是人话**。
- `internal/rules/rules.go:66 type Rule{ID, Title, DefaultSeverity, DefaultEnabled, ...}`；:322 `ForLanguage(id parser.LanguageID) []*Rule` —— `Title` 是英文一句话（如 `"printStackTrace() bypasses the logging pipeline"` java_rules.go:140）。
- `internal/cockpit/dashboard.go:292-303 Handler()` 是注册点，加 `/api/rules` 就在这里加一行。

---

## 3. 数据契约（前后端唯一事实源）

### 3.1 issues 的"最新 1 次运行"档

**不新增参数**，复用 `limit`：

- 前端默认发 `limit=1`（项目/未归属/全局三个 scope 都一样），`status=open` 由前端默认选中（见 §4.3）。
- 响应 `window` 字段（已存在，:495 注释说"服务端实际查询的范围"）必须如实写 `last 1 run`，前端范围行直接显示。
- "放宽到最近 60 次运行"= 前端把 `limit` 改成 60 重取，**URL 里要可见**（新增 `limit` 进 `ROUTE_KEYS` + `RELOAD_KEYS`，值 `1` 时省略，保持旧链接兼容：旧链接无 `limit` → 走默认 1）。

> 为什么默认收到 1 而不是 60：验收报告 D3 判据写的是"本项目+最新运行+未处理"，用户确认"最贴设计文档，首屏可能只有个位数"。风险与缓解见 §7 R1。

### 3.2 项目三态（`ProjectSummary` 新增字段）

`internal/cockpit/dashboard.go:70-86 ProjectSummary` 增加：

```go
RunTotal int  `json:"runTotal"`  // 该项目带身份的运行总数（不是截断后的 len(runs)）
IssueOpen int `json:"issueOpen"` // 该项目最新 1 次运行里 status=open 的问题数
```

- `RunTotal` 来源：`store` 侧新增 `CountRunsForProject(root string) (int, error)`（`SELECT COUNT(*) FROM runs WHERE project_root = ? AND archived = 0`），**不要**用 `len(LatestRunsForProject(20,...))`。
- `IssueOpen` 来源：`scopeRunWindow` 语义下的 `IssuesForQuery`（`Limit:1, ProjectRoot`）的 `total`，一次 SQL 内完成，**不得**为每张卡片跑一次全表 GROUP BY（见 §7 R2 的性能预算）。
- 前端三态判定（纯函数，便于 render_all 断言）：
  - `calls > 0` → **有调用**（现状样式）
  - `calls === 0 && runTotal > 0` → **仅运行**：显示 `N 次运行 · M 个未处理问题` + 两个次级入口
  - `calls === 0 && runTotal === 0` → **空**：显示"没有可验证的调用，也没有检查运行" + 引导（见 §4.2）

### 3.3 `/api/rules` 规则目录（只读）

响应：

```json
{ "generatedAt": "...", "scope": "/abs/root", "policyPath": "...", "languages": ["go","java"],
  "rules": [ { "language":"go", "id":"go-cyclomatic", "title":"...", "enabled":true,
               "severity":"warning", "warn":10, "error":15,
               "reason":"configured", "policySource":"policy_file", "note":"", "origin":"builtin|policy_file|custom" } ] }
```

实现 = `rules.ForLanguage(lang)` 出目录（ID/Title/默认严重度/默认开关）× `config.ExplainRule(lang, id)` 出实际生效值（阈值/来源/开关），**不新造第三份规则元数据**。语言集合来自已加载的 policy（`Config.Languages` 的键），不硬编码。

`reason` 是机器码，**前端负责成人话**（§4.4 映射表），后端不做本地化 —— 与 `displaySource`（policy_api.go:290）既有分工一致。

### 3.4 错误正文（T2.6）

`internal/cockpit` 所有失败出口统一为 JSON：`writeJSONError(w, code, msg)` → `{"error": msg}` + 对应状态码。前端 `api()` 不改逻辑（:420 已优先取 `body.error`）。**新增断言**：`/api/dashboard` 在 db 打不开时返回的正文必须是合法 JSON 且含 `error`。

---

## 4. 逐任务改法

### T3.1 runs 全局页不再误导（前端 0.5d）

1. `index.html:35` 侧栏标签 `质量运行` → **`运行历史`**（审计记录组内保持）。
2. `VIEWS.runs.title` :1583 → 按 scope 三态：全局 `运行历史（全部项目 + 未归属）`、项目 `运行历史 · <name>`、未归属 `未归属运行`。
3. `:1593` 大标题"活跃运行/已归档运行"降为副标题，主位放上面那句 —— 用户先看到"我在看哪个口径"，再看到"活跃还是归档"。
4. `:1596` 的 `unassigned-notice` 在全局态也要出现（现在 `project || s.scope === ""` 门控把它在全局页藏掉了，而全局页恰恰是最需要"其中 N 条无项目身份"这句话的地方）。

### T3.2 项目卡片三态 + 空项目落地（前端 1d，依赖 §3.2 后端字段）

- `projectCard(project)` :1368 增加状态徽标与第三态文案；空态卡片给两个入口：`[查看未归属运行]`（`scope.open` 到 `""`）与 `[去跑一次检查]`（复制 `metricgate check <root>` 命令，纯前端 `navigator.clipboard`，失败降级为选中文本）。
- **关键约束**：卡片内部次级入口**不得复用 `data-action="scope.open"`**，否则 `smoke.mjs:352` 的卡片计数（已知遗留缺陷，迭代 2 修）会进一步失真。新增 `data-action="project.help"` / `project.runs`，`ACTIONS` 表里各写一个。
- 概览页空态（设计文档 §5.2 线框）：`VIEWS.overview` 在项目 `runTotal === 0` 时显示"该项目暂无质量运行"卡 + 上述两个入口，**不回退全局数据**（迭代 1 已保证 gate/measures 不越界，这里补 UI 引导）。

### T3.3 issues 默认收敛 + 一键放宽（前端 1d + 后端 0.25d）

1. `fetchIssues` :480 `limit: 60` → `limit: filters.limit || 1`；`state.limit` 默认 1，`ROUTE_KEYS` 加 `["limit","limit",1]`，`RELOAD_KEYS` 加 `"limit"`。
2. 收敛 chips（`VIEWS.issues.render` :1179 之前插入一行）：
   - `只看最新 1 次运行`（当前 limit=1 时 `is-active`）
   - `放宽到最近 60 次运行`（写 `limit:60`）
   - `只看未处理`（当前 `status==="open"` 时 active；未设时默认即为 open，见 3）
   - chips 复用 `facetRow` :988 的 `is-active` 范式，不新造样式类。
3. **默认 status=open**：`state.status` 默认从 `""` 改 `"open"`（:223 附近），并在范围行显示"范围：最新 1 次运行 · 未处理"。**这是本轮唯一改变"默认看到什么"的动作**，必须在收口截图里显式呈现，并提供"清除全部筛选"回到旧口径。
4. 后端：`window` 文案在 `limit=1` 时输出 `last 1 run`（复数处理，避免 "1 run(s)" 与 UI 其它处不一致）。

### T3.4 规则目录（后端 0.5d + 前端 0.5d）

- 后端：新增 `internal/cockpit/rules_api.go` `rulesAPI(logPath, dbPath string) http.HandlerFunc` + `rulesView(dbPath string, scope store.Window)`（复用 `policyScopeWindow` 与 `scopePolicyPath`，保证"按 scope 读策略"与 policy 页同源）；`dashboard.go:292` 注册 `/api/rules`；`spec.json` 的 `apiVersion` 保持 1（只增端点，不改既有形状）。
- 前端：`VIEWS.rules.load` 改为 `fetchRules()`（`api("/api/rules", {scope: scopeParam(state)})`），渲染规则目录表：`语言 / 规则 / 说明(Title) / 严重度 / warn / error / 生效来源 / 开关`；`reason` 人话映射：

  | `reason` | 显示 |
  |---|---|
  | `configured` | 策略文件里显式配置 |
  | `builtin` | 内置默认，未在策略中覆盖 |
  | `language_not_configured` | 该语言未在策略中启用 |
  | `custom_rule` | 自定义 DSL 规则 |
  | `rule_not_found` | 目录中有、策略未识别（需排查） |

- 侧栏 `规则` 标签保留；页内旧"生效配置键"表**不删**，降级为折叠的"原始配置键（138）"次级区，`/api/rules` 失败时它是回退视图（错误态按 §4.5）。

### T2.5 错误态不再与"0 个"同屏（前端 0.25d）

`VIEWS.issues.render`：`s.error` 为真时 —— 计数行、页码、范围行、`facetPanel` 的严重度/状态/规则计数**全部降级为 `—` 或整块隐藏**，只保留错误卡 + [重试]。实现为一个小函数 `countersOrError(s, fn)`，避免在 8 处各写一遍三元。calls/runs/activity 三个视图的空态同样在 `s.error` 时改口"读取失败"，不显示"暂无数据"。

### T2.6 服务端错误正文透出（后端 0.25d）

`internal/cockpit` 内 `http.Error` 全部替换为 `writeJSONError`（§3.4），共 12+ 处（`dashboard.go:333/350/354/362/375/383/388/399/404/408/423/454` 与 policy/issues/source 各文件）；`policy_api.go` 已有的 `http.Error(err.Error(), 400)`（scope 400）一并换形，**保持正文文本不变**（迭代 1 的 D1-5 断言依赖 `unknown scope (known projects: …)` 这句话）。

---

## 5. 会被打翻的断言（改前必读）

| 断言 | 位置 | 影响 | 处置 |
|---|---|---|---|
| `projects: N cards for M projects` | `web/smoke.mjs:352` | 数 `scope.open` 出现次数；已知遗留缺陷（未归属卡也算） | 本轮**不得**在卡片内新增 `scope.open`（见 T3.2 约束）；断言本身留迭代 2 修 |
| 每视图恰好 1 个 nav 高亮 | `web/smoke.mjs:683-688` | 改侧栏标签不影响；若加次级导航会翻 | 不加二级导航 |
| picker 不得有面包屑 / 有 scope 必含 `scope.clear` | `web/smoke.mjs:321-332` | T3.1 改标题不触碰 | 无需改 |
| overview 只许读 3 个顶层 dashboard 字段 | `web/render_all.mjs:316-352` | T3.2 若让 overview 读 `runTotal` 会**新增顶层读取** | 走项目对象内的字段，或按该文件要求补 L8 注释说明 |
| 面包屑 + 侧栏两组、无二级导航 | `web_contract_test.go:341-370` | T3.1 只改文字 | 无需改 |
| `issue.select` 源码窗口出 `source-line` | `web/smoke.mjs:701-703` | 本轮不碰 source | 迭代 2 项 |
| Go 侧 `go:embed web/*` | — | 改 `web/` 必须 `go build` 才在浏览器可见 | 前端每步 `node --check` + `render_all.mjs`，收口统一 `fast` |

---

## 6. 本轮明确不做

- 门禁徽标 ✕FAILED 与条件表两态矛盾、go/java 度量值重复（T2.1/T2.2）→ 迭代 2
- `/api/source` 指纹 404 与源码窗口渲染（T2.7）→ 迭代 2
- `?limit=500` 34s、`scope=unassigned` 8.1s 的深窗口性能（T2.7 类）→ 迭代 2；本轮**不放宽** `defaultWindowLimit`，也不新建 `findings.fingerprint` 索引
- 瞬时 500 复现（T2.8）→ 迭代 2
- 时间戳格式/时区统一、度量色阶三义区分（T4.x）→ 迭代 4
- 规则目录的"历史 issue 数"列（用户已选：不做，依赖 2.1GB 库聚合有超时风险）
- 让步账本（accept）与规则页的联动

---

## 7. 风险与对策

- **R1 首屏只有个位数，用户以为"坏了"**：范围行必须显式写"最新 1 次运行 · 未处理"，且"放宽到最近 60 次运行"chip 常驻（不是只在空时出现）。收口截图必须包含"有数据"与"0 条"两种首屏。
- **R2 每卡片算 `IssueOpen` 退化成 7×全表 GROUP BY**：实测单项目 `scope=bugbye` 0.79s（迭代 1 数据），7 次串行≈5.5s 会把 `/api/dashboard` 从 8s 推到 14s。**对策**：`IssueOpen` 用一条按 `project_root` 分组的聚合 SQL 一次算完；若做不到（GROUP BY 语义冲突），本轮**降级为只加 `RunTotal`、卡片显示"最近 20 次运行"而不显示未处理问题数**，`IssueOpen` 推到迭代 2。这条降级判断在开发中途就要做，不要等到收口。
- **R3 默认 `status=open` 改变既有链接语义**：带 `status=` 的旧链接不受影响；不带的一律变 open。收口需 curl 验证 `?view=issues`（无 status）与 `?view=issues&status=confirmed` 两条路径。
- **R4 `/api/rules` 目录规模**：`ForLanguage` × 语言数可能上百条；端点必须 <2s（D3 判据），实现里禁止读 `history.db`（纯 policy + 静态目录）。

---

## 8. 验收判据（D3 + 本轮新增）

| # | 判据 | 取证方式 |
|---|---|---|
| D3-1 | 项目卡片可区分 有调用/仅运行/空；空项目有可点入口（不再是死路） | arm64 Chrome 截图 `projects` 全局 + 空项目 overview |
| D3-2 | issues 默认视图 = 本项目 + 最新 1 次运行 + 未处理；3.7 万条不再是唯一默认 | 截图首屏 + `curl "/api/issues?scope=<root>&limit=1&status=open"` total 与页面一致 |
| D3-3 | 质量运行全局页不再误导；规则入口文案与内容一致 | 截图 runs 全局 + rules 页 |
| N-1 | `/api/rules` 真实数据 <2s 且按 scope 返回不同 `policyPath` | curl 计时 × 3 scope |
| N-2 | 超时/失败时不再同屏"0 个问题 / 第 1/1 页 / 暂无数据" | fake-api-server 挂死法（迭代 1 手法）截图 |
| N-3 | 服务端 500/400 正文在前端横幅里可见（非"请求失败 (500)"） | 截图 + curl 正文 |
| N-4 | "放宽到最近 60 次运行"一键生效且 URL 含 `limit=60` | 截图 hash + 二次计时 |

---

## 9. 收口记录（2026-10-16）

### 9.1 判据逐条结果

| # | 结果 | 实测证据 |
|---|---|---|
| D3-1 | ✅ 通过 | `/api/dashboard` 8 个项目：bugbye `calls=0/runTotal=1/issueOpen=41`（仅运行）、metricGate `calls=245/runTotal=699/issueOpen=16`（有调用）、`metricGate-iter2` `calls=2/runTotal=0/issueOpen=0`（空，有可点入口 `project.runs`/`project.help`）。截图 `d3-final/view-projects.png` |
| D3-2 | ✅ 通过 | 首屏 `#view=issues&limit=1&status=open` → **142 个问题**、`范围：last 1 run`；点「放宽到最近 60 次运行」→ **858 个问题**、`范围：last 60 runs`。curl 对照 `/api/issues?limit=1&status=open&scope=all` total=18（全局）/ `scope=bugbye` total=41 |
| D3-3 | ✅ 通过 | runs 页标题「运行历史（全部项目 + 未归属）」+ 副标题「活跃运行 · 全部项目与未归属记录（含 601 条未归属运行）」；rules 页「规则目录」17 行（列：语言/规则/说明/严重度/warn/error/生效来源/开关/存在理由），不再有 138 个配置键与「规则目录缺口」自述 |
| N-1 | ✅ 通过 | `/api/rules?scope=all` 200 / **0.166s** / rules=17 / languages=3；`?scope=bugbye` rules=12 / `policyPath=""`（该项目无策略文件，真实）；`?scope=/etc` 400。远低于 2s 线 |
| N-2 | ✅ 通过 | fake-api-server 挂死法截图：顶部红横幅「接口超时（>15s）」+ 主区「数据读取失败 / 接口超时（>15s）/ 重试」；计数行、页码、facet rail 全部隐藏，不再同屏「0 个问题 / 第 1/1 页 / 暂无数据」 |
| N-3 | ✅ 通过 | 非法 scope 时横幅显示完整服务端正文 `unknown scope: /no/such/repo-xyz (known projects: … and 3 more)`，非「请求失败 (400)」。51 处 `http.Error` 已换 `writeJSONError`（消息文本逐字未动） |
| N-4 | ✅ 通过 | 点「放宽到最近 60 次运行」后 URL 含 `limit=60`（`RELOAD_KEYS` 已含 `limit`，`ROUTE_KEYS` 已登记），二次计时 0.86s |

### 9.2 本轮额外修掉的 4 个真缺陷（验收中新发现，不在原方案内）

1. **门禁借用他人运行却不标注**（P0 级）：空项目 `metricGate-iter2` 的 `/api/gate` 返回 `status=passed runId=1313`，那是**另一个项目**的运行。本迭代新增的空项目入口让这条路径首次可达。修法：`GateView` 新增 `Foreign` 字段（后端）＋门禁横幅显式标注「该结论来自另一次运行，不属于当前项目」（前端）。
2. **converge chip 点了没用**：`convergeRow` 的 dataset 用 `Object.entries().join(" ")` 产出普通字符串再进 `html``` `，被 `escapeHTML` 转义成 `data-limit=&quot;1&quot;` ⇒ 属性在 DOM 上不可见、chip 是死按钮；同时契约测试正则扫不到 `data-action`，报 `ACTIONS["converge.set"] handles an action no template emits`。修法：第二参改字面属性串 + `raw()` 插入。
3. **facet 缓存被默认收敛打死**：`state.status` 默认 `"open"` 使 `unfilteredRead` 恒 false ⇒ `state.facets` 恒 null ⇒ facet rail 永远说「尚无全量计数」，且 rules 组计数随筛选塌缩（服务端 `countRules(filtered)` 在过滤后）。修法：`unfilteredRead` 只认 `rule/file/query`（severity/status 由服务端预过滤计数），新增 `nextFacets(prev, result, refresh)` 按窗口保留 rules 计数。
4. **在飞状态渲染成"没有"**（T2.5 的根因，比方案设想的更深）：所有视图的空态都只看「列表是否为空」，从不看 `loading`；`/api/dashboard` 是 74MB、冷加载 7.5–8.8s，这段时间 runs 页显示「0 条 / 还没有运行记录 / 日志未启用」。修法：新增 `loadPending(s)` 卡片，插在 projects / runs / issues 三处空态之前。冷加载采样 2/2、3.5s 截图 3/3 复现「正在读取本地数据…」贯穿全程。

### 9.3 复现命令

```
go build -o /tmp/mg-iter3 ./cmd/metricgate
/tmp/mg-iter3 cockpit serve -addr 127.0.0.1:8899
bash scripts/cockpit-observe.sh fast          # 全绿
node /tmp/review/shoot-live.mjs 8899 /tmp/review/d3-final '#view=runs' '#view=rules' ...
```

### 9.4 本轮残留（转迭代 4）

- **R-1（新，P2）**：`overview` 页的**让步账本**与**度量实验**两块面板读 `s.dashboard.accepted` / `s.dashboard.metricTrend`（顶层字段），在项目 scope 下不随 scope 变化 ⇒ 非法 scope 页上仍显示全局运行 #1298–#1317 与 `cmd/metricgate-mcp/main.go:35` 的让步。这正是既有后端遗留 **L8**（`ProjectSummary` 不带这三个字段），`smoke.mjs:498-523` 有 tripwire 盯着。本轮未做（方案 §6 已划界），需后端补 `ProjectSummary.accepted/metricTrend` 后前端改读。
- **R-2**：`/api/issues?limit=500` 仍 18–34s（LIMIT 下推只覆盖默认窗口）；`?scope=unassigned` 6–8s。迭代 2 已记 T2.7。
- **R-3**：`/tmp/shots-payloads.json` 的 issues key 未同步 `scope=all`，离线截图 harness 走回落路径。仅影响本地取证工具，不影响产品。
- **R-4**：`converge.clear` 曾因正则误报被判为死处理器；修法已落地，但契约测试 `actionRe`（`web_contract_test.go:53`）仍只认字面属性，未来若再用插值写法会重演。

### 9.5 闸门三触点

- 生成前：`get_constraints` / `find_symbols`（本仓约束包与可复用符号）
- 产出后：对 `scope.go` / `rules_api.go` / `errors.go` / `project_totals.go` / `window.go` / `gate_api.go` / `app.js` 逐个 `check_code` —— **全部 0 error**（`gate_api.go` 6 warning、`app.js` 7 warning 均为既有项；`style.css` 语言不支持，跳过）
- 收尾：`check_path internal/ diff_only=true` → **FAIL (2 errors)**，两条 error 均**不在本迭代改动行**：`cockpit/dashboard.go` `NewDashboard` 圈复杂度 17（并行会话插入的 `UnassignedRuns`/`ArchivedUnassignedRuns` 字段与 `/api/spec` 注册）、`rules/chain.go:49 nestedInChain` 圈复杂度 17（本轮未改该文件）
- `cockpit-observe.sh fast` 内嵌闸门：`PASSED (fail_on=error)`，run #1315 入库

### 9.6 最终闸门复核（收口时实跑）

| 观测 | 结果 |
|---|---|
| `bash scripts/cockpit-observe.sh fast` | ✅ 全绿：node --check / `render_all: 9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok` / live smoke 三条 REQUEST（issues 带 `limit=1&status=open&scope=all`、rules、policy）/ `go build` / `go test ./internal/cockpit/...` ok (19.2s) / gate `PASSED (fail_on=error)` 0 errors 16 warnings run #1318 |
| `check_code` 逐文件 | `scope.go` **PASS** 0/0、`rules_api.go` **PASS** 0/0、`project_totals.go` **PASS** 0/0、`gate_api.go` WARN 0 error/6 warning、`app.js` WARN 0 error/7 warning |
| `check_path internal/ diff_only=true` | `FAIL (2 findings at fail_on=error)`，264 files / 2 errors / 132 warnings |

**两条 error 级 finding 的归属（如实记录，未修）**：

| 位置 | 消息 | 归属 |
|---|---|---|
| `internal/cockpit/dashboard.go:100` | `cyclomatic-complexity of "NewDashboard" is 17 (threshold error 15)` | **与迭代 1 收口时同一条、同一数值**（`iter-05` §8.1 D1-9 已披露）。该函数本轮未新增分支：本迭代对它的改动是 `attachProjectRuns` 循环内查表 + 循环外两次聚合调用，`if err != nil` 守卫链是既有形状。 |
| `internal/rules/chain.go:49` | `cyclomatic-complexity of "nestedInChain" is 17 (threshold error 15)` | **本轮完全未改该文件**（mtime 04:19，早于本迭代开工）。同样与迭代 1 记录一致。 |

⇒ 结论：本迭代**没有引入任何新的 error 级 finding**；`check_path` 的 FAIL 由两条既有项构成，与 `iter-05` §8.1 D1-9 逐字一致。重构 `NewDashboard` 的守卫链属跨迭代技术债，且该函数正被并行会话同时编辑（`/api/spec` 注册、`noStore` 缓存头、`//go:embed` 清单化），本迭代不动。

### 9.7 收口审计发现的一条未修项（如实记录，未擅自"修复"）

**converge rail 的「清除筛选」chip 会与「放宽到最近 60 次运行」同时高亮。**

- 复现（纯函数探针，`/tmp/review/probe-chips.mjs`，直接调 `app.VIEWS.issues.render` 并解析 chip 的 `is-active`）：
  - `limit=60 & status=""`（点过「清除筛选」后的状态）→ 高亮 **2** 个：`["放宽到最近 60 次运行","清除筛选"]`
  - `limit=60 & status=open`（点过「放宽」后的状态）→ 高亮 2 个：`["放宽到最近 60 次运行","只看未处理"]`
  - `limit=1 & status=open`（默认首屏）→ 高亮 2 个：`["只看最新 1 次运行","只看未处理"]`
- 成因：`app.js:1300` 把 `baseline`（`limit===60 && status==="" && 无其它筛选`，定义在 :1292）同时传给「清除筛选」的 active，而「放宽」的 active 条件是 `Number(s.limit) === WIDEN_LIMIT`，两者在 `limit=60 & status=""` 上同时成立。
- **处置：本轮不改，转迭代 4 的 T4.7。** 原因：`app.js:1284-1290` 的注释是一段**刻意的辩护**，它明确写过「It used to hardcode `false` and reset only limit and status, so clicking it with a rule selected left that rule on, dropped both lit chips and lit "放宽" instead」——即硬编码 `false` 曾是**已修的真实 bug**，`baseline` 是为修它而引入的。设计文档 §5.3 只写了「收敛 chips: [只看最近运行][只看未处理]」，未规定「清除筛选」的 active 语义。因此这是**规格空白**，正确做法是回评审确定语义（「清除筛选」该不该有常亮态？还是应拆成两个不同终态？），而不是在收口时凭一个探针结果推翻带理由的既有实现。
- 已核：该 chip 的 `data-action="converge.clear"` 与 `data-limit/data-status` 属性均正常落进 DOM（`raw(attrs)` 修复有效），点击行为正确（`ACTIONS["converge.clear"]` 真的把 `limit` 设为 `WIDEN_LIMIT`、`status` 设为 `""`），**只是视觉上多亮一个**。属 P3 打磨项，不影响功能。

### 9.8 一次未采纳的改动（过程留痕）

收口审计中我一度按记忆把「清除筛选」的 active 改成常量 `false`（并在完成摘要里错误地声称 §9.2 已含此修复）。核对代码注释与设计文档后判定该改动**无依据且会推翻既有刻意实现**，已完整回退（`node --check` OK、`render_all` 9×3×2 ok、探针输出与改动前逐字一致）。教训：完成摘要里不得写入未在树中核实过的修复；凭记忆声称"已修"正是本次审计被拒的原因。

### 9.9 收口验证期间的并发编辑实况（复核证据的成立条件）

本迭代收口时，本仓有**第三个会话**在同时开发迭代 2 的门禁页。这直接影响"验证是否可信"，如实记录：

| 观测 | 现象 | 定性 |
|---|---|---|
| `go build ./...` / `go test` | 一度报 `link: cannot reopen .../go-build/8c/...-d: no such file or directory` 与 `could not import syscall` | **并发会话清空/重建共享 `GOCACHE` 所致**，非代码缺陷。改用 `GOCACHE=/tmp/review/gocache-iter3` 隔离缓存后 `go build ./...` → `Go build: Success` |
| `go test ./internal/cockpit/...` | 一度报 `gate_api_test.go:190: assignment mismatch: 2 variables but gateBand returns 3 values` | 并发会话把 `gateBand`（`gate_api.go:276`）改成 `(warn, errAt int, ok bool)`（mtime 16:18:32）但测试（mtime 14:54）尚未同步。**非本迭代改动**（我本轮只给 `GateView`/`MeasuresView` 加 `Foreign` 字段与 `view.Foreign = true`，未碰 `gateBand`/`gateConditions`）。16:55:04 并发会话自行对齐后消失 |
| `TestRenderAllViews` | 一度报 `FAIL issues range: a payload without a window drew an empty range line` | 并发会话正在重写 `render_all.mjs:394-406` 的断言块（mtime 16:52:48，晚于我的 app.js 改动 16:47:12）。**新块与旧失败互斥**：新块要求"无 window 时必须回落到本地窗口标签"，正是 `issuesWindow`（`app.js:1302`）→ `localWindowLabel`（`app.js:1304`）的兜底行为。文件改完后 `render_all` 与契约测试全绿 |
| 定位手法 | 用 `-overlay`（`/tmp/review/overlay.json`）把测试文件替换成对齐版，**不写入真仓**，避免与并发会话撞写 | 结论：本迭代 37 个相关测试全 PASS（含 `TestGateMarksAnotherProjectsRunAsForeign` / `TestMeasuresMarksAnotherProjectsRunAsForeign`） |

**并发会话的改动一律不回滚**（`gate_api.go` 的 `gateBand` 3 值签名、`render_all.mjs` 的新断言块、`.metricgate.yaml` 的 `architecture` 段 +4 行均属其所有）。判定某失败归属时，一律先比 mtime 与"该行是否在本迭代 diff 内"，再定性。

**最终验证（隔离缓存、并发编辑平息后）**：
- `go build ./...` → `Go build: Success`
- `go test ./internal/cockpit/... ./internal/store/... ./internal/config/...` → 三包全 `ok`
- `bash scripts/cockpit-observe.sh fast` → 全绿：`render_all: 9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok`；live smoke 三条 REQUEST（issues 带 `limit=1&status=open&scope=all`、rules、policy）；gate `PASSED (fail_on=error)` 0 errors 16 warnings，run #1324

### 9.10 闸门三触点最终结果（并发编辑平息后重跑）

| 检查 | 结果 |
|---|---|
| `check_code` × 6 文件 | `scope.go` **PASS** 0/0、`rules_api.go` **PASS** 0/0、`project_totals.go` **PASS** 0/0、`errors.go` **PASS** 0/0、`gate_api.go` WARN **0 error**/6 warning、`app.js` WARN **0 error**/7 warning |
| `check_path internal/ diff_only=true` | FAIL (2 findings at fail_on=error)，均为既有项（见 §9.6，与 `iter-05` §8.1 D1-9 逐字一致） |

`gate_api.go` 的 6 条 warning 逐条归属：
- `file-length 537 (阈值 400)`：文件长度由**本迭代的 `Foreign` 字段/标注**与**并发会话的 `gateBand` 3 值签名、`HasData`/`Failing`、按语言分带**共同推高，非单一来源。
- `gateView fan-out 11`、`measuresView` 的 cyclomatic 12 / fan-out 13 / function-length 57 / npath 960：**均为既有形状**——`gateView`/`measuresView` 本迭代只改了"取哪一次运行"（`scopeRunWindow(scope)`）与"读哪份策略"（`scopePolicyPath(scope)`），未新增分支结构。`measuresView` 的 57 行长度与 npath 960 在 `iter-05` 收口时即存在。

⇒ 本迭代**未引入 error 级 finding**；全部 warning 为文件既有量级或与并发会话改动共担。

### 9.11 交底一句话

迭代 3 的承诺（runs 不再冒充全局 / 项目三态可辨且空项目可走 / issues 默认收敛到最新 1 次运行并可一键放宽 / 规则页变成真规则目录 / 错误态不再与「0 个」同屏 / 服务端错误正文可见）**全部落地并有实测证据**；验收中另外揪出并修掉 4 个真缺陷（其中门禁借用他人运行不标注是 P0 级）。一条规格空白（converge rail「清除筛选」的常亮语义）与 6 条残留已如实转迭代 4（T4.5–T4.7）。所有验证均在隔离 `GOCACHE` 下复跑过，以排除并发会话对共享构建缓存的干扰。
