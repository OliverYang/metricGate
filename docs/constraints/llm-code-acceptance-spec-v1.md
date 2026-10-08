# LLM 生成代码验收规约 v1

> 定位：**给"LLM 生成代码"的验收规约** —— 下一轮端到端试点的规范输入，替代"人写手册"式的 biz-spec。条款按 LLM 四类失败模式组织（依据：[R1 实测](../evaluations/evaluation-base-rate.md)），每条附 metricGate 落地方式与当前状态。范式背景见 [../research/llm-constraint-paradigm.md](../research/llm-constraint-paradigm.md)。
> 版本：v1（2026-09-29）。试点结束后按误报审计结论修订 v2。

## 1. 适用范围与验收协议

适用对象：coding agent 在本仓库产生的一切**变更**（diff 范围），手写与生成同规约 —— 闸门不区分作者。

验收协议（每次变更走一遍，`-diff` 系列均为毫秒级、0 token）：

```bash
# 2 验收（findings JSON → stdout，scorecard JSON → stderr）
./metricgate check -diff -base HEAD -regress -scorecard -format json
# 5 改了导出面时追加：
./metricgate api snapshot -out /tmp/api-base.json   # 基线（每里程碑更新一次）
./metricgate api diff -base /tmp/api-base.json      # 破坏性变更 = 阻断
```

## 2. 条款等级

| 等级 | 含义 | gate 行为 |
|---|---|---|
| **红线** | 违反即不接受 | `fail_on: error` 阻塞 |
| **提示** | 记入 scorecard，人工/CR 消费 | warning/info，不阻塞 |
| 范围外 | 属传统 lint 领域 | 委托 golangci-lint / PMD（互补不替代） |

阈值采用 `preset: agentic` 口径（LLM 冗长倾向下，尺寸类刻意放松，复杂度上限不放松；policy 显式值始终胜出 preset）。

## 3. 条款表

### 类1 · 不认识仓库（幻觉依赖、无视分层、重造已有实现）

| ID | 条款 | 等级 | metricGate 落地 | 状态 |
|---|---|---|---|---|
| LC-101 | 只 import 声明过的依赖（清单内或标准库），禁止幻觉包 | 红线 | `unknown-dependency`（error；allow = policy `dependencies.self/allow` + go.mod） | ✅ |
| LC-102 | 遵守架构分层与禁用边（layer/forbid/无环） | 红线 | architecture `layer-violation` / `forbid*` / `import-cycle`（error） | ✅ |
| LC-103 | 不重造仓库已有实现（near-miss 克隆视为重造信号） | 提示 | `function-clone` / `near-clone` / `clone-drift`（warning）+ scorecard `clonedCode` | ✅ |
| LC-104 | 禁用 API 黑名单（banned-api） | 红线 | `banned-*`（severity 按 policy rules 段） | ✅ |
| LC-105 | 引用的符号必须已定义（unresolved-reference） | 红线 | refs×defs join（infrastructure 已备） | ❌ P2 待建 |

### 类2 · 过度工程（防御性嵌套、复杂度膨胀）—— R1 最高产失败类（18.2%，全 error 级）

| ID | 条款 | 等级 | metricGate 落地（agentic 口径） | 状态 |
|---|---|---|---|---|
| LC-201 | 圈复杂度 ≤15 | 红线 | `cyclomatic-complexity` error 15（warn 10） | ✅ |
| LC-202 | 认知复杂度 ≤25 | 红线 | `cognitive-complexity` error 25（warn 15） | ✅ |
| LC-203 | 嵌套深度 ≤6（目标 4） | 红线 | `nesting-depth` error 6（warn 4） | ✅ |
| LC-204 | 函数参数 ≤9（目标 6） | 红线 | `params-count` error 9（agentic 放松档） | ✅ |
| LC-205 | 函数长度 ≤160 行（目标 80） | 提示 | `function-length` warn 80 / error 160（agentic 放松档） | ✅ |

### 类3 · 伪完成（stub、TODO、panic("not implemented")、空块、静默吞异常）

