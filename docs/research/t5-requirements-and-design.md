# 三方向需求分析与方案设计（goal mv407zjq-jab3l6 · t5）

日期：2026-10-12 · 依据：[t4 实测验证](t4-direction-verification.md) ·
[九方向调研](direction-survey-2026-10-12.md)

> 三个方向全部来自 t4 的实测证据，不是源码阅读推断。设计遵循用户硬指标：
> **不新增外部运行时依赖、不新增必需配置、不提升项目复杂度**。

---

## 0. 总览

| # | 方向 | 改动包 | 新增依赖 | 新增必需配置 | 新增公开 API |
|---|---|---|---|---|---|
| D1 | 继承/实现边补齐 | `internal/refs` | 无 | 无 | 无 |
| D2 | `custom_rules` 文件作用域 | `internal/config`、`internal/rules` | 无 | 无 | 无 |
| D3 | 根一致性（root 传递 + 路径规范化） | `internal/engine`、`internal/git`、`cmd/metricgate` | 无 | 无 | 无（`SanitizePath` 已导出） |

三者都不触碰 `go.mod`、不新增 yaml 必填字段、不新增 CLI 子命令。

---

## D1 · 继承/实现边补齐

### 需求分析

**解决什么实际问题**：`impact` 回答"改这个符号会炸哪些地方"，而**继承是最粗的那条边**——
改基类会炸全部子类。今天在 Python / JavaScript / TypeScript 上这条边**完全不存在**。

**实测规模**（`/Users/mac/Downloads/main/bugbye-parser`，154 个 .py）：

```
grep -rlE '^class +[A-Za-z_][A-Za-z0-9_]*\(BaseToolReport' --include='*.py' . | wc -l   → 36
metricgate impact . -q BaseToolReport -format json                                       → affectedFiles: 1
```

**36 个子类，报 1 个。** 受控探针（`/tmp/inh3`，9 语言各一对 Base/Sub）确认
Py/JS/TS 的 refs 为 `[]`，其余 5 语言为 1–2 条。

**为什么是"漏分支"而不是"没数据"**：`internal/refs/refs.go:29-30` 只有
`KindCall`/`KindType` 两个 kind；`grep -n 'superclass\|super_interfaces\|base_class\|heritage'
internal/refs/*.go` → **0 命中**。Java 之所以"看得见"，是 `internal/refs/java.go:18-19`
的 `case "type_identifier"` 顺手捞到了 `extends` 后的裸类型标识符。

**确定性收益**：`impact` / `get_impact` / `get_review_bundle` 三个消费面在 Py/JS/TS 上
从"看不见继承"变为"看得见"。上面那个仓是 1 → 37 个文件。

**如何度量**：
- 主指标：`impact -q <Base> -format json` 的 `affectedFiles` 数量。
  锚点：bugbye-parser 的 `BaseToolReport` 从 **1** 到 **≥36**。
- 反指标（防回归）：`/tmp/inh3` 上其余 5 语言的 refs 数量不得变化；
  `object` 这类**内建类型**必须仍然报 0（无定义）。
- 单测：每个语言一条 heritage 用例 + 一条"多基类/多接口"用例。

### 方案设计

**数据流**（不变）：`parser.New` → `refs.Extract(file, src, tree, lang)` → `refs.Index`
→ `impact.Compute`。只改中间一步的**分支覆盖**。

**改动点**（3 个文件，全部是既有 walker 加分支）：

1. **`internal/refs/py.go`** — `walkPy` 的 `case "class_definition"` 分支内，
   读 `superclasses` 字段（CST 实测见下），对 `argument_list` 的**具名子节点**逐个记 ref。

2. **`internal/refs/js.go`** — `walkJS` 的 `case "class_declaration",
   "abstract_class_declaration"` 分支内，读 `class_heritage`：
   - `extends_clause` 的 `value` 字段（TS 实测是 `identifier`，JS 实测是 `identifier`）
   - `implements_clause` 的 `type_identifier` 子节点（TS 专有）

3. **`internal/refs/refs.go`** — 新增一个共享 helper（两个 walker 都要用）：

```go
// heritageRefs records the base types named by a class's heritage clause.
// Each base becomes one KindType reference: inheriting from a type is a use
// of it, and it is the coarsest edge an impact query can follow.
func (w *walker) heritageRefs(n *sitter.Node, enclosing string, bases []*sitter.Node)
```

