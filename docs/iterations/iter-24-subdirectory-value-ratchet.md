# 迭代 24 · 子目录 `-diff` 的值棘轮口径（value-delta key space）

日期：2026-10-16

编号说明：本文占用 `iter-24`，因为调研文档 §7 排期表里的 `iter-21` / `iter-22` / `iter-23` 三个序号已被**仍未开工**的工作占着（`iter-21` = D1 标识符规则族、`iter-22` = `policy show` 动词、`iter-23+` = D3 blast-radius / D5 bundle 降级 / D6 复核），本文对应的那一行在表里叫「**iter-22 顺带（新）**」。
这是本仓**已有的惯例而非新例**：`iter-20-mcp-cli-ratchet-parity.md` 同样声明了「文件号 ≠ 调研表里的同名计划行」。
见 [产品演进方向调研 §7](../research/product-evolution-directions-2026-10.md)。

上游：`docs/research/product-evolution-directions-2026-10.md` §7「iter-22 顺带（新）」行；
`docs/iterations/iter-20-mcp-cli-ratchet-parity.md` §五 与 §九 遗留第 1 条（本轮首选）
前置：iter-20（MCP `-diff_only` 棘轮对齐）已收口

## §0 一句话目标

**让「这条 finding 的度量值动了吗」这个问题在子目录下也有答案**——现在它静默地回答「没动」。

这是 iter-20 顺出来的既有缺陷：同一份改动，在仓库根跑 `check . -diff` 与在子目录跑
`check basepkg -diff`，`unchanged` 计数一个为 N、一个为 0。**两个 root 给出不同答案，
而界面上看不出来哪个是「没测」哪个是「测了没变」。**

## §1 病灶：两个键空间只在 `root == 工作树根` 时重合

链路拆开看（全部为 E2 代码定位）：

| 环节 | 键空间 | 定位 |
|---|---|---|
| `engine.Run(root)` 写 finding 的 `file` | **相对 run 根** | `internal/engine/filter.go:30 FilterSummary` 消费 |
| `Snapshot` 从归档目录跑第二遍引擎 | **相对工作树根** | `internal/baseline/baseline.go:89 Snapshot` → `engine.IndexBaseValues`（`internal/engine/filter.go:52`） |
| 关联判定 | 字符串相等 | `internal/engine/filter.go:175 valueMoved(f, base, spans)` |

`Snapshot` 内部：

```go
func Snapshot(cfg *config.Config, root, base string, changed map[string][]diff.Range) (engine.BaseValues, error) {
	repoPaths, err := repoRelativePaths(root, changed)   // ← 已归一到 repo 相对
	...
	dir, cleanup, err := git.ArchivePaths("", git.ResolveBase("", base), repoPaths)
	...
	summary, err := eng.Run(dir)                          // ← 这里跑出来的 file 是相对 dir
	return engine.IndexBaseValues(summary.Metrics), nil
}
```

`dir` 是归档出来的**整树临时目录**，所以 `eng.Run(dir)` 的 finding/file 键是相对工作树根；
而 `check basepkg -diff` 时当前运行的 finding 键是相对 `basepkg`。⇒ `valueMoved` 的
span/base 关联必然 miss，`unchanged` 恒为 0，**value-delta 那一半静默退化为全保留**。

**已证明不是本轮引入**：iter-20 §五 用 HEAD 版二进制（`/tmp/mg-d2-before`）与当前二进制在同一
探针对跑，两种 root 的 diff-only 摘要逐字节相同。CLI 子目录 `-diff` 从发货起就是这个行为。

**已发货的口径告知**（不算修完）：`cmd/metricgate-mcp/tools.go:54` 的 `check_path` 描述里已写明
"Guarantee holds for path at the worktree root: under a subdirectory … nothing is reported as unchanged"。

> **勘察订正（2026-10-16）**：§1 描述的是症状，实测下来它是**两个**缺陷，第二个更危险
> （子目录 `-regress` 报 `regressions: 0` 假阴性）。完整拆分见 **§3.1**。

## §2 先定口径（这一步是阻塞项，不能跳）

修法本身是已知的（基线键归一到 run 根，`internal/git/git.go:130 NewRepoRelativizer` 已在
`internal/baseline/baseline.go:166` 用过一次），但**它会改变 CLI 已发货的子目录输出**。
两个选项都说得通，必须由人定：

