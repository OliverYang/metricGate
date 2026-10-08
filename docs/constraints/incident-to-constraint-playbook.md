# 事故报告 → 约束规则 Playbook（T1a 一期）

> 定位：[`constraint-conversion-sop.md`](constraint-conversion-sop.md)（人工规约输入）的姊妹篇——
> 输入源换成**事故复盘报告**。设计出处：[`../design/constraint-pipeline-design.md`](../design/constraint-pipeline-design.md) §4 T1a。
> 输入模板：[`templates/constraints/incident-template.md`](../../templates/constraints/incident-template.md)。

## 为什么事故报告值得单独一条流程

相对人工规约，事故输入有两个结构性优势：

1. **自带 fixture**：触发代码模式就是现成烟测反例，SOP 第 4 步"fixture 烟测"的验证成本最低——
   零命中规则不可信的问题在这里天然消解；
2. **自带优先级**：定级 × 频次回答了人工手册答不了的"哪条值得建"——不需要从 40 条条款里猜。

## 流程（SOP 的六步映射）

| 步 | 动作 | 产物 | 对应 SOP |
|---|---|---|---|
| ① 结构化 | 从复盘叙述提取八字段（模板；自由文本由 LLM 提取+人确认） | 事故卡 | S1 提取条款化 |
| ② 条款化 | 根因→候选条款，**单卡 ≤3 条**；只收机械可判定的触发模式 | 候选条款 | S1 |
| ③ 三档转化+查重 | 对原语词汇表分档 ✅/⚠️/❌；先查 policy 已有规则（命中即"复现确认"，不新建） | 档位判定 | S2-S3 |
| ④ fixture 烟测 | **事故反例即 fixture**：断言命中 + error 分级 | 烟测证据 | S4 |
| ⑤ 优先级+落地 | 定级×频次排序；写入 policy（reason 必须引事故卡编号）+ conversion-map 增行标 `incident:` | 规则+台账 | S3/S5 |
| ⑥ 接入闭环 | digest 红线节 + check 门禁自动生效 | 闭环 | S6 |

分级纪律（用户已确认）：**fixture 通过即 error**——事故背书（真实发生过）+ fixture 背书（已知违例命中）
双保险下不需要观察期；⚠️/❌ 档自然进不了 error。例外：⚠️ 近似档仍按 SOP 观察期（warning 起步）。

---

## 试点记录（2026-09-29，4 例 × 三档全覆盖）

试点集 = biz-spec 分册内嵌的 3 个真实事故 + 1 例自由文本事故（非标兼容性验证）。

### INC 复现：doc-03 Full GC 风暴 → CONC-01（✅ 复现确认，对照组）

- **输入**：biz-spec-03 §1 背景（"Executors.newFixedThreadPool 无界队列引发 Full GC 风暴，2025-11"）；
- **流程**：结构化 → 条款"禁 Executors 工厂建线程池" → 查重命中 policy 已有
  `banned-Executors-new*` + import 兜底 → 复用 fixture 反例 `Executors.newFixedThreadPool(8)`
  烟测 error 命中（/tmp/incident-fixture，2026-09-29 验证）→ 复现确认，不新建；
- **价值**：证明流程能从纯叙述复现出已验证规则——流程保真度对照。

### INC-2026-001 parallelStream 压测雪崩 → CONC-07（✅ 新增落地）

- **输入**：自由文本事故（无模板、无规约章节，验证非标兼容）；
- **结构化**：现象=压测中多定时任务吞吐互相拖垮；根因=`list.parallelStream()` 共享 JVM 公共
  ForkJoinPool，一个阻塞任务占满公共池；定级 P1×压测可复现；
- **转化查重**：policy 无先例 → 新规则；原语判定 = call ban **0 段 receiver 裸方法名**
  `parallelStream`（banned.go javaCallBan case 0：`method_invocation name: (identifier)`，可匹配
  任意变量的 `.parallelStream()`）→ ✅ 现有原语可检；
- **fixture 烟测**：反例 `taskList.parallelStream().forEach(...)` →
  `banned-parallelStream error` 命中 ✅（/tmp/incident-fixture，IncidentFixture.java L8）；
- **真实项目试跑**：gitee-scan-2.0-portal 命中 **2 处**（TaskPlaceOnFileJob.java L149/L372，
  均为定时归档任务批处理走公共 ForkJoinPool——**正是事故模式本身，0 误报**）；
- **落地**：`demo/policy-pilot-java.yaml` 新增 CONC-07（call ban + severity error），
  reason 引用 INC-2026-001；conversion-map 增行标 `incident:` 来源。

### INC-2025-06 ELK 日志缺失 → LOG-01/02/03（⚠️ 已有近似覆盖，观察期维持）

- **输入**：biz-spec-02 §1 背景（"System.out 与 printStackTrace 不进日志系统，ELK 无迹可寻，2025-06"）；
- **判定**：根因条款化即 LOG-01/02/03，policy 已覆盖（System.out/err → error、print-stack-trace →
  error、log4j/logback/JUL import → error）——真实项目命中 1+44+0 处；精确语义"必须带 traceId"
  需参数/数据流感知（T2 缺口方向），已有近似档足够拦截事故模式；
- **处置**：复现确认 + 观察期维持；traceId 精确语义进缺口台账，复评触发 = T2 DSL 参数感知原语落地。

### INC-2025-isDelete 序列化反转 → NAMING-05（❌ 缺口，台账+复评触发）

- **输入**：biz-spec-01 §1 案例（"`isDelete` 序列化反转导致筛选条件全量失效"）；
- **判定**：条款"POJO 布尔字段不加 is 前缀"需**字段类型+命名联合检查**（conversion-map 已标 ❌），
  现有 call/import ban 原语无法表达字段声明级检查；
- **处置**：进缺口台账（第 5 个缺口：字段声明级检查）；复评触发 = T2 DSL 字段原语落地时自动复评；
  短期兜底 = CR 把关 + RPC 序列化框架侧对齐（事故卡"现有防回归手段"栏如实记录）。

## 试点结论

- 流程在 4/4 输入上产出明确处置（2 复现确认 / 1 新增 error 规则 / 1 缺口台账），**无死账**；
- 自由文本事故（无模板）走通——LLM 提取八字段+人确认路径成立，模板是加速器不是门槛；
- 验收契约达成（按修订后的三档口径，见 pipeline-design §4）：
  ✅≥1 条落地（CONC-07 新增）/ ⚠️ 给出近似档+观察期（ELK）/ ❌ 进台账带复评触发（isDelete）。

## 陷阱清单（事故来源特有，SOP 陷阱之外）

1. **单卡 ≤3 条款**：事故后情绪化加规则是最常见的 FP 爆炸源——只条款化"机械可判定的触发模式"，
   流程性/沟通性问题（复盘、值班）不进原语层；
2. **reason 必须引事故卡编号**：没有出处的规则在豁免审计时无法判定"能不能放"；
3. **0 段 receiver 的误伤面**：裸方法名 ban 全仓生效——落地前必须真实项目试跑 + FP 审计
   （CONC-07 靠 2/2 命中即事故模式本身确认安全；若命中里有正当用法，降级观察期而不是硬上 error）；
4. **正例也要写（已落地为 `use:` 字段）**：ban 规则的合规替代写法现在有专用字段
   `use:`——findings 消息和 digest 红线节自动带上“替代写法：…”（HOW to comply，U04 的教训——
   只说禁什么不说怎么修，修复成本 670K+）。事故卡的“修复方式（正例）”栏直接填进 `use:`。
