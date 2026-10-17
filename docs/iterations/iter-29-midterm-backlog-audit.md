# 迭代 29 · 中期记录：三件待办，与一份「调研过但没做」的实测清单

日期：2026-10-17

性质：**中期记录，不是迭代日志**。它记的是「现在停在哪里、下一步该做什么」，
所以没有 commit 链、没有验收表，只有判断与证据。

上游：`iter-28`（深色 cockpit + store 保留策略落地）之后的清点。

## §0 为什么要写这份

`iter-27` 解决的是「链路断了」——合并、封板、triage 三段走不通，已修完并留下判据。
`iter-28` 解决的是「做完了没到 main」——两批代码躺在本地与分支上，已落地。

剩下的问题换了一种形状：**没有一条断了，但没有一条在往前走**。

具体说：`docs/research/product-evolution-directions-2026-10.md` §7 排了一张表，
`docs/backlog-v1x.md` 留了 10 条 `⬜`，`docs/COCKPIT_产品设计文档.md` §九 排了
`T4.1`–`T4.7`。这三份文档都在描述未来。**本轮把它们的每一条逐条实测了一遍**，
结论是：其中一部分已经做了却没被划掉，一部分写着「已发货」其实不存在，
剩下一部分是真正待做的。

所以这份记录的用处是：**把三份文档从「描述未来」校正成「描述现在」**。

---

## §1 三件待办（234）

这三件事的共同前提：**CI 目前全停**。7 个 workflow 全是 `disabled_manually`
（`iter-27` 之后你主动停的，理由是本地开发 + 每天推一次代码）。所以：

- 凡是要 CI 才能生效的改动，现在做了也**观察不到效果**
- 但凡是不依赖 CI 的，现在就能做

### §1.1 P2 · triage 加 push 触发

**现状**：`.github/workflows/triage.yml` 的触发只有两处 —— `schedule`
（`cron: '0 3 * * *'`）与 `workflow_dispatch`。所以一个 error 级别的 findings
要等到**次日凌晨 3 点**才会变成 issue。

**要做什么**：加 `push: branches: [main]`，让合并进 main 的代码立刻被 triage。

**为什么排在 P2 而不是更高**：它的价值完全取决于 CI 是否在跑。CI 停着的时候
加触发等于加一段不会执行的代码。**等 CI 恢复再做的成本更低**，而且加触发会
让 `iter-27` §7.2 记的「自举真的会创建 label」那套证明重跑一遍。

**判据**：`gh run list --workflow=triage.yml` 里出现 `event=push` 的 run，
且该 run 建出的 issue 带 `metricgate` / `agent` label。

### §1.2 P3 · `test-no-assert` 的 8 条误报 —— ✅ 已修（迭代 29 之后，见 §5）

**现状**：`internal/rules/go_test_no_assert.go:75` 的 `isAssertionCallee` 白名单只认
`assert` / `require` / `must` 前缀、`Error` / `Fatal` / `Fail` / `panic`。
自定义 helper（`wantVis` / `wantDef`）不在白名单里，于是**有断言的测试被判成没有断言**。

8 条误报位置（实测）：

```
internal/cockpit/web_smoke_test.go:34
internal/facts/lang_test.go:183 / 213 / 235 / 284 / 343 / 363
internal/metrics/grammar_probe_test.go:83
```

**为什么值得修**：这 8 条是当前 422 条 warning 里唯一一类**确定性错误**。
其余 warning（fan-out 105 / cyclomatic 76 / npath 67 / clone 37 …）是真实的
复杂度信号，可以接受；这 8 条是规则本身判错了。**一条会撒谎的规则会让整张
warning 列表失去可信度**——既然有 8 条是错的，第 9 条凭什么被当真。

**难点**：白名单不能简单地「凡是被调用的函数都算断言」，那会让规则变成恒真。
可行的判据是**按 helper 内部是否调用 testing.T 的方法**来判定，这需要跨函数
分析；或者引入显式配置（`assertion_helpers: [wantVis, wantDef]`）。后者简单
但把判断责任推给使用者。

