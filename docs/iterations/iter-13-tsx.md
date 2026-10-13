# 迭代 13 · TSX 支持（`.tsx` 用独立语法，与 TypeScript 共用其上每一层）

日期：2026-10-13（用户 ref m12639『发布链路可以先不做，其他的做了吧』授权的其余项之一）

## 为什么做这个

`.tsx` 之前**没有任何语言身份**：`internal/parser/parser.go` 的 `LanguageForFile`
没有 `.tsx` 分支（`.jsx` 有），所以一个 React 组件文件在整条管线里是
「不存在的文件」——`check` 报 0 files，`symbols` / `impact` / `arch` / `deps`
全部看不到它。这不是「报错说我不支持」，而是**静默跳过**：一个 React 仓库的
`.tsx` 占代码量的一半，闸门对它给出的结论和「没有代码」无法区分。

新增它不追求「再造一层」，而是把已付清的成本接上：

| 已付的成本 | 被丢掉的部分 |
| --- | --- |
| tree-sitter-typescript@v0.23.2 同时带 `typescript` 与 `tsx` 两个语法（同一 module，无需新依赖） | tsx 语法从未被注册，`.tsx` 无语言身份 |
| 解析层之上的 10 层表（metrics kindset / IR 适配器 / 规则包 / refs / stubs / deps 查询 / clones / facts 符号表 / arch 抽取 / preset） | 只有 typescript 键，没有 tsx 键 |
| 迭代 9 的九语言拉齐把每层都做成按语言查表 | 新语言要被每张表都认识才算真的支持 |

关键的实测判断（临时探针 `internal/zztsx`，已删）：TSX 语法和 TS 语法在
`export_statement` / `lexical_declaration` / `variable_declarator` /
`arrow_function` / `formal_parameters` / `statement_block` 上**结构完全一致**，
JSX 只是以 `jsx_element` 出现在函数体里（`jsx_opening_element` 的 name 是
直接 `identifier`）。也就是说 JSX 对指标与符号层是惰性子树——所以正确做法是
「注册一个新语言，让它复用 TypeScript 的每一张表」，而不是写一套 tsx 适配器。

## 交付物

| 交付 | 位置 | 说明 |
| --- | --- | --- |
| 语言注册 | `internal/parser/parser.go` | 新增 `LangTSX LanguageID = "tsx"`；`grammars` 加 `tree_sitter_typescript.LanguageTSX`；`Supported()` 变 10 项；`extensions` 加 `.tsx`；`LanguageForFile` 加 `case ".tsx"`；新增导出 `TsTsx *sitter.Language` |
| 度量 profile | `internal/metrics/kindset.go` | 新增 `tsxFuncKinds`（绑定 `parser.TsTsx`）与 `tsxProfile`；`ProfileFor` 加分支 |
| 度量分支 | `internal/metrics/metrics.go` | countParams / countBoolParams / callSpellingFor / lowererFor 四处把 `LangTS` 与 `LangTSX` 并列（TSX 复用 `jsLowerer{ts:true}`） |
| CK 类度量 | `internal/metrics/classes_lang.go` | `parser.LangTS` 条目改为 `tsClassSpec()`，新增 `parser.LangTSX: tsClassSpec()` 并抽出构造函数，两语言共用一份 |
| 规则包 | `internal/rules/rules.go` | `ForLanguage` 改成 `case parser.LangTS, parser.LangTSX: return TSRules()` |
| banned-api | `internal/rules/banned_lang.go` | `langBanBuilders` 新增 `string(parser.LangTSX)` 独立 entry（同一组 builder） |
| stdlib 判定 | `internal/rules/imports.go` | `isStdlibTrimmed` 的 TS 分支并入 TSX |
| 依赖查询 | `internal/deps/scan_lang.go` | `importQueries` 新增 `parser.LangTSX`（与 TS 同查询） |
| refs | `internal/refs/refs.go` | `walkJS` 分派分支并入 TSX |
| stubs | `internal/stubs/stubs.go` | `detectJS` 分支并入 TSX |
| clones | `internal/clones/clones.go` | `declKinds` 新增 `LangTSX` |
| facts 符号表 | `internal/facts/lang.go` | `langSpecs` 新增 `LangTSX`（复用 `tsDecls()+tsExtra()` 与 TS 的 Containers/Vis） |
| arch | `internal/arch/extract.go` | `case parser.LangTS, parser.LangTSX: return extractTS(...)` |
| preset | `internal/config/preset.go` | RuleSeverity / RuleEnabled / BannedSeverity / MetricThresholds 四张表都补 `tsx` 键 |
| explain | `internal/config/explain.go` | `detectLanguage` 加 `.tsx` → `"tsx"` |
| rulegen | `internal/rulegen/rulegen.go` | `allowedKinds` 加 `"tsx"`，`langAliases` 加 `"tsx": "tsx"` |
| 测试 | `internal/metrics/classes_lang_test.go` | `TestHasClassesMatchesSpecs` 的 `want` 表补 `tsx: true` |

