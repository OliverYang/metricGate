# metricgate 能力清单（汇总）

> 本文是**当前版本的完整能力说明**：实际具备什么、从哪个入口用、边界在哪。
> 与 [roadmap-research.md](research/roadmap-research.md)（六个约束族的方法论）和
> [metrics.md](metrics.md)（度量定义）配套；README 是门面，本文是参考。
> **价值主张**（哪些说法有实测支撑、哪些已被证伪、哪些还没测）见
> [claims.md](claims.md) —— 那是唯一的说法来源，本文只描述功能。
> 最后核对：2026-09-28（对照 `metricgate` 二进制与 `cmd/`、`internal/` 源码逐条确认）。

## 0. 一句话定位

**本地、毫秒级、0 token 的确定性约束层**，为 LLM 生成代码提供三个触点：

| 触点 | 时机 | 入口 |
|---|---|---|
| 生成前给约束 | 写代码之前 | CLI `context` · MCP `get_constraints` |
| 生成后验约束 | 每次编辑之后 | CLI `check` · MCP `check_code` / `check_path` |
| 交给宿主 agent 复核 | 需要人/模型判断时 | CLI `bundle` · MCP `get_review_bundle` |

形态是**纯库引擎 + 薄壳**：`internal/engine` 不打印、不写文件、不发网络请求；
CLI 与 MCP 只是两个外壳，且这份"内核纯净"由项目自己的 `architecture:` 分层规则在每次运行时验证。

## 1. 语言与解析

- **支持语言**：Go、Java（tree-sitter 语法）。
- **容错解析**：语法错误降级成 info 级 `parse-error` 而不中断整轮——刚生成的、还没写完的代码也要能给出部分结论。
- **单次遍历**：度量、规则、事实（facts）、跨文件引用索引、clone 指纹、类型普查都来自同一次 parse，没有第二次扫描。
- **唯一的进程边界**：`internal/git`。策略里 `os/exec` 的禁用规则只给它和 CLI 开白名单。

## 2. 六个约束族的现状

| 约束族 | 回答的问题 | 现状 |
|---|---|---|
| **A 模式约束** | 这个 API/import/写法被允许吗？ | ✅ `banned-api:` YAML 数据化 + 内置结构规则 |
| **B 架构约束** | 这个依赖方向/分层合法吗？ | ✅ 分层 / forbid / 循环（Tarjan SCC）/ 稳定依赖原则（SDP） |
| **C 度量约束** | 这个函数/类/包复杂度超标吗？ | ✅ 6 语言级 + 5 CK 类级（Java）+ MI + 5 包级 |
| **D 重复约束** | 这段代码该复用既有的吗？ | ✅ Type-2 function clone + 近似重复 + 双胞胎漂移 |
| **E 深度缺陷** | 空指针/资源泄漏/类型错误？ | ⏸ **未做**（需类型系统，按计划走外部适配器，见 §11） |
| **F 事实与完整性** | 该对齐什么签名？还剩多少桩？ | ✅ facts / stubs / symbols / impact / map |

## 3. 规则清单

### 3.1 内置规则（tree-sitter query + Go 过滤器）

| 规则 id | 语言 | 默认级别 | 说明 |
|---|---|---|---|
| `empty-block` | Go · Java | warning | 空的控制流块（`if`/`for`/`while`/`catch`），通常藏着被吞掉的错误 |
| `todo-comment` | Go · Java | info（**默认关闭**） | TODO/FIXME 注释 |
| `print-stack-trace` | Java | warning | `e.printStackTrace()` 绕过日志管道 |
| `silent-catch` | Java | warning | 捕获异常后既不抛也不记，异常参数也没被引用（catch 体非空 + 异常参数未引用 + 无 throw/log 三信号判据） |
| `call-chain-depth` | Go · Java | warning（可调 `max_depth`，默认 3） | 消息链超过 `max_depth`（Lanza & Marinescu *chain-size > 3* 检测策略）：每多一节链，就多耦合一个类的内部导航；只报最外层整链，修法是把中间步提取成具名变量/函数 |
| `test-no-assert` | Java | warning | 测试方法无任何断言（testsmells.org UT test smell，FSE'20）：要么加 Assert/fail，要么声明预期异常，否则该测试不可能失败 |
| `unknown-dependency` | Go · Java | warning | import 既不是标准库/JDK，也不在已声明依赖里（详见 §5.2） |

