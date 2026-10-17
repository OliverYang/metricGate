# 迭代 26 · post-edit hook 发货（S2：把裁决送到模型眼前）

日期：2026-10-16

编号说明：本文占用 `iter-26`，因为调研文档 §7 排期表里的 `iter-21` / `iter-22` / `iter-23` 三个序号已被**仍未开工**的工作占着。本文对应的那一行在表里叫「**iter-20（短）**」，也就是 `hooks/post-edit.sh` 发货（S2）——
但 `iter-20` 这个文件号已被 D2 棘轮对齐占用（见 `iter-20-mcp-cli-ratchet-parity.md` 开头的编号说明，那里明确写着「本轮按目标合同占用 `iter-20` 这个日志序号，**S2 仍未做**」）。本文就是把那个 S2 补上。
见 [产品演进方向调研 §7](../research/product-evolution-directions-2026-10.md)。

上游：`docs/research/product-evolution-directions-2026-10.md` §7「iter-20（短）」行（S2）；
方案原文已在 `docs/workflow.md:106-137`（§3.2「agent harness 的 post-edit hook」）
前置：无（纯分发面投入，不依赖 S1/D4）

## §0 一句话目标

**让 agent 在每次 Edit/Write 之后立刻看到闸门裁决**，而不是等到 commit 时才发现。

这是本仓**性价比最高的分发面投入**：闸门本身已经做完（diff-scoped、`-arch-base`、
`writeJSONError`、pre-commit 已挂），缺的只是把它接到 agent 的编辑循环上。
现状是：`ls hooks/` 只有 `pre-commit`，`find . -name 'post-edit*'` 为空 ⇒ 模型写坏代码、
跑完整个任务、才在 commit 那一刻被告知。

## §1 为什么是 hook，而不是「让模型记得跑」

| 方案 | 问题 |
|---|---|
| 写进 AGENTS.md 让模型自觉 | 已实测失效：本会话的 worker 就被要求「每步跑 check_code」而整轮零调用（`.metricgate/mcp-calls.jsonl` 最后一条早于 worker 启动 41 分钟） |
| MCP 工具（`check_code`） | 依赖模型主动调；同上一轮实测，子 agent 没有 MCP 工具 |
| **post-edit hook** | **由 harness 触发，不依赖模型意志**；`exit 2` 把 stderr 送进模型的下一轮上下文 |

**关键语义（方案原文已写明，本轮必须原样保留）**：`PostToolUse` **不能**阻止已经执行的编辑，
`exit 2` 的作用是**把 stderr 展示给模型**。这正是我们要的——编辑已经发生，
我们要的是模型看到裁决并去修。**不要把它宣传成「拦截」**，那是它做不到的事。

## §2 方案原文（`docs/workflow.md:106-137`，逐字引用）

`hooks/post-edit.sh`：

```sh
#!/bin/sh
# hooks/post-edit.sh — 把闸门的输出送回模型眼前
out=$(metricgate check . -diff 2>&1) || {
    printf '%s\n' "$out" >&2
    exit 2
}
exit 0
```

