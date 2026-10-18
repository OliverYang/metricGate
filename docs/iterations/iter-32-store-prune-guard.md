# iter-32 — 默认关闭的阀门，和一个真的会删库的命令行

日期：2026-10-18。分支 `feat/salvage-store-retention`（`8bd0387` → `b2f9a81`，已推 `main`）。

## 0. 一句话

捞回 `store prune`（`628cd40`）时验收三条全为真，但捞回来的是一个**默认关闭的阀门**：
`store.retain` 不设界，所以在自己机器上跑 `store prune` 报
`retention: unlimited (no bound set): nothing is ever removed`。本轮不抬界，
而是先把**删**这个动作变成可审计、可拒绝、且不会误中目标的事——因为在把默认改成
有界的路上，先撞上的是一件更硬的事。

## 1. 撞上的那件事（本轮最大的收获，来自一次事故）

本轮在仓库根目录跑过一次这句：

```console
$ cd ~/Downloads/metricGate && go build -o /tmp/mg-retain32 ./cmd/metricgate && /tmp/mg-retain32 store prune -runs 20
removed 78 runs (29503 findings / 1869629 snapshots), about 211.2 MiB
```

`store prune` 的目标库由**当前工作目录发现的 policy** 决定，所以这句话删的是这个仓库
自己的 `.metricgate/history.db`。事先手工 `cp` 的那份副本是唯一没丢的原因。

⇒ 因此本轮的范围决定是：**先做护栏，默认值留给带决策记录的独立一轮**（prd.md
「Why not 默认有界」一节）。有界不等于安全：有界的默认 + cwd 发现 = 把这台机器上每个
带 `.metricgate.yaml` 的仓库一并纳入自动删除范围。

## 2. 四个缺陷与处置

| | 缺陷 | 处置 |
|---|---|---|
| D1 | 空库上 `store prune` 直接失败：`Stats()` 把 `MIN/MAX(started_at)` 读进 `string`，无 run 的库返回 SQL NULL → `converting NULL to string is unsupported`，exit 2。新克隆的仓库全是这个形态。 | `internal/store/stats.go` 两列改 `COALESCE(..., '')`。不改成 `*string`：那会让空态传染到每个调用点和 JSON schema。实测 `prune -dry-run` 在空目录 `store: 0 runs, 0 findings, 0 metric snapshots, 68.0 KiB` + **rc=0**。 |
| D2 | 真实删除可以在不指名目标文件的情况下删掉 78 runs。 | `cmd/metricgate/store_guard.go`：正向判据 `cfg.SourceKind == "policy_file"`（不是 `!= "builtin"`）→ 打印绝对路径与精确计数 → 有终端就问 `remove this history? [y/N]`、没终端就拒 → 写 `store_meta.last_prune_intent`（declared/removed + source + store_path）。`-yes` **只跳提问，不跳源要求、不跳打印、不跳记账**。 |
| D3 | `metricgate store` 裸跑只说 `needs a subcommand`。 | 先打印 prune usage + `metricgate store prune -dry-run`，退出码仍 **2**（与 `dispatch` 其余动词一致；设计稿里那句「保持 0」是事实错误，`cmdCockpit` 也是 2）。 |
| D4 | `-vacuum` 与 `-yes` 在 `usage.go` 与整个 `docs/` 的 grep：**0 处提及**；prune 只删行、vacuum 才还字节这件事没写在任何地方。 | 新建 `docs/store-retention.md`（133 行，§1 三态 / §2 实测 / §3 护栏四步 / §4 `last_prune` vs `last_prune_intent` / §5 空库）；`usage.go:82` 补 DELETE/VACUUM 分工与指针；`docs/README.md` 顶层表加一行；`docs/capabilities.md:234` 的 `store` 行补 `retain.*` / `prune_on_write` 三个键。 |

## 3. 验收现场（全部在真库副本上，真实库本轮只被读过）

