# 迭代 20 · 两个闸门入口的棘轮对齐（D2）

日期：2026-10-16（goal `mvak486g-dgwphk` task-1…task-5）

编号说明：调研文档 §7 排期里 `iter-20` 原行是 S2（`hooks/post-edit.sh` 发货），`iter-22` 行才是 D2 棘轮对齐。
本轮按目标合同占用 `iter-20` 这个日志序号，**S2 仍未做**；D2 那一行已在本日志收口后标记完成。
见 [产品演进方向调研 §4 D2 / §7](../research/product-evolution-directions-2026-10.md)。

---

## 一、为什么做这个

同一个函数、同一个 base=HEAD 口径，**CLI 报 1 条，agent 走 MCP 报 3 条**。

这不是「哪个更严」的口径选择，而是同一份判定代码在两个入口走了两条不同的过滤路径：

| 入口 | 改动前的过滤 | 有基线值吗 |
|---|---|---|
| CLI `metricgate check -diff` | `git.ChangedLinesFor` → `touchedMetricSnapshot`（住在 `cmd/metricgate/regress_cmd.go:55`）→ `engine.SplitSummary(summary, changed, baseValues)` | **有** |
| MCP `check_path {diff_only:true}` | `git.ChangedLinesFor` → `engine.FilterSummary`（`checks.go` 原 `:85`） | **没有** |

`engine.FilterSummary` 是「无基线值」那一档（`internal/engine/filter.go:30`，实现就是 `SplitSummary(s, changed, nil)`），
它的文档注释原本直说服务对象是 MCP——所以这不是意外分叉，是**写在注释里的一个二等入口**。

后果正是 claims #7 的核心那类发现：一个「变得更糟但还没越阈」的度量，
只有基线值能判定它是否真的动了。CLI 判得出，MCP 判不出；而 agent 走的是 MCP。
一个卖「确定性」的闸门，在两个入口给出两个答案，比慢更伤可信度。
`docs/backlog-v1x.md:301` 与 `:308` 把同一件事记了两次（迭代 12 与迭代 15 各一次），
两轮都没做，理由是「要对齐得先把基线取数从 CLI 里搬出来」——那是本轮 task-1。

---

## 二、分叉的实测数字

### 2.1 真实仓库脏树（夹具 `/tmp/mgd2fix` = 主仓 `5c1595c` clone + 16 个脏 `.go` 文件，配置 `.metricgate-nostore.yaml`，base=HEAD）

| 口径 | files | findings | hidden | unchanged |
|---|---|---|---|---|
| CLI `check . -diff -format json` | 254 | **1** | 316 | 2 |
| MCP `check_path {path:".",diff_only:true}`（改动前） | 254 | **3** | 316 | **无此字段** |
| MCP（改动后） | 254 | **1** | 316 | 2 |

多出的 2 条是**值没动过的度量**，只因为 MCP 侧压根没去读基线就被当成新问题报给 agent：

```
(fan-out, cmd/metricgate/check_cmd_test.go, 21, warning)
(fan-out, cmd/metricgate/main.go, 337, warning)
```

⇒ 改动前 MCP **多报 2 条、漏报 0 条**。多报不是「更安全」：agent 拿到一条自己没弄坏的告警，
就会去动一段本不需要动的代码，或者学会忽略这个工具。

### 2.2 受控合成脏树（`basepkg/a.go` 只改一个字符串字面量，改动落在深层嵌套函数体内）

| root 形状 | 全量 findings | `-diff` findings | hidden | unchanged |
|---|---|---|---|---|
| `.`（工作树根） | 4 | **1** | 2 | **3** |
| `basepkg`（子目录） | 4 | **4** | 2 | **0** |

第二行是 §五 那个坐标系问题的实测证据，也是本轮**没有顺手改掉**的东西。

---

## 三、修法：只共用，不另写一套

原则来自调研文档的写法约定——「不许 hook / 入口另写一套过滤规则」。
所以本轮没有在 MCP 侧补一段「读基线」的代码，而是把 CLI 那段搬进共享层，两侧各留一次调用。

