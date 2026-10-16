# iter-05 · 迭代 1（P0）恢复可用与可信 — 落地子方案

> 角色：PM + 设计（方案产出）。上游依据：`docs/COCKPIT_产品验收报告.md`、`docs/COCKPIT_产品设计文档.md` §4/§5.2/§5.3/§5.6/§10-D1。
> 代码事实来源：后端自查（scout 输出损坏后重做）+ 前端 scout 报告（201 行，含 file:line）。
> 行号是移动靶：本文件所有行号为 2026-10-16 实测（app.js 2559 行 / style.css 1411 行），开工前重导一次。

---

## 0. 一句话目标与判据

**目标**：真实浏览器下，问题分诊页不再"超时→空态且无提示"；进入任意项目后屏上每个数字都属于该项目；面包屑不撒谎。

**判据（= 设计文档 §10 D1）**：
| # | 判据 | 度量方式 |
|---|---|---|
| D1-1 | `/api/issues` 在现网数据（1,172,857 findings / 1,246 runs / history.db 2.1GB）下 **< 5s** 返回 | `curl -w '%{time_total}'` |
| D1-2 | 慢接口失败时页面显示**错误态**（含原因 + 重试），不再静默 0 行 | arm64 Chrome 截图 |
| D1-3 | 进入项目后 overview/gate/issues 每个数字都属于该 scope | 逐屏截图比对 |
| D1-4 | `/api/policy?scope=<root>` 返回该 scope 的配置，不再返回服务启动目录那份 | `curl` |
| D1-5 | 面包屑显示 `项目总览 › <项目名>`，任何路径下不出现"未归属记录"错位 | smoke + 截图 |
| D1-6 | 状态流转有当前态高亮 + 可撤销；保存配置后有明确回显 | 截图 + smoke |

---

## 1. 根因收敛：三个症状，两处病灶

验收报告把 T1.0/T1.1/T1.2/T1.3 列成 4 个任务，代码层面它们只有两处根因：

**病灶 A — 窗口 limit 没有下推到 SQL**（→ D1-1、D1-2 的一半、源码窗口渲染不出行）
`internal/store/issues_query.go:251` `issueCandidates` 用 `q.Window.runScope(2)` 生成 WHERE，但 **`Window.Limit` 从未进入 SQL**（全文件只有 `Validate()` 在 :135-136 检查它非负）。于是每次请求都对**全库 117 万行 findings 做 10 列 GROUP BY**。

实测（本机、真实库、同一 SQL 只差一个子查询）：

| 口径 | 结果 | 耗时 |
|---|---|---|
| 现状（无 LIMIT） | 335,991 条候选 | **21.7s** |
| 加 `run_id IN (SELECT id FROM runs ORDER BY started_at DESC, id DESC LIMIT 60)` | 37,388 条候选 | **0.78s** |

→ **28× 提速，且语义正确**（`defaultWindowLimit = 60` 本来就是"最近 60 次运行"的意思，`internal/cockpit/issues_api.go:136`）。

**病灶 B — scope 在服务端被硬编码成进程 CWD**（→ D1-3、D1-4）
三个端点各自把"当前项目"解析成 `os.Getwd()`，且都不读查询串：
- `internal/cockpit/policy_api.go:121` `policyRoot() { root, _ := os.Getwd(); ... }`，:130 `discoveredPolicy() { return configpath.Discover("", policyRoot()) }`；`policyAPI()` 注册时**连参数都不接**（`internal/cockpit/dashboard.go:296`）。
- `internal/cockpit/gate_api.go:154` `projectScopeRoot() string { return policyRoot() }`，:117 `LatestRunsIn(store.Window{Limit: 1, ProjectRoot: projectScopeRoot()})`。
- `internal/cockpit/issues_api.go:143-148` 注释明写 "The project scope is deliberately not read from the URL."

