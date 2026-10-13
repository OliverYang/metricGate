# 迭代 9 · 九语言能力拉齐（语法已移植，语义层却停在 Go/Java）

日期：2026-10-12（承接迭代 6/7/8 之后的追问：为什么"支持新增语言"还有遗漏功能）

## 为什么做这个

迭代 7 把 facts 符号层补到 7 语言、迭代 8 补了继承边与 root 一致性。追问之后做了一次
**只读审计**：拿 9 语言逐个功能层跑实测，发现 9 语言的**语法移植**（tree-sitter 语法、
度量 adapter、规则包、依赖扫描、符号层、重复检测、桩检测）都完成了，但**消费这些
事实的语义层**停在 Go/Java。同一件事被实现了两遍：一遍给 Go/Java，一遍给"其余"——
而"其余"那一遍经常根本没写。

根因不是疏忽，是**架构缺少机制**：`parser.Supported()` 只被 `internal/engine/engine.go`
用来构建 runner（3 个调用点），不驱动任何其他层。其余每层各写自己的
`switch lang` / `map[LanguageID]`，全仓 13 个手写点，**没有任何测试断言"每个 Supported
语言在各层都有实现"**（`grep 'Supported()' --include='*_test.go'` 返回空）。

失败模式统一且危险：**沉默的零**。没实现的层不报错，只是什么都不产出——而"没违规"
和"没看"在报告里长得一模一样。

## 交付物

| 交付 | 位置 | 说明 |
|---|---|---|
| 注释种类按名字匹配 | `internal/engine/suppress.go` | `isCommentKind(kind)` = `strings.Contains(kind, "comment")`，替换 Go/Java 硬编码表 |
| TS 布尔参数计数器修复 | `internal/metrics/metrics.go` | `tsTypeName` 解包 `type_annotation`，不再只认 `type_identifier` |
| arch 多语言抽取 | `internal/arch/extract.go`、`internal/arch/extract_lang.go`（新建） | `Extract` 9 语言分派；新增 `extractTS/JS/Py/C/Cpp/CS/Rust` + `ImportStyle` |
| arch 包的包名规则 | `cmd/metricgate/arch_cmd.go` | Java 点→斜杠；C# 保持点号（与 `using` 同拼写）；其余用目录 |
| banned-api 9 语言 | `internal/rules/banned_lang.go`（新建） | `langBanBuilders()` 9 语言注册表 + `CanExpress(lang, kind)` |
| gatecheck 全语言 | `internal/gatecheck/gatecheck.go` | `candidateLangs(c)`：go/java 优先，其余排序后追加 |
| rulegen 全语言 | `internal/rulegen/rulegen.go` | `allowedKinds` 9 语言；`parseSpec` 抽出降 fan-out |
| digest 语言塑形 | `internal/digest/digest.go` | `precedentPath` 按语言解 import 前缀；`CanExpress` 挡住不可执行的广播 |
| context 约束节 | `internal/facts/render.go` | `presentLangs` 9 语言；`classMetricIDs` 由 `metrics.HasClasses` 决定 |
| CK 类级度量多语言 | `internal/metrics/classes.go`、`internal/metrics/classes_lang.go`（新建）、`internal/metrics/classes_lang_types.go`（新建） | 表驱动每语言类拼写；`HasClasses` 门；类型收集器单独一文件 |
| rulegen YAML 渲染拆分 | `internal/rulegen/yaml.go`（新建） | `banEntryYAML`/`entryYAML`/`policyYAML`/`yamlStr` 移出主文件，降 file-length |
| 文档订正 | `docs/capabilities.md` | 「支持语言：Go、Java」→ 九种；CK 边界从「只有 Java」改为按语言说明 |
| 清理 | `cmd/metricgate/usage.go`、`internal/ci/` | 删除重复的 `cockpit serve/update` 帮助行；删除空目录；帮助文本抽成 `const usageText` |

## 关键设计决策（含理由）

### 1. 注释种类不建表，按名字匹配

`suppress.go` 原来用 `map[parser.LanguageID][]string` 显式列出 Go/Java 的注释 kind。
**不补表，改成 `strings.Contains(kind, "comment")`。** 理由写在代码注释里：表必须为每个
新语言手工扩展，而**忘记的失败模式是不可见的**——指令永远读不到，被静音的规则继续报，
且没有任何输出说明为什么。全仓在用的语法都用 "comment" 拼注释 kind
（`comment` / `line_comment` / `block_comment` / `doc_comment`），而一个仅仅是名字相近的
kind 最多导致第二个相同的抑制区间，`suppress()` 会把它当作一个处理。这是**用机制
替代纪律**：新增语言不可能再漏掉这一层。