### 3.2 由策略编译出的规则

| 规则 id | 来源 | 说明 |
|---|---|---|
| `banned-<pattern>` | `languages.<id>.banned-api` | 调用或 import 的禁用清单。id 由模式折叠而来（`banned-fmt-Print*`、`banned-os-exec`）。支持 `kind: call\|import`、尾部 `*` 前缀匹配、`except_files` 文件名后缀豁免；可按 id 单独调 severity / 加 `allowed_files` |

### 3.3 整图（whole-graph）规则

| 规则 id | 默认级别 | 说明 |
|---|---|---|
| `layer-violation` | warning | 依赖跨越了声明的分层边界 |
| `forbidden-edge` | warning | 命中 `forbid:` 的禁止依赖方向（优先于分层判定，一条边只报一次） |
| `import-cycle` | warning | 包导入环（Tarjan SCC） |
| `sdp-violation` | warning | 稳定依赖原则：稳定包依赖了比它更不稳定的包 |

### 3.4 跨文件 / 增量规则

| 规则 id | 触发方式 | 说明 |
|---|---|---|
| `function-clone` | `clones.enabled: true` | Type-2 克隆：锚定在**重复体**上并指向原件 |
| `clone-drift` | `metricgate clonedrift` | 双胞胎克隆只改了一侧 |
| `metric-regression` | `check -diff -regress` | 度量值比 `-base` 变差（即使没跨阈值） |
| `parse-error` / `read-error` | 始终 | 解析失败（info）/ 读取失败（warning）。**语法错误的文件仍会被分析**，部分结果照常上报，并额外附一条 info 级 `parse-error`，文件名同时进 `parseErrors` 列表 |

### 3.5 度量阈值规则

每个度量 id 同时是一个规则 id，来源标为 `metric`（区别于规则的 `rule`）。命中阈值时按 `value > warn` / `value > error` 生成 finding。

## 4. 度量清单

阈值比较语义：**严格大于**（`value > warn` → warning，`value > error` → error）。

### 4.1 语言级（函数/文件作用域）

| metric id | 作用域 | 默认 warn/error |
|---|---|---|
| `cyclomatic-complexity` | 函数 | 10 / 15 |
| `cognitive-complexity` | 函数 | 15 / 25 |
| `function-length` | 函数 | 50 / 100 |
| `file-length` | 文件 | 400 / 800 |
| `nesting-depth` | 函数 | 4 / 6 |
| `params-count` | 函数 | 5 / 8 |
| `npath-complexity` | 函数 | 200 / 200（Nejmeh 1988，见 docs/metrics.md） |
| `fan-out` | 函数 | 10 / 20（Yourdon & Constantine） |
| `boolean-params` | 函数 | 2 / 4（Fowler flag argument） |

### 4.2 类级（CK 家族，Java）

| metric id | 含义 | 默认 warn/error |
|---|---|---|
| `wmc` | 加权方法复杂度 | 40 / 60 |
| `nom` | 方法数 | 20 / 30 |
| `lcom` | 方法内聚缺失（lite） | 20 / 40 |
| `cbo` | 对象间耦合 | 15 / 25 |
| `dit` | 继承深度 | 5 / 8 |

### 4.3 可维护性指数

| metric id | 口径 | 默认 |
|---|---|---|
| `maintainability` | **函数级** MI（Oman & Hagemeister 1992 / MS 2007 变体），文件取其最差函数；快照存的是**债务** `100-MI`（越大越差） | 0 / 0 —— **默认只报告、不设闸** |