### 3.1 新增 `internal/baseline/baseline.go`（178 行，`package baseline`）

| 导出符号 | 位置 | 原来在哪 |
|---|---|---|
| `Split(cfg, root, base, summary) (Result, error)` | `:45` | **新**：把「取改动行 → 取基线值 → 过滤」串成一次调用 |
| `type Result struct { engine.DiffSplit; Changed map[string][]diff.Range }` | `:66` | **新** |
| `Snapshot(cfg, root, base, changed) (engine.BaseValues, error)` | `:89` | `cmd/metricgate/regress_cmd.go:55` `touchedMetricSnapshot` |
| `FullSnapshot(cfg, base) ([]model.MetricResult, error)` | `:125` | 同文件 `baseMetricSnapshot` |
| `RelativizeMetrics(ms, root)` | `:149` | 同文件 `relativizeMetrics`（导出因为两侧都要） |
| `repoRelativePaths(root, changed)` | `:165` | 同文件同名私有函数（**已是并发会话 S1 批量化后的版本**，本轮搬的是当前树里的它） |

`Split` 是两侧唯一的调用面。它存在的原因是：**入口不该自己挑过滤器**。
改动前 MCP 侧不是「选了 FilterSummary」，是「压根没有基线这一步」，
于是一条值未变的度量被报给了写下它的那个 agent。入口只保留渲染，git 侧与过滤侧都归一处。

`Result` 保留 `Kept / PreExisting / Unchanged` 三组而非一个数字，理由写进注释：
「0 new findings」≠「什么都没错」，而且**基线缺失时绝不能拿 0 顶替**——
`Snapshot` 在「被触碰的文件在 base 里一个都不存在」时返回 nil map，过滤器把 nil 读作「全保留」。

### 3.2 两侧改为各一次调用

```
cmd/metricgate/check_cmd.go:166      split, err := baseline.Split(cfg, root, base, summary)
cmd/metricgate/regress_cmd.go:25,29  baseline.FullSnapshot / baseline.RelativizeMetrics
cmd/metricgate-mcp/checks.go:84      split, err := baseline.Split(s.cfg, path, "HEAD", summary)
```

`regress_cmd.go` 从 131 行缩到 35 行，只剩 `applyRegressions`。
MCP 侧 `checkPathOut` 新增 `Unchanged int \`json:"unchanged,omitempty"\``，
`engine` 与 `git` 两个 import 从 `checks.go` 移除（grep 确认该文件已不引用它们）。

### 3.3 配套口径修正

- `cmd/metricgate-mcp/tools.go:54` 的 `check_path` 描述重写：明确 diff_only=true 走的是与 `check -diff` **同一条棘轮**、
  base 固定 HEAD、`hidden` 与 `unchanged` 两个计数器说的是闸门**没报**多少、新文件没有基线值所以度量必留，
  以及「仅 `path` 在工作树根时这个保证成立」（§五）。实跑 `tools/list` 核过实际字节（§七 命令 6），12 个工具总数未变。
  另修正一处措辞：描述初稿写的是“值与 base revision 逐字节相同就丢掉”，而 `valueMoved`（`internal/engine/filter.go:175`）
  的实口径是 `m.Value > prev`，即**值未上升就归入 `unchanged`**（值下降也算，因为它不是退化）。
  描述已改成 "did not rise"：把过滤器写错一步（哪怕是往宽的方向），agent 就会据此推「降了也不报」。
- `internal/engine/filter.go:30` `FilterSummary` 的注释原文把 MCP 写成它的预期调用方，已改：
  两个闸门入口都走 `baseline.Split`，无基线那一档留给「真的没有 base 树」的调用方（例如对一个不是工作树的目录干活），
  **不是入口图省事时可以选的默认**。只改注释，未动 verdict 语义（合同边界）。

### 3.4 独占性证明（Req1 的 grep）