### 2. CK 家族：表驱动而非 8 份 extractor

`classes_lang.go`（500 行）用 `classSpec` 描述每语言的拼写：

```go
type classSpec struct {
    decls   map[string]string       // 声明 kind → Kind 标签（class/interface/struct/trait/enum/record）
    members map[string]memberReader // body 成员 kind → 字段/方法判定
    impls   bool                    // 方法在类型体之外（Rust impl 块）
    super   func(src, decl) string  // 父类型名；nil = 该语法无继承
    types   func(src, n, parent, refs) // CBO 收集器
}
```

Go **没有条目** → 不产出类指标，也不在它的规则目录里列出 CK 五项。`HasClasses(lang)`
是唯一的门，`IDsFor` 与 `render.go` 都问它，所以"宣传了某语言产不出的指标"不可能发生。

`types` 签名带 `parent` 是实测逼出来的：C# 把成员名（`P`/`Q`/`r`）当成了类型（CBO 5，
应为 2），而 TS/JS 的 `extends` 目标是裸 `identifier`（`typedRefs` 只认 `type_identifier`，
漏计）。要区分"类型位置"与"声明名位置"必须知道父节点——`fieldIn(parent, child)` 按
StartByte/EndByte/Kind 匹配后取 `FieldNameForChild`，据此判断 `type`/`returns`/`type_arguments`
等字段位置。

Rust 的 `impls` 单独处理：`impl Svc` 与 `impl Draw for Svc` 的方法都归 **Svc**
（类型拥有方法数，不是 trait）；trait 自己的默认方法留在 trait。

### 3. arch 的包名规则必须与 import 拼写一致

C# 是最容易错的一个：`namespace App.Core` 是**点号**形态，`using App.Core.Util` 也是点号。
原来的实现把 namespace 斜杠化成 `App/Core`，而 `using` 也斜杠化——单侧不一致就会让
**每一个 namespace import 无法解析**。现在 C# 两侧都保持点号；Java 仍然点→斜杠；
其余语言用目录（与 gate 的 `archFileOf` 一致）。

Python 的 `from a.x import Q` 也修了：模块名是点号形态，必须与 `import a.x` 一样走
`slash()` 变成 `a/x`，否则目录形态的包名永远匹配不上，**该边静默消失**。

### 4. 新增"每个语言在各层都有实现"的测试

`internal/metrics/classes_lang_test.go` 的 `TestHasClassesMatchesSpecs` 对
`parser.Supported()` 逐个断言 `HasClasses` 与 `IDsFor` 是否含 WMC **一致**。
`internal/arch/extract_test.go` 的 `TestExtractEveryLanguageFindsImports` 表驱动
7 语言断言 import 路径序列与 `Line >= 1`——注释写明理由："A language that extracts
nothing does not fail loudly — it produces a graph with no edges, which reads exactly
like a repository with no violations."

## 实测证据

### suppress（最严重：安全阀静默失效）

夹具 `/tmp/suppcheck`（policy 把 9 语言 `cyclomatic-complexity` 设为 warn 2 / error 3）：

| 文件 | 内容 | 修复前 | 修复后 |
|---|---|---|---|
| `a.ts` | 无指令 | 报 1 | 报 1 |
| `b.ts` | `// metricgate: ignore cyclomatic-complexity` | **报 1（失效）** | 0 |
| `c.ts` | `ignore-begin` / `ignore-end` | **报 1（失效）** | 0 |
| `d.py` | 无指令 | 报 1 | 报 1 |
| `e.py` | `# metricgate: ignore ...` | **报 1（失效）** | 0 |
| `f.rs` | `// metricgate: ignore ...` | **报 1** | 0 |
| `g.cs` | `// metricgate: ignore ...` 在**类前**（finding 在方法上，相隔数行） | **报 1** | 报 1（行作用域只覆盖下一非空行——设计如此，不是语言差异） |
| `h.cs` | `// metricgate: ignore ...` 在**方法前** | **报 1** | 0 |
| `i.cs` | `ignore-begin` / `ignore-end` 包住类 | **报 1** | 0 |

