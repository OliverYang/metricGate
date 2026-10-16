# 迭代 2 记录 — 消除自相矛盾（P1）

**状态**：T2.1–T2.4 完成（本 goal `mvacqkra-arplee` 范围）；T2.5–T2.10 不在本 goal 内（见 §5）。
**施工位置**：先在 worktree `/Users/mac/Downloads/metricGate-iter2`（分支 `iter2-language-and-ui`）写与验，**收口前已逐 hunk 合并回主树 `/Users/mac/Downloads/metricGate`（branch `work`）**；本文 §6 的观测数字全部是**主树**实测，不是 worktree 的。
**为什么在 worktree**：迭代 1（goal `mvac0f32-nbtjxt`）在同一棵树的 `internal/cockpit/` 上并发写 `scope.go`/`gate_api.go`。用户决定（m05496）「继续做，新建个分支然后合并进迭代 1」——不再等迭代 1 收口，但也不能在主树硬写，故用 worktree 隔离，改动靠 `/tmp/sync-iter2.sh` 单向同步。
**为什么最终是逐 hunk 合并而不是整文件拷贝**：迭代 1 收口后主树已**领先** worktree —— 主树 `web/app.js` 154275 B（worktree 137022 B）、`web/style.css` 65391 B（63590 B）、`gate_api.go` 里已有 `GateView.Foreign`，`source_api.go` 里已有 `writeJSONError`，`app.js` 里已有 `state.limit`/`fetchRules`/`loadPending`/`issuesWindow`/`gate.foreign` 与迭代 3 的 7 处「未归属运行」。**整文件拷贝会删掉别人的工作**，所以只有 6 个纯新增文件走 `cp`，其余 6 个逐个 hunk 打补丁（过程与逐文件差异数见 §8）。

**前置勘察**：`iter-06-iteration2-diff-baseline.md`（D2 四条差集、锚点、并发 writer 证据、恢复条件）。

---

## 0. 结论

D2 四条**全部通过**，且每条都有「反向验证」（删实现 → 断言报红）而不只是「正向跑绿」。

| 验收项 | 判据 | 结论 | 证据 |
|---|---|---|---|
| D2-1 | 徽标三态正确；状态列 error/warning/通过三色；点击徽标定位到触发 error 的条件行 | ✅ 通过 | §1；真实浏览器 8 项断言全绿 |
| D2-2 | go/java 语言块度量值不相同（分别取数） | ✅ 通过 | §2；真实数据 go 24 vs javascript 14，java 全 `无数据` |
| D2-3 | issues 状态按钮高亮当前态；范围行为人类可读文本 | ✅ 通过 | §3；`aria-current` 此前全仓 0 次 |
| D2-4 | 源码窗口在真实数据下渲染出命中代码行 | ✅ 通过 | §4；修掉一个真 panic |

**顺带修掉两个真缺陷**（都不是 D2 判据要求的，是实测中撞出来的）：
1. **后端 panic**：finding 指向**空文件**时 `/api/source` 直接 `index out of range [-1]` 崩（§4.1）。
2. **相对路径读不到源码**：finding 的 `file` 是仓库相对路径时，源码窗口永远返回「不可读」（§4.2）。

---

## 1. D2-1 门禁徽标三态 + 状态列 + 定位

### 已在位 vs 本迭代补的

勘察（`iter-06-iteration2-diff-baseline.md` §2）当时判「徽标三态已在位」，**过时了**：`gateBanner` 已有 `is-passed`/`is-failed`/`is-unknown` 三态，但另外三项是空的：

| 项 | 状态 | 处置 |
|---|---|---|
| 三态 class | 已在位 | 只补断言 |
| **warn 带** | 已在位（勘察判据过时） | `GateCondition.Warn`（`internal/cockpit/gate_api.go:87`）与 `Warning`（:92）都在，`gateBand`(:276) 返回 `(warn, errAt int, ok bool)`；**勘察时看到的 `if errAt == 0 { errAt = warn }` 折带仍在**（:281），但它折的是「无 error 线时用 warn 线当 error 线」，不是「把 warn 值丢掉」——`warn` 本身已单独返回并进载荷。前端 `gateHeadroom` 用 `c.warn` 出「超出 9」/「余 7」。 |
| **点击定位** | **缺** | 本迭代新增 `conditionId`(:948) / `gateLocateTarget`(:967) / `ACTIONS["gate.locate"]`(:2212) |
| 状态列三色 | 已在位 | `badge()` 四 tone；新增「warning 行不得显示『通过』」断言 |

### 定位的实现

- `conditionId(c)` = `cond-${c.language || "all"}-${c.measure || "unknown"}`（app.js:1018，主树行号，下同）——**语言进 id**，否则两种语言的同一度量共用一个锚点，跳转会落到另一种语言的行上。该碰撞有专门断言。
- `gateLocateTarget(gate)`（:1030）取**第一个 `failing` 行**，取不到返回 `""`。测试 fixture 故意把 warning 行排在首位，让「取第一行」的实现报红。
- `ACTIONS["gate.locate"]`（:2604）`scrollIntoView({block:"center",behavior:"smooth"})` + 临时加 `is-located` 类，2600ms 后移除。类名临时是设计：永久染色会变成表格的正常外观，下一个读者分不出它是否有含义。

