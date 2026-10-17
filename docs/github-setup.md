# GitHub 工作流

本仓的 CI 与 agent 闭环。**一页读完，出问题先看最后一节。**

仓库：<https://github.com/OliverYang/metricGate>（默认分支 `main`）

---

## 0. 一句话拓扑

```
本地（SSH key，无 token）          GitHub（CI 用内置 GITHUB_TOKEN）
─────────────────────────         ────────────────────────────────────────
agent-claim.sh  领 issue
agent-work.sh   建 fix/issue-N-*  ──push──►  autopr.yml  开/更新 PR
   ↑ 改码                                      │
hooks/post-edit.sh（编辑后 1.3s）               ▼
hooks/pre-commit  （提交前 diff）           gate.yml    diff 闸门
agent-pr.sh      （推送前 diff）  ──push──►  review.yml  结构评审
                                                │
                                          merge.yml   两项都绿才合
                                                │
                                                ▼
                                          squash merge to main
                                                │
                              ┌──────────────────┴──────────────────┐
                              ▼                                     ▼
                        seal.yml                            triage.yml（每日）
                    整树闸门 → tag + Release            error findings → issue
                                                                │
                                                                └──► agent-claim.sh
```

`gate` 与 `review` 是**两个独立判据**：前者问「这次新增的行有没有越线」，后者问「这次改动有没有破坏调用方 / 依赖图 / 漏改克隆副本」。两者都不涉及模型，同一个 diff 永远得到同一个结论。

---

## 1. 一次性配置

### 1.1 SSH key（推拉代码）

```sh
# 1. 确认有 key
ls ~/.ssh/id_ed25519.pub

# 2. 把公钥内容贴到 https://github.com/settings/keys → New SSH key
cat ~/.ssh/id_ed25519.pub

# 3. 验证
ssh -T git@github.com
# 期望：Hi <user>! You've successfully authenticated, but GitHub does not provide shell access.
```

若 `Permission denied (publickey)`：

```sh
# 指定 key 试一次，确认是不是 key 选错了
ssh -i ~/.ssh/id_ed25519 -T git@github.com

# 若这条能过，把 host 规则写进 ~/.ssh/config
cat >> ~/.ssh/config <<'EOF'

Host github.com
  HostName github.com
  User git
  IdentityFile ~/.ssh/id_ed25519
  IdentitiesOnly yes
EOF
```

### 1.2 remote

```sh
git remote add origin git@github.com:OliverYang/metricGate.git
git fetch origin
```

### 1.3 gh（可选，只影响「读 issue 列表」）

```sh
gh auth login      # GitHub.com → HTTPS → Login with a web browser
```

**不做也能用**：`scripts/agent-claim.sh` 会降级，打印人工路径。
推代码用 SSH，与 `gh` 无关；建 PR 由 CI 做，也不需要本地 token。

### 1.4 hooks

```sh
./scripts/enable-hooks.sh           # 安装三个触点
./scripts/enable-hooks.sh --check   # 只看状态
./scripts/enable-hooks.sh --remove  # 撤销
```

它会做两件事：`git config core.hooksPath hooks`、从 `.claude/settings.json.example`
生成 `.claude/settings.json`（替换绝对路径）。

### 1.5 仓库设置（必做，否则 autopr/triage 静默失效）

**Settings → Actions → General → Workflow permissions** 必须是
**Read and write permissions**。

这是最容易漏的一步，因为失败是静默的：

- 仓库默认是 **Read repository contents**；
- workflow 里的 `permissions: pull-requests: write` **只能收窄、不能放大**；
- 于是 `GITHUB_TOKEN` 开 PR 时收到 403，`autopr` 报红或什么也没做，
  而 `gate`（只需要 `contents: read`）照样绿——看起来像 autopr 的 bug。

同一个页面顺手勾上 **Allow GitHub Actions to create and approve pull requests**
（有些账号策略把它单独关着）。

| 设置 | 位置 | 谁依赖它 |
| --- | --- | --- |
| Workflow permissions = Read and write | Actions → General | `autopr`（开 PR）、`triage`（建 issue）、`seal`（打 tag + Release） |
| Allow GitHub Actions to create and approve pull requests | 同上 | `autopr` |

**验证**：推一个分支，等 1 分钟，`git ls-remote origin 'refs/pull/*'` 应出现一条。
这是不需要 API token 的检查方式。

