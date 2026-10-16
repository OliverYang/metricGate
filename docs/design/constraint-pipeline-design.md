# 规约 → 约束原语 → 预检门禁 → 流程硬控：四段流水线设计

> 定位判断（用户主张，2026-09-29 确认）：**系统内置检查名单只作为基础默认兜底**；产品主轴是正向流水线
> —— 非标规约/文档/报告 → 统一【约束】原语 → 工程预检门禁落地 → 代码级流程硬控。
> 存在理由一句话：**没有这条流水线，一份规约只是一本没人检查的 PDF**（从 0 到 1，不是从慢到快）。
> 方法论出处：[`../constraints/constraint-conversion-sop.md`](../constraints/constraint-conversion-sop.md)；
> 理论框架：[`../research/llm-constraint-paradigm.md`](../research/llm-constraint-paradigm.md)。

## 1. 架构图

```
 非标输入                    约束原语层                    门禁层                      硬控层
┌──────────────┐   S1-S3   ┌──────────────┐   S4-S5    ┌──────────────┐   S6     ┌──────────────┐
│ 规约手册章节  │  提取/转化  │ .metricgate.yaml  │  烟测/试点   │ fixture 烟测  │  消费    │ 生成前 digest │
│ 团队规范分册  │ ────────▶ │  六族原语词汇  │ ─────────▶ │ FP 审计       │ ──────▶ │ 生成后 check  │
│ 事故复盘报告  │  人+AI辅助  │ (A/B/C/D/E/F)│  独立policy │ rules-gaps    │ 闭环     │ + scorecard  │
│ 论文/外部标准 │           │  reason 溯源  │            │ 豁免纪律      │         │ findings 修复 │
└──────────────┘           └──────────────┘            └──────────────┘         └──────────────┘
      ▲                           │                          │                       │
      │        ❌ 台账 + 复评触发  │     零命中规则不可信      │    观察期定级          │  红线 =0 才放行
      └───────────────────────────┴──────────────────────────┴───────────────────────┘
                    演进回路：违规聚类 → 原语缺口聚类 → DSL 扩词汇 → 重新转化
```

内置名单（builtin 规则包 + 默认阈值）位于门禁层**之下**作为兜底：阈值是地板（policy 只能抬高），
preset 是默认包——项目自己的 policy 永远是第一公民。

## 2. 各段实测地基（不重新造轮子，只补缺口）

| 段 | 已有 | 实测证据 | 状态 |
|---|---|---|---|
| 输入→原语 | SOP 五步（S1-S3）；conversion-map 40 条实例 | 15✅/5⚠️/20❌，❌聚类收敛到 4 个缺失原语 | ⚠️ 有人肉流程，无工具面 |
| 原语→门禁 | policy 独立编译、fixture 烟测、FP 审计、rules-gaps、豁免纪律 | 试点 104 条 policy-FP 修完归零；烟测抓出 2 个引擎语义 bug | ✅ 已验证 |
| 门禁→硬控 | digest 注入 + check/scorecard 验收 + findings 修复回路 | R3 首过违规 31.8%→4.5%；R2 修复 7/7；复合终态 0/22 | ✅ LC-403/404 已闸门化（gatecheck，2026-09-30） |
| 演进回路 | 违规聚类、观察期定级、验收规约 v1→v2 | Java 试点 3 条观察期条款待数据定级 | ⚠️ 待第二次试点 |

## 3. 原语层定义（投资单位是原语，不是条款）

**统一约束原语 = .metricgate.yaml 可表达的最小规则类型**，当前词汇表六族：
A 禁 API/import（call 0-2 段 receiver + 前缀通配 / import 前缀）、B 架构（分层/forbid/环/SDP）、
C 度量阈值（Go+Java 全集）、D 重复（clone 三态）、F 依赖白名单、E 缺陷类（外包给适配器，不自研）。

原语入库判据（缺一不进词汇表）：fixture 可验（对已知违例命中+分级）、FP 可审计（试点归因流程覆盖）、
分级可门（error 进 fail_on / warning 进观察期 / report-only 进 scorecard）。
条款→原语的映射失败不是丢弃信号，是**原语缺口聚类信号**——20 条 ❌ 聚成 4 个缺口就是本设计 P0 任务的依据。

## 4. 任务分解（三个缺口）

