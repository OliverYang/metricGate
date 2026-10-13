# 迭代 8 · t6 三个方向的实现（继承边 / 规则文件作用域 / root 一致性）

日期：2026-10-12（goal mv407zjq-jab3l6 task-t6）

## 为什么做这三件

t3 广度调研了 9 个方向、t4 从中实测选出 3 个（D1/D2/D3），t5 出了需求分析与方案设计。
本文是 t6 的实现记录。三个方向的共同判据是**同一个**：不是"能力不够强"，而是
**已经付了成本的那部分能力被丢掉了**。

| 方向 | 已付的成本 | 被丢掉的部分 |
|---|---|---|
| D1 继承边 | 9 语言 refs walker 都在跑 | 继承/实现位置写的是裸类型名，只有 Go/Java 的 walker 顺带捞到 |
| D2 规则文件作用域 | `banned` 的 `except_files`/`allowed_files` 早已实现 | DSL 规则完全没有文件谓词，命名断言只能全仓生效 |
| D3 root 一致性 | root 参数一路传进 `runDiffMode` | 最后一步没传下去，`-diff` 在子目录/绝对路径下坐标系错配 |

## 交付物

| 交付 | 位置 | 说明 |
|---|---|---|
| 继承边提取 | `internal/refs/refs.go` | `heritageNameKinds` + `(w *walker) heritageRefs(candidates, enclosing)` + `baseName(text)` |
| Python 基类 | `internal/refs/py.go` | `pySuperclasses(n)`：读 `superclasses` 字段的 `argument_list`，跳过 `keyword_argument` |
| TS/JS 基类 | `internal/refs/js.go` | `declName` / `jsClass` / `jsHeritage` / `childByKind` / `namedChildren` |
| 规则文件谓词 | `internal/config/config.go` | `DslRuleConfig.Files []string \`yaml:"files,omitempty"\`` |
| 文件匹配 | `internal/rules/banned.go` | `fileMatches(patterns, file)` + `filePatternMatch(pat, file)`，含 `**/` 前缀特判 |
| 规则作用域门 | `internal/rules/dsl.go` | `dslOnMatch` 开头 `if len(files) > 0 && !fileMatches(files, ctx.File) { return nil }` |
| git 根运行 | `internal/git/changes.go` | `gitDir(dir)`；`LoadChanges` 从 worktree 根跑；`fillHunks` 抽出 |
| 重定基查询 | `internal/git/git.go` | `ChangedLinesFor(checkRoot, base)` + `rebaseChanges` + `rebaseOne` |
| 路径规范化 | `internal/engine/engine.go` | `RunDetailed` 内 `workRoot := filepath.Abs(root)`；`checkFile` 首行 `res.path = selection.SanitizePath(root, path)` |
| 调用点 | `cmd/metricgate/main.go`、`cmd/metricgate-mcp/checks.go` | 改用 `git.ChangedLinesFor(root, base)` |
| 单测 | `internal/refs/refs_test.go`（+4）、`internal/rules/dsl_test.go`、`internal/rules/filematch_test.go`（新建）、`internal/git/changes_test.go`（+3）、`internal/engine/reportpath_test.go`（新建） | 见下 |

## D1 · 继承/实现边

### 关键设计决策

- **`heritageNameKinds` 里 `type_identifier` 刻意缺席。** TS 的 `implements IFoo` 用的是
  `type_identifier`，而 `walkJS` 的通用 `case "type_identifier"` 已经记录了它。列进来会
  **重复计数**——`TestTSHeritageCoversExtendsAndImplements` 当场抓到 IFoo/IBar 各 2 条。
  白名单是 `identifier` / `attribute` / `member_expression` / `scoped_type_identifier`。
- **`baseName` 拆 receiver。** `mixins.Other` → `("Other", "mixins")`，让
  `impact` 的 receiver 区分继续生效（与 t2 的 facts 符号层同一语义）。
