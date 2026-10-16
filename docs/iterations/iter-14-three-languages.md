# iter-14：三门语言（SQL / Verilog / VB.NET）以语法事实为准接入

**日期**：2026-10-16
**范围**：`internal/parser`、`internal/metrics`、`internal/facts`、`internal/clones`、
`internal/deps`、`internal/refs`、`internal/stubs`、`internal/rules`、`internal/arch`、
`internal/rulegen`、`go.mod`、`docs/capabilities.md`、`docs/metrics.md`。
范围外：`internal/cockpit/web/`（并发 goal）、引擎/CLI 架构、把 SQL 做成方言完备。

## 为什么是这三门

不是「再加几门看起来常用的」，而是先看真实语料里到底有什么、再看语法到底给不给力：

- 本地语料 2596 个 `.sql` 全是迁移/DDL 脚本，0 个 `.v`/`.sv`/`.svh`，VB.NET 0 个；
- 语法可得性实测：Verilog `tree-sitter-verilog@v1.0.3`、SQL `DerekStride/tree-sitter-sql`
  （gh-pages 伪版本，模块里有 parser.c）、VB.NET `CodeAnt-AI/tree-sitter-vb-dotnet`
  三者都能编译并解析样本；
- 三门各自的**真实能力边界**（无 CK、函数级单元形态、语法洞）在下面逐条记清，
  文档口径与实测一致，不做成「十三门一律拉齐」的假象。

判据是**语法事实 + 语料事实**，不是语言流行度。

## 三门语法的关键事实（决定每一处填法）

### SQL（DerekStride tree-sitter-sql，gh-pages 伪版本 v0.0.0-20260919000438-39fdb0064037）

- 函数级单元 = `create_function` / `create_procedure`。`function_declaration` 是
  PL/SQL 局部赋值，**不是函数**；`create_table`/`create_view`/`create_index`/
  `create_schema` 是 schema 声明，进 `facts` 不进函数度量。
- 名字在子节点 `object_reference` 的 `name` 字段，body 在子节点 `function_body`/
  `procedure_body` —— **都不是 `body` 字段**，`fillBodyMetrics` 与 `clones` 都要
  按子 kind 定位。
- 控制流只有 `WHILE` 与 `CASE`；routine body 里 `IF … THEN` **不在 grammar 里**，
  解析成 ERROR。
- 调用 = `invocation`，字段 `unit: object_reference`；普通 `fn(a)` 形态不填 `unit`
  字段，callee 要回落「第一个 named child」。
- 注释两个 kind：`--` → `comment`，`/* */` → `marginalia`（todo 规则 query 要同时匹配）。

### Verilog（tree-sitter-verilog v1.0.3，支持 SystemVerilog）

- 单元 = `function_declaration` / `task_declaration` / `class_constructor_declaration`。
  **`module` 是文件级容器，不是函数**（同 SQL 的 table）。`class`/`interface`/
  `package`/`program` 是 `facts` 的容器，成员是子符号。
- **所有节点 node-types 字段都是空**：名字从 `function_identifier → function_identifier
  → simple_identifier`（两层同名）取，body 从 `function_body_declaration` 下钻到
  `function_statement_or_null`；`class_method` 的构造器语句直接挂在声明上，
  body 解析要特例。
- 调用 = `tf_call`（callee 取第一个 named child）；`method_call` 能解析但
  `obj.method(1)` 多数落 ERROR → fan-out 对接收者链欠报，文档如实声明。
- if/case/loop 全靠匿名 token 与子节点形态：`if`/`else` 是匿名 token，case 的
  default 臂由 case_item 内匿名 `default` 判定。

### VB.NET（CodeAnt-AI tree-sitter-vb-dotnet，本地 shim）

- 单元 = `method_declaration` / `constructor_declaration`（后者无 name 字段，
  报为 `New`）。方法体是若干平级 `statement` 子节点，**无 `body` 字段**。
- **成员名大小写不敏感** → `vbDeclName` 归一为小写（`Me.Value`=`me.value`），
  否则仅大小写的改名会读成两个符号。
- **`AndAlso`/`OrElse` 不产生 operator token** → 逻辑运算符不评分（cyclomatic 欠报，
  `internal/metrics/vb_metrics_test.go: TestVBAndAlsoIsNotScored` 把这条钉成测试）。
- **两个上游语法洞**（实测最小复现，会以 `parse-error` 出现，不糊成部分数字）：
  ① 成员/文件级属性特性带点限定名（`<Foo()> Private m As Integer`、
  `<Assembly: A.B("t")>`）解析成 ERROR；② 文件末尾无换行解析成 ERROR。
