# 扫描门户团队研发规范 01 —— 命名与注释规范

> **文档信息**: scan-portal-business 团队研发规范 v1.0（2026-09）
> **来源**: 依据《阿里巴巴Java开发手册》（黄山版）裁剪，结合本团队历史事故与 CR 共识修订。
> **用途**: 本文档是团队"非标规范"的正式输入物料，交由 metricGate 转化为可执行检查
> （转化对照见 `docs/pilot/conversion-map.md`；落地规则集见 `demo/policy-pilot-java.yaml`）。

## 1. 目的与适用范围

统一 scan-portal-business 全部 Java 代码的命名与注释风格，降低 CR 沟通成本，
避免因命名歧义引发的线上事故（案例：`isDelete` 序列化反转导致筛选条件全量失效）。
适用于 scan-portal-business 及其后续拆分模块的所有 Java 源码。

## 2. 命名规约

| 编号 | 级别 | 条款 |
|---|---|---|
| NAMING-01 | 强制 | 类名使用 UpperCamelCase 风格，但以下情形例外：DO / BO / DTO / VO / AO / UID / PO 等 |
| NAMING-02 | 强制 | 方法名、参数名、成员变量、局部变量统一使用 lowerCamelCase |
| NAMING-03 | 强制 | 常量命名全部大写，单词间用下划线隔开，力求语义完整（如 `MAX_SCAN_RETRY_COUNT`） |
| NAMING-04 | 强制 | 抽象类以 `Abstract` 或 `Base` 开头；异常类以 `Exception` 结尾；测试类以被测类名开头、`Test` 结尾 |
| NAMING-05 | 强制 | POJO 类中布尔类型变量一律不加 `is` 前缀（RPC 序列化/反序列化属性名不一致风险） |
| NAMING-06 | 强制 | Service / DAO 层方法命名：获取单个 `getXxx`，获取多个 `listXxx`，统计 `countXxx`，新增 `saveXxx`/`insertXxx`，删除 `removeXxx`/`deleteXxx`，修改 `updateXxx` |

**反例**（NAMING-05）:
```java
// 反例：is 前缀在部分序列化框架下属性名会被解析为 delete，引发数据错乱
private Boolean isDelete;
```
```java
// 正例
private Boolean deleted;
```

## 3. 注释规约

| 编号 | 级别 | 条款 |
|---|---|---|
| COMMENT-01 | 强制 | 类、类属性、类方法的注释必须使用 Javadoc 规范（`/** 内容 */`），不得使用 `// xxx` |
| COMMENT-02 | 推荐 | 所有的抽象方法（包括接口中的方法）必须要用 Javadoc 注释，说明返回值和参数，有异常时注明抛出条件 |
| COMMENT-03 | 强制 | TODO / FIXME 标记必须带责任人与预期时间，且必须有跟踪事项；禁止无归属的 TODO 长期滞留 |
| COMMENT-04 | 推荐 | 与其"半吊子"英文注释，不如用中文注释把问题说清楚；专有名词与关键字保持英文原文 |

## 4. 团队工具化诉求（期望自动检查）

团队希望在 CR 前由工具先行拦截，减少人工 CR 负担：

- **可自动化**（团队预期工具能查）: COMMENT-03（无归属 TODO）、NAMING-05（is 前缀）
- **部分可自动化**: NAMING-04（类名形态与修饰符关联）
- **暂不自动化**: NAMING-01/02/03、COMMENT-01/02（依赖标识符形态分析与类型信息，接受 CR 人工把关或 IDE 插件兜底）

---
*依据: 《阿里巴巴Java开发手册》黄山版 §编程规约-命名风格/注释规约；本团队 2025 年线上事故复盘两则。*
