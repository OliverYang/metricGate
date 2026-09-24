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

---

## 落地记录（Phase A-3：重复检测 / duplicate-code gate）

**已落地**（提交 `clones: Type-2 function clone gate + reuse hints (Phase A-3)`）：

- 新包 `internal/clones`（纯库，core 层）：
  - `Extract`：从 tree-sitter 树提取顶层函数/方法/构造器的 body 指纹（FNV-1a）。
  - 归一化：所有具名叶子（identifier/type/literal）折叠为同一占位 token；运算符与
    标点保留精确文本（`a+b` ≠ `a-b`）；注释（tree-sitter extra 节点）与布局不参与。
  - `Find`：按 `(语言, 指纹)` 分组，`min_lines`/`min_tokens` 双门槛过滤小块
    （默认 10 行 / 60 token），跨文件同语言成类，组内按文件/行/名排序，anchor 即首个。
  - `Summary`：每类一行 context 提示（"clone of X:N (name) also at … — reuse it,
    do not reimplement"）。
- policy 新增顶层 `clones:` 节（`enabled`/`min_lines`/`min_tokens`；0 值 = 用默认；
  `enabled: false` = 整个 pass 关闭）。config 层 `ClonePolicy()` 是唯一装配入口。
- 引擎接入（engine 桥接文件 `internal/engine/clones.go`，与 architecture 同模式）：
  - 每个文件 parse 时顺带提取指纹（`fileResult.cloneFns`），run 末尾一次分组；
  - finding 规则 id `function-clone`，severity warning，锚定在**重复方**的 body 区间，
    message 指向 anchor；anchor 本身永不报；
  - 完全走统一管线：SARIF catalogue、`metricgate: ignore function-clone` 定点豁免、
    `-diff` 新增重复闸门、趋势库。
- context pack / MCP `get_constraints`：facts.Collect 复用同一棵树顺带提取指纹，
  Render 在 constraints 之前渲染 `## reuse before you generate` 区。
- 自吃狗粮：首次开启即在自家仓库抓到 4 个真实克隆
  （`stubs_test`/`metrics_test` 复制了 `facts_test` 的 parse helper；`model.go`
  两个排序函数同构；`go_rules`/`java_rules` 的 empty-block 规则同构）。
  全部按真实重构处理：新增 `internal/parsetest` 共享 parse helper、`model.go`
  改为声明式 key 比较器、新增共享 `newEmptyBlockRule` 工厂——没有放宽任何阈值。
- 性能实测（Go toolchain `src/`，5606 文件，M3）：开 clones 13.27s vs 关 12.26s，
  约 +8%，与 architecture pass 的 +6.4% 同量级；指纹提取复用 per-file parse，
  分组阶段是 O(n) 哈希桶。
- 已知边界（诚实记录）：
  - Type-2：语句级重排、字段访问归一化（`a.b.c` 全折叠为 id）会漏报/误报到什么程度
    取决于归一化粒度；当前粒度在无实测误报前提下偏保守（`+`/`-` 保留精确）。
  - 窗口检测（winnowing）与"新增代码与既有代码部分重复"（<100% 相同）留给
    下一轮，需要先积累 diff-only 模式下的真实误报数据再调粒度。

---

## 落地记录（Phase A-4：Java CK 类度量）

**已落地**（提交 `metrics: CK class metrics for Java (Phase A-4)`）：

- 新文件 `internal/metrics/classes.go`（core 层，纯测量，policy-free）：
  - `ClassStats{Name,Kind,StartLine,EndLine,WMC,NOM,NOF,LCOM,CBO,DIT}`，挂在
    `FileStats.Classes` 上，`AnalyzeFile` 一次解析顺带产出（Go 为空）。
  - **WMC**：类/接口全部方法+构造器圈复杂度求和；无 body（abstract/interface
    方法）记最小值 1——承诺的分发也要计入。
  - **NOM**：直接声明的方法+构造器数；嵌套类型归属自身。
  - **LCOM**：原始 CK 定义 max(0, P−Q)，字段使用按名字匹配；局部变量遮蔽
    只会抬高共享度 → LCOM 偏低（保守方向）。
  - **CBO**：类引用的非自身、非基本类型的不同类型名数量（JDK 类型计入——
    对平台的耦合也是耦合）；嵌套类型是边界。
  - **DIT**：同文件继承链深度；跨文件父类记 1 层（跨文件解析属于未来的
    符号索引）；`implements` 不加深，`interface extends` 加深；环有防御。