**这条不依赖 CI**，现在就能做。

### §1.3 P4 · 分支保护

**现状**：`GET /repos/OliverYang/metricGate/branches/main/protection` → **404**
（确定未配置，不是权限问题）；`branches/main.protected = false`。

`iter-27` §5.1 原文写的是 401，本轮更正为 404。

**为什么排最后**：分支保护守的是「不许绕过 check 直接推 main」。**CI 停着的
时候它守的是空集**——required checks 不存在，任何推送都满足条件。得先有 CI，
它才有意义。

**配置时的注意**：`iter-27` §5.2 建议的 required checks 是 `gate` 与 `review`。
这两个名字来自 workflow 产出的 check run 名，不是 workflow 名。配错名字会让
分支永久锁死（永远等一个不会出现的 check）。

---

## §2 调研过但没做的：逐条实测

这一节把三份文档的每一条都跑了一遍。分四类：**写着已发货但其实没有**、
**写着待做也确实没做**、**写着待做但其实已经做了**、**描述本身有误**。

### §2.1 写着「已发货」，实际不存在 —— ✅ 已补齐（见 §6）

| 声称 | 实测 | 判定 |
|---|---|---|
| `docs/workflow.md:108`「**已发货**：`hooks/post-edit.sh` + 一个不启用的示例 `.claude/settings.json.example`」 | `hooks/post-edit.sh`（4.5K）与 `hooks/post-edit_test.sh`（5.0K）**在** main 上，`bash hooks/post-edit_test.sh` → **12 passed**。但 `ls .claude` → **目录不存在**；`git ls-tree -r feat/issue-1-ci-smoke-test` 也没有 | 缺的只是 **harness 接线样例**。hook 本身是好的 |
| `docs/iterations/iter-26-post-edit-hook.md` | 只在分支上，main 没有 → **已取回 main** | 代码与文档重新同树（`iter-28` §6 已记） |

**这条的意义**：`hooks/post-edit.sh` 是「纯 shell 入口，不绑定任何 harness」，
这个设计是对的——它不需要 `.claude/` 就能用。但**文档承诺了一个接线样例**，
没有它，用户读完文档知道「有这么个 hook」，不知道「怎么让它在我这儿跑起来」。
`iter-20` 把它记成「S2 hooks/post-edit.sh + settings.json 样例发货」，其中
**前半已发货，后半没发**。

### §2.2 `iter-19` 剩下的两项

| 项 | 实测 | 判定 |
|---|---|---|
| pre-commit 改 scoped check 根 | `hooks/pre-commit:50` 仍是 `exec "$bin" check . -diff $archflags` —— **全树**，未改 | **未做**。调研文档 :86 记了 scoped 的额外开销中位数：docs +1.65 / internal +1.95 / cmd +2.05 秒 |
| `toolVersion` 的 ldflags 注入接发布管道 | `.github/workflows/seal.yml:157` 有 `-ldflags "-X main.toolVersion=${version#v}"`；:70 注释说明 var 而非 const 才能被覆盖 | **已完成**，可以从遗留清单划掉 |

### §2.3 `docs/backlog-v1x.md` 的 10 条 `⬜`：全部确认未做

逐条实测，**没有一条是「其实做了」**：