副本 `cp .metricgate/history.db /tmp/retain34/hazard.db`（135 runs / 89,596 findings /
3,410,441 snapshots / 350.4 MiB；比 prd 里记的基线大，因为 `check` 一直在写）。
跑完复核真库：`135 | 89596 | 3410441` —— 一个字节都没变。

| AC | 命令 | 实测 |
|---|---|---|
| AC1 | `store prune -dry-run`（空目录，无 policy） | `store: 0 runs, 0 findings, 0 metric snapshots, 68.0 KiB` + `nothing to retire: no bound is set` + **rc=0**，无 Scan error ✅ |
| AC2 | `store prune -runs 5`（无 policy 目录，即事故原句） | `refusing to prune: no policy file was named` + 两条可执行下一步 + **rc=2**，且 `ls -R` 仍为空——**护栏在 `store.Open` 之前，被拒的运行连库文件都没创建** ✅ |
| AC2′ | 同上但加 `-yes` | 同样被拒（`-yes` 不解锁未指名的目标） ✅ |
| AC3 | `store prune -config named.yaml -dry-run`（`retain: {runs: 20}`） | `would remove 107 runs (39634 findings / 2371675 snapshots), about 277.7 MiB` / `page_count 89696 -> 89696, runs 135 -> 135` ✅ |
| AC3′ | 去掉 `-dry-run`、stdin 为 `/dev/null`、无 `-yes` | `about to remove 107 runs …` / `  from /tmp/retain34/hazard.db` / `remove this history? [y/N]` → `prune cancelled: nothing was removed — re-run with -dry-run to preview, or -yes to confirm …` + rc=2 ✅ |
| AC3″ | 加 `-yes` | 真删，6.1s；`file: 350.4 MiB -> 350.4 MiB; 228.6 MiB is now free pages` ✅ |
| AC3⁗ | 事后重放 | `-dry-run` → `would remove 0 runs …` + `all 28 runs are kept by policy` ✅；`last_prune_intent` 的 `declared` 与 `removed` **逐数相等**，`source: policy_file`、`store_path` 为绝对路径 ✅；`-format json` 里 `guard = {required: true, confirmed: true, source: policy_file, explicit: true}` ✅ |
| AC3ᵛ | `-runs 10 -yes -vacuum` | `file: 350.4 MiB -> 95.9 MiB; vacuumed, so the freed pages are back on disk` ✅；`-vacuum` + `-dry-run` 在解析期即拒（`-vacuum and -dry-run contradict`）✅ |
| AC4 | grep 文档 | prune / `-vacuum` / `-yes` / 实测数都在 `docs/store-retention.md` ✅ |
| AC5 | `go vet ./...` / `go test ./... -count=1` / `check . -arch-base arch.json` | 干净 / **990 passed in 53 packages** / `0 errors, 427 warnings, 27569 snapshots` + `gate: PASSED` ✅ |

一处值得记的判据细化：**prd 的 AC2 原文字面写的是「未设界的实删 → 拒绝」，实现的是
「未指名的实删 → 拒绝」**。未设界的实删不该拒（它删 0 行，是诚实的 no-op，文案已经
说明原因）；被拒的是「目标文件从未被命名」这一件事。判据写反会导致默认无界的 policy
（也就是全部现存 policy）永远跑不过这条命令。

## 4. 一次并发写事故，以及它暴露的约束

本轮中途发现：**同一 cwd 有两个 agent 在写**。`pid 2072`（本会话，起于 10-17 19:40）与
`pid 85419`（另一会话，起于 10-18 10:01，父进程是 Orca supervisor）。对方在
`feat/cockpit-consistency-pass` 上提交了六条 cockpit 改动，并在 11:14:57 `git stash`
+ 11:19:27 `git reset` —— 而我那份未提交的护栏集成恰好就在那棵树上。我为反向验证跑的一次
`git checkout cmd/metricgate/store_cmd.go` 把它还原成了 HEAD 版本，`store_flags.go`
与 `store_cmd.go` 随即 `pruneFlags redeclared`，`go build ./...` 失败。

