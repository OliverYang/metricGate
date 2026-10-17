# 把闸门接进 agent 循环（workflow）

> 前置：`claims.md` 是**唯一的说法来源**。本文只讲**接线**，不讲价值 ——
> 凡是这里出现的"能拦什么"，都能在台账里找到对应的一行。
>
> 最后更新：2026-09-28。本文所有命令都在本机跑过，输出是抄的，不是写的。

## 0. 为什么还缺这一层

前面所有文档都停在"引擎能做什么"：规则对了、指标对了、确定性对了、成本≈0。
但台账里 **#15（闸门在真实 agent 循环里被调用、findings 被修掉）是唯一还没测、
且决定量级的一条**：如果每个任务平均调用不到 1 次，那么"循环内通道"就只是理论。

接线是两件事：**让它被调用**（本文 §1–§3），以及**知道它有没有被调用**（§4）。
第二件事不是附加项 —— 没有它，第一件事的效果无法证伪，也无法证实。

## 1. 三个触点

| 时机 | 工具 | 为什么在这个时机 | 成本 |
|---|---|---|---|
| **生成前** | `get_constraints`、`find_symbols` | 约束要在写之前给，写完再给就只剩返工 | 0 token |
| **每次产出后** | `check_code`（内存，不落盘） | 这是**唯一**能对上文"声称复用/声称遵守"做裁决的时机 | 0 token，毫秒级 |
| **收尾** | `check_path`、`check_path -diff` | 单文件看不见二阶后果（依赖图、克隆、全局限额） | 143 文件 156ms |
| 委托评审 | `get_review_bundle` | 宿主模型要评审一个 diff 时，别把整个文件塞进上下文 | 0 token |

判据必须**机器可判**。人判的"看起来遵守了"不是判据，是印象 ——
台账 #8 的两次实测（Arm A、Arm E）都是"模型自己在注释里点名了既有实现，然后重写了一遍"。

## 2. agent 指令模板（可直接粘贴）

粘到 `AGENTS.md` / `CLAUDE.md` / 系统提示里。它是**请求**，不是机制：
台账 #4 说了一句提示就能把复用率从 0% 拉到 80%，但剩下那 20% 只有检查能给。

```markdown
## 代码约束闸门（metricgate MCP）

本仓库接了一个确定性闸门。它不是建议，是**完成判据的一部分**。

**动手前**（每次要新建符号、改签名、或选实现位置）：
- 调 `find_symbols` 查这个名字是否已存在。存在就复用，不要另起一个。
- 调 `get_constraints` 拿目标路径的精确签名和当前策略阈值，按它写。

**每次产出代码后**（包括你自己刚改的每一段）：
- 调 `check_code`（内存检查，毫秒级，0 token）。
  `verdict` 不是 `PASS` 就继续改，改完再查。
- 不要为了让 verdict 变绿去改 `.metricgate.yaml`、加 ignore 注释、或降低阈值。
  闸门报的是**需求**，不是障碍。

**收尾**：
- 跑 `metricgate check . -diff`。退出码非 0 就不算完成。

**判定"我做完了"的条件**：最后一次 `check_code` 的 verdict 是 `PASS`，
且 `metricgate check . -diff` 退出码为 0。两个都满足才算完成。
```

这段模板对应台账里能说的部分：

- "先查再写"对应 #8（提示是请求，检查是事实）与 #5（模型不知道你的禁用 API）；
- "改完再查"对应 #11（成本≈0，所以没有不查的理由）；
- "不要改 policy 让它变绿"对应 #5 的边界 —— 闸门的价值来自**组织约定**，
  绕开它就等于把约定删了；
- 完成判据对应 #10（逐字节一致、退出码可核对）。

## 3. 编辑后自动检查：三种接法

### 3.1 git pre-commit（通用，已在本机验证）

仓库里已经放好一份：`hooks/pre-commit`。

```sh
cp hooks/pre-commit .git/hooks/pre-commit && chmod +x .git/hooks/pre-commit
# 或者版本化、团队共享：
git config core.hooksPath hooks
```

实测三种情形（本机，临时仓库）：

| 情形 | 结果 |
|---|---|
| 仓库还没有任何提交（HEAD 未出生） | 打印 `no commits yet; skipping` 后**放行** |
| 提交里含 `os.Exit`（策略为 error 级） | **拦下**，HEAD 不动，退出码非 0 |
| 改成返回 error | 放行，提交成功 |

它用 `-diff` 而不是全树检查，理由是**可加严性**：全树检查在有历史债的仓库上
第一次就红、之后永远红，人就会开始 `--no-verify`；`-diff` 只报这次新增的，
第一天就是绿的，之后只可能越来越严。