**关键省力点**：store 层**早就支持**按项目过滤，不需要改 store。`internal/store/window.go:14-27` 的 `Window` 已有 `ProjectRoot` / `Unassigned`，`window.go:125` `runScope` 会渲染 `project_root = ?` 与 `(project_root IS NULL OR project_root = '')`；`internal/store/read.go:284-309` 已有 `ProjectRoots()`、`LatestRunsForProject`、`LatestUnassignedRuns`。所以 B 是**API 层的接线活**，不是存储层重构。

> 原注释的理由要尊重："a shared database answered with another project's issues is the one failure here that looks like a working feature."（拿错项目的数据比报错更危险）。因此 scope 解析必须**白名单化**，见 §3。

---

## 2. 任务分解与归属（前后端 2 路并行）

| 任务 | 内容 | 归属 | 依赖 |
|---|---|---|---|
| T1.0a | issues 候选查询 LIMIT 下推（病灶 A） | 后端 | — |
| T1.0b | 前端 fetch 超时 + 错误态（不再静默空态） | 前端 | 可与 T1.0a 并行 |
| T1.1 | `/api/policy` 接 scope（白名单解析） | 后端 | — |
| T1.2 | 调用/运行归属可见性（未归属口径说清楚） | 后端(数据) + 前端(文案) | T1.1 |
| T1.3 | overview / gate / measures 按 scope 取数 | 后端 | T1.1 的 scope 解析器 |
| T1.5 | 面包屑三态修正 | 前端 | — |
| T1.6 | 状态流转防呆 + 配置保存回显 | 前端 | — |

**并行边界**：后端只碰 `internal/cockpit/*.go`、`internal/store/*.go`；前端只碰 `internal/cockpit/web/{app.js,style.css,index.html}`。**唯一交叉点**是 scope 的查询参数名与取值 —— 先在本文件 §3 定死契约，两边各自实现，不做口头对齐。

---

## 3. 作用域契约（前后端共同的唯一事实源）

### 3.1 参数

```
GET /api/policy?scope=<opaque>
GET /api/gate?scope=<opaque>
GET /api/measures?scope=<opaque>
GET /api/issues?scope=<opaque>
```

- `scope` 缺省或 `all` → 全局（现行为，向后兼容）。
- `scope=unassigned` → 未归属桶（`project_root IS NULL OR project_root = ''`）。
- 其他值 → **必须是服务端已登记的项目 root**（见 3.2），否则 **400**，响应体 `{"error":"unknown scope"}`。**不接受任意路径字符串**，这是防止"看起来能用其实拿错数据"的硬约束。

### 3.2 scope 解析器（后端新增，一处实现三处用）

新增 `internal/cockpit/scope.go`：

```go
// resolveScope maps a URL scope token onto a verified project root.
// The token is never a path: it is an index/key into roots the server has
// already seen, so a typo or a hand-typed path cannot silently select
// another repository's data.
func resolveScope(r *http.Request, dbPath string) (store.Window, error)
```

白名单来源（两路并集，与 dashboard 的口径一致）：
1. `store.ProjectRoots()`（`internal/store/read.go:284`，`SELECT DISTINCT project_root FROM runs WHERE project_root IS NOT NULL AND project_root != ''`）；
2. dashboard 的项目列表（`groupCallsByProject` 一路，`internal/cockpit/dashboard.go:202-220`）。

**归一化**：两侧都用 `filepath.EvalSymlinks(filepath.Clean(...))`（现成实现 `internal/cockpit/policy_api.go:312-318`、`dashboard.go:249-259`）。这是面包屑错位的成因之一（`/tmp` vs `/private/tmp`），必须在服务端统一，不能让前端拿字符串相等去赌。

**token 形态**：用**归一化后的绝对路径本身**作 token（前端已有 `s.scope` 就是 root，见 `app.js:998 projectOf`），服务端收到后同样归一化再与白名单做**精确相等**比较。不引入新编码，避免前端 hash 路由二次改造。

### 3.3 前端改动（最小）

