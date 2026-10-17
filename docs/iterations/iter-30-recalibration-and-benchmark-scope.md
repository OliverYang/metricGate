# 迭代 30 · 把两个 P0 债还完，并记下「215/289」这个词被读错了多久

日期：2026-10-18　分支 `feat/iter30-func-unit-census` → main `739aa11`（三个 commit，已推送）
上游：[iter-29 中期记录](iter-29-midterm-backlog-audit.md) §3 排序建议、[iter-29 整改实测](../evaluations/evaluation-iter29-remediation.md) §7「一处必须记录的副作用」与 §9 下一轮 TODO
本轮产出：[iter-30 重标定实测](../evaluations/evaluation-iter30-tna-recalibration.md)（新增）、[evaluation-commit-replay-results.md](../evaluations/evaluation-commit-replay-results.md)（就地修正）、`docs/claims.md` #19（改判）

## §0 本轮做了什么

iter-29 §9.1 列了五个 P0，本轮做前两个，都是**还债**而不是新增能力：

| 项 | iter-29 留下的状态 | 本轮结果 |
|---|---|---|
| 重跑 commit replay 标定 `test-no-assert` | 「语料仓库不在本地、无法重算 ⇒ 215 应读作上界」 | **那句是错的**。语料可重克隆、归档 JSON 在 `.data/`。289 commit × 双判据全量重放完毕，215/289 **可复现且含义被读错**（§2） |
| `Benchmark*` 是否单开一档 | 剩的 14/19 条都是 benchmark，「判据合理但产品噪音」 | 定口径：`test-no-assert` 只管 `Test*`，**0 条真阳丢失**（§3） |
| C/C++/C# 的等价失明排查（顺带） | R1 修了 JS 系，怀疑别的语言适配器还有同类 kind-set 漏洞 | **别的语言没有同一个洞**，但发现了另一类真缺口并已钉成测试（§4） |

三个 commit：`ed58746` 普查测试 + `docs/metrics.md`、`d2bcf9b` benchmark 判据 + 测试搬家 + 中文表、`739aa11` 重标定文档 + 五处措辞修正。

## §1 判据重放的口径（为什么这次的数字可以信）

- **唯一变量是判据**。同一份 policy、同一份 289 行 commits 清单、同一批 checkout。old 二进制从 `275ee38`（iter-29 排期文档那个 commit，helper 能力判据之前）构建，new 从 main 构建。
- **必须串行**。首轮并行分片在同一工作树上抢 `index.lock`，460/578 个输出是 `{"error":"checkout_failed"}`。改成每个 clone 只服务一个二进制（两侧并发、各自串行），289×2 跑完 **0 失败**。
- **归档可信性是被单独证明的**，不是假定的：用时代正确的一对（`/tmp/mg-era-bin` @ `d5cb01c` + `/tmp/policy-era.yaml` = 157 行版本）在 `396cd3cd8f` 上跑，得 `total 2174 / files 555 / errors 222`，逐条与归档 JSON 相等。
- 这个「时代正确的一对」也是本轮踩的最大的坑：我一开始用 `git show 59e0a24^:policy.yaml`（186 行）当"当时的策略"，重跑数字全部偏高，多出 `unknown-dependency 36622 / wmc 2756 / nom 1810 / empty-block 1132` 等归档里根本没有的条目，一度得出「归档不可复现」的错误结论。**差异只来自 policy 版本**（186 行版本比 157 行多了 2026-10-14 之后加的 `custom_rules` 与 `test_metrics`）。教训：跨版本重放时，policy 与二进制必须同时回到那个 commit。

## §2 215/289 到底说了什么（本轮最重要的负面结论）

| | 归档（原报告） | old = iter-29 前 | new = helper 能力判据 | new + 排除 benchmark |
|---|---|---|---|---|
| findings | **1,783** | **1,783** | 233 | 215 |
| 触发 commit 数 | 215 | 215 | 215 | **215** |
| distinct anchor | 31 | 31 | 2 | **1** |
| 命中文件数 | 6 | 6 | 2 | **1** |
| 单 commit 最大 | 15 | 15 | 2 | 1 |