两个"查不了"的分支都 `exit 0`（二进制不在 PATH、仓库还没有提交）。这是刻意的：
一个装不上工具的 hook 如果阻断所有提交，只会被删掉。但代价要说清楚 ——
**这两种情况下闸门等于没跑**，所以它把原因打在 stderr 上而不是静默通过。

> ⚠️ 一个真实踩到的坑：`banned-api` 默认是 **warning** 级。
> 只写 `banned-api:` 而不写 `banned-api-severity: error` 时，
> 闸门会报出 finding 然后**通过**（`fail_on: error` 只对 error 级生效）。
> 想让某条规则真的拦住提交，必须显式提级：
>
> ```yaml
> languages:
>   go:
>     banned-api-severity: error
>     banned-api:
>       - match: os.Exit
>         reason: return the error instead of exiting
> ```

### 3.2 agent harness 的 post-edit hook

**已发货**：`hooks/post-edit.sh`（纯 shell 入口，不绑定任何 harness）+ 一个不启用的
示例 `.claude/settings.json.example`。验证脚本 `hooks/post-edit_test.sh` 钉住下面 §8 的五条判据。

它做三件事：

1. **解析被改的文件**：harness 通过 stdin 传工具调用 JSON（`{"tool_input":{"file_path":"…"}}`），
   脚本用 `sed` 取出 `file_path`（不依赖 python/jq 存在）。
2. **只查那个文件**：`metricgate check <file>`。实测 **1.3 s / 10 snapshots**；
   作为对照，全树 `check . -diff` 是 **4.4–4.9 s / ~24k snapshots**。
3. **取不到路径时回落全树**：`check . -diff`。宁可慢也不静默跳过。

为什么默认窄口径（这是本轮唯一的决策点，见 [iter-26 §4](iterations/iter-26-post-edit-hook.md)）：
post-edit 的全部价值是**快速反馈循环**，每次编辑多等几秒就是模型开始绕开它的原因；
而 pre-commit 已经在做全树收口。代价要说清：

> **窄口径不覆盖跨文件规则** —— 层违规、fan-out、clone 检测需要全树视野，
> 它们由 `pre-commit` 的 `check . -diff` 兜底。

所以脚本在**每次**裁决（通过也一样）的 stderr 首行打印 scope，避免一个文件的
`PASSED` 被读成「整个仓库干净」：

```
post-edit: checked internal/cockpit/errors.go only (cross-file rules: layering, fan-out, clones run at pre-commit)
post-edit: checked whole tree with -diff
```

harness 接线（Claude Code 为例）：复制示例并替换绝对路径——

```bash
cp .claude/settings.json.example .claude/settings.json
sed -i '' "s|<ABS_PATH>|$PWD|" .claude/settings.json   # macOS；GNU sed 去掉 ''
```

> **为什么示例进得了 git 而 `settings.json` 进不去，以及一个坑**：`.gitignore`
> 里写的是 `!/.claude/` + `/.claude/*` + `!/.claude/settings.json.example` 三行。
> 只写 `!/.claude/settings.json.example` 是不够的 —— 开发者全局 gitignore
> （`core.excludesFile`）里有一条裸 `.claude`，Git 会先把整个目录**剪掉**，
> 位于其下的取反规则根本轮不到执行。必须先 `!/.claude/` 把目录本身放回来，
> 再忽略内容、再取反示例文件。这三行的顺序就是这三件事，少一行 `git add` 会
> 静默失败在目录级剪枝上。

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

语义要对齐：`PostToolUse` **不能**阻止已经执行的编辑，`exit 2` 的作用是把 stderr
展示给模型。这正是我们要的 —— 编辑已经发生，我们要的是模型**看到裁决并去修**。
**不要把它宣传成「拦截」**，那是它做不到的事。

三条降级路径都是**响亮**的（打印一行到 stderr 并 `exit 0`），不是沉默：找不到
`metricgate`（回落链同 `hooks/pre-commit`：`$METRICGATE` → `command -v` → `$PWD/metricgate`）、
被改的文件已不存在（改名/删除，查它只会得到 stat 错误，看起来像违规）、
以及**闸门无法给出裁决**时（例如库被并发写者锁住 `SQLITE_BUSY`，或不在 git 工作树里）——
最后这条特别重要：基础设施故障不能被报告成「发现违规」，否则模型会去追一个不存在的缺陷。
判据是**裁决文本**（`gate: FAILED`）而不是退出码，因为 `check` 对「无法裁决」
也返回 2（`cmd/metricgate/main.go:137 fail()` → `os.Exit(2)`），两者会撞车。

其它 harness（Cursor / Codex / …）的 hook 格式各不相同，本轮只发纯 shell 入口，
由使用者自己接。**post-edit 不是 git hook**，不要装进 `.git/hooks/`。

### 3.3 CI

仓库里有四个 workflow，分工见 **[github-setup.md](github-setup.md)**（含日常循环与故障排查）。
闸门本身的核心仍然只有一行：

