# 闭环易用性与规范性审计（2026-10-12）

> 本文只记录结论与证据，不代表授权实现。
> 复现基线：`go build -o /tmp/mg-self ./cmd/metricgate` 后，在 `/tmp/onboard`
> （一个新建的 TypeScript 两文件仓库，无 `.data/` 干扰）逐条实测。

---

## 0. 结论摘要

以「一条新代码从写入到被闸门放行」为主轴走完整闭环，审计出 **3 个 P0 缺陷**和
**12 类 P1/P2 易用性与规范性问题**。三个 P0 的共同点：**它们都让闭环在某一环
静默断开**——工具返回空却看起来像正常答案，这正是本轮已修复过的「silent zero」
失败类（参照迭代 9 九语言拉齐）。

| 编号 | 缺陷 | 严重度 | 失败形态 |
|---|---|---|---|
| A | `/api/dashboard` 不读历史库 | P0 | cockpit serve 下 runs/projects/trend/archive 恒为空 |
| B | MCP gate preamble 吞掉工具自身 payload | P0 | 同一查询返回内容随闸门状态变化 |
| C | `accept add` 对 error 级规则静默无效 | P0 | 打印 accepted，闸门仍 FAILED |

---

## 1. 审计方法

不读源码猜，每个结论都绑定一条可复现命令与其实测输出。主轴线：

```
写入代码 → 生成前约束（context/digest/bundle）→ 检查（check -diff）
   → 闸门裁决 → 失败后处置（fix / suppress / accept / 改阈值）→ 再检查
   → 趋势与审计（trends/calls/cockpit）
```

对照组：`/tmp/onboard` 用全新仓库（避免 MetricGate 自身 150+ 未提交文件的噪声），
并全程对比 CLI 与 MCP 两条表面。

---

## 2. P0-A：`/api/dashboard` 不读历史库

### 2.1 实测

```bash
cd /tmp/onboard
/tmp/mg-cock cockpit serve -addr 127.0.0.1:8893 &
curl -s http://127.0.0.1:8893/api/dashboard | python3 -c "
import json,sys; d=json.load(sys.stdin)
print('projects:', len(d.get('projects') or []))
print('runs:', len(d.get('runs') or []))"
```
输出：`projects len: 0`、`runs len: 0`，HTTP 200。

而同一时刻直接调库函数（探针 `internal/zzdash`，已删）：
```
projects: 1
  /private/tmp/onboard runs=3 findings=2
unassigned runs: 3
```
且 `.metricgate/history.db` 里确有 6 个 run、8 条 finding。

### 2.2 根因

`internal/cockpit/dashboard.go:282-288`：

```go
func Handler(logPath, dbPath string) http.Handler {
	mux := http.NewServeMHandle... 
	mux.HandleFunc("/api/dashboard", dashboardAPI(logPath))   // ← dbPath 丢了
	mux.HandleFunc("/api/runs/archive", archiveAPI(dbPath))
```

`dashboardAPI(logPath string)` 内部调用 `NewDashboard(logPath, "")`——
**第二个参数恒为空字符串**，而 `NewDashboard` 的约定是 `dbPath == ""` 就
`return d, nil`，完全不碰 store。

`archiveAPI` 正确拿到了 `dbPath`，所以「归档」按钮点下去会 503，而「历史」
面板永远是空的。**两个接口从同一个 `Handler` 出发，一个读库一个不读。**

### 2.3 这是回归还是遗留

从 iter-11 之前的拆分记录看，`Handler` 曾被拆成 `dashboardAPI`/`archiveAPI`/
`decodeArchiveRequest`/`applyArchive`/`webMount`/`withLocalAPIGuard` 以消除
cyc 17/cog 28。拆分时 `dbPath` 传给了 `archiveAPI`，但在 `dashboardAPI` 这一支
漏掉了。**既有的 cockpit 测试没有覆盖「HTTP 层返回 runs>0」**，所以它是静默的。

### 2.4 影响面