- **commit 计数纹丝不动，anchor 数掉了两个数量级**。全部 215 个命中来自 `internal/eventbus/eventbus_test.go:40` 的**同一个函数**，它自 commits.txt 第 75 个（`444b31ec97`，2026-07-15）引入后，在其后 215 个连续 commit 里逐个被重复计数（75..289 零漏网）。
- 那个函数的注释原文：`// Far more events than the buffer holds. If Publish blocked on a full subscriber this would deadlock and the test would time out.` ⇒ 作者把「不返回」当失败手段。规则看不到这一点，所以这条残留命中读作**规则精度下界**，不是「LLM 写了 215 个伪绿测试」。
- 原始报告据此在 `docs/claims.md` #19 上调了证据，措辞是「伪绿测试形态在真实 LLM 仓库大规模存在」。**这句现在已被否证**：per-commit 计数不是 per-event 计数。#19 该项证据不再上调。
- 同一条误读也解释了 old 侧最大的那坨：`internal/assistant/memory_test.go` 1,207 条 findings / **21 个 anchor**——21 个测试各调一次不认识的 helper，每个测试在 289 个 commit 里再算一遍。

### §2.1 顺带查出的另一件事：旧报告 §2 的总量表 13 行不可复现

`.data/commit-replay/devcockpit/*.json` 逐条求和 = 234,606 findings。**只有 5 行**（fan-out 85,771 / cyclomatic 38,150 / npath 37,093 / cognitive 22,926 / function-length 22,361）等于归档 findings 实例数。其余 13 行既不等 findings，也不等 commit 数 / distinct file / distinct anchor / 快照超阈计数——试过的口径全部列在评估文档 §5 的对照表里，没有一个能同时解释它们（`test-no-assert` 报 3,492 而归档 1,783；`nesting-depth` 报 44 而归档 1,507；四条规则归档为 0 却有计数——那四条在当时的策略里 `enabled: false` / 只配 java / `error: off`）。

处理方式：**就地标注「不可复现，以原始 JSON 为准」，不改写数字**。改写会掩盖问题，标注会让下一次引用的人自己去看对照表。报告其余结论（0/289 零检出、error 清零 5/5、npath 极值 218,160、§3 那 5 个 commit 的 before/after）都与归档一致，所以受影响的只有 §2 那一行。

**数据持久性的真问题**：`.data/` 被 `.gitignore:8` 忽略且从未被 git 跟踪过（`git log --all -- .data` 为空）。一份被 claim 引用的原始数据只存在于这台机器上，别人无法复核——这是本轮能重算、下一轮未必能的原因。已写进 iter-31 待办（§6）。

## §3 `Benchmark*` 从 `test-no-assert` 里摘出去

判据改动（`internal/rules/go_test_no_assert.go`）：前缀判断去掉 `Benchmark`，签名门只认 `*testing.T`，`testingTParam` 不再匹配 `*testing.B`。

| 语料 | 含 benchmark | 其中 benchmark | 排除后 |
|---|---|---|---|
| dev-cockpit 289 commit | 233 | 18（7.7%） | 215 |
| dev-cockpit tip 单 commit | 19 | 18 | **1** |
| `/tmp/corpus` 5 包 / 110 `_test.go` | 19 | 14（73.7%） | **5** |

- 那 18/14 条全是同一句话：让一个参数是 `b` 的函数「add `t.Error/t.Fatal`」。**不可执行的提示占该类命中的 100%**，而规则自己的注释就写着「false positive 的代价是没人读的 warning」。
- 排除的 5 条逐条抽查全是真报（`isatty_others_test.go:11 TestTerminal`、`server_test.go:828 TestAddToolInputSchemaComposition`、`binutils_test.go:241/248`、`profile_test.go:1907 TestThreadSafety`）⇒ **一条真阳都没丢**。
- 「benchmark 其实什么都没测」是真缺陷，但那是另一条规则（setup 返回值未被引用之类），不该借这条规则的壳。已记 iter-31。

### §3.1 测试搬家：一个规则改过三次判据，就不该住在共享夹具里

`rules_test.go` 原本 380 行，加 benchmark 夹具会到 413 行，而 `file-length` 的 warning 阈值是 400 ⇒ **这条规则自己的测试文件会出现在它检查的报告里**。这不是为了消警告而搬：Go 这一族已经被改过三次（只看名字 → helper 能力 → 排除 benchmark），每次都要自己的夹具，继续往一个共享源字符串里堆，下一个改动无法定位它真正动了什么。