### 真实浏览器回验（arm64 Chrome，真数据，非 fixture）

脚本 `docs/iterations/cockpit-ui/shots-iter2/verify_iter2.mjs`（CDP 驱动，每项一个 target，用完关掉）。**合并进主树之后又在主树代码上重跑了一遍**：`go build -o /tmp/iter2-main-mg ./cmd/metricgate`（42.7s）→ `cockpit serve -db /tmp/iter2-shots.db -addr 127.0.0.1:8893`（db 是主树 `.metricgate/history.db` 的 APFS clone，0.05s）→ `node verify_iter2.mjs <outdir> http://127.0.0.1:8893 /Users/mac/Downloads/metricGate` → **16 项断言全 PASS / RC=0**，本节截图就是这一轮产出的。脚本原本硬编码 `REPO = "/Users/mac/Downloads/metricGate"`（审计员点名的瑕疵），已改成第三个参数（缺省 `process.cwd()`）。

**一个必须记下来的假阳性教训**：第一次重跑我拿**主树当前库**（run #1351）而不是当初的 run #1308 做回验，脚本立刻报 7 条 FAIL —— 因为 #1351 是 `metricgate check -diff` 的增量运行，`.go` 之外没有文件、门禁是 `PASSED`，于是「banner 必须 is-failed」不成立；源码窗口也因为增量运行没扫那个文件而取不到行。**这不是回归，是语料不匹配**：门禁断言绑的是「某一次全量运行的形状」，回验脚本必须显式绑 db（所以留了 `[base]` 参数而不是写死）。换成 #1308 那份库后 16 条全绿。

11 项 → 16 项的差是重构后拆出来的独立检查点（`lang-*` 四条 + `issues-row-present`），断言集合没缩小。

```
banner: {"cls":"gate-banner is-failed","text":"✕ FAILED 7 errors at or above fail_on=error
        运行 #1308 定位到触发的条件行","btn":true,"target":"cond-go-cyclomatic-complexity"}
PASS gate-banner-failed / gate-locate-present / gate-locate-target / gate-target-row-exists
PASS gate-target-scrolled-out     ← 先滚到底，证明目标行真的不在视口内
after click: {"marked":true,"inViewport":true,"scrollY":0,"top":407,
              "text":"Cyclomatic complexity ≤ 15 24 超出 9 policy_file 发现问题"}
PASS gate-locate-marks-row / gate-locate-scrolls
```

`gate-target-scrolled-out` 这一步是必要的：第一版脚本没先滚动，行本来就在视口里，`scrollY` 全程 0，**「会滚动」这条其实没被证明**。先滚到底再点，才是反证。

截图**已归档进仓**（契约要求「归档到 `docs/iterations/cockpit-ui/`」，不是只放 `/tmp`）：
- `docs/iterations/cockpit-ui/shots-iter2/01-gate-before-click.png`
- `docs/iterations/cockpit-ui/shots-iter2/02-gate-after-click-located.png` — 可见 `go` 段真实值（24/71/93/919/5/8/450561/24/1）、`java` 段全 `无数据`、首行 `Cyclomatic complexity` 被高亮（左侧橙色边条）
- `docs/iterations/cockpit-ui/shots-iter2/03-source-window-real-lines.png` — 见 §4
- `docs/iterations/cockpit-ui/shots-iter2/verify_iter2.mjs` — 回验脚本本体（一起归档，否则别人无法复算那 11 项断言）

（同一份副本也在 `/tmp/review/iter2-real/`，仓内为权威。本仓此前 `git ls-files` 中图片数为 0，iter-05 的先例是只引用 `/tmp` 路径；本迭代契约明写「归档到 docs/」，故此处**打破先例**将图入库。若不希望仓内带二进制，需用户明确改判。）

---

## 2. D2-2 按语言分别取数

### 为什么只能从文件扩展名反查

`MetricResult`（`internal/model/model.go:109-118`）**无 Language 字段**；`metric_snapshots` 表列 = `run_id file scope metric value warn_threshold err_threshold trace_id`，**无 language 列**。故唯一路径是按 `file` 反查。

### 实现

新文件 `internal/store/measures_lang.go`：

```
func (s *Store) RunMeasuresByLang(runID int64) (map[string][]RunMeasure, error)
```
SQL `SELECT metric, file, MAX(value), SUM(CASE WHEN value >= warn_threshold THEN 1 ELSE 0 END)
FROM metric_snapshots WHERE run_id = ? GROUP BY metric, file ORDER BY metric, file`，
语言用 `parser.LanguageForFile(filepath.Base(file))` 反查（**不复制扩展名表**），未注册扩展 `continue` 丢弃而非并入某个桶（并入会让 `notes.txt` 的 4000 分算到 go 头上）。辅助 `mergeMeasure(out, at, lang, m)` 用 `lang+"\x00"+metric` 键，worst 取 max、exceeded 求和。