| # | 条目 | 实测证据 |
|---|---|---|
| 1 | inline directive 加 owner/reason/expiry + 过期告警 | `internal/engine/suppress.go:7` 语法仍是 `metricgate: ignore <rule[,rule2...]> [reason...]`；:249 只有注释提到「the ledger's owner/reason/expiry discipline applied to inline」，**无字段实现** |
| 2 | TS/TSX 箭头函数计入函数级度量 —— **真缺陷** | 见下方 §2.4，已复现 |
| 3 | `metricgate policy show` 动词 | `cmd/metricgate/main.go` 的命令表里没有 `policy` |
| 4 | MCP 侧策略工具 | `cmd/metricgate-mcp/tools.go` 只有 `get_constraints`(:45) 与 `get_review_bundle`(:96)，无独立 policy 读取 |
| 5 | 保存 `.bak` 无轮转上限 | **描述有误**：`internal/cockpit/policy_write.go:195` 是 `os.WriteFile(target+".bak", old, 0o600)`，**每次覆盖同一个 `.bak`**，不追加。所以不存在文件堆积；真实缺陷是「只留一代历史」 |
| 6 | Cockpit owner 视图 | `grep -rn 'owners\.' internal/cockpit/*.go` 为空 |
| 7 | `internal/owners` 不认自定义 CODEOWNERS location | `internal/owners/owners.go:31` `var DefaultPaths = []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}` 硬编码三个，无配置项 |
| 8 | Cockpit trace 过滤不作用于 run 历史视图 | `VIEWS.runs`（`app.js:2001`）里 `grep trace` 无命中 |
| 9 | arch 包名规则手写分支 | `internal/arch/extract.go:33` 与 `:72` 两个 `switch lang` |
| 10 | X4 LLM 元数据自动采集 | `grep -rn 'trace_attrs' cmd/metricgate/*.go` 为空 |

### §2.4 唯一一个「用户能直接感觉到」的真缺陷：TS 箭头函数

这是本轮调研里**唯一一条会让度量数字直接错的**：

```ts
// a.ts —— 箭头函数
export const handler = (x: number) => { if (x>1) {return 1;} for (...) {...} return 0; }
```
```
a.ts  LINES 6  FUNCS 0  MAX-CC 0  MAX-COG 0     ← 0 个函数
```

```ts
// b.ts —— 同构命名函数
export function named(x: number) { if (x>1) {return 1;} return 0; }
```
```
b.ts  LINES 5  FUNCS 1  MAX-CC 2  MAX-COG 1
```

**根因**：`internal/metrics/js_adapter.go:44` 把 `arrow_function` 归入
`Lambda{Body: childrenIR(...)}`，注释写着「An inner function is scored into the
enclosing unit, one level deeper, like Go func_literal / Java lambda_expression」。
也就是说箭头函数被算进**外层**单元的深度，**而不是成为独立函数单元**。
`internal/metrics/metrics.go:580` 的 `isCallTarget` 也把 `arrow_function` 列为
「不是 call target」。

**为什么这个错法很贵**：Go 的 `func_literal` 与 Java 的 `lambda_expression` 都是
**表达式内部的匿名函数**，归入外层是对的。但 TS 里
`export const handler = (x) => {...}` 是**模块级具名导出的函数**，它只是在
语法上用箭头写。把两者当同一件事，等于说「TS 没有顶层函数」。React / Express /
Node 的代码几乎全部用这种写法，所以**这个适配器对现代 TS 代码基本失效**。

### §2.5 写着待做，其实已经做了

| 项 | 实测 | 判定 |
|---|---|---|
| D3 blast-radius 进闸门 | `cmd/metricgate/check_cmd.go:280` 已调 `gatecheck.BlastRadiusCheck(defs, uses, opts)`；flag 是 `-impact-scope`（:60 帮助文本）；:153-154 强制它必须配 `-diff` | **已发货**，调研表该行过时。**剩余缺口**是 tier 过滤：`grep -n tier internal/gatecheck/gatecheck.go` 为空，与 `iter-11` 遗留一致 |
| D5 bundle 降级交付 | `internal/bundle/bundle.go:352` `fitEnvelope` 会 `dropOneOptional(&b.Files)` 逐步裁剪，裁到无可裁才 `BudgetExhausted = true` | **降级交付已实现**。未做的是「预算按仓库规模自适应」 |
| D2 MCP `-diff_only` 棘轮对齐 | 调研文档已划掉 | 已完成（`iter-22`） |
| S3 文档漂移 | 调研文档已划掉 | 已改完 |
| S1 `-diff` 批量化 | 调研文档已划掉（282→8 进程） | 已落地 |

