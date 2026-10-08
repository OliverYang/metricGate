# Pilot：规范文档 → metricGate policy → 真实项目检查（MVP 全流程记录）

**目的**：验证"团队非标规范（以《阿里巴巴 Java 开发手册》为规范源头）→ 可执行 policy → 真实 Java 项目门禁"能否端到端跑通，并沉淀工具能力空隙清单，作为产品路线图输入。

```
《阿里 Java 手册》43 条抽样调研          模拟团队规范 biz-spec-01~04（40 条）
        │ 可检查性分级 ✅17/⚠️13/❌13            │ 逐条编号条款
        └──────────────┬─────────────────────────┘
                       ▼
            demo/policy-pilot-java.yaml（40 条 → 22 条规则）
                       │ ./metricgate check
                       ▼
     gitee-scan-2.0-portal（真实 Maven Spring 项目，1187 个 java 文件，只读）
                       │ 1.2s
                       ▼
        563 error / 523 warning / 30534 snapshots（gate FAILED 属预期：
        存量项目首次接门禁，既有真违规也有需豁免项）
```

## 阅读顺序（建议）

1. **[pilot-report-scan-portal.md](pilot-report-scan-portal.md)** — 先读结论：流程验证情况、命中/误报分析、6 项产品建议
2. **[conversion-map.md](conversion-map.md)** — 40 条条款 ↔ 规则逐条对照 + 6 条工程教训；工具能力空隙清单（最有价值）
3. **[alibaba-manual-research.md](alibaba-manual-research.md)** — 规范源头调研：43 条抽样的可检查性分级依据
4. **biz-spec-01~04** — 输入物料样例：模拟团队规范长什么样，"工具化诉求"表如何标注

## 复现命令

```bash
cd /Users/mac/Downloads/metricGate
./metricgate check -config demo/policy-pilot-java.yaml /Users/mac/Downloads/main/gitee-scan-2.0-portal
```

> ⚠️ 目标项目 `/Users/mac/Downloads/main/gitee-scan-2.0-portal` 为只读参考项目，检查过程中不得修改。

## 文件清单

| 文档 | 角色 | 关键数字 |
|---|---|---|
| [alibaba-manual-research.md](alibaba-manual-research.md) | task-1 调研笔记 | 43 条抽样 ✅17/⚠️13/❌13 |
| [biz-spec-01](biz-spec-01-命名与注释规范.md) · [02](biz-spec-02-异常与日志规范.md) · [03](biz-spec-03-并发与性能规范.md) · [04](biz-spec-04-工程分层与依赖规范.md) | task-2 输入物料 | 4 篇共 40 条条款 |
| demo/policy-pilot-java.yaml（仓库根） | task-3 policy | 22 条规则 / 5 架构分层 |
| [conversion-map.md](conversion-map.md) | task-3 转化对照 | 40 条 ✅15/⚠️5/❌20 + 6 教训 |
| （check 执行） | task-4 跑门禁 | 563 error / 523 warning / 1.2s |
| [pilot-report-scan-portal.md](pilot-report-scan-portal.md) | task-5 夜间总结 | 6 项产品建议 |

## 主要命中类别（真实项目）

| 类别 | 数量 | 说明 |
|---|---|---|
| layer-violation | 132 | 其中 77 处同根因（interceptor 类被 server 层引用，类搬家即消）；web→dao 19 处真违规 |
| cyclomatic / cognitive | 75 / 68 | 超阈值函数复杂度 |
| print-stack-trace | 44 | printStackTrace 直接打栈 |
| function-clone | 72 | 函数级克隆 |
| SimpleDateFormat | 22 | 线程不安全日期格式（观察期 warning） |
| banned-Executors | 8 | 4 call + 4 import |
| nesting（error 档） | 11 | 嵌套超 6 层红线 |
| import-cycle | 1 | annotation/valid↔business↔config↔converter 真实包循环 |