```sh
./metricgate check . -diff -base "origin/${{ github.base_ref }}"
```

| 文件 | 何时 | 范围 |
| --- | --- | --- |
| `gate.yml` | PR / push to main | diff（required check + PR 注释 + SARIF） |
| `autopr.yml` | push 到非 main 分支 | 用 `GITHUB_TOKEN` 开 PR（本地只需 SSH） |
| `triage.yml` | 每日 | 全树 → 把 error findings 提成 issue（指纹去重） |
| `seal.yml` | push to main | 全树 → 绿则 tag + Release（封板） |
| `schedule.yml` | 每周 | 全树 report-only，出趋势 artifact |

两个必须注意的点，都是本机实测出来的：

- **`fetch-depth: 0`**。默认浅克隆没有与目标分支的 merge-base，`-base` 解析不到，
  metricgate 会**硬失败并指名是哪个 ref**（而不是报一个干净的树）：
  ```
  metricgate: git changed paths: exit status 128 — "origin/nope" is not a commit
  this repository knows about; run `git fetch` (CI needs fetch-depth 0), or pass -base explicitly
  ```
  这条消息本身就是本轮修的：之前它会说 "requires a git repository"，把读的人
  引去找一个本来就装着的 git。
- **CI 跑的是 diff，不是全树**。本仓库全树检查带已接受的债务（见 `claims.md`），
  全树闸门会永远红在一个贡献者无从下手的地方。diff 闸门第一天就绿，之后只加严。
  封板（`seal.yml`）才是全树，因为它认证的是"这棵树干净"。

> 状态诚实说明：四个 workflow 的**判据都在本机用真实仓库验证过**（`-base` 行为、
> 退出码、失败消息、triage 的指纹去重、seal 的版本读取），`scripts/*.sh` 与
> `hooks/pre-push` 也都实跑过。**尚未验证的只有 GitHub Actions 自身的运行环境**
> （setup-go、cgo 构建 tree-sitter、`GITHUB_TOKEN` 的权限边界）——那部分要等首次
> 真实推送后看 Actions 日志。首次跑之前请按此判断，别把它当"已验证"。

## 4. 本地埋点：怎么知道它真的被调用了

这是 R5 的装置。**默认关闭，只写本地文件，不发任何网络请求，且永不记录代码。**

### 4.1 打开

```sh
metricgate-mcp -config .metricgate.yaml -calllog .metricgate/mcp-calls.jsonl
# 或者
METRICGATE_CALLLOG=1 metricgate-mcp -config .metricgate.yaml   # 1 表示用默认路径
```

MCP 客户端配置里的写法：

```json
{
  "mcpServers": {
    "metricgate": {
      "command": "/path/to/metricgate-mcp",
      "args": ["-config", "/path/to/.metricgate.yaml", "-calllog", ".metricgate/mcp-calls.jsonl"]
    }
  }
}
```

默认路径是 `.metricgate/mcp-calls.jsonl` —— 和 `history.db` 同目录，且已被
`.gitignore` 覆盖，所以日志不会变成提交物。文件权限 0600。

### 4.2 读

```sh
metricgate calls
metricgate calls -log .metricgate/mcp-calls.jsonl -since 2026-09-01
metricgate calls -format json
```

下面这段是**真实输出**，来自本机一次完整的 stdio 会话（6 次调用，
`check_code` 先 FAIL 后 PASS，`check_path` 先 FAIL 后 PASS）：

```
calls: 6   sessions: 1   range: 2026-09-28T04:48:08Z .. 2026-09-28T04:48:08Z

phase    calls
before       1
explore      1
verify       4

tool             calls  median
check_code           2     0 ms
check_path           2     1 ms
get_constraints      1     0 ms
get_repo_map         1     0 ms

verification (the gate's own verdicts)
  verify calls               4
  clean                      2
  flagged                    2   intercept rate 50%
  tasks                      1   (one per MCP server process)
  flagged at least once      1
  resolved (last = PASS)     1   converge rate 100%
  clean first try            0
```

### 4.3 指标定义

| 指标 | 定义 | 为什么用它 |
|---|---|---|
| `intercept rate` | 非 PASS 的 verify 调用 / 全部 verify 调用 | "闸门有没有东西可说"。**不含** info 级 note —— 判据用的是闸门自己的 verdict，不重新数 finding |
| `converge rate` | 最后一次 verify 为 PASS 的任务 / 至少有一次非 PASS 的任务 | "说了之后有没有被修掉"。这是 R5 真正要看的东西 |
| `clean first try` | 全程 PASS 的任务数 | 分母的对照：一次都没被拦，说明约束已经内化，或者闸门没覆盖到 |
| `phase` 分布 | before / verify / review / explore | 区分"用闸门验证"和"用闸门翻仓库" |

