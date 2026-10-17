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
hooks/pre-commit  （提交前 diff）           gate.yml     diff 闸门（required）
agent-pr.sh      （推送前 diff）  ──push──►      │
                                                 ▼
                                          merge to main
                                                 │
                              ┌──────────────────┴──────────────────┐
                              ▼                                     ▼
                        seal.yml                            triage.yml（每日）
                    整树闸门 → tag + Release            error findings → issue
                                                                │
                                                                └──► agent-claim.sh
```

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

## 3. 四个 workflow

| 文件 | 触发 | 做什么 | 权限 |
| --- | --- | --- | --- |
| `gate.yml` | `pull_request` → main；`push` → main | `check . -diff -base <base>`；PR 上发 required check + 一条 sticky 注释（重复运行更新同一条，不堆叠）+ SARIF + artifact | `checks:write` `pull-requests:write` `contents:read` |
| `autopr.yml` | `push` 到非 main 分支 | 用 `GITHUB_TOKEN` 开 PR（已存在则跳过）；分支名 `fix/issue-N-*` 自动 `Closes #N` | `contents:read` `pull-requests:write` `issues:write` |
| `triage.yml` | 每日 03:00 UTC；手动 | 整树扫描 → **只把 error** 提成 issue（label `metricgate,agent`）；已存在则追加评论不重复建 | `contents:read` `issues:write` |
| `seal.yml` | `push` → main；手动 | 整树闸门（含 `-arch-base`）→ 绿则打 tag + 建 Release，附闸门摘要与二进制 SHA256 | `contents:write` |
| `schedule.yml` | 每周一 02:00 UTC；手动 | 全树扫描，**report-only**，出趋势 artifact | `contents:read` |

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
| `ssh -T git@github.com` → `Permission denied (publickey)` | 公钥没加到 GitHub，或 ssh 选错了 key | 见 §1.1；`ssh -i ~/.ssh/id_ed25519 -T` 单独试 |
| push 成功但没 PR | `autopr.yml` 需要 `pull-requests: write`；或分支与 main 无差异 | Actions 页面看 `autopr` run 日志；`git log origin/main..HEAD` 应有内容 |
| PR 上没有 `gate` check | `paths:` 过滤掉了改动（只列了代码后缀与 `.metricgate.yaml`） | 改 `gate.yml` 的 `paths`，或改 `.metricgate.yaml` 触发 |
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
- **不做自动合并**。`gate` 是 required check，合并是人（或显式 `gh pr merge`）的动作。
- **不改 `third_party/`**。它是 `go.mod` `replace` 指向上游语法的 vendored 副本，改了没法回上游。

---

## 8. 相关文档

- `docs/workflow.md` — 闸门接入 agent 循环的三个触点（更详细的背景与埋点）
- `hooks/pre-commit`、`hooks/pre-push`、`hooks/post-edit.sh` — 三个 hook 的注释就是它们的规格
- `docs/iterations/iter-26-post-edit-hook.md` — post-edit hook 的设计与实测代价
- `.github/actions/metricgate-check/action.yml` — 给**别的仓库**用的 composite action