- **Python 跳过 `keyword_argument`。** `class Sub(Base, metaclass=Meta)` 里
  `metaclass=Meta` 不是基类；只有位置参数是。
- **`class_heritage` 是位置子节点，不是命名字段。** node-types.json 证实 TS 的
  `class_declaration` fields 只有 `body/decorator/name/type_parameters`，所以
  `ChildByFieldName("heritage")` 拿不到——必须走 `childByKind`。JS 的 `extends X`
  更极端，连包装节点都没有（CST 实测 `class_heritage → extends(匿名) → identifier`）。
- **猜错会伪造影响面，所以宁可少报。** `class Sub(make_base())` 必须 0 条 type ref
  （`TestHeritageSkipsComputedBases` 钉住）——把动态计算的基类记成继承边，
  等于给 `impact` 编造调用方。

### 实测证据

`/Users/mac/Downloads/main/bugbye-parser`（Python 仓，154 个 .py）：

| 命令 | 修复前 | 修复后 |
|---|---|---|
| `impact . -q BaseToolReport -format json` | defs 0, refs 1, affectedFiles 1 | **defs 1, refs 37, affectedFiles 37, calls 1, typeUses 36** |

对照真值：`grep -rlE '^class +[A-Za-z_][A-Za-z0-9_]*\(BaseToolReport' --include='*.py' . | wc -l`
= **36**，与 `typeUses 36` 完全对齐。

九语言反指标 `/tmp/inh3`：`Base(py)` 1、`BaseJS` 1、`BaseTS` 1、`BaseCS` 2、`BaseRS` 2、
`BaseCpp` 2、`BaseGo` 1、`BaseJ` 1 —— 修复前 py/js/ts **全为 0**，其余 5 语言数字不变
（证明没有把已有语言的计数改坏）。

## D2 · custom_rules 文件作用域

### 关键设计决策

- **字段名 `files`，词汇与 `banned` 的 `except_files`/`allowed_files` 同族。** 不新造方言。
- **`**/` 前缀必须特判，否则静默失效。** Go 的 `filepath.Match` 不允许 `*` 跨 `/`，
  所以 `**/*_test.go` 在 Go 语义下只能匹配"恰好一层目录"，**根目录文件被静默跳过**。
  `/tmp/d2probe` 实测：`files: ["**/*_test.go"]` 命中 **0 个**（`internal/deep/a_test.go`
  与 `root_test.go` 都没抓到），而 `files: ["_test.go"]`（后缀形）命中 2 个。
  `filePatternMatch` 因此特判 `pat == "**"` 与 `strings.CutPrefix(pat, "**/")`；
  更深的 `**` 仍按普通 `*`（单段）处理，注释明说"不猜半吊子 glob 方言"。
- **拆 `fileMatches`/`filePatternMatch` 两个函数是为了过 cognitive 阈值**：合并版
  cognitive 17 > warn 15。
- **规则语义本身也是错的，不只是缺文件谓词。** 加 `files: ["**/*_test.go"]` 后本仓
  `custom.go-test-func-naming` 只从 6 降到 5：生产文件的
  `internal/gatecheck/gatecheck.go:135 TestsTouchedCheck` 消失了，但 5 个 `_test.go` 里的
  **辅助函数**（`testConfig`/`testInput`/`testIndex`）仍在。根因：`field: name` 只看得见
  名字，而"是不是 `go test` 会跑的入口"根本不是名字的属性。**改为整节点匹配**，
  让签名可见：

```yaml
        node: function_declaration
        files: ["**/*_test.go"]
        match_regex: '^func\s+(?i:test|benchmark|fuzz|example)[A-Za-z0-9_]*\s*\((?:[^()]|\([^()]*\))*\)\s*\{'
        exclude_regex: '^func\s+(Test|Benchmark|Fuzz|Example)[A-Z_]'
```

  `(?:[^()]|\([^()]*\))*` 允许一层括号嵌套——`[^()]*` 会**漏报**
  `func testFuncParam(t *testing.T, f func())`（/tmp/d2b 实测确认）。

