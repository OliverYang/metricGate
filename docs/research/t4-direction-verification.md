# 3 方向实测验证与选定（goal mv407zjq-jab3l6 · t4）

日期：2026-10-12 · 方法：**每个候选都必须真实跑通或跑不通，留原始输出**
测量工具：`/tmp/mg-self`（= `go build -o /tmp/mg-self ./cmd/metricgate`）
探针仓：`/tmp/t4d2`（自定义规则误报）、`/tmp/t4d3`（依赖根）、`/tmp/t4d4`（allowlist + diff 根）
真实仓：`/Users/mac/Downloads/main/bugbye-parser`（154 个 .py）

> 淘汰标准沿用上一轮 Graphify 调研立下的反例：**入选理由必须绑定实测输出**；
> `rationale_for` 收益路径被实测证伪即为反例——宁可淘汰，不留"读源码看起来能做"的方向。

---

## 0. 结论

| 编号 | 方向 | 实测判定 | 决定 |
|---|---|---|---|
| **D1** | 继承/实现边补齐（Py/JS/TS） | ✅ **跑通**：真实仓 36 个子类全部漏报 | 🟢 **入选** |
| **D2** | `custom_rules` 文件作用域谓词 | ✅ **跑通**：受控仓复现 2 处误报，含生产文件 | 🟢 **入选** |
| **D3** | 依赖/文件路径的根敏感面（arch + 门禁 + allowlist + diff） | ✅ **跑通**：4 处独立缺陷，其中 diff 让门禁**静默失效** | 🟢 **入选** |
| D4 | bundle 预算（§6.1 of t3） | 未通过"方向级价值"门槛 | 🔒 淘汰 |
| D5 | MCP 索引缓存（§9 of t3） | 未通过"收益是能力而非延迟"门槛 | 🔒 淘汰 |
| D6 | 存储保留策略（§4 of t3） | 未通过"当前构成问题"门槛 | 🔒 淘汰 |

---

## D1 · 继承/实现边补齐（Py/JS/TS）— 🟢 入选

### 实测 1：真实 Python 仓的漏报规模

仓：`/Users/mac/Downloads/main/bugbye-parser`（154 个 .py）。

grep 地面真相（36 个文件里出现 `class X(BaseToolReport)`）：

```
$ grep -rlE '^class +[A-Za-z_][A-Za-z0-9_]*\(BaseToolReport' --include='*.py' . | wc -l
36
```

metricgate 今天的输出：

```
$ metricgate impact . -q BaseToolReport -format json
  affectedFiles: 1
    analysis/reportBase/reportparser.py
  definitions: 1  references: 1
```

**36 个子类，metricgate 报 1 个（还只是基类自己的定义文件）。漏报 36/36。**

抽样漏掉的文件：`analysis/reportAndroidLint.py`、`analysis/reportBCAAndroid.py`、
`analysis/reportBCACOBOL.py`、`analysis/reportBCACSharp.py`、`analysis/reportBCAKotlin.py`、
`analysis/reportBCAPHP.py`、`analysis/reportBCASQL.py`、`analysis/reportClang.py`、
`analysis/reportClangTidy.py`、`analysis/reportCoverity.py` …（共 36 个）

### 实测 2：受控 9 语言探针（`/tmp/inh3`）

| 语言 | 源码 | `impact -q <Base>` 的 refs |
|---|---|---|
| Java | `class SubJ extends BaseJ {}` | ✅ `java/a.java:3` |
| Go | `type SubGo struct{ BaseGo }` | ✅ `go/a.go:3` |
| C++ | `struct SubCpp : BaseCpp {}` | ✅ `cpp/a.cpp:1,2` |
| C# | `class SubCS : BaseCS {}` | ✅ `cs/a.cs:2,3` |
| Rust | `impl Tr for BaseRS {}` | ✅ `rs/a.rs:1,3` |
| **Python** | `class Sub(Base): pass` | ❌ **`[]`** |
| **JavaScript** | `class SubJS extends BaseJS {}` | ❌ **`[]`** |
| **TypeScript** | `class SubTS extends BaseTS {}` | ❌ **`[]`** |

