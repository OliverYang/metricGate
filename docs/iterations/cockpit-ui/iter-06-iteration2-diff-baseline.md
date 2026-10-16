# iter-06 · 迭代 2（P1）差集基线 — 前置核对 + 锚点重导

> 角色：接手《COCKPIT_产品设计文档》§九 迭代 2（T2.1–T2.4）前的读侧勘察。
> 上游依据：`docs/COCKPIT_产品设计文档.md` §5.5 / §10-D2；`iter-05-p0-availability-and-scope.md`（迭代 1 落地子方案，另一会话产出）。
> goal：`mvacqkra-arplee`。**本文只勘察、不落码**；代码改动一律在恢复条件满足后进行。
> 行号是移动靶：本文所有行号为 2026-10-16 11:16 实测（app.js 2756 行 / style.css 1472 行），开工前必须重导。

---

## 0. 一句话结论

**迭代 2 的四条 D2 判据里，两条已基本在位（徽标三态、源码窗口），两条真缺（按语言分别取数、warn 带 + 徽标定位）。而迭代 1 的 dev-backend 尚未开始，与 T2.1/T2.2 要改的是同一个 `internal/cockpit/gate_api.go` —— 同树单 writer 规则下本 goal 必须挂起，等迭代 1 收口后再按本文差集开工。**

这是本 goal 唯一一处「不照 PRD 原文做」的地方，理由不是省事，是 PRD 写于迭代 1 之前，而 CU-02/CU-03 两个前序 goal 已经把 T2.1 的徽标三态与 T2.4 的源码窗口做掉了：照原文重实现会覆盖它们已经反向验证过的断言。

---

## 1. 前置断言核对：迭代 1 的 T1.3/T1.6 不在位

本 goal 契约把「迭代 1 的 T1.3/T1.6 已并入本树」写成前置断言（缺位即停）。逐条实测：

| 断言 | 判据 | 实测 | 结论 |
|---|---|---|---|
| T1.1 `/api/policy` 支持 scope | `grep -c 'URL.Query' internal/cockpit/policy_api.go` | **0**（`scope` 在该文件也是 0 命中；文件 mtime 10-15 09:31） | **未做** |
| T1.6 状态流转确认/撤销 | `grep -nE '撤销|undoLast|confirmDialog' app.js` | 只有 2 处「撤销失败：${error.message}」的 toast 文案（2188 / 2237），无确认步骤、无撤销动作；`ACTIONS["issue.status"]`(2171) 直接 PATCH 后 rerender | **未做** |
| T1.3 概览/门禁严格按 scope | 全 cockpit 包里只有 `issues_api.go` / `scope.go` / `source_api.go` 读 `r.URL.Query()`；**`gate_api.go` 与 `policy_api.go` 一处都不读**。`projectScopeRoot()`(gate_api.go:154-156) = `return policyRoot()` = `os.Getwd()`，所以「scope」实际是服务进程的工作目录，不是浏览器发来的那个 scope | 前端**已经在发**：`scopeParam`(app.js:391) = `(s) => (s.scope === "" ? "unassigned" : s.scope \|\| "all")`，调用点 app.js:486 / :538(`/api/policy`) / :546(`/api/gate`) / :554(`/api/measures`) —— 四个请求的 scope 参数全被后端丢弃 | **未做（前端已就位，缺后端）** |
| T1.0a issues LIMIT 下推 | `internal/store/issues_query.go:249` `issueCandidates` | **已做**（注释记实测 21.7s/1.17M 行 → 0.78s/最近 60 run） | **在位** |

goal `mvac0f32-nbtjxt`（迭代 1，owner 会话 `01a141fb-…`）在本文写作时的状态：`plan` complete、`dev-frontend` complete、**`dev-backend` pending**、`accept` pending，`currentTaskId = dev-backend`。