cockpit 是「结果呈现 / 可观察」维度的主表面。此缺陷使整个项目维度
（projects）、运行历史、度量趋势（metricTrend）、归档/恢复在真实 HTTP 访问下
全部失效，只剩 MCP call log 一块是活的。而 call log 默认关闭
（`calls` 输出 `note: ... instrumentation is off by default`），所以新用户
打开 cockpit 看到的几乎是空面板。

---

## 3. P0-B：MCP gate preamble 吞掉工具自身 payload

### 3.1 实测（最严重的一条）

```bash
# 同一进程内，连续两次完全相同的查询
call1: 1 block(s)  lens=[139]    ← 只有 gate preamble，constraints 没了
call2: 1 block(s)  lens=[1562]   ← 完整 pack，preamble 没了
```

`get_impact` / `get_constraints` / `get_repo_map` / `check_path` 全部复现。
模拟真实 agent 循环（改文件 → 查询）6 次，**6 次 payload 全被吞**。

### 3.2 根因

`cmd/metricgate-mcp/gate.go:257-280`：

```go
res, out, err := h(ctx, req, in)
if text := g.preamble(); text != "" {
    block := &mcp.TextContent{Text: text}
    if res == nil {
        res = &mcp.CallToolResult{Content: []mcp.Content{block}}   // ← 抢在 SDK 之前
    } else {
        res.Content = append([]mcp.Content{block}, res.Content...)
    }
}
```

这些 handler 的签名是 `(*mcp.CallToolResult, Out, error)` 且 **`res` 返回 nil**，
因为它们依赖 SDK 把 `Out` 自动序列化进 `Content`。`wrapGate` 在 `res == nil`
时抢先把 `Content` 填成只有 preamble 的一个 block，SDK 于是认为
「handler 已经给过 Content 了」，不再追加 `Out` 的 JSON。

`structuredContent` 仍然完整——数据没丢，但**模型的 text 视野里只剩判决**。

### 3.3 为什么这是闭环断点而非噪音

agent 循环是「读约束 → 改代码 → 再查询」。第二步必然让 gate 变脏，于是第三步
（或下一轮的第一步）拿到的是判决不是数据。这恰好命中本工具的核心承诺
「生成前给约束」：**脏 gate 状态下，`get_constraints` 对模型不可见**。

更坏的是它**不确定**：同一查询同一仓库，返回内容取决于「上次调用以来有没有文件
变动」。这让 agent 无法区分「仓库里没有符号」和「结果被判决挤掉了」。

对照：`check_path`/`check_code` 自己构造 `Content`，所以不受影响——
但它们在脏 gate 下同样只剩 preamble（见 3.1 实测）。也就是说**除
`structuredContent` 外无一幸免**。

### 3.4 与已修复失败类的关系

迭代 9 修的是「能力轴没被断言 → 结果是静默的 0」；这里是「呈现层被抢占 →
答案是静默的缺」。同一个失败模式，换了一层。

---

## 4. P0-C：`accept add` 对 error 级规则静默无效

### 4.1 实测

```bash
/tmp/mg-self accept add nesting-depth src/big.ts -line 1 -owner me -reason "legacy parser shape"
# → accepted nesting-depth src/big.ts:1 (owner=me)
# →   reason: legacy parser shape

/tmp/mg-self check . -config policy2.yaml
# → summary: 1 errors, ...
# → gate: FAILED (fail_on=error, 1 findings)      rc=1
```

### 4.2 根因

`cmd/metricgate/accept_cmd.go:35-46`：

```go
func annotateAccepted(summary *model.RunSummary, ledger *accept.Ledger) int {
	for i := range summary.Findings {
		f := &summary.Findings[i]
		if f.Severity != model.SevWarning {
			continue          // ← error 一律跳过
		}
```

而 `accept add` **不校验 severity**（`accept.Validate()` 只查 owner/reason/line/expires），
所以对 error 级规则照样写入账本并打印成功。注释里写了理由
（「an error finding means broken semantics, not taste」），但** CLI 没有任何
地方告诉用户这条规则不能被让步**。

### 4.3 陷阱形状

这是三条 P0 里最「像成功」的一条：命令退出 0、输出明确、账本里确实有记录、
`accept list` 也列得出来——然后闸门照红。用户已经做了工具教他做的一切。