**CST 实测（`internal/refs/zzcst` 临时探针，已删除）**：

```
=== PYTHON ===   class Sub(Base, mixins.Other):
  class_definition
    [name]       identifier "Sub"
    [superclasses]
      argument_list "(Base, mixins.Other)"
        identifier "Base"          ← 基类
        attribute "mixins.Other"   ← 限定基类，取 attribute 字段
          [object]   identifier "mixins"
          [attribute] identifier "Other"

=== TYPESCRIPT ===  class SubTS extends BaseTS implements IFoo, IBar {}
  class_declaration
    [name]        type_identifier "SubTS"
    class_heritage "extends BaseTS implements IFoo, IBar"
      extends_clause
        [value]   identifier "BaseTS"
      implements_clause
        type_identifier "IFoo"
        type_identifier "IBar"

=== JAVASCRIPT ===  class SubJS extends BaseJS {}
  class_declaration
    class_heritage
      extends_clause
        [value]   identifier "BaseJS"
```

**与现有 `internal/*` 包的复用关系**：
- 复用既有 `KindType`（`internal/impact/impact.go:47-48` 已在数 `TypeUses`）→ **不新增 kind**
- 复用既有 `walker.add` / `walker.typeRef` / `oneline`
- 复用既有 `w.skip` 机制避免把**声明名**误记成引用
  （注意：TS 的类名是 `type_identifier`，`js.go:16-19` 已把它加进 `skip`）

**配置形态**：**无新增配置**。继承边是事实，不是策略。

**失败与降级行为**：
- `superclasses` 字段缺失（无基类的类）→ 不记 ref，静默跳过（正常情况）
- 基类是复杂表达式（`class X(factory())`）→ 只记**可命名的**部分（`identifier` /
  `attribute` 的 `attribute` 字段），其余忽略。**不猜测**，因为猜错会伪造影响面
- 多基类 → 每条各记一个 ref（`class Sub(Base, Mixin)` → 2 条）

**不提升复杂度的论证**：
- `internal/refs/` 全目录 **687 行**；预计新增约 **35–50 行**（两个分支 + 一个 helper）
- 不新增 kind、不新增配置、不新增依赖、不新增 CLI flag
- 不改变 `Extract` 签名、不改变 `Ref` 结构体、不改变 `Index` 语义

**明确不做**（避免范围蔓延）：
- 不做**跨文件**继承解析（名字匹配即可，与 `internal/impact` 现有语义一致）
- 不做 MRO / 线性化（Python 的 C3 线性化不是 refs 层的职责）
- 不做 Java 侧的 `superName` 统一（`internal/metrics/classes.go:305` 是**同文件 DIT**，
  语义不同，不动）

---

## D2 · `custom_rules` 文件作用域谓词

### 需求分析

**解决什么实际问题**：`custom_rules`（规则 DSL）今天**只能按语言过滤，不能按文件过滤**，
于是"只该管 `_test.go`"的命名规则会打到生产文件和测试辅助函数。

**实测规模**：
- 受控仓 `/tmp/t4d2`：一条 `go-test-func-naming` 规则打出 **2 处误报**，
  其中 `internal/prod/handler.go:4` 是**生产代码**。
- 本仓真实：**6 处误报**

```
internal/bundle/bundle_test.go:18       testConfig         ← 测试辅助函数
internal/digest/digest_test.go:11       testConfig         ← 测试辅助函数
internal/engine/engine_test.go:13       testConfig         ← 测试辅助函数
internal/gatecheck/gatecheck.go:135     TestsTouchedCheck  ← 生产文件
internal/policygap/policygap_test.go:29 testInput          ← 测试辅助函数
internal/symbols/index_test.go:8        testIndex          ← 测试辅助函数
```

**根因**：`internal/config/config.go:160-171` 的 `DslRuleConfig` 字段是
`ID/Title/Severity/Node/Field/MatchRegex/ExcludeRegex/Languages/Reason/Use`——
**没有文件路径谓词**。而同一套匹配逻辑在 `internal/rules/banned.go:406-421` 的
`excluded()` 里**已经实现**（`except_files` + `allowed_files` 的
suffix / prefix / `filepath.Match` 三种匹配）。DSL 只是没接上。

**确定性收益**：本仓 6 处误报当场消失；兑现
`docs/pilot/pilot-report-scan-portal.md` §4 的 P0 建议（"自定义规则 DSL
（最小版=命名模式断言：标识符正则 × 节点类型 × **包上下文**）"）；
`docs/pilot/conversion-map.md` 的 NAMING-01~06 六条命名条款全部卡在同一处。

