# 迭代 19 · 第一部分：文档信任债与 `version` 真相（S3）+ 定位叙事改写（D6）

日期：2026-10-16（goal mvaclm26-g4opuu task-1/2/3；排期见
[产品演进方向调研 §7](../research/product-evolution-directions-2026-10.md) iter-19 行）

范围说明：iter-19 原本还含 S1（`-diff` 批量化）与 D4（history.db 保留策略），**本轮未做**，
仍挂在 backlog 上。本轮做的是其中「S3 文档漂移 + `toolVersion` 真相」，
外加 §4.6/§5 的落地改写（调研里编号 D6，原排期在 iter-23+，因为它是 S3 主张唯一缺的证据，
提前到同一批）。

---

## 为什么做这个

一个卖「确定性」的工具，自己的 README 数字对不上、对外说话的产品挂了一个连不上的域名。
这两件事都不影响判定结果，但影响**别人愿不愿意相信判定结果**——是 §0 主张 S3 里最便宜的信任债。

`docs/research/product-evolution-directions-2026-10.md` 的 §2.2 列了 4 处数字漂移、
§3.3 列了 `toolVersion` 硬编码 × `DefaultFeedURL` 死域名，而 §0 的 S3 主张（「市场空白」论断过期）
一直缺落地证据——§4.6 已把五家同格竞品的一级来源取回，却没有进任何对外文档。

---

## 一、§2.2 的 4 处数字漂移（task-1）

先定口径，再改数：**口径必须是使用者能自己复现的数**，不是源码内部结构的数。

| 位置 | 原记载 | 改后 | 口径来源 |
|---|---|---|---|
| README.md:69 | "19 CLI subcommands" | "28 CLI subcommands" | `go run ./cmd/metricgate --help` 去重后的动词数 |
| docs/capabilities.md:261 | "20 个子命令" | "28 个子命令" | 同上 |
| docs/capabilities.md:295 | "11 个工具" | "12 个工具" | `grep -cE 'Name:\s+"[a-z_]+"' cmd/metricgate-mcp/tools.go` |
| README.md:500（v2 行） | "HTTP/gRPC service (SDK)" | "Remote HTTP/gRPC service (SDK)" | 与已发货的 `cockpit serve`（loopback HTTP）不再自相矛盾 |

### 踩到的坑：28 还是 29

`cmd/metricgate/main.go` 的 `var commands` 是 **29 键**，`--help` 去重后是 **28 个动词**。
差的那个是 `rules-gaps`：它是 command map 里一个 **help 未列出的连字符别名**
（`metricgate rules-gaps .` 与 `metricgate rules gaps .` 都能跑通，实测 exit 0，但 help 只列了后一种）。
另外 6 个动词（api/arch/check/cockpit/reachability/rules）因为多形态各有第二行，按行计数会得到 37。

**决定：对外报 28（help 口径）**，理由是用户打开 `--help` 能自己数；报 29 会让人对着 help 数不出 29，
反而制造新的「数字对不上」。调研文档 §2.2 里先前写的「29 键（差异来自同一动词多形态）」已就地核正为
上面这句完整口径，避免下一轮再猜一次。

### 补漏：10.1 的标题与表自洽（复核后追加）

首轮只修了「与 help 直接冲突」的那个数，遗留节里记着「§10.1 表只列 22 个动词」。审计复核时确认
**表内漏了 6 个动词**（`accept`、`cockpit`、`deps`、`rank`、`reachability`、`suppressions`），
还有一条把 `stats`/`metrics`/`trends`/`clonedrift`/`nearclones` 捏在一起的合并行（这些动词各有自己的行，
捏行只会让人数不清）。已补：6 行新条目（作用与 flag 从 `<verb> -h` 实跑得到，不靠猜），删掉合并行，
标题下的 28 现在与表逐行对得上。

复核口径写进了 10.1 结尾，但那条命令有两个坑（都是这轮踩出来的）：

1. **`metricgate --help` 写往 stderr**，直接管道给 grep 会得到 0，必须 `2>&1`。
2. 计数用 `^ {2}metricgate [a-z-]+` 抽动词后 `sort -u`；不去重会因多形态行（api/arch/check/cockpit/reachability/rules
   各有第二行）得到 37。
3. **取数要从当前源码来**：PATH 上的 `metricgate`（本机 `/Users/mac/go/bin/metricgate`，2026-10-14 构建）只有 **27** 个动词，
   当前树是 28——拿它复核会得到「文档又飘了」的假结论。计数一律 `go run ./cmd/metricgate --help`。
   这正是 docs/design/cockpit-single-file-tech.md:380 记过的旧坑（`go build ./...` 多包时不写输出，仓根与 PATH 的二进制会陈旧）。