```
$ /usr/bin/grep -rn "baseline.Split(\|SplitSummary(\|FilterSummary(\|ChangedLinesFor(" --include='*.go' ./cmd ./internal | grep -v _test.go
./cmd/metricgate-mcp/checks.go:84:  split, err := baseline.Split(s.cfg, path, "HEAD", summary)
./cmd/metricgate/check_cmd.go:166:  split, err := baseline.Split(cfg, root, base, summary)
./internal/baseline/baseline.go:46: changed, err := git.ChangedLinesFor(root, base)
./internal/baseline/baseline.go:54: return Result{DiffSplit: engine.SplitSummary(summary, changed, baseValues), ...}, nil
./internal/engine/filter.go:30,31,88,106                                          # SplitSummary / FilterSummary 定义与内部互调
./internal/git/git.go:66                                                           # ChangedLinesFor 定义
```

两个入口各只剩一次 `baseline.Split` 调用；`SplitSummary(` / `FilterSummary(` / `ChangedLinesFor(`
在 `internal/baseline` 之外只剩各自的定义与 `internal/engine/filter_test.go` 单测。
基线归档取数（`git.Archive*` + `engine.IndexBaseValues`）在 `internal/baseline` 之外只剩
`cmd/metricgate/clonedrift.go:38` 的 `git.Archive` ——那回答的是「近似克隆在两树间的漂移」，不是 value-delta 棘轮，本轮不碰。

⚠️ grep 口径必须限定 `./cmd ./internal`（或排除 `.data/`）：`.data/m2b-raw/m2b_sandbox/` 是一份 **gitignore 的旧树快照**，
里面 `cmd/metricgate-mcp/checks.go:85` 还留着改动前的 `engine.FilterSummary`。
不加这个限定会得到「MCP 仍在用无基线过滤」的假结论——整树 grep 时留意。

---

## 四、一致性测试（Req2）

三个新文件，都在 `cmd/metricgate-mcp/`（`package main`，所以能直接用 `checkPathOut` 这个真实出参类型）：

| 文件 | 行 | 内容 |
|---|---|---|
| `mcpclient_test.go` | 118 | `mcpLine`：真 stdio JSON-RPC 客户端（`startMCP` / `initialize` / `callTool` / `callCheckPath`），失败时把服务端 stderr 一起打出来 |
| `parity_test.go` | 270 | 夹具 `newDirtyTree`（`gittest` + 断言 `git status --porcelain` == `M basepkg/a.go`）、`buildGatePair`（真编译两个二进制）、四元组 `(ruleId, file, startLine, severity)` 与 `diffTuples`、`runCLI` / `runFullCLI` |
| `parity_diff_test.go` | 164 | `runMCP`、`TestDiffOnlyMatchesCLIOnEveryRootShape`（两个 root 形状）、`assertSameGate`、`assertNonVacuous`、`TestDiffOnlyDropsUnchangedMetrics` |

**走真 stdio transport 而不是 in-process 调 handler**，因为要钉的是操作者实际使用的两个面：
transport、schema 绑定、JSON 形状都在断言范围内。

`assertSameGate` 除 finding 集合外还比 `files` / `hidden` / `unchanged` / `errors` / `warnings`：
两个面可以给出同样的幸存者却藏掉不同数量的东西，而 `hidden` + `unchanged` 正是「闸门没报多少」的自述——
干净的 verdict 配一个错的计数器仍然是谎。

`assertNonVacuous` 用来拒绝「空集相等」这种无意义的通过：`hidden>0`、`findings>0`，
且 `root="."` 时必须 `Unchanged>0`——否则这一轮根本区分不了 value-delta 棘轮和只看行的过滤。

`TestDiffOnlyDropsUnchangedMetrics` 是反向锁死：它断言全量运行与 `-diff` 运行之间
确实有度量被「值未变」这一档丢掉（列 `SrcMetric` 且落在被改动文件上的 finding 才能这么归因，
因为改动文件自身的行过滤本就留得住它们）。**没有这条，把 CLI 的基线也拆掉同样能让一致性测试变绿**，
而那是把闸门削弱的方向。

### 4.1 实跑

```
$ date +%T; go test ./cmd/metricgate-mcp/ -run TestDiffOnly -count=1
15:47:38
ok  	metricgate/cmd/metricgate-mcp	47.328s
15:48:31
```

