# 非标规范 → 约束规则转换 SOP（标准作业程序 v1）

> 本文固化"团队非标规范 → 可执行约束 → 消费闭环"的转换流程。方法论在两次实测中验证：
> Java 试点（[`../pilot/conversion-map.md`](../pilot/conversion-map.md)：40 条逐条转化，✅15/⚠️5/❌20 + 6 条工程教训）
> 和 R3 前置接地实验（[`../evaluations/evaluation-r3-grounding.md`](../evaluations/evaluation-r3-grounding.md)：digest 消费侧把首过违规 31.8%→4.5%）。
> 人类+AI 辅助的定位出自 [`constraint-mvp-design.md`](constraint-mvp-design.md)：不追求全自动转换，追求**可审计、可复现、有验证闭环**的转换。

## 适用输入（非标形态兼容）

SOP 的输入不要求格式统一，实测覆盖过的形态：人工手册章节（阿里黄山版）、团队自写规范文档
（biz-spec-01~04 的条款表 + 反例/正例）、口头约定与事故复盘沉淀（isDelete 序列化事故、Full GC 风暴）、
以及论文/外部标准。任何"能陈述成 [条件 → 要求]"的规范条目都可进入流程。

## 流程五步

### 第 1 步：提取（Extract）— 条款化
- 从源文档抽取**原子条款**：一条 = 一个编号 + 一个可判定要求 + 优先级（强制/推荐）。
- 每条标注来源引用（手册章节 / 事故编号），进 policy 的 `reason` 字段——溯源是硬要求。
- 产出：条款清单（分册保持原始编号体系，如 NAMING-01、EXC-03）。
- 工具面（2026-09-30 落地）：`metricgate constraints extract -file <规范文档> -lang go|java -config .metricgate.yaml` 自动条款化+三档判型+查重，输出候选 YAML 供人工复核后进 policy；要求半句（必须/统一使用…）不会误当禁令目标（required 切分），语义精确型条款诚实分诊 ⚠️ 而非硬转。

### 第 2 步：转化（Translate）— 三档分类
逐条判定可检性并归类，**如实标注，不许为了覆盖率硬转**：

| 档 | 判定标准 | 动作 |
|---|---|---|
| ✅ 直接落地 | 现有规则类型可表达：禁 API/import（A）、分层/禁止方向（B）、度量阈值（C）、克隆（D） | 写入 .metricgate.yaml，`reason` 带来源 |
| ⚠️ 近似 | 需要参数感知/数据流/自定义 DSL 才能精确；手写规则只能近似（参数感知 args_ban、构造器禁令 kind: constructor、静默 catch 内置 silent-catch 均已落地 2026-09-30，见 constraint-pipeline-design.md 缺口2） | 先用近似规则落地 + 在转换对照表记"近似"与已知偏差，进 DSL 待办 |
| ❌ 不可检 | 命名风格、语义正确性、跨文件业务一致性等静态不可判 | 明确记"未转化"，指派给外部适配器（PMD/SpotBugs ingest）或 CR，**不写进 policy** |

关键纪律（来自 Java 试点教训）：类别 A 的 call 禁令默认是**参数不感知**的——禁 `MessageDigest.getInstance` 会误伤合法 SHA-256。
2026-09-30 起参数感知已落地：call ban 可带 `args_ban: ["MD5","MD2"]`，仅当字符串字面量参数命中禁值才触发（见 constraint-pipeline-design.md 缺口2）；无参数语义的条款仍宁标 ⚠️，不做粗粒度禁令。

### 第 3 步：烟测（Fixture Smoke）— 零命中规则不可信
**每条新规则先对已知违例的 fixture 文件验证会命中、会分级**，再上真实项目。
这是 Java 试点最大的工程教训：一条零命中的规则无法区分"代码干净"和"规则静默失效"。
已知引擎语义陷阱（转化时必查，详见 conversion-map 的 6 条教训）：
- banned RuleID 的 `.` 和 `/` 都会被替换为 `-`（`banned-System-out-print*`，点号键静默失配）；
- Java 构造调用是 object_creation 不是 method_invocation（call 禁令抓不到 `new X()`）——2026-09-30 起用 `kind: constructor` 直接禁实例化点（任意类名拼写含 FQN scoped type，尾随 `*` 前缀匹配，T2 原语#2/#3）；泛型构造 `new Foo<T>()` 仍不可检，配对 import 禁令兼底；
- FQN 内联调用逃逸 call 禁令——2026-09-30 原语#3 已关闭：call ban 按源码拼写匹配任意深度 receiver（折行拼写亦可比对）；双保险纪律保留，防 import 路径本身；
- metric 阈值是地板：policy 只能抬高不能降低内置默认。

### 第 4 步：试点（Pilot）— 真实项目 + FP 审计
- 对真实项目跑 `metricgate check -config <独立policy>`（目标仓只读；store 用独立配置关闭）。
- **每个 fire 都要归因**：真阳性（违规确有其事）/ 误报（policy 配置问题——修 policy 不修代码）/ 结构性误报（同一根因，出结构性修复建议）。
- 误报率目标是 policy 质量指标，不是放弃信号（Java 试点首轮 104 条 policy-FP，修 policy 后归零）。

### 第 5 步：消费（Consume）— 接进 LLM 闭环
转化的规则不是给人看的文档，是闭环的输入：
- **生成前**：`constraints digest` 按目标包裁剪注入（R3 实测 31.8%→4.5%）；
- **生成后**：check 闸门 + scorecard 判定，findings JSON 回灌修复（R2 实测 7/7 修复）；
- **演进**：违规聚类 → FP 审计 → policy 调优（rules-gaps 审计防漏、except_files 豁免须带 CR 理由防白名单腐化）；
  验收规约随失败类证据修订（[`llm-code-acceptance-spec-v1.md`](llm-code-acceptance-spec-v1.md) v1→v2）。

## 产出物清单（一次完整转换应留下）

1. 条款清单（带来源引用与优先级）；
2. 独立的 policy YAML（不动主 policy，试点隔离）；
3. 转换对照表（逐条：条款 → 规则 ID → 档位 ✅/⚠️/❌ → 已知偏差）；
4. fixture 烟测记录（每条规则 × 已知违例样本）；
5. 试点运行报告（fire 分布、FP 审计、结构性发现）；
6. 演进待办（DSL 需求清单、外部适配器清单）。

## 边界（诚实声明）

- 静态不可判定的条款（语义正确性）永远到不了 ✅——SOP 的价值是把这一边界**显式化**，而不是消灭它；
- ⚠️ 近似规则的偏差是已接受的债务，必须记入对照表并在演进环节复审；
- 转换质量的上限取决于源文档本身可判定化程度——含糊的规范（"代码要优雅"）在提取步就该被退回。

---
*撰写：2026-09-29。流程有效性证据：Java 试点（转换可行性）+ R2（修复回路 7/7）+ R3（接地预防 31.8%→4.5%）。*
