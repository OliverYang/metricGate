# 《阿里巴巴Java开发手册》调研笔记 —— 可静态检查性分析

> **目的**: 将《阿里巴巴Java开发手册》（黄山版，2022-02 最新版）作为"非标团队规范"的标准来源，
> 逐条分析哪些规则可以转化为 metricGate 的静态检查，为业务研发文档与 policy 规则集提供依据。
>
> **结论先行**: 手册 ~300 条规则中，**约 15-20% 可直接映射**到 metricGate 现有能力
> （banned-api / 内置规则 / 度量阈值 / 架构分层 / 依赖白名单），
> **约 30% 可近似或需要自定义 DSL**（metricGate 规划中的 tree-sitter query + Go 过滤器），
> **其余不可静态检查**（需要类型系统、数据流分析或业务语义）。
> 这正是本 MVP 要验证的边界：**转化率与转化质量**。

---

## 1. 手册概况

| 项目 | 内容 |
|---|---|
| 名称 | 《阿里巴巴Java开发手册》（Alibaba Java Coding Guidelines） |
| 版本 | 黄山版（2022-02，最新公开版）；前序：嵩山版(2020-08)、华山版、泰山版… |
| 维度 | 编程规约、异常日志、单元测试、安全规约、MySQL数据库、工程结构、设计规约 |
| 级别 | **强制**（必须遵守，违反即发布风险）/ **推荐**（尽量遵守）/ **参考**（倡导性） |
| 配套 | P3C 项目（PMD 插件 + IDE 插件）已实现其中一部分的自动检查 |
| 定位 | 阿里集团一线实战经验总结，Java 团队"非标规范"的事实标准样板 |

**选它的原因**：它是中文世界最普及、最贴近真实团队的非标研发规范——
"MVP 流程验证"拿它当输入物料，等价于模拟"一个新团队带着现成规范文档接入 metricGate"的真实场景。

---

## 2. metricGate 现有 Java 能力清单（映射的基准）

| 能力 | 说明 | 对应手册条目的可用性 |
|---|---|---|
| `banned-api`（kind: call / import） | 按调用名/导入路径禁用，尾部 `*` 前缀匹配，`except_files` 豁免 | ⭐ 主要转化通道 |
| 内置规则 `print-stack-trace` | `e.printStackTrace()` 检查 | ⭐ 直接对应 |
| 内置规则 `empty-block` | 空控制流块 | ⭐ 对应手册控制语句类 |
| 内置规则 `unknown-dependency` | 未声明依赖检查（`dependencies.allow/self` 白名单） | ⭐ 对应工程结构类 |
| 度量阈值 | cyclomatic / cognitive / function-length / file-length / nesting-depth / params-count + CK 家族（wmc/nom/lcom/cbo/dit） | ⭐ 对应"可读性/复杂度"类精神 |
| 架构分层 `architecture.layers/forbid/cycles/sdp` | 包依赖方向、循环、禁止边 | ⭐ 对应工程结构分层（metricGate 强项） |
| clone 检测 | Type-2 函数克隆 | 间接相关（可读性/复用） |
| **缺失**：自定义规则 DSL | 规划中（tree-sitter query + Go 过滤器），未实现 | ⚠️ 制约大量"近似可查"条目 |

---

## 3. 逐条分析：可转化为静态检查的规范条目

> 级别列 = 手册原文级别；可检查性分三档：
> **✅ 直接映射**（现有能力可查，零代码）／ **⚠️ 近似或需 DSL**（现有能力可近似，或等自定义规则 DSL）／ **❌ 不可查**（需类型系统/数据流/业务语义）。
> 优先级 = MVP 规则集的采纳建议（P0 本轮必做 / P1 转化对照表内标注 / P2 仅记录）。

### 3.1 编程规约 —— 命名风格

