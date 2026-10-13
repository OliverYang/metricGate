# 迭代 7 · facts 符号层补齐 7 种语言（符号索引的跨语言追平）

日期：2026-10-12（goal mv407zjq-jab3l6 task-t2）

## 为什么做这个

不是加新维度，是**补已付成本却未回收的部分**。`internal/facts/facts.go` 的 `parseFile`
（`facts.go:179-200`）对**全部 9 种语言**都做了解析，也填了两个跨语言字段：

```go
f := Extract(path, src, tree, lang)                                  // ← 只填 Go/Java
f.CloneFns = clones.Extract(lang, path, src, tree, clones.Default)   // 9 语言
f.Refs = refs.Extract(path, src, tree, lang)                         // 9 语言
```

而 `Extract` 的 switch 只有 `case LangGo` / `case LangJava` → `f.Symbols` 对其余 7 种语言为空。
解析成本已经付了，符号索引这一半却是空的。

**实测后果**（这是本次要修的那个数）：Python 仓 `/Users/mac/Downloads/main/bugbye-parser` 上

```
metricgate impact -q BaseToolReport -format json
→ definitions: 0, references: 1, affectedFiles: 1
  note: "no definition in this tree; the symbol is external (dependency or generated code)"
```

约 20 个调用点全部走 `facts.Collect*`，所以非 Go/Java 仓上 `symbols` · `find_symbols` ·
`impact` · `get_impact` · `get_review_bundle` · `get_constraints` · `get_repo_map` 一起失效。

**同一根因的第二处**：`internal/clones/clones.go` 的 `declKinds` 也只有 Go/Java，
所以 `nearclones` / `clonedrift` 对其余 7 语言是死的——而指纹逻辑本身语言无关。
第二处还有一层：`internal/clones/near.go` 的 `findBody` 按**名字+行号**重新定位 body 节点
（因为 `Extract` 不返回节点），`isDeclKind` 硬编码只认 3 个 Go/Java kind。对 C/C++
（名字在声明器链里，`function_definition` 根本没有 `name` 字段）与 C# 字段必然失败。

## 交付物

| 交付 | 位置 | 说明 |
|---|---|---|
| 表驱动抽取器 | `internal/facts/lang.go`（新建） | `declSpec{Kind,Name,Members,NameFromType,FuncValue}` + `langSpec{Decls,Containers,OwnerKinds,Vis}`；`extractTable(f,src,root,lang) bool`；`langWalker.descend(n, owner)`；`symbol`/`members`/`name`/`signature`/`modifiersText`；可见性函数 `tsVis`/`pyVis`/`cVis`/`csVis`/`rustVis` |
| 抽取分派 | `internal/facts/facts.go` | `Extract` 的 switch 改为 `case LangGo` / `case LangJava` / `default: extractTable(...)`。Go/Java 保留专用 extractor |
| 克隆 kind 表 | `internal/clones/clones.go` | `declKinds` 补齐 9 语言；新增导出的 `DeclKinds(lang) []string`；`Func` 增加未导出 `body *sitter.Node`（"only valid while the tree is alive"），`Extract` 填充 |
| 近克隆定位 | `internal/clones/near.go` | `fileShingles` 直接用 `f.body`；**删除** `findBody` 与 `isDeclKind` |
| 单测 | `internal/facts/lang_test.go`（重写） | 11 个测试，见下 |

## 关键设计决策（含理由）

- **表驱动，而不是 7 个 extractor。** Go/Java 的专用 extractor 要处理 receiver 语义、
  modifier 解析、interface method 提升（`goInterfaceMethods`），表表达不了。其余 7 种语言的
  差异只是"哪个 kind 是声明 / 名字在哪个字段 / 成员挂在哪个字段"，正好是表。
- **只遍历 `NamedChildCount()`，不递归函数体。** 这是最关键的不变式：函数体内定义的局部
  class/def **不进索引**（否则每个测试里的 `t.Run` 闭包都会污染符号表）。
  由 `TestExtractSkipsFunctionBodies` 钉住。
- **`OwnerKinds` 用来给 `impl Foo {}` 找 receiver。** Rust 的 `impl_item` 没有自己的名字
  （只有 `type` 字段），但它是所有内部方法的 owner。两个 `impl` 块因此锚到同一个类型——
  这正是 receiver 的语义。
- **`FuncValue` 只收函数值。** TS/JS 的 `variable_declarator` 只有当 `value` 是
  `arrow_function`/`function_expression`/`generator_function` 时才算声明：
  `export const helper = () => …` 进索引，`export const MAX_SIZE = 10` 不进。
  否则每个常量都会变成"符号"。
- **C/C++ 走声明器链取名字。** `function_definition` 没有 `name` 字段，名字埋在
  `declarator` → `function_declarator` → `declarator` 里（最多 8 层）。C# 的
  `field_declaration` 更绕：它自己**没有任何 field**，名字在下一层 `variable_declaration`
  → `variable_declarator.name`，所以有 `varDeclaratorName` 兜底。
- **可见性是"猜错会把真实 API 面藏起来"的方向上偏保守。** C# 无修饰符默认 **exported**：
  顶层类型默认 internal、成员默认 private，但把 public API 误判成 private 会让
  `impact` 漏掉调用方，反向误判只是多几条噪声。
- **`clones.Func.body` 是未导出字段。** 树的生存期由调用方持有，所以注释写明了
  "only valid while the tree is alive"。这让近克隆 pass 复用同一个节点，
  删掉了按名字+行号重定位这一整类 bug（C/C++/C# 上根本重定位不到）。

