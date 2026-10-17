# iter-30 重标定：`test-no-assert` × 289 commit 全量重放（实测）

日期：2026-10-18　状态：**已执行**（dev-cockpit 289 commit × 双判据二进制；另 5 包 110 个真实 `_test.go` 语料）
前置：[evaluation-commit-replay-results.md](evaluation-commit-replay-results.md)（2026-10-09/10 原始报告）、[evaluation-commit-replay-protocol.md](evaluation-commit-replay-protocol.md)（预注册口径）
判据历史：`ecd57b5`（iter-29 R2，「拿到 `*testing.T` 的 helper 即算断言」）→ 本轮排除 `Benchmark*`（代码已改，**尚未提交**）

## 0. 一句话结论

`215/289 commit` 这个数在两条新判据下**都成立、且可从归档复现**，但它的含义被原始报告读错了：全部 215 个命中都来自 `internal/eventbus/eventbus_test.go:40` 的**同一个函数** `TestPublishDoesNotBlockOnFullSubscriber`，它自 commits.txt 第 75 个 commit（`444b31ec97`，2026-07-15）引入后，在其后 215 个连续 commit 里被**逐 commit 重复计数**（75..289 零漏网）。findings 总量 1,783 → 233 → 215（再排除 benchmark），distinct anchor 31 → 2 → **1**。附带查清：原始报告 §2「全历史规则族触发总量」18 行里 **13 行无法从归档 JSON 复现**（口径不可考），已就地标注。

## 1. 为什么这轮必须重跑

`docs/claims.md` #19 与 `evaluation-commit-replay-results.md` §2 都引了 `test-no-assert 在 215/289 commit 触发`，并把「伪绿测试在真实 LLM 仓库大规模存在」建立在这个数上。iter-29 改了判据却没有重跑，只在两处写了「语料仓库不在本地、无法重算 ⇒ 215 应读作上界」。**这句「无法重算」是错的**：语料可重克隆（`git clone https://github.com/marein/dev-cockpit`，289 个 study sha 全在），归档 JSON 也在 `.data/commit-replay/` 里。不重跑就改措辞，等于把一个未经检验的数字继续留在 claim 上。

## 2. 口径与执行

- **两侧唯一变量是判据**。同一份 policy（`/tmp/policy-replay.yaml` = `git show 59e0a24^:policy.yaml`，186 行）、同一份 commits 清单（`.data/commit-replay/commits.txt`，289 行，**时间正序**）、同一批 checkout。
  - old 侧二进制 = `/tmp/mg-old2`，从 `git worktree add /tmp/mg-base-tree 275ee38`（iter-29 排期文档那个 commit，即 helper 能力判据之前）构建。
  - new 侧二进制 = `/tmp/mg-new`，从 main 构建（含 `ecd57b5`）。
- **执行**：`/tmp/replay30/run.sh` 对 289 个 sha 逐个 `git checkout` + `check . -config <policy> -format json`，产物 `/tmp/replay30/<sha10>.{old,new}.json`，各 289 个、**0 失败**。
- **必须串行**：两侧并行分片会在同一个工作树上抢 `index.lock` 而失败（首轮 460/578 个输出是 `{"error":"checkout_failed"}`）。改成每个 clone 只服务一个二进制（`/tmp/dev-cockpit` 跑 old、`/tmp/dev-cockpit-b` 跑 new），两侧并发、各自内部串行。
- **判据身份三向确认**（`/tmp/tnafix/a_test.go`：一个 `wantVis(t, …)` helper + `TestUsesHelper` + `TestReallyNoAssert`）：`/tmp/mg-old2` 报 2 条、`/tmp/mg-before` 报 2 条、`/tmp/mg-new` 报 1 条。
- **归档一致性**（决定性的那条）：用时代正确的一对——`/tmp/mg-era-bin`（从 `git worktree add /tmp/mg-era d5cb01c` 构建）+ `/tmp/policy-era.yaml`（= `git show d5cb01c:policy.yaml`，**157 行**）在 `396cd3cd8f` 上跑 → `total 2174 / files 555 / errors 222`，逐条 `fan-out 826 / cyclomatic 380 / npath 365 / cognitive 201 / function-length 195 / file-length 79 / function-clone 44 / params-count 23`——**与归档 `devcockpit/396cd3cd8f.json` 完全一致**。⇒ 归档可信；早期我拿 186 行 policy 重跑出偏高数字（并多出 `unknown-dependency 36622 / wmc 2756 / nom 1810 / empty-block 1132 / custom.go-test-func-naming 14`）是我用错了策略版本，不是数据坏了。
- **本轮的排除 benchmark 是在 new 侧结果上按函数名前缀复算得到的**（`/tmp/mg-bench` 直接从改后代码构建并在 tip 工作树 `/tmp/tna1` 上跑过一遍，结果一致）。

