# Constraint Inventory - 约束清单

> **目的**: 记录团队的业务约束、研发规范和技术要求，为转化为 metricGate 规则提供依据。
> 
> **维护方式**: 定期更新，按优先级排序 (P0-P2)
> 
> **最后更新**: 2024-09-28

---

## 📊 约束分类

| 类别 | ID 前缀 | 说明 | 示例 |
|-----|--------|------|------|
| API 黑白名单 | A-XXX | 控制允许/禁止使用的 API | banned-md5, no-fmt-print |
| 架构分层 | B-XXX | 确保代码结构符合架构设计 | no-db-in-service, core-no-cmd |
| 安全合规 | C-XXX | 满足安全审计和合规要求 | no-md5-encryption, data-masking |
| 代码规范 | D-XXX | 提升代码质量和可维护性 | require-error-handling, doc-required |
| 性能约束 | E-XXX | 预防性能问题 | no-nested-loops, no-n-plus-one |

---

## 📋 约束列表

### P0 - 高优先级 (必须实现)

| ID | 约束描述 | 来源 | 严重度 | 适用语言 | 业务理由 | 实现状态 |
|----|---------|------|--------|---------|---------|---------|
| A-001 | 禁止使用 fmt.Println 系列函数 | policy.yaml | error | Go | 统一日志方案，便于追踪 | ✅ 已实现 |
| A-002 | 禁止使用 MD5 加密算法 | SECURITY.md | error | Go,Java | 安全合规要求，MD5 已被证明不安全 | ⏳ 待实现 |
| B-001 | Service 层不能直接调用数据库查询 | CR 记录 | warning | Go | 保证依赖注入模式，提升可测试性 | ⏳ 待实现 |
| B-002 | internal 包不能被 cmd 引用 | policy.yaml | warning | Go | 保持架构分层清晰 | ✅ 已有 |

### P1 - 中优先级 (建议实现)

| ID | 约束描述 | 来源 | 严重度 | 适用语言 | 业务理由 | 实现状态 |
|----|---------|------|--------|---------|---------|---------|
| C-001 | 用户敏感数据必须脱敏处理 | SECURITY.md | error | Go,Java | GDPR/隐私保护要求 | ⏳ 待实现 |
| D-001 | 所有返回错误的函数调用必须处理 | code review | warning | Go | 提升代码健壮性 | ⏳ 待实现 |
| D-002 | public 函数必须有注释 | README | info | Go,Java | 提升代码可读性 | ⏳ 待实现 |
| E-001 | 避免嵌套循环 | code review | info | Go,Java | 预防性能问题 | ⏳ 待实现 |

### P2 - 低优先级 (可选实现)

| ID | 约束描述 | 来源 | 严重度 | 适用语言 | 业务理由 | 实现状态 |
|----|---------|------|--------|---------|---------|---------|
| A-003 | 禁止使用 panic (除 main 外) | code review | warning | Go | 避免程序意外崩溃 | ⏳ 待实现 |
| B-003 | adapter 层只能依赖 core | architecture.md | warning | Go | 稳定依赖原则 | ⏳ 待实现 |
| D-003 | 测试文件覆盖率需≥80% | team norm | info | Go | 保证代码质量 | ⏳ 待实现 |
| E-002 | 避免在循环中进行 HTTP 请求 | code review | warning | Go | 防止性能瓶颈 | ⏳ 待实现 |

---

## 🎯 MVP 目标 (Week 1-2)

**计划实现的 3 条规则**:

1. **A-002**: banned-md5-encryption (安全合规)
   - 理由：明确的安全风险，易于理解和接受
   - 难度：⭐ (简单，只需添加 banned-api 规则)

2. **B-001**: no-direct-db-call-in-service (架构分层)
   - 理由：常见的架构反模式，对可维护性影响大
   - 难度：⭐⭐ (中等，需要定义 scope_pattern)

3. **D-001**: require-error-handling (代码规范)
   - 理由：提升代码健壮性的基本要求
   - 难度：⭐⭐ (中等，需要 AST 分析)

---

## 📝 补充说明

