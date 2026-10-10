# 规约 v1 第二次端到端试点设计（P2）

> 目标：用真实 LLM 生成代码跑规约 v1，收集数据回流修订 v2  
> 状态：设计锁定（2026-10-10），待执行

## 1. 试点语料选择

**候选项目**：
- **metricGate 自身**（Go，当前仓库）：最方便，有完整门禁历史，但「自指」风险（模型可能知道规则）
- **marein/dev-cockpit**（Go，commit-replay 已验证 100% vibe coded）：真实 LLM 生成代码，289 commits，之前重放实验证明 error 清零有效
- **ace-ts**（TypeScript，全 Claude 生成）：需要加 TS 语法支持后才能跑

**决策**：先用 **dev-cockpit** 跑规约 v1 试点（Go 已有支持），同时并行做 TS 支持。

## 2. 试点任务集设计

参考 R1/R2/R3 的任务集设计原则：
- **覆盖规约 v1 所有条款**（LC-101~404）
- **混合难度**：简单（LC-101 幻觉依赖）、中等（LC-201~205 复杂度）、复杂（LC-403 影响面）
- **真实场景**：从 dev-cockpit 的 commit 描述中抽取任务文本（避免合成任务的人工痕迹）

**示例任务**（从 dev-cockpit 早期 commit 抽取）：
1. "实现 agent 保存功能" → LC-101 (unknown-dependency), LC-102 (layering), LC-201 (complexity)
2. "添加 skill 列表接口" → LC-103 (clone detection), LC-402 (API diff)
3. "处理 session 流式传输" → LC-301 (empty-block), LC-303 (stub), LC-202 (cognitive complexity)

## 3. 执行流程

```bash
# 1. 基线 snapshot（dev-cockpit 初始 commit）
git checkout <base-sha>
./metricgate api snapshot -out api-base.json

# 2. 对每个任务 run
for task in tasks/*.txt; do
  # 生成代码（headless pi）
  pi --provider 2api --model glm-5.3-flash -p "$(cat $task)" > output.go
  
  # 验收
  ./metricgate check -diff -base HEAD -scorecard -format json \
    > findings.json 2> scorecard.json
  
  # 记录
  echo "$task $(cat scorecard.json | jq '.structureViolations')" >> results.tsv
done

# 3. 误报审计
python3 audit-false-positives.py results.tsv
```

## 4. 成功判据

**规约 v1 修订 v2 的条件**：
- **红线误报率 ≤5%**（LC-101/LC-102/LC-104 等 false positives）
- **提示级条款可消费**（LC-201~205 的 warning 能被 agent 理解并修复）
- **scorecard 字段清晰**（thresholdBreaches/incompleteWork/clonedCode 有明确含义）

**失败条件**：
- 红线误报率 >10% → 条款定义需重写
- scorecard 无法被 agent 消费 → 格式需简化

## 5. 时间表

| 阶段 | 时间 | 产出 |
|---|---|---|
| 任务集编写 | 2026-10-10 | tasks/*.txt (10 个任务) |
| 执行试点 | 2026-10-11 | results.tsv + findings JSONs |
| 误报审计 | 2026-10-12 | audit-report.md |
| v2 修订 | 2026-10-13 | llm-code-acceptance-spec-v2.md |

## 6. 风险与缓解

- **风险**：dev-cockpit 无测试文件 → LC-404 (tests-touched) 无法验证  
  **缓解**：标记为 N/A，v2 中说明「仅对有测试项目的仓库适用」
- **风险**：agent 可能「背答案」（知道 metricGate 规则）  
  **缓解**：任务文本不提及 metricGate，只给业务需求

## 7. 关联工作

- **TS 支持**：parallel track，完成后用 ace-ts 跑第二轮试点
- **A-arm 重跑**：1.74× 归因实验，独立于规约试点
