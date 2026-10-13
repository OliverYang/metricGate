# 九方向广度调研（goal mv407zjq-jab3l6 · t3）

日期：2026-10-12 · 方法：**只读实测**（所有数字来自真实命令输出，不是源码阅读推断）
测量工具：`/tmp/mg-self`（= `go build -o /tmp/mg-self ./cmd/metricgate`）
探针仓：`/tmp/inhprobe`、`/tmp/inh2`、`/tmp/inh3`（9 语言各一对 Base/Sub）

> 本文只记录**调研结论与证据**，不代表授权实现。取舍为初步判断，
> 最终 3 选名单在 t4 用实测确认。状态约定：🟢 建议深挖 · 🟡 边界项（可做可不做）
> · 🔒 舍弃（记录以免重复评估）
>
> **后续更新（2026-10-12）**：本调研选出的 3 个方向（继承/实现边、`custom_rules`
> 文件作用域、root 一致性）已在迭代 8 完成；本文列为边界项的 **N3 arch 多语言**
> 与 N6 清理项也在迭代 9「九语言能力拉齐」中一并完成。最终状态见
> [docs/iterations/iter-8-t6-three-directions.md](../iterations/iter-8-t6-three-directions.md)
> 与 [docs/iterations/iter-9-language-parity.md](../iterations/iter-9-language-parity.md)。

---

## 0. 结论摘要

| # | 方向 | 现状判定 | 初步取舍 |
|---|---|---|---|
| 1 | 外部数据源 | 已落地（feed/extract/rulegen 三条），无缺口 | 🔒 不深挖 |
| 2 | CI 流程控制 | 已落地（3 workflow + composite action），全量 check 2.08s | 🔒 不深挖 |
| 3 | 审计 | 账本已落地；缺"门禁在真实 agent 循环里被调用"的证据（claims #15） | 🟡 边界项 |
| 4 | 中间态数据 | 348.5 MB / 573 runs / 594 KB per run，无保留策略 | 🟡 边界项 |
| 5 | **分析维度** | **继承/实现边在 Py/JS/TS 完全缺失**（refs=[]）；arch 只认 Go/Java | 🟢 深挖 |
| 6 | 结果展示 | `bundle` 在本仓自己身上预算触顶；usage.go 有重复项 | 🟡 边界项 |
| 7 | **门禁控制** | **`custom_rules` 无文件作用域谓词 → 本仓 6 处误报（含 1 处生产文件）** | 🟢 深挖 |
| 8 | 非标输入转换 | 已落地三条，与 #1 同源 | 🔒 不深挖 |
| 9 | 动态注入检查 | MCP `get_impact` 每次调用重建整棵索引（O(tree)） | 🟡 边界项 |

新议题（调研中发现，围绕产品核心能力）：见 §10。

---

## 1. 外部数据源 — 🔒 已落地，不深挖

**结论：这个方向的活已经干完了，只是入口不在 `cmd/` 里。**

- `internal/feed`（764 行，12 测试）在 `cmd/` **零引用**，但**不是死代码**：
  真实入口是 `scripts/osv2policy/main.go`，包注释原话是
  **"the metricgate binary never grows a feed subcommand; this is an ops glue tool"**。
  导出面 `ParseOSV`(:88) / `Plan`(:159) / `PlanGroups`(:176) / `DedupKeys`(:195) /
  `RowFor`(:214) / `Merge`(:243)，配套 `scripts/osv2policy.sh`。
- 它把 OSV 供应链告警翻译成门禁已有的 banned-import 词汇——即"外部源 → 已有能力"
  的兼容层，而不是新维度。诚实边界写在 `docs/constraints/supply-chain-feed.md`：
  源码 import 不带版本（禁令是绊线，版本区间属依赖树工具）；传递依赖在源码层不可见。
- 与它同族的两条也都在：`internal/extract`（非标文本 → 规则候选）被
  `cmd/metricgate/constraints_cmd.go` 用；`internal/rulegen`（外部 agent 用 JSON spec
  描述禁令 → 编译 + fixture 验证 → 可直接并入 policy.yaml 的产物）同处。