恢复：`store_cmd.go` 从 `git show 'stash@{0}:…'` 取回（383 行，那份就是带
`declarePrune`/`executePrune`/`recordPruneOutcome`/`applyVacuum` 集成的正确版本）；
`storeBareMessage` 在任何提交、dangling blob 里都不存在（被 reset 彻底销毁），按
`store_cmd_test.go:88 TestStoreWithoutSubcommandPointsAtDryRun` 与
`:111 TestStoreBareSubcommandStillFails` 的断言重建。此后所有编辑改在隔离的
`git worktree`（`/tmp/mg-guard-wt`，分支 `feat/salvage-store-retention`），
`main` 用 `git branch -f main b2f9a81` + `git push origin main` 更新，
**对方的工作树与分支一次都没有被覆盖**。

⇒ 进 spec 的一条约束（`.trellis/spec/backend/destructive-commands.md` 之外，另一条更基本的）：
**两个 agent 共享一个工作目录时，`git checkout` / `git reset` / `git stash` 任一个都会
销毁对方的未提交工作**。本轮已经付过一次学费。反过来说，`git checkout <file>` 用作
「还原我自己的一次反向验证」是有前提的：那棵树必须只有我在写。这一点应该固化为
工具层约束（每轮开一个 worktree），而不是靠我记得。

## 5. 反向验证（都按预期变红，已还原）

- 把 `if f.yes { return nil }` 移到源判据之前 → `TestRealPruneRefusesBuiltinPolicySource` **FAIL**。
- 删掉 `!prompt.interactive` 分支 → `TestRealPruneAsksBeforeDeleting` **FAIL**：
  `stdin "y\n": a non-interactive real prune with no -yes deleted instead of refusing`。
- `metricgate store` 裸跑 → 完整 usage（含 `-yes`）+ `-dry-run` 提示 + `store needs a subcommand: prune`，**exit 2**。
- 新增测试合计：`cmd/metricgate/store_guard_test.go` 12 个、`internal/store/prune_intent_test.go` 5 个、`internal/store/stats_test.go` 3 个（空库 / 迁移 / 只读统计）。

## 6. 还开着的（不在本轮范围）

- **`retain` 的有界默认**：需要单开一轮明确修订 D22-5，并同时重新回答 `prune_on_write`
  的默认。成本实测：删除前要数，裸的两个 `COUNT(*)` 全扫在 350 MiB 库上 0.329s——
  所以「每次 check 后自动 prune」不该默认开。
- **真实库的自动保留**：这台机器上的 `.metricgate/history.db` 已 367,394,816 字节（350.4 MiB）且无界，
  本轮只证明手动路径安全，没证明它会被人用。
- **cockpit 侧的 prune 可见性**：`internal/cockpit/` 仍然 0 处 Prune/Retain 调用点，
  所以 UI 上看不到「这个库正在无界地长」。
- **护栏只覆盖 `store prune` 一个动词**：`accept remove`、`suppress` 的写路径同样落在
  cwd 发现的 policy 上，未来加删除类动词时按 spec 那份清单逐条走。

## 7. 提交

```
b2f9a81 docs(store): what a prune does, in the three states it can be in
4c6c5a6 fix(store): the prune guard, recovered from a concurrent-write accident
8bd0387 docs(research): iter-31 closed the two rows that made numbers uncheckable
```

`push origin main`：`8bd0387..b2f9a81`。CI 七个 workflow 复跑 `gh workflow list --all`
确认仍全部 `disabled_manually`（autopr / gate / merge / review / schedule / seal / triage），
`gh run list` 最新三条仍是 2026-10-17 的 seal/merge ⇒ 本轮推送产生 **0 个 run**。