| # | 手册条目（要点） | 级别 | 可检查性 | 映射方式 / 缺口 | 优先级 |
|---|---|---|---|---|---|
| N1 | 类名 UpperCamelCase | 强制 | ❌ | 无命名规则引擎；需 DSL 比对标识符形态 | P2 |
| N2 | 方法名/变量名 lowerCamelCase | 强制 | ❌ | 同上 | P2 |
| N3 | 常量全大写、下划线分隔 | 强制 | ❌ | 同上 | P2 |
| N4 | 抽象类以 Abstract/Base 开头；异常类以 Exception 结尾；测试类以 Test 结尾 | 强制 | ❌ | 需 DSL（类名↔修饰符关联） | P2 |
| N5 | POJO 布尔变量不加 is 前缀（序列化坑） | 强制 | ❌ | 需类型信息区分 POJO | P2 |
| N6 | service/dao 方法名前缀约定（get 单个/list 多个/save/insert…） | 强制 | ❌ | 需 DSL + 作用域上下文 | P2 |

### 3.2 编程规约 —— 常量定义 / OOP / 集合

| # | 手册条目（要点） | 级别 | 可检查性 | 映射方式 / 缺口 | 优先级 |
|---|---|---|---|---|---|
| O1 | 不允许使用过时的类或方法（`@Deprecated`） | 强制 | ⚠️ | 需类型系统（外部适配器 golangci/PMD 的活） | P2 |
| O2 | `equals` 常量或确定值放前（避免 NPE） | 强制 | ⚠️ | 需 DSL：receiver 是否字面量 | P1(后) |
| O3 | 包装类比较用 `equals` 不用 `==`（Integer 缓存 -128~127） | 强制 | ⚠️ | 需 DSL：== 两边类型推断 | P1(后) |
| O4 | 集合初始化时指定容量大小 | 推荐 | ⚠️ | 需 DSL：new ArrayList 无参即报（近似，误报可接受） | P1(后) |
| O5 | 不要在 foreach 循环内 remove/add 元素，用 Iterator | 强制 | ⚠️ | 需 DSL：循环体内匹配 `.remove(`/`.add(` | P1(后) |
| O6 | 禁止使用 `printStackTrace()` | 强制 | ✅ | **内置规则 `print-stack-trace`**，零成本 | **P0** |

### 3.3 编程规约 —— 并发处理（转化率最高的一章）

| # | 手册条目（要点） | 级别 | 可检查性 | 映射方式 / 缺口 | 优先级 |
|---|---|---|---|---|---|
| C1 | **线程池不允许用 Executors 创建**（`newFixedThreadPool` 等队列无界/线程数无界 → OOM），必须手动 `new ThreadPoolExecutor(...)` | 强制 | ✅ | `banned-api call: Executors.new*`（手册原文点名四个方法，`*` 前缀匹配全覆盖，语义无损） | **P0** |
| C2 | `SimpleDateFormat` 线程不安全，不要定义为 static 共享实例 | 强制 | ⚠️→✅(近似) | 精确版需知道是否共享；**MVP 粒度**：`banned-api call/import: SimpleDateFormat` 整体禁用（引导用 DateTimeFormatter），误报可接受、语义偏保守 | **P0** |
| C3 | 多线程下避免 `Math.random()`，用 `ThreadLocalRandom` | 推荐 | ✅ | `banned-api call: Math.random` | **P0** |
| C4 | ThreadLocal 用完必须 `remove()`（内存泄漏） | 强制 | ❌ | 需数据流（配对分析） | P2 |
| C5 | 多线程并发修改 HashMap 需用 ConcurrentHashMap | 强制 | ⚠️ | 需上下文（是否多线程共享）；DSL 可近似"static final HashMap"模式 | P1(后) |
| C6 | `Thread` 不建议直接创建（用线程池） | 参考 | ⚠️ | `banned-api call: new Thread`（近似，测试代码需豁免） | P1 |

### 3.4 编程规约 —— 控制语句 / 注释

