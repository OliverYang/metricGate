# metricgate 确定性约束能力评估（2025-09）

所有数据来自本机实测（Apple M 系列 / Go 1.26 / metricgate 0.1.0 @ commit becb5ea+）。
评估对象：确定性检查/度量/约束层（非 LLM 部分）。

## 1a. 是否真的起作用？—— 是，但要把"起作用"拆成三层说

**检出能力（受控实验）**：构造 150 个已知违规种子（40 文件，覆盖全部 6 条启用规则
+ 度量阈值）+ 10 个干净对照文件：

- 查全率 **150/150 = 100%**，无漏报、无误报、零多余 findings
- 10 个干净对照文件 0 findings

**真实性（自吃狗粮）**：开发过程中闸门真实拦截了 4 次问题：

1. `cmdMetrics` 圈复杂度 17（error，>15）→ 重构拆函数
2. facts 开发期 5 个 error（Collect cognitive 32 / constraints 36 / 测试超标）→ 重构
3. MCP/stubs 开发期 4 个 error（Scan cc 16、panicStubMessage 嵌套 8 层）→ 抽公共包重构
4. `-format json` 把 gate verdict 混进 stdout 破坏机器可读性 → 本轮修复

即：它连写它的人/生成它的 agent 都在持续拦截——这是比合成实验更强的证据。

**对真实代码的发现是真事实**（yaml.v3，19 文件手写真实库）：
file-length 12、panic-call 30、cognitive 48、cyclomatic 62、function-length 46、
nesting 16、params 3、no-fmt-print 4。抽查属实：yaml.v3 确有 300+ 行函数和解析器
内部 panic。**注意**："事实为真"≠"都应作为违规"——解析器内 panic 是否合法属于
policy 判断，这正是策略文件和 suppression（待建）存在的意义。

**尚无证据的部分（诚实声明）**：对 LLM 生成质量本身的改善（减少幻觉 API、减少
返工轮次）需要真实 agent 接入做 A/B 对照，本报告不给出未测量的结论。

## 1b. 覆盖面能有多广？—— 结构性约束覆盖完整，语义覆盖为零（by design）

当前：8 条模式规则（禁用 API / 空块 / panic / TODO / printStackTrace）+
6 项度量阈值 + 符号事实（facts）+ 完整性审计（stubs）+ 结构约束闸门。

与主流静态工具的诚实对比：PMD ~300 规则、SonarGo ~150+、Checkstyle ~180。
本工具的规则数量是它们的一个零头，且**这是定位选择**：

| 覆盖 | 不覆盖（有意） |
|---|---|
| 禁用 API / 依赖白名单 | 类型错误、空指针（staticcheck/SpotBugs 领地） |
| 结构度量与阈值（复杂度/长度/嵌套/参数） | 数据流/污点（semgrep Pro 领地） |
| 空块/未完成代码（桩清单） | 跨文件死代码（需全程序分析，有反射误报） |
| 符号事实（防 API 幻觉的 grounding） | 业务逻辑正确性 |
| （路线图）重复检测、依赖图/分层 | 格式化（gofmt/prettier 领地） |

**扩展成本已实测**：加一门语言 = grammar 注册 1 行 + 规则包 1 文件（~110 行）+
度量 profile 1 段（~10 行），框架零改动（Java 接入即证）。规则本身的边际成本是
一条 query + 一个过滤函数（~30-50 行/条）。

## 1c. 系统复杂度 —— 低，且有硬上限

- 工具自身：**4,410 行 Go（含 852 行测试）**，7 个内部包
- 依赖：tree-sitter（CGO，已锁版本）+ MCP SDK + yaml + 纯 Go SQLite；go.mod 无 replace，全传递依赖 55 模块
- 产物：单二进制（CLI 11.6MB / MCP 12.5MB），无运行时服务、无数据库服务、无容器
- 用户侧集成面：**1 个 policy 文件 + 1 个二进制**；CI 集成 = 一行命令 + exit code
- 复杂度风险点（诚实）：CGO 交叉编译需要 C 工具链（各平台 CI 需配）；grammar 版本升级需回归测试（case 箭头 switch 的 ERROR 节点问题即此类）

## 2. 运行效率

**全量检查延迟**（8 worker）：

| 目标 | 规模 | 耗时 | 内存 | 吞吐 |
|---|---|---|---|---|
| 本仓库 | 15 文件 | 16ms | ~29MB 级 | — |
| yaml.v3（真实手写） | 19 文件 | 63ms | 29MB | ~300 文件/s |
| modernc/libc（机器生成，含 11 万行文件） | 1383 文件 | 7.9s（CPU 47s，并行比 6x） | 1.6GB | ~175 文件/s |

- **MCP check_code 单次调用**：引擎侧 3 文件全检查实测 2-3ms；单代码片段检查
  （CheckSource）毫秒级，10 连发全部正确应答（精确的每调用延迟待 in-process
  benchmark，量级为个位数 ms）
- **增量模式**：diff-only 不减少分析时间（仍全量分析后过滤），但把"门禁噪音"
  降为增量；分析提速靠多 worker（已做）+ 解析缓存（已评估为非阻塞项）

**后记（内存问题已修复）**：初步评估后定位到内存随文件数线性累积（4 个 3.8MB
大文件 797MB → 8 个 1547MB，与并发上限无关，GOGC 调参无效），在 >1MB 大文件
处理后触发 `debug.FreeOSMemory()` 归还页：8 大文件 1547→450MB，全量 libc
1589→461MB（**-71%**）；代价是病态仓库分析时间 7.9→15.9s（仅大文件触发，
常规仓库不受影响）。

**Token 经济**（省 token 的量化）：

| 机制 | 实测数据 |
|---|---|
| context 压缩 | 整包公共面+约束打包为原文件的 **17.8%~54.5%**（4 个包实测；api 越密集压缩比越高） |
| check_code 替代审查轮 | 0 token（本地）替代每片段 ~1-2k token 的 LLM 自审/互审 |
| diff-only | 存量代码 0 token（不进 prompt、不产生 review） |
| facts 防幻觉 | 消除"幻觉 API → 编译失败 → 再探索"整轮 turn（每轮数千 token）——幅度取决于 agent 工作流，属待 A/B 项 |

## 主要风险与优化点（按影响排序）

1. **大仓库内存**：113k 度量快照全量驻留导致 libc 场景 1.6GB RSS → 需要流式
   写库/分批 flush（v1 任务）
2. **精度与 policy 的边界**：真实库（yaml.v3）报告了大量"真事实"型 findings，
   若不作调优会淹没问题 → suppression 注释 + severity 分级是可用性前提（P0）
3. **LLM 侧效果未验证**：约束对生成质量/返工轮次的改善需要真实接入 A/B（下一步
   设计的核心实验）
4. grammar 升级回归（Java 箭头 switch ERROR 节点）需一次性处理

## 结论

确定性约束层在**检出有效性（100% recall）、效率（毫秒~秒级、0 token）、
复杂度（4.4k 行单二进制）**三个维度上均达到可支撑"LLM 约束闸门"定位的水平；
主要缺口不在引擎而在**可用性配套**（suppression、大仓库内存、policy 调优）和
**LLM 侧效果实证**。