| ID | 条款 | 等级 | metricGate 落地 | 状态 |
|---|---|---|---|---|
| LC-301 | 禁空实现块（if/for/while/catch 空体） | 红线 | `empty-block`（agentic 档 = error） | ✅ |
| LC-302 | TODO/FIXME 须登记（作为未完成信号计入验收） | 提示 | `todo-comment`（agentic 档启用 = warning）→ scorecard `incompleteWork` | ✅ |
| LC-303 | 禁 panic("not implemented")/empty-body 桩 | 红线* | stubs 扫描（empty-body / panic-not-implemented）→ scorecard `incompleteWork`；*暂为计数未进 fail_on 门禁 | ⚠️ |
| LC-304 | 禁静默吞异常（catch 后不处理不上抛） | 提示* | 内置 `silent-catch`（2026-09-30 落地）：catch 块非空但**未引用异常参数**且无 throw 无日志 → 命中；引用参数（`e.getMessage()` 进响应/包装上抛/传处理器）视为处理中。*观察期 warning，FP 审计后再升 error；已知误报族：解析探测/降级惯用法（`parseInt→0`、`parse→null`），团队用 `except_files` 豁免 | ⚠️ 观察 |

### 类4 · 破坏稳定面（API 破坏、影响面失控、度量悄悄变差）

| ID | 条款 | 等级 | metricGate 落地 | 状态 |
|---|---|---|---|---|
| LC-401 | 度量不得比 base 变差（即使未触阈值） | 红线 | `check -regress` → `metric-regression`（warning；团队可提高） | ✅ |
| LC-402 | 导出面不得破坏性变更 | 红线 | `metricgate api diff -base F` 破坏性变更闸门 | ✅ |
| LC-403 | 影响面超预期须显式说明 | 提示 | `check -diff -impact-scope` → gatecheck 模块：变更符号影响面超 cap（默认 25）出 error，policy 可调 cap/severity 或 enabled:false 否决 | ✅ |
| LC-404 | 新增行为须触碰测试（tests-touched） | 提示 | `check -diff -tests-touched` → gatecheck 模块：逻辑变更未触测试文件出 warning，policy 可调级/否决；scorecard `testsTouched` 保持计数 | ✅ |

### 范围外（委托传统工具，本规约不覆盖）

命名风格、格式、注释拼写给 golangci-lint / PMD / P3C —— R1 实测 LLM 在 lint 类规则上违规率趋零，闸门不重复计费（互补定位，见 [../strategy/positioning.md](../strategy/positioning.md)）。

## 4. 达标判据（scorecard 消费规则）

`check -diff -scorecard` 的 Card JSON 字段 → 判定：

- **红线全 0 才接受**：`structureViolations`、`dependencyViolations`、`bannedApiCalls`、`metricRegressions`、`parseErrors` = 0
- **提示级**：`thresholdBreaches` = 0（目标）；`incompleteWork` ≤ 1 且每条 TODO 带 issue 引用；`clonedCode` 由 CR 判断是否合理的复用变体；`testsTouched` = true（有行为变更时）
- `topRules[].{rule,count}` 用于定位修复优先级：先修 count 最高的红线规则

## 5. 豁免机制

- 红线豁免走 policy `except_files` / `allowed_files` 白名单，须附 CR 理由（沿用试点 [biz-spec-04 约定](../pilot/biz-spec-04-工程分层与依赖规范.md)）。
- 豁免条目进 `rules gaps` 审计视野，防止白名单腐化。
- 度量阈值只能上调不能下调（内置默认 = 下限），故"临时放松"必须在 policy 留痕。

## 6. v1 已知边界（诚实声明）

1. LC-303/404 残余边界：stubs 计数未进门禁；tests-touched 已闸门化（warning 起步）但"触碰测试"是代理信号——测试是否真的覆盖行为仍需 CR；LC-304 静默吞异常为观察期代理判据（三信号）。
2. 纯语义正确性（业务逻辑对不对）不在本规约范围内 —— 业务轴的衡量 = 本规约这类**声明式规则的合规率**，覆盖不到的约束明确标 ❌，不假装覆盖。
3. v1 条款数值取 agentic 档口径，属建议值；试点后按 FP 审计结论修订 v2（回流[约束识别三层架构](constraint-mvp-design.md)的反馈层）。

---
*撰写：2026-09-29；状态更新 2026-09-30（LC-304 静默吞异常由 ❌ 转 ⚠️：内置 silent-catch 规则落地，观察期；LC-403/404 由 ⚠️ 转 ✅：internal/gatecheck + `check -diff -impact-scope/-tests-touched` 落地）。状态标记依据：✅ = 规则已在引擎/policy 层生效；⚠️ = 能力部分存在（计数/报告/观察期）未进门禁；❌ = 待建（已在调研文档排期）。*