现成命令（实测 28，与表内 28 个动词（32 行）、help 去重 28 三方一致）：

```sh
gofmt -l cmd/metricgate-mcp/main.go   # 空 = 格式合规
go run ./cmd/metricgate --help 2>&1 | grep -oE '^ {2}metricgate [a-z-]+' | awk '{print $2}' | sort -u | wc -l
```

---

## 二、`toolVersion` 真相 + update check（task-2）

§3.3 给的修法是个二选一（ldflags 注入 **或** 标未启用）。实测两个都要做，因为它们是两条独立的假话：
`toolVersion` 硬编码让每个本地构建谎称自己是 0.1.0；`DefaultFeedURL` 让每个用户以为存在发布 feed。

### 版本可注入

`cmd/metricgate/main.go:29`、`cmd/metricgate-mcp/main.go:34` 的 `const toolVersion = "0.1.0"`
改为 `var`（默认值不动，本地构建行为不变）。

```bash
go build -ldflags "-X main.toolVersion=9.9.9-test" -o /tmp/mg ./cmd/metricgate
/tmp/mg version        # -> metricgate 9.9.9-test 5c1595c79c62+dirty
go tool nm /tmp/mg | grep toolVersion   # -> D main.toolVersion
```

**坑**：符号名是 `main.toolVersion`，**不是** `metricgate/cmd/metricgate.toolVersion`。
包名叫 `main`（目录是 cmd/metricgate 不影响）。用包路径限定名 `-X` 时 go **静默忽略**、
构建照样成功、版本照样打印 0.1.0——这个失败模式没有任何报错。

这个坑本轮真的被自己踩了一次，记在这里当反例：首版在 `cmd/metricgate-mcp/main.go` 的注释里写了
**包路径限定**的注入配方（`-X metricgate/cmd/metricgate-mcp.toolVersion=v1.5.0`），而同一文件族里的
`cmd/metricgate/main.go` 注释写的是正确拼法——**两份注释互相矛盾，错的那份正在对外发货**。
审计独立复现后拦住：按 MCP 那条命令构建，serverInfo.version 仍是 `0.1.0`，
恰好就是 task-2 要杀掉的那句假话。已改为两处统一 `-X main.toolVersion=`，并把这一步变成可执行断言：

`cmd/metricgate-mcp/version_test.go`（新增）不在源码里找字符串，而是**真的构建两次二进制、真的发一次
stdio initialize**，断言注入值确实到达 `serverInfo.version`；另一个用例断言包路径限定写法**仍然是无效的**
（锁住这个陷阱本身，避免下一轮又把它写回注释——将来 Go 改成对未知 `-X` 报错时，这个测试会红着提醒我们改文档）。
两个用例各要一次 `go build`，所以 `-short` 下跳过，正常 `go test` 才跑。

```bash
go test ./cmd/metricgate-mcp/ -short            # 5.1s，跳过编译型用例
go test ./cmd/metricgate-mcp/ -run Version      # 18.1s，真构建 + 真握手
```

`buildStamp()`（main.go:97）打印 VCS revision + `+dirty`，是「我跑的是刚构建的二进制吗」的真答案；
本轮没改它，只把注释里「hand-written constant」的措辞跟着 `var` 改掉。

### update check：不发一个连不上的请求

`internal/cockpit/cockpit.go` 删除 `const DefaultFeedURL = "https://releases.metricgate.dev/latest.json"`
（全仓只有 `probe()` 一处引用）。新增状态：

```go
StateNotEnabled = "not_enabled"   // no release feed configured; nothing probed
```

`Checker.probe()` 在 `FeedURL == ""` 时返回
`Status{State: StateNotEnabled, Error: "no release feed configured; pass -feed URL"}`，
**且不构造任何请求**。`cmd/metricgate/cockpit_cmd.go` 的 `-feed` 默认值改为
`os.Getenv("METRICGATE_FEED_URL")`（部署侧仍可配置，命令行优先级不变），
`printUpdateStatus` 新增分支打印到 **stdout、exit 0**：

```
$ metricgate cockpit update
update check is not enabled: no release feed configured; pass -feed URL

$ metricgate cockpit update -format json
{ "state": "not_enabled", "current": "0.1.0", "error": "no release feed configured; pass -feed URL", ... }
```