**取舍**：🔒 不深挖。再加外部源只会增加"兼容层"数量而不增加检测能力；
真要扩，应该扩的是**判据**（见 §5/§7），不是输入管道。

**证据**：`internal/feed/feed.go` 包注释；`grep -rn 'internal/feed' cmd/` → 0 命中。

---

## 2. CI 流程控制 — 🔒 已落地，不深挖

- `.github/workflows/metricgate.yml`（diff 范围门禁，需 `fetch-depth: 0`）、
  `metricgate-pr.yml`（9 语言 paths 过滤 + `check . -diff -base origin/<base_ref>
  -sarif findings.sarif -format json` + 内嵌 python 生成 findings-summary.md +
  自更新 PR 评论 marker `<!-- metricgate-gate-report -->` + artifact 上传）、
  `metricgate-schedule.yml`（周一 02:00 UTC 全树只报告扫描，`|| true`，永不作为必需检查）。
- `.github/actions/metricgate-check/action.yml`（composite action）输入
  `base/path/policy/go-version/sarif/max-cognitive/max-cyclomatic`，
  输出 `findings-json/sarif-file/errors/warnings`。
- **成本实测**：`time -p /tmp/mg-self check . -config policy.yaml -format json`
  → **real 2.08s / user 3.52s / sys 0.22s**（225 files）。CI 成本不是瓶颈。

**取舍**：🔒 不深挖。剩余动作（Marketplace v1 tag、MCP 目录收录）是**发布**动作，
不是工程方向，且需用户批准（历史已记录为未授权）。

---

## 3. 审计 — 🟡 边界项

- 两条既有旁路都已落地且可审计：
  - 源码指令 `metricgate: ignore <rule> [reason]`（行作用域）与
    `metricgate: ignore-begin/end`（范围作用域），`internal/engine/suppress.go:33`
    `directiveRe`、`:171 suppress` 返回 `(kept, ignored, byRule)`；
    `engine.go:80-84` 存 `ignored`/`ignoredByRule`，`rank.go:42 countSuppressed`
    把它记进 summary。
  - `.metricgate/accepted.yaml`（让步账本，迭代 1）：233 字节，当前 **1 条**
    （`cmd/metricgate/main.go:35` fan-out）。
- 本仓自检 `-diff -base HEAD`：`0 errors, 115 warnings`，其中
  `1 findings suppressed by metricgate: ignore directives` +
  `1 accepted warning(s) by ledger`。**两条旁路都真的在用**，不是纸面功能。
- `internal/status`（约束落地**之后**的检测状况：每条规则是命中/静默/无文件可看）
  已通过 `constraints status` 暴露。

**缺口**：`docs/claims.md` #15「闸门在真实 agent 循环被调用」标 ⏳ 未测，
且被注释为"**决定量级**"。这不是代码缺口，是**证据缺口**——但它无法靠写代码补，
只能靠真实使用采样。`internal/calllog` + Cockpit 的 verify 覆盖指标
（`CheckCode`/`CheckPath` 计数、InterceptRate、ConvergeRate）已经是采集面。

**取舍**：🟡 边界项。除非要做"审计视图"（把 suppress/accepted/status 三者并成一张
可趋势化的表），否则不新增。并表本身是展示问题，见 §6。

---

## 4. 中间态数据 — 🟡 边界项

**实测**：`.metricgate/history.db` = **350044160 字节（≈334 MiB）**，
`runs` = **575**、`findings` = **45825**、`metric_snapshots` = **3713951**
→ 平均 **约 609 KB / run**（快照占绝对多数：每 run 约 6459 条 metric 快照）。
（同日早先取样为 573 runs / 45193 findings / 3682217 snapshots / 347045888 字节，
即两次全量自检之间约 +2 runs / +632 findings / +31734 snapshots，
印证「增长随自检频率线性、每 run 约 0.6 MB」这一判断。）

表结构只有三张 + 三个索引：`runs(id, started_at, duration_ms, tool_version,
git_commit, files, error_count, warning_count, info_count, project_root, archived)`、
`findings(run_id, rule_id, severity, source, file, start_line, start_column,
end_line, end_column, message)`、`metric_snapshots(run_id, file, scope, metric,
value, warn_threshold, err_threshold)`；索引 `idx_runs_started` / `idx_findings_run`
/ `idx_snapshots_run`。

