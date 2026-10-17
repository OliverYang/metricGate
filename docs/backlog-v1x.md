# metricGate 后续方向候选（v1.x backlog）

> 记录日期：2026-10-11。来源：一轮"外部图源 vs 自身能力"的只读调研
> （含实测对比，证据见下文每条的 file:line 与命令结果）。
>
> 本文只**记录**候选方向与已确认的事实，**不代表授权实现**。优先级为建议，
> 动工前需重新确认范围。
>
> 状态约定：⬜ 待办 · 🔒 搁置（已决定不做，记录以免重复评估）

---

## 候选 1 · git 历史共变（temporal coupling）✅ 已完成（迭代 6）

### 定位

补的是**能力洞**而非语言洞：静态依赖图**原理上**看不见"两个文件没有 import 关系
却在版本历史里锁死"。这正是架构腐蚀的教科书信号（Tornhill 的 change coupling）。

### 现状证据

- 已有 churn 但**丢掉了共变信息**：`internal/hotspots/hotspots.go:48` 的
  `gitLog` 只跑 `git log --numstat --no-renames --format=%x01`，
  `parseNumstat`（`internal/hotspots/hotspots.go:63`）只把行数折成 per-file churn，
  同 commit 内文件之间的共现关系被丢弃。
- 与 `internal/arch` 的关系：`arch` 只看 import 边（`internal/arch/graph.go` +
  `internal/arch/check.go`），因此**看不到共变**；两者是互补视角，不是重复。

### 实测（pilot 仓 `gitee-scan-2.0-portal`，5636 commits，**非浅克隆**）

```
git log --numstat 全量解析: 4.50s / 5636 commits / 2186 distinct files
median files/commit: 1
共变对（同 commit 内）: 12139 对；其中 >=8 次的 240 对，跨包 100 对
```

抽样结果（说明是真信号而非噪音）：

| 共变次数 | 文件对 | 判读 |
|---|---|---|
| 38× | `TaskMapper.java` ↔ `TaskMapper.xml` | **真信号**：AST 图上两者毫无关系 |
| 28× | `TaskServiceImpl.java` ↔ `TaskMapper.xml` | **真信号** |
| 51× | `RuleMapper.xml` ↔ `RuleMapper.xml` | **真信号但同名**：跨模块联动，须用全路径而非 basename |
| 123× | `application-dev.yml` ↔ `application-poc.yml` | **噪音**：配置镜像，需过滤 |

### 为什么契合 metricGate 既有形状

1. 与 `hotspots` **同源**（同一次 `git log --numstat`），是同一数据源的第二个投影，
   不需要额外 git 调用。
2. 直接进现有 `.metricgate.yaml` 阈值体系。
3. 天然适配 `-diff` ratchet 语义：拦截口径是"**新增**的跨层共变对 > N"，
   而不是存量对（存量必然一大片）。
4. **13 种语言全覆盖**——git 不挑语言。

### 必做护栏

- **浅克隆必须显式拦**：`git rev-parse --is-shallow-repository` 检测；浅克隆上
  `git log` 会**静默给出偏小的共变计数**（甚至 0），属静默错误，
  必须报错或显式降级，绝不静默。（CI 里 `fetch-depth: 0` 已经踩过同类坑，
  见 `docs/ci-cd-integration.md`。）
- **整仓 sweep 过滤**：单 commit 改动文件数超阈值（如 >12）的提交不计入共变，
  否则一次格式化/重构会污染全部计数。
- **生成/配置文件过滤**：锁文件、`application-*.yml` 类镜像配置需排除。
- **路径用全路径**：同名文件（`RuleMapper.xml` ×2）用 basename 会串。
- 重命名：现用 `--no-renames`；是否改 `-M` 需单独决策（影响历史连续性）。

### 动工前待定

- 是否入 `internal/store`（历史表 vs 每次现算）。
- 阈值语义与默认值。
- 与 `internal/arch` 分层图的交集定义（"跨层共变对"如何映射到 layer）。

---

## 候选 2 · facts 符号层补齐 7 种语言 ✅ 已完成（迭代 7）

> 交付与实测证据：[docs/iterations/iter-7-facts-symbols.md](iterations/iter-7-facts-symbols.md)。
> 结论：Py 仓 `impact -q BaseToolReport` 的 defs 0→1；`declKinds` 同时补齐，
> 并删掉了 `near.go` 里按名字+行号重定位 body 的 `findBody`/`isDeclKind`（对 C/C++ 与 C# 必然失败）。