## 关键设计决策

**一个语言身份，不是「TS 的别名」。** `.tsx` 拿到自己的 `LanguageID = "tsx"`
（不叫 `typescript`），因此 `policy.yaml` 可以单独为 `tsx` 写阈值和规则，
`explain` 报告 `Language: tsx`，`banned-api` 的 `languages.tsx` 段也独立生效。
代价是每张表多一个键；换取的是配置精度和错误信息不撒谎。

**TSX 共享 TypeScript 的每一张表，但不合并两个 `LanguageID`。** 做法是在表里
让 `LangTS` 与 `LangTSX` 指向同一份数据：`tsClassSpec()` 是构造函数，两份
`*classSpec` 各自独立（`decls`/`members` 是 map，共享会有写冲突风险），但内容
由一个函数产生，不会漂移。度量侧的 `jsLowerer{ts:true}` 对两个语言都返回
`ts:true`，因为 JSX 不需要 lowering（它不含决策结构，`visit` 的 default 分支把
它当普通子节点展开）。

**`tsxFuncKinds` 绑定 `parser.TsTsx` 而不是复用 `tsFuncKinds`。**
`NodeKindSet` 带 `Language` 字段，`Has()` 只按 kind 字符串比较——复用也能工作，
但绑定各自的语法实例让「这个 profile 属于哪个语法」在类型上成立，也和
`NewKindSet` 的其它调用保持一致。

**编译期就抓到的错：map 的 key 不能是 `string(parser.LangTS), string(parser.LangTSX)`
并列形式。** `banned_lang.go` 第一版把两语言写成一个 key，报
`missing key in map literal`；改成两个独立 entry（都指向同一组 builder）即通过。
这是个小坑，但它是这个迭代唯一一次编译失败，值得留一行。

## 实测证据

fixture `/tmp/tsxdemo`（`src/card.tsx` 箭头函数 + JSX 属性与事件、`src/widget.tsx`
class + method、`src/util.ts` function、`panel.tsx` function 返回 JSX map、
`b/mod.tsx` 引用 `../src/util`）：

- 文件进入扫描：`check .` 报 **7 files**（此前 `.tsx` 是 0）。
- 指标产出：`panel.tsx` → `cyclomatic-complexity of "Panel" is 6`，在
  `cyclomatic {warn:2, error:4}` 下报 error。这证明三件事同时成立：TSX 文件被
  解析、`function_declaration` 被 `tsxFuncKinds` 认出、JSX 里的
  `items.map((it) => …)` 让 `call_expression` 计入 fan-out 且箭头函数按既有语义
  折进外层（而不是崩在 JSX 节点上）。
- 符号层：`symbols -q Panel .` → `panel.tsx:1 function function Panel({ items }: …)`；
  `symbols -q Widget .` → `src/widget.tsx:3 class class Widget`。
- CK：`card.tsx` 的 `interface CardProps` 产出 cbo/dit/lcom/nom/wmc 五个类级快照
  （值为 0，与 TS 侧同形状 fixture 一致）。
- 依赖：`deps scan` 起草 `tsx hallucinated-tsx-pkg # src/card.tsx:2`——deps 扫描
  现在会读 `.tsx`，且 `.jsx` 早就走 `LangJS`，两条路不重叠。
- arch：`arch snapshot` 在 `b/mod2.tsx` 写 `import … from "../src/util"` 时报
  **1 dependency direction**（`b → src`），即 TSX 文件参与依赖图。
- rulegen：`constraints rule -spec {lang:"tsx", kind:"call", match:"legacy.emit"}`
  → `pass: true`、`rule_id: banned-legacy-emit`，`policy_yaml` 落在
  `languages.tsx.banned-api` 段。
- explain：`explain src/card.tsx` → `Language: tsx`。

## 单测

