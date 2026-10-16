# Constraint Inventory - 最终版

> **目的**: 记录团队的业务约束、研发规范和技术要求，为转化为 metricGate 规则提供依据。
> 
> **最后更新**: 2024-09-28 (Week 1 完成)

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

## 📋 约束列表 (已验证)

### P0 - 高优先级 (必须实现) ✅

| ID | 约束描述 | 来源 | 严重度 | 适用语言 | 业务理由 | 实现状态 |
|----|---------|------|--------|---------|---------|---------|
| A-001 | 禁止使用 fmt.Println 系列函数 | .metricgate.yaml | error | Go | 统一日志方案，便于追踪 | ✅ 已实现 |
| A-002 | 禁止使用 MD5 加密算法 | SECURITY.md | error | Go,Java | 安全合规要求，MD5 已被证明不安全 | ⏳ Week 2 实现 |
| B-001 | Service 层不能直接调用数据库查询 | 架构模式分析 | warning | Go | 保证依赖注入模式，提升可测试性 | ⏳ Week 2 实现 |
| B-002 | internal 包不能被 cmd 引用 | .metricgate.yaml | warning | Go | 保持架构分层清晰 | ✅ 已有 |

### P1 - 中优先级 (建议实现)

| ID | 约束描述 | 来源 | 严重度 | 适用语言 | 业务理由 | 实现状态 |
|----|---------|------|--------|---------|---------|---------|
| D-001 | 所有返回错误的函数调用必须处理 | 代码规范分析 | warning | Go | 提升代码健壮性 | ⏳ Week 3 考虑 |
| D-002 | public 函数必须有注释 | README | info | Go,Java | 提升代码可读性 | ⏳ 后续 |

---

## 🎯 MVP 目标 (Week 1-2) ✅ 确认

**计划实现的 3 条规则**:

### 1. A-002: banned-md5-encryption (安全合规) ⭐⭐⭐
- **业务理由**: MD5 已被证明存在碰撞漏洞，不符合 OWASP 安全指南
- **实现难度**: ⭐ (简单，只需添加 banned-api 规则)
- **预期效果**: 高 (明确的安全风险，易于理解和接受)
- **实现时间**: Week 2 Day 1-2

### 2. B-001: no-direct-db-call-in-service (架构分层) ⭐⭐⭐
- **业务理由**: 保证依赖注入模式的一致性，提升单元测试的可模拟性
- **实现难度**: ⭐⭐ (中等，需要定义 scope_pattern)
- **预期效果**: 高 (架构一致性，对可维护性影响大)
- **实现时间**: Week 2 Day 3-4

### 3. D-001: require-error-handling (代码规范) ⭐⭐
- **业务理由**: 避免未处理的错误导致程序异常，提升系统健壮性
- **实现难度**: ⭐⭐⭐ (较复杂，需要 AST 分析)
- **预期效果**: 中 (常见但非致命，开发者容易接受)
- **实现时间**: Week 2 Day 5 (或 Week 3)

---

## 📝 详细规则定义

### A-002: banned-md5-encryption

**自然语言描述**:
```
项目中禁止使用 MD5 哈希算法进行任何加密或安全相关的操作。
MD5 已被证明存在碰撞漏洞，不符合 OWASP 安全指南，可能影响安全审计通过。
```

**示例违规代码**:
```go
import "crypto/md5"

hash := md5.Sum([]byte(password))  // ❌ 错误：使用 MD5
```

**正确做法**:
```go
import "golang.org/x/crypto/bcrypt"

hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)  // ✅ 正确
```

**metricGate 规则定义**:
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

**自然语言描述**:
```
Service 层的函数不应该直接调用数据库查询方法，应该通过 Repository 模式访问数据。
这样可以保证依赖注入模式的一致性，提升单元测试的可模拟性，便于后续切换到其他数据存储。
```

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

**metricGate 规则定义**:
```yaml
rules:
  no-direct-db-call-in-service:
    enabled: true
    severity: warning
    description: "Service layer should not directly access database"
    kind: call
    match: "(db\\.Query|db\\.Exec|db\\.Raw)"
    message: "Use repository pattern instead of direct DB access"
    options:
      scope_pattern: "internal/service/**"
      except_files: ["*_test.go"]
```

---

### D-001: require-error-handling

**自然语言描述**:
```
所有返回错误的函数调用都必须进行错误处理，不能忽略错误。
这可以避免未处理的错误导致程序异常，提升系统的健壮性和可靠性，便于问题追踪和调试。
```

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

**metricGate 规则定义**:
```yaml
rules:
  require-error-handling:
    enabled: true
    severity: warning
    description: "All function calls that return errors must be handled"
    kind: call
    query: |
      (call_expression
        arguments: (argument_list))
      (#match? @arguments "\\w+")
    filter: "returns_error"
    message: "Error handling is required for this call"
    except_comments: true
```

---

## 🗓️ Week 2 实施计划

### Day 1-2: A-002 banned-md5-encryption
- [ ] 编写自然语言描述
- [ ] 添加到 .metricgate.yaml
- [ ] 测试规则有效性
- [ ] 验证误报率

### Day 3-4: B-001 no-direct-db-call-in-service
- [ ] 编写自然语言描述
- [ ] 生成 tree-sitter query
- [ ] 人工验证和调整
- [ ] 写入 .metricgate.yaml
- [ ] 测试规则有效性

### Day 5: D-001 require-error-handling (可选)
- [ ] 评估实现复杂度
- [ ] 如果太复杂，推迟到 Week 3
- [ ] 否则实现并测试

---

## 📈 进度跟踪

| 周次 | 目标 | 完成情况 |
|-----|------|---------|
| Week 1 | 完成 Constraint Inventory | ✅ 已完成 |
| Week 2 | 实现 3 条 MVP 规则 | ⏳ 进行中 |
| Week 3 | 验证测试并收集反馈 | ⏳ 待开始 |
| Week 4 | 文档化和总结 | ⏳ 待开始 |

---

*版本：v1.0-Final*  
*维护者：[你的名字]*  
*最后更新：2024-09-28*