---

## 5. P1：闭环断点类

### 5.1 `-diff` 藏起来的既存问题，没有任何查看途径

```bash
/tmp/mg-self check . -diff -config policy.yaml
# diff-only: base=HEAD, 0 new findings, 2 pre-existing hidden
```

「2 pre-existing hidden」是个**只读计数**。`-h` 里没有任何 flag 能展开它，
用户必须自己在脑子里重跑一次不带 `-diff` 的版本。而 JSON 模式下
`findings: []`，机器侧同样看不到。

这不是要推翻 ratchet 设计，而是缺一个**从「被隐藏」到「去查看」的跳转**。

### 5.2 失败后没有 remediation 叙事

`gate: FAILED (fail_on=error, 1 findings)` 之后直接结束。用户下一步有四条路
（修复 / `metricgate: ignore` / `accept add` / 改阈值），工具一条都不说。
对一个「闸门」产品来说，裁决之后不给处置指引，闭环断在最后一步。

### 5.3 抑制与让步两套系统都不被审计

- `metricgate ignore` 指令：只有一行
  `note: 1 findings suppressed by metricgate: ignore directives`，
  没有 `-h` 之外的列表命令，没有 owner/reason/过期概念。
- `accept` 账本：有 `list`，但 `rules gaps`（政策完备性审计）**完全不提这两者**。
  一个仓库里有多少处让步/抑制、分别属于谁、是否过期，没有任何单一视图。

### 5.4 `rules gaps` 与自己结论的落差

`rules gaps` 报告得很诚实（not armed / needs a flag / report-only / firing /
no files），但它的语言是「规则视角」而非「用户视角」。用户问的是
「我这个仓库现在被什么挡着」，答案需要跨规则+账本+抑制聚合。

---

## 6. P1：语义与信息一致性类

### 6.1 `trends` 的误导性提示

```
note: store.enabled is false in policy.yaml — `trends` will only see runs from
prior enabled windows
runs: 6
```
紧接着 `runs: 6`。提示说「你没在存」，下一行显示明明存了 6 次。
实际语义是「本仓库 policy.yaml 没开 store，但库里有历史」——提示文案与事实矛盾。

### 6.2 CLI 与 MCP 的路径坐标不一致

同一 finding：
- CLI：`src/big.ts:1`
- MCP gate preamble：`big.ts:1`（`filepath.Base`）

agent 若把 MCP 输出回贴给 CLI，路径对不上。

### 6.3 JSON 输出缺机器可读的裁决字段

`check -format json` 的顶层只有 `files`/`findings`/`metrics`——
没有 `errors`/`warnings`/`verdict`/`gate`。机器必须自己按 severity 数一遍，
而文本层的 `summary:`/`gate:` 行在 JSON 模式下走 stderr，两边对不上。
（`check_path` MCP 工具反而有 `errors`/`warnings`/`verdict` 字段——
CLI 与 MCP 的形状又不对称。）

### 6.4 `check_code` 的判决不可执行

```json
"verdict": "WARN: fix the warnings before submitting"
```
不含哪几条、在哪、怎么改。而 findings 数组里全有。判决字符串和信息是分离的。

### 6.5 `check_code` 不认账本，MCP preamble 认

MCP gate preamble 用 `countOpenWarnings(findings, g.ledger)`——
**被让步的 warning 不计入判决**；而 `check_code` 的 `summarizeFindings`
没有 ledger 参数，照样计入。同一个 warning，两条表面给 agent 的答案相反。

### 6.6 digest 不知道已经让步过什么

`constraints digest -pkg src` 给出阈值与复用提示，但不提
`.metricgate/accepted.yaml` 里已有的让步。agent 会试图「修复」一个已被人类
明确接受的问题。（direction-survey D3 已把这条记为不对称，本轮实测确认仍在。）

---

## 7. P2：体验细节