| # | 手册条目（要点） | 级别 | 可检查性 | 映射方式 / 缺口 | 优先级 |
|---|---|---|---|---|---|
| F1 | switch 必须带 default | 强制 | ⚠️ | 需 DSL（switch 节点子节点检查） | P1(后) |
| F2 | if/else/for/while 单行不可省略大括号 | 强制 | ⚠️ | 内置 `empty-block` 只覆盖"空块"子集；其余需 DSL | P1(后) |
| F3 | 循环内避免深度嵌套/复杂逻辑 | 推荐 | ✅(近似) | `nesting-depth` 阈值（如 warn 3 / error 5） | **P0** |
| F4 | 注释规约（类必须 Javadoc、抽象方法必须有注释…） | 强制 | ❌→⚠️ | `todo-comment` 只查 TODO/FIXME；Javadoc 缺失检查需 DSL | P2 |

### 3.5 异常日志（转化率第二高）

| # | 手册条目（要点） | 级别 | 可检查性 | 映射方式 / 缺口 | 优先级 |
|---|---|---|---|---|---|
| L1 | **禁止 `System.out` / `System.err` 直接输出**，统一日志框架 | 强制 | ✅ | `banned-api call: System.out.print*` / `System.err.print*`（手册原文点名，前缀匹配语义无损） | **P0** |
| L2 | **应用内不可直接使用日志系统 API**（Log4j/Logback），必须走 SLF4J 门面 | 强制 | ✅ | `banned-api import: org.apache.log4j` / `ch.qos.logback` / `java.util.logging`（引导统一门面） | **P0** |
| L3 | 异常不要用来做流程控制/条件控制 | 强制 | ❌ | 需语义分析 | P2 |
| L4 | 不要捕获 `Exception`/`RuntimeException` 大而化之吞掉；catch 需处理或抛出 | 强制 | ⚠️ | 需 DSL：catch 参数类型匹配 | P1(后) |
| L5 | finally 中不能 `return`/`continue`/`break` | 强制 | ⚠️ | 需 DSL（finally 块内 return 匹配） | P1(后) |
| L6 | 空控制流块（catch 后什么都不做） | 强制(精神) | ✅ | **内置规则 `empty-block`** | **P0** |
| L7 | 方法返回值可以忽略时必须注释说明 / 不要吞返回值 | 推荐 | ❌ | 同 Go 版 require-error-handling 的难点 | P2 |

### 3.6 单元测试 / 安全规约 / MySQL

| # | 手册条目（要点） | 级别 | 可检查性 | 映射方式 / 缺口 | 优先级 |
|---|---|---|---|---|---|
| S1 | 用户敏感数据（手机号/身份证）必须脱敏展示 | 强制 | ❌ | 业务语义，不可静态判定 | P2 |
| S2 | MyBatis 用 `#{}` 不用 `${}`（SQL 注入） | 强制 | ❌ | Mapper XML 不在解析范围；Java 注解 SQL 字符串可做字符串匹配但误报高 | P2 |
| S3 | 密码必须加盐哈希存储；禁用 MD5 做安全用途 | 强制 | ✅(近似) | `banned-api call/import: MessageDigest.getInstance("MD5")`、`java.security.MessageDigest`（近似：MD5 也有非安全用途） | P1 |
| S4 | 单元测试覆盖度/断言要求 | 强制 | ❌ | 需覆盖率工具（外部适配器） | P2 |
| S5 | 避免 `select *` | 参考 | ⚠️ | 同 S2，字符串匹配不可靠 | P2 |

### 3.7 工程结构 / 设计规约（metricGate 强项，全量可查）

| # | 手册条目（要点） | 级别 | 可检查性 | 映射方式 / 缺口 | 优先级 |
|---|---|---|---|---|---|
| A1 | **分层架构**：web/controller → service → manager → dao，禁止跨层/逆向依赖 | 强制(精神) | ✅ | `architecture.layers` + `forbid`（**手册是"参考"描述，团队落地时应升为强制**） | **P0** |
| A2 | 禁止循环依赖（包级） | 强制(精神) | ✅ | 内置 `import-cycle`（Tarjan SCC） | **P0** |
| A3 | 不允许引入未声明的第三方依赖（幻觉包/私服外的包） | 强制 | ✅ | 内置 `unknown-dependency` + `dependencies.allow`（需从 pom.xml 提取真实依赖清单） | **P0** |
| A4 | 稳定依赖原则（稳定的包不要依赖易变的包） | 参考 | ✅ | 内置 `sdp-violation` | P1 |
| A5 | 避免重复代码（DRY） | 推荐(精神) | ✅ | `clones.enabled`（Type-2 函数克隆） | P1 |

