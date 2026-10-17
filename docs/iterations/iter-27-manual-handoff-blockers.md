# 迭代 27 · 两个手动阻塞项（凭证与合并动作）

日期：2026-10-17

编号说明：本文占用 `iter-27`。它不对应调研文档 §7 排期表里的任何一行计划 —— 表里
`iter-21` / `iter-22` / `iter-23` 三个序号仍被**未开工**的工作占着（见该文 §7.1），
而本文记录的是 `iter-26` 之后真实发生的一次交付阻塞。沿用 `iter-24`/`iter-25`/`iter-26` 的惯例：
**文件号是日志序号，不是计划行号**。

上游：`docs/github-setup.md`（§1.3 gh 认证、§1.5 仓库设置、§9 GitHub MCP）
关联：PR [#1](https://github.com/OliverYang/metricGate/pull/1)、`.github/workflows/merge.yml`、`.github/workflows/seal.yml`
状态：**两个阻塞项均已解除**（2026-10-17 晚）：B（MCP 授权）走 Bearer header 路线已通；
A（合并 PR #1）已用 MCP 完成 squash 合并，`v0.2.0` 已封板。两处断链（merge 段、triage 段）已接上。
修订：2026-10-17 晚第一轮，§2 全部按实测重写，§4 清单按实测勾选，新增 §6 实测矩阵
修订：2026-10-17 晚第二轮，§1 合并完成、§5.2 label 自举已修，新增 §7 闭环实录
修订：2026-10-17 晚第三轮，**让 `merge.yml` 自己合一次之后**又发现两个断点（竞态自锁、
`GITHUB_TOKEN` 合并不触发 `seal`），新增 §8。§7 的"已接上"只在手动合并路径上成立

## §0 一句话

CI 链路已经全绿并推到远端。原本有两个阻塞项都落在人手：合并 PR 需要写权限的 GitHub 身份，
`github` MCP 需要浏览器 OAuth。

**§2 的凭证阻塞已解除** —— `github` MCP 现已 `connected, 46 tools`，本节保留是因为
"为什么它曾经挡住整条链"仍是有效知识，但结论要按 §2.2 的实测重读。
**§1 的合并阻塞也已解除** —— 第二轮用 MCP `merge_pull_request` 完成 squash 合并，
`main` 前移到 `f8365113`，`seal` 自动打出 `v0.2.0`。§1 保留是因为"第一次合并必须人点"
这个边界每一轮都会再撞上（见 §7.1）。

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

### §1.2 合并前必须成立的状态（2026-10-17 晚重测，以本节为准）

| 检查项 | 值 | 取法 |
|---|---|---|
| PR | [#1](https://github.com/OliverYang/metricGate/pull/1)，base `main` | MCP `pull_request_read(method:get)` |
| head commit | `2f60d7861b0a877438ba8973e062b30680848e31` | 同上 `head.sha` |
| 规模 | **18 commits / 14 files / +1177 −100** | 同上 |
| `mergeable_state` | `clean` | 同上 |
| head 上的 check runs | `open` ✅ / `review` ✅ / `gate` ✅（3/3 completed success） | `GET /commits/<sha>/check-runs` |
| gate 结论 | `⚠ gate passed with warnings` — 0 error / 2 warning | PR 注释 `<!-- metricgate-gate-report -->` |
| review 结论 | `✅ approved` — 无 breaking API / 无依赖漂移 / 无单边 clone 编辑 | PR 注释 `<!-- metricgate-review-report -->` |
| 整树闸门（seal 的判据） | 0 errors / 421 warnings / PASSED | `./metricgate check . -arch-base arch.json` |
| 分支保护 | **404，未配置** | `GET /branches/main/protection` |

> 早先本表记的是 `deb9d05` / 17 commits / 13 files / +938 −100 —— 那是 `deb9d05` 时刻的快照，
> 之后又推了 `2f60d78`（本文档自身），规模已变。数字以本节为准，结论（`clean` + 3/3 绿）未变。

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

## §2 阻塞项 B：`pi mcp login github` —— **已解除**（2026-10-17 晚实测）

### §2.1 为什么它曾经无法自动

`github` MCP server 指向 GitHub 官方的远端实现
`https://api.githubcopilot.com/mcp/`，它走 **OAuth 授权码流程**：
`pi mcp login github` 会起本地回调并**打开浏览器**让你在 GitHub 页面上点"Authorize"。
这一步需要人在浏览器里操作，无人值守脚本跑不完 —— 本会话没有浏览器交互能力。

`gh auth login` 同理会弹设备码，也不可代做。

> 这段结论仍然正确，只是**它已经被人在浏览器里做过了**。下面记结果，不记过程。

### §2.2 当前状态（2026-10-17 晚实测）

```
$ pi mcp list
...
github: connected, 46 tools (codemode, global)
  https://api.githubcopilot.com/mcp/
  tools: add_comment_to_pending_review, add_issue_comment, add_reply_to_pull_request_comment,
         create_branch, create_or_update_file, create_pull_request, create_repository, delete_file,
         fork_repository, get_commit, get_file_contents, get_label, get_latest_release, get_me,
         get_release_by_tag, get_tag, get_team_members, get_teams, issue_read, issue_write,
         list_branches, list_commits, list_issue_fields, list_issue_types, list_issues,
         list_pull_requests, list_releases, list_repository_collaborators, list_tags,
         merge_pull_request, pull_request_read, pull_request_review_write, push_files,
         request_copilot_review, run_secret_scanning, search_code, search_commits, search_issues,
         search_pull_requests, search_repositories, search_users, sub_issue_write, ui_get,
         update_issue_comment, update_pull_request, update_pull_request_branch
```

认证成功，`mcp__github__get_me` 返回 `{"login":"OliverYang","id":15098266,…}`。

`~/.pi/agent/mcp-auth.json` **不是空的**：它持有 `mcp__github|https://api.githubcopilot.com/mcp/`
的 OAuth discovery 元数据（`authorization_endpoint` / `token_endpoint` / `resourceMetadata` 等）。
原文写的 `{}` 是授权前的快照，已失效。

### §2.3 历史坑（**这条原文写错了，本次修正**）

原文写的是：

> 历史坑（已修，记录以免重犯）：`pi mcp add` 会自动写入
> `"headers": {"Authorization": "Bearer ${GITHUB_TOKEN}"}`……已删除。

**实际上没有删。** 本轮实测 `~/.pi/agent/mcp.json` 里 github 条目仍带着：

```json
{
  "url": "https://api.githubcopilot.com/mcp/",
  "description": "GitHub API: issues, pull requests, branches, checks, and reviews as the signed-in user",
  "headers": { "Authorization": "Bearer ${GITHUB_TOKEN}" }
}
```

它一直能用，**只因为**当前 shell 里恰好有 `GITHUB_TOKEN`：

```
$ grep -o '^\s*export\s*[A-Z_]*' ~/.config/zsh/secrets.env
  GITHUB_TOKEN          # ~/.zshenv 会 source 它
$ gh auth status
  ✓ Logged in to github.com account OliverYang (GITHUB_TOKEN)
  - Token scopes: 'audit_log', 'gist', 'notifications', 'project', 'repo', 'user', 'workflow', 'write:packages'
```

也就是说：**"修好了"这个结论是在一个碰巧有 token 的终端里得出的**，
换一台机器、换一个未导出该变量的终端、或把它交给 CI，就会原样复现原文那条报错：

```
$ env -u GITHUB_TOKEN pi mcp list
github: failed (codemode, global)
  https://api.githubcopilot.com/mcp/
  Failed to resolve MCP server "github" header "Authorization" from environment variable: GITHUB_TOKEN
```

**为什么这个 header 是有害的**，不只是多余：

pi 的规则（`docs/mcp.md:133` 原文）是
> OAuth applies to HTTP servers **without** an `Authorization` header.

带了 header，pi 就认为这是 token 认证，**不会**走 OAuth。于是这个 server 的可用性
被绑在了一个与环境无关的变量上 —— 而 OAuth 凭证明明已经存在 `mcp-auth.json` 里。

### §2.4 尝试修复与回滚（本轮实测记录）

删掉 header 后（`cp` 备份在 `/tmp/mcp.json.bak`）：

```
$ env -u GITHUB_TOKEN pi mcp list
github: needs sign-in (codemode, global)
  sign in with: pi mcp login github

$ pi mcp list          # 带 GITHUB_TOKEN 也一样
github: needs sign-in (codemode, global)
  sign in with: pi mcp login github
```

**结论：`mcp-auth.json` 里只有 discovery 元数据，没有 access token / refresh token。**
之前的 connected 状态完全来自那个 Bearer header —— 也就是说这台机器**从未真正完成过 OAuth 授权**，
`iter-27` 原文 §2.2 的判断（"未授权"）在事实上是对的，只是被一条 header 掩盖了。

已回滚 header（`cp /tmp/mcp.json.bak ~/.pi/agent/mcp.json`），`github: connected, 46 tools` 恢复。

### §2.5 现在的真实形态：双凭证，**二选一**

| 路线 | 配置 | 凭证位置 | 跨机器可复现 | 配额 |
|---|---|---|---|---|
| **A · Bearer header**（当前） | `headers.Authorization: Bearer ${GITHUB_TOKEN}` | 环境变量（`~/.config/zsh/secrets.env`） | 否，换机即断 | 随 PAT |
| **B · OAuth**（原文想要的） | 无 header，只留 `url` | `~/.pi/agent/mcp-auth.json` | 是，但每台机器要 `pi mcp login` 一次 | 随 OAuth app |

两条都行，**不能混用**。当前处于 A 路线且工作正常。
切到 B 路线需要人工跑一次 `pi mcp login github`（浏览器），然后才能删 header ——
**顺序不能反**，先删 header 会直接掉线（就是 §2.4 的实测）。

若选 B，删 header 后建议把 PAT 从 `secrets.env` 里移除，避免两个凭证长期并存互相掩盖。

### §2.6 授权/连通后解锁了什么（本轮实测核对）

| 能力 | 实测结果 | 工具 |
|---|---|---|
| 读 PR 详情 | ✅ `open` / `clean` / `2f60d78` / 18 commits / +1177 −100 / 14 files | `pull_request_read(method:get)` |
| 读 PR 变更文件 | ✅ 逐文件 patch | `pull_request_read(method:get_files)` |
| 读 issue 列表 | ✅ 正常配额（open: 0 条） | `list_issues` |
| 搜索 issue / PR | ✅ `total_count: 1` | `search_issues` / `search_pull_requests` |
| 读 branches / tags | ✅ `main`+`feat/…`；`v0.1.0` | `list_branches` / `list_tags` |
| 读 Release | ✅ `v0.1.0` 含 Evidence + Artifact sha256 | `list_releases` / `get_latest_release` |
| 读文件内容 | ✅ `cmd/metricgate/main.go` | `get_file_contents` |
| 读 commit 历史 | ✅ `main` 上只有 1 个 squash commit `d191d6b` | `list_commits` |
| 读 label | ✅ 能查（`metricgate` → not found，见 §5.2） | `get_label` |
| **写**：合并 / 建 issue / 改 PR | 工具存在（`merge_pull_request` / `issue_write` / `update_pull_request`） | — |
| 分支保护配置 | ❌ **404 未配置**（MCP 无此工具，走 REST 实测） | — |
| 读 Actions run 历史 | ❌ MCP 无 actions 工具，走 REST | — |
| 代码搜索 | ⚠️ `search_code` 返回 `total_count: 0, incomplete_results: true` —— GitHub 对**单 commit 仓库**的代码索引基本是空的，不是权限问题 | `search_code` |

**建议仍然保留 §1 的人工合并**：MCP 授权给的是"你本人"的身份，
而 `merge.yml` 的判据（读全部 check run、钉住 `pr.head.sha`）是**仓库里的、可 diff 审查的**规则。
把合并交回给一个能在本地被 prompt 影响的 agent，等于把"确定性门禁"换成"模型心情"。
MCP 的用途是**读**（领任务、看保护配置），不是**写**（合并）。

### §2.7 为什么 token 不进 GitHub secrets（与既有约束一致）

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

## §4 交接清单（按 2026-10-17 晚实测勾选）

- [x] **B1** 合并 PR #1（§1.3），确认 squash —— **已完成**：MCP `merge_pull_request`，
      `merge_method: squash`，`expectedHeadSha` 钉在 `71a3b26`，产出 `f8365113`
- [x] **B2** 验证三件事（§1.4）—— **全部成立**：`merge` 已在 workflow 列表、
      `v0.2.0` → `f8365113`、Release body 含 `errors: **0**` 与 sha256
      `80d47dc7c59e1d5ffae355a988dc6c58b1a32fde66afd150de99352ddca55a54`
- [x] **B3** `pi mcp login github`（§2.3）—— **已可用**（46 tools），但走的是 Bearer header 路线，不是 OAuth。
      要换成 OAuth 需先跑 login 再删 header，顺序见 §2.5
- [x] **B4** 用 MCP 读 `GET /branches/main/protection` —— **已实测：404，确认未配置**（不是 401）
- [ ] **B5** 决定 `test-no-assert` 的口径（§3.1），单开迭代 —— 未做
- [x] **B6** `triage` 的 label 自举缺失，见 §5.2 —— **已修**：`triage.yml` 用
      getLabel→createLabel，`agent-claim.sh` 用 `gh label create --force`，三个 label 已建好
- [ ] **B7** MCP 凭证路线二选一（§2.5）—— 当前停在 A 路线，可用但不可跨机复现

## §5 开放问题

1. **分支保护是否要配？** 本轮用带凭证的 REST 实测：
   `GET /repos/OliverYang/metricGate/branches/main/protection` → **404**，
   即**确定未配置**（原文写的 401 是匿名视角，现已推翻）。
   若不配 required checks，`merge.yml` 仍能工作（它自己读全部 check run，不依赖分支保护），
   但**人工**合并时 GitHub 不会拦你合一个红 PR。两条路各有代价：
   - 配：多一层保护，但 required check 名字写错会让所有 PR 永久 pending（这正是 `gate.yml` 去掉
     `pull_request` paths 过滤的原因，见该文件注释）
   - 不配：`merge.yml` 是唯一防线，绕过它（人工点合并）就没有防线
   建议：**配**，required checks 填 `gate` 与 `review`（这两个名字就是 job id，已在
   `.github/workflows/gate.yml` 与 `review.yml` 里）。

2. **`triage.yml` 缺 label 自举** —— **已修（2026-10-17 晚第二轮）**

   原实测（修复前）：

   ```
   $ curl .../actions/workflows/triage.yml/runs
   total: 0                       # 一次都没跑过（只在 schedule + workflow_dispatch 上）
   $ curl .../labels | grep -c 'metricgate\|agent'
   0                              # 仓库只有 GitHub 默认 9 个 label
   $ mcp__github__get_label(name: "metricgate")
   label 'metricgate' not found in OliverYang/metricGate
   ```

   而 `triage.yml` 在建 issue 时写死 `labels: ['metricgate', 'agent']`。
   GitHub REST 对**不存在的 label** 返回 **422 Validation Failed**（不是静默忽略），
   所以 triage 第一次真正跑起来时会在第一个 error 上就失败。
   同样的问题也在 `scripts/agent-claim.sh`：它 `--claim N` 要打 `in-progress`，那个也不存在。

   **修法（已实施）**：
   - `.github/workflows/triage.yml` 的 filing 步骤在 `issues.create` **之前**，对
     `metricgate` / `agent` 逐个 `getLabel`，404 才 `createLabel`。
     用 getLabel-then-create 而不是 createLabel-then-ignore-422：404 是常规路径
     （"这个 label 是新的"），而吞掉 422 会把真正的参数错误一起吞掉。
   - `scripts/agent-claim.sh` 新增 `ensure_label` / `ensure_labels`，用
     `gh label create --force`（已存在则更新而非报错，无需解析 404），在
     `cmd_claim` 里先于 `gh issue edit --add-label` 调用。
   - 三个 label 已用 REST 建好：`metricgate #0e8a16` / `agent #1d76db` / `in-progress #fbca04`。

   **证明自举真的会创建（而不只是"没报错"）**：triage 的扫描结果是 0 errors，
   所以第一次 dispatch 根本没走到建 issue 的循环，创建分支没被执行。于是删掉 `agent` label
   再 dispatch 一次，日志里出现：

   ```
   scan produced 0 error-severity finding(s)
   created label agent
   ```

   label 列表回到 13 个。这才算证明，否则只是"跑了没崩"。

3. **`v0.2.0` 是否是你想要的版本号？** —— **已封板**。`seal` 读
   `var toolVersion = "0.2.0"`（`cmd/metricgate/main.go:42`）打出 tag `v0.2.0`，
   现在改版本号需要 bump `toolVersion` 再合并一次（tag 永不移动）。

## §6 本轮 GitHub MCP 实测矩阵（2026-10-17 晚）

### §6.1 链路现状：通到哪一步，断在哪一步

| 环节 | workflow | 远端 run 数 | 最后状态 | 判定 |
|---|---|---|---|---|
| 分支推送 → 自动开 PR | `autopr` | 17 | ✅ success | **通** |
| PR diff 约束闸门 | `gate` | 9 | ✅ success | **通** |
| PR 审查（breaking API / 依赖漂移 / 单边 clone） | `review` | 8 | ✅ success | **通** |
| 合并 | `merge` | 1（本次引入，未自触发） | — | **已接上**：`f8365113` 之后已在 main，`workflow_run` 可触发（§7.2） |
| 封板（tag + Release） | `seal` | 3 | ✅ success @ `f8365113` | **通**：已封 `v0.2.0` |
| 定时全树扫描（趋势） | `schedule` | 0 | — | 未触发（周扫，正常） |
| error → issue | `triage` | 2 | ✅ success | **已接上**：label 自举已验证（§5.2 / §7.3） |

一句话（第一轮结论，现已完成）：**"推送 → PR → 两道闸门全绿"这一段是活的；"合并 → 封板下一版"与"发现问题 → 建 issue"两段是断的，
而两处断因都已定位到具体文件的具体行，不是模糊的"链路不通"。**

**第二轮已把这两段接上**，实测记录见 §7。

### §6.2 用 MCP 做的实测（不依赖 `gh` CLI、不依赖匿名 API）

| 调用 | 结论 |
|---|---|
| `get_me` | `OliverYang`，已认证 |
| `pull_request_read(method:get)` | PR #1 open / clean / head `2f60d7861b0a877438ba8973e062b30680848e31` / 18 commits / 14 files / +1177 −100 |
| `pull_request_read(method:get_files)` | 逐文件 patch 可读 |
| `list_commits(perPage:3)` | `main` 上**只有 1 个** commit（squash 合并形态） |
| `list_branches` | `main`@`d191d6b` / `feat/issue-1-ci-smoke-test`@`2f60d78` |
| `list_tags` / `get_tag` | 仅 `v0.1.0` → `d191d6b` |
| `list_releases` / `get_latest_release` | `v0.1.0`：errors 0 / warnings 413 / 24542 snapshots / PASSED / 二进制 sha256 `932d8f9e…1de1` |
| `list_issues(state:OPEN)` | 0 条 |
| `search_issues` / `search_pull_requests` | 各 1 条（PR #1） |
| `search_code` | 0 条，`incomplete_results: true` —— 单 commit 仓库索引为空 |
| `get_label("metricgate")` | not found（见 §5.2） |
| `get_file_contents(cmd/metricgate/main.go)` | 可读 |

不走 MCP 的两条（MCP 无对应工具，改用 REST + `GITHUB_TOKEN`）：

```bash
# check-runs：3/3 success
GET /repos/OliverYang/metricGate/commits/2f60d78…/check-runs
  open completed success / review completed success / gate completed success

# 分支保护：404 未配置
GET /repos/OliverYang/metricGate/branches/main/protection   → 404

# workflow 列表：6 个，无 merge
GET /repos/OliverYang/metricGate/actions/workflows
  autopr / gate / review / schedule / seal / triage

# label 列表：只有 GitHub 默认 9 个
GET /repos/OliverYang/metricGate/labels
```

### §6.3 结论：MCP 能不能承担研发工作流

**能，但只承担"读"这一半，而且这一半已经完整。**

- 判断"这个 PR 能不能合"所需的一切（check-runs、mergeable_state、head sha、变更规模、review 结论）
  都能从 MCP 读到，不需要 `gh` CLI，不需要 PAT 权限之外的任何东西。
- 但"合"这个动作**不应该**交给 MCP。理由不是能力不足（`merge_pull_request` 就在工具列表里），
  而是 `merge.yml` 已经把判据写成了仓库里的、可 diff 审查的代码；让一个能被 prompt 影响的
  本地 agent 去执行同一件事，等于用一个不确定的门禁替换一个确定的门禁。
- MCP 真正的增量价值在**领任务**：`list_issues` + `issue_read` 让 `scripts/agent-claim.sh` 从
  「人工给 issue 编号」升级成「自动列出 `label=agent` 未分配 issue」。
  §5.2 的 label 缺口已修，这条路现在通了。

## §7 第二轮：两段断链的实际接法（2026-10-17 晚）

任务：`.trellis/tasks/10-17-merge-triage-loop`（prd + implement，已 start）。

### §7.1 顺序约束（这条比代码重要）

```
R1 label 自举（本地改代码）
  ↓ 必须先进 main，否则 triage 跑起来仍然 422
R2 合并 PR #1（把 merge.yml + triage.yml 改动一起带进 main）
  ↓ push to main 自动触发
R3 seal 打 v0.2.0
```

把 label 自举**搭 PR #1 这趟车**，而不是合并完再开第二个 PR：后者需要“第一次合并”
再发生一次，而那时 merge.yml 已在 main 上、第二个 PR 会被自动合并 —— 也能验证 §7.2，
但多一次往返。先改再合更省。

### §7.2 首次合并（MCP 执行）

合并前的复验（任一项不成立就停）：

| 项 | 值 |
|---|---|
| `state` | `open` |
| `merged` | `false` |
| `draft` | `false` |
| `mergeable_state` | `clean` |
| `head.sha` | `71a3b26402d15852468e1ba73f34d97df123ee2d` |
| `base.ref` | `main` |
| commits / files | 19 / 16 / +1448 −100 |
| check-runs @`71a3b26` | `open` ✅ / `gate` ✅ / `review` ✅（3/3 completed success） |

调用：

```js
mcp__github__merge_pull_request({
  owner: "OliverYang", repo: "metricGate", pullNumber: 1,
  merge_method: "squash",
  expectedHeadSha: "71a3b26402d15852468e1ba73f34d97df123ee2d",
})
// → {"sha":"f836511314ae85642ce6f5bb7c1c5ec6dfc38a70","merged":true}
```

`expectedHeadSha` 与 `merge.yml` 里钉 `sha: pr.head.sha` 是同一个理由：
如果中间有 push 落地，合并应被拒绝，而不是合一个没人审过的 commit。

**关键细节：PR #1 自己的合并不会触发 `merge`。** 触发时 `merge.yml` 还不存在于 main 上
（`workflow_run` 只对默认分支上已存在的 workflow 生效）。所以 workflow run 列表里
`merge` 的 run 数是 0 —— 这是**预期行为，不是失败**。真正生效的判据是下一节。

### §7.3 生效判据：`merge` 已注册为可触发 workflow

```bash
GET /repos/OliverYang/metricGate/actions/workflows
# → ['autopr', 'gate', 'merge', 'review', 'schedule', 'seal', 'triage']
```

`merge` 出现在这个列表里，就是 `workflow_run` 能触发的**必要条件**。
充分条件的判据：**下一次有非 `main` 分支的 PR、且 `gate` 与 `review` 都绿时，
`merge` 应自动把它 squash 合并。** 本轮不制造空 PR 去验证（会污染历史），
所以这条留待下一个真实 PR 兑现。

### §7.4 封板实测

`push` to `main`（`f8365113`）→ `seal` 自动跑 → 4 次轮询（约 60s）后 `completed success`。

| 项 | 值 |
|---|---|
| `main` HEAD | `f836511314ae85642ce6f5bb7c1c5ec6dfc38a70` |
| tags | `v0.2.0` → `f8365113`；`v0.1.0` → `d191d6b`（未动） |
| Release `v0.2.0` | errors **0** / warnings 415 / 24598 snapshots / PASSED |
| 二进制 sha256 | `80d47dc7c59e1d5ffae355a988dc6c58b1a32fde66afd150de99352ddca55a54` |
| PR #1 | `state: closed` / `merged: true` / `merge_commit: f8365113` / `merged_at: 2026-10-17T12:00:43Z` |

warnings 从 v0.1.0 的 413 升到 415：本轮新增的两个文件（`agent-claim.sh` 的
`ensure_label` / `ensure_labels`）带来的函数长度与参数计数，不是回退。

### §7.5 怎么证明 label 自举真的会创建（而不只是“没报错”）

第一次 dispatch triage 时，扫描结果是 **0 errors**，所以建 issue 的循环一次都没进，
`createLabel` 分支根本没被执行。**“跑绿了”不能当作“自举生效”的证据。**

于是：

```bash
DELETE /repos/OliverYang/metricGate/labels/agent   # 204，label 列表回到 12 个
POST   /repos/OliverYang/metricGate/actions/workflows/triage.yml/dispatches  # 204
# 等待 ~25s → completed success
```

job 日志里出现（时间戳 `2026-10-17T12:04:32`）：

```
scan produced 0 error-severity finding(s)
created label agent
```

label 列表回到 13 个。**删掉再跑**是这里唯一可信的验证方式：只要 label 还在，
自举的创建分支就永远是死代码。

### §7.6 仍开着的三件事

1. **分支保护仍未配**（§5.1，404）。`merge.yml` 不依赖它，但人工点合并时 GitHub 不拦红 PR。
2. **`triage` 只跑在 cron + dispatch 上**，从未在 push/PR 上跑过。这意味着新引入的 error
   要等到次日 03:00 UTC 才会变成 issue。是否加 `push` 触发是下一个决策点
   —— 加会让每次 push 都扫全树（贵），不加则发现延迟一天。
3. **`test-no-assert` 8 条误报仍在**（§3.1，B5 未做）。

## §8 第三轮：自动合并之后才发现的两个断点

§7 的结论是"两段断链已接上"。这句话在**手动合并**的路径上成立，在**自动合并**的路径上
还差两步。§7 之所以没暴露它们，是因为 §7 的合并是 MCP 用 PAT 做的，而 PAT 的行为
和 `GITHUB_TOKEN` 不一样。**这两步只有在真的让 `merge.yml` 自己合一次才会出现。**

### §8.1 断点一：合并竞态会把 `open` check 弄红，然后自我死锁

`autopr.yml` 在 `pulls.create` 之前会先 `pulls.list` 查有没有已存在的 PR（`autopr.yml:63-72`），
但**查和建是两次 API 调用**，中间任何东西都能插进来。

实测（PR #3）：

| 事件 | 时间 |
|---|---|
| PR #3 由 API 调用创建 | `12:10:47Z` |
| `autopr` 在 `pulls.create` 上失败：`Validation Failed: A pull request already exists for OliverYang:docs/iter27-evidence` | `12:10:47.247Z` |
| `merge` 拒绝合并：`no merge: other checks are red: open=failure` | `12:11:27Z` |

同一个秒。而 `merge.yml` 是 **fail-closed**：任何一个 check 红就不合。于是这个竞态
自己把自己锁死 —— 竞态创建了 PR，竞态把 `open` 弄红，`merge.yml` 于是拒绝合并这个
刚刚出现的 PR。而 `gate` 与 `review` 都是绿的。

**修法**（`fix(autopr): a lost create race is not a failure`）：`422` 且消息含
`already exists` 时，重新读一次 PR 列表，报告哪个 PR 已经盯上这个分支，然后正常返回。
理由不是"少一个红叉"：`open` 这个 check 的存在意义就是告诉 `merge.yml` 可以合，
而"PR 已经存在"恰恰是它想要的状态。

### §8.2 断点二：`GITHUB_TOKEN` 合并不产生 `push` 事件，所以 `seal` 从不运行

这是本轮最重要的一条，也是最难自己发现的一条。

`merge.yml` 最后一行日志写的是：

```
merged PR #3 (c89c538...); seal.yml takes it from here.
```

**它没有。** 实测：

```
main HEAD = f38a2599
total runs on that sha: 0        # 该 sha 上零个 workflow run，任何 workflow 都没有
```

原因写在 GitHub 文档里（`docs.github.com` › Actions › security › `github_token`）：

> When you use the repository's `GITHUB_TOKEN` to perform tasks, events triggered by
> the `GITHUB_TOKEN` will not create a new workflow run, with the following exceptions:
> `workflow_dispatch` and `repository_dispatch` events always create workflow runs.

`merge.yml` 用 `GITHUB_TOKEN` 合并 → 它对 main 的那次 push **不产生 `push` 事件** →
`seal.yml`（`on: push: branches: [main]`）**永远不会启动**。

失败形态是最坏的一种：**PR 合了、所有 check 全绿、run 也是绿的、Release 从来没发生。**
没有任何东西变红，所以没有任何东西会来问。

**为什么 §7 没发现**：`v0.2.0` 那次合并是 **MCP 用 PAT** 做的。PAT 是真人身份，
它的 push 是真实用户事件，`seal` 正常跑了。**"它跑过一次"这个事实，恰恰是这个 bug
看起来不存在的原因。** 只有让 `merge.yml` 自己合一次（PR #3、PR #4），缺口才显形。

**修法**：合并之后显式 `createWorkflowDispatch('seal.yml', ref: 'main')`，并给 job 加
`actions: write`（原来只有 `contents` / `pull-requests` / `checks`）。
必须用 dispatch 而不是"让 seal 也监听别的事件"，因为**根本没有 push 事件可监听**。
dispatch 失败不把 job 判红（合并已经落地了，把它报成失败是错的），只 `core.warning`
并给出人工恢复命令。

**已验证修法生效**（PR #6 合并后）：

```
main HEAD = 104a1100
total runs on that sha: 1
  seal  event=workflow_dispatch  completed/success
```

`event=workflow_dispatch` 而不是 `push` —— 这正是修法在起作用的样子：没有 push 事件，
是 `merge.yml` 自己把 `seal` 拉起来的。`seal` 跑完的判定是第三态（跳过而非失败）：

```
v0.2.0 already seals f836511314ae; this tree (104a1100589b) is NOT sealed
```

即 tag 已被别的 commit 占用 → 跳过并说明。这正是 §7.4 里设计的三种状态之一，
版本号没 bump 时不重复封板、也不把 main 弄红。

> 注意这里有一个容易搞错的地方：`seal` **跑起来了**（这是 §8.2 修的）与 `seal` **封了板**
> （需要 bump `toolVersion`）是两件事。修 §8.2 让前者不再静默丢失；后者仍然要求
> 每次发版都改 `cmd/metricgate/main.go:42` 的 `var toolVersion`。

### §8.3 三个断点的共同形状

§7 的 label 缺口、§8.1 的竞态、§8.2 的 `GITHUB_TOKEN` 静默，看起来是三件不同的事，
但判据是同一条：

> **"跑绿了"不等于"这段链路通了"。**

- label 自举：第一次 dispatch 扫描 0 errors，`createLabel` 分支是**死代码**，
  跑绿说明不了任何事 —— 删掉 label 再跑才算（§7.5）。
- 竞态：`gate` / `review` 绿、PR 存在、run 绿，但 `open` 红，链路死锁。
- `GITHUB_TOKEN`：PR 合了、check 全绿、run 绿，**该发生的 Release 没发生**。

三次都是"绿灯掩盖了断点"。所以这一轮的验收方式不是看 run 的颜色，而是**指定一个
具体的产物并去找到它**：`v0.2.0` 这个 tag 指向哪个 commit、`f38a2599` 上有几个 run、
删掉 label 之后日志里有没有 `created label`。找不到产物就是断的，run 再绿也一样。

### §8.4 本轮的重复 PR 问题（未修）

PR #4 与 PR #5 是同一个分支、同一个 head、同一秒（`12:21:45Z`）创建的两个 PR：
autopr 建一个，MCP 建一个。§8.1 的修法让**丢竞态的那一方不再报错**，
但它**阻止不了重复 PR 本身** —— 两次 `pulls.create` 都成功了。

彻底修需要二选一：让 agent 永远不自己建 PR（只推分支，交给 autopr），
或者给分支名加锁。本轮没做，因为触发条件是"人/agent 和 autopr 同时建 PR"，
正常流程里只有一方会建。**但如果以后 agent 频繁自己开 PR，这个会再犯。**


