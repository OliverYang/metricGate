# MVP 试点夜间总结 —— 从团队规范到可执行门禁的端到端验证

> 试点对象：gitee-scan-2.0-portal（scan-portal-business，真实 Maven Spring 项目，约 1187 个 Java 文件，只读）
> 试点日期：2026-09-28 夜间（自主推进）
> 一句话结论：**流程已端到端跑通**——4 篇团队规范文档 → 1 份 policy → 真实项目 1.2 秒产出 563 error / 523 warning，其中既有真违规（线程池、printStackTrace、包循环、controller 直连 dao），也暴露了 6 个产品级改进点。规范源（阿里手册）43 条抽样 ✅17/⚠️13/❌13；转化阶段 40 条条款中 15 条直接落地、20 条未转化（多指向自定义 DSL 路线图）。

## 1. 交付物清单（明早审阅顺序建议）

| # | 文件 | 内容 | 建议关注点 |
|---|---|---|---|
| 1 | `docs/pilot/alibaba-manual-research.md` | 手册 43 条抽样逐条标注可检查性（✅17/⚠️13/❌13）与 P0-P2 | 结论部分：P0 十条零代码可落地 |
| 2 | `docs/pilot/biz-spec-01~04-*.md` | 4 篇模拟团队研发规范 v1.0（输入物料，40 条编号条款） | 每篇的"工具化诉求"表 |
| 3 | `demo/policy-pilot-java.yaml` | 可执行 policy（563/523 那次运行的输入） | 分级注释与观察期条款 |
| 4 | `docs/pilot/conversion-map.md` | 40 条逐条转化对照 + 6 个工程教训 | **最有价值的一份**：工具能力空隙清单 |
| 5 | 本文档 | 流程验证、命中/误报分析、产品走向建议 | §5 产品走向 |

## 2. 流程是否跑通：每一步的验证方式

```
规范文档(biz-spec-01~04) ──转化──> policy-pilot-java.yaml ──check──> 真实项目 findings ──> 对照表+报告
```

- **policy 有效性**：用冒烟样本（故意包含全部禁 API 的两个小文件）逐条验证 banned 规则真实触发、severity 分级正确（error/warning 各就各位），排除"规则静默失效"。这一步抓出了规则 ID 字符替换（`.`→`-`）导致 severity 覆写全部失效的问题——全量跑 banned 命中为 0 时，冒烟测试是区分"代码干净"和"规则没生效"的唯一手段。
- **可复现性**：`./metricgate check -config demo/policy-pilot-java.yaml /Users/mac/Downloads/main/gitee-scan-2.0-portal`，1.2s wall / 6.3s CPU，30534 metric snapshots。性能足以做 pre-commit / agent post-edit 门禁。
- **策略迭代闭环**：首轮 658 error → 3 处 policy 修正（见 §4）→ 563 error。修正全部由真实 findings 驱动，模拟了真实团队的 policy 调优过程。

## 3. 真实项目 findings 分析

### 3.1 强制条款（error 级）命中 —— 都是真的

| 规则 | 命中 | 抽查结论 |
|---|---|---|
| layer-violation | 132 | 77 处同根因（见 3.2），19 处 controller 直连 dao 是真违规，15 处 config 装配引用业务 Bean |
| print-stack-trace | 44 | LOG-02 真违规，高频且机械可修 |
| cyclomatic/cognitive/cbo/wmc/lcom/nom/file-length/params/nesting/function-length | 296 | 内置默认红线，度量面真实分布（见教训 3） |
| banned-Executors（call+import 双计） | 8 | CONC-01 真违规：确实有 4 处 `Executors.new*` 建线程池 |
| banned-System.out | 1 | LOG-01 真违规 |
| import-cycle | 1 | **ARCH-02 抓到真实包循环**：annotation/valid ↔ business(access/rule) ↔ config ↔ converter——手册说"包循环是腐化前兆"，编译器管不到，正是门禁价值所在 |
| empty-block | 1 | EXC-01 真违规（TaskPlaceOnFileJob 空 catch/分支） |

### 3.2 误报/根因分析（policy 修正记录）

首轮 658 error 中有 104 条是 policy 配置问题（非代码问题），已修正：