新文件 `internal/rules/go_test_no_assert_test.go`（217 行）收纳四个 Go 夹具 + 一个 `anyContains` helper；`rules_test.go` 回到 216 行。本仓 warning 432 → 430（少的两条正是那个测试文件的 `file-length` 与它引出的 `function-clone`）。

## §4 跨语言普查：JS 那个洞没有别的语言共享（已提交测试，未修的新缺口另记）

iter-29 §9.1 怀疑「kind-set 漏掉某种声明形态」在别的语言适配器里还在。**结论：不是同一个洞。**

- C / C++ / C# / Java / Python / Rust 的 `name = <closure>` 全部 `FUNCS=0`——**这是设计**：那些就是闭包，全站规则是折叠进外层函数（`docs/metrics.md` 明写 Go `func_literal`、Java `lambda_expression` 同规则）。JS 需要特例，因为 `export const f = (x) => …` 在模块顶层**没有外层函数**可折叠。
- 反向验证：把 `accessor_declaration` 塞进 `internal/metrics/kindset.go:71` 的 `csFuncKinds`，普查测试立即失败并打 `FUNCS=2 names=[get set]` + `MI=90.19615793986456`。名字是编的——正是 iter-29 对 `export default` 明确拒绝的「往快照里塞假标识符」。
- **真缺口是「分支不在任何单元里」这一类**：C# property `accessor_declaration`（get/set）、Java `static_initializer` 与实例初始化块、C# top-level statements。若整文件没有别的单元，`AnalyzeFile` 置 `MI=0`，而 `engine.go:466/:570` 只在 `MI>0` 时发快照 ⇒ **一条 maintainability 都不发**，文件隐形。修法要先解决命名（`P.get`？）并改 lowerer（`cs_adapter.go:21` 的 `lowerFunc` 没有 accessor 的 case），按项目规矩走策略评审。已用 `TestAccessorAndInitializerBranchesAreCountedNowhere` 钉住现状。
- 无法本地量化：本仓只有 4 个 `.java`、**0 个 `.cs`**。所以这轮的交付是测试 + 文档，不是代码。
- 4 个探针测试文件（`func_unit_probe_test.go` 等，只 `t.Logf` 不断言）已全部删除，结论提升为 `internal/metrics/func_unit_census_test.go` 的 5 个真断言测试。留一个"探针"在树里，下次它会替我们假装还有事没做完。

## §5 文档债（本轮还的）

`test-no-assert` 判据改了，引用旧数的地方全部就地修正而不是新开一节辩解：

- `docs/claims.md` #19：把「215 应读作上界、待重跑标定」换成实测；证据**不再上调**，并把「大规模存在」改成「持续触发但集中在极少数函数」。
- `docs/evaluations/evaluation-commit-replay-results.md` §2 两行（总量行 + 215 那行）、§6、§8.3。
- `docs/evaluations/evaluation-iter29-remediation.md` §7 标题加「已于 iter-30 结清」，并记录本轮原先的估计方向对、量级错得远（commit 数根本没变）。
- `docs/iterations/iter-29-midterm-backlog-audit.md` §5 副作用段、`docs/README.md` 两行（新增评估文档索引 + 改判 replay 报告那行）。
- `docs/iterations/iter-27-manual-handoff-blockers.md`：§3.1 标 ✅ 并说明**两个候选修法都没采用**（实际走了第三条：既不做跨函数分析，也不放宽到任何本地 helper）；B5 勾掉；§7.6 的第 3 条划掉并补两条新开口。
- `docs/capabilities.md` 的 `test-no-assert` 行原写**只有 Java**，与代码不符：它在 6 个 pack 里（Go/Java/TS/JS/Py/Rust/C#），C/C++ 是**故意不做**（`c_rules.go:192` 注释：Check/CppUTest/gtest 命名各不相同，名字判据会大量误伤）。这轮把语言列、各语言的"哪个函数算测试"判据、benchmark 排除、以及 `tt := t` 这个精度下界一次写全。
- `internal/cockpit/rules_zh.go` 的 `test-no-assert` 中文描述补上「只算 Test 开头的函数，benchmark 按设计不能失败」——前端那格是读者判断 finding 真假的唯一中文来源，缺这句就会有人去给 benchmark 加 `t.Error`。

## §6 下一轮（iter-31）排期

按「不做会让下一个数字错」排序。CI 仍是 7 个 workflow 全 `disabled_manually`，所以凡是依赖 CI 的项都标了前置。

