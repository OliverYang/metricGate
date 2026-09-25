# Goal Contract: P0-A `metricgate explain` 与 `check --preview`

## 目标
实现纯确定性、无 LLM 依赖的**文件/规则排除原因解释器**，让开发者明确知道：
- 为什么某个文件没被扫描
- 为什么某条规则没触发
- 当前 policy 的最终生效值来自哪一层（builtin/policy/override）

## 范围

### 包含
1. **Core API**: `Engine.ExplainPath(path string) *ExplainDecision`
   - 不调用规则执行器
   - 不写 DB/状态
   - 只走 collect/filter/config resolve
   - 返回 decision 结构体

2. **CLI 命令**:
   - `metricgate check --preview [path] [-format text|json]`
   - `metricgate rules explain <path>`

3. **输出格式**:
   - 每个候选路径：`status=included|excluded|unsupported`
   - `reason`: 排除原因的机器可读描述
   - `language`: 检测到的语言
   - `matched_pattern`: 匹配的正则模式
   - `effective_policy_source`: builtin/policy/override
   - `rule_ids`: 关联的规则 ID 列表

4. **Fixture 测试**:
   - `.go/.java/unsupported/excluded/testdata`
   - policy disabled 场景
   - allowed_files 场景

### 不包含
- 规则实际执行逻辑
- 数据库写入
- Git shell-out（除非显式 `-diff preview`）
- LLM 相关逻辑
- Web viewer

## 验收标准

### 功能验收
- [ ] `ExplainPath` 返回的 included 文件集合与 `Engine.Run` 完全一致
- [ ] `check --preview` 输出 JSON 可被稳定 snapshot
- [ ] `rules explain` 显示 banned-api/metric threshold/architecture/clone 的最终值及来源
- [ ] 预览不产生 findings、不写 DB、不触发 git shell-out

### 性能验收
- [ ] fixture < 100 files 时 `Duration < 100ms`
- [ ] 零 LLM 调用，纯确定性执行

### 代码质量验收（自吃狗粮）
- [ ] `go test ./...` 全部通过
- [ ] `go vet ./...` 无警告
- [ ] `metricgate check .` 对 metricgate 自身仓库无新告警
- [ ] `internal/config/explain.go` CC ≤ 15
- [ ] `cmd/metricgate/explain.go` 行数 ≤ 150

## 技术设计

### 数据结构
```go
// internal/config/explain.go
type ExplainDecision struct {
    Path             string            // 相对路径
    Status           string            // included|excluded|unsupported
    Reason           string            // 机器可读原因
    Language         string            // go/java/unknown
    MatchedPattern   string            // 匹配的正则（如适用）
    EffectivePolicy  map[string]string // rule_id -> source(builtin/policy/override)
    RuleIDs          []string          // 关联规则 ID
}

type ExclusionReason int

const (
    ReasonIncluded ExclusionReason = iota
    ReasonExtensionMismatch
    ReasonExcludedPattern
    ReasonUnsupportedLanguage
    ReasonParseError
    ReasonPolicyDisabled
    ReasonAllowedFilesMismatch
)
```

### 模块职责
- `internal/config/explain.go`: 配置解析 + 过滤逻辑
- `internal/engine/explain.go`: Engine 层 ExplainPath 实现
- `cmd/metricgate/explain.go`: CLI 命令实现
- `cmd/metricgate/rules_explain.go`: rules explain 子命令

### 依赖关系
```
cmd/metricgate/
  └── explain.go (CLI)
       └── internal/engine/explain.go
            └── internal/config/explain.go
                 └── internal/config/config.go (read-only)
```

**禁止**: explain 模块不得导入 parser/rules/engine.Run

## 实施计划

### Phase 1: Core API (预计 2 小时)
- [ ] 定义 `ExplainDecision` 结构体
- [ ] 实现 `internal/config/explain.go` 过滤逻辑
- [ ] 实现 `internal/engine/explain.go::ExplainPath`

### Phase 2: CLI Commands (预计 2 小时)
- [ ] 实现 `metricgate check --preview`
- [ ] 实现 `metricgate rules explain <path>`
- [ ] 支持 text/json 两种输出格式

### Phase 3: Test Fixtures (预计 1 小时)
- [ ] 创建测试 fixture 目录
- [ ] 编写单元测试覆盖所有分支
- [ ] 添加 golden file snapshot 测试

### Phase 4: Dogfood & Validation (预计 1 小时)
- [ ] 在 metricgate 自身仓库运行 preview
- [ ] 验证与 Engine.Run 的一致性
- [ ] 性能测试（< 100ms for < 100 files）

## 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 过滤逻辑复杂度高 | CC > 15 | 拆分多个小函数，每函数单一职责 |
| 与 Engine.Run 不一致 | 误报/漏报 | 先写测试再实现，golden file 对比 |
| 性能不达标 | 用户体验差 | 避免重复计算，缓存 config 解析结果 |

## 成功指标
- 开发者能明确知道"为什么我的文件没被扫描"
- 配置调试时间从小时级降低到分钟级
- 零 LLM 依赖，100% 确定性