### 定位

不是加新维度，是**补已付成本却未回收的部分**。

### 现状证据（根因明确）

`internal/facts/facts.go:216` 的 `parseFile` 对**全部 13 种语言**都做了解析，
并且填了两个跨语言字段：

```go
f := Extract(path, src, tree, lang)                                  // ← 只填 Go/Java
f.CloneFns = clones.Extract(lang, path, src, tree, clones.Default)   // 9 语言
f.Refs = refs.Extract(path, src, tree, lang)                         // 9 语言
```

而 `internal/facts/facts.go:57-59` 的 `Extract` switch **只有 `case LangGo` /
`case LangJava`** → `f.Symbols` 对其余 7 种语言为空。

`internal/refs/refs.go:49-61` 的分派覆盖全部 9 语言（Go/Java/TS+JS/Py/C+Cpp/CS/Rust），
`internal/clones/clones.go:73-77` 的 `Extract` 语言无关——**只有符号提取是 Go/Java 专属**。

### 实测后果

- Python 仓上 `impact` 基本不可用：`impact -q BaseToolReport -format json`
  → `defs=0 refs=1 affectedFiles=1`，note 为
  `no definition in this tree; the symbol is external (dependency or generated code)`。
  （对照：同仓同符号 Graphify 给 73 节点 / 37 文件。）
- 影响的调用面（全部走 `facts.Collect*`，约 20 个调用点）：
  `symbols` · `find_symbols` · `impact` · `get_impact` · `get_review_bundle` ·
  `get_constraints` · `get_repo_map`（`cmd/metricgate-mcp/main.go:100/215/262/297`、
  `cmd/metricgate/impact_cmd.go:31`、`cmd/metricgate/main.go:311/346/569/571/619/641` 等）。

### 附带发现（同根，可一并修）

`internal/clones/clones.go:62-65` 的 `declKinds` **也只有 Go/Java**，所以
`nearclones` / `clonedrift` 对其余 7 语言是死的——而指纹逻辑本身语言无关。

### 工作量估计（比"7 × extractGo"小得多）

- `extractGo` 在 `internal/facts/facts.go:265-429` 约 164 行（要处理
  struct/interface/method receiver/可见性），`extractJava` 在 `:430-500`。
- 通用符号提取器**不需要那么长**：`internal/metrics/kindset.go` 的
  `LangProfile.FuncKinds` 已给出各语言函数/类节点集合，缺的只是 name/params 字段查表。
- 唯一麻烦是 C/C++ 的 `function_definition` 没有 `name` 字段（要走 declarator 链），
  而该 helper（`cFuncName` / `cParamsNode`）**已在 `internal/metrics/metrics.go` 中
  为指标实现**，可直接复用。
- 量级估计 **150–250 行表驱动实现**。

### 注意

- 路径必须保持 root-relative：`internal/facts/facts.go:159-165` 已用
  `selection.SanitizePath` 重写 `File` / `CloneFns[].File` / `Refs[].File`；
  新提取的符号路径也要走同一处理，**不得引入绝对路径**（破坏确定性 + 污染 LLM 面）。

---

## 候选 3 · Graphify 图源适配 🔒 已决定搁置

> 结论：**不做**。以下是当时的评估结论与实测证据，保留以便将来需要时不必重跑调研。

### 结论

即使做，也只能作为**只读的补盲输入**（非 Go/Java 仓的 impact + 继承边），
不能当存储引擎，不能进 CI（Graphify 是 Python 包，会破坏"纯本地、零 key、
零依赖"卖点）。

### 关键实测

| 对比 | metricGate `impact` | Graphify |
|---|---|---|
| Py 仓 `BaseToolReport` | defs=0 refs=1 → **1 文件** | **73 节点 / 37 文件** |
| Java 仓 `ServerResponse` | defs=5 refs=352 → **69 文件** | 412 文件（depth 2，全关系） |
| 同上，仅 calls+inherits+implements | — | **1 文件** |

- Java 仓上 metricGate 的 69 个文件是 Graphify depth-1 的**精确子集**
  （overlap 69/69，mg-only 0）。
- depth-1 的关系构成：`references` 67 文件 + `imports` 65 文件 + `contains` 1 + `method` 1
  → **主要是 import 闭包，不是调用闭包**。有用信号完全取决于 relation 过滤 × depth 组合，
  而 CLI 给不了这个（`affected` 无 `--json`）。