- 默认阈值（`metrics.DefaultThresholds`）：WMC 40/60、NOM 20/30、
  LCOM 20/40（advisory 语义，先观察再收紧）、CBO 15/25、DIT 5/8。
- 引擎：类级快照与 findings 完全走既有 metric 管线；finding 锚定类声明行；
  顺带把 `metricFindings` 重构为 `metricVerdict`+`scopeRange`（本轮自吃狗粮
  报出该函数 cognitive 31 > error 25，真实重构而非豁免）。
- context/MCP：Java 的 limits 区多渲染 5 条 CK 限额（Go 不渲染）。
- 测试：`internal/metrics/classes_test.go`（Alpha 精确值断言、split-brain
  LCOM、平凡类为 0、DIT 链/跨文件/接口语义、Go 空输出）+ 引擎端到端
  （fixture `testdata/ck/SplitBrain`，快照存在 + lcom warning 锚定第 5 行）。
- 自吃狗粮结果：`metricgate check .` 0 errors / 20 warnings，比 A-3 基线
  23 还低 3（重构顺带消掉旧债），gate PASSED。
- 已知边界（诚实记录）：全部 name-based（无类型解析）；LCOM 对 setter/getter
  泛滥型代码会偏高，属于真实设计问题而非误报；跨文件 DIT 低估不高估。

---

## 落地记录（Phase A-5：符号索引 v1）

**已落地**（提交 `symbols: cross-file definition index, near-miss hints, located stubs (Phase A-5)`）：

- 新包 `internal/symbols`（core 层，零项目依赖、零 tree-sitter 依赖）：
  - `Def{Name,Receiver,Kind,File,Line,Signature,Language,Package,Visibility}`；
    `Build(defs)` 构建不可变索引；`Lookup` 精确名查找；`Similar`/`SimilarElsewhere`
    近似名建议。
  - 相似度优先级：精确 > 大小写折叠 > 小编辑距离 > 包含（≥5 字符）；
    编辑距离用 restricted Damerau-Levenshtein（OSA）——相邻换位记 1 距离，
    "Rneder"→"Render" 是一键盘移的 typo 而非两个错误（这个 bug 是 context-pack
    端到端测试抓出来的）。
  - 打分上限随查询长度收缩（≤4:1，≤9:2，>9:3），防止短名泛滥匹配。
- facts 桥接（adapter 层）：`Defs` 把事实表展平为可索引定义（Java 类成员
  以类名为 receiver 独立成条），`Index(files)` 一次构建，
  `CollectWithIndex(root)` 一次遍历同时返回事实与索引；
  `SimilarSymbols` 生成"已有符号，对齐勿重造"提示（精确孪生优先于近似）。
- 三个消费端：
  - **context pack / MCP `get_constraints`**：新增
    `## existing symbols (align, do not reinvent)` 区（在 clone 复用区之后）；
  - **MCP `find_symbols`**（新工具）：exact + suggestions + 索引规模；
  - **MCP `list_stubs`**：每条 stub 附 `at` 字段（定义点 `file:line (签名)`），
    修复工单直接指向待补的函数体。
- CLI：`metricgate symbols [path] -q NAME [-max N]`——回答"这个 helper
  是不是已经存在"；无名命中时给 near-miss 建议。
- 测试：`internal/symbols/index_test.go`（精确/大小写/换位/包含/松匹配拒绝/
  确定性排序/nil 安全），`internal/facts/symbols_test.go`（跨文件查找、
  Java receiver、context pack 渲染、提示内容）。
- 已知边界（诚实记录）：索引 = 单次遍历边界内的文件（没有跨 repo /
  vendor 解析）；成员索引来自 facts（interface 方法集与类成员），裸函数
  与方法全覆盖，局部变量不在索引内；`SimilarElsewhere` 排除自身文件以
  避免自指提示。

---

## 落地记录（Phase D3：Delegate Bundle / `metricgate bundle`）

**已落地**：`internal/bundle`（adapter，core 之外）+ CLI `metricgate bundle`
+ MCP `get_review_bundle`，共用唯一 builder `bundle.Build`（CLI/MCP schema
不可能漂移）。