### 3.8 度量类（手册无明确数值，属工程实践补充，体现"规范可量化"）

| # | 条目（要点） | 级别 | 可检查性 | 映射方式 | 优先级 |
|---|---|---|---|---|---|
| M1 | 单个方法总行数不超过 80 行 | 参考 | ✅ | `function-length` warn 80 / error 160 | **P0** |
| M2 | 单个文件过长（可读性） | 参考 | ✅ | `file-length` warn 1000 / error 2000（Java 类普遍偏长，阈值放宽） | P1 |
| M3 | 方法入参个数尽量 ≤ 5，超出用 DTO 合并 | 参考 | ✅ | `params-count` warn 5 / error 8（⚠️ 条文出处需核对原文，按推荐级采纳） | **P0** |
| M4 | 类复杂度（CK 家族：wmc/cbo/dit…） | 参考 | ✅ | `wmc` warn 40 / `cbo` warn 15 / `dit` warn 5（默认值已接近工程共识） | P1 |

---

## 4. 转化率统计（按上表）

| 档位 | 条目数 | 占比（本表 43 条抽样） | 说明 |
|---|---|---|---|
| ✅ 直接映射 | **17** | ~40% | banned-api×8、内置规则×3、架构/依赖×3（含度量） |
| ⚠️ 近似或需 DSL | 13 | ~30% | 等自定义规则 DSL 或接受近似语义 |
| ❌ 不可查 | 13 | ~30% | 需类型系统/数据流/业务语义（诚实边界，走外部适配器或不做） |

> **口径说明**：43 条是"代表性抽样"（覆盖手册七大维度中每章的高频条目），不是全量 300 条统计。
> 抽样里 ✅ 占比被"并发/异常日志/工程结构"三个转化率高的章节拉高；
> 命名风格整章（~15 条）几乎全军覆没，全量统计下 ✅ 占比会显著低于 41%。

---

## 5. 给 MVP 的直接结论

1. **P0 规则集（10 条，全部 ✅ 零代码可落地）**：
   `Executors.new*`（线程池）、`SimpleDateFormat`（近似）、`Math.random`、
   `System.out.print*` / `System.err.print*`、log4j/logback import 禁用、
   `print-stack-trace`（内置）、`empty-block`（内置）、`unknown-dependency`（内置），
   加上架构三件套 `layers/forbid/import-cycle` 与度量三件套 `function-length/params-count/nesting-depth`。
2. **⚠️ 档是产品路线图**：switch-default、foreach-remove、catch-Exception、finally-return、
   命名前缀等 9 条"高频告警点"需要**自定义规则 DSL**（tree-sitter query + Go 过滤器）才能落地——
   这是调研给出的最有力的"下一个功能优先级"证据。
3. **❌ 档是诚实边界**：脱敏、SQL 注入、ThreadLocal remove、测试覆盖率 → 计划内走外部适配器（PMD/SpotBugs ingest），
   文档里明说"metricGate 不做"，与 capabilities.md §11 的诚实清单一致。
4. **验证方式**：把 P0 规则集写进独立 policy YAML（demo/policy-pilot-java.yaml），
   对真实项目 gitee-scan-2.0-portal 跑 `check`，用真实命中数据回答"这套转化有没有用"。

---

*来源：《阿里巴巴Java开发手册》黄山版（alibaba.github.io/p3c / 官方 PDF）；能力基准：metricGate docs/capabilities.md（2026-09-28 版）*
*调研日期：2026-09-29（夜间自主执行，task-1 产出）*