`app.js` 现有取数全都不带参数：`fetchDashboard` :476、`fetchPolicy` :493、issues :440、gate :503、measures :511。改为在 URL 上带 `scope`：

```js
const scopeParam = (s) => (s.scope === "" ? "unassigned" : s.scope || "all");
```

`RELOAD_KEYS`（`app.js:261`）**必须加 `"scope"`** —— 现在改 scope 只靠同时改 view 才触发 `rerender()`，这正是"scope 切换是无效控件"的前端半边。

---

## 4. 逐任务改动点

### T1.0a · issues 候选查询 LIMIT 下推（后端，0.5d）

**文件**：`internal/store/issues_query.go`（`issueCandidates` :251）
**改法**：照抄本仓既有范式 `internal/store/read.go:184-193`（`RuleFrequencyScoped`）：

```go
inner := `SELECT id FROM runs` + where + ` ORDER BY started_at DESC, id DESC`
if w.Limit > 0 { inner += ` LIMIT ?` }
query := `... FROM findings f WHERE f.run_id IN (` + inner + `) GROUP BY ...`
```

**必须保持的语义**：
- 现在传的是 `q.Window.runScope(2)`，archiveMode=2 = **不过滤 archived**。下推后内层子查询同样**不要**加 `archived = 0`（实测两者都 <1s：不过滤 0.776s / 37,388 条；过滤 0.96s / 37,389 条。现库 `archived` 全为 0，但语义不能顺手改，改了就是另一个 bug）。
- facet 计数仍在过滤前算（`IssuesForQuery` :202 注释：否则选中值的 chip 会显示 0，"a filter panel renders itself as a dead end"）。下推后 facet 的**基数**变成 60-run 窗口 —— 这是正确行为（窗口内计数），但要在 `result.Window` 文案里说清楚，见 T1.2。
- `paginate`/`pageSlice` 在内存里做，不动。
- 下游 `statesFor`（:303）→ `IssueStates`（`internal/store/issues.go:178`，chunk=400 的 `IN` 查询）：候选从 335,991 降到 ~37,388，chunk 查询从 840 次降到 94 次，顺带变快。

**验证**：`go build ./...` + `go test ./internal/store/... ./internal/cockpit/...` + `curl -w '%{time_total}' 'http://127.0.0.1:8899/api/issues'`（目标 <5s）。

**不做**：不给 `findings.fingerprint` 建索引（下推后不需要，且 2.1GB 库上建索引本身就是一次长操作）；不改 `defaultWindowLimit`。

### T1.0b · 前端超时与错误态（前端，0.5d）

**文件**：`internal/cockpit/web/app.js`
**现状**：`api()` :368-394 只在 `!response.ok` 时 reject（:385）。全文件**没有任何** `AbortController` / `AbortSignal.timeout`（唯一的 `setTimeout` 是 :290-291 的搜索去抖）。连接挂死时 UI 停在"读取本地数据…"、刷新键常驻 disabled（:2487-2491、:2518）。

**改法**：
1. `api()` 加 `AbortSignal.timeout(15000)`（15s：大于后端修复后的 P99，小于用户耐心）；捕获 `AbortError` → `throw new Error("接口超时（>15s）")`。
2. 各视图 `load` 失败时**不要**把 `issues: []` 当空态渲染。错误出口已有（`set({error})` → `#errorBanner`，:2507-2513；容器 `index.html:51 role="alert"`），但**视图级**要区分"0 条"与"没拿到"：
   - `issues` 空态 `app.js:1100`、`calls` :1437、`runs` :1484、`activity` :1780、`rules` :1940、`gate` :1894 —— 在渲染空态前先判 `s.error`，有错则渲染错误态卡片：`数据读取失败 · <原因>` + `重试` 按钮（`data-action="view.retry"` → `rerender()`）。
3. `loading` 期间刷新键 disabled 是对的，但超时后必须恢复：`api()` 的 `finally` 里清 `inFlight`（:377-392 已有）并确保 `set({loading:false})` 在 reject 路径也执行。