## 3. 实测结果

### 3.1 289 commit 上的总量对比

| | 归档（原报告那次跑） | old = iter-29 之前判据 | new = helper 能力判据 | new + 排除 `Benchmark*`（本轮） |
|---|---|---|---|---|
| findings 总数 | **1,783** | **1,783** | **233** | **215** |
| 触发 commit 数 | 215 | 215 | 215 | **215** |
| distinct anchor（file,line） | 31 | 31 | **2** | **1** |
| 命中文件数 | 6 | 6 | 2 | **1** |
| 单 commit 最大 / 均值 | 15 / 8.3 | 15 / 8.3 | 2 / 1.1 | **1 / 1.0** |
| 按名前缀拆分 | Test 1,765 + Benchmark 18 | Test 1,765 + Benchmark 18 | Test 215 + Benchmark 18 | Test 215 + Benchmark 0 |

- old 的每 commit 命中数分布：`{1:41, 7:19, 8:1, 9:93, 11:10, 12:33, 13:4, 14:4, 15:10}`——93 个 commit 恰好命中 9 条，这种「整段不变」的形态本身就是「同一批 anchor 被重复计数」的信号。
- new 的分布：`{1:197, 2:18}`；去掉 benchmark 后：`{1:215}`。
- old 的逐文件构成：`internal/assistant/memory_test.go` 1,207（21 anchors）/ `internal/eventbus/eventbus_test.go` 215（:40）/ `internal/assistant/job_test.go` 174 / `internal/docker/service_test.go` 155 / `internal/filesystem/quickopen_bench_test.go` 18（:45）/ `internal/cost/price/price_test.go` 14。new 只剩后两者。

### 3.2 215 到底是什么（机制，已确证）

- old 与 new 两侧**首次命中都在 commits.txt 第 75 个**：`444b31ec97`，2026-07-15，"UI updates on server changes"。
- 从第 75 到第 289 **一个不漏**（holes = 0），共 **215** 个 commit。`git log --diff-filter=A --follow internal/eventbus/eventbus_test.go` 同样指向 `444b31e`。
- 该函数原文（`internal/eventbus/eventbus_test.go:38`）：

  ```go
  func TestPublishDoesNotBlockOnFullSubscriber(t *testing.T) {
      b := New()
      _, cancel := b.Subscribe() // registered but never drained
      defer cancel()

      // Far more events than the buffer holds. If Publish blocked on a full
      // subscriber this would deadlock and the test would time out.
      for i := 0; i < subBuffer*4; i++ {
          b.Publish(Event{Type: "terminals"})
      }
  }
  ```

- 读法：规则说它「不可能失败」，而作者用注释主张**超时即失败**（订阅者不排空 → `Publish` 阻塞 → 死锁 → go test 超时）。这是一个真实的设计取舍，不是伪绿测试；规则的命名判据看不到「不返回」也是一种失败模式。**所以这条残留命中应读作规则精度下界的一个例子，不是「LLM 写了 215 个假测试」的证据。**

### 3.3 由此得出的 claim 措辞边界

- 能说：**按 commit 计数，无断言测试形态覆盖 215/289 个 commit**（旧新判据都是 215，可复现）。
- 不能说：**215 个独立的伪绿测试**；真实独立事件数是 **1 个函数**（排除 benchmark 后）。原始报告「大规模存在」的表述来自把 per-commit 计数当成 per-event 计数——`memory_test.go` 那 1,207 条 / 21 个 anchor 也是同一机制（旧判据不认识 helper，一个 helper 型断言函数被 21 个测试调用，每个测试在每个 commit 里再算一遍）。

