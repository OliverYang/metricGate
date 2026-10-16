# MetricGate 约束识别 MVP 设计方案

## 🎯 MVP 目标

**核心目标**: 验证"团队约束 → 工具规则"的转化流程是否可行，不追求自动化程度，先跑通人工 +AI 辅助的流程。

**成功标准**:
- ✅ 能从现有文档/代码中提取至少 3 个有效规则
- ✅ 规则能正确应用到项目并产生有意义的发现
- ✅ 开发者愿意使用这套流程

---

## 📊 第一阶段：约束调研 (Week 1)

### 1.1 调研范围

基于对 Go/Java 项目的通用约束模式分析，我们聚焦以下**5 类最常见、最有价值的约束**:

| 类别 | 典型场景 | 业务价值 | 优先级 |
|-----|---------|---------|--------|
| **A. API 黑白名单** | "禁止使用 fmt.Println"、"只能用内部 HTTP 客户端" | 防止技术债、统一日志方案 | ⭐⭐⭐⭐⭐ |
| **B. 架构分层** | "Service 层不能直接访问 DB"、"core 包不能被 cmd 引用" | 保证架构清晰、可维护性 | ⭐⭐⭐⭐⭐ |
| **C. 安全合规** | "禁止 MD5 加密"、"用户数据必须脱敏" | 满足安全审计要求 | ⭐⭐⭐⭐ |
| **D. 代码规范** | "所有接口必须加注释"、"错误必须处理" | 提升代码质量 | ⭐⭐⭐ |
| **E. 性能约束** | "循环内不能有数据库查询"、"避免 N+1 查询" | 预防性能问题 | ⭐⭐⭐ |

### 1.2 数据来源

#### 来源 1: 现有政策文件
```bash
# 收集位置
./.metricgate.yaml              # 已有策略
./README.md                # 项目说明
./docs/architecture.md     # 架构文档
./SECURITY.md             # 安全规范
```

#### 来源 2: Code Review 记录
```bash
# 提取历史 CR 中的常见意见
git log --all --format="%H %s" | head -50
gh pr list --state merged --limit 20
gh pr comments <PR_NUMBER>
```

#### 来源 3: 团队访谈 (可选但强烈推荐)
准备 5-7 个问题清单:
1. "你们最常在 CR 中驳回的代码问题是什么？"
2. "有哪些技术债务是你们特别想避免的？"
3. "有没有什么约定是新人经常违反的？"
4. "安全/合规方面有什么硬性要求？"
5. "哪些代码模式会让你们觉得'这代码写得不专业'？"

### 1.3 输出物

**Constraint Inventory 表格**:
| ID | 约束描述 | 来源 | 严重度 | 适用语言 | 业务理由 |
|----|---------|------|--------|---------|---------|
| C-001 | 禁止使用 fmt.Println | .metricgate.yaml | error | Go | 统一日志方案 |
| C-002 | Service 层不能直接调用 db.Query | CR 记录 | warning | Go | 依赖注入模式 |
| C-003 | 禁止使用 MD5 加密 | SECURITY.md | error | Go,Java | 安全合规 |

---

## 🔧 第二阶段：规则实现 (Week 2)

### 2.1 实现方法

**不追求全自动**,采用"人工定义 + AI 辅助"的方式:

#### Step 1: 自然语言描述
```markdown
## 约束 C-002: Service 层不能直接访问数据库

**描述**: Service 层的函数不应该直接调用数据库查询方法。应该通过 Repository 模式访问数据。

**示例违规**:
```go
// ❌ 错误
func (s *UserService) GetUser(id int) (User, error) {
    return s.db.Query("SELECT * FROM users WHERE id = ?", id)
}

// ✅ 正确
func (s *UserService) GetUser(id int) (User, error) {
    return s.userRepo.FindByID(id)
}
```

**技术实现思路**:
- 检测函数名中包含 `db.`、`.Query`、`.Exec` 等模式的调用
- 检查调用者是否在 service 相关的包路径下
- 如果是，则报告违规
```

#### Step 2: AI 辅助转换为 AST Query
使用 LLM (ollama/gpt) 将自然语言转换为 tree-sitter query:

```python
# prompt_template.py
PROMPT = """
你是一个 tree-sitter 专家。请将以下代码约束转换为对应的 AST 查询:

约束描述: {description}
示例违规代码: {example_violation}
期望匹配的模式: {pattern_description}

请返回一个有效的 tree-sitter query，用于 {language} 语言。
只返回 query 本身，不要有其他解释。
"""
```

#### Step 3: 人工验证与调整
- 在测试代码上运行 query
- 检查是否有误报/漏报
- 手动微调 query

#### Step 4: 写入 .metricgate.yaml
```yaml
rules:
  service-layer-db-access:
    enabled: true
    severity: warning
    description: "Service layer should not directly access database"
    kind: import  # 或 call
    match: "(db\\.Query|db\\.Exec|db\\.Raw)"
    except_files: ["*_test.go"]
    message: "Use repository pattern instead of direct DB access"
```

### 2.2 MVP 规则集 (建议先实现 3-5 条)

#### Rule 1: banned-fmt-print (已有，作为 baseline)
```yaml
banned-api:
  - match: fmt.Print*
    reason: use logger instead of direct stdout
    severity: error
    except_files: ["cmd/*"]
```

#### Rule 2: banned-md5-encryption (安全合规)
```yaml
banned-api:
  - match: md5.New
    reason: MD5 is not secure for encryption; use SHA256 or bcrypt
    severity: error
  - match: crypto/md5
    kind: import
    reason: MD5 is not secure for encryption; use SHA256 or bcrypt
    severity: error
```

#### Rule 3: no-direct-db-call-in-service (架构分层)
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
```

#### Rule 4: require-error-handling (代码规范)
```yaml
rules:
  require-error-handling:
    enabled: true
    severity: warning
    description: "All function calls that return errors must be handled"
    kind: call
    match: "\\w+\\(.*\\)"
    filter: "returns_error"
    message: "Error handling is required for this call"
```

#### Rule 5: no-nested-loops (性能约束)
```yaml
rules:
  no-nested-loops:
    enabled: true
    severity: info
    description: "Avoid nested loops as they can cause performance issues"
    kind: ast
    query: |
      (for_statement
        body: (block
          statements: [(for_statement)]))
    message: "Consider refactoring to avoid nested loops"
```

---

## 🧪 第三阶段：验证与反馈 (Week 3)

### 3.1 验证流程

#### Step 1: 在现有项目上运行
```bash
# 应用新规则
./metricgate check . --rules C-001,C-002,C-003

# 查看结果
./metricgate check . --format json > findings.json
cat findings.json | jq '.findings[] | select(.rule_id == "no-direct-db-call-in-service")'
```

#### Step 2: 人工审核发现
创建审核清单:
- [ ] 每条发现都能追溯到具体的业务约束
- [ ] 没有明显的误报 (false positive)
- [ ] 发现的严重程度与实际影响匹配
- [ ] 消息提示清晰易懂

#### Step 3: 收集开发者反馈
设计简单的反馈表单:
```markdown
## 规则反馈表

规则名称：{rule_name}
你的角色：[ ] 开发负责人  [ ] 普通开发者  [ ] Tech Lead

1. 这条规则是否合理？ [ ] 非常合理  [ ] 基本合理  [ ] 不太合理  [ ] 完全不合理
2. 你遇到过类似的违规吗？ [ ] 经常  [ ] 偶尔  [ ] 很少  [ ] 从未
3. 修复这条规则的难度如何？ [ ] 很容易  [ ] 容易  [ ] 中等  [ ] 困难
4. 建议改进：_____________________
```

### 3.2 效果评估指标

**定量指标**:
- **规则覆盖率**: 已实现的约束 / 调研的约束 (目标：≥60%)
- **误报率**: 无效发现 / 总发现 (目标：<20%)
- **发现有效性**: 真正需要修复的发现 / 总发现 (目标：≥80%)

**定性指标**:
- 开发者满意度评分 (1-5 分)
- 规则可理解性评分
- 执行效率评价

---

## 📝 第四阶段：文档化与标准化 (Week 4)

### 4.1 编写 Constraint Guide

创建 `docs/constraint-guide.md`:

```markdown
# MetricGate 约束指南