**验证**：`node --check` + 把后端端口停掉/或临时把 timeout 调到 1ms，截图确认显示错误态而非"暂无数据"。

### T1.1 · `/api/policy` 接 scope（后端，1d）

**文件**：`internal/cockpit/policy_api.go`（:103 `policyAPI()`、:121 `policyRoot()`、:130 `discoveredPolicy()`、:136 `servePolicyView`）+ `internal/cockpit/dashboard.go:296` 注册处。
**改法**：
- `policyAPI()` → `policyAPI(dbPath)`（注册处已有 `dbPath` 变量，dashboard.go:294 就在用）。
- `policyRoot()` 保留为"无 scope 时的默认值"（向后兼容 CLI 用法），新增按 scope 解析的分支：`configpath.Discover("", resolvedRoot)`。
- 响应体 `root` 字段（:41）必须回显**实际查询的 root**，前端据此显示"配置来自哪里"。这条是 D1-4 的可验证凭据。
- scope 未命中白名单 → 400（§3.1）。
**验证**：`curl 'http://127.0.0.1:8899/api/policy?scope=/Users/mac/Downloads/main/bugbye-miserver'` 的 `root` 不再是 `/Users/mac/Downloads/metricGate`；`curl '...?scope=/etc'` → 400。

### T1.2 · 归属口径可见（后端 0.5d + 前端 0.5d）

**数据事实**：`runs` 1,246 条中 **645 条带 `project_root`**（59.6%），598 条无项目身份 —— 与验收报告的 41.6% 调用归属率同源。这些是"记录项目路径之前写入的历史运行"，**不可能靠改代码追认**。

**产品决策（本轮定最小方案，标注留评审）**：不做追认，做**诚实呈现**：
- 后端：`/api/dashboard` 已有 `unassignedCalls`；补 `unassignedRuns`（若缺）并在项目对象上补 `runsTotal`，让"未归属"成为可量化的口径而非猜测。
- 前端文案：`app.js:1332` 全局 scope 下的"质量运行"卡 note 现在写死"全部项目与未归属记录"，`app.js:1492` 趋势标题已区分未归属序列 —— 统一为：**全局视图明确写"含 N 条未归属运行"**，不再用"质量运行"这种把未归属桶当全局结论的标题（验收报告 P2）。
- `app.js:1462` 已有正确说明（"只显示带项目身份的新运行；早期运行没有记录项目路径，仍留在「未归属记录」里"），把它提到 overview 的项目卡上。

### T1.3 · overview / gate / measures 按 scope 取数（后端 1d，前端 0.5d）

**这是 D1-3 的正身，前端单独改无法达标**（前端 scout 结论一致）。
**后端**：
- `gate_api.go:154` `projectScopeRoot()` → 改为读 §3.2 解析器；`gate_api.go:117` 的 `LatestRunsIn(store.Window{Limit:1, ProjectRoot: ...})` 随之接 scope；`measuresAPI` 同源。
- 未归属 scope 用 `Window{Unassigned:true}`，全局用零值 `Window{}`。
**前端**：
- `app.js:1588-1591`（overview load：`Promise.all([fetchDashboard(), fetchGate(), fetchMeasures()])`）与 `app.js:1875-1878`（gate load）改为带 `scope` 参数。
- `app.js:1593-1602` overview render 的 `gateBanner(s.gate)` 与 measures 网格消费 scope 化载荷。
- 保留客户端过滤（`callsInScope` :1006、`scopeAggregate` :1021、`runsInScope` :1046）用于 dashboard 载荷；但**门禁/度量不再走客户端过滤**，改由服务端 scope 决定 —— 否则又回到"换个更礼貌的说法"。
**验证**：arm64 Chrome 进 bugbye，overview 顶部不再出现全局 `✕ FAILED 运行 #1233` 与全局度量（Cyclomatic 1112 / File length 298040），而是该项目的结论或明确的"该项目暂无运行记录"。