## 4. 排除 `Benchmark*` 的判据决定

原始判据把 `Benchmark*` 与 `Test*` 同等对待（`go_test_no_assert.go:34`），并要求签名含 `*testing.T` **或** `*testing.B`。实测代价：

| 语料 | 新判据（含 benchmark） | 其中 benchmark | 排除 benchmark 后 |
|---|---|---|---|
| dev-cockpit 289 commit | 233 | **18（7.7%）** | 215 |
| dev-cockpit tip 工作树（单 commit） | 19 | 18 | **1** |
| `/tmp/corpus` 5 包 / 110 `_test.go` | 19 | **14（73.7%）** | **5** |

- 这 18 条（tip 上的 `BenchmarkQuickOpenQuery`）与 14 条（go-humanize 12：`bigbytes_test.go:210/216`、`bytes_test.go:136/142`、`comma_test.go:77/83/89`、`commaf_test.go:12`、`ftoa_test.go:104/115/121`、`si_test.go:124`；go-sdk `mcp/event_test.go:490`；uuid `uuid_test.go:686`）全是同一句话：让一个参数是 `b` 的函数去「add `t.Error/t.Fatal`」。**不可执行的提示占该类命中的 100%**，而规则自己的注释就写着「false positive 的代价是没人读的 warning」。
- benchmark 不失败是**设计**（它报 ns/op，工具链从不指望它 `b.Error`）。「benchmark 其实什么都没测」是真缺陷，但那是另一条规则（例如 setup 里的返回值未被引用），不该借这条规则的壳。
- 排除的 5 条残留（`/tmp/corpus`）逐条为名：`go-isatty@v0.0.24/isatty_others_test.go:11 TestTerminal`、`go-sdk@v1.8.0/mcp/server_test.go:828 TestAddToolInputSchemaComposition`、`pprof/internal/binutils/binutils_test.go:241 TestSetTools` 与 `:248 TestSetFastSymbolization`、`pprof/profile/profile_test.go:1907 TestThreadSafety`——全是真报。⇒ **去掉 benchmark 一条真阳都没丢**。
- `TestMain(m *testing.M)` 本来就被签名门排除；排除 `*testing.B` 后 `TestMain` 与 benchmark 一起被前缀挡住，无需特例。

### 4.1 代码与测试（`internal/rules/`）

- `go_test_no_assert.go`：前缀判断去掉 `Benchmark`，签名门只认 `*testing.T`，`testingTParam` 不再匹配 `*testing.B`；函数头注释记录上面的实测数字与理由。
- `rules_test.go`：`TestGoTestNoAssertNamesAnyTParam` 的夹具里 `check` 的参数从 `*testing.B` 改成 `*testing.T`、删掉 `BenchmarkViaHelper`（它本来就因把裸 `b` 传给 helper 而不被 flag，留着会让这条测试在改动前后都绿，测不到新边界）。**want 数不变（1 条 = `TestPassesAnAlias`）**，精度下界仍然被钉住。
- 新增 `TestGoTestNoAssertIgnoresBenchmarks`（夹具用 dev-cockpit 的 `BenchmarkQuickOpenQuery` 形态 + 一个 `b.Loop()` 版 + `TestMain`，**want 0 findings**），并把 Go 这一族的四个夹具全部搬进新文件 `internal/rules/go_test_no_assert_test.go`。搬的理由不是美观：`rules_test.go` 已到 413 行，加上 benchmark 夹具就要越 `file-length` 的 warning 阈值（400），而这个规则族已经被改过三次判据（只看名字 → helper 能力 → 排除 benchmark），每次都需要自己的夹具，继续往一个共享源字符串里堆会让下一个改动无法定位它真正动了什么。搬完 `rules_test.go` 216 行、新文件 217 行，本仓 warning 432 → 430。
- 验证：`go vet ./...` 干净；`go test ./internal/rules/ -count=1` → 47 passed（四个 Go 夹具逐条 `-run TestGoTestNoAssert` 全 PASS，Java/chain 等留在原文件的也照跑）；`go test ./... -count=1` → 全部 ok；语料与 tip 实测见上表。

## 5. 负面结论：原始报告 §2 的「全历史规则族触发总量」13 行不可复现