**冲突不是抽象的「同树危险」，是同一文件**：迭代 1 的 dev-backend 契约是 T1.0/T1.1/T1.2（含 scope 口径），而本 goal 的 T2.1（warn 带、`GateCondition` 加字段）与 T2.2（`RunMeasures` 分组）要改的正是 `internal/cockpit/gate_api.go:220 gateConditions` / `:259 gateBand` / `:330 measuresAPI` —— 三处全在 scope 口径的射程内。本仓已有并发覆盖的实例（`goal-contracts.md` 记 10:49 `internal/store/store.go` 短暂 `method LatestRunsIn already declared` 打断 `go build ./...`）。

→ **按契约第 1、2 条硬约束：本 goal 挂起，等迭代 1 的 `dev-backend` + `accept` 收口后恢复。**

### 1.5 并发 writer 的直接证据（不是推测）

| 证据 | 实测 |
|---|---|
| 新文件正被写入 | `internal/cockpit/scope.go` **未跟踪**、5399 B、mtime **11:31**（本文勘察时间 11:35） |
| 内容就是 T1.3 的 scope 解析 | `resolveScope(r *http.Request, dbPath string) (store.Window, error)`:50（读 `r.URL.Query().Get(scopeParam)`）、`applyScope(dst *store.Window, src store.Window)`:75、`knownProjectRoots(dbPath string) ([]string, error)`:88、`matchScopeRoot(roots []string, token string) (string, bool)`:107、`canonicalScopePath(path string)`:121 |
| 接线尚未完成 | 五个函数**全仓除 scope.go 自身外零调用点** —— 接入目标就是 `gate_api.go` / `policy_api.go`（即 §1 表里现在读不到 `URL.Query` 的那两个文件） |
| 另一会话 goal 当场在推 | `goal_events.jsonl` 尾部：`mvac0f32-nbtjxt` 的 `dev-frontend` 于 11:14 complete（app.js 2559→2753、style.css 1411→1472）、`dev-backend` 仍为 `currentTaskId`；`mvaclm26-g4opuu` 11:27 `completion_requested` + `audit_started` |

这直接解释了为什么本文的行号只能算快照：`scope.go` 一接入，`gateView:117` / `measuresView:390` 的 `projectScopeRoot()` 就会被换成 `resolveScope`，§2 关于「读不到 scope」的结论同时作废。

---

## 2. D2 四条的实测差集

### D2-1 徽标三态 + 状态列 error/warning/通过三色 + 点击徽标定位触发行

**已在位**（CU-03 遗产，勿重做）：
- 徽标三态：`gateBanner`(app.js:941) 已发 `is-${tone}`，tone ∈ `passed / failed / unknown`，字形 `✓ / ✕ / ?`，并带 `gate.reason` 与「运行 #N」。
- 徽标旁的 error 计数：`gateVerdict`(gate_api.go:202) 的 reason 已是 `"%d %s at or above fail_on=%s"`（:214），fail_on=error 时 blocking = `run.ErrorCount`。
- 「无数据」是独立档位：`gateHeadroom`(app.js:967) 把 `hasData === false` 当第三 tier，注释写明「drawing that as "0" or "—" makes an unmeasured condition look like a comfortable pass」。

**真缺**：
1. **状态列只有两态 + 无数据**：`gateConditionRows`(app.js:1963) 的 :1979 是
   ``c.failing ? badge("发现问题") : c.hasData ? badge("通过") : badge("无数据")``。
   `badge()`(:744) 虽有 `error / flagged / pass / unknown` 四种 tone，但**没有一条路径能产出 warning**——因为 warn 线在 payload 里不存在。
2. **payload 里没有 warn 带**（根因）：`gateBand`(gate_api.go:259) 末尾
   ```go
   if errAt == 0 {
       errAt = warn
   }
   ```
   把「只配了 warn」折进 error 阈值；且 `GateCondition`(:57) 只有 `Value`（判据用的那一条线）与 `Current`/`HasData`/`Failing`，**没有 `Warn` 字段**。要出三色必须让后端把 warn 与 error 两条线都发出来，前端才有「超 warn 未超 error」这一档可渲染。这不是前端能自修的。