**取舍**：🟡 边界项。增长是**真实但可控**的（573 run ≈ 348 MB，本地单文件）。
真正的风险不是磁盘，是**没有保留策略**：`runs.archived` 只做"隐藏"，不回收字节。
若将来要动，最小改动是"按 run 数或按时间的快照降采样"（保留 findings、折叠旧
snapshot），而不是删数据。但当前 348 MB 不构成问题，**不优先**。

---

## 5. 分析维度 — 🟢 深挖（本块最强证据）

### 5.1 继承/实现边在 3 种语言里完全不可见

探针 `/tmp/inh3`：9 语言各写一对 `Base`/`Sub`，然后
`impact . -q <Base> -format json` 查 refs。

| 语言 | 源码形态 | heritage 作为 ref？ | refs 实测 |
|---|---|---|---|
| Java | `public class SubJ extends BaseJ {}` | ✅ | `java/a.java:3` |
| Go | `type SubGo struct{ BaseGo }` | ✅ | `go/a.go:3` |
| C++ | `struct SubCpp : BaseCpp {}` | ✅ | `cpp/a.cpp:1,2` |
| C# | `public class SubCS : BaseCS {}` | ✅ | `cs/a.cs:2,3` |
| Rust | `impl Tr for BaseRS {}` | ✅ | `rs/a.rs:1,3` |
| **Python** | `class Sub(Base): pass` | ❌ | **`[]`** |
| **JavaScript** | `class SubJS extends BaseJS {}` | ❌ | **`[]`** |
| **TypeScript** | `class SubTS extends BaseTS {}` | ❌ | **`[]`** |

TS 的 `implements` 同样不可见：`thing.ts` 里 `BaseRunner implements Runner` +
`FastRunner extends BaseRunner`，`impact -q BaseRunner` 返回的 refs **只有 CS/RS
的同名文件**（跨语言同名干扰），TS 自身 **0 refs**。

**根因（源码确认）**：`internal/refs/refs.go:29-30` 只有两个 kind——
`KindCall = "call"` / `KindType = "type"`；`grep -n 'superclass\|super_interfaces\|
base_class\|heritage' internal/refs/*.go` = **0 命中**。Java/Go/C++/C#/Rust 之所以
"能看见"，是因为它们的 heritage 位置写的是**裸类型标识符**，被 walker 的
`type_identifier` 分支顺带捞到了（Java 的 `w.typeRef` 在 `java.go:18-19`）；
而 Py 的 `superclasses` 是 `argument_list`、JS/TS 的 `extends` 是 `class_heritage`
下的 `identifier`/`member_expression`，都不走 `type_identifier` 分支。

**DIT 也只在 Java**：`internal/metrics/classes.go` 的 `ditOf`(:276-279) /
`superName`(:305) 是同一文件内的名字匹配，且 `classes.go` 早退除非 `LangJava`
（`classes.go:47`）。`internal/impact/impact.go` 的 `Confidence` 只有
`ConfidenceASTOnly = "ast-only"`。

**反例对照（证明 Java 侧确实记了）**：探针 `/tmp/inhprobe` 上
`impact -q Base` → `references: [{name: Base, kind: type, file: Child.java, line: 2}]`、
`affectedFiles: [Child.java]`。

### 5.2 arch 只认 2 种语言

`internal/arch/extract.go:32-41`：

```go
switch lang {
case parser.LangGo:
    return extractGo(src, root)
case parser.LangJava:
    return extractJava(src, root)
default:
    return Raw{}
}
```

`extractGo` 在 `:43`、`extractJava` 在 `:94`。其余 **7 种语言不进架构图**，
于是分层检查 / 环检测 / SDP 对它们**全静默**——静默是这里最坏的结果，
因为"没有违规"和"没看"在输出上无法区分。

### 5.3 取舍

🟢 **深挖 5.1**（继承边）。理由：
1. 收益是**确定性**的：`impact` 在 Py/JS/TS 仓上现在漏掉最粗的那条边（继承），
   而继承恰恰是"改这个类会炸哪些子类"的主答案。