### §2.6 `COCKPIT_产品设计文档.md` §九 迭代 4（T4.1–T4.7）

| 项 | 实测 | 判定 |
|---|---|---|
| **T4.1** 统一时间格式 | 三处口径不一致：`app.js:144` `formatTime` 用 `toLocaleString("zh-CN", { hour12: false })`；`:1201` gateMeta 用裸 `toLocaleString()`（无 locale、无 hour12）；`:3171` 用 `toISOString()` | **未做** |
| **T4.2** 运行卡补触发命令/路径 | ~~无触发命令~~ → **已做**（见 §7.2）：`runs` 表加可空 `invocation` 列，前端补「触发命令」「路径」两列 | ✅ **已做** |
| **T4.3** error 调用 tooltip；度量实验结论摘要 | tooltip 已有一定覆盖；`rules_zh.go` 已捞回进 `main`（`f0b0a6e`，见 §7.1），规则表「说明」列改读中文标题并新增「默认」列；**度量实验结论摘要仍未做** | **部分** |
| **T4.4** 度量面板三义语义区分 | `measureCard` 输出 `measure-label` + `measure-grade grade-X` + `measure-value` + `measure-bands`（`warn W · err E`）—— 评级/触发数/上限**三义混在同一视觉层** | **未做** |
| **T4.5** 项目级 overview 两块面板不随 scope（后端遗留 L8） | **仍未修**。`internal/cockpit/dashboard.go:70-90` 的 `ProjectSummary` 无 `accepted` / `metricTrend` / `summary` 字段；`app.js:2074` 与 `:2388` 就是那两个顶层读。`web/smoke.mjs:499-517` 是 **L8 tripwire**：断言 project 里**不得**出现这三个字段，一旦出现就 fail 并提示「L8 is fixed, so the top-level reads in app.js must become scope-aware」 | **未做**，但**有守门人** |
| **T4.6** 空态与在飞态统一 | 逐视图统计 `loading`/`loadError`/`loadPending`（行号为 VIEWS 定义行）：projects 1/0/1、issues 2/1/1、**calls 0/0/0**、runs 1/1/1、**overview 0/0/0**、**policy 0/0/0**、activity 0/1/0、**gate 0/0/0**、rules 3/0/0 | **未做**，9 个视图里 4 个完全没有 |
| **T4.7** converge rail 两个 chip 同时高亮 | `app.js:1420-1421` `const baseline = Number(s.limit) === WIDEN_LIMIT && s.status === "" && ...`；:1414-1418 注释说明旧版硬编码 `false` 且只重置 limit+status | **已修**，不是遗留 |

---

## §3 排序建议与理由

把上面所有条目按「现在做值不值」排：

| 优先级 | 做什么 | 依赖 CI？ | 理由 |
|---|---|---|---|
| **现在** | TS 箭头函数函数级度量（§2.4） | 否 | 唯一一条让度量数字直接错的缺陷。React/Node 代码几乎全用箭头函数，等于适配器对现代 TS 基本失效 |
| **现在** | `test-no-assert` 8 条误报（§1.2） | 否 | 唯一一类确定性错误的 warning。规则撒谎会让整张列表失去可信度 |
| 现在 | `.claude/settings.json.example`（§2.1） | 否 | 文档已承诺，缺一个文件。成本最低的一条 |
| ~~现在~~ ✅ | `rules_zh.go` 从分支捞回来（`iter-28` §6.1） | 否 | 已做，见 §7.1 |
| ~~现在~~ ✅ | T4.2 运行表补触发命令（§2.6） | 否 | 已做，见 §7.2 |
| 稍后 | pre-commit scoped 根（§2.2） | 否 | 纯性能优化（省 1.6–2 秒），不是正确性 |
| **等 CI** | P2 triage push 触发（§1.1） | **是** | CI 停着时加的是不会执行的代码 |
| **等 CI** | P4 分支保护（§1.3） | **是** | 没有 required checks 时它守的是空集 |
| 等 CI | T4.5 L8（§2.6） | 否 | 有 tripwire 守着，不会悄悄变坏；但它要改后端 `ProjectSummary` + 前端两处读，是中等工作量 |
| 排期 | T4.1 / T4.4 / T4.6（§2.6） | 否 | 都是 cockpit 一致性收尾，适合合并成一轮「视觉口径统一」 |
| 排期 | backlog 10 条（§2.3） | 部分 | 见 `docs/backlog-v1x.md` 原表 |