对归档 `.data/commit-replay/devcockpit/*.json`（289 个，findings 总量 **234,606**）逐条求和：

| 规则 | 报告 §2 | 归档 findings | 归档 distinct file | 归档 distinct anchor |
|---|---|---|---|---|
| fan-out | 85,771 | **85,771** ✅ | 291 | 2,767 |
| cyclomatic-complexity | 38,150 | **38,150** ✅ | 213 | 1,213 |
| npath-complexity | 37,093 | **37,093** ✅ | 206 | 1,172 |
| cognitive-complexity | 22,926 | **22,926** ✅ | 157 | 704 |
| function-length | 22,361 | **22,361** ✅ | 147 | 743 |
| params-count | 9,847 | 2,749 | 28 | 125 |
| file-length | 5,296 | 9,903 | 88 | 504 |
| function-clone | 4,912 | 7,647 | 50 | 345 |
| **test-no-assert** | **3,492** | **1,783** | 6 | 31 |
| call-chain-depth | 279 | 14 | 1 | 1 |
| nesting-depth | 44 | 1,507 | 17 | 48 |
| banned-os-exec | 43 | 3,388 | 28 | 32 |
| banned-panic | 3 | 883 | 4 | 30 |
| banned-fmt-Print* | 3 | 148 | 2 | 10 |
| boolean-params / todo-comment / silent-catch / empty-block | 334 / 87 / 55 / 21 | **0 / 0 / 0 / 0** | — | — |

- 能试的口径都试过：findings 全量、仅 error、commit 数、distinct file、distinct anchor、metric 快照行数、快照 `value>0` / `v>=warn` / `v>warn`、把 `replay_out/` 与 `h0_out/` 一起递归 glob（299 个 JSON，fan-out 只多 210）。**没有任何一种口径同时解释这 13 行**，⇒ 结论是「口径不可考」，不是「换个算法就能对上」。
- 4 条零检出规则可由时代策略解释（`/tmp/policy-era.yaml`）：`boolean-params: { warn: 2, error: off }`（:32、:72）——快照有 654,123 行、`v>=warn` 1,382，但 error off 所以**不产 finding**；`todo-comment: enabled: false`（:17-19 go、:60-62 java）；`empty-block: severity: warning` 只配在 java 段（:58-59）；`silent-catch` **完全没有条目**。
- 报告其余部分**与归档一致**：0/289 零检出、首个 commit 75/7、findings 75→2174、npath 极值 218,160、§3 那 5 个 commit 的 before/after 表（逐条复核 75/7、83/8、90/9、92/9、157/14 与 `check_r1_after` 的 50/0 全对）。⇒ **只有 §2 的总量那一行需要标注，报告的结论层不受影响。**
- 数据持久性提醒：`.data/` 被 `.gitignore:8` 忽略且**从未被 git 跟踪过**（`git log --all -- .data` 为空）⇒ 归档只存在于这台机器，报告引用的原始数据不可从仓库恢复。

## 6. iter-30 另一项 P0：命名函数单元的跨语言普查

结论：**JS 那个洞没有别的语言共享，照搬 `isJSModuleFunc` 是错的；真正的缺口是「分支不在任何单元里」这一类。**