| 口径 | 含义 | 代价 |
|---|---|---|
| **A. 退化**（保守全保留） | 子目录跑时明确声明「值棘轮在子目录下不可用」，输出里带一行 `value-ratchet: unavailable under subdirectory root` | 不改变现有判定结果，只把沉默变成明说；用户拿不到值棘轮 |
| **B. 报错** | 子目录 + `-diff` 直接拒绝：「无法证明的过滤不做」 | 最诚实；但会让现有 `check basepkg -diff` 脚本从「能跑」变成「报错」，属破坏性变更 |
| **C. 修正键空间** | `Snapshot` 的键归一到 run 根，子目录下值棘轮真的生效 | 输出变了（`unchanged` 从 0 变成真实值），与 A/B 都不同 |

**推荐 C**（它才是修好，A/B 只是把问题说清楚），但 C 需要接受「已发货输出变化」。
若评审不接受输出变化，退到 A（把沉默变成明说，零判定变更）。

> 阻塞点：本迭代的第一个 commit 必须是「口径决策」，写进本文 §2 并落成测试；
> 在此之前不要动 `Snapshot`。

### §2.1 决策：选 C（2026-10-16）

**裁定：C。** 理由不是「C 更彻底」这种空话，而是实测出来的三条：

1. **真仓上子目录 `-regress` 已经在对用户说反话**（本轮勘察新发现，见 §3.1 缺陷 2）。
   同一棵脏树、同一条命令，只换 root：
   ```
   $ metricgate check .             -diff -regress  →  regressions: 1 measurement(s) got worse than HEAD
   $ metricgate check internal/git  -diff -regress  →  regressions: 0 measurement(s) got worse than HEAD
   ```
   「0 处变差」不是保守，是**假阴性**：它让一个子目录的 CI 门禁对真实退化放行。A（只加一行
   `unavailable` 声明）能解释 `unchanged=0`，但**解释不了 `regressions: 0`** —— 那行声明不在
   regress 的渲染路径上，用户看到的仍然是一个干净的数字。B（报错）能拦住它，代价是把所有
   `check <sub> -diff` 脚本变成硬失败。
2. **C 的收益不止「`unchanged` 不再是 0」**，而是让 `-regress` 也拿到可比较的键空间。A/B 一个都修不到。
3. **D21-2 把 C 的破坏面压到零**：`root == 工作树根` 时输出逐字节不变是硬保证（已用 `cmp` 钉住），
   所以「破坏性变更」只落在本来就在骗人的那条路径上。

**A/B 不采纳的代价**：留一个会静默放行真实退化的子目录门禁。这比「已发货输出变化」严重。

**夹具实测（口径 C 的目标行为，`/tmp/iter24-fix`）**：
```
修前  root:  diff-only: base=HEAD, 1 new findings, 2 pre-existing hidden, 3 unchanged metric(s) dropped
修前  sub :  diff-only: base=HEAD, 4 new findings, 2 pre-existing hidden            ← unchanged 子句整段消失（即静默 0）
修后  root:  diff-only: base=HEAD, 1 new findings, 2 pre-existing hidden, 3 unchanged metric(s) dropped   ← 与修前逐字节相同
修后  sub :  diff-only: base=HEAD, 1 new findings, 2 pre-existing hidden, 3 unchanged metric(s) dropped   ← 与根下一致
```
修前子目录那行不只是 `unchanged=0`，而是**连 `unchanged metric(s) dropped` 子句都不渲染**（计数为 0 时
渲染器省略该子句），所以用户看到的是一份「什么都没被过滤」的报告，与根下差 3 个 finding 而无任何解释。

**缺陷 2 的夹具实测（`/tmp/iter24-regress`，`basepkg/c.go` 把嵌套 if 从 1 层加到 5 层）**：
```
修前  root:  regressions: 5 measurement(s) got worse than HEAD
修前  sub :  regressions: 0 measurement(s) got worse than HEAD      ← 假阴性
修后  root:  regressions: 5 measurement(s) got worse than HEAD      ← 与修前逐字节相同
修后  sub :  regressions: 5 measurement(s) got worse than HEAD
```

**D21-2 的实测**：`/tmp/iter24-fix` 与 `/tmp/iter24-regress` 上各跑多轮，修前/修后二进制输出
（把 `metricgate 0.1.0 — N files, Xms` 的耗时数字归一化后）`cmp` 全部 identical。
注意耗时数字每次都变（实测同一二进制三轮 5ms/3ms/2ms），不归一化则 `cmp` 必报
`differ: char 123, line 3` —— 那不是回归。

