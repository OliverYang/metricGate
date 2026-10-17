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

### §2.1 写着「已发货」，实际不存在

| 声称 | 实测 | 判定 |
|---|---|---|
| `docs/workflow.md:108`「**已发货**：`hooks/post-edit.sh` + 一个不启用的示例 `.claude/settings.json.example`」 | `hooks/post-edit.sh`（4.5K）与 `hooks/post-edit_test.sh`（5.0K）**在** main 上，`bash hooks/post-edit_test.sh` → **12 passed**。但 `ls .claude` → **目录不存在**；`git ls-tree -r feat/issue-1-ci-smoke-test` 也没有 | 缺的只是 **harness 接线样例**。hook 本身是好的 |
| `docs/iterations/iter-26-post-edit-hook.md` | 只在分支上，main 没有 | 代码与日志分离（`iter-28` §6 已记） |

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
| **T4.2** 运行卡补触发命令/路径 | `runRow`（`app.js:1020`）列是 结果/运行/结论/提交/开始时间/规模/计数/操作，**无触发命令** | **未做**（P4 刚改成表格，现在补成本最低） |
| **T4.3** error 调用 tooltip；度量实验结论摘要 | `grep -c 'title=' app.js` = 13 处（tooltip 已有一定覆盖）；但 `internal/cockpit/rules_zh.go`（规则中文名/说明）**只在分支上，main 没有** | **部分** |
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
| 现在 | `rules_zh.go` 从分支捞回来（`iter-28` §6.1） | 否 | T4.3 的前置，且是现成的 59 行 |
| 现在 | T4.2 运行表补触发命令（§2.6） | 否 | P4 刚把 runs 改成表格，现在加列成本最低，越晚越贵 |
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