TS 的 `implements` 同样不可见：`impact -q BaseRunner` 的 refs 只命中 **C#/Rust 的同名文件**
（跨语言同名干扰），TS 自身 0 refs。

### 实测 3：为什么"能看见"的语言其实也是巧合

`internal/refs/refs.go:29-30` 只有两个 kind：`KindCall = "call"` / `KindType = "type"`。
`grep -n 'superclass\|super_interfaces\|base_class\|heritage' internal/refs/*.go` → **0 命中**。

Java 之所以"能看见"，是 `internal/refs/java.go:18-19` 的 `case "type_identifier": w.typeRef(...)`
顺手捞到了 `extends` 后的裸类型标识符。Py 的 `superclasses` 是 `argument_list`、
JS/TS 的 `extends` 是 `class_heritage` 下的 `identifier`/`member_expression`，
都不走 `type_identifier` 分支——**漏的是 walker 的分支，不是数据不存在**。

### 成本与收益

- **成本**：`internal/refs/` 全目录 **687 行**（`clike.go` 170 / `refs.go` 172 / `js.go` 74 /
  `go.go` 49 / `py.go` 48 / `java.go` 39）。改动 = `py.go` 加 superclasses 分支、
  `js.go` 加 class_heritage 分支，复用**既有** `KindType`（`internal/impact/impact.go:47-48`
  已经在数 `TypeUses`）。
- **收益**：`impact`/`get_impact`/`get_review_bundle` 在 Py/JS/TS 上补回最粗的边。
  对本仓探针而言是 1 → 37 个文件。
- **复杂度**：不新增 kind、不新增配置、不新增依赖。

### 淘汰对照

`/tmp/inh3` 的 `object` 基类：grep 37 个文件子类化它，`impact -q object` 报 0 —— 这是
**内建类型，本就不该报**（无定义）。选 `BaseToolReport` 作为对照就是为了排除这一类噪音。

---

## D2 · `custom_rules` 文件作用域谓词 — 🟢 入选

### 实测 1：受控仓复现（`/tmp/t4d2`）

两个文件，一条规则（`policy.yaml:15-22` 的 `go-test-func-naming`）：

- `internal/prod/handler.go:4` — **生产代码** `func testHelper() int`
- `internal/core/helper_test.go:6` — 测试**辅助函数** `func testConfig() int`

```
$ metricgate check . -config policy.yaml
  internal/core/helper_test.go:6: [warning] custom.go-test-func-naming (Go test entrypoints must be TestXxx)
  internal/prod/handler.go:4: [warning] custom.go-test-func-naming (Go test entrypoints must be TestXxx)
summary: 0 errors, 2 warnings, 0 notes, 34 metric snapshots
```

**两个都是误报**，且一个是生产文件。

### 实测 2：本仓真实误报（6 处）

```
$ metricgate check . -config policy.yaml -format json | <filter ruleId == custom.go-test-func-naming>
internal/bundle/bundle_test.go:18      testConfig   ← 测试辅助函数
internal/digest/digest_test.go:11      testConfig   ← 测试辅助函数
internal/engine/engine_test.go:13      testConfig   ← 测试辅助函数
internal/gatecheck/gatecheck.go:135    TestsTouchedCheck  ← 生产文件
internal/policygap/policygap_test.go:29 testInput   ← 测试辅助函数
internal/symbols/index_test.go:8       testIndex    ← 测试辅助函数
```

### 根因

`internal/config/config.go:160-171` 的 `DslRuleConfig` 字段是
`ID/Title/Severity/Node/Field/MatchRegex/ExcludeRegex/Languages/Reason/Use`——
**没有任何文件路径谓词**。`Languages` 只能按语言过滤，所以
"只该管 `_test.go` 里 TestXxx 命名"的规则必然打到生产文件里的任何 `test*` 函数。