## §3 选 C 的改动契约（已按实测订正）

### §3.1 病灶其实有两处，不是一处

§1 描述的是**症状**（`unchanged` 恒 0），实测下来它是**两个独立缺陷**，都源自
「`changed` 在 root 键空间，而归档跑出来的基线在**工作树根**键空间」：

| # | 缺陷 | 错位 | 症状 |
|---|---|---|---|
| **1** | 值棘轮（`valueMoved`） | `Snapshot` 的 `IndexBaseValues` 键 = 工作树根相对（`:55 filepath.ToSlash(m.File)`），当前 run 的 finding/span 键 = run 根相对 | `unchanged` 恒 0 |
| **2** | `-regress` | `applyRegressions` 里 `current := RelativizeMetrics(summary.Metrics, root)` 是 root 相对，而 `FullSnapshot` 的基线是工作树根相对；`regress.Compare` 按 `key{File,Scope,Metric}` **字符串相等**配对（`internal/regress/regress.go:55`）| 两侧全不配对 → `regress.FilterToChanged` 无事可做 → **`regressions: 0` 假阴性** |

**缺陷 2 更危险**：`unchanged=0` 顶多是「该丢的没丢」，`regressions: 0` 是**对真实退化报告「没有」**。

根因是一行 `SanitizePath` 语义：`SanitizePath(root, path)` 对**相对**路径是 no-op（`internal/selection/selection.go:146`，只做 normalize），
所以 `RelativizeMetrics(summary.Metrics, root)` 在 root=`basepkg` 时得到的是 `a.go`，而基线给的是 `basepkg/a.go`。
根下两者恰好都是 `a.go`，于是错位被掩盖。

### §3.2 改动契约（两处缺陷同一个修法）

把**基线产物**的键从「工作树根相对」映射到「run 根相对」，与当前 run 对齐。
不要反过来去动 `changed` —— 它已经是 root 相对（`git.ChangedLinesFor(root, base)`），动它会制造对称的
第二个 bug（根下恒 0）。

```go
// internal/git：新增「工作树根相对 → check 根相对」方向，复用既有 rebaseOne（:174），不新写路径数学。
// 现成件 RepoRelativizer.Rel（:142）是反方向（check 根 → 工作树根），rebaseOne 就是它的对偶。
func (r RepoRelativizer) Unrel(path string) (string, bool)
```

| 环节 | 定位 | 改法 |
|---|---|---|
| 值棘轮 | `internal/baseline/baseline.go:89 Snapshot` | `eng.Run(dir)` 之后、`IndexBaseValues` 之前，把 `summary.Metrics` 的 `File` 逐个 `Unrel` 成 root 相对；`repoRelativePaths`（:165）**不动**，它的语义是对的 |
| `-regress` 基线 | `internal/baseline/baseline.go:125 FullSnapshot` | 增加 `root` 参数，同样在返回前 `Unrel`（唯一调用点是 `cmd/metricgate/regress_cmd.go:25`，那里有 `root`）|
| `-diff` 附加项的 `changed` | `cmd/metricgate/check_cmd.go:170` | **不改**（已 root 相对，与 `summary` 同键空间）|
| MCP `check_path -diff_only` | `cmd/metricgate-mcp/checks.go:87` | **不改**，走同一个 `baseline.Split` |

`Unrel` 在 `root == 工作树根` 时是恒等映射（`checkBase == repoRoot` ⇒ `filepath.Rel(repoRoot, repoRoot+"/"+rel) == rel`），
这正是 D21-2 能成立的机制。

**不要做的事**：不要把 `ArchivePaths` 的 `dir` 参数从 `""` 改成 `root`。那会把基线键从工作树根换成 root，
症状从「子目录恒 0」翻转成「根下恒 0」，正好把唯一被保证的口径打碎。

## §4 会被打翻的断言（已按最终落地状态结清）