## 如何使用

### 添加新约束

1. **填写约束登记表** (`docs/constraint-inventory.md`)
   - 描述约束的业务背景
   - 指定适用语言和范围
   - 确定严重度级别

2. **编写规则定义**
   ```yaml
   rules:
     constraint-name:
       enabled: true
       severity: warning/error/info
       description: "人类可读的描述"
       kind: call/import/ast
       match: "正则表达式或 tree-sitter query"
       message: "给开发者的提示信息"
       options:
         # 可选的配置项
   ```

3. **验证规则**
   ```bash
   ./metricgate validate rule constraint-name
   ./metricgate check . --rule constraint-name
   ```

4. **收集反馈并迭代**

### 约束分类

#### A. API 约束
控制允许/禁止使用的 API

#### B. 架构约束
确保代码结构符合架构设计

#### C. 安全约束
满足安全合规要求

#### D. 质量约束
提升代码质量

### 最佳实践

1. 从业务需求出发，而不是技术细节
2. 提供清晰的示例和反例
3. 设置合理的严重度级别
4. 定期回顾和清理过时规则
5. 收集开发者反馈持续优化
```

### 4.2 建立约束模板库

创建 `templates/constraints/`:
```
templates/constraints/
├── banned-api-template.yaml
├── architecture-template.yaml
├── security-template.yaml
├── quality-template.yaml
└── performance-template.yaml
```

每个模板包含:
- 典型场景
- 配置示例
- 常见陷阱
- 调试技巧

---

## 🚀 快速启动 checklist

### Day 1-2: 调研准备
- [ ] 列出要调研的约束类型 (5 类)
- [ ] 收集现有文档 (.metricgate.yaml, README, etc.)
- [ ] 准备访谈问题清单
- [ ] 创建 Constraint Inventory 表格

### Day 3-5: 数据收集
- [ ] 阅读现有文档，提取显式约束
- [ ] 扫描 Git 历史，识别常见模式
- [ ] (可选) 访谈 2-3 位团队成员
- [ ] 完成 Constraint Inventory 初稿

### Day 6-8: 规则实现
- [ ] 选择 3-5 个高优先级约束
- [ ] 为每个约束编写自然语言描述
- [ ] 使用 AI 辅助生成 AST query
- [ ] 人工验证并调整 query
- [ ] 写入 .metricgate.yaml

### Day 9-11: 验证测试
- [ ] 在现有项目上运行规则
- [ ] 人工审核所有发现
- [ ] 收集开发者反馈
- [ ] 调整规则参数

### Day 12-14: 文档化
- [ ] 编写 Constraint Guide
- [ ] 创建约束模板库
- [ ] 整理最佳实践
- [ ] 准备分享材料

---

## 💡 关键成功因素

1. **从小处开始**: 先实现 3-5 条规则，验证流程后再扩展
2. **人工介入**: MVP 阶段不追求全自动，人工审核是关键
3. **业务驱动**: 每条规则都要有明确的业务理由
4. **快速迭代**: 根据反馈快速调整，不要追求一次性完美
5. **开发者参与**: 让实际使用者参与规则制定和评审

---

## 📈 预期成果

完成这个 MVP 后，你将获得:

1. **一套可执行的约束规则** (3-5 条)
2. **验证过的约束转化流程** (调研→实现→验证→文档)
3. **开发者反馈数据** (满意度、误报率等)
4. **约束管理指南** (后续扩展的基础)
5. **约束模板库** (加速未来规则开发)

---

## ⚠️ 注意事项

1. **不要过度自动化**: MVP 阶段重点是验证流程，不是追求全自动
2. **接受不完美**: 初期可能有误报/漏报，关键是收集反馈
3. **保持灵活性**: 根据实际反馈调整方向，不要拘泥于计划
4. **重视文档**: 好的文档能让后续工作事半功倍
5. **建立信任**: 让开发者看到规则的价值，才能获得支持

---

*版本：v1.0-MVP*  
*最后更新：2024-09-28*