对照：`internal/rules/banned.go:406-421` 的 `excluded()` 已经实现了
`except_files` + `allowed_files` 的 glob/suffix/prefix 匹配——**DSL 只是没接上同一套机制**。

### 成本与收益

- **成本**：`DslRuleConfig` 加一个 `Files []string`（yaml `files,omitempty`），
  `internal/rules/dsl.go` 的 `dslOnMatch` 里复刻 `excluded()` 的匹配。
- **收益**：本仓 6 处误报当场消失；兑现 `docs/pilot/pilot-report-scan-portal.md` 的 P0
  建议（"自定义规则 DSL（最小版=命名模式断言）"）；`docs/pilot/conversion-map.md` 的
  NAMING-01~06 六条命名条款全部卡在同一处。
- **复杂度**：不新增依赖、不新增必需配置（不写 `files` 保持现行为）。

---

## D3 · 依赖/文件路径的根敏感面 — 🟢 入选

调研中偶然发现的**产品级正确性缺陷**，四个独立现场，同一个根因族：
**入口 root 与内部 git/cwd 基准不一致**。

### D3-a：`-diff` 门禁在绝对 root 下静默失效（最严重）

同一个仓库、同一个 commit、同一个工作树，只改 root 的写法：

```
### A. relative root（正常用法）
$ metricgate check . -diff -base HEAD
diff-only: base=HEAD, 115 new findings, 201 pre-existing hidden
summary: 0 errors, 115 warnings, 0 notes, 15867 metric snapshots
gate: PASSED (fail_on=error, 1 accepted warning(s) by ledger)

### B. absolute root，SAME repo，SAME commit
$ metricgate check /Users/mac/Downloads/metricGate -diff -base HEAD
diff-only: base=HEAD, 0 new findings, 465 pre-existing hidden
summary: 0 errors, 0 warnings, 0 notes, 15867 metric snapshots
gate: PASSED (fail_on=error)
```

**B 把所有 115 条 finding 判成"pre-existing hidden"，门禁静默通过。**

根因（源码确认）：
- `cmd/metricgate/main.go:250` `runDiffMode(cfg, root, base, ...)`
- → `main.go:253` `git.ChangedLines("", git.ResolveBase("", base))` —— **传空字符串，
  即进程 cwd**，而不是传进来的 `root`
- → `internal/git/git.go:38` `ChangedLines(dir, base)` → `LoadChanges(dir, ...)`
- → `internal/git/changes.go:350-356` `gitBytes(dir, args...)` 里 `cmd.Dir = dir` 只在
  `dir != ""` 时设置；`dir == ""` 就用进程 cwd
- 而 `ChangedLines` 的文档注释（`internal/git/git.go:38-41`）明确写着
  "Reported paths are relative to the repository root whether or not dir was given"
- → `internal/engine/filter.go:62-67` `rangesFor(changed, file)` 只做**精确字符串匹配**，
  注释原话 "with no suffix matching to avoid false positives from similar paths"
- → 而 `Engine.Run(root)`（`internal/engine/engine.go:181`）在绝对 root 下产出的是
  **绝对路径** finding（实测 B 里是 `/Users/mac/Downloads/metricGate/cmd/...`），
  与 cwd 相对的 key（`cmd/...`）永不相等

**为什么这是产品级问题**：绝对 root 是合法用法（`check /path/to/repo`），
且 CI/pre-commit 里很常见（worktree 路径、容器挂载路径）。
它不会报错——**它会报绿**。这正是 metricgate 自己的 `docs/claims.md` #15
（"闸门在真实 agent 循环被调用"标 ⏳ 未测）最怕的失败模式：
门禁看起来在工作，实际上什么都没检查。

注：这不是本次改动引入的——`git show HEAD:cmd/metricgate/main.go:299` 已是
`git.ChangedLines("", git.ResolveBase("", base))`，即 **HEAD 上就是这个行为**。

### D3-b：allowlist 在绝对 root 下失效（同一根因）

