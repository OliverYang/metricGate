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
- 不要为了让 verdict 变绿去改 `policy.yaml`、加 ignore 注释、或降低阈值。
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

给 hook 一个**纯 shell** 的入口，剩下的交给 harness：

```sh
#!/bin/sh
# hooks/post-edit.sh — 把闸门的输出送回模型眼前
out=$(metricgate check . -diff 2>&1) || {
    printf '%s\n' "$out" >&2
    exit 2
}
exit 0
```

以 Claude Code 为例（`settings.json`，格式取自其 hooks 文档）：

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

### 3.3 CI

仓库里已放好 `.github/workflows/metricgate.yml`，核心就一行：

```sh
./metricgate check . -diff -base "origin/${{ github.base_ref }}"
```

两个必须注意的点，都是本机实测出来的：

- **`fetch-depth: 0`**。默认浅克隆没有与目标分支的 merge-base，`-base` 解析不到，
  metricgate 会**硬失败并指名是哪个 ref**（而不是报一个干净的树）：
  ```
  metricgate: git changed paths: exit status 128 — "origin/nope" is not a commit
  this repository knows about; run `git fetch` (CI needs fetch-depth 0), or pass -base explicitly
  ```
  这条消息本身就是本轮修的：之前它会说 "requires a git repository"，把读的人
  引去找一个本来就装着的 git。
- **CI 跑的是 diff，不是全树**。本仓库全树检查带 1 个已接受的 error（见 `claims.md`），
  全树闸门会永远红在一个贡献者无从下手的地方。diff 闸门第一天就绿，之后只加严。

> 状态诚实说明：这个 workflow 文件**尚未在 GitHub 上真实运行过**（本机没有远程）。
> 其中 `-base` 的行为、退出码、失败消息都在本机用真实仓库验证过；
> 未验证的部分只有 GitHub Actions 自身的环境（setup-go、cgo 构建 tree-sitter）。
> 首次跑之前请按此判断，别把它当"已验证"。

## 4. 本地埋点：怎么知道它真的被调用了

这是 R5 的装置。**默认关闭，只写本地文件，不发任何网络请求，且永不记录代码。**

### 4.1 打开

```sh
metricgate-mcp -config policy.yaml -calllog .metricgate/mcp-calls.jsonl
# 或者
METRICGATE_CALLLOG=1 metricgate-mcp -config policy.yaml   # 1 表示用默认路径
```

MCP 客户端配置里的写法：

```json
{
  "mcpServers": {
    "metricgate": {
      "command": "/path/to/metricgate-mcp",
      "args": ["-config", "/path/to/policy.yaml", "-calllog", ".metricgate/mcp-calls.jsonl"]
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