### T1.5 · 面包屑三态（前端 0.5d）

**病灶**：`app.js:1304-1311`，字面量在 **:1309**：
```js
${s.scope ? html`<span>›</span>${project ? html`<strong>${project.name}</strong>` : html`<strong>未归属记录</strong>`}` : ""}
```
`s.scope` 为真值时只分"命中项目 / 没命中"，**没命中一律写"未归属记录"**。而真·未归属是 `s.scope === ""`（falsy，走不到这个分支）。所以这个文案**当前只被"项目找不到"这一条路径触发** —— 纯属撒谎。
**改法**：显式三态 + 加载中态：
```js
// scope === ""  → 未归属记录
// project hit   → project.name
// dashboard 未加载 → “读取中…”（不猜）
// 加载完仍未命中 → “未知项目”（异常态，可诊断）
```
根上的 root 字符串不一致由 §3.2 服务端归一化解决；前端 `projectOf`（:998）保持字符串相等，但**不再用"未归属"掩盖 miss**。
**注意断言**：`smoke.mjs:321-332`（picker 不得有面包屑；有 scope 必须含 `scope.clear`）、`web_contract_test.go:355`（面包屑 + 侧栏两组）会受影响，改文案要同步改断言，不能只改一边。

### T1.6 · 状态防呆 + 保存回显（前端 1d）

**(a) 状态流转**：`statusSelect` `app.js:1172` 是裸 `<select class="issue-status">`（样式只有 `style.css:1213 cursor:pointer`），当前态无高亮；`ACTIONS["issue.status"]` :2008 直接 `patchIssue` → `rerender()`，**无确认、无撤销**；`bulk.status` :2038-2051 逐条 PATCH，同样无确认。全文件唯一 `window.confirm` 在 :2251（归档运行）。
**改法**（最小可用，不引入新组件）：
1. 当前态高亮：给 select 加 `data-status="${issue.status}"`，`style.css` 用既有 tone token 着色（success `#75a92c` / danger `#d92d20` / warning `#f5b840` / accent `#5d6cd0`）；旁边 `statusTag`（:713-716，`tone-*` 样式 :714-728）已提供配色语义，直接对齐。
2. 撤销而非确认弹窗：PATCH 成功后在 `#errorBanner` 下方给一条 **toast**：`已将 N 条标记为「已确认」 · 撤销`，撤销=再 PATCH 回旧值（`issue_states` 是可覆盖的读时裁决，见 `internal/store/issues.go:198+` `StatusOverride` 注释，无需后端改动）。批量流转（:2038-2051）必须用同一个 toast，不逐条弹。
3. 失败路径已有（:2019 `状态写入失败：...`），保留。

**(b) 配置保存回显**：`savePolicy` `app.js:2272-2286` **确实**在 PUT 后 `await fetchPolicy()`，并在顶栏回显"配置已保存 <时间>"（:2487-2491）。真正的缺口两个：
1. 成功后没有 `set({ policyDraft: null })` —— textarea 继续显示草稿文本（此时恰好与服务器副本相等，脏提示自动隐藏，所以症状隐蔽）。**补上**。
2. "生效键表"取值列全空（`app.js:1681`/`app.js:1941` 的 `k.value` 永远为空；:1962-1965 注释直说 "the value is a display string that is empty for every key today (see L10)"；服务端 `policy_api.go:240 policyKeys` / :290 `displaySource` 不产出 per-key 值）。**本轮产品决策：前端显式声明该列不可用**（表头改为"取值（当前版本不展示）"或整列收起），后端补 per-key 值推到迭代 4。**不要**留一个空白列让用户以为数据丢了。
3. 顺带纠正验收报告的一处事实错误：报告称"保存配置成功后串行触发 `/api/gate` 再 `/api/measures` 自检"，**与现码不符**（`savePolicy` 只调 `fetchPolicy()`）。收口验收时在报告里更正。

---

## 5. 已知会打翻的断言（改前先看这张表）