同一条断言此前多次通过（46.3s / 50.4s / 52.4s / 56.9s / 68.7s）。
包级全量：`go test ./cmd/metricgate-mcp/... -count=1` → ok 93.496s（15:33:29–15:35:09）。

### 4.2 反向验证（把 MCP 换回改动前，测试必须报红）

临时把 `cmd/metricgate-mcp/checks.go:84` 的 `baseline.Split` 换回
`git.ChangedLinesFor` + `engine.FilterSummary`（备份 `/tmp/checks.go.mine3`，改完立刻还原）：

```
$ date +%T; go test ./cmd/metricgate-mcp/ -run 'TestDiffOnly' -count=1
15:46:18
--- FAIL: TestDiffOnlyMatchesCLIOnEveryRootShape (28.36s)
    --- FAIL: TestDiffOnlyMatchesCLIOnEveryRootShape/root=. (6.54s)
        parity_diff_test.go:48: the two entry points gated the same tree differently (base=HEAD):
              CLI only: (none)
              MCP only: (cognitive-complexity, basepkg/a.go, line 5, warning), (nesting-depth, basepkg/a.go, line 5, warning), (params-count, basepkg/a.go, line 5, warning)
              CLI=1 findings, MCP=4 findings
        parity_diff_test.go:48: unchanged count: MCP=0 CLI=3
        parity_diff_test.go:48: severity counts: MCP errors=0 warnings=4, CLI errors=0 warnings=1
    --- PASS: TestDiffOnlyMatchesCLIOnEveryRootShape/root=basepkg (…s)
FAIL
FAIL	metricgate/cmd/metricgate-mcp	50.389s
```

这个红/绿的分布就是本轮的因果证明：**只有 `root="."` 报红**（值棘轮该生效的地方），
`root=basepkg` 两侧同样退化所以照旧通过，
`TestDiffOnlyDropsUnchangedMetrics` 是纯 CLI 断言所以照旧通过。测试不是「只要有差异就叫」的机关枪。

同一操作做了两次（15:40:13、15:46:18），结果一致；还原后 `grep -c baseline.Split` == 1，gofmt 净。

---

## 五、坐标系归因（Req3：`path` 与 `root` 是不是同一坐标系）

**结论：是同一坐标系，本轮不需要修；但顺着这个问题挖出一个既有缺陷，本轮也没修，记在遗留里。**

`internal/git/git.go:66 ChangedLinesFor(checkRoot, base)` 做三件事：
`Root(checkRoot)` 取工作树根 → `LoadChanges(repoRoot, ModeWorkspace, ResolveBase)` →
`rebaseChanges(changes, repoRoot, checkBaseDir(checkRoot))`，
返回的 map 键是**相对 check 根**的路径。CLI 传的是 `root`（`check_cmd.go:83` 起于 `"."`，可被 `Arg(0)` 覆盖），
MCP 传的是 `path`（`checks.go:70-72` 空值同样起于 `"."`）——两侧都是「本次检查的根」，
且都同一个值传给 `engine.Run` 做范围界定，所以按构造就是同一坐标系。
实测两种 root 形状（`.` 与 `basepkg`，相对与绝对路径都试了）下两侧计数与集合全等（§二 表 2.2、§四）。

顺出来的缺陷在这条链的**另一段**：`engine.Run(root)` 把 finding 的 `file` 键写成**相对 run 根**，
而 `Snapshot` 从归档目录跑第二遍引擎、`IndexBaseValues` 出来的键是**相对工作树根**。
两个键空间只在 `root == 工作树根` 时重合；子目录运行时 `valueMoved` 的 span/base 关联必然 miss，
于是 value-delta 那一半**静默退化为全保留**（§二 表 2.2 第二行：`unchanged=0`）。

- 这不是本轮引入的：改动前的 CLI 子目录 `-diff` 就是这个行为，
  用 HEAD 版二进制（`/tmp/mg-d2-before`）与当前二进制在同一探针对跑，两种 root 的 diff-only 摘要逐字节相同。