- **Git 数据面补齐**：设计文档指出的缺口（`ChangedLines` 只有区间、无
  unified diff/hunks）已补：`internal/git` 新增
  `LoadChanges(ChangeRequest) ([]FileChange, error)`，支持
  `workspace | range | commit` 三模式，返回 repo-relative path / status /
  hunks；`ChangedLines` 改为由同一解析结果派生（
  `ChangedLinesFromChanges`），于是 bundle 的 ranges 与 `-diff` 过滤管线
  **输入同源**。shell-out 仍只在 `internal/git`。
  真实临时仓库测试覆盖 staged+unstaged、untracked、rename、delete、
  含空格/tab 的路径、range/commit、NUL name-status 畸形记录拒绝。
- **bundle schema**：`schemaVersion / run{root,mode,base,head,commit} /
  policy{digest,source} / selection{candidates,included,excluded,unsupported,
  decisions} / budget{requested,used,counter,exact,budgetExhausted} /
  constraints[]（扁平 rule|metric|constraint） / files[]{path,status,language,
  diff{ranges},facts?,source?} / pack{included,counter,exact} / omitted[] /
  instructions{findingSchema,onlyReportChangedCode}`。
- **预算即契约**：预算作用于**完整序列化 JSON envelope**，不是内部片段。
  原子项先按 required→priority→id 装箱（constraints 与当前 diff 为 required，
  绝不静默丢失）；可选 facts 按确定性顺序移除直到 fit，否则返回结构化
  `budget_exhausted`。`pack.Included/Omitted` 记录选取结果；facts 只有在
  packer 收纳时才挂到 `files[].facts`，避免“报告已包含”却实际丢失。
- **隐私/路径**：默认只发 diff + 选中 facts；完整源码需 `-include-source`
  显式开启；`run.root` 归一化为相对路径，绝对机器路径不进入 bundle；
  `-out` 以 `0600` 写入并提示可能含源码 diff；不联网、不调 LLM。
- **计量诚实**：`-max-chars` 为精确 UTF-8 字节数；`-token-budget` 用
  bytes/4 估算并在 `budget.exact` 标 `false`，不声称硬 token 上限。
- **测试**：`internal/bundle` 8 个用例（workspace schema/selection、
  deterministic bytes、range、commit、budget 溢出、envelope 预算、
  绝对路径不泄漏、directory 模式、omitted 与 files 分离）；
  MCP JSON-RPC 冒烟确认 6 个工具与 CLI 同构。

**顺带修掉的真实仓库缺陷**：`.gitignore` 首行 `metricgate` 把
`cmd/metricgate/` 目录一并忽略，导致 CLI 主源码 `main.go` **从未被 git
跟踪**（dogfooding 抓出）。已改为 `/metricgate`、`/metricgate-mcp`
只忽略根目录二进制，并纳入 `.pi/`。

**自吃狗粮（真实重构，不放宽阈值）**：D3 落地后自家闸门先报 11 个 error，
逐个真实修复：拆 `Build` 为 `validate/collectChanges/normalizeFacts/
addFilesAndItems/fitEnvelope`；`cmdBundle` 抽独立文件并按
`bundleFlags.request/emit` 分解；`cmdRules` 文本渲染抽
`writeExplainText/printExplainItem`；拆分超标的测试函数；新增
`internal/gittest` 共享真实 Git 测试 fixture 并让 git/bundle 测试复用，
消除 3 个 Type-2 clone。当前 `metricgate check .` 为
`0 errors, gate PASSED`。

---

## 落地记录（Phase D4：Evidence Anchor）

**已落地**：`model.Finding` 新增可选 `evidence`（`startByte/endByte/snippetHash/anchorStatus`），
把“确定性锚点”做成可机器校验的证据，而不是只有行列号。

- `model.Evidence` + `model.NewEvidence(src,start,end)`：对**原始字节区间**
  直接 SHA-256（不 trim / 不 normalize）。编辑使 span 位移或改写 → hash 变 →
  宿主可判定锚点失效。span 越界时降级为 `line_only`，不谎报精确位置。
- 锚点级别：`exact`（规则命中 AST 节点，有 byte span + hash）/
  `line_only`（度量、parse/read 错误、架构、clone 等只有行号）/
  `unanchored`（保留枚举）。
- `rules.Rule` 新增 `AnchorCapture`：声明哪个 capture 的节点区间成为锚点
  （`empty-block→blk`、`todo-comment→c`、`banned call→call`、`banned import→path`、
  Java `print-stack-trace→call`）。`Runner.Run` 集中 `attachEvidence`，
  规则回调只管匹配决策，不各自算 hash。