3. **点击定位完全没有**：`gateBanner` 无 `data-action`，条件行无 `id`，全仓 `grep -nE 'data-goto|scrollIntoView' app.js` = **0**。PRD §5.5 的「在徽标旁给 "N 项 error" 可定位」目前是纯文本。
4. **口径连带：门禁徽标会静默显示别的目录的 run**。实测比先前记的更糟一层 —— `GateView`(gate_api.go:83-92) **根本没有 `Foreign` 字段**（只有 FailOn/Status/Reason/Conditions/RunID/Commit/StartedAt/Error）。`gateView:98` 在 :117 用 `LatestRunsIn(store.Window{Limit: 1, ProjectRoot: projectScopeRoot()})` 落空后，:127 `runs, _ = st.LatestRunsIn(store.Window{Limit: 1})` **直接回落到「最新一条任意运行」且不打任何标记** —— 连 err 都被 `_` 丢掉。`Foreign` 只存在于 `MeasuresView`（字段 :366、赋值 :406，所在函数是 `measuresView`:379，其 Window 是 :409 的 `fmt.Sprintf("last run %s", run.StartedAt)`），而前端 `grep -n foreign app.js` = **0**，所以 measures 侧的标记也没人渲染。
   D2-1 的「三态正确」必须把这一态也定义清楚（建议：给 `GateView` 补 `Foreign`，徽标在 foreign 时强制 `is-unknown` 措辞或在 reason 前置「非本项目运行」），否则修了三色仍然撒谎 —— 而且门禁侧现在是**无标记地**撒谎。

### D2-2 go/java 语言块度量值不相同（分别取数）

**全缺，且是四差集里唯一必须改后端的。**

- 取数口径：`gateConditions`(:220) 与 `measuresAPI`(:330→:414) 都调 `st.RunMeasures(run.ID)`；该 SQL（store/issues_query.go:598）是
  ```sql
  SELECT metric, MAX(value),
         SUM(CASE WHEN value >= warn_threshold THEN 1 ELSE 0 END)
  FROM metric_snapshots WHERE run_id = ?
  GROUP BY metric ORDER BY metric
  ```
  **分组键只有 metric**。所以同一个 run 里 `language: go` 与 `language: java` 的同名 measure 拿到的是**同一个全 run 聚合值** —— PRD 说「现网两语言块 9 项值一字不差」，根因就在这条 GROUP BY。
- 可行性：`metric_snapshots` 列 = run_id, file, scope, metric, value, warn_threshold, err_threshold, trace_id —— **没有 language 列**；`internal/model/model.go` 的 `MetricResult` 也没有 Language 字段。唯一可行路径是按 `file` 扩展名反查 `parser.LanguageForFile` 在 store 侧分组。
- 附带过期项：`knownLanguages`(gate_api.go:185) 仍是 9 门 `{"go","java","javascript","typescript","python","c","cpp","csharp","rust"}`，而 `parser.Supported()` 现在 13 门（本仓 iter-14 加的 sql / verilog / vbnet 不会出门禁表）。T2.2 落地时要么显式声明「门禁只出这 9 门」，要么改为从 `Supported()` 派生 —— **不能让它继续静默不一致**（iter-14 刚立的规则：缺席与有意不产出必须可区分）。
- **现网无法演示**：最新 run #1261（`history.db` 2214.4 MB）里 `.java` 的 metric 行 = **0**，`.v` = **0**，全部 42 行里 `.go` 占 42。所以 D2-2 的「go/java 值不相同」只能用**构造语料的 unit test** 证明，不能靠现网截图；收口时这一点要如实写，不伪造。

### D2-3 issues 状态按钮高亮当前态 + 范围行人类可读