`internal/cockpit/gate_api.go` 的 `gateConditions`(:229) 改用 `byLang, err := st.RunMeasuresByLang(run.ID)`（:236），`worst` map **移进** `for _, lang := range configLangs(cfg)` 循环内（原来在循环外，全 run 一个 worst —— 这正是 go/java 两块值一字不差的根因）。

### 真实数据证据

`/api/gate?scope=all`（run #1308）：

| 语言 | cyclomatic | cognitive | function-length | file-length | fan-out |
|---|---|---|---|---|---|
| go | **24** FAIL | **71** FAIL | 93 WARN | **919** FAIL | **24** FAIL |
| javascript | **14** WARN | **16** WARN | **52** WARN | **2894** WARN | **17** FAIL |
| java | 0 `无数据` | 0 `无数据` | 0 `无数据` | 0 `无数据` | 0 `无数据` |

修复前这三块会印同一个 run-wide 最差值。库内实况：`metric_snapshots` 19792728 行中 `.java` 仅 526 行、`.go` 15325438 行、`.v/.sv` 421884 行、`.sql` 295 行 —— 所以 **java 全 `无数据` 是如实结果，不是缺陷**：本次 run 里几乎没有 java 文件。

### 载荷文案同步（否则界面自相矛盾）

`app.js:2383` 的 `panel-foot` 原文写「实测列按度量聚合、**不分语言** —— 同一度量在不同语言下显示的是本次运行里的同一个最差单文件值」。这是修复前的行为描述，**修复后就是假话**。改写为「实测列按语言分组、组内取该语言本次运行的最差单文件值：同一度量在两种语言下是两个独立结论，某语言本次没有文件时显示『无数据』而不是借用别的语言的值」，并加双向断言（必须含「按语言分组」、不得含「不分语言」）。

---

## 3. D2-3 issues 状态高亮 + 范围行可读

### 已在位 vs 本迭代补的

| 项 | 状态 | 处置 |
|---|---|---|
| 范围行人类可读 | 已在位 | `s.issues.window` 已由服务端给人类可读句子；只补断言（必须有「范围：」+ `title="服务端实际查询的范围"`，空 window 不得画出空的「范围：」） |
| `scopeLabel` 四态 | 已在位 | 补断言：`{scope:""}`→`未归属记录`、`{scope:null}`→`全部项目`、已知项目→项目名、未知→`未知项目`；未加载态取路径尾段 |
| **状态按钮高亮当前态** | **缺** | `facetRow` 补 `aria-current` |

`aria-current` 在修复前全仓出现 **0 次** —— 即「已应用但只靠颜色」。修复：`facetRow` 的 live chip 同时带 `is-active` **和** `aria-current="true"`；idle chip 两者皆无。

### 反向验证

删掉 `is-active` 与 `aria-current` 两行 → 报红两条，RC=1：
```
FAIL issues status: the live status chip is not marked is-active
FAIL issues status: ... carries no aria-current, so the applied filter is sight-only
```

---

## 4. D2-4 源码窗口 —— 并修掉两个真缺陷

渲染路径本已在位（`issueWhere`(app.js:1501) 已输出 `.source-no` 行号、`is-anchor` 锚点行、`hl-*` 命中高亮、「已截断」标记）。真缺口是**空态**，加上实测撞出的两个后端缺陷。

### 4.1 后端 panic（空文件 → `/api/source` 崩溃）

`internal/cockpit/source_api.go` 的 `buildSourceView`(:234) 在空文件时 `windowBounds`(:163) 返回 `(0,0)`，于是 `for n := first; n <= last` 跑 `n=0` → `lines[n-1]` = `lines[-1]`：

```
panic: runtime error: index out of range [-1]
  metricgate/internal/cockpit.buildSourceView(...) 循环 source_api.go:264
```
即「finding 指向一个空文件」会让接口 500/崩，而不是返回空窗口。修复：`len(lines) == 0` 时 `view.Lines = []SourceLine{}` 并提前 return（**显式空 slice 而非 nil**，让载荷走 `"lines":[]` 而不是 `null` —— `null` 只能靠 falsy 侥幸通过，而**整个 key 缺失**与「老服务端从不发 lines」不可区分）。

反证：给早退加 `&& false` → panic 重现。

### 4.2 相对路径读不到源码（更严重：功能整体失效）

`readSourceWindow`(:148) 首行 `if path == "" || !filepath.IsAbs(path) { return nil, fmt.Errorf("finding has no readable file path") }`。而 `metric_snapshots.file` 存的是**仓库相对路径**（如 `internal/cockpit/dashboard.go`）—— 于是真实数据下源码窗口**永远**返回「不可读」，D2-4 在真机上根本不可能通过。