### 若将来重启，必须处理的坑

1. **新鲜度会静默撒谎**：`built_at_commit` 需与 `git rev-parse HEAD` 比对
   （实测 pilot 仓 `b3a290db…` 与 HEAD 一致）；且 `.graphify_root` 可能指向**另一份
   checkout**（实测 `bugbye-parser` 的 `.graphify_root` 写
   `/Users/mac/Downloads/realo/main/bugbye-parser`，而图文件在
   `/Users/mac/Downloads/main/bugbye-parser`）。root 不存在时**必须报错退出**。
2. **格式会漂且无版本字段**：`extract --code-only` 写出顶层 `edges` 且无 `directed`；
   跑过 `cluster-only` 后被重写为 node-link 的 `links` + `directed:false` + `graph`
   + `multigraph`。Graphify 自己源码里就有兼容垫片（`build.py:807-809`、
   `paths.py:415-435`）。适配器须同时接受两种键名并记下实测版本（本机 0.9.53）。
3. **`directed: false` 是正确性地雷**：`graphify path A B` 直接拒答并提示
   `--undirected`。**绝不能**把无向边喂进 `internal/arch/check.go` 的分层/环检测
   （无向边分不清 A→B 与 B→A，会把依赖倒置判反）。
4. **`INFERRED` ≠ LLM**：`--code-only`（零 key）照样产出 14/179 条 INFERRED，
   `_origin=ast`、`confidence_score=0.8`——是启发式猜的。规则应写"只信 EXTRACTED"，
   而不是"只信 ast"。
5. **不要 shell 调 CLI**：`affected` / `explain` / `path` 均无 `--json`
   （只有 `god-nodes --json` 和 `diagnose multigraph --json` 是机器可读的）。
6. 节点 ID 是 `<repo>::<path_slug>_<label_slug>` 的不可逆拼串
   （`LockParam` / `lockparam` 会撞），**不能反解路径**；join 只能走 `source_file`。

### 已证伪的一条收益路径

早期判断"Graphify 的 `rationale_for` 边（聚合 918 条）能补 `docs/pilot` 里 13 条
未转化条款"——**数据不支持**：`rationale_for` 只存在于 4 个 Python 仓
（bugbye-scanner 266 / bugbye-parser 347 / bugbye-miserver 114 / bugbye-stylecheck 168），
而 13 条条款所在的 `gitee-scan-2.0-portal` 是 **0 条**。

---

## 候选 4–6 · 第二轮广度调研（9 方向 → 3 个入选）✅ 已完成（迭代 8）

### 定位

候选 1/2 完成后做了一轮覆盖 9 个方向的广度调研
（→ [docs/research/direction-survey-2026-10-12.md](research/direction-survey-2026-10-12.md)），
从中实测选出 3 个高价值方向（→ [docs/research/t4-direction-verification.md](research/t4-direction-verification.md)），
需求分析与方案设计见 → [docs/research/t5-requirements-and-design.md](research/t5-requirements-and-design.md)，
实现与验收见 → [docs/iterations/iter-8-t6-three-directions.md](iterations/iter-8-t6-three-directions.md)。

三个方向的共同判据是**同一个**：不是"能力不够强"，而是**已经付了成本的那部分能力被丢掉了**。

| 候选 | 方向 | 已付的成本 | 被丢掉的部分 |
|---|---|---|---|
| 4 | 继承/实现边（分析维度） | 9 语言 refs walker 都在跑 | 继承位置写裸类型名，只有 Go/Java 顺带捞到 |
| 5 | custom_rules 文件作用域（门禁控制） | `banned` 的 `except_files`/`allowed_files` 早已实现 | DSL 规则完全没有文件谓词 |
| 6 | root 一致性（门禁控制） | root 参数一路传进 `runDiffMode` | 最后一步没传下去，`-diff` 坐标系错配 |

### 实测锚点（入选理由全部绑定真实输出）

- **候选 4**：Python 仓 bugbye-parser 上 `impact -q BaseToolReport -format json`
  修前 defs 0 / refs 1 / affectedFiles 1 → 修后 **defs 1 / typeUses 36 / affectedFiles 37**，
  与 `grep -rlE '^class +[A-Za-z_][A-Za-z0-9_]*\(BaseToolReport' --include='*.py' . | wc -l` = 36 对齐。