> 说明：文件没有函数时 MI 记 0 = "未测量"，此时**不落快照**，不会被当成"最差可维护性"。

### 4.4 包级（Martin 包度量，报告型）

| metric id | 含义 | 说明 |
|---|---|---|
| `pkg-ca` / `pkg-ce` | 传入/传出耦合 | 仅在策略配了阈值时才采集 |
| `pkg-i` | 不稳定性 `Ce/(Ca+Ce)` | 始终采集，报告型 |
| `pkg-a` | 抽象度 | 始终采集，报告型 |
| `pkg-d` | 距主序列距离 `\|A+I-1\|` | 始终采集，报告型 |

度量定义与偏差见 [metrics.md](metrics.md)。

## 5. 架构与依赖

### 5.1 架构约束（`architecture:`）

- **包 = 目录（Go）或 package 子句（Java）**。标准库与三方 import 不是图节点，所以图描述的是**你自己的**架构。
- 模式按**尾段**匹配：`internal/rules` 也能命名 `yourmodule/internal/rules`；`/**` 跨层，`*` 单段。
- **只有被分层的包受约束**：不在任何 layer 里的包、以及指向所有 layer 之外的边，一律合法——只描述你在意的部分。
- `forbid` 命中优先于分层判定；finding 锚定在 **import 行**，因此 `-diff` 能闸住新增的非法边，`metricgate: ignore` 也能精确抑制某一条。

**稳定依赖原则（`sdp-violation`）** —— 整图规则里唯一不需要架构段就能生效的一条：

| 配置 | 默认 | 作用 |
|---|---|---|
| `architecture.sdp` | `warning` | 严重度；`off` 表示只记快照、不报 finding |
| `architecture.sdp_tolerance` | `0`（严格） | 不稳定性差 ≤ 该值的边不报 —— 不稳定度是整数耦合比，分辨率 ~1/(Ca+Ce)，差小于一个量化步长时不是设计债 |

- 报的是"稳定包依赖了更不稳定的包"，锚定在 **import 行**；同一包有多个候选目标时取**最不稳定**的那个，并列时**按包名**决定（保证逐字节可复现）。
- 消息给出**正确修法**并显式排除错误修法（"invert the edge behind an interface, or move the shared part down. Do not copy the code instead."）—— 因为复用是工具鼓励的行为，告警若暗示"抄一份"就等于亲手制造重复。
- **严重度必须低于 `gate.fail_on`**：复用会新增依赖边，而新增边可能触发 SDP。默认 `warning` + 默认 `fail_on: error` ⇒ 复用永不被拦。见 [evaluation-hint-and-cost.md](evaluations/evaluation-hint-and-cost.md) §4。
- 注意：**本仓库自己的 `policy.yaml` 有 6 个包不在任何层里**（`apisurface`/`bundle`/`clonedrift`/`clones`/`hotspots`/`symbols`）。把 `apisurface` 补进 `core` 会让 `apisurface → facts` 从一条含糊的 SDP 告警变成精确的 `layer-violation`（core→adapter）。补齐层声明是待办的架构决策，不是策略调整。

### 5.2 依赖白名单（`unknown-dependency`）

用来在合并前拦下**幻觉包 / 未声明的新依赖**：

- 已声明依赖 = `go.mod` 的 module + require（向上查找）∪ 策略的 `dependencies.allow` / `dependencies.self`。
- Go：**首个路径段不含点**即视为标准库（也覆盖本模块的无点路径）；其余必须在白名单内。
- Java：`java.` / `javax.` / `jdk.` / `sun.` / `com.sun.` 前缀视为 JDK；其余必须在白名单内。
- 匹配按**分隔符边界**（Go `/`、Java `.`），所以 `example.com/foo` 不会误放行 `example.com/foobar`。