Claude Code 的 `settings.json`：

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write",
        "hooks": [{ "type": "command", "command": "/abs/path/hooks/post-edit.sh" }]
      }
    ]
  }
}
```

## §3 草图的两个问题（本轮必须修，不能照抄发货）

### 3.1 `metricgate` 不在 PATH 时静默不检查

草图直接用 `metricgate`，与 `hooks/pre-commit:18-31` 的健壮写法不一致。pre-commit 的做法是
`bin=${METRICGATE:-metricgate}` → `command -v` 失败则回落 `$PWD/metricgate` → 都没有则
**打印一行说明并 `exit 0`**（文档化的降级，不是沉默）。

post-edit 必须照抄这套，否则 fresh clone 里 hook 每次编辑都静默通过，**看起来在工作实际没有**。

### 3.2 全树 `check .` 太贵，且每次编辑都写库

实测（本仓 2.4 GB 库，已构建二进制）：

| 命令 | real | snapshots |
|---|---|---|
| `./metricgate check .` | 3.7–4.4 s | 24,388 |
| `./metricgate check . -diff` | **4.40–4.93 s** | 24,388 |
| `./metricgate check internal/cockpit/errors.go` | **1.26–1.31 s** | 10 |

（本表为收口时复测值。开工时记的 `check .` 1.53–2.30 s 是同一台机在**低负载**下的读数，
收口时机器 load average ≈18 使全树涨到 3.7–4.4 s —— 这正是判据不能只靠墙钟的原因，
见 §10。§4 表里 A 的「~0.1 s」也偏乐观，实测单文件是 1.3 s。）

每次编辑跑全树 = 1.5–2.3 s + **一条新 run 入库**（实测 `SELECT COUNT(*) FROM runs` 从 1376
涨到 1382，只因为我跑了 6 次 check）。一个 20 次编辑的会话会往 2.4 GB 的库里再塞 20 条 run
——与 iter-25 的保留策略直接对冲。

**改法（三选一，见 §4 决策点）**：

- **A. 编辑后只查被改的文件**：`metricgate check <file>`（实测支持单路径，`cmd/metricgate/check_cmd.go:84-85`）。
  最便宜（10 snapshots），但丢掉了跨文件规则（层违规、fan-out 需要全树）。
- **B. 保留全树但关 store**：`check` 无 `-no-store` flag（实测 `check -h` 无 store 选项），
  要靠 `.metricgate.yaml` 的 `store.enabled` 或临时配置——**会与仓库共享的配置打架**。
- **C. 全树 + store，但只在本机 opt-in**：把「写库」当成本地开发者的选择，
  hook 默认走 A。

## §4 决策点（阻塞项）

**口径必须由人定**，因为它决定 hook 是「快而窄」还是「全而慢」：

| 选项 | 延迟 | 覆盖 | 写库 |
|---|---|---|---|
| **A（推荐，已采纳）** `check <改过的文件>` | **1.26–1.31 s** | 单文件规则 | 10 snapshots/次 |
| B `check . -diff`（方案原文） | **4.40–4.93 s** | 全树 + 只报改动行 | 24k snapshots/次 |

**推荐 A 作为默认**，并在脚本注释里写明「跨文件规则（层违规、fan-out）不在 post-edit 覆盖内，
它们由 pre-commit 的 `check . -diff` 兜底」。理由：post-edit 的价值是**快速反馈循环**，
4.4–4.9 s 的延迟会让模型开始绕开它；而 pre-commit 已经在做全树收口，两者分工清晰。
**收口结果：用户确认采纳 A。**

**注意 A 需要拿到「刚被改的文件」**——Claude Code 的 `PostToolUse` 会把工具输入
（含 `file_path`）通过 stdin 以 JSON 传给 hook。脚本要读 stdin 取 `file_path`，
取不到时**回落全树 `check . -diff`**（宁可慢也不要漏）。

## §5 逐任务改法

| ID | 改动 | 落点 | 角色 |
|---|---|---|---|
| T26.1 | 写 `hooks/post-edit.sh`：bin 回落链（抄 pre-commit）+ 读 stdin 取 `file_path` + 单文件 check + 失败 `exit 2` | `hooks/post-edit.sh`（新建，`chmod +x`） | 0.25d |
| T26.2 | 在 `docs/workflow.md` §3.2 把草图替换成实际发货版本，并写明 A 口径与「不覆盖跨文件规则」的边界 | `docs/workflow.md:106-137` | 0.25d |
| T26.3 | 仓库内提供一个**可被 harness 复用**的示例：`.claude/settings.json.example`（**不启用** —— 用户决策：本仓 clone 后不应自动生效） | 新建 | 0.25d |
| T26.4 | 验证脚本：模拟 harness 调用（`echo '{"tool_input":{"file_path":"…"}}' \| hooks/post-edit.sh`），断言 exit 0/2 与 stderr 内容 | `hooks/post-edit_test.sh`（新建，**12 条断言**） | 0.25d |
| T26.5 | `README.md` 的接入段落补一行「post-edit hook 已发货，见 docs/workflow.md §3.2」 | `README.md` | 0.25d |

**合计 1.25 人日。**

## §6 本轮明确不做

- **不为每个 harness 都写适配**（Cursor / Codex / Claude Code 的 hook 格式各不相同）。
  本轮只发 `hooks/post-edit.sh` 这个**纯 shell 入口** + 一个 Claude Code 示例；
  其它 harness 由使用者自己接——这正是方案原文的意图（「给 hook 一个纯 shell 的入口，
  剩下的交给 harness」）。
- **不在 hook 里做 `git add` / 自动修复**：它只报告。
- **不把 hook 装到本仓的 `.git/hooks/`**（post-edit 不是 git hook，装错位置等于没装）。
- **不改 pre-commit**（它的 scoped check 根是 S1 的独立候选，另案）。
- **不改 Go 代码**（用户决策）：单文件 check 的 1.3 s 里包含 store 写入，
  「不写库」需要新的 `-no-store` flag，那是另一轮的事。本轮只在脚本注释里指向 iter-25。

## §7 风险

| # | 风险 | 缓解 |
|---|---|---|
| R1 | hook 静默失效（bin 找不到 / stdin 格式不符）看起来像在工作 | §3.1 的回落链 + §5 T26.4 的验证脚本；找不到 bin 时打印一行到 stderr |
| R2 | 每次编辑写一条 run，加速 2.4 GB 库膨胀 | §4 选 A（10 snapshots）；并在 `hooks/post-edit.sh` 注释里指向 iter-25 |
| R3 | `exit 2` 被误认为「编辑被拦下了」 | 脚本注释 + `docs/workflow.md` 明文写「编辑已经发生，这里只是把裁决给模型看」 |
| R4 | 单文件 check 给出「PASSED」让模型误以为全树干净 | stderr 输出里带一行 scope 声明：`post-edit: checked <file> only; cross-file rules run at pre-commit` |
| R5 | 与 `core.hooksPath=hooks` 的 pre-commit 冲突 | 两者文件名不同、触发者不同（harness vs git），无冲突；但要在 README 里说清区别 |

## §8 验收判据

- **D26-1**：`echo '{"tool_input":{"file_path":"internal/cockpit/errors.go"}}' | hooks/post-edit.sh; echo $?`
  → **exit 0**（干净文件）。
- **D26-2**：对一个人为引入 error 的文件 → **exit 2**，且 stderr 含 finding 原文（不是「失败」二字）。
- **D26-3**：**不传 stdin** 时回落全树 `check . -diff`，仍能给出裁决（不静默通过）。
- **D26-4**：`PATH` 里没有 `metricgate`、但仓库根有可执行 `metricgate` 时，hook 用它而不是跳过。
- **D26-5**：stderr 首行含 scope 声明（`checked <file> only` 或 `checked whole tree`）。
- **N-1**：`bash scripts/cockpit-observe.sh fast` 全绿；`go test ./...` 全绿（本轮不动 Go 代码）。
- **N-2**：`check_path hooks/ -diff` 0 error。

## §9 复现与实测命令

```bash
# 现状（本轮起点）
ls hooks/                              # 只有 pre-commit
find . -name 'post-edit*'              # 空