2. 成本极低：refs walker 每个语言加一个分支，把 heritage 位置显式记成
   `KindType` ref。`internal/refs/` 全目录只有 **687 行**（`clike.go` 170 /
   `refs.go` 172 / `js.go` 74 / `go.go` 49 / `py.go` 48 / `java.go` 39）。
3. 不提升复杂度：不新增 kind（复用既有 `KindType`）、不新增配置、不新增依赖。

🟡 **5.2 arch 多语言** 是边界项：收益同样明确（7 语言从静默变可用），但每个语言
都要定义"包身份"（Python 是目录、Rust 是 crate/mod、C/C++ 是头文件路径），
且 `internal/deps/scan_lang.go` 已有 9 语言的 import 查询可复用——工作量中等、
语义决策多。**留作 t4 的备选第三项**。

> ✅ **已实现（迭代 9）**：`internal/arch/extract.go` 已 9 语言分派，
> 每语言拼写表在 `internal/arch/extract_lang.go`；实测同构 TS/C# 仓
> 均输出真实边与 `import-cycle`/`layer-violation`。

---

## 6. 结果展示 — 🟡 边界项

### 6.1 `bundle` 在本仓自己身上预算触顶

```
$ /tmp/mg-self bundle .
metricgate: bundle budget_exhausted: required constraints and diffs need more than 6000 units
```

即 `internal/bundle`（host-agent 输入打包器，CLI 与 MCP `get_review_bundle` 共用同一个
builder）在**本仓规模**（28509 行非测试 Go 代码）上无法产出结果。`newBundleFlags`
在 `cmd/metricgate/bundle.go:29`。预算机制本身工作正常（这是设计中的显式失败），
但默认 6000 units 对真实仓偏小——**产品功能在自家仓库上不可用**是可信度问题。

### 6.2 其他展示面事实

- Cockpit（`internal/cockpit/`）已有：项目优先下钻、runs/trends/findings、
  active/archive 可逆可见性、metric 维度趋势面板。
- **既有文档缺陷**：`cmd/metricgate/usage.go:69-72` 的 `cockpit serve` /
  `cockpit update` 两行**重复连续出现两次**（非本次引入，一行修完）。
- `internal/ci/` 是**空目录**（0 文件），残留物。

**取舍**：🟡 边界项。6.2 的两处是 5 分钟修完的清理项，可搭车；6.1 的预算是
"调参 + 说明"，也可以很小。但都不构成方向级价值，**不进 3 选**。

---

## 7. 门禁控制 — 🟢 深挖（第二强证据）

### 7.1 `custom_rules` 缺文件作用域谓词 → 本仓 6 处误报

`policy.yaml:15-22` 定义的规则：

```yaml
custom_rules:
  - id: go-test-func-naming
    node: function_declaration
    field: name
    match_regex: '^(?i)(test|benchmark|fuzz|example)'
    exclude_regex: '^(Test|Benchmark|Fuzz|Example)[A-Z_]'
    severity: warning
    reason: "Go test entrypoints must be TestXxx/BenchmarkXxx/FuzzXxx/ExampleXxx ..."
```

全量 `check .` 的实际命中（6 处，**全部是误报**）：

| 位置 | 实际名字 | 为什么是误报 |
|---|---|---|
| `internal/bundle/bundle_test.go:18` | `testConfig` | 测试**辅助函数**，不是 test entrypoint |
| `internal/digest/digest_test.go:11` | `testConfig` | 同上 |
| `internal/engine/engine_test.go:13` | `testConfig` | 同上 |
| `internal/policygap/policygap_test.go:29` | `testInput` | 同上 |
| `internal/symbols/index_test.go:8` | `testIndex` | 同上 |
| **`internal/gatecheck/gatecheck.go:135`** | **`TestsTouchedCheck`** | **生产文件**！名字只是恰好以 `Tests` 开头 |