`policy.yaml` 里 `banned-fmt-Print*` 配了 `allowed_files: ["cmd/metricgate/"]`。
`internal/rules/banned.go:406-421` 的 `excluded()` 做 `HasPrefix(ctx.File, pat)` /
`HasSuffix` / `filepath.Match`。绝对路径下 `ctx.File` 是
`/Users/.../cmd/metricgate/accept_cmd.go`，**不以 `cmd/metricgate/` 开头**。

```
absolute root: banned-fmt-Print* findings = 153
  in cmd/metricgate/ (policy-allowed):     149   ← 149 条本该被抑制
relative root: banned-fmt-Print* findings = 4
  in cmd/metricgate/ (policy-allowed):     0
```

**153 → 4，差 149 条全是 allowlist 失效。**

> **后续更新（2026-10-12，迭代 9）**：D3-c 与本文其它项已在迭代 9「九语言能力拉齐」
> 中一并完成（`arch` 已支持 9 语言，`arch snapshot` 在同构 TS/C# 仓输出真实边）。
> 本文保留为当时的设计依据与实测记录；最终状态见
> [docs/iterations/iter-9-language-parity.md](../iterations/iter-9-language-parity.md)。

### D3-c：`arch` 在 7 种语言上静默输出空图

```
--- /Users/mac/Downloads/main/bugbye-parser (Python, 154 files)
wrote arch.json: 25 packages, 0 dependency direction(s)

--- /tmp/tsprobe (TypeScript)
wrote arch.json: 1 packages, 0 dependency direction(s)

--- /tmp/rsprobe (Rust)
wrote arch.json: 1 packages, 0 dependency direction(s)

--- /Users/mac/Downloads/metricGate (Go, 对照)
wrote arch.json: 103 packages, 343 dependency direction(s)
```

`internal/arch/extract.go:32-41` 的 switch 只有 `LangGo` / `LangJava`，`default: return Raw{}`。
包是数出来了（25/1/1），边是 0 —— **"没有违规"和"没看"在输出上无法区分**。

### D3-d：绝对 root 污染输出路径（同根因，不同后果）

```
$ cd /tmp/t4d3 && metricgate check . -config policy.yaml -format json
   ['main.go']
$ cd /Users/mac/Downloads/metricGate && metricgate check /tmp/t4d3 -config ... -format json
   ['/tmp/t4d3/main.go']
```

`internal/selection/selection.go:142` `SanitizePath(root, path)` 的文档注释明确写着
"given a walk root, an absolute result under it becomes root-relative…
Relative paths are normalized and never rewritten, **so behavior is identical whether
root is "." or absolute**"。实际不然：`grep -rn 'SanitizePath' internal/engine/` → **0 命中**，
engine 从不调用它（只有 `facts`/`stubs`/`risk`/`scorecard`/`regress_cmd` 调）。
于是引擎产出的 finding 路径直接跟随 root 写法，泄漏机器绝对路径
（破坏确定性 + 污染 LLM 面）。

### 成本与收益

- **成本**：三处都是"把 root 传下去"的接线问题：
  1. `main.go:253` 传 `root` 而非 `""`；
  2. `internal/engine` 在产出 finding 时过一遍 `selection.SanitizePath(root, ...)`
     （或 `rangesFor` 两侧都规范化）；
  3. `internal/arch/extract.go` 的 switch 加语言分支（复用
     `internal/deps/scan_lang.go:118-155` 的 9 语言 import 查询）。
- **收益**：D3-a 是**门禁静默失效**——修它等于修一个"闸门假绿"；D3-b/D3-d 是假阳性
  与确定性；D3-c 是能力覆盖。
- **复杂度**：不新增依赖、不新增配置。

**范围界定**：D3 是一个"根因族"而非单个 bug。t5 需要把它收窄成一个**最小可交付**——
优先 D3-a（门禁假绿）+ D3-b/D3-d（同一处修复路径），D3-c 作为可选的第三块。

---

## 淘汰方向及理由

