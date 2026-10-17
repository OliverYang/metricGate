# 迭代 27 · 两个手动阻塞项（凭证与合并动作）

日期：2026-10-17

编号说明：本文占用 `iter-27`。它不对应调研文档 §7 排期表里的任何一行计划 —— 表里
`iter-21` / `iter-22` / `iter-23` 三个序号仍被**未开工**的工作占着（见该文 §7.1），
而本文记录的是 `iter-26` 之后真实发生的一次交付阻塞。沿用 `iter-24`/`iter-25`/`iter-26` 的惯例：
**文件号是日志序号，不是计划行号**。

上游：`docs/github-setup.md`（§1.3 gh 认证、§1.5 仓库设置、§9 GitHub MCP）
关联：PR [#1](https://github.com/OliverYang/metricGate/pull/1)、`.github/workflows/merge.yml`、`.github/workflows/seal.yml`
状态：**两个阻塞项均待人工执行**（不是缺陷，是能力边界）

## §0 一句话

CI 链路已经全绿并推到远端，但**最后一步和凭证那一步都落在人手**：
合并 PR 需要写权限的 GitHub 身份，`github` MCP 需要浏览器 OAuth。两者都无法由本会话代为完成。

本文把这两件事写成可执行的交接单，而不是留在对话里 —— 因为对话会滚动掉，
而"为什么这件事自动化不了"是本项目后续每一轮都会再撞上的同一个边界。

## §1 阻塞项 A：合并 PR #1

### §1.1 为什么不能自动

`merge.yml` 就是为"自动合并"写的，它在本次 PR 里、也通过了全部检查，
但它**这次合并自己用不上**。原因是 GitHub 的一条硬规则，写在
`.github/workflows/merge.yml:27-30` 的头注释里：

> **THIS FILE MUST BE ON main TO DO ANYTHING**
> `workflow_run` only fires for a workflow whose file exists on the default
> branch, so the merge job starts working one merge after this file lands.

`workflow_run` 事件只对**已存在于默认分支**的 workflow 生效。`merge.yml` 是本次新增的文件，
在它落进 `main` 之前，它永远不会被触发 —— 包括触发它自己落进 `main` 的那次合并。
这不是设计缺陷，是事件模型的规定；代价就是**第一次合并必须有人点**。

第二次起（后续所有 PR）走自动路径，无需人工。

### §1.2 合并前必须成立的状态（本会话已逐项实测）

| 检查项 | 值 | 取法 |
|---|---|---|
| PR | [#1](https://github.com/OliverYang/metricGate/pull/1)，base `main` | `GET /repos/OliverYang/metricGate/pulls/1` |
| head commit | `deb9d05a03082b55bd5506cde57d722a858b7078` | 同上 `head.sha` |
| 规模 | 17 commits / 13 files / +938 −100 | 同上 |
| `mergeable_state` | `clean` | 同上 |
| head 上的 check runs | `open` ✅ / `gate` ✅ / `review` ✅（3/3 completed success） | `GET /commits/<sha>/check-runs` |
| gate 结论 | `⚠ gate passed with warnings` — 0 error / 2 warning | PR 注释 `<!-- metricgate-gate-report -->` |
| review 结论 | `✅ approved` — 无 breaking API / 无依赖漂移 / 无单边 clone 编辑 | PR 注释 `<!-- metricgate-review-report -->` |
| 整树闸门（seal 的判据） | 0 errors / 421 warnings / PASSED | `./metricgate check . -arch-base arch.json` |

两条 warning 的原文（**已知误报，不阻塞**，见 §3.1）：

```
⚠ internal/facts/lang_test.go:343 test-no-assert — test "TestExtractJSModuleLocalDeclarationsAreNotExported" has no assertion
⚠ internal/facts/lang_test.go:363 test-no-assert — test "TestExtractJSClassMembersKeepClassReachability" has no assertion
```

### §1.3 执行步骤

任选其一，**都用 squash**（`merge.yml` 用的也是 squash，保持历史形态一致）：

```bash
# 方式一：已登录 gh 的终端
gh pr merge 1 --repo OliverYang/metricGate --squash

# 方式二：网页
# https://github.com/OliverYang/metricGate/pull/1 → "Squash and merge"
```

### §1.4 合并之后应立刻出现的三件事（用于验证，不是预期，是判据）

1. **`merge` workflow 出现在仓库的 workflow 列表里**（本次 PR 新增了它）：
   ```bash
   curl -s https://api.github.com/repos/OliverYang/metricGate/actions/workflows \
     | grep -o '"name": *"merge"'
   ```
2. **`seal` run 打出 tag `v0.2.0` 并建 Release**。判据：
   ```bash
   curl -s https://api.github.com/repos/OliverYang/metricGate/tags
   # 期望看到 v0.1.0（旧）+ v0.2.0（新）
   curl -s https://api.github.com/repos/OliverYang/metricGate/releases
   # 期望 v0.2.0 的 body 含 Evidence（errors/warnings/snapshots/commit）与 Artifact（二进制 sha256）
   ```
   若 `seal` 成功但**没有** tag，说明它走了 §2 之外的另一条分支 —— 查 job summary 里的
   `## Not sealed` 段，那里会写明 tag 为什么被判定为已占用。
3. **`main` 上的整树闸门仍然是 0 errors**。`seal` 在打 tag 之前会跑整树闸门，
   所以"Release 存在"本身就隐含了这一点；但若 Release 存在而你想复核，
   `seal` run 的 `Gate the whole tree` 步骤日志里有 `summary:` 行。

### §1.5 回滚点

| 误操作 | 回滚 |
|---|---|
| 误合了不该合的分支 | `git revert -m 1 <merge-commit>`（squash 合并则直接 `git revert <squash-sha>`） |
| tag 误打 | `git tag -d v0.2.0 && git push origin :refs/tags/v0.2.0`，再删 Release |
| `seal` 把 `main` 判成未封板 | 无需动作 —— 那是**跳过**而非失败（见 §2.1），job 会绿 |

## §2 阻塞项 B：`pi mcp login github`

### §2.1 为什么不能自动

`github` MCP server 指向 GitHub 官方的远端实现
`https://api.githubcopilot.com/mcp/`，它走 **OAuth 授权码流程**：
`pi mcp login github` 会起本地回调并**打开浏览器**让你在 GitHub 页面上点"Authorize"。
这一步需要人在浏览器里操作，无人值守脚本跑不完 —— 本会话没有浏览器交互能力。

`gh auth login` 同理会弹设备码，也不可代做。

### §2.2 当前状态（实测）

```
$ pi mcp list
github: needs sign-in (codemode, global)
  https://api.githubcopilot.com/mcp/
  sign in with: pi mcp login github
```

```
$ cat ~/.pi/agent/mcp-auth.json
{}
```

`~/.pi/agent/mcp.json` 里的条目**已经修好**，只剩授权：

```json
{
  "url": "https://api.githubcopilot.com/mcp/",
  "description": "GitHub API: issues, pull requests, branches, checks, and reviews as the signed-in user"
}
```

> 历史坑（已修，记录以免重犯）：`pi mcp add` 会自动写入
> `"headers": {"Authorization": "Bearer ${GITHUB_TOKEN}"}`。环境里没有 `GITHUB_TOKEN` 时，
> 整条 server 解析失败并报
> `Failed to resolve MCP server "github" header "Authorization" from environment variable: GITHUB_TOKEN`。
> 那个 header 是多余的 —— 远端 server 走 OAuth，不需要预置 token。已删除。

### §2.3 执行步骤

```bash
pi mcp login github     # 浏览器里点 Authorize
pi mcp list             # 期望 github: connected, N tools
```

### §2.4 做完之后解锁了什么

| 能力 | 现在（未授权） | 授权后 |
|---|---|---|
| 读 issue / PR 列表 | 只能匿名 API，**60 req/hour**，且看不到 `permissions` / `allow_auto_merge` 等字段 | 正常配额，能读全字段 |
| 本地 agent 领任务 | `scripts/agent-claim.sh` 降级到「人工给 issue 编号」（设计文档 §2.3 的 C 路线） | 走 A 路线：列 `label=agent` 未分配 issue 并打 `in-progress` |
| 分支保护是否配置 | `GET /branches/main/protection` → **401**，看不到 | 可读，能验证 required checks 是否真的生效 |
| 合并 / 打 label / 建 issue | 做不到 | 能做到（但仍建议合并走 §1 的人工路径，理由见下） |

**建议仍然保留 §1 的人工合并**：MCP 授权给的是"你本人"的身份，
而 `merge.yml` 的判据（读全部 check run、钉住 `pr.head.sha`）是**仓库里的、可 diff 审查的**规则。
把合并交回给一个能在本地被 prompt 影响的 agent，等于把"确定性门禁"换成"模型心情"。
MCP 的用途是**读**（领任务、看保护配置），不是**写**（合并）。

### §2.5 为什么 token 不进 GitHub secrets（与既有约束一致）

`docs/github-setup.md` §7 的约束原文：

> **不把 API key 放进 GitHub secrets**。代码修改在本地做，云端 CI 只做判断与记录。

MCP 的 OAuth token 存在 `~/.pi/agent/mcp-auth.json`（本机），不进仓库、不进 CI、不进 workflow。
`pi mcp login` 与「不把 key 放进 secrets」不冲突：前者是**本地 agent 的凭证**，后者约束的是**云端 CI**。

## §3 顺带记录（不阻塞，但别丢）

### §3.1 `test-no-assert` 不认自定义断言 helper（误报源头）

PR #1 上那 2 条 warning 就是它。`internal/rules/go_test_no_assert.go:75 isAssertionCallee` 的
白名单是：名字含 `assert` / `require`、`must` 前缀、精确等于
`Error` / `Errorf` / `Fatal` / `Fatalf` / `Fail` / `FailNow` / `panic`。

测试里调自定义 helper（`wantVis(t, ...)`、`wantDef(t, ...)`）不命中任何一条，
于是被判定"没有断言"。**整树闸门当前共 8 条同类误报**（`./metricgate check .` 实测）：

```
internal/cockpit/web_smoke_test.go:34
internal/facts/lang_test.go:183, 213, 235, 284, 343, 363
internal/metrics/grammar_probe_test.go:83
```

这不是本轮引入的，也不是"规则错了"——规则的设计前提是 AST-only、命名驱动、无类型信息
（`internal/rules/go_test_no_assert.go:56-57` 的注释明说 "AST-only and naming-based by design"）。
要修就得选一条路：
跟随被调函数的定义判断它是否含断言（跨函数分析，成本高），
或者放宽到"调用了任何 `t` 相关方法或任何本地 helper"（会放过真没断言的测试）。
**这是一个口径决策，不是补个字符串。**

### §3.2 `merge.yml` 落进 main 之前的中间态

本次合并之后、下一个 PR 之前，仓库处于一个特殊状态：`merge.yml` 已在 main 上，
所以**下一个 PR 会被自动合并**。如果你不希望它自动合，合并 PR #1 之前先在
`merge.yml` 里加一个开关（例如 `if: vars.METRICGATE_AUTOMERGE == 'on'`）。
本会话没有加，因为你的决策是「check 全绿后自动 squash merge」（m06096），默认就该开。

### §3.3 本轮的两次误提交（教训）

| 提交 | 症状 | 根因 | 修法 |
|---|---|---|---|
| `39e88ca` | CI 报 `undefined: cmdStore` | 用 `git add cmd/metricgate/main.go` 把**并发会话**未完成的 `"store": cmdStore` 注册带进了提交，而 `store_cmd.go` 仍未跟踪 | `deb9d05` 撤回注册与两个文件 |
| `82f6075` | CI 报 `undefined: pruneOutput` / `config.StoreConfig.Retain` | 试图补 `store_cmd.go`，但它依赖的 `store_render.go`、`internal/store/retain.go`、`internal/config` 的 retain 改动全都未提交 | 同上 |

**教训（写进这里因为会再犯）**：

1. **提交任何文件前，先确认它的全部依赖也在这一批里**。单文件 `git add` 是危险的。
2. **并发会话改过的文件，不要按单文件 add**。本仓有多会话同时编辑（见任务 PRD 的
   「并发编辑告警」），`git add <file>` 分不清"我的改动"和"别人的改动"。
   安全做法：`git diff <file>` 逐行确认，或干脆不碰该文件。
3. **CI 是唯一的裁判**。本地 `go build ./...` 通过不代表提交能构建 ——
   本地工作区有未跟踪文件时，`go build` 会用到它们，而 CI 拿到的是提交。

## §4 交接清单（按顺序）

- [ ] **B1** 合并 PR #1（§1.3），确认 squash
- [ ] **B2** 验证三件事（§1.4）：`merge` workflow 出现 / `v0.2.0` tag + Release 存在 / `main` 整树闸门 0 error
- [ ] **B3** `pi mcp login github`（§2.3），`pi mcp list` 显示 connected
- [ ] **B4**（可选）用 MCP 读一次 `GET /branches/main/protection`，确认 required checks 配置；若 404 说明没配分支保护，见下
- [ ] **B5**（可选）决定 `test-no-assert` 的口径（§3.1），单开迭代

## §5 开放问题

1. **分支保护是否要配？** 本会话匿名读 `/branches/main/protection` 是 **401**（看不到，不是没有）。
   若不配 required checks，`merge.yml` 仍能工作（它自己读全部 check run，不依赖分支保护），
   但**人工**合并时 GitHub 不会拦你合一个红 PR。两条路各有代价：
   - 配：多一层保护，但 required check 名字写错会让所有 PR 永久 pending（这正是 `gate.yml` 去掉
     `pull_request` paths 过滤的原因，见该文件注释）
   - 不配：`merge.yml` 是唯一防线，绕过它（人工点合并）就没有防线
   建议：**配**，required checks 填 `gate` 与 `review`（这两个名字就是 job id，已在
   `.github/workflows/gate.yml` 与 `review.yml` 里）。

2. **`v0.2.0` 是否是你想要的版本号？** 本轮把 `toolVersion` 从 `0.1.0` 提到 `0.2.0`（见
   `cmd/metricgate/main.go:42` 的注释），理由是 v0.1.0 封的 commit 早于 cockpit workspace、
   语言对齐、review+merge 链路。若你想要 `v1.0.0` 或别的值，**在合并 PR #1 之前改**
   —— 合并之后 tag 就打出来了，而 tag 永不移动。