| 断言 | 位置 | 处理结果 |
|---|---|---|
| 子目录 `unchanged=0` | `cmd/metricgate-mcp/parity_diff_test.go`（原 `if root == "." && cli.Unchanged == 0`）| **已改**：去掉 `root == "."` 门槛，对所有 root 都断言 `cli.Unchanged != 0`。原注释把退化写成「documented degradation」的那一段整段改写为「两种 shape 都必须打满棘轮」——否则测试自己会说谎 |
| 子目录 `regressions: 0` | 无断言（**这就是它至今没被发现的原因**）| **已补**：`internal/baseline/baseline_test.go` 的 `TestFullSnapshotKeysJoinTheGatedRunUnderASubdirectory`。它断言的不是计数（计数还依赖夹具真的退化），而是**两侧键空间必须能 join**：join 为 0 即无条件缺陷。见 §9 的反向验证 |
| `check_path` 工具描述里的 "nothing is reported as unchanged" | `cmd/metricgate-mcp/tools.go:54` | **已改写**为「The base values are keyed in the same coordinate system as the run, so the value test holds at the worktree root and under a subdirectory alike.」 |
| 两入口一致性 | `cmd/metricgate-mcp/parity_diff_test.go` | **通过**（CLI 与 MCP 都变，一致性断言仍成立，这正是它的价值）|

## §4.1 本轮自己踩到的门禁（诚实记录）

新增的 `internal/baseline/baseline_test.go` 第一版**让 `check internal/ -diff` 从 0 error 变成 1 error**：

```
baseline_test.go:213: [error] cognitive-complexity (cognitive-complexity of
  "TestFullSnapshotKeysJoinTheGatedRunUnderASubdirectory" is 30 (threshold error 25))
```

外加 8 条 warning（function-length / nesting-depth / cyclomatic-complexity / npath-complexity，
分布在 `TestSnapshotKeysTheBaseRunInTheCheckRootsOwnSpace` 与上述新测上）。

这直接违反 N-2。**修法是拆函数，不是加 ignore**：把断言抽成 `assertKeySpace`、把「取两侧样本」
抽成 `fullSnapshotAndRun`、把键集合与配对抽成 `metricKeySet` / `countJoined`；`git_test.go` 同样
拆成 `assertUnrelIsIdentityAtRoot` / `assertRelUnrelRoundTrips`（原单函数 cyclomatic 13、npath 896）。
拆完 `check internal/ -diff` = **0 errors, 0 warnings, 0 notes**，与 pristine 完全一致。

教训：**度量工具自己的迭代必须用自己的门禁量自己**。这份测试是给棘轮加棘轮的，它自己先触发了棘轮。

## §5 本轮明确不做

- MCP `check_path` 的 `-base` 入参（iter-20 合同明令不扩，另案）。
- 「一次性取基线要跑第二遍引擎」的性能问题（与 D4 同族，见 iter-25 §5）。
- `internal/baseline` 自己的单测（iter-20 §九 遗留第 5 条）——但若选 C，`Snapshot` 的键空间
  契约值得**本轮补一条直测**，因为改动正是在这里。

**实际执行结果**：选 C 后确实补了，而且是 3 条（`internal/baseline/baseline_test.go`）：
键空间直测、子目录 join、`FullSnapshot` join。最后一条覆盖的是 §3.1 缺陷 2，
它在此之前**完全没有断言**，所以那条缺陷才能以「已修」的名义活到现在。

## §6 风险

| # | 风险 | 缓解 |
|---|---|---|
| R1 | 选 C 后子目录输出变化被下游脚本依赖 | **已处理**：子目录输出前后对比写入 §8，破坏性变更限定在「本来就在骗人」的那条路径上；根下逐字节不变由 D21-2 的 `cmp` 钉住 |
| R2 | 键空间换算引入新的 off-by-one / 路径拼接错误 | **已处理**：复用既有 `rebaseOne`（`Unrel` 是 `Rel` 的对偶），未手写 `filepath.Join`；越界路径返回 `false` 且条目被丢弃（不猜路径），由 `TestRepoRelativizerUnrelInvertsRel` 钉住 |
| R3 | 非 git 目录下 `Snapshot` 已提前返回 `nil` | **已保持**：`rebaseToCheckRoot` 在 `git.NewRepoRelativizer(root)` 失败时原样返回，early-return 未被绕过 |

## §7 验收判据

- **D21-1**：同一脏树、同一改动，`check . -diff` 与 `check basepkg -diff` 的 `unchanged`
  **不再是「一个有值一个为 0」**（iter-20 §九 的原话）。
- **D21-2**：`check . -diff` 在 `root == 工作树根` 时的输出**逐字节不变**（回归保护：
  这是唯一被保证的口径，修子目录不能碰它）。