1. **45 条 unknown-dependency → 0**：JUnit/Mockito 是 pom 的 test scope 依赖，初版 allow 清单只覆盖编译 scope。→ 白名单必须含测试依赖。
2. **59 条 common→pojo layer-violation → 0**：文档 ARCH-03 只要求 common 不依赖业务层；pojo 是数据载体，exception/converter 引 bean 属正常。→ 初版分层比文档更严，修正 `may_depend_on: [common, pojo]`。
3. **banned severity 全部回落 warning**：规则 ID 中 `.` 被替换为 `-`（`banned-System-out-print*`），按直觉写 `banned-System.out.print*` 匹配不上。→ 冒烟测试发现并修正。

剩余 132 条 layer-violation 的定性：**77 条（58%）是"类放错包"而非"依赖写错"**——`LoginContextHolder`/`BaseUserDetails` 放在 `interceptor` 包被 server 层全量引用。工具判定正确（依赖方向真实存在），但给团队的修复建议应是"把用户上下文类搬到 common/context"，这类**结构性修复建议**是报告层该自动生成的，不是让团队改代码绕规则。

### 3.3 观察期条款（warning 级，待定级数据）

| 规则 | 命中 | 观察点 |
|---|---|---|
| banned-SimpleDateFormat | 22 | 近似语义（整体禁用 vs 文档"禁 static 共享"）；22 处命中说明迁移成本真实存在，观察误报后再定 error |
| function-clone | 72 | DEP-03"强制"但首轮仅报告；72 处 Type-2 克隆是收敛清单 |
| nesting-depth warn 3 | 127 | 参考=3 的团队基线偏紧，看分布再调 |
| sdp-violation | 16 | 参考级 |

## 4. metricGate 产品走向建议（按优先级）

试点暴露的工具能力空隙，每条都有真实文档条款背书（详见 conversion-map 教训 1-6）：

1. **P0 · 自定义规则 DSL/钩子**：13 条未转化条款中 11 条卡在同一处——命名规约（NAMING-01~06）、catch 参数类型（EXC-02）、finally-return（EXC-03）等"AST 模式明确但内置没有"的规则。建议最小 DSL：**命名模式断言**（标识符正则 × 节点类型 × 包上下文）即可吃掉 6 条命名条款。这是"把非标规范变成工具规则"这个核心命题的最大杠杆。
2. **P0 · 架构违规的文件级豁免与结构修复建议**：banned-api 有 `except_files`，layer-violation 没有；且应支持在报告中输出"类搬家"类修复建议（77/132 的 layer 违规同根因）。
3. **P1 · 度量阈值表达能力**：内置 error 阈值无法单独关闭（只能整体 `enabled:false`），文档"参考级仅报告"语义表达不完整。建议 MetricConfig 支持 `error: off`。同时 rules-gaps 审计应把"policy 表达力不足"作为一类缺口输出。
4. **P1 · call ban receiver 深度 / 构造器**：全限定名调用逃逸 call ban（冒烟确认）；当前对策是 call+import 双保险，长期应支持更深的 receiver 匹配或字符串参数检查（SEC-01 的 `MessageDigest.getInstance("MD5")` 也卡在这）。
5. **P2 · 依赖清单半自动生成**：allow 清单提取是本次最费时的手工步骤；`metricgate` 可提供 `deps scan` 类命令从 import 面/构建清单生成白名单草案（含 test scope），把 DEP-01 的接入成本从小时降到分钟。
6. **P2 · 测试代码一等公民**：test scope 依赖、测试目录的度量基线（测试函数长/参数多属正常）应内建，而不是靠 exclude 或手工豁免。

## 5. 复审清单（供明早决策）

- [ ] 认可 4 篇 biz-spec 文档作为"输入物料"的形态（团队规范 v1.0 的模拟是否贴近真实团队文档）
- [ ] 认可 conversion-map 的分级结论与 6 条教训 → 决定 P0 是否立项（自定义规则 DSL）
- [ ] 真实项目的 563 error 是否需要给 scan-portal 团队看（建议：先给 §3.1 的 9 条 banned/架构/循环，度量面后置）
- [ ] SimpleDateFormat 22 处、function-clone 72 处的观察期定级
- [ ] policy-pilot-java.yaml 是否转正为该团队的 policy v1.0

---
*所有产物在 /Users/mac/Downloads/metricGate 下；目标项目全程只读未修改。检查命令与结果可复现（见 §2）。*
