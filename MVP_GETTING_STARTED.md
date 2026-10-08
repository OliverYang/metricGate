# MetricGate 约束识别 MVP - 快速开始指南

## 🎯 核心目标

在**4 周内**验证"团队约束 → 工具规则"的转化流程是否可行。

**成功标准**:
- ✅ 实现 3-5 条有效规则
- ✅ 误报率 < 20%
- ✅ 开发者满意度 ≥ 3.5/5

---

## 📋 你需要做什么？

### Week 1: 约束调研 (Day 1-5)

#### Day 1: 准备
```bash
# 阅读计划
cat MVP_PLAN.md

# 查看现有文档
cat policy.yaml
cat README.md
ls docs/
```

#### Day 2-3: 数据收集
```bash
# 查看 Git 历史
git log --oneline --since="3 months ago" | head -30

# 查找常见模式
git log -p --all | grep -E "(db\.|Query|Exec|md5)" | head -20
```

#### Day 4-5: 填写 Constraint Inventory
编辑 `docs/constraint-inventory.md`,记录你发现的约束。

**建议先填这些**:
- A-001: banned-fmt-print (已有，作为 baseline)
- A-002: banned-md5-encryption (安全合规)
- B-001: no-direct-db-call-in-service (架构分层)

### Week 2: 规则实现 (Day 6-10)

#### Step 1: 编写自然语言描述
```markdown
## Rule: banned-md5-encryption

**描述**: 禁止使用 MD5 哈希算法进行任何加密或安全相关操作。

**业务理由**: 
- MD5 已被证明存在碰撞漏洞
- 不符合 OWASP 安全指南
- 可能影响安全审计通过

**示例违规**:
```go
import "crypto/md5"
hash := md5.Sum([]byte(password))  // ❌
```

**正确做法**:
```go
import "golang.org/x/crypto/bcrypt"
hash, _ := bcrypt.GenerateFromPassword([]byte(password), 10)  // ✅
```
```

#### Step 2: 转换为 YAML
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

#### Step 3: 测试规则
```bash
./metricgate check . --rules banned-md5-encryption
```

### Week 3: 验证测试 (Day 11-15)

#### 运行规则
```bash
# 生成报告
./metricgate check . --format json > findings.json

# 查看结果
cat findings.json | jq '.findings[] | select(.rule_id == "banned-md5-encryption")'
```

#### 人工审核
- 检查每条发现是否合理
- 统计误报数量
- 记录开发者反馈

#### 收集反馈
发送简单的问卷给团队成员:
```markdown
## 规则反馈表

规则名称：banned-md5-encryption

1. 这条规则是否合理？ [ ] 非常合理  [ ] 基本合理  [ ] 不太合理
2. 你遇到过类似的违规吗？ [ ] 经常  [ ] 偶尔  [ ] 很少
3. 修复难度如何？ [ ] 很容易  [ ] 容易  [ ] 中等
```

### Week 4: 文档化 (Day 16-20)

#### 编写指南
创建 `docs/constraint-guide.md`,包含:
- 如何添加新约束
- 配置示例
- 最佳实践
- 常见问题

#### 创建模板库
在 `templates/constraints/` 中创建:
- banned-api-template.yaml
- architecture-template.yaml
- security-template.yaml

#### 总结分享
整理 MVP 成果报告，准备团队分享。

---

## 🛠️ 需要的资源

### 人力
- **主导**: 1 人 (你)
- **参与**: 2-3 人 (访谈和反馈)

### 工具
- ✅ metricGate CLI (已构建)
- ✅ LLM 模型 (ollama/gpt, 可选)
- ⚠️ Git 访问权限

### 时间投入
- Week 1: 2-3 小时/天
- Week 2: 2-3 小时/天
- Week 3: 1-2 小时/天
- Week 4: 1 小时/天

**总计**: 约 60-80 小时

---

## 📚 关键文档