**根因**：`config.DslRuleConfig` 的字段是
`ID/Title/Severity/Node/Field/MatchRegex/ExcludeRegex/Languages/Reason/Use`
（`internal/config/config.go`），**没有文件路径谓词**。`Languages` 只能按语言过滤，
所以一条"只该管 `_test.go` 里 TestXxx 命名"的规则会打到：
(a) 测试文件里的辅助函数（名字合法但不符合 `TestXxx` 形状），
(b) 生产文件里任何以 `test`/`Tests` 开头的函数。

**这不是规则写错，是 DSL 表达力缺口**：`docs/pilot/conversion-map.md` 的 gap 列表里
NAMING-01~06 六条命名条款全部卡在同一处——"标识符正则 × 节点类型 × **文件/包上下文**"
的最小 DSL 只差最后一项。`docs/pilot/pilot-report-scan-portal.md` 的 P0 建议
原话就是"自定义规则 DSL（最小版=命名模式断言）"。

### 7.2 已有的门禁面（供对照）

- `internal/engine/suppress.go`（源码指令）、`.metricgate/accepted.yaml`（账本，1 条）
- `internal/gatecheck`（`impact-scope` LC-403 / `tests-touched` LC-404，默认 warning
  观察期；`gatecheck.go:184 isTestPath` 委托 `sources.IsTestPath`，
  注释明确"门禁的测试覆盖检查与 engine 的测试基线永远不能分歧"）
- `internal/status`（规则命中/静默/无文件可看的三态）

### 7.3 取舍

🟢 **深挖**。理由：
1. **确定性收益**：本仓 6 处误报当场消失，且这 6 处正是"假阳性侵蚀门禁可信度"的
   典型样本——用户看到误报的第一反应是加 `metricgate: ignore`，账本于是膨胀。
2. **成本极低**：`config.DslRuleConfig` 加一个 `Files []string`，
   `internal/rules/dsl.go` 的 `dslRule`/`dslOnMatch` 里用已有 glob 匹配
   （`internal/coupling` 的 `MatchFile` 或 `internal/arch` 的 pattern 已有实现可参考）。
3. **不提升复杂度**：不新增依赖、不新增必需配置（不写 `files` 就保持现在的行为）。
4. 直接兑现 pilot 的 P0 建议，且**同时**让 §5.1 之外的第二类"假阳性"问题收敛。

---

## 8. 非标输入转换 — 🔒 已落地，不深挖

与 §1 同源：`internal/extract`（规范表/事故报告/访谈笔记 → 标准化规则候选；
确定性、无 LLM、无网络、一过筛，注释明确"绝不是终判"）、`internal/rulegen`
（JSON spec → 编译 + fixture 验证 → policy.yaml 产物；"数据进数据出，
调用方从不读 metricgate 源码"）、`internal/feed`（OSV → banned-import 词汇）。
三条覆盖了"外部知识 → 可执行规则"的完整链路。

**取舍**：🔒 不深挖。真正的瓶颈不在转换，在**判据质量**（§5/§7）。

---

## 9. 动态注入检查 — 🟡 边界项

- `cmd/metricgate-mcp/main.go:286-289` 的 `getImpact` 每次调用都执行
  `facts.CollectWithRefs(path)` —— **重建整棵索引**（O(tree)）。瓶颈是 tree-sitter
  解析，不是图 join。
- 真实仓测量：`/Users/mac/Downloads/main/bugbye-parser`（**154 个 .py**）上
  `impact -q BaseToolReport` 约 **0.69s**。
- MCP 循环里每问一次重建一次；但 0.7s 在交互里不构成体验问题，且
  `internal/facts/budget.go` 已经给输出侧设了预算（`ErrBudgetExhausted`/`PackResult`）。

**取舍**：🟡 边界项。缓存索引（按 root + mtime 失效）是明确的优化，但**收益是延迟
而非能力**，且引入"缓存新鲜度"这一类新的正确性风险（正是 Graphify 适配评估里
被列为"新鲜度会静默撒谎"的那类坑）。**不优先**。

---

## 10. 调研中发现的新议题

按"围绕产品核心能力、有确定性收益、不提升复杂度"筛过后，只有前两条够格：