**范围行已在位**（勿重做）：`app.js:1175` 渲染
``${s.issues.window ? html`<span class="issues-window" title="服务端实际查询的范围">范围：${s.issues.window}</span>` : ""}``，
其值来自 `store/issues_query.go:214` 的 `q.Window.String()`（实现在 `store/window.go:46`），输出形如 `last 60 run(s) · project /abs/path · since …`，**是人类可读文本而不是内部 id**。PRD 这条已满足；剩余只是措辞本地化（`last 60 run(s)` 是英文）这种 D4 级打磨，不属迭代 2。

**状态按钮是口径问题，不是缺席**：`statusSelect`(app.js:1256) 是原生 `<select class="issue-status tone-${statusOf(issue.status).tone}">` + `selected` 属性 —— 当前态在语义上已选中、tone 类已随状态变化，但不是「一排按钮里高亮当前态」。要做成按钮组就是替换控件；**且必须与迭代 1 的 T1.6（确认/撤销）一起设计**，否则换完控件 T1.6 的撤销入口还要再改一遍。这一条应在恢复后与迭代 1 的收口产物对齐再定。

### D2-4 源码窗口在真实数据下渲染出命中代码行

**基本已在位**（CU-02 遗产，勿重做）：`issueWhere`(app.js:1287) 渲染 `<ol class="source-lines">`，带 `.source-no` 行号、`is-anchor` 高亮命中行、`.source-anchor-note`（命中行显示 issue.message），空态有明确文案「代码窗口不可用」；数据链是 `ensureSource` + `ACTIONS["issue.select"]`；`render_all.mjs` 有 source 夹具（startLine 1 / endLine 3 / totalLines 12 / anchorLine 2），`smoke.mjs:702` 已断言

> `issue.select: the code window rendered no lines from the captured /api/source payload`

**剩余工作不是代码，是回验**：夹具与断言只证明「payload 有数据时画得出行」。D2-4 的「真实数据下」要在现网库上点一条真 issue 才有证据 —— 属收口阶段 Chrome 回验的一张截图，不额外落码。

---

## 3. 差集汇总（恢复后按这张表开工）

| D2 | PRD 原文 | 实测状态 | 恢复后要做的 | 估时 |
|---|---|---|---|---|
| 1 | 徽标三态 | **在位** | 无（只补 §2-D2-1 第 4 点：`GateView` 补 `Foreign` + 徽标措辞） | 0.1d |
| 1 | 状态列三色 | **缺** | `GateCondition` 加 `Warn`，`gateBand:259` 停止把 warn 折进 error，前端出「超 warn 未超 error」档 | 0.4d |
| 1 | 点击定位触发行 | **缺** | 徽标加 `data-action`，条件行加 `id`，锚点跳转 + 高亮 | 0.3d |
| 2 | go/java 分别取数 | **全缺** | `RunMeasures` 按扩展名反查语言分组；`knownLanguages` 与 `Supported()` 对齐；构造 go/java 差异语料的 unit test | 0.5d |
| 3 | 范围行可读 | **在位** | 无 | 0 |
| 3 | 状态按钮高亮 | **口径待定** | 与迭代 1 的 T1.6 撤销入口一起定控件形态 | 0.3d |
| 4 | 源码窗口真数据 | **在位** | 真实数据 Chrome 回验截图 | 0.1d |

合计 ≈ 1.7d（PRD 原估迭代 2 = 3d；差 1.3d 全在「前序 goal 已做过」的两条上）。

---

## 4. 恢复条件（三条全满足才开工）

1. goal `mvac0f32-nbtjxt`（迭代 1）的 `dev-backend` 与 `accept` 均为 complete，`accept` 按 §10-D1 六条给出通过/不通过证据。
2. `pgrep -fl 'cockpit-observe|go test|metricgate check|cockpit serve'` 为空 —— 观测与写码都无人占用（`check_path` 写 `.metricgate/history.db`，当前 2214.4 MB，并发会撞 `SQLITE_BUSY`）。
3. 重导锚点：`wc -l internal/cockpit/web/*` + `grep -n -E 'export const (gateBanner|gateMeta|gateConditionRows|issueWhere|statusSelect|badge)' app.js` + `grep -n -E 'func (gateBand|gateConditions|measuresAPI|gateVerdict)' internal/cockpit/gate_api.go`。本文行号在迭代 1 的 dev-backend 落码后**必定失效**（`gate_api.go` 是它的直接目标），照抄即错。