实测（2026-10）：两个开关勾上之后 `autopr` **仍然**报
`Unhandled error: HttpError: GitHub Actions is not permitted to create or approve pull requests.`
（run `39021394163`，失败步骤 `Open or update the pull request`）。
要么是开关没保存，要么是账号级策略覆盖了仓库设置。
`autopr.yml` 现在会在这种失败时把上表两个开关直接写进 job summary，不用再猜。

---

## 2. 日常循环

```sh
# 1. 看有什么可领
./scripts/agent-claim.sh

# 2. 领一条（打 in-progress 标签）
./scripts/agent-claim.sh --claim 42

# 3. 建分支 + 拿到 brief
./scripts/agent-work.sh 42 complexity-in-registry-test

# 4. 改代码
#    每次编辑后 hooks/post-edit.sh 会把该文件的 verdict 打给 agent（~1.3s）

# 5. 判据（与 CI 逐字一致）
./metricgate check . -diff -base origin/main

# 6. 提交 + 推送（autopr.yml 会开 PR）
./scripts/agent-pr.sh 42 "fix(test): split the parity table out of the test"
```

第 6 步会先跑一遍闸门；红了不推（除非 `--force-push` 且你说明理由）。

---

## 3. 六个 workflow

| 文件 | 触发 | 做什么 | 权限 |
| --- | --- | --- | --- |
| `gate.yml` | `push` 到**任意分支**（无 paths 过滤） | `check . -diff -base <base>`；往 check-run 与一条 sticky 注释（重复运行更新同一条，不堆叠）+ SARIF + artifact | `checks:write` `pull-requests:write` `contents:read` |
| `review.yml` | `push` 到**任意分支** | 结构评审：从 base ref **现场生成**三份基线再比（exported API / package 依赖图 / clone 漂移）。前两项阻塞，clone 漂移只警告；`allow-api-breaking` / `allow-arch-drift` 标签（从该分支的 open PR 读）可降级为警告 | `contents:read` `pull-requests:write` |
| `merge.yml` | `workflow_run`（gate 或 review 完成） | 读该 PR 上**全部** check，`gate` 与 `review` 都绿且无其它红/待跑 → `pulls.merge` squash；否则只记日志不动作 | `contents:write` `pull-requests:write` `checks:read` |
| `autopr.yml` | `push` 到非 main 分支 | 用 `GITHUB_TOKEN` 开 PR（已存在则跳过）；分支名 `fix/issue-N-*` 自动 `Closes #N`；403 时把「去 Settings 勾哪两个开关」写进 job summary | `contents:read` `pull-requests:write` `issues:write` |
| `triage.yml` | 每日 03:00 UTC；手动 | 整树扫描 → **只把 error** 提成 issue（label `metricgate,agent`）；已存在则追加评论不重复建 | `contents:read` `issues:write` |
| `seal.yml` | `push` → main；手动 | 整树闸门（含 `-arch-base`）→ 绿则打 tag + 建 Release，附闸门摘要与二进制 SHA256 | `contents:write` |
| `schedule.yml` | 每周一 02:00 UTC；手动 | 全树扫描，**report-only**，出趋势 artifact | `contents:read` |

### 为什么 review 现场生成基线，而不是读一个提交进仓的快照

提交进仓的基线回答的是「这棵树和上次祝福的那棵差多少」，把本 PR 的改动和它之前所有未评审的改动混在一起：一个 PR 会继承它没造成的历史漂移，于是永远绿不了。现场从 base ref 生成，回答的是评审真正关心的那个问题——**这次改动做了什么**。

### 为什么 merge 自己判，而不用 GitHub 原生 auto-merge

原生 auto-merge 等的是 **required checks**，而 required checks 存在分支保护里——workflow 的 token 看不见、也很容易没人配。**没配 required checks 时，armed auto-merge 会立刻合**，`gate` 还没开始跑就合掉了：本该保护合并的那个检查，恰恰是被跳过的那一个。把判断放进 `merge.yml`，规则就在仓库里、能 diff 评审、不依赖一个从代码里看不见的设置。

`merge.yml` 的触发是 `workflow_run` 而非 `check_suite`：`gate` 与 `review` 是两个 workflow，谁先跑完都不知道对方，所以它读该 PR 上**所有** check 来判断，先跑完的那个看到另一个还在跑就什么都不做，后跑完的那个看到两个都绿才合。

