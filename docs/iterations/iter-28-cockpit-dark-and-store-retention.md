# 迭代 28 · 深色 cockpit 落地 + 把 store 保留策略从分支上捞回来

日期：2026-10-17

编号说明：本文占用 `iter-28`。`iter-25`（store 保留策略）与 `iter-26`（post-edit hook）
的日志原本只在 `feat/issue-1-ci-smoke-test` 上，其中 `iter-25` 随本轮代码一并进入 `main`
（见 §3），`iter-26` 仍留在分支上未取。`iter-27` 是 GitHub 链路阻塞项的手动交接记录。

上游：本会话的两次请求 —— 「用 Stitch 重构前端」（承接 `iter-27` 之后的四轮 cockpit 重构）
与「P0 推 main / P1 捞 store 保留策略」。

## §0 一句话目标

**把两件已经做完但还没进 `main` 的事真正落地**：四轮 cockpit 深色重构（6 个 commit
躺在本地），以及一个写完又被 revert 掉的 history.db 保留策略（5.5k 行躺在分支上）。

两件事的共同形状不是「没做」，是**「做完了但没到 `main`」**——所以本轮的验收标准
不是「写出来」，是「在 `main` 上能跑」。

## §1 P0：本地 main 领先远端 6 个 commit

| commit | 内容 |
|---|---|
| `f733c07` | 令牌换 Stitch 深色（1 file +139 −106） |
| `cf16094` | 表格密度（36px 行高、大写小字表头） |
| `c7bb752` | issues 表格化 + 右侧抽屉（+163 −29） |
| `6ebd69c` | runs 表格化 + overview 综合评级卡（+170 −39） |
| `1e6846c` | 文档/spec 回写（CU-05、规则 7/8、cockpit-ui-contract.md） |
| `791b198` | `scripts/cockpit-run.sh` 一键编译启动 |

**推送前的检查**（三条都通过才推）：

1. `git fetch origin` → `origin/main` 仍是 `32529ba`，`ahead 6 / behind 0`，没有人动过远端
2. `bash scripts/cockpit-observe.sh quick` → render_all 9 views × 3 scopes × 2 modes + 7 router
   round trips ok、contract tests ok
3. 这 6 个 commit 没碰 `.github/`，且 7 个 workflow 全是 `disabled_manually`，所以推送
   不会触发任何 run（`iter-27` §3 之后的停用状态仍在）

推送结果：`32529ba..791b198  main -> main`，0 个 workflow run。

## §2 P1 的难点：为什么不能直接 merge 分支

`feat/issue-1-ci-smoke-test` 与 `main` 的分叉点是 `d191d6b`（v0.1.0），落后 13、领先 23。
它里面有两类东西，而**整体 merge 会把 P0 的四轮成果全部盖掉**：

| 内容 | 后果 |
|---|---|
| `internal/store/retain.go` 342 行 + `retain_test.go` 476 行 | 真活儿，`main` 上没有 |
| `internal/store/meta.go` 83 + `stats.go` 79 行 | 真活儿 |
| `cmd/metricgate/store_cmd.go` 264 + `store_render.go` 193 行 | 真活儿 |
| `internal/cockpit/rules_zh.go` 59 行 | 真活儿（规则中文名/说明） |
| `internal/cockpit/web/` 浅色 `#fcfcfd` | **一 merge 就盖掉深色重构**（`app.js` 989 行 / `style.css` 892 行差异） |
| 43 个 `.agents/skills/` 文件 | 噪音 |

所以路径是**逐路径捞取**，不是 merge：

```bash
git checkout -b feat/salvage-store-retention
git checkout feat/issue-1-ci-smoke-test -- \
  internal/store/{retain.go,retain_test.go,meta.go,meta_test.go,stats.go} \
  cmd/metricgate/{store_cmd.go,store_cmd_test.go,store_render.go,prune_on_write_test.go} \
  internal/config/retain_test.go
```

三个**必须打补丁而不是整文件取**的路径，因为 `main` 上它们已经因 P0 与其它迭代而改变：

| 路径 | 取法 | 为什么 |
|---|---|---|
| `internal/config/config.go` | `git diff main...分支 -- <path> \| git apply --3way` | 只需要 `RetainConfig` / `PruneOnWrite` / `normalizeStore` 三块，整文件取会退回旧内容 |
| `internal/store/store.go` | 同上 | 需要 `store_meta` 表与它的 migration；整文件取会丢掉 `issues_query` 侧的改动 |
| `cmd/metricgate/check_cmd.go` | 同上 | 需要 `pruneAfterWrite`；`main` 上该文件已有 P0 之后的其它改动 |

**还有一个「revert 留下的坑」**：`deb9d05 revert(build): drop the store subcommand from this
branch` 把 `main.go` 里的 `"store": cmdStore` 注册行撤了，但 `store_cmd.go` 留着。
这就是分支上 22 个 commit 在 `git cherry` 里显示未合、而内容大半已在 `main` 的原因，
也是**只取文件不补注册行就会 `undefined: pruneAfterWrite`** 的原因。所以 `main.go`
的注册行与 `usage.go` 的帮助行必须手写补上。

