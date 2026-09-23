# 确定性约束能力补全调研与行动方案（2025-09）

调研对象：为支撑"LLM 生成代码的确定性约束层"定位，还需实现/集成哪些能力。
开源数据为 GitHub 实测（★/活跃度，2025-09）。评估维度：成本 / 效率 / 扩展性 / 维护性。

## 1. 能力地图：从约束场景倒推的六个约束族

| 约束族 | 回答的问题 | 现状 |
|---|---|---|
| A 模式约束 | 这个 API/import/写法被允许吗？ | 部分（8 条硬编码规则）|
| B 架构约束 | 这个依赖方向/分层合法吗？ | **缺失** |
| C 度量约束 | 这个函数/类/包复杂度超标吗？ | 部分（6 指标，缺类级/包级）|
| D 重复约束 | 这段代码该复用既有的吗？ | **缺失** |
| E 深度缺陷 | 空指针/资源泄漏/类型错误？ | **缺失**（类型系统领地）|
| F 事实与完整性 | 该对齐什么签名？还剩多少桩？ | 已有（facts/stubs）|

## 2. 自研 vs 集成：决策框架

三条判据（按序）：

1. **树语法可达 + 输出要进统一 findings/LLM 工单** → 自研。我们已有 parser/度量/
   findings 管线，边际成本是一次遍历；外部工具的输出还得写适配器。
2. **需要类型系统/数据流/全程序分析** → 永远集成，不重造。staticcheck/SpotBugs
   这类工具背后是数十年类型分析积累，AST 层模拟只会做出低配误报版。
3. **集成方式一律走 CLI 适配器**（`metricgate ingest`），不做进程内库依赖：
   - 松耦合：外部工具版本演进不碰核心
   - 许可证干净：独立进程调用，无 LGPL/许可证传染问题（SpotBugs 是 LGPL）
   - CI 可并行：外部工具跑自己的，结果合并进统一 SARIF/store

## 3. 逐项评估

### 3.1 自研项（复用现有管线，边际成本低）

| 能力 | 方案 | 成本 | 效率 | 扩展性 | 维护性 |
|---|---|---|---|---|---|
| **policy 数据化禁用 API/Import** | YAML `banned-api:` 清单参数化现有规则框架 | 0.5d | 毫秒级（并入现有遍历）| 组织加规则=改 YAML | 极低（纯数据）|
| **架构/分层约束** | 自研：facts 已提取 imports，建包依赖图 + policy 声明允许/禁止边 + SCC 循环检测；参考 arch-go（★273，Go 单语言，概念验证了需求）| 2-3d | 图算法，万包级 <100ms | 新语言加 import 提取即得 | 低（核心是 ~200 行图代码）|
| **重复检测（clone）** | 自研：tree-sitter token 归一化 + winnowing 指纹（参考 PMD CPD 思路）；输出 clone class → context 的"复用提示" + diff-aware 新增重复闸门 | 3-4d | O(n) 指纹哈希，10k 文件秒级 | 语言无关（token 流已按语言分好）| 低；阈值与算法可调 |
| **类级/包级度量（CK 家族）** | 自研：Java WMC/NOM/LCOM-lite/CBO/DIT；Go 包级聚合 | 2d | 并入现有遍历 | 指标=纯函数 | 低 |
| **符号索引 v1** | 自研：跨文件 definition map（facts 已有原料）→ get_constraints 附带"相似符号"、stub 修复工单定位 | 2-3d | 索引构建 = 现有遍历 | 结构化 JSON，天然进 context | 低；引用分析只做同仓 AST 级（带置信度）|
| **自定义规则 DSL** | YAML：tree-sitter query + Go 过滤器名 + message（不复用 semgrep DSL——那会拖进整个引擎）| 3d | 同现有规则 | 用户加规则不改源码 | 中（DSL 演进要克制）|

### 3.2 集成项（适配器，站在巨人肩上）