修复：新增 `resolveSourcePath(st *store.Store, issue store.IssueRow) (string, error)`（:130）—— 先看 `filepath.IsAbs(issue.File)`，是绝对路径直接用；否则用 `issue.LastRunID` 查 run 的 `ProjectRoot` 拼出绝对路径。相应地 `buildSourceView` 签名加了 `st *store.Store`（:237）。修复前后对比（真实 run）：

| | 修复前 | 修复后 |
|---|---|---|
| `reason` | `finding has no readable file path` | `""` |
| `lines` | 0 | 31 行真实代码 |
| `totalLines` | 0 | 116 |
| `window` | — | `40-70 truncated: True` |
| `anchorLine` | — | 55 |

### 4.3 前端空态

`issueWhere` 在 `!source.lines || !source.lines.length` 时提前返回空态 `<div class="empty-state">` + `<strong>代码窗口为空</strong>` + 「`<文件>` 在锚点附近没有可显示的行（文件可能已变化）。」。此前会画出**一个空 `<ol class="source-lines">` + 「第 0-0 行 / 共 0 行」** —— 读起来像「文件内容为空」而不是「窗口取不到行」。`source.reason` 分支优先级更高（不可用 vs 为空是两回事）。

### 4.4 真实浏览器回验

```
selected fingerprint: 95fc457e4844442e
source: {"numbers":["169","170","171","172","173"],"rows":31,"anchors":1,"highlighted":10,
         "body":"... registry_parity_test.go:184 ... 第 169-199 行 / 共 337 行 · 锚点 184 · 已截断"}
PASS source-rows-rendered / source-numbers-present / source-anchor-marked / source-no-unreadable-reason
```

截图 `docs/iterations/cockpit-ui/shots-iter2/03-source-window-real-lines.png`：可见行号 169–199、锚点行 184 `func TestPerLanguageRegistryParity(t *testing.T) {` 重复 finding 文案、`hl-*` 高亮 10 行。

### 4.5 新增测试

`internal/cockpit/source_api_test.go`（4 用例，用 `os.WriteFile(abs, nil, 0o600)` 造空文件种子）：
- `TestSourceAPIEmptyFileIsAnEmptyWindowNotAReason`(:76)：空文件 → `Reason==""`、`len(Lines)==0`、`TotalLines==0`、`StartLine==EndLine==0`。
- `TestSourceAPIResolvesRelativePathAgainstRunProjectRoot`(:117)：相对路径能拼到 run 的 `ProjectRoot` 并读出代码行。
- `TestSourceAPIUnanchoredRelativePathIsAReason`(:137)：拼不到（无 run / 无 root）时给 reason 而非崩。
- `TestSourceAPIEmptyWindowSendsAnEmptyList`(:154)：`json.Marshal` 后必须含 `"lines":[]`。

---

## 5. 不在本 goal 范围（如实登记，不要当成已完成）

PRD 迭代 2 共 10 项，本 goal 只覆盖 **T2.1–T2.4**（差集口径，见 `iter-06-iteration2-diff-baseline.md` §2）：

| ID | 项 | 归属 |
|---|---|---|
| T2.5 | 错误态与计数不再同屏矛盾 | 迭代 1 收口残留 → 迭代 1 的 accept |
| T2.6 | 透出服务端错误正文 | 同上（迭代 1 已有 `writeJSONError` 基础设施） |
| T2.7 | `scope=unassigned` 8.10s / `?limit=500` 34s 热点 | 迭代 1（T1.0a 已做 `issueCandidates` LIMIT 下推） |
| T2.8 | 瞬时 500 复现与慢请求防护 | 迭代 1 |
| T2.9 | 侧栏「日志未启用」与真值不符 | 未做 |
| T2.10 | 顶栏「尚未同步」混淆 | 未做 |

另有一条**已知不一致**未修：`internal/cockpit/gate_api.go:194` 的 `var knownLanguages = []string{"go", "java", "javascript", "typescript", "python", "c", "cpp", "csharp", "rust"}` 仍是 9 门，而 `parser.Supported()` 已是 13 门（iter-14 引入 SQL/Verilog/VB.NET 后未同步）。与 T2.2 同源（都涉及语言集合），但本 goal 未覆盖该文件，登记于此。

---

## 6. 观测与闸门（最终树复跑）

下列数字是在**全部反证还原、文档与截图入库之后**重跑的，不是中途快照：

```
$ bash scripts/cockpit-observe.sh fast        # 主树 /Users/mac/Downloads/metricGate
== node --check ==
== render_all (all views x all scopes, offline) ==
render_all: 9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok
== live smoke (real payloads through a DOM stub) ==
REQUEST /api/issues?limit=1&page=1&pageSize=50&status=open&sort=severity&scope=all
REQUEST /api/rules?scope=all
REQUEST /api/policy?scope=all
== go build ==
== go test ./internal/cockpit/... ==
ok  	metricgate/internal/cockpit	23.927s
== gate (metricGate findings on the web tree) ==
  render_all.mjs:12: [warning] unknown-dependency (import ./app.js is not a declared dependency of this repository; ...)

summary: 0 errors, 16 warnings, 0 notes, 429 metric snapshots
run #1325 stored in .metricgate/history.db
gate: PASSED (fail_on=error)
```