### §3.1 关于 CI 停用的一个判断

7 个 workflow 全停着，这是你为了本地开发速度做的选择，**不是问题**。但有一件事
需要记住：**停用期间 `iter-27` 修好的三段链路都在退化**。

- merge workflow 不在 main 上时它不触发（已修，但现在停着）
- seal 靠 merge 显式 dispatch（已修，但现在停着）
- triage 靠 label 自举（已修，但现在停着）

三段都「修好了但没在跑」。`iter-27` §8.3 的判据是「跑绿了不等于这段链路通了」，
现在要加一句：**「修好了但停着」等于「不知道还通不通」**。恢复 CI 时不要假设
它还是好的——按 `iter-27` §7 的顺序重跑一遍证明（合并 → 封板 → 自举删 label 再跑）。

## §4 一句话总结

`iter-27` 修的是「断了」，`iter-28` 修的是「没到 main」，这一轮清点出来的
是「**没人划掉**」——三份文档里 10 条 backlog、7 条 T4、若干条 iter-19 遗留，
混着「已做没划掉」「声称已做其实没做」「描述本身有误」三种。

本轮实测把三类分开了，并且找到了其中唯一一条会让产品数字直接错的缺陷
（TS 箭头函数）。

---

## §5 P3 的实际修法（修完之后补记）

原计划列了两个候选判据（跨函数分析 helper 内部是否调用 `testing.T` 的方法；
或引入显式配置 `assertion_helpers: [...]`）。**两个都没用**，因为都存在更简单
且更准的结构事实可用：

> 一个 helper 能失败这个测试，当且仅当它拿到了失败这个测试的手段 ——
> 也就是 `*testing.T`。所以判据是**调用是否把测试自己的 `*testing.T` 值
> 当参数传出去**，而不是 helper 叫什么、内部写了什么。

`goBlockHasAssertion` 因此接受第二个参数（从签名里解析出的 T 参数名），
两档判据：① 调用名在断言词表里（原有逻辑）；② 调用的实参里有那个裸标识符。

**为什么这个判据比两个候选都好**：
- 不需要跨函数分析，所以不引入「helper 在另一个文件」的失效
- 不需要用户配置，所以不会有「配置漂移」
- `*testing.B` 与改名参数（`tb`/`tx`）天然覆盖，因为匹配的是**类型解析出的名字**
  而不是字面量 `t`

**精度下界（写进测试，不是写进注释）**：`tt := t; check(tt, true)` 仍被 flag。
追别名要做数据流，这条规则从来不做，所以这是**已知且被记录的下界**，
而不是待修缺陷 —— `TestGoTestNoAssertNamesAnyTParam` 把它钉住了。

**实测（同一份真实 Go 语料，5 个开源包 / 110 个 `_test.go`）**：

| | 修复前 | 修复后 |
|---|---|---|
| `test-no-assert` 命中 | 55 | 19 |
| 其中 Benchmark | 41 | 14 |
| 其中 Test | 14 | 5 |

剩下的 19 条**逐条抽查全部为真**：`TestTerminal` 只有 `t.Log`（注释写着
"test for non-panic"）、`TestSetTools` 只调两次 `bu.SetTools("")`
（注释 "Test that multiple calls work"）。**误报清零，真报未减。**

本仓库自身：8 条 → **0 条**，全树 422 → 428 warnings（R1 新暴露的 JS 函数
贡献了增量），gate 仍 PASSED。