- **D21-3**：CLI 与 MCP 两入口在同一子目录 root 上给出同一 finding 集合（`parity_diff_test.go` 通过）。
- **D21-4**（本轮新增，针对 §3.1 缺陷 2）：同一脏树、同一改动，子目录 `check <sub> -diff -regress`
  报出的 `regressions` 计数与根下**一致**（不再是 0）；且根下计数**逐字节不变**。
- **N-1**：`go build ./...` + `go test ./...` 全绿；`bash scripts/cockpit-observe.sh fast` 全绿。
- **N-2**：`check_path internal/ -diff` 0 error。

## §8 复现命令（已按实测定稿）

**重要前提：真仓当前不构成证据。** 在 `internal/git` 下确实没有「值上升」的指标，
所以根与子目录都报 0，两者恰好一致 —— 这掩盖了缺陷，而不是证明它不存在。
下面的受控夹具才是 D21-1 / D21-4 的证据。

```bash
# 无区分力的真仓探针（只用来确认「没回归」，不能用来证明修复）
./metricgate check .            -diff -regress | grep -E '^diff-only|regressions'
./metricgate check internal/git -diff -regress | grep -E '^diff-only|regressions'
# 两者都是 0/0：真仓在该目录无上升指标
```

**受控夹具 A（`/tmp/iter24-fix`，D21-1/D21-2 的证据）**：一个自带 `.metricgate.yaml` 的
独立 git 仓，`basepkg/a.go` 在 `A()` 的最深层把 `"deep"` 改成 `"deeper"`（行数与全部复杂度指标不变），
`basepkg/b.go` 不动。只有「值未上升」这一个理由能丢掉 a.go 的 metric finding，
所以 `unchanged` 的计数有真实区分力。

```bash
cd /tmp/iter24-fix
metricgate check .       -diff   # → 1 new findings, 2 pre-existing hidden, 3 unchanged metric(s) dropped
metricgate check basepkg -diff   # 修前：4 new findings, 2 pre-existing hidden（无 unchanged 子句）
                                 # 修后：1 new findings, 2 pre-existing hidden, 3 unchanged metric(s) dropped
```

**受控夹具 B（`/tmp/iter24-regress`，D21-4 的证据）**：同样独立，`basepkg/c.go` 把
`C(p1,p2)` 的嵌套 `if` 从 1 层加到 5 层，使复杂度**真的上升**。

```bash
cd /tmp/iter24-regress
metricgate check .       -diff -regress   # → regressions: 5 measurement(s) got worse than HEAD
metricgate check basepkg -diff -regress   # 修前：regressions: 0（假阴性）
                                          # 修后：regressions: 5
```

**D21-2（根下逐字节不变）**：两夹具上分别用修前/修后二进制跑 `check . -diff`，
输出需先把 `metricgate 0.1.0 — N files, Xms` 的耗时数字归一化（它每次都变，实测 5ms/3ms/2ms），
再 `cmp`：全部 identical。不归一化则 `cmp` 必报 `differ: char 123, line 3`，那不是回归。

**两入口对照**：

```bash
go test ./cmd/metricgate-mcp/ -run DiffOnly -v -count=1
# 明细输出需要绝对路径的 go：/opt/homebrew/Cellar/go/1.26.2/libexec/bin/go
# PATH 上的 go 被包了一层，-v 会被吞成一行汇总
```

**新补的单测**：

```bash
go test ./internal/baseline/ -run 'Snapshot|FullSnapshot' -v
go test ./internal/git/ -run Unrel -v
```

## §9 收口记录

**状态：已完成（2026-10-16）。** 口径 C 落地，两处缺陷同修，D21-1..D21-4 + N-1 + N-2 全部通过。

### 9.1 改动清单（7 个文件）

| 文件 | 改动 |
|---|---|
| `internal/git/git.go` | 新增 `func (r RepoRelativizer) Unrel(path string) (string, bool)`，即 `rebaseOne(path, r.repoRoot, r.checkBase)`；`Rel` 的逆向对偶，root==工作树根时恒等 |
| `internal/baseline/baseline.go` | 新增 `rebaseToCheckRoot(ms, root)`；`Snapshot` 经它归一；`FullSnapshot` 签名加 `root` 参数 |
| `internal/git/git_test.go` | 新增 `TestRepoRelativizerUnrelInvertsRel`（往返 + 根下恒等 + 越界 false），拆成两个 helper 以过门禁 |
| `internal/baseline/baseline_test.go`（新增）| 3 条测试：键空间直测、子目录 join、`FullSnapshot` join（缺陷 2 的首个断言）|
| `cmd/metricgate/regress_cmd.go` | 唯一调用点适配 `FullSnapshot(cfg, root, base)` |
| `cmd/metricgate-mcp/tools.go` | `check_path` 描述尾句按新口径改写 |
| `cmd/metricgate-mcp/parity_diff_test.go` | `assertNonVacuous` 去 `root == "."` 门槛；documented-degradation 注释改写 |