```
$ check_path('/Users/mac/Downloads/metricGate/internal/cockpit/web', diff_only:true)
{"verdict":"PASS","files":3,"errors":0,"warnings":15,"hidden":1}
```

```
$ go test ./... -count=1        # 主树，RC=0
47 个包 ok、6 个包 no test files、0 FAIL
$ go test ./internal/cockpit/... ./internal/store/... -count=1
132 passed in 2 packages
$ gofmt -l cmd/ internal/       # 空
```

**主树 `check_path` 的 15 条 warning 全是 `app.js`/`render_all.mjs` 的既有指标告警**（`unknown-dependency` 2、`fan-out` 4、`cyclomatic-complexity` 5、`function-length` 2、`call-chain-depth` 1、`cognitive-complexity` 1），本 goal 未引入 error，也未引入新种类的 warning。

### 6.1 逐文件 `check_code`（收口口径，改后重跑）

| 文件 | errors | warnings | 内容 |
|---|---|---|---|
| `internal/cockpit/source_api.go` | 0 | 0 | PASS |
| `internal/cockpit/gate_api.go` | 0 | 6 | 既有 metric 告警（`gateConditions`/`measuresView` 的 fan-out 等） |
| `internal/store/measures_lang_test.go` | 0 | 3 | `TestRunMeasuresByLangKeepsLanguagesApart` cyclomatic 14（warn 10）/ function-length 53（warn 50）/ npath 2304（warn 200）——**都在 warning 线内**，error 线（15/100）由 `worstMeasure` 辅助拆出来守住（见 §7） |
| `docs/…/shots-iter2/verify_iter2.mjs` | 0 | 9 | 归档的回验脚本：1 条 empty block + fan-out/depth 类；`checkGate` 已从 function-length 106 拆到 error 线以下 |

### 6.2 文档改动后的复跑（防止改文档时碰坏代码）

```
$ node --check app.js && node render_all.mjs     RC=0
render_all: 9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok
```

行号锚点（§1/§2/§3/§4 引用的 app.js 与 gate_api.go 行号）在合并后已按**主树**重新对齐：`conditionId:1018`、`gateLocateTarget:1030`、`gateBanner:1036`、`facetRow:1142`、`issueWhere:1501`、`gateConditionRows:2330`、`ACTIONS["gate.locate"]:2604`、panel-foot `:2383`；`gate_api.go` `GateCondition.Warn:87`/`Warning:92`/`gateConditions:229`/`byLang:236`/`gateBand:276`/`knownLanguages:194`；`source_api.go` `resolveSourcePath:130`/`buildSourceView:237`。`go build ./...` Success、`gofmt -l cmd/ internal/` 空。

---

## 7. 反向验证汇总

每条都是「**编译/语法通过 + 断言报红**」——编译失败不算反证（第一次用 `perl -0pi -e` 正则改文件，正则没匹配上是**空跑**，报了假绿）。下表 7 组全部**在本次收口里重跑过**（不是引用早先的结果），原始输出逐条列在 §7.1。

| # | 实现 | 反向操作 | 报红原文 |
|---|---|---|---|
| 1 | `RunMeasuresByLang` 按扩展名反查语言 | 换成恒返回 `parser.LanguageID("go"), true` | `store (0 passed, 2 failed)`：`= 1 languages, want 2`；`map[go:[{file-length 4000 1}]]`（.txt 被塞进 go 桶） |
| 2 | `gateConditions` 的 `worst` 按语言建 | 把 `worst` 改回扫全 `byLang`（run-wide） | `gate_api_test.go:176: java hasData true / current 15, want false / 0: no java file was measured i…` |
| 3 | `gateLocateTarget` 取失败行 + 横幅定位分支 | 分支前置 `${false && target ? …}` | `gate locate: the banner has no locate control aimed at cond-go-cyclomatic-complexity` |
| 4 | `facetRow` 的 `is-active` + `aria-current` | 删两行 | `the live status chip is not marked is-active` / `carries no aria-current, so the applied filter is sight-only` |
| 5 | 源码窗口空态 | 删 `!source.lines.length` 早退分支 | 3 条 FAIL（未说空 / 仍画列表框 / 仍印「第 0-0 行」） |
| 6 | `buildSourceView` 空文件早退 | 早退条件加 `&& false` | `panic: runtime error: index out of range [-1]` |
| 7 | 按语言取数（前端行值） | 行值改读 `conditions[0].current` | `gate: the java section does not show java's own measured value` |
| 8 | 载荷文案「按语言分组」 | 换回旧文案「不分语言」 | `the panel does not say the measurement column is grouped by language` / `still claims ... not split by language` |