## 5. 遗留（不在本 goal 内，需另行决定）

- **`Foreign` 的前端兑现属迭代 1 还是迭代 2？** `MeasuresView` 的字段与注释承诺在迭代 1 的 T1.3 射程里，UI 却没人接；`GateView` 侧连字段都没有。本文按「T2.1 徽标口径的一部分」处理，因为徽标是它唯一的显示位；若迭代 1 的 accept 判定 T1.3 不通过，应回给迭代 1 而不是本 goal。
- **`internal/cockpit/scope.go` 正在被另一个 writer 写**（见 §1.5）：它一旦接入 `gate_api.go` / `policy_api.go`，§2 里所有 gate/measures 的行号与「读不到 scope」这条结论同时失效。恢复开工时**第一件事是重跑 §4 第 3 条的锚点重导**，不要拿本文的 D2 差集当现状。
- **`knownLanguages` 9 门 vs `Supported()` 13 门** 是 iter-14 遗留的静默不一致，与 cockpit 无关的一仓级问题（iter-14 只补了 metrics/rules 侧的 parity，`internal/cockpit/` 不在其范围内）。修法与 T2.2 同源（都要按语言分组），故挂在这里，不单开。

---

## 6. 挂起期检查点（每次唤醒跑一次 §4 三条，只记录差量）

| 时间 | §4-1 迭代 1 dev-backend/accept | §4-2 无人观测/写码 | §4-3 锚点 | scope.go 接线 | 判定 |
|---|---|---|---|---|---|
| 11:35 | dev-backend pending / accept pending | pgrep 空 | app.js 2756 | 五函数零调用点 | 停 |
| 11:47 | **同上，未变** | `cockpit-serve` 在跑（PID 47531，`:8899` LISTEN）；无 go test / check | scope.go mtime 11:31→**11:44**、5399→**7349 B**（另一 writer 仍在写）；gate_api.go 未动（02:22）；app.js 未动（11:11） | **仍只 scope.go 自己命中** `resolveScope/applyScope` | **停，条件 1+2 未满足** |
| 11:56 | **同上，未变**（dev-backend pending / accept pending） | 无 test/check/build；两个 `cockpit serve` 仍在：PID 47531 `./metricgate cockpit serve -addr 127.0.0.1:8899`、PID 95463 `/tmp/mg-iter1 cockpit serve -addr 127.0.0.1:8877`（后者是迭代 1 自己的临时二进制，说明对方还在迭代中） | scope.go 自 11:44 未再动（writer 静默 12 min）；gate_api.go / app.js 同 11:47 | **仍只 scope.go 自己命中** | **停：§4-1 未满足；静默 ≠ 收口** |

> 本表曾有一处自错的教训：11:56 行初版写「cockpit serve 已停」，因为那次 pgrep 的 pattern 只给了 `cockpit-observe|go test|metricgate check`，漏了 §4-2 原文要求的 `cockpit serve` 项 —— **收紧 pattern 会造出假「已清空」**。§4-2 的判据必须原样用四个 pattern。

检查点只读，不落码。`cockpit serve` 常驻不是阻塞根（它只读库），真正未满足的是 §4-1：`internal/cockpit/scope.go` 还没接到 `gate_api.go`，此时改 `gateBand:259` / `gateConditions:220` / `measuresAPI:330` 会直接覆盖对方正在进行的工作。

**挂起期不能自行转成定时等待**：11:55 试过 `update_goal continuation.kind=wait`（wait_id `iter1-closure-gate`，1800s 轮询），被工具拒：`New waits require strictExecutionContract=true in /goal-settings`。该开关属用户侧配置，未授权我不改；因此 checkpoint 仍按约 3 min 一次推醒本会话，每轮只做上面这张表的差量核对。