### 9.2 验收结果

| 判据 | 结果 | 证据 |
|---|---|---|
| **D21-1** | ✅ | `/tmp/iter24-fix`：修前子目录无 `unchanged` 子句（静默 0），修后两种 root 都是 `1 new findings, 2 pre-existing hidden, 3 unchanged metric(s) dropped` |
| **D21-2** | ✅ | 两夹具上修前/修后输出（耗时归一化后）`cmp` identical，多轮稳定 |
| **D21-3** | ✅ | `go test ./cmd/metricgate-mcp/ -run DiffOnly -v`：`root=.` PASS 4.83s、`root=basepkg` PASS 3.11s、`TestDiffOnlyDropsUnchangedMetrics` PASS 11.77s |
| **D21-4** | ✅ | `/tmp/iter24-regress`：修前根 5 / 子目录 **0（假阴性）**，修后两种 root 都是 `regressions: 5` |
| **N-1** | ✅ | `go build ./...` CLEAN；`go test ./...` 48 ok / 5 no-test / **0 FAIL**；`bash scripts/cockpit-observe.sh fast` → `summary: 0 errors, 16 warnings, 0 notes, 429 metric snapshots` + **`gate: PASSED (fail_on=error)`** |
| **N-2** | ✅ | `check internal/ -diff` → `0 errors, 0 warnings, 0 notes, 21487 metric snapshots` + `gate: PASSED (fail_on=error)`，与 pristine 逐项一致 |

### 9.3 反向验证（断言真的会报红）

要求是「编译通过 + 断言报红」，三次变异均满足：

| 变异 | build | 报红 |
|---|---|---|
| M1：去掉 `Snapshot` 的 `rebaseToCheckRoot` | 成功 | `TestSnapshotKeysTheBaseRunInTheCheckRootsOwnSpace/subdirectory`（`keys present: basepkg/a.go`）与 `TestSnapshotSubdirectoryKeysJoinTheRunBeingGated` |
| M2：把 `rebaseToCheckRoot` 内的 `Unrel` 换成 `Rel`（方向反转）| 成功 | 键变成 `basepkg/basepkg/a.go` |
| M3：去掉 `FullSnapshot` 的 remap | 成功 | `TestFullSnapshotKeysJoinTheGatedRunUnderASubdirectory/subdirectory`（base keys 全为 `basepkg/a.go\|…`，run keys 全为 `a.go\|…`）|

三次还原后 md5 与原始一致、复跑全绿。变异后 `internal/baseline/baseline.go` md5 仍为 `958e3a7b928715caf5a0b07e71ba98c2`。

### 9.4 诚实边界

- **真仓探针不构成证据**：`check internal/git -diff -regress` 修前 0、修后仍 0，因为该目录下确实没有上升的指标。
  这正是 §8 警告的「真仓此刻 root 与子目录都给 0」。D21-4 靠受控夹具 `/tmp/iter24-regress` 成立。
- **已发货输出变化**：只有子目录路径变（本来就在骗人），根下逐字节不变（D21-2 已钉）。
- **`repoRelativePaths` / `RelativizeMetrics` 未动**：它们对绝对路径的语义是对的，只对相对路径 no-op，
  所以修在「基线产物」侧而不是「当前 run」侧。
- **构建环境**：`go test -v` 的明细输出需要绝对路径 `/opt/homebrew/Cellar/go/1.26.2/libexec/bin/go`；
  PATH 上的 `go` 被包了一层，`-v` 会被吞成一行汇总。
- **本仓当时有并发会话在改 iter-25（store retention）**，共享工作树上 `go build ./...` 会因
  `internal/store`、`internal/cockpit` 的未完成改动而失败。N-1/N-2 均在隔离工作树
  （pristine HEAD + 仅本迭代 7 个文件）上验证，与并发会话无关。