# 两种口径的实测代价（收口时复测，见 §10）
./metricgate check internal/cockpit/errors.go | grep summary   # 10 snapshots
./metricgate check . | grep summary                            # 24388 snapshots
for i in 1 2 3; do /usr/bin/time -p ./metricgate check . 2>&1 | grep '^real'; done   # 3.72 / 4.37 s（低负载时 2.30 / 1.53 / 1.64）

# 发货后的验收（本轮新增）
bash hooks/post-edit_test.sh                                   # 12 passed, 0 failed
echo '{"tool_input":{"file_path":"internal/cockpit/errors.go"}}' | hooks/post-edit.sh; echo $?   # exit 0

# check 是否支持单路径（决定 §4 选 A 可行）
grep -n 'NArg\|Arg(0)' cmd/metricgate/check_cmd.go   # :84-85 root = set.Arg(0)

# store 是否可关（决定 §4 选 B 是否可行）
go run ./cmd/metricgate check -h | grep -i store      # 无输出 ⇒ 无 flag
grep -n 'cfg.Store.Enabled' cmd/metricgate/check_cmd.go   # :441
```

## §10 收口记录

日期：2026-10-16。goal `mvb1w0dp-kd5w15`（3/3 tasks）。

**决策点（§4）由用户拍定：采纳 A（只查被改的文件）**，其余三个同轮决策：不碰 store
（守 §6 边界）、只发纯 shell 不带任何 harness 示例、示例文件**不启用**（`.example` 后缀）。

### 交付物

| 文件 | 内容 |
|---|---|
| `hooks/post-edit.sh`（新建，可执行，3.9 KB） | bin 回落链（抄 `hooks/pre-commit`）+ 读 stdin 的 `file_path` + 单文件 check + 全树回落 + scope 首行 + 只把 `gate: FAILED` 当违规 |
| `hooks/post-edit_test.sh`（新建，可执行） | 12 条断言，全在 `mktemp -d` 的临时仓里跑且 `store.enabled=false` |
| `.claude/settings.json.example`（新建） | 不启用的 Claude Code 接线示例，`"//"` 键存说明以保持 JSON 合法 |
| `docs/workflow.md` §3.2 | 草图整段替换为发货版本（口径 A + 不覆盖跨文件规则的边界 + 三条响亮降级） |
| `README.md:159-161` | 接入段落补 post-edit hook |
| `docs/README.md:13` | workflow.md 行补「post-edit 校验」 |

### §8 判据实测（`bash hooks/post-edit_test.sh`）

```
12 passed, 0 failed
```

- **D26-1** 干净文件（`internal/cockpit/errors.go`）→ exit 0，stderr 首行
  `post-edit: checked <file> only (cross-file rules: layering, fan-out, clones run at pre-commit)`
- **D26-2** 合成仓里 function-length 122（> error 阈值 100）→ exit 2，stderr 含 finding 原文
  `function-length of "Big" is 122 (threshold error 100)`
- **D26-3** 不传 stdin → 全树 `check . -diff`，仍给出裁决（不静默通过）
- **D26-4** `env -i PATH=/usr/bin:/bin`（PATH 里没有 metricgate）→ 回落 `$PWD/metricgate` 并真检查
- **D26-5** stderr 首行含 scope 声明（两种口径各测一次）
- **N-1** 本轮不动 Go 代码，`go build ./...` / `go test ./...` 不受影响
- **N-2** `check_path hooks/ -diff` 0 error

另加三条超出 §8 的断言（都是收口时踩出来的真问题）：缺二进制时**响亮**降级（`not found;
skipping the gate`）且 exit 0；被改文件已删除时不报假违规；干净运行**也**打印 scope 行（R4 的缓解）。

### 收口时踩到、并已修掉的三个真问题

1. **`exit 2` 会撞车**。`cmd/metricgate/main.go:137 fail()` 是 `os.Exit(2)`，而
   `gate: FAILED` 也是非零退出。只看退出码会把「库被锁住 / 不在 git 工作树里」这类
   **基础设施故障报告成「发现违规」**，模型会去追一个不存在的缺陷。
   修法：判据改为匹配输出里的 `gate: FAILED` 文本；非违规的非零退出打印
   `post-edit: the gate could not produce a verdict (exit N); not reporting a violation` 并 exit 0。
   （第一次实测就撞上了：并发写者让 `check` 返回 `metricgate: database is locked (5) (SQLITE_BUSY)`。）
2. **库没有 busy timeout**。`internal/store/store.go:89 Open` 用 `sql.Open("sqlite", path)`，
   没带 `_busy_timeout` ⇒ 并发写者立刻失败而不是等待。post-edit hook 恰好就是第二个
   并发写者，所以这条值得记一笔（不属本轮范围，见 §6 不改 Go 代码）。
3. **`-diff` 需要真 git 仓**。验证脚本最初的 fixture 不是 git 仓，
   `check . -diff` 报 `. is not inside a git worktree (a repository is required for -diff)` ⇒
   回落路径没被测到。修法：fixture 里 `git init` + 一次 commit，`big.go` 故意留 untracked
   （`-diff` 会把 untracked 算进去，所以不需要 `git add`）。

### 实测数字（E1，本机，收口时）

| 命令 | real | snapshots |
|---|---|---|
| `./metricgate check internal/cockpit/errors.go` | 1.26 / 1.31 / 1.26 s | 10 |
| `./metricgate check .` | 3.72 / 4.37 s | 24,388 |
| `./metricgate check . -diff` | 4.93 / 4.70 / 4.40 s | 24,388 |
| hook 全路径（stdin → 单文件 check → scope 行） | 1.57 / 1.80 / 1.36 s | 10 |

⇒ 窄口径把每次编辑的成本从 4.4–4.9 s 降到 1.3–1.8 s，写入量从 ~24k 降到 10 snapshots。

### 遗留

- **跨文件规则不在 post-edit 覆盖内**（设计取舍，已写进脚本注释与 workflow.md）。
- **每次编辑仍写一条 run**（10 snapshots）：用户决定本轮不碰 store；
  真要消掉需给 `check` 加 `-no-store`（另案）。
- **其它 harness 未适配**（§6 明确不做）。
- **本仓自己不用 pi 的 hook 跑它**：pi 的 hook 面未勘察，本轮只发纯 shell 入口。