> **注意（容易误读）**：`g.cs` 修复后仍报 1，这**不是** C# 的遗漏。`metricgate: ignore`
> 是**行作用域**指令，只覆盖自身行与紧随的下一个非空行；把指令放在类前、finding 落在
> 类内方法上，本来就覆盖不到（Go 同样：`go_cmp.go` 指令在 `type A struct{}` 前 → 仍报；
> 指令在方法前 → 0）。要覆盖整块请用 `ignore-begin`/`ignore-end`（`i.cs` 验证）。
> 修复前的失效形态是另一种：指令直接贴在目标行上方也读不到（b/c/e/f/h）。

### arch（旗舰能力在 7/9 语言静默假绿）

同构的两个仓（循环依赖 + 分层）：

| 仓 | `arch snapshot` | `check` |
|---|---|---|
| `/tmp/archts`（TS） | 修复前 `2 packages, 0 dependency direction(s)` | 修复前 0 errors 2 warnings（无 cycle、无 layer-violation） |
| `/tmp/archgo`（Go） | `2 packages, 2 dependency direction(s)` | `import-cycle` + `layer-violation` |
| `/tmp/archcs`（C#） | 修复后 `2 packages, 2 dependency direction(s)` | `import-cycle (package cycle: App/Core, App/Util)` + `layer-violation` |

### CK 类级度量

夹具 `/tmp/ckcheck`（TS `wmc: warn 1 / error 2`、`cbo: warn 0 / error 1`）：

```
s.ts:1: [error] cbo (cbo of "Store" is 2 (threshold error 1))
s.ts:1: [error] wmc (wmc of "Store" is 3 (threshold error 2))
```

修复前 TS 完全没有 CK 指标（`metrics` 只输出文件级行）。Go 对照仍不产出 CK
（`check g.go` 0 errors 0 warnings，10 metric snapshots），因为 Go 没有类。

### banned-api

夹具 `/tmp/bancheck`（9 语言各一条禁令 + 一个违规文件）：

| 文件 | 修复前 | 修复后 |
|---|---|---|
| `a.ts` `fetch(` | **0 findings** | `banned-fetch (fetch is banned: no network)` |
| `a.js` `fetch(` | **0** | `banned-fetch` |
| `a.py` `requests.get` | 只有 unknown-dependency | `banned-requests-get` |
| `a.go` `fmt.Println` | 命中 | 命中 |
| `a.rs` `println!` | **0** | 宏调用按裸名匹配（`banned-dangerous_fn` 验证普通调用） |
| `A.cs` `Console.WriteLine` | **0** | `banned-Console-WriteLine` |
| `a.c` / `a.cpp` `printf` | 命中 | 命中 |

### boolean-params

夹具 `/tmp/boolcheck`（warn 2 / error 4）：

| 文件 | 修复前 | 修复后 |
|---|---|---|
| `b.ts` `a: boolean, b: boolean, c: boolean` | **0（计数器恒 0）** | `is 3 (threshold warning 2)` |
| `b.go` `a, b, c bool` | 命中 | 命中 |
| `b.rs` `a: bool, b: bool, c: bool` | 命中 | 命中 |
| `b.cs` `bool a, bool b, bool c` | 命中 | 命中 |
| `b.c` `bool a, ...` | 命中 | 命中 |
| `b.py` 无标注 | 0（设计取舍：Python 无静态类型） | 0 |

TS 的根因：判定写成 `ta.Kind() == "type_identifier" && text(src, ta) == "boolean"`，
而实测语法树是 `required_parameter → type_annotation → predefined_type → boolean`，
**没有 `type_identifier` 节点**，计数器永远返回 0。修复后 `tsTypeName` 解包
`type_annotation` 取内层类型节点。

### context 约束节

夹具 `/tmp/gapdemo`（TS 文件 + policy 设了 `boolean-params`）：修复前 `context src/big.ts`
只输出 repo map + 符号，**没有任何 `## 约束` 段**；修复后输出 14 条 limit/rule 行，
含 `limit cbo` / `limit dit` / `limit lcom` / `limit nom` / `limit wmc`（CK 家族，
因为 TS `HasClasses` 为真）。

## 单测