### 缺口 1：转换环节没有工具面（T1）
**T1a 事故报告→约束流程**（最高价值密度，先行）：输入模板（编号/现象/根因/影响面/频次）→ 复用 S1-S3
条款化 → 优先级由「频次×影响」自动排序（人工手册答不了的“哪条值得建”，事故报告自带答案）。
验收契约（修订为三档各有处置）：✅≥1 条落地 / ⚠️ 给出近似档+观察期 / ❌ 进台账带复评触发。
**状态：一期已完成（2026-09-29）**——流程+模板+4 例试点（含 1 例自由文本事故）全部达成：
CONC-07 新增落地（真实项目 2 命中 0 误报），doc-03/ELK 复现确认，isDelete 进缺口台账；
见 [`../constraints/incident-to-constraint-playbook.md`](../constraints/incident-to-constraint-playbook.md)。
**T1b 访谈/提取套件**——工具面已落地 2026-09-30：`metricgate constraints extract -file <规范文档> -lang go|java -config .metricgate.yaml` 把非标文档自动条款化（markdown 表格/自由行拆分、代码围栏/引用块过滤），按禁令/要求语义判型三档（call/import/constructor+args_ban ✅、语义精确型 ⚠️ 需人工补 match、内置规则承载、正向要求 ❌ 台账），带 policy 查重，产出候选 YAML（SOP 步骤 2-5 前置烟测）；正向要求标记（必须/应当/统一使用…）处切分防语义反转（要求半句被当禁令目标）。验收剩余：非工程人员按 README 走通一条非标条款全流程（工具面已备，见缺口 4 检出情况）。

### 缺口 2：原语词汇表不够用（T2，DSL P0）
4 个聚类缺口的优先序（对应 conversion-map ❌ 条目）：
1. ✅ **参数感知禁令**（已落地 2026-09-30）：`banned-api` 条目新增 `args_ban: ["MD5","MD2"]` 字段——仅当字符串字面量参数命中禁值才触发，变量参数不触发；实况为 ban 编译器的 tree-sitter query 扩展（五个 call 查询补 argument_list 捕获 + Go 侧字面量过滤器），未引入独立 DSL 文件。SEC-01 已据此转化 ✅（fixture 烟测 + 真实项目 2 处命中 0 误报）；
2. ✅ **构造器禁令**（已落地 2026-09-30）：`kind: constructor` 新 BanKind——Java `new X()` 是 object_creation_expression 非 method_invocation，call ban 天然不可见；新查询匹配 `type: (type_identifier)`（简单类名，尾随 `*` 前缀匹配）。Java-only（Go 无此缺口）。CONC-02 据此获得第二通道（import ban + 实例化点禁令，观察期 warning）；诚实边界：FQN 写法 `new java.text.SimpleDateFormat()` 的 scoped type 节点仍是残余缝隙，靠配对 import ban 兼底（与 call 深度 receiver 同族逃逸）；
3. ✅ **深 receiver / FQN 逃逸**（已落地 2026-09-30）：call ban 改为按**源码拼写**匹配任意深度 receiver——单查询通配捕获 + `receiverMatch` 后缀规则（短 receiver 也命中其 FQN 形式，`x.Now` 这类不同拼写不误伤），折行 FQN 去空白后仍可比对；构造器禁令同步支持 FQN scoped type（`new java.text.SimpleDateFormat()`）。诚实边界：无类型解析，泛型构造 `new Foo<T>()` 仍不可检（残余缝隙靠配对 import ban 兼底）；
4. ✅ **静默 catch**（已落地 2026-09-30）：内置 Java 规则 `silent-catch`（非 ban 词汇，与 print-stack-trace/javaEmptyBlock 同类）——catch 块非空但**未引用异常参数**且无 throw 无日志调用才命中（引用参数 `e.getMessage()` 进响应/包装上抛/传处理器视为处理中——首版「仅查 throw+日志」在真实项目 79 命中中误伤 35 处响应模式后反转为三信号判据）；空块让给 empty-block 不重复计费。观察期 warning（FP 审计：抽样 12 处中 9 处为解析探测/降级惯用法、3 处为真吞异常，全量 44 处待观察期逐条归因），诚实边界：判据 fail-open 朝干净方向——只要引用了参数即视为处理中，handler 内部是否真用 e 无类型解析不可见；Go 侧错误值忽略是另一原语；
实现形态：tree-sitter query + Go filter 的自定义规则 DSL（每原语 ≤1 周粒度）；
附带 LC-105 unresolved-reference（refs×defs join，基础设施已就绪）。
验收契约：每原语配 fixture；conversion-map 对应 ❌ 条目转 ✅/⚠️。