- **候选 5**：本仓 `custom.go-test-func-naming` 误报 **6 → 0**（含生产文件
  `internal/gatecheck/gatecheck.go:135 TestsTouchedCheck`）；反指标
  `/tmp/d2anchor` 里 `func testbad(t *testing.T)` 仍被抓。
- **候选 6**：同仓同 commit，`check <子目录> -diff` 修前 `0 new / 1 hidden`（假绿）
  → 修后 `1 new / 0 hidden`；单文件 root + `-diff` 修前直接崩
  （`fork/exec /usr/bin/git: not a directory`）→ 修后正常。

### 淘汰的 6 个方向（记录理由，不重复评估）

- **bundle 预算不足**：本仓 `bundle .` 直接 `budget_exhausted`——但这是预算调参问题，
  不是能力缺失，且调大预算会破坏"上下文可控"这个卖点。
- **MCP 索引缓存**：`get_impact` 每次重建整索引（bugbye-parser 上约 0.69s）——
  收益是**延迟不是能力**，且引入缓存新鲜度风险，与"确定性"卖点冲突。
- **存储保留策略**：`history.db` 约 609 KB/run，`archived` 只隐藏不回收字节——
  真实痛点，但属于运维配置而非分析能力，可由用户自行清理。
- **`internal/ci/` 空目录**、**`usage.go` cockpit 重复行**：文档/清理类，非能力缺口。
- **结果展示（Cockpit 项目维度）**：依赖尚未定论的项目身份方案。
- **`docs/claims.md` #15「闸门在真实 agent 循环被调用」**：是**证据缺口非代码缺口**，
  需要真实使用数据而非新代码。

### 已落地但未深挖的 4 个方向（判定为 🔒 已落地，不是缺口）

外部数据源（`internal/feed` 经 `scripts/osv2policy/main.go` 消费）、
CI 流程控制（3 个 workflow + composite action）、
非标输入转换（`internal/extract`/`internal/rulegen`/`internal/feed`）、
审计（`internal/engine/suppress.go` 的 ignore 指令 + `.metricgate/accepted.yaml` 账本）。

---

## 候选 7 · 九语言能力拉齐 ✅ 已完成（迭代 9）

### 定位

不是新方向，是**已付成本的能力被丢掉**：tree-sitter 语法、度量 adapter、规则包、
依赖扫描、符号层、重复检测、桩检测都移植到了 9 语言，但**消费这些事实的语义层**
停在 Go/Java。审计结论：全仓 13 个手写 `switch lang` / `map[LanguageID]`，
`parser.Supported()` 只被 `internal/engine/engine.go` 用来建 runner，
**没有任何测试断言「每个 Supported 语言在各层都有实现」**。
失败模式统一是**沉默的零**——「没违规」与「没看」在报告里长得一样。

### 实测锚点（修复前 → 修复后）

- **`metricgate: ignore` 在 7/9 语言静默失效**（最严重：安全阀失效）：
  TS `// metricgate: ignore cyclomatic-complexity` → 仍报 1 条；修复后 0 条。
  根因 `internal/engine/suppress.go` 的 `commentKinds` 只注册 Go/Java 的注释 kind。
- **arch 在 7/9 语言静默假绿**：同构 TS 仓 `arch snapshot` = `2 packages, 0 dependency direction(s)`、
  `check` 0 errors 2 warnings；Go 同构 = `2 packages, 2 dependency direction(s)` + `import-cycle` + `layer-violation`。
- **CK 类级度量 Java-only**：TS 类 `metrics` 只有文件级行；修复后 `wmc of "Store" is 3` / `cbo of "Store" is 2`。
- **`banned-api` 在 7/9 语言无效**：TS `fetch(` 0 findings；修复后 `banned-fetch`。
- **`boolean-params` 在 TS 恒 0（真 bug）**：判定写成 `type_identifier == "boolean"`，
  而实测语法树是 `type_annotation → predefined_type → boolean`，无 `type_identifier` 节点。
- **context/constraints pack 缺该语言约束节**：TS 文件只输出 repo map + 符号，无 `## 约束`；
  修复后 14 条 limit/rule 行（含 CK 家族）。

### 动工前待定

无——m08341 用户直接授权「凡是缺失的这些能力，各种语言都应该拉齐才对，把它补充上吧」。