**如何度量**：
- 主指标：`check . -config policy.yaml -format json` 中
  `ruleId == "custom.go-test-func-naming"` 的条数，锚点 **6 → 0**。
- 反指标：`/tmp/t4d2` 加 `files: ["**/*_test.go"]` 后误报 **2 → 0**，
  且**故意写错的** `func testbad(t *testing.T)` 仍被抓（防止"一加 files 就全静默"）。
- 单测：文件匹配命中 / 不命中 / 未配置 `files` 保持现行为。

### 方案设计

**数据流**（不变）：`config.CustomRules(lang)` → `rules.DslRules` → `dslRule` →
`dslOnMatch`。只在 `dslOnMatch` 里多一道文件谓词。

**改动点**（2 个文件）：

1. **`internal/config/config.go`** — `DslRuleConfig` 加一个字段：

```go
	// Files restricts the rule to matching paths (glob, or a prefix/suffix
	// like "cmd/" or "_test.go"). Empty = every file, so an existing policy
	// behaves exactly as before.
	Files []string `yaml:"files,omitempty"`
```

2. **`internal/rules/dsl.go`** — `dslOnMatch` 开头加一道守卫，并抽一个 helper：

```go
// dslFileMatches reports whether the rule applies to this file. The pattern
// vocabulary mirrors banned.go's excluded(): an exact path, a glob matched
// against the whole path or the base name, or a bare prefix/suffix. Keeping
// the two in step matters because a policy author reads them as one feature.
func dslFileMatches(patterns []string, file string) bool
```

**关键设计选择：复用 `excluded()` 还是新写？**

| 选项 | 评价 |
|---|---|
| 直接调 `banned.go` 的 `excluded(ctx, b)` | ❌ 签名绑死 `Ban` 类型，语义是"排除"而非"限定" |
| 抽公共函数到 `internal/rules` 内共用 | ✅ **选定**——`excluded()` 改为调用同一个 `fileMatch(patterns, file)`，行为**逐字保持**（现有 3 种匹配 + 空串跳过） |

抽公共函数是**净减少**重复，符合"不提升复杂度"。风险点：`banned.go` 的
`except_files`/`allowed_files` 语义**绝不能变**，所以单测必须先把现有行为钉住。

**配置形态**（向后兼容，不写即现行为）：

```yaml
languages:
  go:
    custom_rules:
      - id: go-test-func-naming
        node: function_declaration
        field: name
        files: ["**/*_test.go"]        # ← 新增，可选
        match_regex: '^(?i)(test|benchmark|fuzz|example)'
        exclude_regex: '^(Test|Benchmark|Fuzz|Example)[A-Z_]'
        severity: warning
        reason: "Go test entrypoints must be TestXxx/BenchmarkXxx/FuzzXxx/ExampleXxx"
```

**失败与降级行为**：
- `files` 为空 → 现行为（应用到所有文件）。**不新增必需配置**。
- glob 语法错误 → `filepath.Match` 返回 `err`，该 pattern **不匹配**（不是 fail-fast）。
  理由：与 `banned.go:412-419` 现有行为一致（`m, _ := filepath.Match(...)`），
  且 policy 里的 glob 拼错不应该让整次 check 挂掉——DSL 的既有原则就是
  "a bad entry must never break a run"（`dsl.go:26-28` 包注释原话）。

**不提升复杂度的论证**：
- `DslRuleConfig` **+1 字段**（可选）
- `internal/rules/dsl.go` 约 **+15 行**，`banned.go` 的 `excluded()` 约 **-6 行**（改调公共函数）
- 不新增依赖、不新增 CLI flag、不新增规则、不改变 `Rule` 结构体

---

## D3 · 根一致性（root 传递 + 路径规范化）

### 需求分析

**解决什么实际问题**：`-diff` 门禁在**绝对 root** 下**静默报绿**。
这是四个独立现场同一个根因族：**入口 root 与内部 git/cwd 基准不一致**。

**实测规模**（同一仓库、同一 commit、同一工作树，只改 root 的写法）：