### 缺口 3：硬控面有洞（T3）✅ 已落地 2026-09-30
- **LC-403 impact-scope 闸门化** ✅：`check -diff -impact-scope`——internal/gatecheck 对变更符号跑 impact.Compute，AffectedFiles 超 cap（默认 25，policy rules.impact-scope.options.max_affected_files 可调）出 error 级 finding，锚定符号定义行、列出受影响文件；诚实边界：AST-only 无类型解析（同 impact 自身），跨语言 Go/Java 符号不互通。
- **LC-404 tests-touched 闸门化** ✅：`check -diff -tests-touched`——有逻辑文件变更且无测试文件变更（_test.go / Test.java / test|tests 路径段）出 1 条 warning 级 finding；flag 激活、policy 只可否决（enabled:false）或调级，与统一阈值标定延后决策一致。
验收契约：agentic fixture 验证分级正确；验收规约 v1 对应条款状态 ⚠️→✅。

## 5. 价值主张红线（对外话术纪律）

**不卖 token 省钱**——R3 实测预防端点 0.0014 违规避免/千 token < 修复端点 0.0041，P+B 比 R2 路径贵
~15-20%（`../evaluations/evaluation-r3-grounding.md` §6）。
**卖三件实测成立的事**：首过绿（4.5% vs 31.8%）、零修复往返（6/7 任务一轮达标）、质量上移
（接地改变实现路径：U09 复用既有 hotspots.Compute 而非自行 shell out）。
外加结构性理由：硬控发生在秒级生成循环内，CR/CI 的分钟-天级带宽在 LLM 时代必然成为瓶颈（GitClear 2025）。

## 6. 落地顺序与前置条件

1. **T1a 事故报告→约束**（复用全部已有设施，价值密度最高）；
2. **T2 DSL 原语 P0**（决定流水线宣称覆盖率：37.5%→接近全量结构性条款）——原语 #1 参数感知禁令（SEC-01 转 ✅）、#2 构造器禁令（CONC-02 双通道）、#3 深 receiver/FQN 逃逸关闭与 #4 静默 catch（内置 silent-catch，LC-304 转 ⚠️，FP 审计后判据反转三信号）均已落地 2026-09-30，见缺口2；
3. **T3 LC-403/404 闸门化**（硬控名副其实）——已落地 2026-09-30：gatecheck 模块 + check -diff 新增 -impact-scope（LC-403，error）/ -tests-touched（LC-404，warning）两 flag，验收规约条款 ⚠️→✅，见缺口3；
4. **T1b 转换套件工具化**——已落地 2026-09-30：`constraints extract`（非标文档→条款→三档判型+查重→候选 YAML）+ `constraints status`（约束落地后的检出情况：按语言分流的命中/零命中/不适用 + 闸门预判）；结构化输入快车道（供应链告警 OSV→import 禁令）落地为**外部 CLI 适配层**而非内置子命令——scripts/osv2policy.sh 编排外部 osv-scanner → scripts/osv2policy 适配器转换/查重/合并（回读校验，绝不写输入）→ 独立 patch 人批，外部工具保持外部，引擎与 CLI 不为具体外部格式长代码（Loop A 数据循环首个接线的触发器，见 docs/constraints/supply-chain-feed.md）；
5. **规则集成接口**（LLM 提案、引擎裁决的落地形态）——已落地 2026-10-01：`constraints rule`——LLM 按 policy 词汇出 Spec JSON（lang/kind/match/args_ban/use/…）+ 反例/正例 fixture，引擎真跑验证（已知命中+零误伤+其他规则命中不干扰判分），返回 PASS/FAIL + 可直接合并的 policy 片段；节点全落盘可改可重跑可回退，集成者不读 metricgate 源码（见 docs/constraints/rule-integration-interface.md）；
5. **全检测项统一阈值标定**（2026-09-30 用户决策：延后）——门禁涉及匹配/阈值校准问题，等检测项全部完善后针对全部检测项统一设置阈值，避免逐条落地时反复调参；此前各新规则按既定纪律起步（观察期 warning / 可配置阈值），标定属于后续独立一轮。

前置诚实条件：采纳 R3 预防路径前应补 **A 臂同条件复跑**拆解生成成本通胀归因
（digest 探索 / 早夭重试 / 单基线方差三因未拆，见 R3 报告 §6）——只影响成本话术，不影响违规率结论。

## 7. 边界

- 纯语义正确性条款**永远**进不了原语层（静态不可判定是数学边界）——级 4 人环 + ❌ 台账 + 复评触发条件兜底；
- E 族缺陷类不自研，走 PMD/SpotBugs 适配器归一化，metricGate 定位聚合验收层；
- 本设计不改变 gate 语义纪律：新增任何硬控规则必须先过 fixture 烟测 + 观察期，report-only→warning→error
  逐级升级，豁免进 rules-gaps 审计。

---
*撰写：2026-09-29。依赖文档：SOP / paradigm / conversion-map / acceptance-spec-v1 / R2-R3 实验报告（均见上方链接）。*