| 测试 | 位置 | 断言 |
| --- | --- | --- |
| `TestHasClassesMatchesSpecs` | `internal/metrics/classes_lang_test.go` | `want` 表补 `"tsx": true`，遍历 `parser.Supported()` 时每种语言都必须有期望，防止再出现「新语言没进表」这种静默失败 |
| 既有全量回归 | `go test ./internal/... ./cmd/...` | 除预先存在的 `internal/digest TestBuildTaskFilteredBans` 外全绿 |

## 五性核对

| 维度 | 状态 | 证据 |
| --- | --- | --- |
| 可介入 | ✅ | `.tsx` 从「静默跳过」变成可被 `check` / `symbols` / `impact` / `arch` / `deps` 介入 |
| 可审计 | ✅ | policy 可独立配 `languages.tsx`；`explain` 报 `Language: tsx`；rulegen 接受 `lang: tsx` |
| 可度量 | ✅ | cyclomatic / cognitive / fan-out / CK 全部产出（`panel.tsx` cyc 6 实测） |
| 可回溯 | ✅ | findings 带 `src/panel.tsx` 这类 root-relative 路径与行号 |
| 可观察 | ✅ | `deps scan` 起草 tsx 行、`arch snapshot` 出 TSX 边 |

## 阈值纪律（本迭代被自检抓到 0 次）

本迭代没有新增「大函数」：改动全部是**表里加一个键**或**分支里并列一个
case**，最集中的 `internal/parser/parser.go` 也只是多一个常量、一个 map 项、
一个 case 和一个导出声明。自检 `check . -diff -base HEAD` 的新增 finding 数与
加 `.tsx` 之前一致（122 new，全部来自既有未提交改动），没有引入新的
complexity / file-length 条目。

## 验证

- `gofmt -l internal/ cmd/` 干净；`go build ./...` OK；`go vet ./internal/... ./cmd/...` 干净。
- `go test ./internal/metrics/ ./internal/parser/ ./internal/rules/ ./internal/refs/ ./internal/clones/ ./internal/facts/ ./internal/deps/ ./internal/arch/ ./internal/config/ ./internal/rulegen/ ./internal/stubs/ ./internal/engine/ -count=1` 全 ok。
- `go test ./internal/... ./cmd/... -count=1` 只 FAIL 预先存在的
  `internal/digest TestBuildTaskFilteredBans`（digest_test.go:389，HEAD worktree 同口径复现确认预存）。
- 仓自检 `check . -diff -base HEAD -config policy.yaml`：122 new / 173 hidden /
  10 unchanged dropped，2 errors 均为预先存在（`cmd/metricgate/explain_cmd.go:45`
  cmdExplainPreview cyc 18、`internal/digest/digest.go:377` banSection cyc 16）。
- 临时探针 `internal/zztsx` / `internal/zztsx2` / `internal/metrics/zzprof_test.go`
  均已删除，`ls internal/ | grep -c zz` = 0。

## 遗留

1. **TS/TSX 的箭头函数不计入函数级度量。** `tsFuncKinds` / `tsxFuncKinds` 不含
   `arrow_function`，所以 `const Card = ({title}) => {…}` 这类写法不产
   cyclomatic/cognitive 快照——`/tmp/tsxdemo/src/card.tsx` 因此 0 个函数。
   这是 **TypeScript 侧 12 个月以来的既有行为，不是本迭代引入**（用同一形状的
   `.ts` fixture 复现：1 个 funcs，只有 `function Panel` 被度量）。它的后果是
   TSX 组件（React 组件绝大多数是箭头函数）目前只被 `file-length` 和类级 CK
   覆盖。修它等于改变 TS 的函数定义语义，属于独立决策，已记入 backlog。
2. **arch 的 TSX 相对导入仍是既有近似。** `import … from "../util"`（相对路径）
   解析不出边，写成 `../src/util` 才有边——与 TS 完全相同，不是 TSX 特有。
3. **`rules gaps` / policygap 的语言清单** 走 `parser.Supported()`，会自动包含
   tsx，本迭代没有单独验证其输出（未改动）。

### 补充修复：`-diff` 闸门用了未过滤的 summary（iter-12 引入的回归）

见 [iter-12](iter-12-closed-loop-usability.md) 的补充修复节。要点：
`runCheckAddOns` 现在**返回**过滤后的 summary，调用点必须赋值
（`summary = runCheckAddOns(...)`），否则闸门会在自己刚刚宣布「hidden」
的 finding 上判红。本迭代的自检数据因此从 `2 errors / FAILED` 变成
`0 errors / PASSED`。