### 如何添加新约束

1. 在上方表格中添加一行，填写基本信息
2. 在下方"详细记录"部分补充技术细节
3. 与团队成员讨论确认业务理由
4. 评估实现难度和优先级

### 约束转化流程

```
业务需求 → 自然语言描述 → AST Query → Policy YAML → 验证测试 → 上线
     ↓                                        ↑
     └─────────────── AI 辅助 ────────────────┘
```

### 优先级评估标准

- **P0**: 安全风险、法律合规、严重影响架构
- **P1**: 代码质量问题、常见反模式、影响可维护性
- **P2**: 风格规范、最佳实践、锦上添花

---

## 🔍 详细记录

### A-002: banned-md5-encryption

**详细描述**: 项目中禁止使用 MD5 哈希算法进行任何加密或安全相关的操作。

**业务理由**: 
- MD5 已被证明存在碰撞漏洞
- 不符合 OWASP 安全指南
- 可能影响安全审计通过

**示例违规代码**:
```go
import "crypto/md5"

hash := md5.Sum([]byte(password))  // ❌ 错误
```

**正确做法**:
```go
import "golang.org/x/crypto/bcrypt"

hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)  // ✅ 正确
```

**实现思路**:
```yaml
banned-api:
  - match: md5.New
    reason: MD5 is not secure; use SHA256 or bcrypt instead
    severity: error
  - match: crypto/md5
    kind: import
    reason: MD5 is not secure; use SHA256 or bcrypt instead
    severity: error
```

---

### B-001: no-direct-db-call-in-service

**详细描述**: Service 层的函数不应该直接调用数据库查询方法，应该通过 Repository 模式访问数据。

**业务理由**:
- 保证依赖注入模式的一致性
- 提升单元测试的可模拟性
- 便于后续切换到其他数据存储

**示例违规代码**:
```go
// internal/service/user_service.go
func (s *UserService) GetUser(id int) (User, error) {
    return s.db.Query("SELECT * FROM users WHERE id = ?", id)  // ❌ 错误
}
```

**正确做法**:
```go
// internal/service/user_service.go
func (s *UserService) GetUser(id int) (User, error) {
    return s.userRepo.FindByID(id)  // ✅ 正确
}

// internal/repository/user_repo.go
type UserRepository interface {
    FindByID(id int) (User, error)
}
```

**实现思路**:
```yaml
rules:
  no-direct-db-call-in-service:
    enabled: true
    severity: warning
    description: "Service layer should not directly access database"
    kind: call
    match: "(db\\.Query|db\\.Exec|db\\.Raw|\\.Create|\\.Update|\\.Delete)"
    message: "Use repository pattern instead of direct DB access"
    options:
      scope_pattern: "internal/service/**"
      except_files: ["*_test.go"]
```

---

### D-001: require-error-handling

**详细描述**: 所有返回错误的函数调用都必须进行错误处理，不能忽略错误。

**业务理由**:
- 避免未处理的错误导致程序异常
- 提升系统的健壮性和可靠性
- 便于问题追踪和调试

**示例违规代码**:
```go
result := doSomething()  // ❌ 错误：忽略了返回值中的错误
```

**正确做法**:
```go
result, err := doSomething()
if err != nil {
    return err  // ✅ 正确：处理了错误
}
```

**例外情况**:
```go
_ = doSomething()  // 允许的忽略，但必须有注释说明原因
// TODO: 这个错误可以忽略，因为...
```

**实现思路**:
需要 tree-sitter query 检测函数调用，判断是否包含错误返回值处理。

---

## 📈 进度跟踪

| 周次 | 目标 | 完成情况 |
|-----|------|---------|
| Week 1 | 完成 Constraint Inventory | ⏳ 进行中 |
| Week 2 | 实现 3 条 MVP 规则 | ⏳ 待开始 |
| Week 3 | 验证测试并收集反馈 | ⏳ 待开始 |
| Week 4 | 文档化和总结 | ⏳ 待开始 |

---

*版本：v1.0*  
*维护者：[你的名字]*  
*最后更新：2024-09-28*