| 文件 | 覆盖 |
|---|---|
| `internal/metrics/classes_lang_test.go`（新建） | `TestClassMetricsPerLanguage`（7 语言表驱动，NOM/NOF/CBO/DIT）、`TestClassMetricsCSharpNestedFieldNames`、`TestClassMetricsGoHasNoClasses`、`TestHasClassesMatchesSpecs` |
| `internal/arch/extract_test.go`（+3） | `TestExtractEveryLanguageFindsImports`（7 语言 import 序列 + 行号）、`TestExtractCSharpNamespaceBecomesPackage`、`TestImportStylePerLanguage` |
| `internal/engine/catalog_test.go`（改） | `TestCatalogCoversEveryMeasuredMetric` 加 TS 夹具；`TestCatalogListsMetricRulesPerLanguage` 加 `tsMetas` 断言 |
| 夹具 | `internal/engine/testdata/ckts/Service.ts`（新建）；`TestRunOnTestdata` 文件数 19→20 |

`classCases()` 里的每个数值都是**先实测再写下来**的，注释说明理由：语法拼写变了会
表现为这个表的 diff，而不是一个沉默的零。

## 五性核对

| 性 | 结论 |
|---|---|
| 可介入 | ✅ 9 语言的 `metricgate: ignore` 从"完全无效"变为生效（安全阀）；arch 在 7 语言从假绿变为真红；CK 让 TS/JS/Py/C++/C/C#/Rust 的类设计约束第一次可执行；`banned-api` 从 2 语言扩到 9 语言 |
| 可审计 | ✅ `HasClasses` 是 CK 是否产出的唯一判定点，`CanExpress(lang, kind)` 是"这条禁令在该语言能否执行"的唯一判定点；`classSpec` 表把每语言的拼写集中在一处，读表即知覆盖范围 |
| 可度量 | ✅ 修复前后的 finding 数逐项实测（suppress 8 例、arch 3 仓、banned 8 文件、bool 6 文件）；`typeUses 36` 与 grep 真值对齐 |
| 可回溯 | ✅ `isCommentKind` 的注释写明"为什么用名字匹配而不是表"（表的失败模式不可见）；`classSpec.types` 的 parent 参数注释写明"语法靠它区分类型位置与声明名位置"；C# 点号/斜杠的注释写明"单侧斜杠化会让每个 namespace import 无法解析" |
| 可观察 | ✅ `context` 现在对 TS/Py 等语言也输出 `## 约束` 段（14 条 limit 行含 CK）；`arch snapshot` 的 `N dependency direction(s)` 从 0 变为真实数字，0 即"没看"的信号 |

## 阈值纪律（自检抓到即当场拆函数）

| 位置 | 触发 | 处理 |
|---|---|---|
| `internal/metrics/classes.go measure` | fan-out 12 | 抽 `score(n, c)`：`measure` 只组装 Name/Kind/行号 |
| `internal/metrics/classes_lang.go fieldIn` | fan-out 11 | 内联三条件抽 `sameNode(a, b)` |
| `internal/metrics/classes_lang_test.go` | function-length **133 error** | 表抽成 `type classCase` + `func classCases()` |
| `internal/metrics/metrics.go measureFunc` | fan-out 11 | 抽 `paramsNode(lang, fn)` + `fillBodyMetrics(st, lang, src, fn)` |
| `internal/metrics/metrics.go fanOut` | cog 17 / cyc 15 / npath 576 | 抽 `callSpelling` + `callSpellingFor` + `callsOf` + `isCallTarget` |
| `internal/metrics/metrics.go countBoolParamsClassic` | cog 25 / cyc 14 / fan-out 13 / npath 480 | 抽 `classicParamCount` + `classicBoolType` + `classicGroupSize` |
| `internal/metrics/metrics.go countBoolParams{Rust,CS}` | 互相 function-clone | 都委托 `countBoolParamsTyped(src, list, kind, want)` |
| `internal/arch/extract_lang.go jsRequires` | fan-out 11 | 抽 `requireCall(src, c) (path, line, ok)` |
| `internal/arch/extract_test.go TestExtractEveryLanguageFindsImports` | cog 17 | 抽 `assertImports` + `extractImports`（返回 `extracted{Imports, Paths}`） |
| `internal/engine/suppress.go buildSuppressions` | fan-out 17 | 改成 `suppressionBuilder` 结构体 + `walk`/`descend`/`onComment`/`directive`/`closeOpen` |
| `internal/rulegen/rulegen.go Verify` | fan-out 16 | 抽 `parseSpec(specBytes) (Spec, error)` + `noteExisting(res, base, s)` |
| `internal/rulegen/rulegen.go` | file-length 419（warn 400） | YAML 渲染抽到 `internal/rulegen/yaml.go`（`banEntryYAML`/`entryYAML`/`policyYAML`/`yamlStr`），主文件回到 353 行 |
| `internal/metrics/classes_lang.go` | file-length 501（warn 400） | 类型收集器（`typedRefs`/`tsTypes`/`heritageTypes`/`pyTypes`/`csTypes`/`csTypePosition`/`fieldIn`/`sameNode`/`isTypeName`/`nonClassNames`）抽到 `internal/metrics/classes_lang_types.go`，两文件均 0 findings |
| `internal/metrics/classes_lang_test.go classCases` | function-length 100（warn 50） | 拆成 `classCasesScript()` + `classCasesNative()`，夹具源码抽成包级 `ckTSSrc`/`ckJSSrc`/`ckPySrc`/`ckCppSrc`/`ckCSrc`/`ckCSSrc`/`ckRustSrc` 常量 |
| `cmd/metricgate/usage.go usage` | function-length 66（warn 50） | 帮助文本抽成包级 `const usageText`，`usage()` 变成一行 |