| 能力 | 候选（GitHub 实测） | 成本 | 取得 | 风险 |
|---|---|---|---|---|
| **Go 深度缺陷** | golangci-lint ★19.4k（聚合 staticcheck ★6.9k/go-critic ★2k/govet…）| ingest 适配 1d + 每工具映射 0.5d | 数百条类型级规则 | 输出格式随版本变（锁版本）|
| **Java 深度缺陷** | SpotBugs ★3.9k（LGPL，原生 XML/SARIF 插件）、PMD ★5.5k（原生 SARIF 输出）| 各 0.5-1d | 漏洞/并发/资源类规则 | 需 JVM 环境（CI 镜像预装）|
| **secrets** | gitleaks ★29.4k，单二进制，原生 SARIF | 0.5d | 密钥泄漏检测 | 几乎无 |
| **安全模式** | semgrep ★16.7k（LGPL-2.1，CLI 输出 JSON/SARIF）| 1-2d（可选，v2）| OWASP 模式库 | Python 运行时；规则调优成本 |

明确**不采用**：CodeQL（需要数据库构建，分钟级，且服务化，与本地轻量定位冲突）；
SonarQube（服务端重）；进程内嵌入 semgrep 引擎（许可与复杂度）。

### 3.3 优先级不高的项

- autofix/codemod：信任成本最高，等闸门建立信任后再评估（ast-grep rewrite 可作参考）
- Python/TS 语言包：框架已验证，随时可加（每语言 ~1-2d），排在约束族补齐之后
- LSP/IDE 插件：闸门与 MCP 触点已覆盖主场景

## 4. 行动方案（三阶段，之后全面测试）

**Phase A — 约束核心自研冲刺（~1.5 周）**
1. ~~policy banned-api 数据化（0.5d）~~ ✅ 已完成 → 组织约束从"改源码"变"改配置"
2. ~~依赖图 + 分层规则 + 循环依赖（2-3d）~~ ✅ 已完成 → 约束族 B 落地，组织级约束的最强载体
3. 重复检测 + diff-aware 新增重复闸门（3-4d）→ 约束族 D，"先复用再生成"
4. Java CK 类度量（2d）→ 约束族 C 补全
5. 符号索引 v1（2d）→ context 质量跃升（相似符号/工单定位）

产出：4 个约束族 × 双语言，"约束足够丰富"的门槛达成。

**Phase B — 集成适配（~1 周）**
6. `metricgate ingest` 框架 + golangci-lint 适配（Go 深度规则一次全量接入）
7. PMD（SARIF）/SpotBugs 适配（Java）
8. gitleaks 适配（可选）

产出：规则覆盖从两位数跳到数百条，且不用自建类型分析。

**Phase C — 规模化与开放性（按需）**
9. Python/TS 语言包；10. 自定义规则 DSL；11. 解析缓存/流式存储

**之后**：按 docs/ab-experiment.md 做全面 A/B 测试 → v1.0 release。

## 5. 维护性账本（诚实估计）

- 自研新增 ~1.5k 行（Phase A）+ 适配器 ~600 行（Phase B）
- 长期跟进项：grammar 版本（低频）、golangci/PMD 大版本（年 1-2 次，适配器隔离）、
  Go/Java 语法演进（tree-sitter 社区承担）
- 外部依赖风险排序：golangci-lint 输出格式 > PMD SARIF 字段 > grammar API
  （适配器 + 锁版本 + golden 测试控制）

## 6. 结论

约束层缺的不是"更多 lint 规则"，而是**四个缺失的约束族（架构/重复/类度量/
符号索引）+ 一个接入生态的适配器层**。前者全部自研（管线现成，总成本 ~1.5 周，
且输出直接进 LLM 工单这个差异化通道）；后者全部 CLI 集成（type 分析不重造、
许可证干净、CI 可并行）。这是成本、效率、扩展性、维护性四个维度上的最优解。

## 7. 落地记录

### A-1 policy banned-api 数据化 ✅

实现要点（`internal/rules/banned.go` + `internal/config`）：

- policy 结构：`languages.<lang>.banned-api: [{kind?, match, reason?, except_files?}]`；
  `kind` 默认 `call`，`import` 走 import 节点匹配（Java 端含子包前缀）
- match 语法：点分模式，`pkg.Name`（Go）/ `recv.method` / `Object.field.method`（Java）；
  裸名匹配无限定调用；尾随 `*` = 名称前缀（`fmt.Print*` 覆盖 Println/Printf/...）
- 规则 id 稳定可配：`banned-` + 点与斜杠折叠成短横线，前缀保留 `*`
  （`banned-fmt-Print*`、`banned-os-exec`、`banned-System-out-print*`），
  因此 severity / allowed_files / `metricgate: ignore` 全部照常可用