```
$ metricgate check . -diff -base HEAD
diff-only: base=HEAD, 115 new findings, 201 pre-existing hidden
gate: PASSED (fail_on=error, 1 accepted warning(s) by ledger)

$ metricgate check /Users/mac/Downloads/metricGate -diff -base HEAD
diff-only: base=HEAD, 0 new findings, 465 pre-existing hidden
gate: PASSED (fail_on=error)
```

子目录 scoping 同样中招（CI 里 `check "$GITHUB_WORKSPACE/src"` 是常见写法）：

```
$ metricgate check internal/refs -diff -base HEAD
diff-only: base=HEAD, 4 new findings, 1 pre-existing hidden

$ metricgate check /Users/mac/Downloads/metricGate/internal/refs -diff -base HEAD
diff-only: base=HEAD, 0 new findings, 5 pre-existing hidden
```

其余三个现场：

| 现场 | 实测 | 后果 |
|---|---|---|
| **D3-b** allowlist | `banned-fmt-Print*`：相对 root **4** 条 / 绝对 root **153** 条（其中 **149** 条落在 `allowed_files: ["cmd/metricgate/"]` 里） | 149 条假阳性 |
| **D3-c** `arch` 非 Go/Java | bugbye-parser（154 .py）→ `25 packages, 0 edges`；metricGate（Go）→ `103 packages, 343 edges` | 7 语言分层检查**全静默** |
| **D3-d** 输出路径 | `['main.go']` vs `['/tmp/t4d3/main.go']` | 泄漏机器绝对路径，破坏确定性 |

**根因链（D3-a，逐行确认）**：

```
cmd/metricgate/main.go:250   runDiffMode(cfg, root, base, ...)   ← root 传进来了
cmd/metricgate/main.go:253   git.ChangedLines("", git.ResolveBase("", base))
                             └─ 传的是 ""，即进程 cwd，不是 root   ← 断点
internal/git/git.go:38       ChangedLines(dir, base) → LoadChanges(dir, ...)
internal/git/changes.go:350  gitBytes(dir, args...)  cmd.Dir = dir 只在 dir != "" 时设置
                             └─ dir=="" → 用进程 cwd
internal/engine/filter.go:62 rangesFor(changed, file) 只做精确字符串匹配
                             └─ 注释原话 "no suffix matching to avoid false positives"
internal/engine/engine.go:247  files = append(files, f.absolute)
                             └─ 绝对 root 下 finding.File 是绝对路径
                             └─ 与 cwd 相对的 key 永不相等 → 全部 hidden
```

`internal/git/git.go:38-41` 的注释明确承诺
"Reported paths are relative to the repository root **whether or not dir was given**"——
实现没做到。

**为什么这是产品级问题**：绝对 root 是合法用法，且 CI/pre-commit 里很常见
（worktree 路径、容器挂载路径、`$GITHUB_WORKSPACE/src`）。它**不报错，它报绿**。
这正是 `docs/claims.md` #15（闸门在真实 agent 循环被调用，标 ⏳ 未测）
最怕的失败模式：门禁看起来在工作，实际什么都没检查。

**这是既有缺陷，不是本次引入**：`git show HEAD:cmd/metricgate/main.go:299` 已是
`git.ChangedLines("", git.ResolveBase("", base))`。

**确定性收益**：门禁不再假绿。这是三个方向里**唯一一个"安全性质"的修复**——
前两个是能力/精度，这个是**正确性**。

**如何度量**：
- 主指标（回归锚点）：同一仓同 commit，`check . -diff` 与
  `check <绝对路径> -diff` 的 `new findings` 数量必须**相等**。
  今天：115 vs 0。修后：两者相等（且都等于相对 root 的值）。
- 反指标：`check . -diff` 的既有 115 条**不得变化**（不能靠"把相对 root 也弄坏"
  来制造相等）。子目录场景 4 条同样。
- 单测：绝对 root 与相对 root 在**同一份 fixture 仓**上产出相同的
  finding 集合（file 字段逐条相等）。

### 方案设计

**三个子改动，按优先级：**

#### D3-a / D3-d：把 root 传下去 + 规范化 finding 路径

**改动点 1** — `cmd/metricgate/main.go:253`：

```go
changed, err := git.ChangedLines(root, git.ResolveBase(root, base))
```

注意 `ResolveBase(dir, base)` 的第一参数也是 `dir`（`internal/git/git.go:31`），
**今天同样传空**。两处一起改。

**改动点 2** — 在引擎出口规范化路径。`internal/engine/engine.go:257`
`res.path = path` 与 `res.findings` 里的 `File` 都取 `path`（绝对）。
选**引擎出口**而不是每个规则里改：