## §3 捞回来之后发现的两个缺口

### §3.1 `Store.Vacuum()` 是死代码

`retain.go` 里写了 `func (s *Store) Vacuum() error`，注释解释得很清楚（DELETE 只标页
空闲，VACUUM 才回收；需要等量空闲磁盘；不能顺手在 check 里做）。但：

- **零调用者**：`grep -rn '\.Vacuum()' --include=*.go .` 为空
- **零测试**：`retain_test.go` 与 `store_cmd_test.go` 都不提它
- 而 prune 的输出却在说 **「VACUUM would return it (not run automatically)」** ——
  操作者读完这句话，**没有任何命令可以执行**

这是「文档暗示一个不存在的入口」这一类缺陷，与 `iter-27` §8.3 记的「跑绿了不等于
链路通了」同源：一个方法的**存在**不等于它**可达**。

修法是加 `-vacuum` flag（commit `4ed4ee3`）：

- 只在**真的删了东西**的 prune 之后跑（重写 500MB 文件去回收 0 页是纯成本）
- 与 `-dry-run` **在 parse 阶段就互斥**：dry run 承诺「什么都不改」，VACUUM 重写整个
  文件，两者放一起就是自相矛盾
- text 与 json 输出都带上「是否真的 vacuum 过」，让「is now free pages」那句话
  不会在一次 vacuum 过的运行里存活

**实测（真实库 497.5 MiB）**：

```
$ metricgate store prune -runs 30
removed 284 runs (36485 findings / 4015513 snapshots), about 440.1 MiB
kept: 37 runs
file: 497.5 MiB -> 497.5 MiB; 371.0 MiB is now free pages. ...

$ metricgate store prune -runs 30 -vacuum
file: 497.5 MiB -> 121.5 MiB; vacuumed, so the freed pages are back on disk.
```

### §3.2 测试断言写错了判据

第一版 `TestVacuumReturnsTheFreedPages` 断言 **文件变小**，在 12 runs 的小库上失败：

```
store_cmd_test.go:159: file did not shrink: 69632 -> 69632 bytes
```

原因：VACUUM **按定义**是压缩页，不承诺缩短文件；小库重写后落在同一页数上完全正常。
真正的判据是 `freelist_count == 0`。**断言选错判据的测试比没有测试更糟**——它会在
正确的实现上失败，然后被删掉或被 `t.Skip` 掉，把「这个行为没人守」伪装成「这个行为
不需要守」。

## §4 验收

| 判据 | 结果 |
|---|---|
| `go build ./...` | 干净 |
| `go vet ./...` | 干净 |
| `go test ./... -count=1` | **925 passed / 53 packages** |
| `bash scripts/cockpit-observe.sh quick` | render_all 全视图 × 全 scope + contract tests ok |
| `metricgate check . -arch-base arch.json` | **0 errors / 422 warnings / 25450 snapshots / PASSED** |
| `metricgate store prune -dry-run` | `nothing to retire: no bound is set`（默认不限，行为与旧版一致） |
| `metricgate store prune -dry-run -runs 5` | `would remove 308 runs ... about 476.4 MiB` / `dry run: database unchanged — page_count 126705 -> 126705` |
| `metricgate store prune -runs 30 -vacuum` | 497.5 MiB → 121.5 MiB |

warning 从 415 升到 422 是新代码带来的（`retain.go` / `store_cmd.go` 的 fan-out 与
复杂度），不是回退。

## §5 三个 commit

```
628cd40 feat(store): the history database gets a retention policy and a prune verb
433c7d7 docs(store): the iter-25 log travels with the code it describes
4ed4ee3 feat(store): -vacuum gives the prune a way to return the pages it frees
```

`main` 从 `32529ba` 走到 `4ed4ee3`，已推送。

## §6 遗留

1. ~~**`internal/cockpit/rules_zh.go` 还在分支上**~~ → **已捞回并进 `main`**（`f0b0a6e`，
   iter-29 R4）：`rules_zh.go` + `rules_catalogue_test.go` + `rules_api.go` 的
   语言分组，加上前端「说明」列改读中文标题 + 新增「默认」列。`T4.3` 的前置已清。
   一个未消化的观察：`/api/rules` 返回的 `languageGroups` 至今**没有任何消费者**
   （前端与 MCP 都不读），要么按语言分组渲染，要么删字段。
2. ~~**`iter-26-post-edit-hook.md` 还在分支上**~~ → **已解**（`iter-29` §6）：
   `iter-26-post-edit-hook.md` 与 `docs/workflow.md:108` 承诺的
   `.claude/settings.json.example` 都已进 `main`。文档与代码重新同树。
3. **分支 `feat/issue-1-ci-smoke-test` 仍保留**（`79497d1`），兜底 tag
   `wip/snapshot-2026-10-17` → `e1e1042`。本轮只捞了 store 一族，`web/` 与
   `.agents/` 不动。