## 6. 重复代码

| 能力 | 入口 | 做什么 |
|---|---|---|
| Type-2 克隆闸门 | `check`（`clones.enabled`） | 归一化标识符/类型名/字面量，保留运算符与标点；重命名+改格式的复制会被抓到，`a+b` vs `a-b` 不会 |
| 近似重复 | `metricgate nearclones` | 归一化 token 集合的 Jaccard 相似度 ≥ `-threshold`（默认 0.8）的"复制+改名+小改"对 |
| 双胞胎漂移 | `metricgate clonedrift` | 对比 `-base` 与工作树：一侧被编辑、另一侧没动 → `clone-drift` |

克隆类会同时渲染进 `context` / `get_constraints` 的 **"reuse before you generate"** 段，模型在写之前就看到原件。

## 7. 导航与排序（0 token 的"先读哪些"）

| 能力 | 入口 | 回答的问题 |
|---|---|---|
| 符号索引 | `symbols -q NAME` · MCP `find_symbols` | 这个 helper 已经存在吗？（精确命中 + 拼写/大小写近似建议） |
| 影响面 | `impact -q NAME` · MCP `get_impact` | 改这个符号会波及谁？（全部调用点/类型引用 + 受影响文件） |
| 仓库重要性地图 | `map` · MCP `get_repo_map` | 先读哪些文件？（引用图 + PageRank，逐字节稳定） |
| 热点 | `hotspots` · MCP `get_hotspots` | 改动最频繁 × 债务最重的文件在哪？（Tornhill 热点分析） |
| 分布统计 | `stats` · MCP `get_metric_stats` | 整体有多糟、最糟的是谁？（min/p50/p90/p95/max/mean + top-N） |

**风险聚合分**（`internal/risk`）：把 findings 按严重度加权（error 10 / warning 3 / info 1）做饱和归一，再与最差 MI 债务按 7:3 混合成 0–100。低于 1.0 不报（几乎每个文件都有点 MI 债务，不设下限就是噪声）。`hotspots` 与 `map` 都会带上它——**只做标注，不改变原有排序**。

> **诚实说明（已实测）**：不要把这些工具当"省探索"来卖。
> 实测发现一份既有实现只需 ~4 KB（`map` 3.9 KB + 目标文件，对比盲读全仓 428 KB），
> 但**不用任何工具**的 agent 也能 5/5 复用既有实现 —— **瓶颈从来不是"找不到"**。
> 这一族真正的价值是**验证**（把"我复用了"从自述变成一条 grep 的事实）和
> **二阶后果的可见性**（复用改变了整张依赖图，单文件视角看不见）。
> 数据见 [evaluation-hint-and-cost.md](evaluations/evaluation-hint-and-cost.md) §3。

## 8. 增量与回归

| 能力 | 入口 | 语义 |
|---|---|---|
| 只闸新增 | `check -diff [-base ref]` | 规则 finding 看行号是否在 diff 内；函数级度量看区间是否与函数相交（"你碰了它就归你"）；`file-length` 只报**新增文件**；未跟踪文件视为全新增 |
| 变差即报 | `check -diff -regress` | 对比 `-base`（默认 merge-base）报告"度量变差但没跨阈值"的函数 |
| API 面快照/闸门 | `api snapshot -out F` · `api diff -base F` | 导出导出面；签名删除/变更即 exit 1 |
| 架构形状漂移闸门 | `arch snapshot -out F` · `arch diff -base F` | 冻结包依赖方向；新增/删除任一方向即 exit 1（逐次运行的分层闸门抓不住的“架构形状变化”） |
| 路径选择解释 | `explain [path]` | 这个文件为什么被纳入/排除？适用哪些规则、来自哪一层策略？ |
| 策略来源追溯 | `rules explain list` · `rules explain <id>` · `rules presets` | 每个值的来源：`builtin` \| `policy_file` \| `preset:NAME`；`list` 含 `[graph]` 段，列出每条整图规则及武装它的策略键 |
| 策略完备性审计 | `rules gaps [path]` | 逐条规则给出沉默的原因：`firing` / `armed` / `report_only` / `disabled` / `undeclared` / `flag_only` / `no_files`，外加 hot rules、分层覆盖缺口、按规则的压制计数 |