- 各语言「lambda 绑到名字」的写法实测全部 `FUNCS=0`（Go `var helper = func(x int) int {…}`、Java `static final Function<Integer,Integer> HELPER = x -> {…}`、C# `static Func<int,int> Helper = (x) => {…}`、C++ `auto helper = []…` 与 `const std::function<int(int)> helper = []…`、Python `helper = lambda x: …`、Rust `const HELPER: fn(i32)->i32 = |x| {…}`）。**这是设计，不是缺陷**：这些就是闭包，全站一致规则是折叠进外层函数（`docs/metrics.md` 明写 Go `func_literal`、Java `lambda_expression` 同规则）。JS 之所以要特例，是因为 `export const f = (x) => …` 在模块顶层**根本没有外层函数**可折叠。
- 反向验证：把 `accessor_declaration` 塞进 `internal/metrics/kindset.go:71` 的 `csFuncKinds`，普查测试立即失败并打出 `FUNCS=2 names=[get set]` + `MI=90.19615793986456`。名字是编的——这正是 iter-29 对 `export default` 明确拒绝的「往快照里塞假标识符」。
- 真缺口（已用测试钉住，未修）：**C# property `accessor_declaration`（get/set）、Java `static_initializer` 与实例初始化块、C# top-level statements**——它们的分支不在任何单元里；若整个文件没有别的单元，`AnalyzeFile` 置 `MI=0`，而 `engine.go` 只在 `MI>0` 时发快照 ⇒ **一条 maintainability 都不发**，文件隐形。修它需要命名方案（`P.get`？）加 lowerer 改动（`cs_adapter.go:21` 的 `lowerFunc` 没有 accessor 的 case），并按项目规矩走策略评审。
- 已提交在分支 `feat/iter30-func-unit-census`（`ed58746 test(metrics): census the file-scope function spellings of every language`，**尚未合 main**）：`internal/metrics/func_unit_census_test.go` 5 个测试 + `docs/metrics.md` 新增「the file-scope function census (iter-30)」一节。本地量化不了：本仓只有 4 个 `.java`、**0 个 `.cs`**。

## 7. 复现命令

```bash
# 289 commit 双判据重放（必须串行；两个 clone 各服务一个二进制）
bash /tmp/replay30/run.sh                       # 产物 /tmp/replay30/<sha10>.{old,new}.json

# 单点复核（tip 工作树，排除 benchmark 后的当前判据）
git worktree add /tmp/tna1 396cd3cd8f
go build -o /tmp/mg-bench ./cmd/metricgate
/tmp/mg-bench check /tmp/tna1 -format json | python3 -c "
import json,sys
d=json.load(sys.stdin)
print([f['file']+':'+str(f['startLine']) for f in d['findings'] if f['ruleId']=='test-no-assert'])"

# 归档聚合（§5 那张表的来源）
python3 - <<'PY'
import json,glob,collections
c=collections.Counter()
for p in glob.glob('.data/commit-replay/devcockpit/*.json'):
    for f in json.load(open(p))['findings']: c[f['ruleId']]+=1
print(sum(c.values()), dict(c.most_common(20)))
PY

# 时代复现（证明归档可信的那条）
git worktree add /tmp/mg-era d5cb01c   && (cd /tmp/mg-era && go build -o /tmp/mg-era-bin ./cmd/metricgate)
git show d5cb01c:policy.yaml > /tmp/policy-era.yaml
cd /tmp/tna1 && /tmp/mg-era-bin check . -config /tmp/policy-era.yaml -format json | jq '.findings|length'   # 2174
```

## 8. 下一轮（iter-31）待办

1. **提交本轮判据改动**：`internal/rules/go_test_no_assert.go` + `rules_test.go` 目前**未提交**；合入后 `docs/capabilities.md` 的 `test-no-assert` 行、`internal/cockpit/rules_zh.go` 的中文描述也要跟着改（现描述未区分 Go/Java 之外的拼写）。
2. **合并 `feat/iter30-func-unit-census`（`ed58746`）到 main**，它还在分支上。
3. **就地修措辞**（本轮已改：`evaluation-commit-replay-results.md` §2/§6/§8.3、`docs/claims.md` #19、`docs/README.md` 该行、`iter-29-midterm-backlog-audit.md`、`evaluation-iter29-remediation.md` §7 与 §9 表、`docs/capabilities.md` 的 `test-no-assert` 行）：`docs/github-setup.md` 与其余任何仍写着「不在本地、无法重算 / 215 是上界」的地方，现在都过时了。
4. **新 P1：`Benchmark*` 该由谁管**——本轮把它从 `test-no-assert` 摘出来了，但没有规则回答「这个 benchmark 其实什么都没测」。
5. **`test-no-assert` 对「靠超时/死锁失败」的取舍**：`eventbus_test.go:40` 那条是规则精度下界的现存样本；若要修，方向是把「有 `defer cancel()` + 无界循环」这类形态识别为失败手段，代价是判据复杂化，需评审。
6. **`.data/` 不在版本控制里**：任何被 claim 引用的原始数据都应该有仓内可寻址的落点（摘要 JSON 或校验和），否则结论不可复核。