| 断言 | 位置 | 影响 |
|---|---|---|
| picker 无面包屑 / 有 scope 必含 `scope.clear` | `smoke.mjs:321-332` | T1.5 改文案需同步 |
| 每视图恰好 1 个 nav 高亮 | `smoke.mjs:683-688` | 加 toast/错误态卡片时注意 |
| `issue.select` 必须出现 `source-line` | `smoke.mjs:701-703` | T1.0a 之后才有救（见 §6） |
| overview 只许读 3 个顶层 dashboard 字段，多读要写 L8 注释 | `render_all.mjs:316-352` | T1.3 若多读必须写 L8 |
| 面包屑 + 侧栏两组结构 | `web_contract_test.go:355` | T1.5 |
| `writeInto` 是唯一 innerHTML sink，Go 契约测试计数 | `app.js:2416` | 新增 DOM 一律走 `html` 模板 |

**`go:embed web/*`**：改 `web/` 下任何文件都要 `go build` 后浏览器才看得见。前端任务每步都要重新 build 再截图，否则会验到旧包。

---

## 6. 本轮不做（明确划界，避免范围蔓延）

- **门禁徽标自相矛盾**（P1：`✕ FAILED 6 errors` 但条件表只有"通过/发现问题"两态、不区分 warning/error；go/java 两个语言块 9 项度量值一字不差）→ 迭代 2。
- **33.6 万问题无收敛引导**：T1.0a 之后 60-run 窗口内是 ~3.7 万条，仍需收敛引导 → 迭代 2/3。
- **`/api/source` 指纹查不到**（`source_api.go:108` 在 60-run 窗口里内存比对指纹，早期运行指纹必 404）：T1.0a 让列表能出行了，源码窗口的窗口错配是独立缺陷 → 迭代 2（需要给 `IssueQuery` 加 fingerprint 过滤）。
- **`findings.fingerprint` 索引 / history.db 归档策略**：下推后查询已 <1s，不动库结构。归档入口 `POST /api/runs/archive`（`dashboard.go:295`）已有，运维策略另议。
- **rules 页自我声明半成品（138 配置键无规则目录）** → 迭代 3。
- **时间戳三格式 / UTC-本地偏移** → 迭代 4。

---

## 7. 排期与验收

| 任务 | 归属 | 估时 |
|---|---|---|
| T1.0a issues LIMIT 下推 | 后端 | 0.5d |
| T1.1 policy 接 scope | 后端 | 1d |
| T1.3 gate/measures 接 scope | 后端 | 1d |
| T1.2 归属口径（后端半边） | 后端 | 0.5d |
| T1.0b 超时 + 错误态 | 前端 | 0.5d |
| T1.3 前端接线（含 RELOAD_KEYS 加 scope） | 前端 | 0.5d |
| T1.5 面包屑三态 | 前端 | 0.5d |
| T1.6 状态防呆 + 保存回显 | 前端 | 1d |
| T1.2 文案（前端半边） | 前端 | 0.5d |
| **小计** | | **6d**（与设计文档"迭代 1 = 5–6d"一致） |

**收口验收（accept 任务）**：
1. `bash scripts/cockpit-observe.sh fast` 全绿（含 `go build ./...`、`go test ./internal/cockpit/...`、live smoke、`metricgate check`）。
2. arm64 Chrome 真实浏览器回验，按 §0 判据逐条截图归档（`/tmp/review/out/`，文件名带 scope）。
3. `curl -w '%{time_total}'` 记录 `/api/issues`、`/api/policy?scope=...` 的实测值与响应 `root` 字段。
4. 按设计文档 §10 D1 六条逐条给"通过/不通过 + 证据"。
5. 闸门三触点（`get_constraints` / 每段 `check_code` / 收尾 `check_path`）不可绕行；`.metricgate/mcp-calls.jsonl` 不得删。

## 8. 收口记录（迭代 1 验收，2026-10-16）