- 它也不破坏本轮的一致性以及两入口同退化（这正是 §四 的两个子测试都要跑的原因）。
- 本轮没修的边界理由：目标合同把 `internal/engine/filter.go` 的 verdict 语义列为不可动，
  而要让子目录也生效就得把 `Snapshot` 的键空间改到 run 根，**那会改变 CLI 已发货的子目录输出**——属于合同里「停下问用户」的那一类。
- 反过来说，`root="."` 是唯一被保证的口径，所以 `cmd/metricgate-mcp/tools.go:54` 的 `check_path` 描述里已写明这一点（“Guarantee holds for path at the worktree root: under a subdirectory … nothing is reported as unchanged”），
  让 agent 在调用前就知道子目录跑拿不到值棘轮。真正的修法见遗留第 1 条。

---

## 六、顺带：全树 `go vet ./...` 转绿

`internal/store/metritrend.go:86`（现 `:88`）：

```go
-	args := append(append([]any{}, filterArgs...))
+	args := append([]any{}, filterArgs...)
```

内层那个 `append(filterArgs...)` 没有传值，是 vet 的 `append with no values`，
由 `5c1595c`（2026-10-14）带入。上一轮（迭代 19）因此只能说「定向 vet 干净」，
不能写「全仓 vet 干净」——那行限定语现在可以删了，见本日志 §七 与迭代 19 的对应遗留行。

保留这一层 `append` 而不是干脆写 `args := filterArgs`：`LIMIT ?` 的值要往这个列表上再 append，
直接在 `runFilter()` 交回的 slice 上追加会写穿它。args 个数与 SQL 占位符数一致
（`filterArgs` 个数 == `runScope` 渲染的谓词数，`window.go:111-117` 无谓词时返回 nil；只有 `w.Limit>0` 时多一个）。

```
$ date +%T; go vet ./...; echo "exit=$?"; wc -c < 输出
15:53:22 → 15:53:24   exit=0，输出 0 字节
$ go test ./internal/store/... -count=1
ok  	metricgate/internal/store	1.348s        # 47 passed
```

---

## 七、验收命令（复刻清单）

```bash
# 0) 一致性测试（约 47–95 s；会真编译两个二进制 + 起一个 MCP 进程）
go test ./cmd/metricgate-mcp/ -run TestDiffOnly -count=1        # 期望 ok

# 1) 独占性：两入口只经 baseline.Split（口径与 .data/ 的坑见 §3.4）
/usr/bin/grep -rn "baseline.Split(\|SplitSummary(\|FilterSummary(\|ChangedLinesFor(" --include='*.go' ./cmd ./internal | grep -v _test.go

# 2) 两入口同树对测（真实脏树；夹具自己 clone，别用主仓工作树以免被并发会话影响）
git clone --no-local . /tmp/mgd2fix && cd /tmp/mgd2fix
#    把主仓的脏 .go 文件 cp 过来，配置关掉 store：
sed 's/^  enabled: true$/  enabled: false/' .metricgate.yaml > .metricgate-nostore.yaml
metricgate check . -diff -config .metricgate-nostore.yaml -format json > /tmp/cli.json
#    MCP：initialize → notifications/initialized → tools/call check_path {"path":".","diff_only":true}
#    期望：findings / files / errors / warnings / hidden / unchanged 与 CLI 四元组集合全等

# 3) 搬迁行为等价（Req4）：同一脏树、同一 base，搬迁前后 -diff JSON 逐字节相同
#    sha256 80d157536d7c84b5ecb850ed3c7400fb67c2c715e8f2a06c6678bdf4e509bd5e（改动前 == 改动后）
#    文本输出只自报耗时不同，用 sed -E 's/,[ ]*[0-9]+\.[0-9]+s//' 归一后逐字节相同

# 4) 全树 vet / build / 定向测试
go build ./... && go vet ./...                                   # 都 exit 0
go test ./cmd/metricgate/... -count=1                            # 60 passed（14:17:37 / 14:41:36 两轮）
go test ./internal/store/... -count=1                            # 47 passed

# 5) 闸门收口：scripts/cockpit-observe.sh fast 出 `gate: PASSED (fail_on=error)` + 整树 check_path
#    本轮实测值与逐条 error 归属见 §八（不要拿数字对，拿「本轮 10 个文件里 0 条 error」这个口径对）

# 6) 工具描述实跑核字（防注释与线上说法分叉）+ 工具总数
#    initialize → tools/list，取 check_path.description；本轮用 /tmp/mgmcp-docs 实跑，结果见 §3.3
go build -o /tmp/mgmcp-docs ./cmd/metricgate-mcp && cd /tmp/mgd2fix && python3 - <<'PY'
import json,subprocess
p=subprocess.Popen(["/tmp/mgmcp-docs"],stdin=subprocess.PIPE,stdout=subprocess.PIPE,text=True)
def send(o): p.stdin.write(json.dumps(o)+"\n"); p.stdin.flush()
send({"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}); json.loads(p.stdout.readline())
send({"jsonrpc":"2.0","method":"notifications/initialized"})
send({"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}})
r=json.loads(p.stdout.readline())
print("TOOLS_TOTAL=",len(r["result"]["tools"]))
for t in r["result"]["tools"]:
    if t["name"]=="check_path": print(t["description"])
p.kill()
PY
#    期望：TOOLS_TOTAL= 12，描述含 "same ratchet" / "did not rise" / "worktree root"
```