- 上游模块两处 bug（go.mod 声明路径与仓库路径不一致、binding 调的 C 符号不存在）
  用仓库内嵌套模块 `third_party/tree-sitter-vb-dotnet` + `replace` + 本地 shim 解决，
  shim 头注释逐字记录两个 bug 的报错。

## 架构改动：消灭三处静默 fallback（task-2）

`ProfileFor` / `lowererFor` / `callSpellingFor` 三处原本有 `default: return goXxx // fallback`，
新增语言漏填会**静默拿到 Go 的表**。全部改成 map 查表、未注册返回 nil：

- `kindset.go` `langProfiles map[LanguageID]*LangProfile`，`ProfileFor` 返回 nil；
  `LangProfile.HasFunc(n)` nil 安全。
- `metrics.go` `langLowerers map[LanguageID]nodeLowerer`、`callSpellings map[...]callSpelling`，
  `fillBodyMetrics` 里 `if lowerer == nil { return }`、`if sp := callSpellingFor(lang); sp != nil`。
- `irScore`/`irNPath`/`fanOut` 签名改收 lowerer / callSpelling 而非语言。
- 反向验证三组：给任一 map 注入 `goLowerer{}` / Go profile / Go callSpelling fallback，
  `internal/metrics/langparity_test.go` 按预期报红。

### parity 的强制机制分两层，且每层都反证过

只把 map 改成「查不到返回 nil」还不够——`nil`（不产出）与「漏写」在测试眼里仍然一模一样。
所以强制机制做成两层，各自对应一类漏填：

1. **包内层** `internal/metrics/langparity_test.go`：遍历 `parser.Supported()`，
   按 **key 是否存在于 map**（`langProfiles`/`langLowerers`/`callSpellings`）断言，而不是按
   取到的值是否为 nil 断言。有意不产出必须显式登记 nil 条目。反证：从 `kindset.go` 删掉
   `parser.LangVB: {FuncKinds: vbFuncKinds}` 一行 → `langparity_test.go:32 vbnet: no entry in
   langProfiles` 报红（此前该测试首句是 `if prof == nil { continue }`，删整门照样全绿）。
2. **跨包层** `cmd/metricgate/registry_parity_test.go`：一张按语言 id 索引的期望表，
   一行一门语言、一格一个注册表的**实测数字**（`symbols`/`declKinds`/`cloneFns`/`refs`/`stubs`/
   `archPkg`/`archImprts`/`rulePack`/`ckFamily`/`banKinds`）。`Supported()` 里新增一门而表里没行 →
   `no row in perLanguage` 直接 `t.Fatalf`；表里有行而某注册表漏接 → 那一格按实测值报红。
   反向验证三组：① 删 `deps/scan_lang.go` 的 VB extractor → `deps: vbnet reported as lacking an
   import extractor`；② 把 VB 的 `refs` 格从 0 改成 1（模拟「注册表停止产出」）→
   `refs: 0 references, want 1`；③ 给 SQL 的 `banKinds` 加 `constructor`（模拟 `CanExpress` 与
   `rulegen.allowedKinds` 两边不一致）→ 两条断言同时报红，因为该格检查的是两个来源必须同意。
   另一侧 `TestPerLanguageTableHasNoOrphanRows` 防反向漂移：表里留着已从 `Supported()` 删掉的
   语言也报红。
   `deps` 那张 map 的 key 不可导出，所以它的观测面是 `ScanRoot` → `Draft.Files` +
   `LanguagesWithoutImports()`，即「每门都被计数，无 extractor 的语言被点名」。

## 每语言注册表（15 处，全部显式 case，区分「有意不产出」与「漏写」）

下表逐格取值由 `cmd/metricgate/registry_parity_test.go` 的 `perLanguage` 期望表钉住，
不再是手写用例的集合；新增语言必须在该表里建行，否则 `t.Fatalf`。