---

## 明确不做（本次一并记录）

- **Graphify 适配**（候选 3）——已决定搁置。
- **把存储换成图数据库**（Neo4j / JanusGraph / Dgraph / Kuzu / Cayley）——
  破坏"纯本地零 key"卖点；数据量（约 42k 边/次）远低于图数据库价值区间；
  图本身是每次可重建的物化视图。
- **把 Python 运行时拉进 CI**——同上，损害零依赖卖点。

---

## 优先级建议

| 顺序 | 候选 | 理由 |
|---|---|---|
| ~~1~~ | ~~git 历史共变~~ | ✅ 已完成 → [docs/iterations/iter-6-coupling.md](iterations/iter-6-coupling.md) |
| ~~1~~ | ~~facts 符号层补齐 7 语言~~ | ✅ 已完成 → [docs/iterations/iter-7-facts-symbols.md](iterations/iter-7-facts-symbols.md) |
| ~~2~~ | ~~继承/实现边（分析维度）~~ | ✅ 已完成（迭代 8）→ [docs/iterations/iter-8-t6-three-directions.md](iterations/iter-8-t6-three-directions.md) |
| ~~2~~ | ~~custom_rules 文件作用域（门禁控制）~~ | ✅ 已完成（迭代 8）→ 同上 |
| ~~2~~ | ~~root 一致性（门禁控制）~~ | ✅ 已完成（迭代 8）→ 同上 |
| ~~3~~ | ~~九语言能力拉齐（arch/CK/banned/suppress/digest/context）~~ | ✅ 已完成（迭代 9）→ [docs/iterations/iter-9-language-parity.md](iterations/iter-9-language-parity.md) |
| — | Graphify 适配 | 🔒 搁置 |
| — | 存储保留策略 / MCP 索引缓存 / bundle 预算 | ⬜ 已评估未入选（理由见候选 4–6 节） |
| ~~3~~ | ~~impact 传递闭包 + 命中可信度分层~~ | ✅ 已完成（迭代 11）→ [docs/iterations/iter-11-impact-reachability.md](iterations/iter-11-impact-reachability.md) |
| ~~3~~ | ~~reachability（死代码 + 测试触达）报告~~ | ✅ 已完成（迭代 11）→ [docs/iterations/iter-11-impact-reachability.md](iterations/iter-11-impact-reachability.md) |
| ~~—~~ | ~~闭环易用性：P0 cockpit 读库 / P0 MCP preamble 吞载荷 / P0 accept 对 error 静默 no-op~~ | ✅ 已完成（迭代 12）→ [docs/iterations/iter-12-closed-loop-usability.md](iterations/iter-12-closed-loop-usability.md) |
| — | 让步系统问责化：inline directive 加 owner/reason/expiry + 过期告警 | ⬜ 遗留，见迭代 12 日志「遗留」节 |
| — | ~~MCP `check_path -diff_only` 改用 value-delta 棘轮（与 CLI 对齐）~~ | ✅ 已完成（迭代 20）→ [docs/iterations/iter-20-mcp-cli-ratchet-parity.md](iterations/iter-20-mcp-cli-ratchet-parity.md)：基线取数抽到 `internal/baseline`，两入口同走 `baseline.Split`；同一脏树实测 MCP 由 3 条收到 1 条（与 CLI 全等） |
| ~~—~~ | ~~TSX / JSX 支持~~ | ✅ 已完成（迭代 13）→ [docs/iterations/iter-13-tsx.md](iterations/iter-13-tsx.md) |
| ~~—~~ | ~~TS/TSX 箭头函数不计入函数级度量（`const C = () => {}` 无 cyclomatic/cognitive 快照）~~ | ✅ 已完成（迭代 29）→ [docs/iterations/iter-29-midterm-backlog-audit.md](iterations/iter-29-midterm-backlog-audit.md) §2.4：改为「模块级绑定的函数表达式算一个单元」的结构判据（不是「凡箭头函数都算」，否则 `a.map(f)` 会被双算）。本仓库自食其果：`app.js` 里 27 个此前不可见的函数被量到，其中 3 个真的越了 error 阈值，已重构收敛 |
| ~~—~~ | ~~策略指纹落库（`runs.policy_digest`）~~ | ✅ 已完成（迭代 14）→ [docs/iterations/iter-14-policy-fingerprint.md](iterations/iter-14-policy-fingerprint.md) |
| ~~—~~ | ~~组织策略 `extends:` 单继承~~ | ✅ 已完成（迭代 15）→ [docs/iterations/iter-15-extends-and-trace.md](iterations/iter-15-extends-and-trace.md) |
| ~~—~~ | ~~`trace_id` 贯通 runs/findings/snapshots + calllog + bundle + `runId` 输出~~ | ✅ 已完成（迭代 15）→ 同上 |
| — | `trace_attrs`（模型名 / prompt digest / harness）与 LLM 侧维度自动采集 | ⬜ 遗留，见迭代 15 日志「遗留」节 |
| — | ~~MCP `check_path -diff_only` 改用 value-delta 棘轮（与 CLI 对齐）~~ | ✅ 同上（迭代 20）；两处重复登记的是同一件事，当时共同的阻塞项「先把基线取数从 CLI 里搬出来」已完成 |
| — | arch 包名规则做成机制（当前按语言手写分支） | ⬜ 遗留，见迭代 9 日志「遗留」节 |
| — | blast-radius 进闸门（impact-scope 带 tier 过滤） | ⬜ 遗留，见迭代 11 日志「遗留」节 |
| ~~—~~ | ~~`impact.go` 中 `affectedFiles`/`fileSet` 泛型化后重复~~ | ✅ 已清理（同一轮） |
| ~~—~~ | ~~reachability / impact 读 policy `exclude`~~ | ✅ 已修（`CollectSelectedWithRefs`，见迭代 11 日志） |
| ~~—~~ | ~~配置发现链：`.metricgate.yaml` 隐藏文件 + 就近退化 + 停在 Git 根~~ | ✅ 已完成（迭代 16）→ [docs/iterations/iter-16-config-discovery.md](iterations/iter-16-config-discovery.md) |
| ~~—~~ | ~~Cockpit 策略可查可解析可改（发现链 / 分节 / 生效键出处 / yaml 校验原子写）~~ | ✅ 已完成（迭代 16）→ 同上 |
| — | 发现链 CLI 查询动词（`metricgate policy show`）；MCP 侧策略工具 | ⬜ 遗留，见迭代 16 日志「遗留」节 |
| — | 保存 `.bak` 只有一份且**每次覆盖**（实测：连续 3 次保存后目录里只有 `.metricgate.yaml` + `.metricgate.yaml.bak`，bak 内容是**上一版** v2，不是 v1 的累积） | ⬜ 遗留，见迭代 16 日志「遗留」节。**原描述「每次保存追加一份」有误**：`writePolicyAtomic`（`internal/cockpit/policy_write.go:194`）写的是固定名 `target+".bak"`，`os.WriteFile` 是覆盖语义 ⇒ 无轮转上限的前提（会累积）不成立，真实缺口是**只能回退一步** |
| ~~—~~ | ~~CODEOWNERS 归属：解析 + `reachability`/`impact` 归属列 + `rules gaps` 按 owner 分组~~ | ✅ 已完成（迭代 17）→ [docs/iterations/iter-17-ownership-and-trend-window.md](iterations/iter-17-ownership-and-trend-window.md) |
| ~~—~~ | ~~`trends` 可用性：`-out / -since / -project`~~ | ✅ 已完成（迭代 17）→ 同上 |
| — | Cockpit 侧 owner 视图（复用 `internal/owners`，需要前端交互验证） | ⬜ 遗留，见迭代 17 日志「遗留」节 |
| — | `internal/owners` 不认 GitHub 支持的自定义 CODEOWNERS location | ⬜ 遗留，见迭代 17 日志「遗留」节 |
| ~~—~~ | ~~`trace_attrs`（X2）KV 白名单 + `trace` 组合报表（X3）~~ | ✅ 已完成（迭代 18）→ [docs/iterations/iter-18-trace-attrs-and-report.md](iterations/iter-18-trace-attrs-and-report.md) |
| — | X4 LLM 侧元数据自动采集（模型名/prompt digest 由宿主设 env，工具不探测） | ⬜ 遗留，见迭代 18 日志「遗留」节 |
| — | Cockpit trace 过滤只作用于调用视图，未作用于 run 历史视图 | ⬜ 遗留，见迭代 18 日志「遗留」节 |

> 注：本文与 `docs/roadmap.md`（审美回路 5 迭代计划，已全部完成）正交，
> 是 v1.x 之后的候选池。