### 7.1 原始输出

```
# 1 store 层反查
$ go test ./internal/store/ -run TestRunMeasuresByLang
RC=1  store (0 passed, 2 failed)
  [FAIL] TestRunMeasuresByLangKeepsLanguagesApart
     measures_lang_test.go:54: RunMeasuresByLang() = 1 languages, want 2: map[go:[{cyclomatic-complexi...
  [FAIL] TestRunMeasuresByLangDropsUnregisteredFiles
     measures_lang_test.go:104: RunMeasuresByLang() = map[go:[{file-length 4000 1}]], want only go wit...

# 2 gate_api.go 反证（worst 改回 run-wide）
$ go test ./internal/cockpit/ -run TestGateConditions
RC=1  cockpit (3 passed, 1 failed)
  [FAIL] TestGateConditionsDoNotBorrowAnotherLanguagesMeasurement
     gate_api_test.go:176: java hasData true / current 15, want false / 0: no java file was measured i...

# 3 徽标定位分支
$ node render_all.mjs
RC=1  FAIL gate locate: the banner has no locate control aimed at cond-go-cyclomatic-complexity

# 4 facetRow 高亮
$ node render_all.mjs
RC=1  FAIL issues status: the live status chip is not marked is-active
     FAIL issues status: the live status chip carries no aria-current, so the applied filter is sight-only

# 5 源码窗口空态
$ node render_all.mjs
RC=1  FAIL source window: an empty window did not say it is empty
     FAIL source window: an empty window still drew the list frame, which reads as an empty file
     FAIL source window: an empty window printed "第 0-0 行" as if it were a range

# 6 buildSourceView 早退
$ go test ./internal/cockpit/ -run TestSourceAPI
RC=1  cockpit (5 passed, 1 failed)
  [FAIL] TestSourceAPIEmptyFileIsAnEmptyWindowNotAReason
     panic: runtime error: index out of range [-1] [recovered, repanicked]
```

每组还原后均重新跑绿（`render_all ... ok` / `Go test: N passed`）。

### 7.2 反向验证在**主树代码**上重跑（不是 worktree）

合并进主树之后，8 组反证全部在主树的代码上重做了一遍 —— 施工位置是一次性变异副本 `/tmp/mg-mut`（`internal/`+`cmd/`+`third_party/` 从主树 `tar` 复制，基线与主树 `cmp` 全 SAME）。**不在主树原地做变异**：那里另有并发 writer 在跑 test，改一下就会被误归因或被人还原。

| # | 变异 | 主树代码上的报红原文 |
|---|---|---|
| 1 | `parser.LanguageForFile(filepath.Base(file))` → `parser.LanguageID("go"), true` | `store (0 passed, 2 failed)`；`measures_lang_test.go:54 = 1 languages, want 2`；`:106 map[go:[{file-length 4000 1}]]` |
| 2 | `worst` 改回扫全 `byLang`（run-wide） | `cockpit (6 passed, 1 failed)`；`gate_api_test.go:178: java hasData true / current 15, want false / 0` |
| 3 | 定位分支前置 `${false && target ? …}` | `FAIL gate locate: the banner has no locate control aimed at cond-go-cyclomatic-complexity`（RC=1） |
| 4 | 删 `facetRow` 的 `is-active` + `aria-current` | 2 条 FAIL：`not marked is-active` / `carries no aria-current, so the applied filter is sight-only` |
| 5 | 删 `issueWhere` 空态早退 | 3 条 FAIL：`did not say it is empty` / `still drew the list frame` / `printed "第 0-0 行"` |
| 6 | `if len(lines) == 0 && false` | `cockpit (5 passed, 1 failed)`；`panic: runtime error: index out of range [-1]` |
| 7 | 实测列改 `conditions[0].current` | 3 条 FAIL：`unmeasured condition still draws the em-dash` / `a measured 0 and an unmeasured value are not distinguishable` / `the java section does not show java's own measured value` |
| 8 | panel-foot 换回「不分语言」 | 2 条 FAIL：`does not say the measurement column is read per language` / `still claims ... not split by language` |

**变异工具坑（本轮新踩）**：`python3` 正则/字符串替换前先 `assert count==1`。我有一次替换锚点 `class="facet-row${active ? " is-active" : ""}"` 匹配失败（AssertionError），脚本在**第一条 replace 之后**就抛异常，于是文件停在半成品状态，而后续 `node render_all.mjs` 报的是**上一组变异**的 FAIL —— 差点把「#3 的残留」当成「#4 的反证」。要么用 edit 工具，要么让脚本在任何写入前完成全部断言。

### 7.3 全量回归（主树）

```
$ go test ./... -count=1                     RC=0
47 包 ok / 6 包 [no test files] / 0 FAIL / 0 panic
$ go test ./... -count=1 -v | grep -c '^--- PASS'
753
$ go test ./internal/cockpit/... ./internal/store/... -count=1
132 passed in 2 packages
$ gofmt -l cmd/ internal/
（空）
```

