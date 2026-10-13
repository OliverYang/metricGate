# 喂给 LLM 审查器：哪一段输出该给它

日期：2026-10-12（`/goal` 之后的 P2 项，`docs/claims.md` §外部对拍的落地建议）

> 本文**只写怎么用**，不引入代码依赖。新增任何 LLM SDK、`go.mod` 依赖、
> 或把 LLM 拉进闸门，都**不在本计划内**——那会动「零网络、零 key、确定性」
> 这条承重墙。

## 为什么值得写

2026-10-12 的 metricgate-vs-`codex review` 对拍（`docs/claims.md` §外部对拍）
结论是两者**问的问题不同**：

- MetricGate 问「这块代码有几个超阈值的度量，数值比 base 高了多少」
- LLM 审查器问「这次的逻辑对不对，有没有跨文件的语义分叉」

LLM 侧唯一真实的 P2（`OpenApiMsAuthInterceptor` 漏了 `splitSpace.shenwan`
用户组角色分支）是 MetricGate 的度量指标**结构上表达不了**的。反过来，
MetricGate 报出的 11 条 `metric-regression`（含 `npath 10836 → 32436`）
也不需要 LLM 来确认。

所以接缝不是「让 LLM 复核我们的 finding」，而是**先给 LLM 一个吃力的入口**：
它最缺的是「该看哪里、哪里变差了」，而这正是 `-diff` 数值 delta 已经算出来的。

## 三个可用入口，按成本的顺序

### 1. `check -diff -regress -format json`（零配置，推荐起点）

一条命令给出「本次改动让哪些 scope 变差了」：

```bash
metricgate check . -diff -base HEAD~1 -regress -format json
```

`findings` 里 `ruleId == "metric-regression"` 的项就是喂 LLM 的素材。实测在
`gitee-scan-2.0-portal` 的一个提交上产出 11 条，每条形如：

```
cyclomatic-complexity of "taskIssues" rose 17 → 19 (regression vs base)
npath-complexity of "mistakes" rose 10836 → 32436 (regression vs base)
```

把它拼成 prompt 比让 LLM 从零读 diff 便宜：它可以直接跳到 `taskIssues`
/`mistakes` 两个方法，而不是在 1187 个文件里找改动。

**用法**：把 JSON 的 findings 数组连同 `-format text` 的 summary 一起传给
审查器即可。`message` 里带 scope 名、base 值、当前值。

### 2. `metricgate bundle`（预算受控的生成前/审查前 grounding）

`bundle` 是确定性、预算封顶的审查输入信封：约束 + 选定文件内容 + diff，
`max-chars` 硬约束。**实测口径**：提交级模式在本仓可用
（`bundle -commit <sha> -max-chars 6000` 直接产出 JSON 信封）；
包级模式（`bundle internal/engine`）连 20000 units 都装不下——
约束节自身就超过 6000 units，这是**记录在案的行为**。
所以它适合**范围明确的调用**：

```bash
# 单个包 —— 注意本仓 internal/engine 连 20000 units 都装不下（约束节自身
# 就超过 6000，这是已记录的行为：get_constraints 的预算是信封的一部分）
metricgate bundle internal/engine -max-chars 20000

# 一次提交 —— 实测可用，直接产出 JSON 信封
metricgate bundle -commit 9f5d21c -max-chars 6000
```

对拍实验里 LLM 侧自己用 git 读代码时的困难是「类跨 2800 行、一个提交动两个
3 行 guard」，而 bundle 的包级信封正好回答这类问题。

### 3. MCP `get_review_bundle`（同 2 的 MCP 形态）

`cmd/metricgate-mcp` 已注册该工具（`tools.go:74`），输入是
`append/base/commit/format/from/to/include_source/max_chars/to`，
输出是同一个信封的 JSON。**接缝本来就在**，MCP 客户端无需额外接线。

## 明确不做

| 不做的事 | 理由 |
|---|---|
| MetricGate 调用 LLM | 破「零网络」承重墙；`docs/capabilities.md:313` |
| 闸门吃 LLM 的判定 | 破坏确定性 / 可复现（台账 #10） |
| 把 LLM 写进 `go.mod` 或 MCP server | 同上，且引入凭据 |
| 让 `-diff` 的过滤结果依赖 LLM | 本仓的 MCP 侧还没有 base 树 |

## 复现

```bash
# 入口 1（实测 11 条 metric-regression）
cd /tmp/rb/<any-commit-worktree>
metricgate check . -diff -base HEAD~1 -regress -format json

# 入口 2
metricgate bundle internal/engine -max-chars 6000

# 入口 3
# 任意 MCP 客户端调用 get_review_bundle { append: ".", max_chars: 6000 }
```
