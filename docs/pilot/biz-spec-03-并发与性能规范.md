# 扫描门户团队研发规范 03 —— 并发与性能规范

> **文档信息**: scan-portal-business 团队研发规范 v1.0（2026-09）
> **来源**: 依据《阿里巴巴Java开发手册》（黄山版）裁剪，结合本团队压测与 OOM 复盘修订。
> **用途**: 团队"非标规范"正式输入物料，交由 metricGate 转化为可执行检查
> （转化对照见 `docs/pilot/conversion-map.md`；落地规则集见 `demo/policy-pilot-java.yaml`）。

## 1. 目的与适用范围

约束多线程与资源使用方式。背景：扫描门户存在大量批处理与定时任务，
历史上因 `Executors.newFixedThreadPool` 无界队列引发过一次 Full GC 风暴（2025-11）。
适用于 scan-portal-business 全部 Java 源码。

## 2. 并发规约

| 编号 | 级别 | 条款 |
|---|---|---|
| CONC-01 | 强制 | **线程池不允许使用 Executors 去创建**（`newFixedThreadPool`/`newSingleThreadExecutor` 队列无界堆积请求 → OOM；`newCachedThreadPool`/`newScheduledThreadPool` 线程数无界 → OOM），必须通过 `new ThreadPoolExecutor(...)` 手动创建，明确 corePoolSize / maximumPoolSize / workQueue / handler |
| CONC-02 | 强制 | **`SimpleDateFormat` 是线程不安全的类**，不允许定义为 `static` 共享实例；统一使用 `DateTimeFormatter`（JDK8+，线程安全） |
| CONC-03 | 强制（推荐级来源） | 多线程场景避免使用 `Math.random()`（单例 Random CAS 竞争），使用 `ThreadLocalRandom.current().nextXxx()` |
| CONC-04 | 强制 | `ThreadLocal` 对象使用完毕必须显式调用 `remove()`，防止内存泄漏与脏数据 |
| CONC-05 | 强制 | 多线程并发修改共享 HashMap 需使用 `ConcurrentHashMap`（HashMap 并发扩容可能死循环） |
| CONC-06 | 推荐 | 减少不必要的锁粒度与锁持有时间；能用无锁数据结构不用锁 |

**反例**（CONC-01）:
```java
// 反例：无界队列，任务堆积即 OOM
ExecutorService pool = Executors.newFixedThreadPool(8);
```
```java
// 正例：显式定义队列与拒绝策略
ExecutorService pool = new ThreadPoolExecutor(
    8, 16, 60, TimeUnit.SECONDS,
    new ArrayBlockingQueue<>(1024),
    new ThreadPoolExecutor.CallerRunsPolicy());
```

## 3. 性能与可读性量化基线

团队在手册基础上补充了量化阈值（压测结论 + CR 共识），作为工具化基线：

| 编号 | 级别 | 条款 |
|---|---|---|
| PERF-01 | 参考 | 单个方法总行数不超过 **80 行**（超过说明职责过多，需拆分） |
| PERF-02 | 参考 | 方法入参个数不超过 **5 个**，超出需用 DTO/参数对象合并 |
| PERF-03 | 参考 | 控制流嵌套深度不超过 **3 层**（循环内复杂逻辑必须提取方法） |
| PERF-04 | 参考 | 方法圈复杂度不超过 **15**（决策点过多需拆分） |
| PERF-05 | 推荐 | 集合初始化时指定容量大小（避免频繁扩容），如 `new HashMap<>(expectedSize)` |

## 4. 团队工具化诉求（期望自动检查）

- **可自动化**（团队预期工具能查）: CONC-01、CONC-02、CONC-03、PERF-01~04
- **部分可自动化**: CONC-05（需判断共享上下文，先按"近似"落地再观察误报）
- **暂不自动化**: CONC-04（需数据流配对分析）、CONC-06、PERF-05（接受 CR 把关）

---
*依据: 《阿里巴巴Java开发手册》黄山版 §编程规约-并发处理；本团队 2025-11 Full GC 风暴复盘与 2026-03 压测基线。*