**结论：D1 通过（2 项带残留）。** 所有判据均有真实数据/真实浏览器证据，证据目录 `/tmp/review/after-fix-123333/`、`/tmp/review/d1-shots-real/`。

### 8.1 D1 逐条

| # | 判据 | 结果 | 证据 |
|---|---|---|---|
| D1-1 | /api/issues 不再 30s 超时 | ⚠️ 通过（带残留） | 同一份 2.1GB 库：旧二进制 8899 `code=000 time=37.03s`（或 `time=35.008` 超时）→ 新二进制 `code=200 time=2.86s~3.56s`、`total=37519~37543`、`window="last 60 run(s)"`，**≈13×**。SQL 计划已确认 LIMIT 下推（`SEARCH f USING INDEX idx_findings_run` + `LIST SUBQUERY` 含 LIMIT 60）。**残留**：`scope=unassigned` 8.10s（该桶 60 个 run 真的含 201,175 个候选指纹，是数据量不是缺索引，裸 sqlite 3.96s）；`?limit=500` 34.09s / `+page=2` 39.12s / `+sort=file` 34.32s（回到 30s 级）。二者均记迭代 2，本轮不扩大范围。 |
| D1-2 | 慢接口有错误卡+重试，不静默空态 | ✅ 通过 | 假后端（接受连接后 sleep 120s）→ 前端 15s `AbortSignal.timeout` 触发：顶部红横幅「接口超时（>15s）」+ 主区错误卡「⊘ 数据读取失败 / 接口超时（>15s）/ [重试]」。截图 `/tmp/review/D1-2-issues-timeout.png`。修复前同位置是静默「0 个问题 / 暂无数据」。 |
| D1-3 | 进项目后数字属于该项目 | ✅ 通过 | `curl` 同库三 scope 对照：gate 取到的运行号随之变化（`all`→#1270 / `unassigned`→#1260 / `bugbye`→**#961**）；issues `total` 37543（all）vs **41**（bugbye）。真实浏览器 bugbye 概览度量 **Cyclomatic 7 / Cognitive 15 / Function length 64 / File length 302 / Nesting 4**，与全局（1112 / 25 / 12209 / 298040 / 5）完全不同；门禁徽标为 `✓ PASSED 运行 #961`（bugbye 自己的运行），不再顶全局 FAILED #1233。 |
| D1-4 | policy 按 scope 返回 | ✅ 通过 | `root` 字段随 scope 变化：无 scope→`/Users/mac/Downloads/metricGate`；`scope=bugbye-miserver`→`/Users/mac/Downloads/main/bugbye-miserver`；`scope=unassigned`/`all`→200；**`scope=/etc`（白名单外）→400**。真实浏览器 policy 页（bugbye）显示「使用内置默认值 / 未找到配置文件」+ 发现链，不再显示 metricGate 的 `.metricgate.yaml`。截图 `13-final-policy-project.png`。 |
| D1-5 | 面包屑为「项目总览 › <项目名>」 | ✅ 通过 | 真实浏览器：`项目总览 › bugbye-miserver`（修复前为 `› 未归属记录`）；非法 scope `/no/such/repo-xyz` → `项目总览 › 未知项目`（**绝不显示「未归属记录」**），截图 `11-overview-badscope.png`。 |
| D1-6 | 状态按钮防呆 + 保存回显 | ✅ 通过 | `statusSelect` 带 `data-status`，下拉显示当前态（真实浏览器显示「未处理」）；改动后 toast + 撤销（`#toastUndo`）；`savePolicy` 成功后 `set({policyDraft:null})`，不再长期挂草稿。截图 `12-final-issues-project.png`。 |
| D1-7 | cockpit-observe.sh fast 全绿 | ✅ 通过 | `bash scripts/cockpit-observe.sh fast` 实跑输出：`node --check` ok；`render_all: 9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok`；live smoke 实际发出 `?scope=all` 三个请求（证前端已在接线）；`go build` 成功；`go test ./internal/cockpit/...` → `ok metricgate/internal/cockpit 10.209s`（连同 store 包共 **118 passed**）；`gate: PASSED (fail_on=error)`，`0 errors, 16 warnings, 421 metric snapshots`，`run #1273 stored`。 |
| D1-8 | arm64 Chrome 回验截图归档 | ✅ 通过 | `/tmp/review/d1-shots-real/` 共 8 张（10-overview-project、11-overview-badscope、12-final-issues-project、13-final-policy-project 等），均为 `arch -arm64` 原生 Chrome + CDP，非 harness 模拟。 |
| D1-9 | 闸门三触点不绕行 | ✅ 通过 | `get_constraints`/`check_code`/`check_path` 已调；新增文件 `internal/cockpit/scope.go` 单独 `check_code` → **PASS 0 error 0 warning**；`scope_test.go` → 9 条 warning（均为测试函数复杂度，error 0）；`check_path internal/ --diff-only` → `FAIL (2 findings at fail_on=error)`，两条 error 分别落在 `internal/cockpit/dashboard.go:98 NewDashboard`（复杂度 17>15）与 `internal/rules/chain.go:49 nestedInChain`（17>15），**均不在本迭代改动行上**（dashboard.go 该 hunk 是并行会话的 `UnassignedRuns` 字段插入，chain.go 本轮未改），已在报告残留中标注归属。 |