| 层 | SQL | Verilog | VB.NET |
|---|---|---|---|
| `parser.go` | LangSQL/.sql | LangVerilog/.v,.sv,.svh,.vh | LangVB(vbnet)/.vb |
| `kindset.go` | create_function,create_procedure | function_declaration,task_declaration,class_constructor_declaration | method_declaration,constructor_declaration |
| `metrics.go`(callSpelling) | invocation/unit | tf_call/首 named child | invocation/target + new_expression |
| `metrics.go`(lowerer) | sqlLowerer | verilogLowerer | vbLowerer |
| `metrics.go`(countParams/declBody/measureName) | function_arguments/function_body/object_reference | tf_port_list/*_body_declaration/function_identifier | parameter_list/statement 子节点/name（ctor→New） |
| `facts/lang.go` | sqlDecls + statement 容器 | verilogDecls + Recurse 容器 | vbDecls + namespace_block/type_declaration 容器 |
| `clones.go` | declKinds+declBodies | declKinds+declBodies | declKinds+declBodies(平级 statement 全 hash) |
| `deps/scan_lang.go` | 无（LanguagesWithoutImports 可见） | `include_compiler_directive` | `imports_statement` |
| `refs.go` | 显式 return nil | 显式 return nil | 显式 return nil |
| `stubs.go` | 显式 return nil | detectVerilog（空函数/$fatal） | detectVB（空方法/Throw） |
| `rules.go` ForLanguage | SQLRules | VerilogRules | VBRules |
| `rules/banned_lang.go` | BanCall,BanImport | BanCall,BanImport | BanCall,BanImport,BanConstructor |
| `arch/extract.go` | 显式 Raw{} | 显式 Raw{} | 显式 Raw{} |
| `rulegen allowedKinds` | call,import | call,import | call,import,constructor |
| `classes_lang`（CK 门） | false | false | false |

`deps.Draft.Files` 计数**移到 import 查表之前**，否则「无 import extractor 的语言」
与「无 import 的语言」无法区分；`LanguagesWithoutImports()` 把 SQL 报出来。

规则层基础设施两处改动：`Rule.patterns()` 返回 `[]*Rule`（alternate 带自己的
OnMatch），修掉「alternate 用父规则的 callback 跑、命中恒 0」这个 bug；
`emptyBlockRule` 支持自定义判据（VB 的 `If` 恒有 condition named 子节点、
SQL 的 `WHILE` 把条件与关键字留在同节点，默认的 `NamedChildCount()==0` 是错的）。

## Rust 既有集成核对（task-6）

12 处注册表全部在位，无缺口，且每格数字都进了上面那张跨包期望表（rust 行：symbols=1 /
declKinds=1 / cloneFns=1 / refs=1 / stubs=1 / rulePack=5 / ckFamily=true / ban=call,import）：
kindset(68,111) / metrics(461,549,769,905) /
classes_lang.go:115 CK 表（struct_item/trait_item/enum_item + impls:true）/ facts:252 /
clones:90 / deps:162 / refs:61 / stubs:60 / rules:340 / banned_lang:282 / arch:50,79 /
rulegen:113。实测 `/tmp/rustcheck.rs`：3 函数 + 3 类（Point struct WMC=1 NOM=1 NOF=2、
Shape trait WMC=2 NOM=2 CBO=1、Color enum）。反向验证：删 `banned_lang.go:282`
→ `TestPolicyBanCoversEveryLanguage/rust_qualified_call` +
`TestPolicyImportBanCoversEveryLanguage/rust_use` 报红。

## 真实公开仓库实测（task-7，可复算）

github.com 直连不通，经 ghproxy.net 取 `archive/refs/heads/master.tar.gz`，
扫描日期 2026-10-16，二进制 `go build -o metricgate ./cmd/metricgate` 后
`./metricgate check <repo> -format json`。master 是移动目标且 GitHub API
被 WAF 拦住（无法记 commit），故每个数字都按「fetch 方式 + 二进制 + 上面的命令」
描述，重跑会因 master 前移而略有出入；本文的数字是 2026-10-16 那一版的复算结果。
**去掉 parse-error 的规则命中数**（`parse-error` 本身不是规则判断，是语法报告）。

| 语言 | 仓库 | 该语言文件数 | 指标行 | 函数 | 规则命中（除 parse-error） | parse-error |
|---|---|---|---|---|---|---|
| Verilog | verilator/verilator master | 3743 个 .v/.sv/.svh/.vh，其中 3726 个 .v/.sv/.svh 被扫出指标（`test_regress` + `include`；tarball 里 `verilator-master/` 是该树的一份副本，已剔除，否则 10920 个函数是双计） | 48320 | 5460 | 1396（empty-block 349 + function-clone 230 + function-length 175 + cyclomatic 155 + cognitive 100 + params-count 100 + npath 91 + call-chain-depth 87 + file-length 60 + nesting-depth 26 + fan-out 23） | 2303 |
| VB.NET | icsharpcode/SharpDevelop master | 82 (.vb，全部被扫出指标) | 681 | 74 | 0 | 73 |
| SQL | cockroachdb/cockroach master | 90 (.sql；仅 10 个非空且语法覆盖内的文件产出指标) | 10 | 0 | 0（file-length 1） | 6 |

- **Verilog 是三门里唯一有大规模函数级度量与规则命中的**，证明函数/任务/构造器
  口径、fan-out、clone、empty-block 在真实代码上都 work。`boolean-params` 全 5460
  行为 0 不是 bug：Verilog 用 `logic`/`bit`/`reg`，没有 boolean 类型。
  2303 个 parse-error 占 62% 的文件，主因是 `test_regress` 故意放大量语法边界样本；
  干净文件仍有 181 个贡献 5460 个函数。
- **SQL 的 6 个 parse-error 与 0 函数**：90 个 .sql 里只有 4 个含 CREATE FUNCTION/
  PROCEDURE，且全是方言形态。逐一判定后 0 函数的原因是方言而不是语法洞：
  `create_function_sql.sql` 用 `RETURNS SETOF INT` 与 `DO $a$ … END $a$`，
  `create_function_plpgsql.sql` 用 `FOR … LOOP` / `IF … THEN`（本语法只有
  `WHILE` 与 `CASE`）。作为对照，把同一函数改写为语法覆盖内的形态
  （`RETURNS int AS $$ SELECT CASE WHEN a > b THEN a ELSE b END; $$ LANGUAGE sql`）
  得到 **1 个函数、10 指标行、cyclomatic 2、params 2、0 parse-error**
  ——即 SQL 的函数级度量确实工作，CockroachDB 方言落在覆盖外，如实记
  「语法覆盖 PostgreSQL 子集」而不是伪造数字。另有一个与上述无关的语法洞：
  `SELECT … LIMIT <identifier>`（`LIMIT lim`）解析失败，`LIMIT <literal>` 正常。
- **VB.NET 的 73 个 parse-error**：不再声称「全部来自上面两个语法洞」——那是错的。
  用最小文件逐一复现，语法洞实为五类（都可最小复现）：① 泛型实参
  `List(Of Boolean)`；② 任何 `Implements` 子句（类级或成员级、带点不带点都错，
  没有 Implements 的同类文件正常）；③ 成员上的 attribute（`<Obsolete> Sub F()`，
  文件级 `<Assembly: …>` 正常）；④ class 体内的 `#Region`（方法体内的正常）；
  ⑤ 文件末尾无换行。82 个文件里 34 个产出函数，其中**只有 1 个（Solution.vb）
  来自完全干净的文件**，其余 73 个来自部分解析的文件。这组数字按上面的 fetch +
  二进制 + 命令可复算。

## 收口

- `go build ./...` 干净；`go test ./internal/{metrics,rules,facts,clones,deps,stubs,
  refs,arch,rulegen,parser}/` 全过（含新增 `sql/verilog/vb_metrics_test.go`、
  `langpacks_test.go`、三门 clones/stubs/deps 测试）。
- 上一轮收口被独立审计员以两条否决，本条已逐条复现并修掉：
  **G1** VB.NET `boolean-params` 静默落 Go 形默认分支（恒 0，而 VB 有
  `As Boolean` 参数）。已加 `countBoolParamsVB` + `TestVBBoolParams` +
  `TestBoolParamsCoversEveryLanguage`（遍历 `Supported()` 的语言必须各自有样本，
  漏 arm 即报红），`docs/metrics.md` 的 VB 小节补上该指标口径。
  **G2** 本节把两处不可复算的声明换成了可复算数字：旧文写「39 个 parse-error
  全部来自上面两个语法洞」「干净子集给出 72 个函数」——按最小文件复现，语法洞
  实为五类，74 个函数里只有 1 个来自完全干净的文件。VB 语法洞清单现已由
  `TestVBDocumentedGrammarGaps` 断言（洞被上游修好会让测试报红）。
- `docs/capabilities.md` 语言行改 13 门并加「能力按语法如实声明」口径；
  `docs/metrics.md` 加 SQL/Verilog/VB.NET 的能力缺口小节；
  `docs/backlog-v1x.md` 的「9 种语言」过期口径订正为 13。
- 第二轮审计又以两条否决，本条也已逐条复现并修掉（这一轮打的是**强制机制的覆盖面**，
  不是当前接线的正确性——审计员确认 13 门在每处都是显式 case）：
  **P1** 成功标准第 2 条要的「遍历 `Supported()` 断言每门在注册表显式实现」只存在于
  parser/metrics 两包，其余十处注册表（facts/clones/deps/refs/stubs/arch/rules/
  banned_lang/rulegen）没有逐语言遍历，第 14 门语言漏填不会变红；且
  `TestLanguageParityProfile` 首句 `if prof == nil { continue }` 让「漏填」与「有意不填」
  在同一张表里不可区分——审计员从 `kindset.go` 删掉整行 VB 条目，`-run TestLanguageParity`
  仍报 ok，我复现了同样结果。修法见上一节「parity 的强制机制分两层」。
  **P2** 本节旧文把 Verilog 规则命中总数写 1487，而其自身逐条分布相加为 1396
  （npath 91 被重复计入）。已改为 1396 并把明细换成加号，使总数与明细可互相核对。
- 观测三档：`fast` 实测绿；`check_path('internal/cockpit/web', diff_only:true)` errors=0。