### 实测证据

- **主指标**：本仓 `custom.go-test-func-naming` **6 → 0**。
- **反指标**：`/tmp/d2anchor` 里 `func testbad(t *testing.T)` **仍被抓**；
  `func testHelper(t *testing.T) *int`（辅助函数，有返回值）**不抓**。
- **作用域本身**：`/tmp/d2scope`（`prod/a.go` 与 `tests/b_test.go` 各一个 `scopeBad`）
  只报 `tests/b_test.go:3`。
- `/tmp/t4d2`（复现原误报的最小仓）改用新规则形态后 2 条误报清零。

## D3 · root 一致性

### 三个缺陷，两条根因

D3 在 t4 阶段实测出**三个**独立缺陷，t6 全部修掉：

**（a）绝对 root 下 `-diff` 静默报绿。**
`cmd/metricgate/main.go:250 runDiffMode(cfg, root, base, …)` 把 root 传进来了，
但 `:251` 调 `git.ChangedLines(root, git.ResolveBase(root, base))`，而
`internal/engine/engine.go:247 files = append(files, f.absolute)` 使**绝对 root 下
finding.File 是绝对路径**，与 git 的 root 相对 key 永不相等 → 全部被算作
pre-existing hidden → 门禁静默报绿。实测同仓同 commit：相对 root 115 findings，
绝对 root 0 findings。
修法：`RunDetailed` 内 `workRoot, err := filepath.Abs(root)`，`collect`/`runWorkers`
统一用 `workRoot`；`checkFile` 首行 `res.path = selection.SanitizePath(root, path)`。

**（b）allowlist 副产物。** `banned-fmt-Print*` 在相对 root 下 4 条、绝对 root 下
153 条（149 条落在 `allowed_files: ["cmd/metricgate/"]`）。修（a）后两侧均为 **4**。

**（c）子目录 root 下 `-diff` 静默漏报（独立缺陷，本迭代新定位）。**
`internal/git/changes.go:52 LoadChanges` 用 `gitBytes(dir, patchArgs(normalized, paths)...)`，
而 `paths` 来自 `changePaths` = **repo-root-relative**，pathspec 又是在 `cmd.Dir=子目录`
下解析的 —— 一个也匹配不到。`/tmp/d3g` 实测（在 `internal/core` 里跑）：

```
git diff -U0 HEAD -- internal/core/core.go   → 空输出
git diff -U0 HEAD -- core.go                 → 正常 patch
git diff --name-status HEAD                  → M	internal/core/core.go   （子目录里也正常）
```

后果链：`Hunks` 为空 → `ChangedLinesFromChanges` 只投影 `NewCount > 0` 的 hunk →
`changed` 为空 map → `FilterSummary` 把**全部** findings 判为 pre-existing hidden → 报绿。

**坐标系总结**：git key = **worktree-root-relative**；engine 报告路径 =
**check-root-relative**。`root != repoRoot` 时两者不同坐标系，而
`internal/engine/filter.go:62 rangesFor` 只做精确字符串匹配（注释原话
"no suffix matching to avoid false positives"），所以错配是**静默**的。

### 关键设计决策

- **`LoadChanges` 改为从 worktree 根跑，而不是在调用方补路径。**
  新增 `gitDir(dir)`：`if root := Root(dir); root != "" { return root }; return dir`。
  理由：`--name-status`、`ls-files`、`patch` 都是 repo 级输出、与 cwd 无关，而 pathspec 是
  repo-root-relative；从根跑严格更正确，clonedrift/bundle/gate/MCP 一并受益。
  非仓库路径原样返回，保住"requires a git repository"这条既有错误措辞
  （`TestLoadChangesOutsideRepoKeepsTheGenericMessage` 仍绿）。