### 8.2 并发编辑实况（必须记录）
本机有第二个会话在同仓并行改动，已确认不属于本迭代且**未回滚**：`internal/cockpit/dashboard.go` 的 `//go:embed` 清单化 + `noStore` 缓存头 + `UnassignedRuns`/`ArchivedUnassignedRuns` 字段、`internal/webspec` 包的 `/api/spec` 路由、`docs/strategy/positioning.md` 等。本轮沿用其 `policyAPI(logPath, dbPath)` / `issuesAPI(logPath, dbPath)` 签名，无冲突。

### 8.3 本轮新发现的残留（非 D1 判据，转下一迭代）
1. **错误态与计数自相矛盾**：超时/失败时仍同屏显示「0 个问题 第 1 / 1 页」与 facet「暂无数据」——错误卡说读不到、标题说是 0 个（迭代 2）。
2. **服务端错误正文被丢弃**：非法 scope 时横幅显示「请求失败 (400)」，而服务端返回的是可读的 `unknown scope: … (known projects: …)`，最有用的解释被前端通用文案覆盖（迭代 2）。
3. **深链面包屑曾卡在「读取中…」**：只有 projects/overview 会加载 dashboard，直接深链 `#view=issues&scope=X` 时面包屑无数据可依。已在本轮修掉（`scopeLabel` 在 dashboard 未加载时按路径 basename 取名，加载后不匹配才回落「未知项目」）。
4. **一个不可复现的瞬时 500**：取证中见过一次 `/api/issues` 500（服务端响应，非网络中断）。随后 6 路并发复测（3×dashboard + 3×issues）全部 200（dashboard 8.1~8.3s、issues 5.4~5.5s），混合 5 路（2×dashboard+gate+measures+policy）亦全 200，**未复现**，归档为无解释瞬态。

### 8.4 开放问题状态
Q1–Q4 见 §7 末尾，本轮均按最小方案落地，未阻塞；新增 Q5：`unassigned` scope 的 8s 与 `limit=500` 的 34s 是否要在迭代 2 引入候选计数缓存/更深分页？

- Q1 scope token 用绝对路径明文，是否改为不透明 id？（现方案：明文 + 服务端白名单，已排除任意路径注入）
- Q2 全局视图是否应把"未归属"从项目列表里彻底分离成独立一级入口？（现方案：保留卡片 + 明确计数）
- Q3 状态流转要不要"确认弹窗"而非"撤销 toast"？（现方案：toast + 撤销，理由是误操作可逆比多一步点击更省）
- Q4 生效键表取值列：本轮声明不可用，后端补 per-key 值放迭代 4 还是提到迭代 2？
