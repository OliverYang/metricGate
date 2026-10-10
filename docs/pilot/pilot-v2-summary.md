# 规约 v1 第二次端到端试点（P2）

> 状态：任务集已准备，待 TS 支持完成后执行  
> 目标项目：ace-ts（TypeScript, 全 Claude 生成）

## 任务集

10 个真实场景任务，覆盖规约 v1 所有条款（LC-101~404）：
- T01: Agent 会话持久化 → LC-101 (unknown-dependency), LC-102 (layering), LC-201 (complexity)
- T02: Skill 管理器 → LC-103 (clone detection), LC-402 (API diff)
- T03: Session 流式传输 → LC-301 (empty-block), LC-303 (stub), LC-202 (cognitive complexity)
- T04-T10: 模型客户端、工具解析器、记忆系统、配置加载器、日志系统、错误处理、插件系统

## 执行计划

1. **TS 支持完成**（当前阻塞点）
2. **运行试点**：`./run_pilot.sh` 对每个任务生成代码并检查
3. **误报审计**：统计红线误报率，评估 scorecard 可消费性
4. **v2 修订**：根据审计结果修订 llm-code-acceptance-spec-v2.md

## 成功判据

- ✅ 红线误报率 ≤5%
- ✅ scorecard 字段清晰可被 agent 消费
- ❌ 红线误报率 >10% → 条款定义需重写