```go
// Engine.Run / RunDetailed 收尾处，对 summary 做一次统一规范化
for i := range summary.Findings {
    summary.Findings[i].File = selection.SanitizePath(root, summary.Findings[i].File)
}
```

`selection.SanitizePath`（`internal/selection/selection.go:142`）**已导出且已实现**
正确语义（root 下绝对路径 → root-relative；其余绝对路径 → base name；
相对路径规范化且不改写）。它的文档注释已经承诺
"behavior is identical whether root is "." or absolute"——**只是 engine 从没调它**
（`grep -rn 'SanitizePath' internal/engine/` → 0 命中）。

**为什么放在出口而不是 `checkFile`**：`res.path` 还用于
`summary.ParseErrors`（`worker.go:100`）与 `archFileOf`（`engine.go:293`），
两者需要**真实文件系统路径**去读文件/建包身份。只在**面向输出的 finding/metric 字段**上
规范化，避免把"读文件用的路径"和"报告用的路径"混为一谈。

**风险与对策**：`FilterSummary` 在 `runDiffMode` 里**先于** `emitReports` 运行，
而规范化若放在 `RunDetailed` 内部，则 `-diff` 过滤看到的就是规范化后的路径 →
**与 cwd 相对的 git key 自然匹配**。这正是我们要的。但必须同时确认
`scorecard`/`coupling`/`gatecheck` 这些吃 `changed` map 的消费者不会被打破
（它们已经各自调 `selection.SanitizePath`，见 `internal/scorecard/scorecard.go:77`）。

#### D3-b：allowlist 根敏感性

一旦 finding.File 变成 root-relative，`banned.go:406-421` 的
`HasPrefix(ctx.File, "cmd/metricgate/")` 自然命中，**不需要单独改 banned.go**。
D3-b 是 D3-a 的**副产物**——这就是为什么它们必须一起做。

#### D3-c：arch 多语言

**改动点**：`internal/arch/extract.go:32-41` 的 switch 加语言分支。

**复用**：`internal/deps/scan_lang.go:118-155` 的 `importQueries` map 已经有
**全部 9 语言**的 import 查询（Go/TS/JS/Py/C/Cpp/CS/Rust + Java 在
`internal/arch/extract.go:94 extractJava`）。

**关键设计决策：包身份（package identity）** —— 这是 D3-c 唯一有语义分歧的地方：

| 语言 | 包身份 | 依据 |
|---|---|---|
| Python | 目录（`__init__.py` 的祖先目录） | 与 Go 同形 |
| TypeScript/JavaScript | 目录 | 与 Go 同形 |
| C/C++ | 头文件所在目录 | 无模块系统，只能按目录 |
| C# | namespace 声明 | 有 `namespace_declaration` |
| Rust | `mod` 路径 / crate 根 | `use` 的 crate-root 归约已有（`deps.rustImporter`） |

**范围收窄决策**：D3-c 的工作量与语义分歧**明显大于** D3-a/D3-b。
t5 建议 **D3 只交付 D3-a + D3-b + D3-d**（三者是同一个修复路径，
D3-d 是 D3-a 的必然结果），**D3-c 单独记为"已实测确认、设计已定、待排期"**，
理由：

1. D3-a/D3-b/D3-d 是**正确性**修复，D3-c 是**覆盖扩展**——优先级不同。
2. 用户硬指标里的"项目本身复杂度不能提升"：D3-a/D3-b/D3-d 是**净简化**
   （删掉一个不一致），D3-c 是**净增加**（5 个语言的包身份规则）。
3. 三个方向（D1/D2/D3）的 t6 实现量已经不小，把 D3-c 塞进来会让 D3
   变成"一个方向里装两个方向"。

**这一收窄需要用户确认**（t5 contract 要求交付前经用户确认）。

**配置形态**：**无新增配置**（D3-a/D3-b/D3-d）。D3-c 同样无新增配置。

**失败与降级行为**：
- 非 git 目录 + `-diff` → 今天的行为是 `fail(err)` 退出 2（实测：
  `metricgate: git changed paths: exit status 129 (requires a git repository)`）。
  改后不变——这个报错是**对的**，不能降级成"0 findings"。
- 规范化后路径为空（`SanitizePath` 对空串返回空）→ 保留原值，不产出空 file 字段。