- 编译入口收敛为 `rules.RulesFor(lang, cfg)`：**引擎与 context 生成器共用**，
  保证 prompt 里承诺的约束与闸门实际执行的约束不可能漂移
- 表达不了的条目静默丢弃（未知 kind、空 match、Java 三层以上 receiver），
  不炸整轮；Java 深 receiver 属类型分析问题，按决策框架留给适配器
- 原有的 `no-fmt-print` / `panic-call` / `system-out-print` 三条硬编码规则已删除，
  改为默认 policy 里的 banned-api 条目（行为等价，测试对齐）

顺带修掉的可用性缺陷：CLI 参数解析原先用 Go `flag` 的默认行为，**位置参数之后的
flag 被静默忽略**（`check . -format json`、`check . -config prod.yaml` 全部失效，
而 README 就是这么写的）。`cmd/metricgate` 现在把 flag 及其取值重排到位置参数之前
再解析，`--` 语义保留，并补了回归测试。

验证：`go test ./...` 全绿（新增 7 个 ban 用例 + 4 个参数解析用例）；
`metricgate check .` 自吃狗粮 0 error（19 warning，均为既有度量债）；
MCP `check_code` / `get_constraints` 冒烟通过，禁令在两个触点上同源。

### A-2 依赖图 + 分层约束 + 循环检测 ✅

实现要点（新包 `internal/arch`：pattern / graph / tarjan / check / extract）：

- **whole-run pass**：每文件 parse 时顺手抽 `package + imports[行号]`
  （`arch.Extract`，Go/Java 各自形状：Java 的包节点是 `package_declaration`），
  文件遍历结束后建图、跑策略；不额外 parse 一次，成本 O(边数)
- **包身份**：Go 用「相对 run root 的目录」，Java 用 package 子句（点转斜杠）。
  策略里的模式按**尾段对齐**匹配，所以 `internal/rules` 同时命中
  `metricgate/internal/rules`；`*` 一段、`/**` 跨段；字面模式可后缀匹配
- **解析方向按语言区分**（测试逼出来的）：Go import 路径即包路径（后缀匹配），
  Java import 是「包 + 类型名」（前缀匹配），因此 `arch.File.Java` 决定 resolve 方向；
  取最长匹配包，`com/foo` 不会被 `com/foobot` 误命中
- **只有被分层覆盖的包受约束**：未分层包、指向"层外代码"的边一律放过 ——
  老仓库可以只描述自己在意的那部分架构，避免上线即 500 条噪音
- **forbid 优先于 layer**：同一条边只出一条结论，不重复报
- **循环 = Tarjan SCC**（>1 成员才报；自环视为解析噪声）
- 三类结论（`layer-violation` / `forbidden-edge` / `import-cycle`）走**统一 findings
  模型**：同一闸门、同一 SARIF 目录（配置了才登记）、锚在 import 行 →
  `-diff` 天然只卡新增边，`metricgate: ignore import-cycle ...` 可定点豁免；
  `architecture.cycles` / `architecture.edges` 单独定级（默认 warning，不吓人）
- context 同源：`arch.Summary(policy)` 渲染成 `layer X (…) may depend on: …` /
  `forbid A -> B: reason` 行，进 `metricgate context` 与 MCP `get_constraints`

**自吃狗粮的真实产出**：给自己打开架构层后，立刻报出
`internal/engine → internal/diff`（core 依赖了会 `os/exec` shell out 的包），
即"core 是纯库"这条不变量当时并不成立。处理方式不是放宽规则，而是**真拆分**：
`internal/diff` 只留纯区间运算（Range/Intersects/Len，归 core），
git 相关（ResolveBase/ChangedLines/parseDiff/exec）搬到新包 `internal/git`（归 adapter），
CLI 与 MCP 改调 `git.*`。仓库现 policy 里落的三层模型：
`cli → core/adapter`、`core → core`、`adapter → core/adapter`，
外加 `forbid internal/** → cmd/**`（谁都不许依赖 CLI 壳）；当前 0 违规，闸门通过。

测试：`internal/arch` 20 个用例（模式匹配、解析方向、外部 import 丢弃、
SCC、分层/禁止/优先级、Summary）+ 引擎端到端 5 个用例
（`testdata/arch` 三包真实循环、锚点行号、`testdata/archsup` 定点豁免并计入
`Ignored`、未配置时不污染规则目录、配置后进入 SARIF 目录）。