**一个必须记录的副作用**：`docs/evaluations/evaluation-commit-replay-results.md`
用旧版规则在 dev-cockpit 上得到「test-no-assert 215/289 commit 触发」，
并据此在 `docs/claims.md` #19 上调了证据。**那个数字现在偏高且无法在本机重算**
（语料仓库不在本地）。新判据会滤掉其中"传 `*testing.T` 给 helper"的部分。
`claims.md` #19 的该项证据需要重新标定 —— 已记入 `iter-29` 之后的待办。

---

## §6 R3 的实际修法（`.claude/settings.json.example`）

`.claude` 被**两层**忽略：仓库 `.gitignore` 从没写过它，但开发者的全局
`core.excludesFile`（本机是 `/Users/mac/.gitignore:9`）有一条裸 `.claude`。
Git 对一个被忽略的**目录**直接剪枝，位于其下的取反规则永远不会被读到 —— 所以
只写 `!/.claude/settings.json.example` 时 `git add` 依然报 "paths are ignored"。

仓库 `.gitignore` 因此是三行，顺序即语义：

```
!/.claude/                        # 先把目录本身放回来
/.claude/*                        # 忽略内容：settings.json 带本机绝对路径
!/.claude/settings.json.example   # 唯独样例进 git
```

判据不是「`git check-ignore` 返回什么」这种容易读反的东西，而是**走完文档里的
接线流程再看 hook 是否真的跑**：`cp example → settings.json` → 替换 `<ABS_PATH>`
→ 喂一条 `{"tool_input":{"file_path":"internal/rules/go_test_no_assert.go"}}` 的
payload 给 `hooks/post-edit.sh` → 输出 scope 行 + `exit 0`。这条链路跑通才叫
「样例可用」。

顺带修好一个断链：`docs/research/v02-requirement-schedule.md:86` 写的是
`../../iterations/iter-18-…`（多爬一级）。`docs/` 全量扫描后断链数为 0。

**样例保持"不启用"的两处设计**：文件里 `"//"` 键存说明（这样它仍是合法 JSON，
不需要 `jsonc` 解析器）；`iter-26` §T26.3 的原话是「本仓 clone 后不应自动生效」，
所以只有 `settings.json`（真跑的那份）刻意留在忽略区。

**验证**：`bash hooks/post-edit_test.sh` → 12 passed / 0 failed（脚本未改，
证明新增样例没影响入口）；`git ls-files .claude/` → 只有 `settings.json.example`。

**新增 TODO（本轮发现，未做）**：docs/ 内部相对链接目前没有守卫。这轮的断链是
人肉发现的。加一个 `scripts/docs-links.sh`（遍历 `docs/**/*.md`，解析
`]` 后的相对路径，`os.path.exists` 判据，非零退出）成本很低，应挂在 pre-commit
而不是 CI（CI 现在是停的）。已记入排期文档。

## §7 R4 / R5 的实际修法（做完之后补记）

### §7.1 R4 · 规则目录中文化（`f0b0a6e`）

从 `feat/issue-1-ci-smoke-test` 取回 `rules_zh.go`（59 行，7 个 rule id 的中文标题 +
「什么算违规」一句话）、`rules_catalogue_test.go`（272 行）、`rules_api.go`（195→395
行）与配套的 `rules_api_test.go`。**必须一起取回**：`rules_catalogue_test.go:176` 用到
`getRaw`，而 `getRaw` 只存在于分支版的 `rules_api_test.go:227`。

前端**没有整体搬分支版 `app.js`**（分支 3937 行 vs main 3522 行），因为分支版依赖 main
上不存在的 `tablePager` / `pageRuleGroups` / `rulesLangMatches` / `RULES_LANG_ALL` /
`state.rulesLang`。做法是在 main 版本上做最小嫁接：`ruleCopy(rule)`（无中文时回退英文
title，**不编造** desc）、`ruleCopyCell`、「说明」列表头从 `title` 改 `titleZh`、新增
「默认」列、`rulesOrderBy` 加两个派生 cell。