计数永远和比率一起打印。一个没有分母的百分比会让人把"没跑过"读成"从不发生"。

### 4.3b trace：把一次任务的调用与运行串起来

一个 `session` 是进程级的，一个 `run` 是一次 check 级的，两者一对多。要让
"一次任务"的全部工具调用与全部 check 归到一起，需要第三把键：**trace**。
它由宿主（agent harness）在启动 MCP server 之前用环境变量设好，metricgate
只读不探测——工具不知道你用的是哪个模型，也不该去猜。

```sh
# 一次任务（一个 PR / 一个 prompt 版本 / 一个模型）开工前设一次：
export METRICGATE_TRACE_ID="task-2026-10-14-001"
# 可选：模型、提示词指纹、harness 等维度（逗号分隔的 key=value）：
export METRICGATE_TRACE_ATTRS="model=gpt-5,prompt_digest=sha256:ab12,harness=codex"
metricgate-mcp -config .metricgate.yaml -calllog .metricgate/mcp-calls.jsonl
```

- 白名单键：`model` / `prompt_digest` / `harness` / `agent` / `branch` /
  `tool_invocations`。**未知键被丢弃并在 `check` 的 stderr 上计数提示**，
  值超 256 字节按 rune 边界截断。
- **空 trace 是合法状态**：不设 env 时行为与以前逐字节一致，check 照常跑，
  只是这些 run 不属于任何 trace。
- 读回来：`metricgate trace <traceID>` 给出该 trace 的 runs + findings +
  属性 + MCP 调用；`metricgate trends -trace/-model/-prompt-digest` 按 trace
  或属性切窗口。MCP 侧对应 `get_trace` 与 `get_trends` 的同名参数。
- trace **只出现在输出侧，不参与判定**：同一份代码的结论不因 trace 而变。

### 4.4 已知失效模式（必须连这些一起读）

- **一个 MCP 服务进程 = 一个任务**，这是代理。一个会话里跑多个任务只算一个；
  一个会话在 agent 修完之前就结束，会被记成"未收敛"，即使闸门完全做对了它该做的。
- **记录的是意图不是结果**：`check_code` 的 PASS 说明这段代码干净，
  不说明它被写进了文件。日志看不见磁盘。
- **没有采样、没有基线**：它只回答"调用了几次、收敛了几次"。
  要回答"没有闸门时会怎样"，得跑 `ab-experiment.md` 里的 A/B（R2，未跑）。
- **不含上游 agent 的信息**：日志里没有 prompt、没有 token 数、没有任务描述。
  所以 `intercept rate` 不能和任何"每千 token"的指标直接相乘。

### 4.5 隐私边界（结构性，不是承诺）

- 只写本地文件；这个包里没有网络客户端，也没有远程端点可以"以后打开"。
- 记录字段：时间、会话 id、工具名、阶段、**定位串**（`path=` / `query=` / `name=`）、
  耗时、finding 计数、verdict、错误文本。
- **不记录** `code` 参数。定位串走的是一份**白名单**（`path`/`store_path`/`query`/
  `metric`/`name`），承载源码的 `Code` 字段不在名单里。
  有一条测试专门盯着这件事（`TestWrapToolRecordsVerdictAndNeverSource`）：
  一旦日志里出现源码片段，测试就红。
- 写日志失败**不会**让工具调用失败（`TestWrapToolSurvivesUnwritableLog`）。
  埋点把检查搞挂，比没有埋点更糟。

## 5. 复现

```sh
# 构建
go build -o metricgate ./cmd/metricgate
go build -o metricgate-mcp ./cmd/metricgate-mcp

# 1) 全树 / diff 闸门
./metricgate check .
./metricgate check . -diff
./metricgate check . -diff -base "$(git rev-parse HEAD~1)"

# 2) hook：见 hooks/pre-commit，先 cp 或 git config core.hooksPath hooks

# 3) 埋点：开一次真实 stdio 会话（任何 MCP 客户端，或 §4.2 的驱动方式），然后
./metricgate calls -log .metricgate/mcp-calls.jsonl

# 4) 测试（含端到端埋点与"不泄漏源码"两条）
go test ./cmd/metricgate-mcp/... ./internal/calllog/... ./internal/git/...
```

## 6. 与其它文档的关系

| 文档 | 分工 |
|---|---|
| `claims.md` | 说法台账。本文出现的每个"能拦什么"都在那里有一行 |
| `value-proof.md` | 方法论 + R5 的设计。本文是它的装置落地 |
| `ab-experiment.md` | R2 闭环 A/B（**未跑**）。本文 §4 的日志是它的输入端，不能替代它 |
| `capabilities.md` | 引擎能力清单。本文只讲怎么接、怎么量 |
| `evaluation-hint-and-cost.md` | 为什么"省探索 token"这条价值主张死了（所以本文的模板不卖这个） |