- `Engine.Run` 与 `Engine.CheckSource` 末尾统一 `ensureEvidence`：所有 finding
  都有 `anchorStatus`（有 span 的保持 exact，其余标 line_only），宿主无需从
  “字段缺失”去猜。
- 出口：SARIF `result.properties.evidence`（locations 仍是权威编辑器位置，
  不改 schema）；check JSON 的 finding 内联 `evidence`。
- 测试：`CheckSource` 锚点（empty-block=exact 且 span 命中 `{ }`、
  metric=line_only、所有 finding 都有锚点）；hash 随 span 变化；
  越界降级；SARIF properties 携带 evidence 且 location 不变。
- 实测：`check . -format json` 与 `-sarif` 中 `empty-block` 的
  `snippetHash` 与 `python3 hashlib.sha256(src[42:46])` 完全一致（span 为 `{\n\t}`）。

D3+D4 至此完成：bundle（CLI+MCP 共用 builder）+ evidence anchors 都落地；
自家闸门 `metricgate check .` = 0 errors, PASSED。

---

## 落地记录（Phase E：狗粮 / 可证伪验收 / 量化报告）

**已落地** `docs/evaluation-phase-d.md`（D0–D4 的落地清单 + 可证伪验收 + 量化），
对应设计文档 §5 的 E 阶段“3 repo fixture；selection parity；hard constraint
recall；token char/estimate delta；耗时；隐私扫描”。

- **可证伪验收固化为测试**（不是只写在文档里）：
  - preview/Run included set 逐路径相等（既有 D0 测试）；
  - bundle 逐字节 deterministic（实测 `cmp` IDENTICAL + 单测）；
  - diff range 可作 `FilterSummary` 输入（`TestBundleDiffRangesFeedFilterAndEnforceHardConstraints`：
    1-based、变更文件有 range、未变更文件不在变更集）；
  - hard constraint 100% 覆盖（同测试：每个选中语言的 rule 都出现在 bundle constraints）。
- **量化（本机实测，语料=本仓库自身 112 文件）**：
  - 整仓源码 425,276 字符 vs bundle compact JSON **6,832 字符 = 1.6%（降 98.4%）**；
    诚实标注这是“整仓全量发送”基线的对照，单文件全文基线约 8.2k，优势收窄。
  - `bundle -max-chars 6000` 0.06s（3 次稳定）；预算 6000→used 4611 装入，
    2000/600→结构化 `budget_exhausted`（required 不被静默截断）。
  - evidence hash 与 `sha256(src[42:46])` 独立复算一致。
- **隐私扫描**：绝对 root 下 bundle/context/MCP 出站工件 0 绝对路径泄漏（`SanitizePath`）。
- **未验证（诚实声明）**：LLM 生成质量/成本的改善仍需真实 agent A/B
  （见 `docs/ab-experiment.md`）；本轮不写未测结论。

---

## 落地记录（研究驱动的 P1 迭代）

依据 `docs/research-notes.md` 的 P1 建议，已落地两项：

**P1① pack 排序契约**（依据 Lost-in-the-Middle, TACL 2024）：
- `facts.PackItem` 新增 `Order`，并定义 `OrderHeader/Constraints/Diff/Facts/Reuse/Symbols/Source`；
  packer 现在**显式**按 `(Order,Priority,ID)` 输出，admission 仍按 priority 选取。
  硬约束与当前 diff 因此稳定落在 prompt 前部。
- 可证伪测试：`internal/facts` 的文本顺序断言（header<constraints<facts）；
  `internal/bundle` 的 `TestPackOrderingContract`（included 首位是 header、constraints 先于 diff/facts）。

**P1② clone-drift 检测**（依据 *Consistent/Inconsistent Changes to Code Clones*, WCRE 2007）：
- 新包 `internal/clonedrift`（纯库）+ `git.Archive`（`git archive` 经 stdlib tar 解包 base 到临时目录，
  不依赖系统 tar 二进制）。
- `metricgate clonedrift [path] [-base ref] [-format json]`：对比 base 与工作树的 clone 分组，
  仅当两侧都还在、恰有一侧落在 diff 内、且 body 已分叉时报 `clone-drift`（warning）。
  删除一侧=去重、两侧同步修改=仍克隆，都**不**报。
- 测试：一方被改（报）、未动（不报）、对称修改（不报）、删除（不报）、禁用（不报）；
  端到端 CLI 冒烟通过。

两项均 `metricgate check .` = 0 error, gate PASSED。