**观测口径教训**：`node render_all.mjs 2>&1 | tail -12; echo "RC=$?"` 取到的是 `tail` 的退出码（恒 0），会把报红读成绿。必须 `node render_all.mjs > f 2>&1; echo $?` 或 `set -o pipefail`。

---

## 8. 合并进主树的逐文件账（2026-10-16 16:2x–17:0x）

独立审计（否决第一次 `update_goal(complete)`）给的决定性缺口是「主树完全没有迭代 2、worktree 分支零 commit、目录一清就全丢」。已按「逐 hunk 打补丁、禁止整文件拷贝」落地：

| 文件 | 方式 | 落了什么 / 保住了什么 |
|---|---|---|
| `internal/store/measures_lang.go` | `cp`（纯新增） | `RunMeasuresByLang` + `mergeMeasure` |
| `internal/store/measures_lang_test.go` | `cp`（纯新增） | 2 用例；`worstMeasure` 辅助（把 cyclomatic 16 的 error 拆到 15 以下） |
| `internal/store/run_lookup.go` | `cp`（纯新增） | `runColumns` + `RunByID`；从 `read.go` 拆出以免加重它既有的 file-length 告警 |
| `internal/cockpit/source_api_test.go` | `cp`（纯新增） | 4 用例（空文件 / 相对路径 / 无锚点相对路径 / 载荷 `"lines":[]`） |
| `internal/store/read.go` | 1 处 edit | `latestRunsScoped` 的 runs 列清单改用 `runColumns`（去掉重复） |
| `internal/cockpit/gate_api.go` | 5 处 edit | `GateCondition.Warn`/`Warning`、`gateConditions` 用 `RunMeasuresByLang` 且 `worst` 移到语言循环**内**、`gateBand` 返回 `(warn, errAt int, ok bool)`；**主树的 `GateView.Foreign` 与 6 处 `writeJSONError` 未动** |
| `internal/cockpit/gate_api_test.go` | 6 处 edit | `gateTestRunFiles` + `TestGateConditionsDoNotBorrowAnotherLanguagesMeasurement`；**主树的 `TestGateMarksAnotherProjectsRunAsForeign`/`TestMeasuresMarksAnotherProjectsRunAsForeign` 完整保留** |
| `internal/cockpit/source_api.go` | 2 处 edit | `resolveSourcePath`（按 `runs.project_root` 拼相对路径，不猜 cwd）+ `buildSourceView` 空文件早退 |
| `internal/cockpit/web/app.js` | 7 处 edit | `conditionId`/`gateLocateTarget`/`gate.locate`/空态/`aria-current`/状态列四档/panel-foot 文案；`state.limit`·`fetchRules`·`loadPending`·`issuesWindow`·`gate.foreign`·7 处「未归属运行」全在 |
| `internal/cockpit/web/render_all.mjs` | 5 处 edit | T2.1 三态+定位、T2.2 语言切片、T2.3 facet/范围行/scopeLabel、D2-4 行号与空态 |
| `internal/cockpit/web/style.css` | 1 处 edit | `.gate-locate` + `tr.is-located`；主树 92 行独有 CSS 未动 |
| `docs/COCKPIT_产品设计文档.md` | 1 处 edit | D2 四条 `[ ]`→`[x]` + 实测数字 + 标题标注 T2.5–T2.10 未做 |
| `docs/iterations/cockpit-ui/iter-07-iteration2-close.md`、`shots-iter2/` | `cp`（纯新增） | 本文与 3 张真数据截图 + `verify_iter2.mjs` |

**移植中踩到的一个真失败（值得留）**：我从 worktree 带过去的断言「`issues.window` 为空时不得画 `范围：`」在主树**不成立** —— 主树的 `issuesWindow(s) = s.issues.window || localWindowLabel(s)`（`app.js:1302-1308`）是迭代 3 有意加的本地回落，永远非空。判据改成「禁止出现空的 `范围：</`，且必须匹配 `范围：最新 N 次运行`」。**跨分支移植断言之前，先确认主树当前的行为是不是别人的改进，不要把它当 bug 断掉。**

---

## 9. 遗留