实测：`/api/rules?scope=all` → 13 门语言 / 61 条规则行 / **61 行全部带 titleZh**，
`ids WITHOUT Chinese: []`。7 个 distinct id 恰好等于中文表的条数 —— 度量类规则
（function-length 等）**不是 rule pack 成员**，不在这 61 行里，所以不存在「漏翻译」。

**两个必须记下来的坑**：

1. `rulesHead` 的 `title` 属性**不能放在 `<th>` 上**。`render_all.mjs` 的表头正则锚定在
   `<th aria-sort="…"` 后紧跟 `>`，任何插在 `<th` 与 `aria-sort` 之间的属性会让那一列
   **从所有排序断言里静默消失**，而不是失败。修法：hint 放内层 `<button>`。
   反向验证手法：临时给 `<th` 后插 `class="x"`（只对 `titleZh` 生效，避免全局 `<th class="x">`
   把 `states.key` 弄成 undefined 而崩溃）→ `node render_all.mjs` 输出
   `FAIL rules: titleZh not matched by the header regex — a <th> attribute moved ahead of
   aria-sort, and every sort check below just went blind`。
2. `languageGroups` 由 `/api/rules` 提供但**没有任何消费者** —— 前端与 MCP 都不读它
   （`grep -rn languageGroups internal/mcp/` 为空）。本轮只加了断言保证它存在，没有删。
   要么前端按语言分组渲染，要么删字段，不能长期挂着一个没人读的字段。

### §7.2 R5 · 运行表补「触发命令」与「路径」（本轮）

**为什么不能走 trace_attrs**：`internal/store/trace_store.go` 确实能按 trace 存任意
kv，但实测 `SELECT COUNT(*) FROM runs WHERE trace_id IS NOT NULL AND trace_id <> ''` =
**0** —— CLI 的 check 路径根本不写 trace_id，只有 MCP 与测试写。往一个从来不被填的表里
加键，等于给一个空房间装锁。所以走「加列」。

**后端**：`runs` 表加可空 `invocation TEXT`，迁移 `migrateRunsInvocation` 走既有
`hasColumn` 判存模式（老库保持可用）。`RunMeta.Invocation` → `saveRun` 的 INSERT；
`RunRecord.Invocation` → `runColumns` 的 `COALESCE(invocation, '')`。
**列顺序是硬约束**：`runColumns` 的顺序必须与 `scanRuns`（`read.go:369`）的 Scan 参数
顺序一致，`archived` 保持在最后。一个 scan 失败会带走整张运行历史，所以
`TestRunWithoutInvocationReadsBackEmpty` 专门跑 `LatestRuns(10)` 这条同 scan 路径。

**CLI 侧脱敏**（`cmd/metricgate/invocation.go`，~150 行）：这一列会把命令行原样存进
一个**局域网可读**的 HTTP 面板，所以「记录它」的前提是「不会泄露」。

- `secretFlags` 名单（`token`/`password`/`secret`/`api-key`/`authorization`/`private-key`…）
  命中时值写成 `<redacted>` —— **固定串而非等长掩码**，等长掩码会泄露密钥长度。
- `-name value` 与 `-name=value` 两种形态都处理；裸 `-name` 只在**下一个 token 是 flag
  时**才不吃值（Go flag 包在第一个非 flag 参数后停止解析，所以位置参数 `src/token`
  后面的东西不能被吞）。
- 没有 flag 名可对的情况（bare positional、`FOO=bar` 环境变量赋值）用凭据前缀兜：
  `ghp_` `github_pat_` `gho_` `ghu_` `ghs_` `ghr_` `xoxb-` `xoxp-` `xoxa-` `AKIA` `ASIA`
  `AIza` `sk-`。