它按 `pr.head.sha` / `workflow_run.head_sha` / `refs/pull/N/head` / `refs/pull/N/merge` 四个 ref 取 check 并集——`pull_request` 触发的 workflow 把 check 挂在 merge commit 上，只读一个 ref 会让 required check 永远「未上报」，PR 永远合不了。

合并时 `sha: pr.head.sha` 钉住被评审的那个提交：如果读 check 和合并之间有人 push 了新提交，合并被拒，而不是合进一个没人评审的版本。

### 为什么 gate 与 review 由 push 触发，而不是 pull_request

**用 `GITHUB_TOKEN` 开出来的 PR，不会触发自己的 workflow。** GitHub 文档原文：

> When a workflow using `GITHUB_TOKEN` creates or updates a pull request, the resulting `pull_request` event creates workflow runs in an **approval-required** state.

`autopr.yml` 就是用这个 token 开 PR 的，于是 `pull_request` 触发的 gate 与 review 在每一个自建 PR 上都停在 `action_required`，等人点「Approve workflows to run」。2026-10-17 在 PR #1 上实测：run `39026847781`（gate）/ `39026847835`（review）的 job 数为 **0**，该 commit 的 check-run 里**只有 autopr 自己的 `open`**，gate 与 review 根本没上报——而一个永远不上报的 required check 会让 PR 永远合不了。

**push 带 actor**，`git push` 启动的 run 不是 bot 创建的，不需要任何批准。**check 挂在 commit 上，不挂在 PR 上**，所以门禁 push 的结论照样落在 PR 的 head SHA 上。同一个 commit 用真人身份推一次，gate 与 review 立刻 `success`，没有任何批准步骤（run `39026945200` / `39026945207`）。

代价与对应处理：

- push 事件里没有 `github.event.pull_request`，所以两个 workflow 改成**按分支名查 open PR**（`pulls.list({head})`）来拿 PR 号与标签。
- gate 在这棵树上几秒就跑完，而 `autopr` 开 PR 要更久。gate 会**最多等 60s** 轮询 PR 是否存在，否则每个分支的第一次 push 都会「找不到 PR」而不发注释。找不到也不算失败：check-run 本身已经带着结论。
- `merge.yml` 的 `workflow_run` 条件里不能要求 `event == 'pull_request'`（现在它是 `push`），改成 `head_branch != 'main'` + 同仓守卫。
- **`workflow_run` 只在 workflow 文件存在于 default branch 时触发**，所以 `merge.yml` 要等它自己合进 main 之后的**下一次**才真正开始工作。

### 为什么 gate 没有 paths 过滤

**永不运行的 required check 不是「跳过」，是「pending」**，于是被它保护的分支永远合不掉一个被过滤掉的改动。第一版在 `pull_request` 上也加了 paths，结果一个只改文档的 PR 卡死等一个永远不会启动的 gate。

第二版在 `push` 上按 21 个后缀手写 paths，还有第二个失败模式：往 `internal/parser/parser.go` 加一门语言时，没有人记得回来补这个列表，于是新语言静默地不过闸门。diff 闸门在这棵树上约 2s，所以现在每个 push 都跑，没碰源码时报告「no new violations」。

`push` → main 保留 paths 过滤：那里 seal 已经整树闸门了，文档合并不需要第二次全量。

### 为什么 gate 与 seal 跑不同的范围

`gate.yml` 是 **diff-scoped**：只判本次改动新增的行。理由写在 `hooks/pre-commit`
注释里——全树闸门在有历史债的仓库上从第一天就是红的，红了就没人看，最后大家都
`--no-verify`。diff 闸门第一天就绿，之后只加严。

`seal.yml` 是 **whole-tree**：封板要认证的是"这棵树是干净的"，不是"这次改动没添新债"。
两者不能互相替代。

### 为什么 triage 只提 error

当前整树有 419 条 warning。全提出来会把 error 埋掉，也会训练所有人忽略这个 label。
warning 留在 `schedule.yml` 的 artifact 里看趋势。

---

## 4. 去重与幂等

| 机制 | 键 | 位置 |
| --- | --- | --- |
| triage 不重复建 issue | `sha1(ruleId\|file\|startLine)[:12]`，写进 issue 正文的 `<!-- metricgate-fp: … -->` | `triage.yml` |
| PR 注释不堆叠 | `<!-- metricgate-gate-report -->` 标记，命中则 update 而非 create | `gate.yml` |
| autopr 不重复开 PR | 按 `head=owner:branch` 查 open PR | `autopr.yml` |
| seal 不移动 tag | tag 已存在（本地或远端）则失败并提示 bump `toolVersion` | `seal.yml` |