## 9. 策略文件（约束模型）

`policy.yaml` 是项目的约束模型；**同一份文件**今天驱动闸门，将来驱动上下文/骨架生成。

| 段 | 字段 | 作用 |
|---|---|---|
| — | `version` | 策略版本（当前 1） |
| — | `preset` | 启用内置预设（见下） |
| `gate` | `fail_on` | `error`（默认）/ `warning`：达到该级别即 exit 1 |
| — | `exclude` | 按**目录名**跳过的目录 |
| `languages.<id>` | `rules.<id>` | 单条规则的 `enabled` / `severity` / `options` |
| | `metrics.<id>` | 单度量的 `enabled` / `warn` / `error` |
| | `banned-api` | 禁用清单（见 §3.2） |
| | `banned-api-severity` | 该语言所有 ban 的兜底 severity |
| `dependencies` | `allow` / `self` | 依赖白名单（见 §5.2） |
| `architecture` | `edges` / `cycles` / `sdp` | 三类整图规则的 severity（`sdp: off` = 只记快照不报 finding） |
| | `sdp_tolerance` | 不稳定性差 ≤ 该值的边不报（默认 0 = 严格，见 §5.1） |
| | `layers` / `forbid` | 分层与禁止边 |
| `clones` | `enabled` / `min_lines` / `min_tokens` | 重复检测开关与门槛（零值 = 关闭） |
| `store` | `enabled` / `path` | 本地趋势库（SQLite） |

### 预设（`preset:`）

`agentic` —— 面向"模型生成的代码"的闸门调法：**失败模式收紧**（`empty-block`、`print-stack-trace`、`unknown-dependency`、所有 banned-api 升为 error），**纯体量/风格上限放宽**（function-length 80/160、file-length 800/1600、params-count 6/9），**复杂度度量保持不动**（那是闸门存在的理由）。

预设是**默认层**而非第二份策略：显式 YAML 永远优先，且每个值都被记账，`rules explain` 会标成 `preset:agentic`。不写 `preset:` 时行为与预设出现前逐字节一致。用 `metricgate rules presets` 查看它改了哪些值。

### 抑制（就地豁免）

```go
fmt.Println("boot")        // metricgate: ignore banned-fmt-Print* CLI 入口，允许

// metricgate: ignore params-count 兼容旧签名，不动调用方
func Parse(a, b, c, d, e, f, g int) int { ... }

// metricgate: ignore-begin cyclomatic-complexity
... 生成的表驱动分发 ...
// metricgate: ignore-end
```

范围：同一行（注释独占一行时含下一行），或 `ignore-begin`/`ignore-end` 区间。规则列表逗号分隔，`all` 或省略表示全部，其余是自由文本理由。被抑制的 finding 不进闸门，但会在报告里计数（"N findings suppressed"）。

## 10. 输出与集成

**输出格式**：终端文本（默认）、JSON（多数命令 `-format json`）、**SARIF 2.1.0**（`check -sarif out.sarif`，兼容 GitHub code scanning）、**本地 SQLite**（`runs` / `findings` / `metric_snapshots` 三张表；只有 `check` 写库，`trends` 只读）。

### 10.1 CLI（20 个子命令）