为什么不是 error：没发布 feed 是**发行决策**，不是网络故障。旧行为拿 DNS 失败填 `StateError`，
把「产品指向一个不存在的主机」伪装成「你的网络暂时有问题」——正是 §3.3 要避免的那件事。
`-feed` 给了真地址时，原有的 available / not_available / error 三态和 `sanitizeErr` 行为完全不变。

### 契约测试

`internal/cockpit/cockpit_test.go` 新增 `TestCheckNotEnabled`：`HTTPClient` 用「任何请求都返回 error」的
transport，空 `FeedURL` 仍必须得到 `not_enabled` —— 这样测试锁住的是「不触网」而不仅是状态字符串。
同时锁 `Error` 非空（要说清怎么开启）。`go test ./internal/cockpit/` 66 passed。

---

## 三、D6：定位叙事从「空白」改到「三轴事实 + 规则归属」（task-3）

`docs/strategy/positioning.md` 升到 **v2.0（2026-10-16）**。改动不是润色，是把论证换一个不会过期的轴：

- **核心结论**：删掉「填补了以下市场空白」，换成**频率 × 成本 × 确定性**三轴表，
  每轴带一个可反证的口径（`check -diff` 墙钟时间 / 同一次检查的 token 与网络依赖 / 5 次运行 JSON 字节相同）。
  三轴是事实陈述，不随竞品功能表移动。
- **新增「🌐 2026-10 外部检索」一节**：五家同格竞品对照表（SonarQube Vortex、SonarQube Claude Code 插件、
  Verificate Gate、Vivari Guard、Trustabl Agent Analyzer），逐条带**原文关键句 + 一级来源 + 检索日期 + E4 级别**，
  由 §4.6 引入；并带三条改写出来的打法（不与 Sonar 打分析深度 / 不与 Verificate 打闸门权威 /
  不与 Vivari·Trustabl 打本地+0 token）和**取证边界**（GateTest、Kete/taniwha.ai 未取回一级来源，
  按 E4 规则不引用细节）。
- **真正的定位收敛到「规则归属」**：上表五家的规则集是**产品的**，不是**你的**——这是唯一没被逐条占住的一格
  （claims #5/#12）。
- **v1.0 的自我否证标注**（保留原文而不是删掉，让过期判断可审计）：
  「各司其职，互不重叠」→ 标注已被否证，重叠的是三轴、不重叠的是规则归属；
  详细对比矩阵加 ⚠️「未经 2026-10 逐格复核，不得作为『无人能做』的证据引用」；
  「市场空白点」小节改名「能力缺口盘点（注意：缺口 ≠ 无人竞争）」；
  共性缺陷表标注哪四行已被竞品自述直接竞争、哪三行仍然成立；
  短期机会里「竞争：较少」→「已有同格」、「几乎空白」→「仍空着（五家规则集都是产品的）」。
- **新增「🚫 明确不做（取舍与反证）」**：六行（autofix/codemod、自建类型系统与数据流分析、远程服务化、
  语义级 AI review、调阈值降噪、改 hook 语义/权限模型），每行带理由与**重开条件**。
  定位文档只讲做什么不够——否则每格新能力都会被竞品一句自述抵消掉。
- **市场定位结论**重写，`填补市场空白` 不再出现在任何论断位置（只在 ⚠️ 变更说明里作为被替换的旧论断引用）。

三处 `../research/product-evolution-directions-2026-10.md` 内链已校验存在。

### 顺手扫掉的同源过期断言（否则 S3 只修了一半）

`grep -rn "填补市场空白"` 发现定位段不是唯一会说这句话的地方：

| 文档 | 处置 |
|---|---|
| docs/strategy/competitor-analysis.md:314 | §4「市场机会」加 ⚠️ 取代声明（列出五家 + 指向 positioning.md v2.0），尾部版本改「v1.0（已被取代）」 |
| docs/strategy/competitor-research-summary.md:310 | 同上（它本身自述是 competitor-analysis.md 的「另一版归纳」） |
| docs/README.md:34-38 | 索引行标注哪份是现行口径、哪两份仅存档 |
| WEEK1_COMPLETE.md:85 | **不改**：根目录 week-1 工作记录，未被任何索引引用，本身就是在记录当时的认知 |

取舍：这两份 strategy 文档只加**取代声明 + 指向 v2.0**，不重写全文——
它们的价值在历史判断存档，重写反而抹掉「我们什么时候错在哪里」这条审计链。

---

## 审计返工（第一次 completion 被驳回）