**为什么不用 message 做指纹**：message 里嵌着阈值（`is 23 (threshold warning 15)`），
阈值一调就变成新 issue。`ruleId|file|startLine` 只在违规真的移动时才变。

---

## 5. 封板

版本号只有一个来源：`cmd/metricgate/main.go` 的

```go
var toolVersion = "0.1.0"
```

它被声明成 `var` 而非 `const`，就是为了让 `-ldflags -X main.toolVersion=` 能覆盖
（见该行上方的注释）。`seal.yml` 从源码里读它，拼成 `v0.1.0`。

封一个新版本 = **改这一个值 + 合并进 main**。tag 已存在则 seal 失败，不会移动 tag
（两个 commit 不能都是 v0.1.0）。

Release 正文含：errors / warnings / metric snapshots / commit sha / 二进制 SHA256。

---

## 6. 故障排查

| 症状 | 原因 | 处理 |
| --- | --- | --- |
| `ssh -T git@github.com` → `Permission denied (publickey)` | 公钥没加到 GitHub，或 ssh 选错了 key | 见 §1.1。**先确认账号上到底是哪把 key**：`curl -s https://api.github.com/users/<user>/keys`，再 `ssh-keygen -lf ~/.ssh/id_rsa.pub` 对比指纹。本仓踩过：账号上注册的是 RSA，而 `~/.ssh/config` 里写了 ed25519 |
| push 成功但没 PR（`git ls-remote origin 'refs/pull/*'` 为空） | **Workflow permissions 是只读**（默认值） | 见 §1.5。`permissions:` 块只能收窄不能放大，所以这个失败在 workflow 文件里看不出来 |
| push 成功但没 PR（有 pull refs 但内容不对） | 分支与 main 无差异，或已有 open PR | `git log origin/main..HEAD` 应有内容；`autopr` 对同一 head 是幂等的 |
| PR 上没有 `gate` check | gate 与 review 是 `push` 触发（见 §3），而该分支从没被 push 过，或 run 还没开始 | 看 `Actions` 里该 commit 的 gate run；本地 `git ls-remote origin 'refs/heads/<branch>'` 确认分支在远端 |
| gate / review run 是 `action_required` 且 job 数为 0 | 这个 PR 的 `pull_request` 事件是 `GITHUB_TOKEN` 产生的（旧版 workflow 用 `pull_request` 触发） | 已改成 `push` 触发，见 §3。若还看到这种 run，它是改动落地前的旧 run |
| PR 上 `review` 红但看不出为什么 | `review.yml` 的 Review 步骤 `exit $fatal` 会让 job 红，同时把表格写进 PR 注释 | 看 PR 注释的 `<!-- metricgate-review-report -->`，或下载 `metricgate-review` artifact（含 `api-diff.json` / `arch-diff.json`） |
| PR 全绿但没自动合并 | `merge.yml` 只在前两项绿时合；`workflow_run` 要求 head 属于本仓库（fork PR 被拒）；**`workflow_run` 只在文件已存在于 default branch 时触发** | 看 `merge` run 的日志，它会打印 `no merge: <原因>`。若根本没有 merge run，先确认 `merge.yml` 已在 `main` 上 |
| gate 报 `"origin/main" is not a commit ... fetch-depth 0` | checkout 太浅，没有 merge-base | `gate.yml` 已固定 `fetch-depth: 0`；本地则 `git fetch origin` |
| triage 建了 0 条 issue | 整树 0 error（正常），或 label 已存在 | 看 run 的 summary 表 |
| seal 失败 `tag v0.1.0 already exists` | 版本没 bump | 改 `cmd/metricgate/main.go` 的 `toolVersion` 再合并 |
| 整树闸门红在 `.trellis` / `.pi` | 工具目录被当代码扫 | `.metricgate.yaml` 的 `exclude` 已含 `.trellis .pi .agents .claude`；若新增工具目录按同样方式加（**写裸段名，不是路径前缀**） |
| `pre-push` 说 `origin/main is not a known ref` | 没 fetch 过 | `git fetch origin` |

### exclude 的匹配规则（踩过坑）