| 现象 | 实测 |
|---|---|
| 新仓库首次 `-diff` 报错措辞 | `git changed paths: exit status 128 — HEAD has no commits yet...`（准确但术语门槛高） |
| 无 policy 文件 | `note: no policy file at policy.yaml, using built-in defaults`（好），但内置默认值不可见 |
| `fail_on` 只能改 policy.yaml | `check -h` 无 `-fail-on`，本地试不同档位要手改文件 |
| `bundle .` 默认预算必失败 | `bundle .` → `budget_exhausted: ... exceed 2000 units`；`-include-source` 与 12000 才可用 |
| bundle 不含 finding 本体 | `'findings' in json.dumps(bundle)` → `False`；给 LLM 的修复输入里没有问题清单 |
| 退出码契约未成文 | `help` 只有 `exit 1` 两处（api diff / arch diff），`check` 的 0/1/2 无说明 |

---

## 8. 与既有文档的关系

- `docs/research/direction-survey-2026-10-12.md` §3（D3 审计）已把
  「抑制无需 owner/reason/expiry，让步账本需要」记为不对称，但当时结论是
  「🟡 边界」。本轮实测把它升级为具体断点（5.3 / 6.6）。
- `docs/research/llm-review-input.md` 记录了 `get_review_bundle` 作为 LLM 输入
  接缝。P0-B 说明这个接缝当前不可靠（脏 gate 下 bundle 本体被吞）。
- 迭代 10 的 `-diff` 值增量把「既存债务不再重复报告」做到了 CLI；5.1 说明
  「被Hidden 的看不见」这一侧还没对称处理。

---

## 9. 修复优先级建议

**P0（各约一行到十几行，直接决定闭环是否通）**
1. `dashboardAPI(logPath, dbPath)` —— 把 `Handler` 已有的 `dbPath` 传进去。
2. `wrapGate` 不要把 preamble 写进 `res == nil` 的 `Content`；改为在
   `Out` 侧携带（或让 handler 自己拼 content 时把 preamble 作为第一个 block
   追加，保证 `Out` 的 JSON 永远在）。
3. `accept add` 校验 severity：error 级直接拒绝并说明理由，或降级为 warning
   后再让步；至少不能静默。

**P1（每条独立，改动小但需要对闭环语义做选择）**
4. `-diff` 提供查看 hidden 的途径（flag 或 `-show-hidden`）。
5. `gate: FAILED` 后输出四选一 remediation 指引。
6. 统一的让步/抑制审计视图（`rules gaps` 扩展或新 verb）。
7. `trends` 提示文案与事实一致。
8. MCP preamble 路径与 CLI 对齐（root-relative）。
9. `check -format json` 增补 `errors`/`warnings`/`verdict`。
10. `check_code` verdict 串带上前 N 条 finding 摘要。
11. `check_code` 接入账本，与 MCP preamble 口径一致。
12. digest 输出已有让步清单。

**P2**：退出码文档化、`-fail-on` flag、bundle 内置 finding、首跑引导。

---

## 10. 复现命令清单

```bash
# 一次性准备
go build -o /tmp/mg-self ./cmd/metricgate
go build -o /tmp/mg-mcp ./cmd/metricgate-mcp
rm -rf /tmp/onboard && mkdir -p /tmp/onboard/src && cd /tmp/onboard && git init -q .

# P0-A
/tmp/mg-cock cockpit serve -addr 127.0.0.1:8893 &
curl -s http://127.0.0.1:8893/api/dashboard   # projects 0 / runs 0

# P0-B
python3 /tmp/loopC.py    # 6/6 payload 被吞

# P0-C
/tmp/mg-self accept add nesting-depth src/big.ts -line 1 -owner me -reason x
/tmp/mg-self check . -config policy2.yaml      # 仍 FAILED

# P1
/tmp/mg-self check . -diff -config policy.yaml # hidden 不可见
/tmp/mg-self check . -format json              # 无 verdict 字段
/tmp/mg-self trends                            # 提示与 runs: 6 矛盾
/tmp/mg-self rules gaps .                      # 不提账本/抑制
/tmp/mg-self accept list                       # 唯一可用的审计入口
```

---

## 附：本次审计未改动任何产品代码

所有结论来自只读运行与探针；临时探针（`internal/zzdash` 等）已按规矩删除。