独立审计接受了 task-1 / task-3（并独立抓取了 5 个竞品源页逐一核对引句），只在 task-2 拦下一条：

| 审计点 | 处置 |
|---|---|
| **阻断**：`cmd/metricgate-mcp/main.go:34-37` 的注释在对外发货一条无效注入配方（包路径限定 `-X`），与 `cmd/metricgate/main.go` 的正确注释自相矛盾，且按它构建后面版本仍是 0.1.0 | 改为统一 `-X main.toolVersion=`，并新增 `cmd/metricgate-mcp/version_test.go` 真构建 + 真握手把注入路径锁成断言（详见第二节） |
| `capabilities.md` §10.1 标题说 28 、表只列 22 个动词 | 补 6 行（accept/cockpit/deps/rank/reachability/suppressions，作用与 flag 从 `-h` 实跑取值），删掉 `stats`/`metrics`/`trends`/`clonedrift`/`nearclones` 合并行；现在表内 28 个动词（32 行：constraints 四子项、trends 两口径各占一行），与标题、help 去重均为 28 |
| 「go vet 无问题」说法不精确 | 验证口径里改写为**定向 vet**，并注明全树 `go vet ./...` 在 `internal/store/metritrend.go:86:10` 报 `append with no values`（5c1595c 带入，本轮未碰）。→ 该问题已于 2026-10-16 迭代 20 修掉，全树 vet 现已 exit 0，限定语可删 |
| 竞品表 `github.com/trustabl/trustabl` 已 301 | 改为 `trustabl/agent-reliability-analyzer` 并标注改名日期 |
| `.metricgate/history.db` 2.32 GB 无保留策略 | 既有 D4/S1 项，非本轮引入，见遗留表 |

---

## 验证口径（E1，全部实跑）

```bash
# 1. 数字漂移：口径自己算（去重后 28 个 help 列出的动词；command map 29 键，多的那个是 help 未列的 rules-gaps 别名）
# help 走 stderr，必须 2>&1（漏了会得到 0 这个假数）；用 go run 取当前源码，裸 metricgate
# 会拿到 PATH 上 2026-10-14 的旧构建（只有 27 个动词，会得出「对不上」的假结论）
go run ./cmd/metricgate --help 2>&1 | grep -oE '^  metricgate [a-z-]+' | awk '{print $2}' | sort -u | wc -l   # 28
grep -cE 'Name:\s+"[a-z_]+"' cmd/metricgate-mcp/tools.go                                      # 12
# 2. 版本注入（两条命令、两个面）
go build -ldflags "-X main.toolVersion=9.9.9-test" -o /tmp/mg ./cmd/metricgate && /tmp/mg version
go test ./cmd/metricgate-mcp/ -run 'TestToolVersion|TestPackageQualified' -count=1   # 18s：真构建 + 真握手，
# 断言 serverInfo.version == 9.9.9-test（正）且包路径限定写法注入不生效（反，锁住陷阱本身）
# 包路径限定写法 -X metricgate/cmd/metricgate-mcp.toolVersion=PKGFORM 实测被静默忽略，仍报 0.1.0
# 3. update check 不再指向死域名（且默认构建无 DNS 查询）
/tmp/mg cockpit update; echo "exit=$?"          # update check is not enabled…, exit=0
grep -rn "releases.metricgate.dev" --include=*.go .   # 无结果
# 4. 闸门
bash scripts/cockpit-observe.sh quick            # node --check + render_all + web 契约测试
go build ./... && go vet ./cmd/... ./internal/cockpit/   # 本轮当时的定向口径：全树 go vet ./... 会在
# internal/store/metritrend.go:86:10 (append with no values) 报错，那是 5c1595c 带进来的既有问题，
# 本轮未碰该文件——不要把「定向 vet 干净」说成「全仓 vet 干净」
# 【已作废】该一行已在迭代 20 修完，现在直接 `go vet ./...`（exit 0，15:53:22–24 实测）；
# 保留上面那行是为了说明当时为何多说一句限定语
go test ./internal/cockpit/                      # 含新增的 TestCheckNotEnabled
# 注：用例**总数**随工作树浮动（本轮上午 67 → 收口时 72），增加的是并发写入者新加的
gate_api_test.go / issues_api_test.go，不是本轮引入。报“N passed”要同时报取数时刻，
# 否则下一轮对不上数又会当成一次漂移。
# 5. 同源断言不再出现在对外口径里（只剩历史存档与带 ⚠️ 取代声明的位置）
grep -rn "填补市场空白" --include=*.md . | grep -v ".pi/goals"
```