**不提升复杂度的论证**：
- `main.go` **2 行**改动
- `engine.go` **约 8 行**（一次规范化循环 + 注释）
- 不新增依赖、不新增配置、不新增 CLI flag、不新增包

---

## 4. 三方向共同的三触点闸门循环（t6 执行方式）

按 `AGENTS.md` 与本仓约定，每个方向的**每段代码**产出后立即：

1. `get_constraints` / `find_symbols`（生成前，拿本仓约束与可复用符号）
2. `check_code`（虚拟文件名按扩展名选语言）→ 修完 findings 再继续
3. `check_path`（收尾，必要时 `-diff` 口径）

**阈值纪律**（t6 contract 明确要求）：新增 helper 必须**当场拆分**以满足本仓
自检阈值（cyclomatic ≤15 / cognitive ≤25），**不得靠 accept 账本掩盖**。
预估风险点：
- `dslFileMatches` —— 与 `excluded()` 同形，分支数接近阈值，**抽公共函数本身就是拆分**
- `heritageRefs` —— 三种 base 形态（identifier / attribute / type_identifier）+
  空值守卫，预计 cyclomatic ~8，安全
- `engine` 的规范化循环 —— 单循环，安全

---

## 5. 交付顺序与验收锚点

| 顺序 | 方向 | 验收锚点（可复现命令） |
|---|---|---|
| 1 | D1 继承边 | bugbye-parser `impact -q BaseToolReport` affectedFiles **1 → ≥36**；`/tmp/inh3` 其余 5 语言不变 |
| 2 | D2 文件作用域 | 本仓 `custom.go-test-func-naming` **6 → 0**；`/tmp/t4d2` 故意写错的仍被抓 |
| 3 | D3 根一致性 | 同仓同 commit `check . -diff` 与 `check <绝对路径> -diff` **new findings 相等**；allowlist 绝对 root **153 → 4**；输出无绝对路径 |

**D1 → D2 → D3 的顺序理由**：D1 最独立（只碰 `internal/refs`）；
D2 次之（碰 `config`+`rules`）；D3 碰 `engine` 出口，会改变**所有** finding 的
file 字段，放最后可以让 D1/D2 的验收锚点先在**稳定**的路径语义下确认。

---

## 6. 三处待定项的默认决定（已按会话授权自行拍定）

用户在本次 goal 下达时已明确授权：**「不用停下来，直接做，另外本次会话后面也都不用询问了，
全部按照你的默认推荐来做」**。因此本节记录的是**默认决定**而非待确认问题；
若用户事后不同意，任一决定都可单独回滚。

1. **D3 的范围收窄 → 决定：收窄。**
   D3 只交付 D3-a（root 传递）+ D3-b（allowlist，D3-a 的副产物）+ D3-d（路径规范化）。
   **D3-c（arch 多语言）记为「已实测确认、设计已定、待排期」，不进 t6。**
   理由：D3-a/b/d 是**正确性**修复且是**净简化**（删掉一处不一致）；
   D3-c 是**覆盖扩展**且是**净增加**（5 种语言的包身份规则），
   与用户硬指标「项目本身复杂度不能提升」相左。

2. **D3-a 会让部分 CI 从绿变红 → 决定：接受，并视为修复的证明。**
   修后 `check <绝对路径> -diff` 会**多报**（今天误报 0 条）。
   那些 finding **本来就该报**——今天不报是因为路径基准不匹配导致它们被误判为
   "pre-existing hidden"。修复让一个**假绿**变成**真红**，这正是 D3 的全部价值。
   风险控制：D3 的验收锚点包含**反指标**——`check . -diff` 的既有 115 条
   **不得变化**，防止「把相对 root 也弄坏」来制造相等。

3. **D2 的字段名 → 决定：`files`。**
   与 `banned.go` 的 `except_files`/`allowed_files` 词汇同族但更短；
   `yaml:"files,omitempty"`，不写即现行为（不新增必需配置）。

**唯一影响范围的一处判断**（在此显式留痕，便于用户否决）：
D3-a 会把 `cmd/metricgate/main.go` 的 `git.ChangedLines("", ...)` 改成
`git.ChangedLines(root, ...)`。这会让**仓库外调用**（如 MCP `gate.go:104`
已传 `g.root`、`bundle.go:250` 已传 `req.Root`）与 CLI 行为对齐——
即 CLI 向既有正确实现看齐，而不是发明新语义。
