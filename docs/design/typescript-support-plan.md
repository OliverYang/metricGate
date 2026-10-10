# TypeScript 语法支持计划（P2）

> 目标：让 metricGate 能检查 TypeScript/TSX 代码，为 ace-ts 规约 v1 试点做准备  
> 状态：设计锁定（2026-10-10），待网络可用时实施

## 1. 依赖安装

```bash
go get github.com/tree-sitter/typescript@latest
go get github.com/tree-sitter/tsx@latest  # for .tsx files
```

**注意**：当前网络无法访问 GitHub，需等待网络恢复后执行。

## 2. parser.go 扩展

添加语言 ID 和 grammars 映射：

```go
const (
    LangGo   LanguageID = "go"
    LangJava LanguageID = "java"
    LangTs   LanguageID = "ts"   // TypeScript
    LangTsx  LanguageID = "tsx"  // TSX (React)
)

var grammars = map[LanguageID]func() unsafe.Pointer{
    LangGo:   tree_sitter_go.Language,
    LangJava: tree_sitter_java.Language,
    LangTs:   tree_sitter_typescript.TypeScript.Language,
    LangTsx:  tree_sitter_tsx.TSX.Language,
}

func Supported() []LanguageID {
    return []LanguageID{LangGo, LangJava, LangTs, LangTsx}
}
```

## 3. rules 扩展

复用 Go/Java 规则结构，新增 `typescript_rules.go`：

```go
// TypescriptRules is the built-in TS rule pack. Banned APIs live in policy.
func TypescriptRules() []*Rule {
    return []*Rule{
        // TODO: add TS-specific rules (e.g., ban any, ban unknown)
    }
}
```

**关键点**：
- banned-api 词汇表跨语言通用（policy.yaml 中定义）
- metrics（cyclomatic/cognitive/npath）语言无关，只需解析器支持
- TS 特有的规则：ban `any`, ban `unknown`, ban `console.log`（可选）

## 4. metrics 适配

metrics 包已支持语言无关计算，只需确保 parser 返回正确的 IR。

## 5. 测试策略

1. **fixture 烟测**：用 ace-ts 的真实文件作为 fixture
2. **端到端**：对 ace-ts 跑 `check -diff`，验证无 panic
3. **回归测试**：确保 Go/Java 不受影响

## 6. 时间表

| 阶段 | 时间 | 条件 |
|---|---|---|
| 依赖安装 | T+0 | 网络可用 |
| parser.go 扩展 | T+0.5 | go-get 成功 |
| rules 编写 | T+1 | TS 语法稳定 |
| 测试 + 文档 | T+1.5 | 端到端 green |

## 7. 与规约 v1 试点的关系

- **并行推进**：规约 v1 试点用 dev-cockpit（Go）先跑，TS 支持完成后用 ace-ts 跑第二轮
- **v2 修订**：规约 v1 的误报审计结论可能影响 TS 规则的阈值设定

## 8. 风险

- **风险**：tree-sitter-typescript 不稳定或有 breaking changes  
  **缓解**：锁定版本，不自动 upgrade
- **风险**：TS 语法树结构与 Go/Java 差异大，IR 转换需调整  
  **缓解**：参考现有 go_adapter/java_adapter 实现模式