---

## 八、收口实测（2026-10-16 17:13，主仓工作树）

```
16:19:43  go build ./...                      exit 0（16:19:46 首次撞到并发写者，见下）
16:23:05  go vet ./cmd/... ./internal/baseline/... ./internal/store/... \
             ./internal/engine/... ./internal/git/...                 exit 0
16:23:34  HEAD 快照 + 只叠 metritrend.go 那一行 → go vet ./...          exit 0
16:48:20  go vet ./...（主仓全树）                                      exit 1 ← 他人 WIP
17:08:57 → 17:09:00  go vet ./...（主仓全树）                          exit 0，0 字节输出
17:09:44 → 17:12:34  bash scripts/cockpit-observe.sh fast              gate: PASSED (fail_on=error)
16:23:56 → 16:24:27  go test ./cmd/metricgate/... -count=1             ok 19.086s
16:38:44 → 16:39:52  go test ./cmd/metricgate-mcp/ -run TestDiffOnly   ok 62.611s（两 root 形状全绿）
```

整树 `check_path`（17:13:18，非 diff 口径）：**322 files / 9 errors / 426 warnings**，9 条 error 逐条为
`cmd/metricgate/registry_parity_test.go:184`（cognitive + cyclomatic）、`internal/cockpit/dashboard.go:100`（cyclomatic）、
`internal/metrics/metrics.go:1`（file-length）、`internal/rules/chain.go:49`（cyclomatic 17）、
`third_party/tree-sitter-vb-dotnet/src/parser.c`（file-length / cyclomatic / function-length / npath）。
**没有一条落在我改的 10 个文件里**（上一轮迭代 19 计 6 条，多出的 3 条是其他会话在这些文件里新加的）。
本轮 10 个文件的逐文件 `check_path`：`baseline.go` / `regress_cmd.go` / `checks.go` / `tools.go` /
`mcpclient_test.go` / `parity_test.go` / `metritrend.go` / `filter.go` 全 PASS 0 errors 0 warnings，
`check_cmd.go` 0 errors / 11 warnings、`parity_diff_test.go` 0 errors / 1 warning（均为遗留第 4 条）。

### 两条观测方法上的坑（都花了一轮才排掉）