`.metricgate.yaml` 的 `exclude` 按**路径段（basename）精确相等**匹配，不是前缀、不是 glob。

- ✅ `"third_party"` 匹配任意深度的 `third_party/` 目录
- ❌ `"docs/iterations/shots"` 什么都不匹配

依据：`internal/selection/selection.go` 的 `Decide` 对目录比 `filepath.Base`，对文件比每个祖先段。

---

## 7. 边界（明确不做）

- **不把 API key 放进 GitHub secrets**。代码修改在本地做，云端 CI 只做判断与记录。
- **不建 bot 账号**。issue 由 `github-actions[bot]` 建，PR 由同一个 bot 开；需要人味时在正文里写明来源。
- **不做无条件的自动合并**。`merge.yml` 只在 `gate` 与 `review` 都绿、且该 PR 上没有任何其它红/待跑 check 时才 squash merge；合并前把被评审的 commit sha 钉住。想拦下某个 PR，让它红，或加 label 走人工。
- **不改 `third_party/`**。它是 `go.mod` `replace` 指向上游语法的 vendored 副本，改了没法回上游。

---

## 8. 相关文档

- `docs/workflow.md` — 闸门接入 agent 循环的三个触点（更详细的背景与埋点）
- `hooks/pre-commit`、`hooks/pre-push`、`hooks/post-edit.sh` — 三个 hook 的注释就是它们的规格
- `docs/iterations/iter-26-post-edit-hook.md` — post-edit hook 的设计与实测代价
- `.github/actions/metricgate-check/action.yml` — 给**别的仓库**用的 composite action

---

## 9. 用 GitHub MCP 代替本地 PAT（可选）

需要 agent 直接读 issue / 开 PR / 看 check 时，用 GitHub 官方 MCP 而不是手造 PAT：

```sh
pi mcp add github --url https://api.githubcopilot.com/mcp/ \
  --description "GitHub API: issues, pull requests, branches, checks, and reviews"
pi mcp login github      # 浏览器 OAuth，token 存 ~/.pi/agent/mcp-auth.json
pi mcp list              # 应显示 connected
```

**为什么不用 PAT**：PAT 要手选 scope、会过期、要放进环境变量或 keychain；OAuth 由服务端签发与刷新，scope 由服务端按客户端登记决定。§7 的「不把 API key 放进 GitHub secrets」仍然成立——这是**本地** agent 的凭证，不进仓库、不进 CI。

注意 `pi mcp login` 需要浏览器交互，无法在无人值守的脚本里跑完。

### 9.1 一个会静默失效的坑：`pi mcp add` 自动塞进来的 Bearer header

`pi mcp add` 会写入：

```json
"headers": { "Authorization": "Bearer ${GITHUB_TOKEN}" }
```

这条 header 的后果不是报错，而是**悄悄改变认证方式**。pi 的规则是
（`pi` 文档 `docs/mcp.md:133` 原文）：

> OAuth applies to HTTP servers **without** an `Authorization` header.

带了 header，pi 就认为这是 token 认证，**不会**走 OAuth。于是：

| 场景 | 表现 |
|---|---|
| 环境里有 `GITHUB_TOKEN` | `connected` —— 但用的是 PAT，不是 OAuth |
| 环境里没有 `GITHUB_TOKEN` | `failed`：`Failed to resolve MCP server "github" header "Authorization" from environment variable: GITHUB_TOKEN` |
| 以为已经 OAuth 授权过 | `mcp-auth.json` 里**只有 discovery 元数据**，没有 token；删掉 header 立刻 `needs sign-in` |

**正确顺序（不能反）**：

```sh
pi mcp login github      # 1. 先真正完成 OAuth
pi mcp list              # 2. 确认 connected
# 3. 再从 ~/.pi/agent/mcp.json 的 github 条目删掉整个 "headers" 键
pi mcp list              # 4. 仍然 connected → 才算切到 OAuth
env -u GITHUB_TOKEN pi mcp list   # 5. 这一步才是真验证
```

先删 header 会直接掉线 —— 这就是 `docs/iterations/iter-27-manual-handoff-blockers.md` §2.4 的实测记录。

**若暂时不想走 OAuth**：保留 header 也可以，但要接受它不可跨机器复现，且不要把
`GITHUB_TOKEN` 同时留在 `secrets.env` 里 —— 两个凭证并存时，"能用"不能证明哪一条在工作。
