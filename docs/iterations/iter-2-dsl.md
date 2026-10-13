# 迭代 2：自定义规则 DSL

日期：2026-10-11 · 对应 docs/roadmap.md 断点 C（试点 13 条未转化规范中 11 条卡在"AST 模式明确但内置没有"）

## 交付物

| 文件 | 内容 |
|---|---|
| `internal/config/config.go` | `LanguageConfig.CustomRules []DslRuleConfig`（yaml `custom_rules`）；`DslRuleConfig{ID,Title,Severity,Node,Field,MatchRegex,ExcludeRegex,Languages,Reason,Use}`；`Config.CustomRules(lang)` 访问器 |
| `internal/rules/dsl.go` | `DslRules(lang, langPtr, entries)`：每条规则编译为 tree-sitter query + 正则断言；规则 ID 前缀 `custom.` |
| `internal/rules/banned.go` | `RulesFor(id, cfg, langPtr)` 签名加 `langPtr *sitter.Language`（nil 跳过 DSL） |
| `internal/config/explain.go` | `ExplainRule` 识别 `custom.` 前缀，报告 custom_rule / policy_file |
| `policy.yaml` | 狗粮规则 `go-test-func-naming`（见下） |
| `internal/rules/dsl_test.go` | 6 测试：命中/不命中/import 禁令/Python 标识符/坏条目丢弃/编译+命中 |

## 语义与设计决策

- **match_regex 是"违规模式"，不是"合规模式"**。命名约束写 `exclude_regex` 断言合规（首个 /tmp/dsldemo 探针用反了方向，立即被 e2e 抓住修正）。
- **坏 DSL 条目丢弃而非 fatal**：一个 typo 不能炸整个 engine（`dslQuery` 用 `sitter.NewQuery` 试编译验证 node/field；error 时不 Close——binding 返回部分初始化值，Close 会 nil deref）。未知 severity 字符串同样丢弃，不静默降级为 warning。
- **DSL 匹配 = tree-sitter 节点 × 正则**：`(node field: (_) @cap) @m`，capture 文本先过 exclude_regex 再断言 match_regex。
- **ID 强制 slug 形式**（`^[a-z][a-z0-9-]*$`）：规则 ID 出现在 findings、policy review、accept 账本里，必须可 grep。

## 吃狗粮实录

1. **gate 抓住自己**：`dslRule` cyclomatic 20（阈值 15）→ 拆为 `dslQuery`/`dslRegexes`/`dslSeverity`/`dslOnMatch` 四个 helper 后清零。
2. **狗粮规则本仓零误报**：`check . -diff -base HEAD` → custom 规则 0 findings = 本仓所有测试函数合规；3 条 accepted fan-out warning 照常由迭代 1 账本消化。
3. **/tmp/dsldemo e2e**：非 -diff 下 `testlowercase` 命中 bad_test.go:5、`TestProper` 不命中；-diff 下 ratchet 语义正确（HEAD 已有违规 = pre-existing hidden，新引入的 `alsobad` 命中 bad_test.go:9 → gate FAILED）。
4. **explain 审计口径补全**：`rules explain custom.go-test-func-naming` 最初 rule_not_found → ExplainRule 增加 custom_rules 分支（期间一次 edit 留下孤立 `}` 被 gofmt/build 抓住，属可回溯修复）。

最终：`go build ./...` OK；全测试仅剩预先存在的 `internal/digest TestBuildTaskFilteredBans`（digest_test.go:389，git stash 已证与迭代无关）；self-gate PASSED。

## 狗粮规则（policy.yaml）

```yaml
languages:
  go:
    custom_rules:
      - id: go-test-func-naming
        node: function_declaration
        field: name
        exclude_regex: '^(Test|Benchmark|Fuzz|Example)[A-Z_]'
        severity: warning
        reason: "Go test entrypoints must be TestXxx/BenchmarkXxx/FuzzXxx/ExampleXxx (capitalized after the prefix)"
```

## 五性核对

| 性质 | 结论 | 证据 |
|---|---|---|
| 可介入 | ✅ | `rules explain custom.go-test-func-naming` 完整呈现生效配置（custom_rule / policy_file / warning）；坏条目丢弃有据可查 |
| 可审计 | ✅ | DSL 规则进 policy.yaml 进版本库，diff 即评审；ID slug 可 grep；explain 报 PolicySource=policy_file |
| 可度量 | ✅ | findings 走标准 RunSummary/JSON/SARIF 管道，trends/store 无特殊化 |
| 可回溯 | ✅ | `-diff` ratchet 语义自动生效（pre-existing hidden，新违规命中）；finding 定位到行:列 |
| 可观察 | ✅ | /tmp/dsldemo 命中+不命中+新违规三段式演示；文本/JSON 报告与内置规则同格式 |

## 断点遗留

- MCP `get_constraints` digest 未展示 custom_rules 清单（CLI `rules explain` 已覆盖；下次触及 digest 时补）。
- DSL 仅支持"节点 × 字段 × 正则"单断言，不支持跨节点组合（如"调用方在测试文件内"）；试点规范中 2 条需要跨节点模式，留待真实需求驱动。