1. **`fast` 的 gate 步骤依赖目录里有 `.git`**：为了隔离并发写入，我把整棵树 rsync 到 `/tmp/mgd2full` 跑 `fast`，
   前几步都过，最后 `metricgate check internal/cockpit/web` 输出
   `note: no policy file found; using built-in defaults` —— 而不是仓库策略。原因在 `internal/configpath/configpath.go:132`
   `stopDir()`：非 git 目录时 `git.Root()` 返回空 ⇒ `stop = start`，`:98` 的 `if dir == stop { break }` 让搜索**原地停止**，
   连当前目录的策略都不读。`cd /tmp/mgd2full && git init -q` 后立刻恢复 `gate: PASSED`（17:08:05）。
   ⇒ 复制树做收口观测必须带上 `.git`；这条不算缺陷（用户没让我改 `configpath`），但它会让任何「在临时目录里跑闸门」的做法
   静默降级到内置默认阈值，属于下一轮值得确认的口径问题。
2. **`go vet`/`go test` 撞并发写者的窗口**：16:19:46 `internal/store/measures_lang.go` 与 16:18:32
   `internal/cockpit/gate_api.go` 被另一会话同时改，`go build ./...` 报
   `internal/cockpit/gate_api.go:236:20: st.RunMeasuresByLang undefined`；16:48:20 全树 vet 报
   `gate_api_test.go:190:16: assignment mismatch: 2 variables but gateBand returns 3 values`。
   两处在 16:21:48 / 17:08:57 都由对方自己补完自行消失（mtime 归属：`gate_api.go` 16:18:32 vs `gate_api_test.go` 14:54:24 →
   16:55:04），**一行都不是我改的**。⇒ 全树观测前先看 `pgrep`，并报时间戳；否则会把别人的在途编辑当成自己的回归。
3. **Go 模块缓存被并发驱逐**：16:25:14 跑一致性测试报
   `.../tree-sitter-verilog@v1.0.3/bindings/go/binding.go:4:11: fatal error: '../../src/parser.c' file not found`，
   且整个 `/Users/mac/go/pkg/mod/github.com/tree-sitter/` 目录消失（其他会话的 `go mod tidy`/`clean`）。
   `go mod download github.com/tree-sitter/tree-sitter-verilog` 重取后 16:39:52 全绿（62.611s）。
   ⇒ 测试失败先分清「编译不过」还是「断言不过」，前者大概率是环境问题。

---

## 九、遗留

| 项 | 状态 | 说明 |
|---|---|---|
| 子目录 root 时 value-delta 静默退化（§五） | ⬜ **下一轮首选** | 修法是让 `Snapshot` 的基线键归一到 run 根（`engine.Run` 侧同一段逻辑已有 `NewRepoRelativizer`），但会改变 CLI 已发货的子目录 `-diff` 输出，需要用户先定口径：**子目录跑是该退化（保守全保留）还是该报错（拒绝一个当前无法证明的过滤）**。本轮已把该口径写进 `check_path` 工具描述（`tools.go:54`），但只限于告知，不算修完 |
| MCP `check_path` 没有 `-base` 入参 | ⬜ 合同明令本轮不扩 | base 硬编码 HEAD；agent 想对 `origin/main` 收紧只能走 CLI |
| 一次性取基线要跑第二遍引擎（`Snapshot` 里 `eng.Run(dir)`） | ⬜ 性能 | 与 D4/S1 同族：现在两侧共用一处，改进一次两侧同时受益，正是本轮把它抽出来的原因之一 |
| `cmd/metricgate-mcp/parity_diff_test.go` 有 1 条 `npath-complexity of "assertSameGate" is 216`（阈 200）warning | ⬜ | 断言密集函数的固有形状；错误分支本身就是路径数。已用 `check_code` 确认 0 errors |
| `internal/baseline` 无自己的单测（`go test ./internal/baseline/...` → no test files） | ⬜ | 本轮由两个入口的测试间接覆盖；`Snapshot` 的 nil-map 契约值得一条直测 |
| `.metricgate/history.db` 2.3 GB 无保留策略（D4） | ⬜ 另案 | 与本轮无依赖 |
| 非 git 目录下 `check` 静默降级到内置默认阈值（§八 坑 1） | ⬜ 新发现 | `internal/configpath/configpath.go:98` 的 `if dir == stop { break }` 在 `stop == start` 时连当前目录都不试；修法是搜索至少试完 `start` 自己（或 `stop` 算不出时不设界），但会影响所有命令的策略发现口径，需单独一轮 |