1. **整棵 `work` 分支仍未提交，且这不是本 goal 能单独决定的**：迭代 2 的代码已逐 hunk 落进主树工作区，但主树 `git status --porcelain` = 184 项、HEAD 仍是 `5c1595c`。三种收法各有代价，都需用户放行：(a) 只提交本 goal 的文件 → **构建不出来**（主树的 `internal/cockpit/gate_api.go`、`scope.go`、`errors.go`、`rules_api.go`、`web/render_all.mjs`、`web/fixtures.json` 全是未跟踪，HEAD 里只有 9 个 cockpit 文件，我的 `gate_api.go` 依赖 `configLangs`/`writeJSONError`）；(b) 提交全部 184 项 → 把迭代 1/3 两个会话的在制品以本 goal 的名义固化；(c) 等它们的会话各自 commit。pre-commit 会跑 `metricgate check -diff`：我在 worktree 试提交时报 22 error，其中**只有 2 个是我的**（已修，见 §9.1），其余是既存/他人（13 条 arch-drift 因 worktree 的 `arch.json` 基线过期、`third_party/tree-sitter-vb-dotnet/src/parser.c:9470 ts_lex` cyclomatic 1112、`cmd/metricgate/registry_parity_test.go:184` cognitive 71、`internal/rules/chain.go:49` 与 `internal/cockpit/dashboard.go:100` cyclomatic 17）。**注意本 goal 的验证不依赖提交**：§6/§7 的每一条都是在主树工作区的真实文件上跑出来的。

### 9.1 我自己的两个闸门 error（已修，非绕过）

| 位置 | 原值 | 修法 |
|---|---|---|
| `internal/store/measures_lang_test.go:15` | `TestRunMeasuresByLangKeepsLanguagesApart` cyclomatic 16 > error 15 | 抽辅助 `worstMeasure(ms []RunMeasure, metric string) (int, bool)`（:72），循环体 12 行 → 4 行；现 14，仍在 warning 区但不再 error |
| `shots-iter2/verify_iter2.mjs:150` | `checkGate` function-length 106 > error 60 | 拆 `readGateBanner:154` / `checkPerLanguage:180` / `checkGate:207` |

**拆函数时留下的坑（本轮新踩，值得记）**：拆完 `checkGate` 后我漏删了原来那行 `console.log("banner:", banner…)`，而 `banner` 已是 `readGateBanner` 的局部变量 —— `node --check` 只查语法，报不了未定义变量，于是脚本语法「OK」却在运行时必然 `ReferenceError`。**语法检查通过 ≠ 脚本能跑**；归档可执行脚本必须实跑一次。本轮已在主树代码上重跑（§7.2 的 16 项 PASS 就是那次实跑，删掉了残留行并把 `REPO` 硬编码改成第三个参数）。曾在收口前试 commit，被 pre-commit（`metricgate check -diff`）以 22 error 拦下 —— 其中**只有 2 个是我的**（`measures_lang_test.go` 的 `TestRunMeasuresByLangKeepsLanguagesApart` cyclomatic 16 > 15、`shots-iter2/verify_iter2.mjs` 的 `checkGate` function-length 106 > 60），两个都已在收口前修掉（见 §9 表）；其余 20 个是既存/他人的：13 条 arch-drift（worktree 的 `arch.json` 基线比主树旧，导致 `internal/cockpit -> internal/{metrics,model,parser,rules,webspec}` 等新边全部误报）、vendored 的 `third_party/tree-sitter-vb-dotnet/src/parser.c:9470 ts_lex`（cyclomatic 1112）、`cmd/metricgate/registry_parity_test.go:184`（cognitive 71，iter-14 的测试）、`internal/rules/chain.go:49 nestedInChain`（cyclomatic 17）、`internal/cockpit/dashboard.go:100 NewDashboard`（cyclomatic 17）。**处置建议**：主树既然已吸收全部改动，worktree 可以 `git worktree remove --force` 丢弃；丢弃前把 `verify_iter2.mjs` 与 3 张截图留在主树 `docs/iterations/cockpit-ui/shots-iter2/`（已确认在位）。此动作会删目录，需用户放行。
2. **本 goal 自己的 mtime 清单（合并时要用）**：`internal/store/measures_lang.go`、`internal/store/measures_lang_test.go`（新增）；`internal/cockpit/gate_api.go`（`RunMeasuresByLang` + `worst` 按语言建）、`internal/cockpit/gate_api_test.go`、`internal/cockpit/source_api.go`（`resolveSourcePath` + 空文件早退）、`internal/cockpit/source_api_test.go`（新增 4 用例）；`internal/cockpit/web/app.js`（conditionId/gateLocateTarget/ACTIONS["gate.locate"]、空态、facetRow aria-current、panel-foot 文案）、`internal/cockpit/web/render_all.mjs`（新增断言）、`internal/cockpit/web/fixtures.json`、`style.css`；`docs/iterations/cockpit-ui/iter-07-iteration2-close.md`、`docs/COCKPIT_产品设计文档.md`（D2 勾选 + 排期表）。
3. **`/tmp/sync-iter2.sh` 的护栏**：清单**减去**保护表 `/tmp/iter2-mine.txt`（现 10 项）；冲突判定 = 主树该文件 mtime 相比上次同步发生变化（不是「两边不同」，那是我自有文件的正常状态）。早期版本在 `conflict=1` 时仍推进基线，导致下一次跑报「no upstream edits」的**假绿**，已修。
4. **`knownLanguages` 9 vs 13**（§5）。
5. **T2.5–T2.10**（§5）。
