# 业务规范 → metricGate policy 转化对照表（pilot v1.0）

- 输入物料：`docs/pilot/biz-spec-01~04`（模拟 scan-portal 团队研发规范，40 条）
- 输出物：`demo/policy-pilot-java.yaml`
- 目标项目：`/Users/mac/Downloads/main/gitee-scan-2.0-portal`（只读）
- 转化状态：✅ 直接落地 / ⚠️ 近似或部分 / ❌ 本轮未转化
- 命中数据来源：`./metricgate check -config demo/policy-pilot-java.yaml <项目>`（563 error / 523 warning，30534 snapshots，1.2s）

## 01 命名与注释

| 条款 | 级别 | policy 位置 | 状态 | 说明 |
|---|---|---|---|---|
| NAMING-01 类名 UpperCamelCase | 强制 | — | ❌ | 需标识符风格检查器，内置无（自定义 DSL 路线图） |
| NAMING-02 lowerCamelCase | 强制 | — | ❌ | 同上 |
| NAMING-03 常量全大写 | 强制 | — | ❌ | 同上 |
| NAMING-04 Abstract/Base/Exception/Test 词缀 | 强制 | — | ❌ | AST 可查但需自定义命名规则（路线图） |
| NAMING-05 POJO 布尔不加 is 前缀 | 强制 | — | ❌ | 需字段类型+命名联合检查 |
| NAMING-06 getXxx/listXxx/saveXxx 前缀 | 强制 | — | ❌ | 需"所在层+方法名模式"联合规则 |
| COMMENT-01 Javadoc 规范 | 强制 | — | ❌ | 无 Javadoc 检查器 |
| COMMENT-02 抽象方法 Javadoc | 推荐 | — | ❌ | 同上 |
| COMMENT-03 TODO 带归属人/时间 | 强制 | `todo-comment: enabled:false` | ⚠️ | 内置 todo-comment 只查有无，语义不满足 → 试点显式关闭，留路线图 |
| COMMENT-04 中文注释质量 | 推荐 | — | ❌ | 语义级，不可自动化 |

## 02 异常与日志

| 条款 | 级别 | policy 位置 | 状态 | 说明 / 真实项目命中 |
|---|---|---|---|---|
| LOG-01 禁 System.out/err | 强制 | banned `System.out.print*` / `System.err.print*` → error | ✅ | 1 处命中 |
| LOG-02 禁 printStackTrace | 强制 | 内置 `print-stack-trace` → error | ✅ | 44 处命中 |
| LOG-03 禁直用日志框架 API | 强制 | import ban：log4j2 / logback / log4j1 / JUL → error | ✅ | 0 处（该项目日志面干净） |
| LOG-04 日志占位符 `{}` | 推荐 | — | ❌ | 需字符串拼接参数检测（部分可查，未转化） |
| LOG-05 禁敏感信息明文 | 强制 | — | ❌ | 需数据流/词典，外部适配器 |
| EXC-01 禁空 catch 块（吞异常，至少记录日志或向上抛出） | 强制 | 空块：内置 `empty-block`（catch/if/for/while）→ error；非空但未处理：内置 `silent-catch`（2026-09-30 原语#4）→ warning 观察期 | ✅ | empty-block 1 处（TaskPlaceOnFileJob）；silent-catch 44 处（FP 审计：抽样 12 处中 9 处解析探测/降级惯用法、3 处真吞异常，见教训 7；观察期逐条归因后定档） |
| EXC-02 禁捕 Exception 大而化之 | 推荐 | — | ⚠️ | 需 catch 参数类型规则，内置无，路线图 |
| EXC-03 禁 finally return | 强制 | — | ❌ | 需自定义 DSL |
| EXC-04 异常不做流程控制 | 强制 | — | ❌ | 语义级 |
| EXC-05 事务必须回滚 | 强制 | — | ❌ | 需 Spring 事务语义，外部适配器 |
| EXC-06 返回 Optional 建议 | 推荐 | — | ❌ | 语义级 |

## 03 并发与性能

