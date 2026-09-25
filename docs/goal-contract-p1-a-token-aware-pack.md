# Goal Contract: P1-A Token-aware Context Pack v2

## 目标
实现**预算感知的上下文打包器**，替代当前粗糙的字符数 `/4` 估算 + tail-cut 策略，确保：
- 硬约束（policy/rules）永不丢失
- 预算不足时按优先级省略低价值详情
- 绝不截断 UTF-8、JSON 或结构化对象

## 范围

### 包含
1. **原子项模型**: `PackItem{section, priority, text, required, source}`
   - section: hard_constraints / edited_file_signature / direct_deps / near_symbols / clone_reuse / metrics_context_notes
   - priority: 1-5 (1 最高)
   - required: bool (硬约束必须为 true)

2. **预算接口**: `RenderOptions{MaxTokens, TokenCounter}`
   - Tokenizer 接口在 adapter 层
   - core 内默认 deterministic estimate（显式标记 approximate）

3. **优先级装箱器**: 
   - 按优先级排序，不拆 item
   - 硬约束 > 当前文件公开签名/依赖 > 相似符号/clone > 深层统计
   - 低优先级被省略时输出 omission manifest

4. **Pack 结构**: `Pack{Text, EstimatedTokens, ExactTokens?, Included[], Omitted[]}`
   - 保留旧 `Render(...,maxChars)` 包装器兼容 CLI/MCP

5. **MCP 增强**: `get_constraints` 增加 `token_budget` 参数与实际估算

### 不包含
- 真实 tokenizer 实现（adapter 层可选）
- LLM 调用逻辑
- Session 管理
- Web viewer

## 验收标准

### 功能验收
- [ ] 1k/2k/6k budget 均严格不超 token estimate
- [ ] hard constraints 在 budget 过小无法容纳时返回 `budget_exhausted`
- [ ] 同输入同预算输出 bytes 稳定
- [ ] hard constraints 召回率 100%
- [ ] 与 v1 对比：fixture 下 token 总数降低 ≥ 30%

### 代码质量验收（自吃狗粮）
- [ ] `go test ./...` 全部通过
- [ ] `go vet ./...` 无警告
- [ ] `metricgate check .` 对 metricgate 自身仓库无新告警
- [ ] `internal/facts/budget.go` CC ≤ 12
- [ ] `internal/facts/token_counter.go` 行数 ≤ 100

## 技术设计

### 数据结构
```go
// internal/facts/budget.go
type PackSection string

const (
    SectionHardConstraints  PackSection = "hard_constraints"
    SectionEditedFileSig    PackSection = "edited_file_signature"
    SectionDirectDeps       PackSection = "direct_deps"
    SectionNearSymbols      PackSection = "near_symbols"
    SectionCloneReuse       PackSection = "clone_reuse"
    SectionMetricsContext   PackSection = "metrics_context"
)

type Priority int

const (
    PrioCritical Priority = 1
    PrioHigh     Priority = 2
    PrioMedium   Priority = 3
    PrioLow      Priority = 4
    PrioExtra    Priority = 5
)

type PackItem struct {
    Section   PackSection
    Priority  Priority
    Text      string
    Required  bool
    Source    string // policy/config/generated
}

type RenderOptions struct {
    MaxTokens    int
    TokenCounter TokenCounter
}

type TokenCounter interface {
    Count(s string) int
}

type Pack struct {
    Text            string
    EstimatedTokens int
    Included        []PackItem
    Omitted         []OmittedItem
    BudgetExhausted bool
}

type OmittedItem struct {
    Section  PackSection
    Count    int
    Reason   string
}
```

### 模块职责
- `internal/facts/budget.go`: PackItem + 装箱算法
- `internal/facts/token_counter.go`: TokenCounter 实现
- `internal/facts/render.go`: 适配旧 Render API
- `cmd/metricgate/context_cmd.go`: CLI 集成
- `cmd/metricgate-mcp/tools.go`: MCP 工具集成

### 依赖关系
```
cmd/metricgate-mcp/
  └── tools.go
       └── internal/facts/budget.go
            └── internal/facts/render.go
                 └── internal/facts/facts.go (read-only)
```

**禁止**: budget 模块不得导入 parser/rules/engine.Run

## 实施计划

### Phase 1: Core Model (预计 2 小时)
- [ ] 定义 PackItem/Pack/OmittedItem 结构体
- [ ] 实现优先级排序逻辑
- [ ] 实现基础装箱算法（不拆 item）

### Phase 2: Token Counter (预计 1 小时)
- [ ] 定义 TokenCounter 接口
- [ ] 实现 deterministic estimate（字符/4）
- [ ] 可选：添加简单 tokenizer adapter

### Phase 3: Render Integration (预计 2 小时)
- [ ] 改造 render.PackItemsBudgeted
- [ ] 保留旧 Render API 兼容层
- [ ] 生成 omission manifest

### Phase 4: Test & Dogfood (预计 2 小时)
- [ ] 编写单元测试覆盖所有优先级分支
- [ ] golden file snapshot 测试
- [ ] 在 metricgate 自身仓库验证 token 降低效果

## 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| Token 估算不准 | 超限/浪费 | 显式标记 approximate，提供 lower/upper bound |
| 装箱算法复杂 | CC > 12 | 拆分 splitRequired/admitOptional/sortPackItems |
| 向后兼容问题 | CLI/MCP break | 保留旧 Render API 包装器 |

## 成功指标
- 硬约束召回率 100%
- token 节省 ≥ 30%（vs 整仓 dump）
- 零截断错误（UTF-8/JSON 完整性）
