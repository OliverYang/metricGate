# 扫描门户团队研发规范 02 —— 异常与日志规范

> **文档信息**: scan-portal-business 团队研发规范 v1.0（2026-09）
> **来源**: 依据《阿里巴巴Java开发手册》（黄山版）裁剪，结合本团队故障复盘修订。
> **用途**: 团队"非标规范"正式输入物料，交由 metricGate 转化为可执行检查
> （转化对照见 `docs/pilot/conversion-map.md`；落地规则集见 `demo/policy-pilot-java.yaml`）。

## 1. 目的与适用范围

统一异常处理与日志输出方式。背景：排查线上问题时，控制台 `System.out` 输出与
`printStackTrace` 堆栈不进日志系统，导致 ELK 无迹可寻（2025-06 事故复盘）。
适用于 scan-portal-business 全部 Java 源码。

## 2. 日志规约

| 编号 | 级别 | 条款 |
|---|---|---|
| LOG-01 | 强制 | **应用中禁止直接使用 `System.out` / `System.err` 输出日志**，必须统一使用日志框架（SLF4J 门面） |
| LOG-02 | 强制 | **禁止使用 `e.printStackTrace()`**，必须以日志参数形式输出异常堆栈：`log.error("xxx", e)` |
| LOG-03 | 强制 | **应用内不可直接使用日志系统（Log4j、Logback）的 API**，必须通过 SLF4J 门面 `LoggerFactory.getLogger` 获取 Logger，便于统一配置与替换实现 |
| LOG-04 | 推荐 | 日志使用占位符 `{}`，避免字符串拼接（性能 + GC 压力） |
| LOG-05 | 强制 | 生产环境禁止输出用户敏感信息（手机号、身份证、密码）明文日志 |

**正例**（LOG-02/LOG-04）:
```java
log.error("scan task {} submit failed, userId={}", taskId, userId, e);
```

## 3. 异常规约

| 编号 | 级别 | 条款 |
|---|---|---|
| EXC-01 | 强制 | **catch 块不允许为空**（吞异常），至少记录日志或向上抛出说明 |
| EXC-02 | 推荐 | 不推荐捕获 `java.lang.Exception` / `java.lang.Throwable` 大而化之；应捕获具体异常类型 |
| EXC-03 | 强制 | **finally 块中禁止 `return`**（会吞掉 try/catch 的返回值与异常） |
| EXC-04 | 强制 | 异常不要用来做流程控制、条件控制（异常的处理效率远低于条件判断） |
| EXC-05 | 强制 | 事务场景中，捕获异常后必须回滚事务（手工回滚或声明式事务自动回滚），否则日志中必须说明保留原因 |
| EXC-06 | 推荐 | 返回值可以为 null，调用方需显式判空；建议返回 `Optional` 或抛出业务异常代替 null |

**反例**（EXC-01/EXC-03）:
```java
try {
    doScan();
} catch (Exception e) {          // EXC-02 大而化之
    // 忽略                       // EXC-01 空块吞异常
} finally {
    return RESULT_OK;            // EXC-03 finally return
}
```

## 4. 团队工具化诉求（期望自动检查）

- **可自动化**（团队预期工具能查）: LOG-01、LOG-02、LOG-03、EXC-01
- **部分可自动化**: EXC-02（catch 参数类型匹配）、EXC-03（finally 块内 return 匹配）
- **暂不自动化**: EXC-04/05/06、LOG-05（业务语义，CR 把关）

---
*依据: 《阿里巴巴Java开发手册》黄山版 §异常日志；本团队 2025-06 日志缺失事故复盘。*