| 条款 | 级别 | policy 位置 | 状态 | 说明 / 真实项目命中 |
|---|---|---|---|---|
| CONC-01 禁 Executors 建线程池 | 强制 | call ban `Executors.new*` + import ban `java.util.concurrent.Executors`（兜底）→ error | ✅ | 4+4 处命中 |
| CONC-02 SimpleDateFormat 线程不安全 | 强制 | import ban `java.text.SimpleDateFormat` + **构造器禁令** `kind: constructor`（T2 原语#2）→ 均 warning 观察期 | ⚠️ | 精确语义是"禁 static 共享实例"（字段存储级），仍待字段原语；构造器禁令后实例化点单独可检：import 22 处 + 构造 69 处（抽样全真，含 PlanDTO `static final SimpleDateFormat` 旗舰案例）；本仓 FQN 内联逃逸 0 处 |
| CONC-03 禁 Math.random | 强制（推荐级来源） | call ban `Math.random` → warning | ✅ | 0 处命中 |
| CONC-04 ThreadLocal 必须 remove | 强制 | — | ❌ | 需数据流分析 |
| CONC-05 共享 HashMap→ConcurrentHashMap | 强制 | — | ❌ | 需类型+数据流 |
| CONC-06 锁粒度 | 推荐 | — | ❌ | 语义级 |
| CONC-07 禁 parallelStream | 强制（事故来源） | call ban `parallelStream`（0 段 receiver 裸方法名）→ error | ✅ `incident:` | 2 处命中 0 误报，均为定时任务批处理走公共 ForkJoinPool；来源 INC-2026-001，流程见 incident-to-constraint-playbook |
| PERF-01 方法 ≤80 行 | 参考 | `function-length: warn 80` | ✅ | warn-only 报告 33 处；内置默认 error 100 仍在（见"教训 3"），47 处 |
| PERF-02 入参 ≤5 | 参考 | `params-count: warn 5` | ✅ | warn 88 / error(内置 8) 15 |
| PERF-03 嵌套 ≤3 层 | 参考 | `nesting-depth: warn 3, error 6` | ✅ | 6 层红线为试点补充硬线：warn 127 / error 11 |
| PERF-04 圈复杂度 ≤15 | 参考 | `cyclomatic-complexity: warn 15` | ✅ | 内置默认 error=15 同值保留 → >15 即 error（75 处）；文档"仅报告"与内置红线有偏差，如实保留 |
| PERF-05 集合指定容量 | 推荐 | — | ❌ | 需类型信息，未转化 |

## 04 工程分层与依赖

| 条款 | 级别 | policy 位置 | 状态 | 说明 / 真实项目命中 |
|---|---|---|---|---|
| ARCH-01 分层单向 controller→server→dao | 强制 | architecture.layers may_depend_on → error | ✅ | 132 处；其中 77 处是 interceptor.BaseUserDetails/LoginContextHolder 被 server 层引用（修复=类搬家到 common/context），19 处 controller 直连 dao 是真违规 |
| ARCH-02 禁包循环 | 强制 | `cycles: error` | ✅ | 1 条真实包循环（annotation/valid ↔ business ↔ config ↔ converter） |
| ARCH-03 common 不依赖业务层 | 强制 | common 层 may_depend_on: [common, pojo] | ✅ | 首轮 59 条 common→pojo 误报 → 对齐文档语义修正（教训 5）；现余 common→service/web/dao 约 15 处（config 装配类引用业务 Bean） |
| ARCH-04 SDP 稳定依赖 | 参考 | `sdp: warning` | ✅ | 16 处 |
| DEP-01 禁未声明依赖 | 强制 | `dependencies.allow` 白名单 + `unknown-dependency` → error | ✅ | 修正后 0 处（首轮 45 处为测试依赖漏配，教训 4） |
| DEP-02 依赖评审流程 | 强制 | — | ❌ | 流程性条款，工具管不了评审本身 |
| DEP-03 克隆函数收敛 | 强制 | `clones.enabled`（默认开）→ warning | ⚠️ | 72 处 Type-2 克隆；"强制"语义但首轮只报告，阈值收敛后可升级 |
| SEC-01 禁 MD5/SHA-1 做密码 | 强制 | `banned-api` + `args_ban: ["MD5","MD2"]`（T2 原语#1 参数感知禁令，仅命中字面量参数）→ error | ✅ | 2 处（Md5Util.java L21/L55 均 "MD5" 字面量，0 误伤 SHA-256）；诚实边界：hutool `SecureUtil.md5`/`DigestUtil` 包装库调用在 ban 面之外，需库级 import ban 兼底 |

## 统计

40 条：✅ 直接落地 16 条 · ⚠️ 近似/部分 4 条 · ❌ 未转化 20 条（多为命名规约、异常/并发语义类，需要自定义 DSL、类型/数据流或业务语义，明细见各表）。与 task-1 调研笔记的可检查性分布（✅17/⚠️13/❌13，43 条抽样）方向一致。