## 实测证据

**真实仓（Python）`/Users/mac/Downloads/main/bugbye-parser`**

| 命令 | 修复前 | 修复后 |
|---|---|---|
| `impact -q BaseToolReport -format json` | defs 0, refs 1, files 1 | **defs 1, refs 1, files 1** |
| `impact -q ParseGeneralToolReport -format json` | defs 0 | **defs 2, refs 2, files 2** |
| `symbols -q BaseToolReport -max 3` | 空 | `analysis/reportBase/reportparser.py:15  class  class BaseToolReport(object):` |
| `symbols -q parse` | 空 | 4 个不同类的同名方法（`AST.parse` / `DiffHelper.parse` / `JunitParse.parse` …），**receiver 区分生效** |

**受控 7 语言探针 `/tmp/symprobe`**（`m.py` / `m.ts` / `m.js` / `m.c` / `m.cpp` / `m.cs` / `m.rs`）：
dump 全部 `Defs` 逐条核对 kind/name/receiver/visibility/signature/line 全部正确。
要点：C 的 `static int internal_add` → private（`storage_class_specifier`）；
Python `_helper` → private、`__init__` → exported（dunder 例外）；
TS `export const helper = () => …` 进索引而 `export const MAX_SIZE = 10` 不进；
Rust 两个 `impl` 块的 `new`/`hidden` 都锚到 `Point`，`impl` 本身不是 symbol；
C++ namespace `geo` 内的 `Circle` 是成员、类内 `area()` receiver=`Circle`、自由函数 `total` receiver=`geo`。

**TS / C# / Rust 仓**：`/tmp/tsprobe` 的 `symbols -q HttpService` → `src/service.ts:2 class`，
`impact -q HttpService` → defs 1 refs 4 files `[src/service.ts, src/use.ts]`，
`context .` 现在能列出 `interface Client` / `class HttpService` / `function makeService(...)`；
`/tmp/csprobe` 的 `impact -q ITransport` → defs 1 refs 2；
`/tmp/rsprobe` 的 `impact -q Transport`（trait）→ defs 1 refs 2。

**近克隆（第二处根因）**：`/tmp/cclone`（两个结构相同、仅改名/改字面量的 C 函数）
→ `nearclones .` 报 `a.c:2 (<anonymous>) ~100% b.c:2 (<anonymous>)`。
`/tmp/clonedemo`（两个 Python 近克隆，结构差异 ≥3 处）在 `-threshold 0.7` 起报 1 对
（0.9/0.8 不报，符合 Jaccard 阈值语义）。注意默认阈值 0.8 + `MinTokens:40` 刻意保守，
小幅改写不会报——这是设计。

## 单测

`internal/facts/lang_test.go`（11 个）：`TestExtractEveryTableLanguageHasDefinitions`、
`TestExtractPythonDefinitionsAreIndexed`、`TestExtractSkipsFunctionBodies`（关键不变式）、
`TestExtractCVisibilityAndDeclaratorNames`、`TestExtractTypeScriptExportsAndArrowFunctions`、
`TestExtractRustImplAnchorsReceiver`、`TestExtractCSharpMembersAndFields`、
`TestExtractCppNestedClassMethods`、`TestExtractEmptyFileHasNoSymbols`、
`TestExtractSignatureStopsBeforeBody`（`def f(a, b):` 的签名不含 body）、
`TestExtractMemberNamesUseOwnerReceiver`（同名成员靠 receiver 区分）。
helper `find` 同时搜成员，因为 C++ namespace 内的类与 TS interface 方法是 children。

`internal/clones/near_test.go`：`shingleOf` 改为直接用 `f.body`，删除内联的按名重定位 walker。

## 五性核对

| 性 | 结论 |
|---|---|
| 可介入 | ✅ 非 Go/Java 仓的 `impact` 从"几乎不可用"变为可用；同一处修复同时解锁 `symbols`/`find_symbols`/`get_impact`/`get_review_bundle` 等约 20 个调用点 |
| 可审计 | ✅ 每条 def 带 file:line/kind/receiver/visibility/signature；同名成员靠 receiver 区分而不是被覆盖 |
| 可度量 | ✅ Py 仓 defs 0→1（对照 Graphify 73 节点/37 文件的差距，至少不再是"零定义"）；`nearclones` 在 C 上从"死"变为能报 ~100% 克隆 |
| 可回溯 | ✅ 路径仍走 `selection.SanitizePath`（root-relative，不引入绝对路径）；`declKinds` 由 `clones.DeclKinds` 单点导出，避免某语言被一个 pass 覆盖而被另一个忽略 |
| 可观察 | ✅ `symbols`/`impact`/`context` 直接输出；`nearclones -threshold` 可调灵敏度 |

## 遗留

- `findBody` 已删，但 `internal/clones/near.go` 的 shingle 仍按**文件内**比较，
  跨文件近克隆（同名不同文件）依赖 `clonedrift` 的另一条路径。
- 可见性是启发式（C# 无修饰符默认 exported 是刻意选择的偏保守方向），
  在纯 internal 的 C# 项目上会多报 API 面。
- 表驱动抽取器只覆盖**顶层 + 一层成员**（`Members` 字段一层）。更深嵌套
  （C# 嵌套类里的嵌套类）不会被索引为独立符号。
- 本次只改了 `Extract` 的分派与 `declKinds`；`internal/facts/facts.go` 里 Go/Java 专用
  extractor（约 235 行）未动，也未与表驱动实现合并。