| 优先级 | 项 | 为什么 | 验收 |
|---|---|---|---|
| **P0** | **被 claim 引用的原始数据落进仓库** | §2.1：`.data/` 不被跟踪，本轮能重算是因为这台机器还在。换机器/换人即失效 | `docs/evaluations/` 或 `.data` 的替代位置有仓内可寻址的摘要 JSON（per-rule totals + commit 清单 + policy 版本与 sha），`claims.md` #19 指向它 |
| **P0** | **旧评估文档里的数字逐个标判据版本** | iter-29 §9.4 的债只还了一半：R1（TS 失明）与 R2（helper）之后，`evaluation-commit-replay-results.md`、`evaluation-m2b-results.md`、`docs/evaluations/` 其余各篇的函数级/测试级数字都跨了判据 | 每篇加一行「本报告数字来自 <commit> 的判据」；不确定的标不可比，不要换算 |
| **P1** | **C# accessor / Java static-init 的单元缺口** | §4 已钉住现状，未修。零单元文件连 maintainability 都不发 | 先出命名方案（`P.get` 还是 `get@line`）走策略评审；**不要先动 kindset**，那会让 `MI=90.2` 这种假快照流进历史库 |
| **P1** | **新规则：benchmark 其实没测东西** | §3 把噪音摘出去了，真缺陷现在无人回答 | 一条规则 + 一个 fixture，判据必须是可执行的提示（例如 setup 返回值未被引用） |
| **P1** | `languageGroups` 有数据无消费者 | iter-29 §9.2 遗留，`/api/rules` 给 13 个分组，`app.js` 与 MCP 都不读 | 前端按语言分组，或删字段。**不要留着** |
| **P1** | 别名追踪（`tt := t`） | 已知精度下界，已钉进测试。dev-cockpit 那条残留命中也属于这一类「命名判据看不到能力」 | 要么做局部数据流，要么在规则文档明写不追（现状是后者，但只在测试注释里） |
| **P2** | `pre-commit` 改 scoped check 根 | iter-29 §2.2 实测未做；纯性能（省 1.6–2.05 秒），但它是唯一每天跑的入口 | 同一脏树前后耗时中位数对比 |
| — | **恢复 CI 的验证循环** | 前置：CI。停着时加的是不会执行的代码 | 按 iter-27 §7 顺序重跑三段证明（合并 → 封板 → 删 label 再跑自举），判据在 iter-27 §8.3 |

### §6.1 本轮学到、值得写进 spec 的一条

**跨版本重放时，policy 与二进制必须同时回到那个 commit。** 我因为只回滚了二进制、policy 用了 `59e0a24^`（比实际晚 5 天、多 29 行），得出「归档不可复现」的错误结论，差点把一份可信数据标成坏数据。`.trellis/spec/backend/` 下已有 `tooling-credentials.md`，这条属于评测方法而非凭证，暂记本文档与评估文档 §2；若再做一次重放，应在 `evaluation-commit-replay-protocol.md` 里加「工具与策略必须同版本」的硬前置。

## §7 验证

- `go vet ./...` 干净；`go test ./... -count=1` 全部 ok（`internal/rules/` 47 passed，四个 Go 夹具逐条 `-v` 全 PASS）；`gofmt -l` 空。
- `bash scripts/cockpit-observe.sh quick` → `render_all: 9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok` + `ok metricgate/internal/cockpit`。
- `go build -o /tmp/mg-check ./cmd/metricgate && /tmp/mg-check check . -arch-base arch.json` → **0 errors / 430 warnings / 27139 snapshots / gate PASSED**，`run #1618` 已入库（触发命令列记录为 `/tmp/mg-check check . -arch-base arch.json`，即 R5 那条链仍在生效）。
- 判据前后对比（同一棵树、`git stash` 两侧）：warning 净 **−2**，减少的两条正是 `internal/rules/rules_test.go` 的 `cognitive-complexity` 与 `function-clone`；`internal/cockpit/rules_zh.go` 的中文描述加长未触发任何新 warning（文件长度仍在线内）。
- docs 相对链接全仓扫描：0 条断链。
- 推送：`94bb28a..739aa11 main -> main`；7 个 workflow 全 `disabled_manually`，本次推送产生 **0 个 run**（与 iter-29 的口径一致，CI 停用状态未变）。
