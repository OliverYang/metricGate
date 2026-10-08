# Week 2: 规则实现指南

## 🎯 目标

将 3 个 P0 约束转化为可执行的 metricGate 规则。

---

## 📋 Day 1-2: A-002 banned-md5-encryption

### Step 1: 查看现有 policy.yaml 结构

```bash
cat policy.yaml | grep -A10 "banned-api"
```

### Step 2: 添加 MD5 规则

编辑 `policy.yaml`,在 `banned-api` 部分添加:

```yaml
banned-api:
  - match: fmt.Print*
    reason: direct fmt.Print* writes to stdout; use the project logger
  - match: panic
    reason: direct panic(); return an error so callers can decide
    except_files: ["_test.go"]
  - kind: import
    match: os/exec
    reason: only the git adapter and the CLI may shell out
    except_files: ["internal/git/git.go", "cmd/metricgate/main.go", "_test.go"]
  
  # === 新增 MD5 规则 ===
  - match: md5.New
    reason: MD5 is not secure for encryption; use SHA256 or bcrypt instead
    severity: error
  - kind: import
    match: crypto/md5
    reason: MD5 is not secure for encryption; use SHA256 or bcrypt instead
    severity: error
```

### Step 3: 测试规则

```bash
# 创建测试文件
cat > test_md5.go << 'TESTCODE'
package main

import (
    "crypto/md5"  // ❌ 应该触发警告
)

func main() {
    hash := md5.Sum([]byte("test"))
    _ = hash
}
TESTCODE

# 运行检查
./metricgate check . --rules banned-crypto-md5

# 清理
rm test_md5.go
```

### Step 4: 验证结果

预期输出:
```
test_md5.go:4: [error] banned-crypto-md5-import (crypto/md5 import is banned...)
test_md5.go:7: [error] banned-md5-new (md5.New is banned...)
```

如果成功，继续下一步!

---

## 📋 Day 3-4: B-001 no-direct-db-call-in-service

### Step 1: 分析现有代码模式

```bash
# 查找类似调用
grep -r "\.Query\|\.Exec\|\.Raw" --include="*.go" internal/service/ | head -10
```

### Step 2: 生成 tree-sitter query

使用 LLM 辅助生成:

```bash
# 示例 prompt (使用 ollama)
echo "请将以下约束转换为 Go 语言的 tree-sitter query:
约束：Service 层不能直接调用 db.Query, db.Exec, db.Raw 方法
示例违规代码：s.db.Query(\"SELECT...\")
只返回 query 本身，不要有其他解释" | ollama run llama3
```

或者手动编写:

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

### Step 3: 写入 policy.yaml

找到 `rules:` 部分，添加新规则:

```yaml
rules:
  # ... existing rules ...
  
  # === 新增 Service 层 DB 访问规则 ===
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

### Step 4: 测试规则

```bash
# 创建测试文件
cat > test_service_db.go << 'TESTCODE'
package service

type UserService struct {
    db *Database  // ❌ 不应该直接持有 db 引用
}

func (s *UserService) GetUser(id int) (User, error) {
    return s.db.Query("SELECT * FROM users WHERE id = ?", id)  // ❌ 违规
}
TESTCODE

# 运行检查
./metricgate check . --rules no-direct-db-call-in-service

# 清理
rm test_service_db.go
```

### Step 5: 人工验证

检查是否有误报:
- ✅ 真正的 Service 层 DB 调用被捕获
- ❌ 非 Service 层的调用不应被捕获
- ❌ 测试文件不应被捕获

---

## 📋 Day 5: D-001 require-error-handling (可选)

### 评估复杂度

这个规则需要:
1. 检测所有函数调用
2. 判断是否返回错误
3. 检查是否有错误处理

**建议**: 如果时间紧张，可以推迟到 Week 3。

### 如果决定实现

#### Step 1: 设计规则

```yaml
rules:
  require-error-handling:
    enabled: true
    severity: warning
    description: "All function calls that return errors must be handled"
    kind: call
    filter: "returns_error"
    message: "Error handling is required for this call"
    allow_ignored_with_comment: true
```

#### Step 2: 需要自定义逻辑

这可能需要编写 Go 代码来:
1. 解析 AST
2. 追踪函数签名
3. 检查返回值处理

**建议**: 先实现前两条规则，评估效果后再决定是否实现这条。

---

## 🔍 调试技巧

### 1. 查看详细日志

```bash
./metricgate check . --debug --rules banned-crypto-md5
```

### 2. 验证 query 匹配

```bash
# 使用 tree-sitter 命令行工具
tree-sitter parse test_file.go --query your_query.scm
```

### 3. 检查 scope_pattern

确保 `scope_pattern` 正确匹配你的目录结构:
- `"internal/service/**"` 匹配 `internal/service/*.go`
- `"internal/service/**/*.go"` 匹配子目录

---

## ✅ 完成标准

Day 1-2 (A-002):
- [ ] MD5 规则已添加到 policy.yaml
- [ ] 测试文件能正确触发警告
- [ ] 没有明显的误报
- [ ] 消息提示清晰易懂

Day 3-4 (B-001):
- [ ] Service 层 DB 规则已添加到 policy.yaml
- [ ] 测试文件能正确触发警告
- [ ] 非 Service 层不会被误判
- [ ] 测试文件被正确排除

Day 5 (D-001):
- [ ] 评估是否实现
- [ ] 如果实现，通过测试
- [ ] 否则记录为 Week 3 任务

---

## 📝 常见问题

**Q: MD5 规则没有生效？**
A: 检查:
1. 规则 ID 是否正确 (banned-crypto-md5)
2. severity 是否设置为 error
3. 是否在 banned-api 部分

**Q: Service 层规则误报了？**
A: 检查:
1. scope_pattern 是否正确
2. 是否包含了不该包含的目录
3. 是否需要添加 except_files

**Q: 如何知道规则是否被加载？**
A: 运行 `./metricgate rules explain list` 查看已激活的规则

---

*版本：v1.0*  
*最后更新：2024-09-28*