| 命令 | 作用 | 主要 flag |
|---|---|---|
| `init [file]` | 写出起始策略 | `-preset` |
| `check [path]` | 规则 + 度量 + 闸门 | `-sarif` `-diff` `-base` `-regress` `-format` `-preview` `-max-cognitive` `-max-cyclomatic` `-config` |
| `metrics [path]` | 逐文件度量表 | `-config` |
| `stats [path]` | 分位数表 + 最差 scope | `-top` `-format` `-config` |
| `context [path]` | 给 LLM 的约束包（预算受限） | `-max-chars` `-max-tokens` `-all` `-config` |
| `bundle [path]` | 给宿主 agent 的复核信封 | `-base` `-from` `-to` `-commit` `-max-chars` `-token-budget` `-include-source` `-out` |
| `symbols [path]` | 符号查找 + 近似建议 | `-q` `-max` |
| `impact [path]` | 引用点与影响面 | `-q` `-max` `-format` |
| `map [path]` | 重要性排序（+风险分） | `-n` `-max-chars` `-risk` `-format` |
| `hotspots [path]` | 改动×债务热点（+风险分） | `-since` `-max` `-format` |
| `stats` / `metrics` / `trends` / `clonedrift` / `nearclones` | 见各节 | — |
| `trends` | 从趋势库读严重度趋势 + 高频规则 | `-runs` `-format` |
| `calls` | 把 MCP 本地调用日志压成一张表：按阶段/工具的调用次数、耗时中位数、拦截率、收敛率 | `-log` `-since` `-format` |
| `clonedrift [path]` | 双胞胎漂移 | `-base` `-format` |
| `nearclones [path]` | 近似重复对 | `-threshold` |
| `rules explain list\|<id>` · `rules presets` | 策略来源追溯 / 预设明细 | `-lang` `-format` |
| `explain [path]` | 路径选择与适用规则解释 | `-preview` `-format` |
| `api snapshot -out F` · `api diff -base F` | API 面快照 / 破坏性变更闸门 | `-out` `-base` `-format` |
| `arch snapshot -out F` · `arch diff -base F` | 架构形状快照 / 漂移闸门 | `-out` `-base` `-format` |
| `constraints digest` | 非标约束摘要：按包裁剪的分层契约/禁用API/阈值/复用清单/红线，注入生成前（R3 实测 31.8%→4.5%） | `-pkg` `-config` `-lang` `-format` `-no-reuse` |
| `constraints extract` | 非标规范文档→条款→三档判型（✅/⚠️/❌）+ policy 查重→候选 YAML | `-file` `-lang` `-config` `-format` `-out` |
| `constraints status` | 约束落地后的检出情况：逐规则命中/零命中/不适用（按语言分流）+ 闸门预判 | `-config` `-format` |
| `constraints rule` | 规则集成接口：Spec JSON+fixture → 引擎真跑验证（反例必中/正例零误伤）→ PASS/FAIL + 可合并 policy 片段（节点落盘可改可重跑可回退） | `-spec` `-bad` `-good` `-config` `-format` `-out` |
| `version` | 版本 | — |

`-max-chars` 与 `-max-tokens`、`-token-budget` 与 `-max-chars` 互斥；`-max-chars` 报的是精确 UTF-8 字节数，token 预算是 bytes/4 **估算**，信封里标 `"exact": false`——绝不当成硬保证。

### 10.2 MCP stdio 服务（11 个工具）

`metricgate-mcp -config policy.yaml`，供编码 agent 在会话中调用：

| 工具 | 用途 |
|---|---|
| `get_constraints` | 生成**前**的稠密事实+约束包（签名/导入/规则/上限） |
| `check_code` | 每次编辑**后**检查内存中的代码（0 token，毫秒级，不落盘） |
| `check_path` | 检查磁盘文件，`diff_only=true` 时只报新增违规 |
| `list_stubs` | 修复工单：空实现、`panic("not implemented")`、`UnsupportedOperationException`，各带精确定义位置 |
| `find_symbols` | 跨文件符号索引：精确命中 + 近似建议 |
| `get_impact` | 改这个符号会波及谁 |
| `get_repo_map` | 重要性排序（默认附风险分，`skip_risk=true` 可跳） |
| `get_hotspots` | 改动×债务热点（附风险分，需 git 仓库） |
| `get_metric_stats` | 度量分布 + 最差 scope（可按 metric 过滤） |
| `get_trends` | 读本地趋势库（只读） |
| `get_review_bundle` | 预算受限的复核信封（diff + 选择 + 约束 + 事实） |