`grep -rn "releases.metricgate.dev"` 现在只剩调研文档 §3.3 里作为**被修对象的历史记载**，代码中零引用。

### 闸门现状（check -diff 口径，E1）

`metricgate check -diff .`：6 errors / 104 warnings。**6 个 error 全部不来自本轮**（已逐个对基线）：
registry_parity_test.go 与 third_party/tree-sitter-vb-dotnet/src/parser.c 本轮未改（前者是 untracked
新文件），internal/rules/chain.go 是前序会话遗留的 modified 文件。

本轮改到的 6 个代码/测试文件单独跑 `check_path` 全部 **PASS（0 error）**：
cockpit.go 3 warnings、cockpit_cmd.go 6 warnings、main.go 16、metricgate-mcp/main.go 3、
cockpit_test.go 10、**version_test.go 0**。**新增的都是 warning，没有新增 error**；
pre-commit hook 的 error 口径不会拦住提交。

新增部分全在既有噪声形状里（cmd/ 下共 184 处 `fmt.Print*`，所以上面那 4 条 banned-fmt warning
跟周围一致）。一个没被本轮放大的既有事实：`Checker.probe` 的 npath-complexity 为 **768**。
把 HEAD 版本以**仓内临时副本**重跑 `check` 得到完全相同的三条 finding（cyclomatic 11 / fan-out 11 /
npath 768，均为 warning）——本轮删掉 DefaultFeedURL 分支后**指标一项没变**，属于待重构项（见下节遗留）。
（先前的对照误用：把基线拷到 /tmp 跑时同一值会报成 error 严重度——policy 解析跟路径上下文有关，
基线对比必须放在仓内同目录做，否则结论失真。）

---

## 遗留

| 项 | 状态 |
|---|---|
| positioning.md 详细对比矩阵逐格复核（「实时反馈」「LLM 友好」两格已被竞品自述竞争） | ⬜ 本轮只加了 ⚠️ 边界标注 |
| `Checker.probe` 重构（npath 768 已在阈值上方 3.8 倍，本轮只拆出 not_enabled 早返回、未动其余分支） | ⬜ 基线同值，不是本轮引入 |
| capabilities.md §10.1 表格补全 6 个缺失动词（accept/cockpit/deps/rank/reachability/suppressions） | ✅ 已补；表内 28 个动词（32 行）与标题、help 去重均为 28 |
| 发布流程真正接上 `-ldflags -X main.toolVersion=`（CI 构建脚本 / release workflow） | ⬜ 本轮只保证「能注入且有测试口径」（新增 version_test.go 捏住注入拼法）；三个 workflow 目前都是 `go build -o metricgate ./cmd/metricgate`，不带 -X——发布时需补 |
| 真发布 feed 上线（届时 `-feed` / `$METRICGATE_FEED_URL` 立即恢复三态探测） | ⬜ 不在工程范围 |
| GateTest、Kete/taniwha.ai 的一级来源取证 | ⬜ 下一轮检索第一批目标 |
| 全树 `go vet ./...` 在 `internal/store/metritrend.go:86:10` 报 `append with no values`（`args := append(append([]any{}, filterArgs...))`，内层第二个 append 没传值） | ✅ 已修（2026-10-16 迭代 20）→ [iter-20 §六](iter-20-mcp-cli-ratchet-parity.md)：一行改成 `append([]any{}, filterArgs...)`，全树 `go vet ./...` exit 0（实测 15:53:22–24）。“定向 vet”这个限定语已从验收口径里删除 |
| 本轮收口时工作树有**并发写入者**（同一仓另一个 pi 会话，mtime 实测：`internal/cockpit/scope.go` 12:13、`scope_test.go` 12:15/12:18、`issues_api*.go` 12:07/12:14） | ⚠️ 影响复核口径：`go vet ./internal/cockpit/` 在 12:14–12:18 之间报过 `issues_api_test.go:47 not enough arguments`、`scope_test.go undefined: fmt/store.RunSummary`，随后自行修复——**那些都不是本轮改动**（本轮只碰 cockpit.go / cockpit_test.go）。同一文件两次读数不同不是幻觉，必须按 mtime 归因后再下结论 |
| PATH / 仓根的二进制陈旧（`/Users/mac/go/bin/metricgate` 2026-10-14 构建、只有 27 个动词） | ⚠️ 文档里的**可复核性**取决于取数入口，所有计数口径已改成 `go run ./cmd/metricgate` |