- 超过 512 rune 截断并留标记，而不是塞进单元格把行撑爆。
- `quoteArg` 只在含 shell 元字符时加单引号，让这一行**可以原样粘回终端**。
- `TestSecretFlagNamesAreSpelledAsTheFlagsAre` 是反向断言：`secretFlags["config"]`、
  `["base"]`、`["format"]` **必须为 false**。把路径 redact 掉会毁掉这列存在的意义。

**前端**：`runRow` 增两列，合并成一个导出的 `provenanceCells(run)`（两列回答同一个问题
——「这些数字怎么来的」——而行的职责是排版；分开写会让 `runRow` 的 fan-out 越到 11）。
单元格显示**去掉 argv[0] 的部分**，因为行间真正变化的是 flag，二进制路径每行都一样；
完整行进 tooltip。空值一律显示「未记录」而不是空白 —— 空白会被读成「这次运行没带
任何 flag」，而 store 并没有这个事实。路径列显示末两段（一行 36px，长路径不能换行）。

**这一轮真正的收获是列宽那个 bug，不是那两列**：

首版给 `col-cmd` **不设宽度**，指望 `table-layout: fixed` 用它吸收剩余空间；同时没写
`min-width`。1600px 截图上「触发命令」列只剩几个像素。根因：11 列声明宽度之和
（95rem）超过面板可用宽度时，浏览器**按比例压缩每一列**而不是让 wrapper 出横向滚动条。
`col-cmd` 本来就想要最大吸收空间，于是被压得最狠 —— 这列存在的理由就是它被压掉的那部分。

补了 `min-width` 之后又踩第二个：`col-counts` 是 `white-space: nowrap` 的三段计数，
没有 `overflow: hidden`，于是它**画穿自己的边框盖到下一列上**，看起来像「触发命令」列
凭空消失。源码 grep 完全看不出问题，只有截图能看出来。

最终：`min-width: 96rem`（≥ 95rem 之和）、`col-counts 21rem` + `overflow: hidden`、
`col-cmd 16rem`、`col-path 11rem`。1920px 下 11 列全展开；1100px 下横向滚动而不是压扁。

**新增两条守卫**（`internal/cockpit/web_contract_test.go`），因为这两类失败都是
「浏览器里看不出来、源码里 grep 不出来」：

- `TestRunTableWidthsAddUp` —— 声明宽度之和 > 面板可用宽度时，表必须有一个不小于该和的
  `min-width`。`panelContentRem = (1100 − 15×16 − 2×1.125×16) / 16` 是 1100px 断点下
  面板的真实可用宽度（减去导航栏与 panel padding）。
- `TestRunTableColumnsAreAllDeclared` —— 表头发出的每个 `col-*` 必须在样式表里有一条
  带 `width:` 的规则。只按类名选不够：`.runs-table .col-path { overflow: hidden; }`
  这种规则什么都没定。

两条都做了反向验证：把 `min-width` 改回 90rem → 第一条报
`runs-table: min-width is 90rem but its 11 columns need 95rem`；删掉 `col-path` 的
`width` → 第二条报 `the run table emits col-path but style.css never declares a width
for it`。**「加了测试」不等于「测试会失败」，改回坏值跑一遍才算。**

**验证**：`go test ./...` 48 个包全绿；`go vet ./...` 干净；`node --check` +
`render_all` 9 views × 3 scopes × 2 modes ok；`gate: PASSED`、0 errors、431 warnings；
`git stash` 前后对比 warning 集合 → **净新增 0**（R5 一度给 `runRow` 和 `storeRun` 各加
一条 fan-out 11，用抽组件/抽 `runMeta(cfg)` 解决，而不是抬阈值）。
真机：`/tmp/mg-inv check . -arch-base arch.json` → `run #1593`，
`SELECT id, COALESCE(invocation,'<NULL>') FROM runs ORDER BY id DESC LIMIT 3` →
`1593|/tmp/mg-inv check . -arch-base arch.json` / `1592|<NULL>` / `1577|<NULL>`
（旧行为 NULL，走「未记录」分支）。