## 验证

- `gofmt -l internal/ cmd/` 干净；`go build ./...` OK。
- `go test ./...` 仅剩**既有**失败 `internal/digest TestBuildTaskFilteredBans`
  （`internal/digest/digest_test.go:389`，用户既有未提交改动，**不得归因**）。
- 相关包全绿：`internal/metrics`、`internal/engine`、`internal/arch`、`internal/facts`、
  `internal/config`、`internal/policygap`、`internal/rulegen`、`internal/git`、`cmd/metricgate`。
- 本仓自检 `check . -diff -base HEAD -config policy.yaml`：**1 error**，唯一 error 是
  `internal/digest/digest.go:377 banSection` cyclomatic 16（阈值 error 15）——
  已在 HEAD 纯净树（`git worktree add`）上复现同样为 error（HEAD 版第 376 行），
  确证是**用户既有脏改动**，非本次引入。
- 四路 root 等价保持：`check .` = `check <abs>`；`-diff .` = `-diff <abs>` = 116 new findings。
- 最终自检：`check . -diff -base HEAD -config policy.yaml` = `116 new / 182 pre-existing hidden`，
  `summary: 1 errors, 115 warnings`，唯一 error 仍是既有的 `internal/digest/digest.go:377`。
  本次新增的三个文件（`classes_lang.go` 拆分后两文件、`classes_lang_test.go`、`rulegen/yaml.go`）
  在单文件检查下均为 0 findings。
- `git diff --check` 干净；`node --check < internal/cockpit/web/app.js` 通过。

## 遗留

- **CK 家族对 C 只产出 NOF**（C 无方法、无继承，WMC/NOM/DIT 恒 0，CBO 只看 struct
  成员类型）。这是语言的真实形状，不是缺口，但报表里会出现大量 0。
- **Python 的 `boolean-params` 恒 0**：无类型标注时无法判断 bool，属设计取舍
  （与 Go 的"分组声明"同类边界）。
- **Rust 的 `impl` 方法归属按类型名做名字级匹配**：`impl Svc` 与 `impl Draw for Svc`
  都归 Svc；若同一文件有两个同名类型（不同模块），会合并。
- **`classDeclKinds` 作为 CBO 递归边界**：嵌套类型声明内的类型引用不计入外层 CBO，
  这是刻意的（嵌套类型是独立的设计单元），但意味着"外层类通过嵌套类间接耦合"不可见。
- **arch 的包名规则按语言分支**（Java 点→斜杠、C# 保持点号、其余用目录）：
  新增语言必须同时确认 `extract_lang.go` 的包名形态与 `arch_cmd.go` 的处理一致，
  否则边会静默消失（C# 与 Python 都踩过）。这仍是手写点，未做成机制。
- **`langBanBuilders` 是 9 语言注册表**，但每个新语言仍需手写 call/import/constructor
  三种 query；`CanExpress` 只能回答"有没有"，不能发现"该有但没有"。
