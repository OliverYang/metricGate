# 扫描门户团队研发规范 04 —— 工程分层与依赖规范

> **文档信息**: scan-portal-business 团队研发规范 v1.0（2026-09）
> **来源**: 依据《阿里巴巴Java开发手册》（黄山版）§工程结构 裁剪，结合本仓库实际包结构制定。
> **用途**: 团队"非标规范"正式输入物料，交由 metricGate 转化为可执行检查
> （转化对照见 `docs/pilot/conversion-map.md`；落地规则集见 `demo/policy-pilot-java.yaml`）。

## 1. 目的与适用范围

约束模块分层与第三方依赖引入。背景：门户类项目依赖面广，
出现过"直接 import 内部实现类""未走采购流程私自引包"两类问题。
适用于 scan-portal-business 全部 Java 源码。

## 2. 分层规约

本仓库实际包结构为 Spring 分层风格：`controller`（web 层）→ `service`（业务层）→
`mapper`/`dao`（持久层），横切 `common`/`util`/`config` 基础设施。

| 编号 | 级别 | 条款 |
|---|---|---|
| ARCH-01 | 强制 | 分层单向依赖：`controller → service → mapper/dao`；**禁止逆向依赖**（如 dao 依赖 service）、**禁止跨层依赖**（controller 直接依赖 mapper） |
| ARCH-02 | 强制 | **禁止循环依赖**：包与包之间不允许出现循环引用（编译器只查 Bean 循环，不查包循环，包循环是腐化前兆） |
| ARCH-03 | 强制 | `common`/`util` 等基础层不允许依赖业务层（service/controller） |
| ARCH-04 | 参考 | 遵循稳定依赖原则（SDP）：越稳定的包越不应依赖易变的包 |

## 3. 依赖引入规约

| 编号 | 级别 | 条款 |
|---|---|---|
| DEP-01 | 强制 | **禁止引入未在 pom.xml 声明的第三方依赖**（含传递依赖直引、幻觉包名）——编译期 IDE 能过是因为 classpath 恰好有传递包，升级即碎 |
| DEP-02 | 强制 | 新增第三方依赖必须走依赖评审（用途、 License、维护活跃度、替代方案） |
| DEP-03 | 强制 | 不允许重复代码段超过阈值的克隆函数长期并存（DRY），必须收敛到公共方法/工具类 |
| SEC-01 | 强制 | 禁止将 MD5/SHA-1 用于密码等安全敏感场景（仅允许非安全用途的校验和），密码存储必须加盐哈希（BCrypt 等） |

**反例**（DEP-01）:
```java
// 反例：pom.xml 未声明 commons-lang3，靠传递依赖侥幸编译通过
import org.apache.commons.lang3.StringUtils;
```

## 4. 团队工具化诉求（期望自动检查）

- **可自动化**（团队预期工具能查）: ARCH-01、ARCH-02、ARCH-03、DEP-01、DEP-03、SEC-01（近似）
- **暂不自动化**: ARCH-04（SDP 可查但作为观察项先开报告不设卡）、DEP-02（流程性条款）

## 5. 附注：与门禁流程的关系

团队约定：上述"强制"条款进入 metricGate 门禁（`fail_on: error` 即失败），
"推荐"条款仅报告不阻塞；规则豁免必须走 `except_files` 白名单并在 CR 中说明理由，
豁免清单随本规范文档一同维护。

---
*依据: 《阿里巴巴Java开发手册》黄山版 §工程结构；本仓库 scan-portal-business 实际包结构（org.osc.scan.portal）。*