CLI 与 MCP **共用同一份实现**（`facts.RankedFiles`、`hotspots.FromSummary`、`store.Report`、`dist.Reports`、`bundle.Build`），因此两边的 schema 与结果不会漂移。

**本地调用日志（默认关闭）**：`metricgate-mcp -calllog <file>`（或 `METRICGATE_CALLLOG=1` 用默认路径
`.metricgate/mcp-calls.jsonl`）会给每次工具调用追加一行 JSONL；`metricgate calls` 读它出汇总。
约束是结构性的：**只写本地文件、没有网络客户端、永不记录 `code` 参数**
（定位串走 `path`/`store_path`/`query`/`metric`/`name` 白名单），写日志失败也不会让工具调用失败。
接线方式与指标定义见 [`workflow.md`](workflow.md)。

## 11. 边界与**不做**的事（诚实清单）

- **名字级、AST-only**：接口分派、反射、动态构造的名字看不到。`impact` / `map` 的置信度标为 `ast-only`，宁可承认盲区也不假装是完整调用图。
- **没有类型系统**：约束族 E（空指针/资源泄漏/类型错误）**不做**，按计划走外部 CLI 适配器（golangci-lint / SpotBugs / PMD），保持松耦合与许可证干净。
- **不做服务端**：明确不采用 CodeQL（需建库、分钟级、服务化）与 SonarQube（服务端重），与本地的轻量定位冲突。
- **不调用 LLM、不发网络请求**：`bundle` 只产出信封，推理交给宿主 agent。
- **只有 Go 与 Java**：Python/TS 语言包框架已验证，但排在约束族补齐之后。
- **不做 autofix/codemod**：信任成本最高，等闸门建立信任后再评估。

## 12. 如何自证

```bash
go build ./... && go test ./...     # 构建 + 单元/端到端测试（25 个包）
go run honnef.co/go/tools/cmd/staticcheck@latest ./...   # 零告警
gofmt -l internal/ cmd/             # 空
./metricgate check .                # 用自己检查自己（dogfood）
./metricgate check . -diff          # 只报本次改动新增的（hook / CI 用的就是这个）
./metricgate calls                  # 读本地 MCP 调用日志（默认关闭，见 workflow.md）
```

本仓库用自己当第一个用户：`metricgate check .` 必须通过。历史上有两处正是被它逼出来的真实修复——把 `internal/diff`（纯行号算术）从 `internal/git`（适配器）里拆出来，以及把重复的测试辅助函数抽成 `internal/parsetest`。

接线层也走过同一条路：把 `cmd/metricgate-mcp/main.go` 的 `-calllog` 接线与两个闸门入口
（`check_code`/`check_path`）拆到 `calllog.go` / `checks.go`，正是因为加了埋点后
`main.go` 越过 400 行的 `file-length` 阈值——**按闸门报的修结构，而不是加 ignore**。

> 截至 2026-09-28，dogfood 结果为 **1 error / 66 warnings**；唯一的 error 是既有的
> `cmd/metricgate/explain_cmd.go:45 cmdExplainPreview`（cc=18），非本轮引入。
> （变化轨迹：1/70 → 1/67（SDP 加 `sdp_tolerance` 吸收一条量化噪声，另两条是改动自己引入
> 又被重构消掉）→ 1/66（清理死代码，`clonedrift` 的一个 cognitive 越界随之消失）；
> 接线层新增的 6 个文件与 3 处改动**净新增 0 条发现**。
> 该数值会随代码演进而变，请以实际运行结果为准。