> 2026-09-30 更新：SEC-01 由 ⚠️ 转 ✅——T2 原语#1 参数感知禁令落地（`args_ban` 字段，fixture 烟测 + 真实项目 2 处命中 0 误报，见 constraint-pipeline-design.md 缺口2）。
>
> 2026-09-30 更新（二）：EXC-01 第二通道落地——内置 `silent-catch` 规则（T2 原语#4，验收规约 LC-304 由 ❌ 转 ⚠️）：catch 块非空但**未引用异常参数**且无 throw 无日志才命中（首版「仅查 throw+日志」判据被 FP 审计反转，见教训 7）；真实项目 44 处命中走 warning 观察期。

## 试点过程沉淀的 7 条工程教训（工具能力 ↔ 规范语义的反馈环）

1. **编译规则 ID 的字符替换**：banned 规则 ID = `"banned-" + match`，其中 `.` 和 `/` 都替换为 `-`（`internal/rules/banned.go` RuleID）。按直觉写 `banned-System.out.print*` 查不到 severity 覆写，静默回落默认 warning。**必须写 `banned-System-out-print*`**。冒烟样本验证是唯一可靠的确认方式——首轮全量跑出 banned 全 0 命中，靠冒烟才定位是 severity 没生效而不是代码真干净。
2. **per-ban severity 覆写走 `languages.<lang>.rules.<ruleID>.severity`**；`banned-api-severity` 是语言级 string 兜底（一刀切），不能写 map（`internal/config/config.go` BanConfig 无 per-ban severity 字段）。
3. **度量的内置 error 阈值不可单独关闭**：`EffectiveThresholds` 中 policy 只能上调（`if er > 0`），`enabled:false` 会连 warn 一起关。文档"参考级仅报告"对有内置 error 的度量（cyclomatic 15 / cognitive 25 / file-length 800 等）无法完整表达——试点决定保留内置红线并在本表如实标注，这是"规范文档语义 vs 工具语义"的真实空隙，也是 policy 审计（rules-gaps）该暴露的一类问题。
4. **allow 清单必须覆盖 test scope**：JUnit/Mockito 是 pom 里 test scope，首轮 45 条 unknown-dependency 全是测试文件。依赖白名单的来源应该是"编译 scope + test scope"两段。
5. **分层误报的根因常常是"类放错包"而不是"依赖写错"**：77/132 的 layer-violation 是 `LoginContextHolder`/`BaseUserDetails` 放在 `interceptor` 包被全项目引用。工具的判定是对的（依赖方向确实存在），修复动作是类搬家到 common/context——这类"结构性修复建议"应当进报告而不是让团队改代码绕规则。
6. **call ban 的 receiver 深度边界**（2026-09-30 原语#3 已关闭）：旧实现只枚举 0/1/2 段 receiver，全限定名 `java.util.concurrent.Executors.newCachedThreadPool()` 逃逸（冒烟确认）。现按**源码拼写**匹配任意深度 receiver（`receiverMatch` 后缀规则：短 receiver 也命中其 FQN 形式，折行拼写去空白后仍可比对），构造器禁令同步支持 FQN scoped type（`new java.text.SimpleDateFormat()`）。Java 试点双保险纪律（call ban + import ban 配对）保留——它防的是 import 路径本身，与拼写匹配互补。
7. **规则的命中判据要被真实项目校准，而不是凭直觉定稿**（2026-09-30 原语#4）：silent-catch 首版判据「catch 块无 throw 且无日志即命中」在 fixture 上全绿，但真实项目 79 处命中中 35 处是 `catch (Exception e) { return ServerResponse.createByErrorMessage(e.getMessage()); }` 这类**异常消息流入业务响应**的主流模式——不是吞异常。判据反转为三信号：**未引用异常参数** 且 无 throw 且 无日志（引用参数 = 异常信息有去处）。两个推论：fixture 全绿 ≠ 判据正确，fixture 只能证明「能命中该命中的」，证明不了「不命中不该命中的」；消息文案也要跟着判据走（尾部建议曾写「comment why ignoring is safe」但注释不构成信号，会误导修复回路白忙一轮，已改为「log it, wrap and throw, or pass it to a handler」）。

## 豁免机制（文档附注约定 → policy）

- 强制条款的个别豁免：banned-api 的 `except_files`（文件名后缀列表）；分层违规暂无文件级豁免（路线图）。
- 推荐条款不设门禁：warning 不触发 `gate.fail_on: error`。
- 观察期条款：先 warning 跑真实数据，误报率确认后升级 error（当前观察期：CONC-02 SimpleDateFormat、CONC-03 Math.random、DEP-03 function-clone）。