| 方向 | 实测/评估 | 淘汰理由 |
|---|---|---|
| **D4 · bundle 预算** | `metricgate bundle .` → `budget_exhausted: required constraints and diffs need more than 6000 units` | 收益是**单点调参 + 说明文案**，不是方向级能力。已并入 D3 的搭车清理项候选 |
| **D5 · MCP 索引缓存** | `getImpact` 每次 `facts.CollectWithRefs(path)` 重建索引；真实仓 154 个 .py 上约 0.69s | 收益是**延迟而非能力**，且引入"缓存新鲜度"这一新的正确性风险（正是 Graphify 适配评估里被列为"新鲜度会静默撒谎"的那类坑）。0.69s 不构成体验问题 |
| **D6 · 存储保留策略** | `.metricgate/history.db` = 350044160 字节，575 runs / 45825 findings / 3713951 snapshots ≈ 609 KB/run | 增长**真实但可控**（本地单文件 334 MiB）；真正的风险是没有保留策略，但当前不构成问题 |
| 外部数据源 / CI 流程控制 / 非标输入转换 | 已落地（feed/extract/rulegen；3 workflow + composite action；全量 check 2.08s） | 无缺口，再加只增兼容层不增检测力 |
| Graphify 图源适配 | `docs/backlog-v1x.md` 候选 3 已记录为 🔒 搁置 | `rationale_for` 收益路径已被实测证伪：13 条未转化条款所在的 `gitee-scan-2.0-portal` 仓该边 **0 条** |
| 换图数据库存储 | 数据量（约 42k 边/次）远低于图数据库价值区间 | 破坏"纯本地零 key"卖点 |

---

## 三选名单（交 t5 做需求分析与方案设计）

| # | 方向 | 一句话需求 | 实测锚点 |
|---|---|---|---|
| 1 | **继承/实现边补齐** | `impact` 在 Py/JS/TS 上漏掉继承边，36 个子类全部看不见 | bugbye-parser：grep 36 → mg 1 |
| 2 | **`custom_rules` 文件作用域** | 命名类自定义规则会打到生产文件和测试辅助函数 | 本仓 6 处误报（含 `gatecheck.go:135` 生产文件） |
| 3 | **根一致性（root 传递与路径规范化）** | 绝对 root 让 `-diff` 门禁静默报绿、allowlist 失效、输出泄漏绝对路径 | 同仓同 commit：相对 root 115 findings / 绝对 root 0 findings |

**共同点（符合用户硬指标）**：三者都**不新增外部运行时依赖**、**不新增必需配置**、
改动都落在既有包内（`internal/refs`、`internal/rules`+`internal/config`、
`internal/git`+`internal/engine`+`cmd/metricgate`），且都能用本仓 `check . -diff` 自证。

---

## 附：复现命令

```bash
# 构建
go build -o /tmp/mg-self ./cmd/metricgate

# D1
cd /Users/mac/Downloads/main/bugbye-parser
grep -rlE '^class +[A-Za-z_][A-Za-z0-9_]*\(BaseToolReport' --include='*.py' . | wc -l   # 36
/tmp/mg-self impact . -q BaseToolReport -format json                                       # affectedFiles: 1

# D2
mkdir -p /tmp/t4d2 && cd /tmp/t4d2 && git init -q . && git add -A && git commit -qm base
/tmp/mg-self check . -config policy.yaml    # 2 处误报（helper_test.go + prod/handler.go）

# D3-a（同一仓同 commit，只改 root 写法）
cd /Users/mac/Downloads/metricGate
/tmp/mg-self check . -diff -base HEAD                                  # 115 new findings
/tmp/mg-self check /Users/mac/Downloads/metricGate -diff -base HEAD    #   0 new findings

# D3-b
cd /tmp && /tmp/mg-self check /Users/mac/Downloads/metricGate -config /Users/mac/Downloads/metricGate/policy.yaml -format json
# → banned-fmt-Print* = 153（相对 root 为 4）

# D3-c
cd /Users/mac/Downloads/main/bugbye-parser && /tmp/mg-self arch snapshot   # 25 packages, 0 edges
# ↑ 已于迭代 9 修复：arch 现在 9 语言全覆盖
```