- **新增 `ChangedLinesFor(checkRoot, base)` 而不是改 `ChangedLines` 的契约。**
  `ChangedLines` 的 repo-root-relative 契约被 `cmd/metricgate/clonedrift.go:34-35` 与
  `internal/bundle/bundle.go:250` 依赖（后者的 `selection.Decide(change.Path, …)`、
  `langByPath[d.Path]`、`isIncluded(plan.decisions, change.Path)` 都假设同一坐标系），
  所以只加一个重定基的包装：`filepath.Rel(checkBase, filepath.Join(repoRoot, rel))`，
  rel 以 `..` 开头或为绝对则丢弃该 key。
- **`checkBase` 必须 `EvalSymlinks`。** 这是本次唯一一个"差点交付一个看起来对、
  实际全空"的实现细节：`repoRoot` 来自 `Root()`（内部 `EvalSymlinks`），而
  `checkBase` 只是 `filepath.Abs`。在 macOS 上 `/tmp` → `/private/tmp`，两者不在同一
  规范空间，于是**每一条路径都被判为"逃出 check 作用域"而丢弃**，`changed` 全空 ——
  与修复前的症状一模一样。探针实测（`/tmp/d3h`，`git init` 在 `/tmp/d3h`）：

```
修 EvalSymlinks 前：ChangedLinesFor("/tmp/d3h") → map[]           ← 空
修 EvalSymlinks 后：ChangedLinesFor("/tmp/d3h") → map[internal/core/core.go:[{3 10}]]
```

- **`fillHunks` 抽出。** 加 `gitDir` 后 `LoadChanges` 的 fan-out 从 15 涨到 16，
  被本仓自检当场抓到（`internal/git/changes.go:57 fan-out of "LoadChanges" is 16`）。
  抽出一个 `fillHunks(root, req, *FileChange)` 后回到基线，**没有用 accept 账本掩盖**。

### 实测证据

`/tmp/d3h`（git init；base commit 为 `func Simple() int { return 1 }`，工作区改成带
两层嵌套 if 的版本；`policy.yaml` 设 `cyclomatic-complexity { warn: 2, error: 3 }`）：

| root | 修复前 | 修复后 |
|---|---|---|
| `.` | 1 new / 0 hidden | 1 new / 0 hidden |
| `/tmp/d3h` | 1 new / 0 hidden | 1 new / 0 hidden |
| `internal/core` | **0 new / 1 hidden（假绿）** | **1 new / 0 hidden** |
| `/tmp/d3h/internal/core` | **0 new / 1 hidden（假绿）** | **1 new / 0 hidden** |
| `internal/core/core.go` | **崩溃**：`fork/exec /usr/bin/git: not a directory` | **1 new / 0 hidden** |
| `/tmp/d3h/internal/core/core.go` | **崩溃** | **1 new / 0 hidden** |

非 `-diff` 四路等价（本仓）：`check .` / `check $PWD` 各 308 findings；
`check internal/refs` / `check $PWD/internal/refs` 各 4 findings。
`-diff` 四路等价（本仓）：`.` 与 `$PWD` 各 `116 new findings, 192 pre-existing hidden`；
`internal/refs` 与其绝对路径各 `3 new, 1 hidden`。

### 单测

- `internal/git/changes_test.go`：`TestLoadChangesFromSubdirectoryKeepsHunks`（同一改动
  从 worktree 根与从 `internal/core` 查，都必须拿到 1 个带 hunk 的 change，且 path 是
  worktree 相对）、`TestChangedLinesForRebasesOntoCheckRoot`（三种 root 拼法分别得到
  `internal/core/core.go`+`other/other.go` / `core.go` / `core.go`）、
  `TestChangedLinesForOutsideRepoFailsLoudly`（**静默空 map 会被读成"没改动"**）。
- `internal/engine/reportpath_test.go`：`TestRunReportPathsAreRootRelative` 五例
  （`root relative dot`→`keep/a.go`、`root absolute`→`keep/a.go`、`root relative name`→
  `a.go`、`sub absolute`→`a.go`、`inside sub dot`→`a.go`），helper `assertReportPaths`
  + `containsPath` + `chdir`。**反向验证过**：临时还原修复后 `root relative name`
  一例 FAIL，恢复后通过。

