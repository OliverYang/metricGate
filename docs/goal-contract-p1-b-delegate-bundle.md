# Goal Contract: P1-B Delegate Bundle / `metricgate bundle`

## 目标
实现**委托评审任务包**，将 metricgate 从"可单独运行的 checker"转成可被任意 coding agent 调用的可信约束输入层。输出结构化的 JSON bundle，包含：
- Git diff + 变更文件列表
- 硬约束（policy/rules）
- 符号索引 + 事实
- 度量 findings
- Token 预算控制

## 范围

### 包含
1. **Bundle Schema**: 结构化 JSON 输出
   ```json
   {
     "schema_version": 1,
     "run": {"mode":"diff","base":"...","head":"..."},
     "budget": {"max_tokens":6000,"estimated_tokens":...},
     "constraints": [...],
     "files": [{
       "path":"...",
       "status":"included",
       "reason":"changed",
       "diff":"...",
       "facts":[...],
       "symbols":[...],
       "findings":[...]
     }],
     "omitted": [...],
     "instructions": {
       "report_only_changed_code":true,
       "finding_schema":"metricgate.finding.v1"
     }
   }
   ```

2. **CLI 命令**: `metricgate bundle [path] [-options]`
   - `-diff`: Git diff 模式
   - `-base`: 基础 commit（默认 merge-base）
   - `-out`: 输出文件（默认 stdout）
   - `-token-budget`: 最大 token 数

3. **MCP 工具**: `get_review_bundle`
   - 复用 CLI 的 service 层
   - 支持 `token_budget` 参数

4. **Selection Plan**: preview 与 bundle 共用 selection 逻辑
   - 防止"预览说扫描 A、bundle 却送 B"

5. **Token-aware Packing**: 集成 P1-A 的预算控制
   - 硬约束永不丢失
   - 按优先级省略低价值内容

### 不包含
- LLM 调用逻辑
- Session 管理
- Web viewer
- 第三方分析器 ingest（P2-B）

## 验收标准

### 功能验收
- [ ] Bundle JSON 可 round-trip（序列化/反序列化稳定）
- [ ] schema_version 显式声明
- [ ] 同一 Git 状态重复构建内容稳定
- [ ] changed file selection 与 `check -diff` 集合一致
- [ ] paths repo-relative、slash-normalized
- [ ] token budget 超限按 P1-A 策略省略，硬约束不丢失

### 性能验收
- [ ] diff 模式下 bundle 生成时间 < 2s（fixture < 100 files）
- [ ] token 对比：vs 整仓 dump 降低 ≥ 50%

### 代码质量验收（自吃狗粮）
- [ ] `go test ./...` 全部通过
- [ ] `go vet ./...` 无警告
- [ ] `metricgate check .` 对 metricgate 自身仓库无新告警
- [ ] `internal/bundle/bundle.go` CC ≤ 15
- [ ] `cmd/metricgate/bundle.go` 行数 ≤ 200

## 技术设计

### 数据结构
```go
// internal/bundle/bundle.go
type Bundle struct {
    SchemaVersion int               `json:"schema_version"`
    Run           RunConfig         `json:"run"`
    Budget        BudgetConfig      `json:"budget"`
    Constraints   []Constraint      `json:"constraints"`
    Files         []FileBundle      `json:"files"`
    Omitted       []OmittedSection  `json:"omitted,omitempty"`
    Instructions  Instructions      `json:"instructions"`
}

type RunConfig struct {
    Mode   string `json:"mode"` // diff/full
    Base   string `json:"base,omitempty"`
    Head   string `json:"head,omitempty"`
}

type BudgetConfig struct {
    MaxTokens       int `json:"max_tokens"`
    EstimatedTokens int `json:"estimated_tokens"`
}

type FileBundle struct {
    Path    string     `json:"path"`
    Status  string     `json:"status"` // included/excluded
    Reason  string     `json:"reason"`
    Diff    string     `json:"diff,omitempty"`
    Facts   []Fact     `json:"facts,omitempty"`
    Symbols []Symbol   `json:"symbols,omitempty"`
    Findings []Finding `json:"findings,omitempty"`
}

type Instructions struct {
    ReportOnlyChangedCode bool   `json:"report_only_changed_code"`
    FindingSchema         string `json:"finding_schema"`
}
```

### 模块职责
- `internal/bundle/bundle.go`: Bundle 结构体 + 序列化
- `internal/bundle/pack.go`: 打包逻辑（集成 P1-A）
- `cmd/metricgate/bundle.go`: CLI 命令实现
- `cmd/metricgate-mcp/tools.go`: MCP 工具 `get_review_bundle`

### 依赖关系
```
cmd/metricgate-mcp/
  └── tools.go
       └── get_review_bundle()
            └── internal/bundle/service.go
                 ├── internal/bundle/bundle.go
                 ├── internal/bundle/pack.go (P1-A)
                 ├── internal/git/changes.go (read-only)
                 └── internal/facts/ (read-only)

cmd/metricgate/
  └── bundle.go
       └── internal/bundle/service.go
```

**禁止**: bundle 模块不得导入 parser/rules/engine.Run

## 实施计划

### Phase 1: Bundle Schema (预计 2 小时)
- [ ] 定义 Bundle/FileBundle/RunConfig 结构体
- [ ] 实现 JSON 序列化/反序列化
- [ ] 添加 schema_version 验证

### Phase 2: Service Layer (预计 3 小时)
- [ ] 实现 `internal/bundle/service.go::BuildBundle`
- [ ] 集成 git diff 读取（复用 internal/git）
- [ ] 集成 facts/symbols/findings 收集
- [ ] 集成 P1-A 的 token-aware packing

### Phase 3: CLI Command (预计 2 小时)
- [ ] 实现 `metricgate bundle` 命令
- [ ] 支持 `-diff`, `-base`, `-out`, `-token-budget` 选项
- [ ] 支持 text/json 输出格式

### Phase 4: MCP Integration (预计 1 小时)
- [ ] 实现 `get_review_bundle` MCP 工具
- [ ] 复用 CLI 的 service 层
- [ ] 添加 `token_budget` 参数

### Phase 5: Test & Dogfood (预计 2 小时)
- [ ] 编写单元测试覆盖所有分支
- [ ] golden file snapshot 测试
- [ ] 在 metricgate 自身仓库验证 bundle 质量
- [ ] 性能测试（< 2s for < 100 files）

## 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| Bundle 体积过大 | token 超限 | 集成 P1-A 的优先级装箱器 |
| 与 check -diff 不一致 | 误导 agent | 共用 selection plan，golden file 对比 |
| JSON schema 不稳定 | consumer break | 显式 schema_version，向后兼容策略 |
| 性能不达标 | 用户体验差 | 避免重复计算，增量构建 |

## 成功指标
- 宿主 agent 能直接消费 bundle 进行代码评审
- token 节省 ≥ 50%（vs 整仓 dump）
- hard constraints 召回率 100%
- bundle 生成时间 < 2s（小型项目）
