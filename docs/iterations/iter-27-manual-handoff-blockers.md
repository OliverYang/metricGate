# 迭代 27 · 两个手动阻塞项（凭证与合并动作）

日期：2026-10-17

编号说明：本文占用 `iter-27`。它不对应调研文档 §7 排期表里的任何一行计划 —— 表里
`iter-21` / `iter-22` / `iter-23` 三个序号仍被**未开工**的工作占着（见该文 §7.1），
而本文记录的是 `iter-26` 之后真实发生的一次交付阻塞。沿用 `iter-24`/`iter-25`/`iter-26` 的惯例：
**文件号是日志序号，不是计划行号**。

上游：`docs/github-setup.md`（§1.3 gh 认证、§1.5 仓库设置、§9 GitHub MCP）
关联：PR [#1](https://github.com/OliverYang/metricGate/pull/1)、`.github/workflows/merge.yml`、`.github/workflows/seal.yml`
状态：**B（MCP 授权）已解除；A（合并 PR #1）仍待人工**（不是缺陷，是能力边界）
修订：2026-10-17 晚，§2 全部按实测重写，§4 清单按实测勾选，新增 §6 实测矩阵

## §0 一句话

CI 链路已经全绿并推到远端。原本有两个阻塞项都落在人手：合并 PR 需要写权限的 GitHub 身份，
`github` MCP 需要浏览器 OAuth。

**§2 的凭证阻塞已解除** —— `github` MCP 现已 `connected, 46 tools`，本节保留是因为
"为什么它曾经挡住整条链"仍是有效知识，但结论要按 §2.2 的实测重读。
**§1 的合并阻塞仍然成立**，且本轮用 MCP 独立复核了一遍前置条件（见 §6.1）。

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

- [ ] **B1** 合并 PR #1（§1.3），确认 squash —— **仍未做**，`main` 还在 `d191d6b`
- [ ] **B2** 验证三件事（§1.4）—— **仍未做**：`merge` workflow 不在仓库列表、无 `v0.2.0` tag、无对应 Release
- [x] **B3** `pi mcp login github`（§2.3）—— **已可用**（46 tools），但走的是 Bearer header 路线，不是 OAuth。
      要换成 OAuth 需先跑 login 再删 header，顺序见 §2.5
- [ ] **B4** 用 MCP 读 `GET /branches/main/protection` —— **已实测：404，确认未配置**（不是 401）
- [ ] **B5** 决定 `test-no-assert` 的口径（§3.1），单开迭代 —— 未做
- [ ] **B6** （本轮新增）`triage` 的 label 自举缺失，见 §5.2 —— 未修
- [ ] **B7** （本轮新增）MCP 凭证路线二选一（§2.5）—— 当前停在 A 路线，可用但不可跨机复现

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

2. **`triage.yml` 从未成功跑过，且它缺 label 自举**（本轮新增发现）

   实测：

   ```
   $ curl .../actions/workflows/triage.yml/runs
   total: 0                       # 一次都没跑过（只在 schedule + workflow_dispatch 上）
   $ curl .../labels | grep -c 'metricgate\|agent'
   0                              # 仓库只有 GitHub 默认 9 个 label
   $ mcp__github__get_label(name: "metricgate")
   label 'metricgate' not found in OliverYang/metricGate
   ```

   而 `triage.yml:159` 在建 issue 时写死：

   ```js
   labels: ['metricgate', 'agent'],
   ```

   GitHub REST 对**不存在的 label** 返回 **422 Validation Failed**（不是静默忽略）。
   所以 triage 第一次真正跑起来时会在第一个 error 上就失败，而不是"建了 issue 但没打标签"。
   同样的问题也在 `scripts/agent-claim.sh`：它 `--claim N` 要打 `in-progress` label，那个也不存在。

   两条修法，**本轮都不做**（超出收口范围，且会动 workflow）：
   - 在 `triage.yml` / `agent-claim.sh` 里先 `gh label create … --force` 再使用（自举，幂等）
   - 或把 `labels` 参数去掉，改成建完 issue 后单独调用 `issues.addLabels` 并容忍 404

   建议：**前者**。label 是协议的一部分（`agent` = 可领，`in-progress` = 已领，
   见 `scripts/agent-claim.sh` 头注释），应当随仓库一起存在，而不是靠人记得手工建。

3. **`v0.2.0` 是否是你想要的版本号？** 本轮把 `toolVersion` 从 `0.1.0` 提到 `0.2.0`（见
   `cmd/metricgate/main.go:42` 的注释），理由是 v0.1.0 封的 commit 早于 cockpit workspace、
   语言对齐、review+merge 链路。若你想要 `v1.0.0` 或别的值，**在合并 PR #1 之前改**
   —— 合并之后 tag 就打出来了，而 tag 永不移动。

## §6 本轮 GitHub MCP 实测矩阵（2026-10-17 晚）

### §6.1 链路现状：通到哪一步，断在哪一步

| 环节 | workflow | 远端 run 数 | 最后状态 | 判定 |
|---|---|---|---|---|
| 分支推送 → 自动开 PR | `autopr` | 17 | ✅ success | **通** |
| PR diff 约束闸门 | `gate` | 9 | ✅ success | **通** |
| PR 审查（breaking API / 依赖漂移 / 单边 clone） | `review` | 8 | ✅ success | **通** |
| 合并 | `merge` | — | — | **断**：文件不在 main，`workflow_run` 不触发（§1.1） |
| 封板（tag + Release） | `seal` | 2 | ✅ success @ `d191d6b` | **通**，但已封过 `v0.1.0`，新代码未封 |
| 定时全树扫描（趋势） | `schedule` | 0 | — | 未触发（周扫，正常） |
| error → issue | `triage` | 0 | — | **断**：从未跑过 + label 不存在（§5.2） |

一句话：**"推送 → PR → 两道闸门全绿"这一段是活的；"合并 → 封板下一版"与"发现问题 → 建 issue"两段是断的，
而两处断因都已定位到具体文件的具体行，不是模糊的"链路不通"。**

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
  但这条路径现在被 §5.2 的 label 缺口堵着 —— **先修 label 自举，agent 领任务的闭环才真的成立**。