## 五性核对

| 性 | 结论 |
|---|---|
| 可介入 | ✅ D1 让非 Go/Java 仓的 `impact` 从"几乎不可用"变为可用（Py 仓 defs 0→1、typeUses 36）；D2 让命名断言真正能限定到测试文件（本仓 6 条误报→0）；D3 把三条**静默假绿**路径变成真红 |
| 可审计 | ✅ 继承边与调用边共用 `KindType`+receiver，来源可追；`files` 与 `except_files` 同词汇，一条规则的作用域一眼可见；`ChangedLinesFor` 明确记录了两个坐标系的重定基规则 |
| 可度量 | ✅ Py 仓 `typeUses 36` 与 `grep` 真值 36 对齐；本仓自定义规则 6→0；`-diff` 六路等价可复跑 |
| 可回溯 | ✅ `checkFile` 单点规范化（arch 图抑制、`clones.Func.File`、ban 的 `allowed_files` 三者读到的路径因此一致）；`**/` 特判与 `EvalSymlinks` 都有注释记录"不这么做会怎样" |
| 可观察 | ✅ `impact -format json` 直接给出 typeUses；`-diff` 头部打印 `N new findings, M pre-existing hidden`，M 异常变大即是坐标系错配的信号 |

## 阈值纪律（本迭代被自检抓到 4 次，全部当场拆函数）

| 位置 | 触发 | 处理 |
|---|---|---|
| `internal/git/changes.go:57 LoadChanges` | fan-out 15→**16** | 抽 `fillHunks` |
| `internal/rules/dsl.go` | cognitive 23 / cyclomatic 13 / fan-out 11 / function-length 53 / npath 288 | 抽 `dslTextNode`/`dslViolates`/`dslMessage`/`dslFinding` → 0 warnings |
| `internal/rules/banned.go` | 合并版 `fileMatches` cognitive 17 | 拆 `fileMatches`/`filePatternMatch` |
| `internal/engine/reportpath_test.go` | 测试函数 cognitive 26 > error 25 | 抽 `assertReportPaths` |

## 验证

- `gofmt -l` 干净；`go build ./...` OK。
- `go test ./internal/git/ ./internal/engine/ ./internal/bundle/ ./internal/clonedrift/ ./internal/scorecard/ ./internal/gatecheck/ ./internal/regress/ ./cmd/metricgate/ ./cmd/metricgate-mcp/ -count=1` 全 ok。
- `go test ./...` 仅剩既有失败 `internal/digest TestBuildTaskFilteredBans`
  （`internal/digest/digest_test.go:389`，用户既有未提交改动，**不得归因**）。
- 本仓自检 `check . -diff -base HEAD -config policy.yaml`：**PASSED**
  （0 errors / 116 warnings / 1 accepted by ledger / 1 suppressed，run #668）。
- MCP `check_path . diff_only=true`：**PASS**（0 errors / 118 warnings / 207 hidden / 226 files）。
- `git diff --check` 干净。

## 遗留

- **D3-a 会让部分 CI 从绿变红。** 这是**假绿变真红**，正是 D3 的价值；
  但下游若依赖旧的"绝对路径也是绿"，需要重新跑一次确认。
- **clonedrift 的绝对 root 仍不匹配**（既有缺陷，未修）：它的 `changed` 必须与
  `workFiles = sources.Files(root)` 同坐标系，而后者在绝对 root 下给绝对路径、
  `changed` 给 repo 相对 key。记为遗留。
- **D3-c（arch 多语言抽取）** 已实测确认、设计已定，按 t5 §6.1 记录为"待排期"，
  本迭代不做（正确性修复 vs 覆盖扩展；净简化 vs 净增加）。
- `internal/git/changes.go` 的 `LoadChanges` 仍有 fan-out 12（阈值 10）——已回到基线，
  未进一步拆（继续拆会把一个线性流程切碎）。