| # | 新议题 | 证据 | 判定 |
|---|---|---|---|
| N1 | **继承/实现边**（Py/JS/TS 的 `extends`/`implements`/`superclasses` 未记为 ref） | §5.1 三语言 refs=`[]` | 已并入 §5，🟢 深挖 |
| N2 | **`custom_rules` 文件作用域谓词** | §7.1 本仓 6 处误报 | 已并入 §7，🟢 深挖 |
| N3 | arch 多语言 | §5.2 只有 Go/Java | ✅ 已完成（迭代 9） |
| N4 | bundle 预算 | §6.1 本仓触顶 | 🟡 边界项 |
| N5 | 存储保留策略 | §4 594 KB/run | 🟡 边界项 |
| N6 | `usage.go` 重复行 + 空 `internal/ci/` | §6.2 | ✅ 已清理（迭代 9） |
| N7 | MCP 索引缓存 | §9 | 🟡 边界项（收益是延迟不是能力） |

---

## 11. 明确不进入深挖的方向及理由

| 方向 | 理由 |
|---|---|
| 外部数据源 | 已落地（feed/extract/rulegen），再加只增兼容层不增检测力 |
| CI 流程控制 | 已落地；剩余是发布动作（需用户批准），非工程方向 |
| 非标输入转换 | 同上，与外部数据源同源 |
| Graphify 图源适配 | 已在 `docs/backlog-v1x.md` 候选 3 记录为 🔒 搁置（`rationale_for` 收益路径已被实测证伪：13 条未转化条款所在的 `gitee-scan-2.0-portal` 仓该边 **0 条**） |
| 换图数据库存储 | 破坏"纯本地零 key"卖点；数据量（约 42k 边/次）远低于图数据库价值区间 |
| 把 Python 运行时拉进 CI | 同上，损害零依赖卖点 |
| 语义级检查（中文注释质量、异常流控制） | 已在 `docs/roadmap.md` §三「明确不做」中记录为不可自动判定 |

---

## 12. 候选短名单（交 t4 实测确认）

> 相关既有记录：[docs/backlog-v1x.md](../backlog-v1x.md)（候选 1/2 已完成、候选 3 搁置）、
> [docs/roadmap.md](../roadmap.md) §三「明确不做」与 §五「v1.x 已落地」。

1. **继承/实现边补齐（Py/JS/TS）** — 证据：§5.1 三语言 refs=`[]`；成本：refs walker
   3 个分支；收益：`impact`/`get_impact`/`get_review_bundle` 在 3 语言上补回最粗的边。
2. **`custom_rules` 文件作用域** — 证据：§7.1 本仓 6 处误报（含 1 处生产文件）；
   成本：config 一个字段 + dsl 一处 glob 匹配；收益：假阳性收敛 + 兑现 pilot P0。
3. **备选第三项**（t4 用实测择一）：
   - **arch 多语言**（§5.2）——收益面最大（7 语言分层检查从静默变可用），成本中等。
   - **bundle 预算**（§6.1）——收益单点但"自家仓库不可用"是可信度问题，成本最低。
   - **N6 清理项**——不计入方向级。

t4 的淘汰标准（沿用上一轮 Graphify 调研立下的反例标准）：**入选理由必须绑定实测
输出；上一轮 `rationale_for` 收益路径被实测证伪即为反例——宁可淘汰，不留"读源码
看起来能做"的方向。**

---

## 附：本调研用到的复现命令

```bash
# 继承边探针（9 语言）
mkdir -p /tmp/inh3/{py,js,ts,cs,rs,cpp,go,java}
# ... 各写一对 Base/Sub（源码形态见 §5.1 表格）
/tmp/mg-self impact /tmp/inh3 -q Base -format json

# 自定义规则误报
/tmp/mg-self check . -config policy.yaml -format json \
  | python3 -c 'import json,sys; [print(f"{x[\"file\"]}:{x[\"startLine\"]}") for x in json.load(sys.stdin)["findings"] if x["ruleId"]=="custom.go-test-func-naming"]'

# 全量 check 耗时
time -p /tmp/mg-self check . -config policy.yaml -format json

# 存储规模
ls -l .metricgate/history.db
sqlite3 .metricgate/history.db 'select (select count(*) from runs),(select count(*) from findings),(select count(*) from metric_snapshots);'

# bundle 预算
/tmp/mg-self bundle .
```