| 文档 | 用途 | 何时阅读 |
|-----|------|---------|
| `MVP_PLAN.md` | 详细实施计划 | Day 1 |
| `README_MVP.md` | 快速开始指南 | Day 1 |
| `docs/constraint-inventory.md` | 约束清单 | Day 1-5 |
| `docs/interview-questions.md` | 访谈问题 | Day 2-3 (可选) |
| `policy.yaml` | 现有策略 | Day 1 |

---

## 💡 关键原则

### 1. 小步快跑
- 先实现 3-5 条规则，不追求大而全
- 每周都有可交付的成果

### 2. 人工 +AI
- MVP 阶段不追求全自动
- 人工审核是关键，AI 只是辅助

### 3. 业务驱动
- 每条规则都要有明确的业务理由
- 不要为了技术而技术

### 4. 快速迭代
- 根据反馈快速调整
- 允许有不完善的规则

---

## ⚠️ 注意事项

### 不要做的
- ❌ 不要追求一次性完美
- ❌ 不要过度自动化
- ❌ 不要忽略开发者反馈

### 要做的
- ✅ 重视业务理由
- ✅ 记录每一步的发现
- ✅ 建立与开发者的信任
- ✅ 保持灵活和调整

---

## 🎯 推荐先实现的 3 条规则

### 1. banned-md5-encryption (⭐ 简单)
**业务理由**: 明确的安全风险  
**实现方式**: 添加 banned-api 规则  
**预期效果**: 高 (安全合规要求)

### 2. no-direct-db-call-in-service (⭐⭐ 中等)
**业务理由**: 保证依赖注入模式  
**实现方式**: 定义 scope_pattern + call 规则  
**预期效果**: 高 (架构一致性)

### 3. require-error-handling (⭐⭐ 中等)
**业务理由**: 提升代码健壮性  
**实现方式**: AST 分析错误处理  
**预期效果**: 中 (常见但非致命)

---

## 📊 进度跟踪模板

```markdown
## Week X 进度

### 已完成
- [ ] 任务 1
- [ ] 任务 2
- [ ] 任务 3

### 进行中
- [ ] 任务 4

### 遇到的问题
- 问题 1: 解决方案

### 下周计划
- 计划 1
- 计划 2
```

---

## ❓ 常见问题

**Q: 我没有时间访谈团队成员怎么办？**  
A: 可以先从现有文档和 Git 历史中提取约束，访谈可以稍后补充。MVP 的核心是验证流程，访谈是优化项。

**Q: 我不懂 tree-sitter 和 AST query 怎么办？**  
A: 可以使用 LLM 辅助生成 query，然后人工验证和调整。或者先从简单的 banned-api 规则开始，不需要 AST query。

**Q: 误报很多怎么办？**  
A: MVP 阶段允许有不完善的规则。关键是:
1. 记录误报情况
2. 分析原因
3. 调整规则参数
4. 收集开发者反馈

**Q: 规则太多会不会阻碍开发？**  
A: 是的，所以要:
1. 控制规则数量 (3-5 条)
2. 设置合理的严重度 (warning/info 为主)
3. 提供清晰的例外机制
4. 定期回顾和清理

**Q: 如何说服团队使用这个工具？**  
A: 展示价值比推销工具更重要:
1. 用实际发现证明价值
2. 让开发者参与规则制定
3. 倾听并解决他们的顾虑
4. 从小范围试点开始

---

## 🚀 立即行动

### Day 1 任务清单

```bash
# 1. 阅读计划 (30 分钟)
cat MVP_PLAN.md

# 2. 查看现有文档 (30 分钟)
cat policy.yaml
cat README.md

# 3. 开始填写 Constraint Inventory (1 小时)
vim docs/constraint-inventory.md

# 4. 初步扫描 Git 历史 (30 分钟)
git log --oneline --since="3 months ago" | head -30

# 5. 规划下周 (30 分钟)
vim README_MVP.md
```

**Day 1 总计**: 约 3-4 小时

---

## 📞 需要帮助？

参考以下文档获取指导:
- `MVP_PLAN.md` - 详细计划
- `docs/constraint-guide.md` - 操作指南 (Week 4 创建)
- `templates/constraints/` - 配置模板

---

*版本：v1.0*  
*最后更新：2024-09-28*  
*祝你好运！🚀*
