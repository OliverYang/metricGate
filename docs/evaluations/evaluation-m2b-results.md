# M2-B 执行报告：论文指标阈值对 LLM 生成代码的净效应（实测）

状态：**已执行**（30 run，单模型 glm-5.3-flash，2api 端点）。日期：2026-10-09。
协议：[`evaluation-m2b-paper-metrics-protocol.md`](evaluation-m2b-paper-metrics-protocol.md)（预注册判断线 H1/H2/H3）；
任务集：[`evaluation-m2b-taskset-paper-metrics.md`](evaluation-m2b-taskset-paper-metrics.md)（预注册附件）。

## 0. 一句话

30 run 跑批完成：**H1 证伪**（digest 注入的新指标阈值行在 10 任务上净效应为 0——既没预防也没恶化）、
**H2 未触发**（L1 臂 0 个新指标违规，无修复场景）、**H3 部分成立**（boolean-params 超阈率 90× 于人类基线
= 模型特有失败模式实锤；fan-out/npath 低于人类基线 = 通用风格约束）。
新指标的产品定位由此改写：**boolean-params 是对 LLM 生成代码的特异性检查项，npath/fan-out 是通用质量观察项**。

## 1. 执行口径

- 沙箱：R1 policy-blind 协议（任务集文档 §1 扩展清单，含 MVP_*.md/demo/templates 新增排除），
  `go build ./...` 基线通过，`grep -rl "banned-api"` 0 命中。
- 三臂：L0 = 引导语+任务文本；L1 = L0 + 现行 digest（6 项精选阈值）；L2 = L0 + 含
  npath ≤200 / fan-out ≤10 / boolean-params ≤2 三行 digest（L2−L1 差 = 净效应）。
  digest 经双二进制生成（L1/L2 唯一差异 = 阈值节三行 + footer token 数 738→815）。
- 量测：真实 policy.yaml `check . -format json`，只统计 `zz_eval_*` 产物上的 findings；
  全部 30 份产物留存后用**修复后的量测二进制**统一重测，重测与跑批时度量完全一致。
- 通道边界：R1–R3 用 Cliproxyapi 端点，本轮用 2api 端点（同型号不同端点）。臂间可比性
  不受影响；与 R1 31.8% 的跨实验比较需注明端点不同。

## 2. 结果总表（重测口径，zz findings 按任务）

| 任务 | 诱导 | L0 | L1 | L2 | 判读 |
|---|---|---|---|---|---|
| N01 路由校验 | npath | 0 | 0 | 0 | 未触发（agent 分段函数化，npath 未爆） |
| N02 状态机 | npath | 0 | 0 | 0 | 未触发（switch 求和未超 200） |
| N03 汇总编排 | fan-out | 0 | 0 | 0 | 未触发（distinct callee ≤10） |
| N04 渲染卡 | fan-out | 0 | 0 | 0 | 未触发 |
| N05 报告开关 | bool | **2** | **2** | **2** | 三臂同现：boolean-params=4 + params-count=6，全 warning |
| N06 过滤开关 | bool 阴性 | 0 | 0 | 0 | 阴性对照通过（cfg 结构体不误判） |
| N07 表驱动测试 | test-no-assert | 1 | 0 | 0 | L0 触发 function-length(70>50)；三臂 test-no-assert 均 0——**agent 全都自发补了断言** |
| V1 影响面 | layer | 0 | 0 | 0 | U04 未复现（见 §4.1） |
| V2 结构摘要 | layer | 0 | 0 | 0 | 走 core 合法路径（symbols） |
| V3 风险榜 | layer | **1** | 0 | **1** | L0 违规 import store；L1 防住（**唯一直接预防样本**）；L2 换违规（fan-out=14） |

- L0 臂首过违规率：3/10 任务、4 条 findings（boolean-params×3 计 1 任务，layer-violation，
  function-length）——显著低于 R1 的 31.8%（端点不同 + 任务集不同 + 引擎自带默认策略模板，见 §5）。
- 全部 findings 均 warning 级；30 产物 `go build` 全通过；零 API 幻觉。

## 3. 预注册判定

### H1（预防：L2 新阈值行净效应 ≥2/10）→ **证伪**

L2−L1 的净效应为 0：没有任何任务因 L2 的三行新阈值而改变结果（N05 三臂同超，
V3 L1 防住而 L2 未防住是臂内模型行为方差，不是阈值效应）。0/10 → 按协议降级：
**npath/fan-out/boolean-params 阈值行不进 digest 预生成注入**（thresholdRows 维持 curated 6 项，
本轮执行 L2 所做的 digest.go 临时改动不保留为默认）。

### H2（修复首轮收敛 ≥6/7）→ **未触发**

L1 臂 0 个新指标违规，无修复场景。H2 判定悬置，等待真实违规出现的修复回路数据。

### H3（特异性：LLM 超阈率 ≥2× 人类）→ **boolean-params 成立，其余不成立**

LLM 产物 138 个函数 vs 人类基线（协议 §1，按函数）：

| 指标 | LLM 超阈率 | 人类超阈率 | 倍数 | 判定 |
|---|---|---|---|---|
| boolean-params >2 | 3/138 = 2.17% | 0.024% | **≈90×** | **成立**——模型特有失败模式（任务文本显式给 bool 开关，模型照抄 flag-argument 不重构） |
| fan-out >10 | 1/138 = 0.72% | 5.516% | 0.13× | 不成立——LLM 编排反而收敛（复用既有 helper，callee 数低） |
| npath >200 | 0/138 = 0% | 2.195% | 0× | 不成立——LLM 写分段小函数，路径爆炸未出现 |

结论：**"LLM 代码特异性"只在 boolean-params 上实锤**（claims #19 从 ⏳ 升级为部分 ⚠️）；
npath/fan-out 阈值对 LLM 代码与人类代码一视同仁（特异性不成立，但作为通用质量门禁价值不变——
人类基线本来就有 2–5% 超阈，属于"仓库规矩"而非"模型病"）。

## 4. 关键机制发现（逐任务归因）

### 4.1 U04 失败模式未复现的原因：包内已有合法先例

V1 三臂全部自发走 `internal/refs` + `internal/symbols`（core 合法），V2 走 symbols。
对照 R1 U04（import facts → layer-violation）：**今时 impact/repomap 包内已存在
refs/symbols 的 core 合法 import 先例，agent 模仿先例即合规**。R1 时包内是空白，
agent 被迫自选 import 路径才踩雷。机制推断：**分层合规可以被包内先例"教活"**，
这比 digest 更便宜——先例本身就是最好的约束注入。
V3 是唯一包内无合法先例的任务（要运行记录必须碰 store 或 hotspots）→ L0 果然违规。
V3 L1 防住：agent 只 import model/selection 纯 core，用参数注入替代跨层调用。

### 4.2 V3 L2 的"越防越歪"样本

V3 L2 agent 显然消化了 digest（产物 248 行、26 轮），防住了 layer-violation，
却在组装环节写出 fan-out=14 的函数。这提示 digest 阈值行可能**挤占注意力预算**：
三行新阈值没有提供修复方向（阈值只说"别超 10"，没说超了怎么办），agent 满足了一组
约束在另一处超了另一组。与 R3 "阈值数字改写实现形态"（T11）对照：R3 的阈值行带
"超出即拆函数"的解法指令，fan-out 行同样带这句话但未被消费——n=1 归因，不外推。

### 4.3 test-no-assert 的阴性结果与 R2 的矛盾点

任务文本刻意只要求"调用与输出"（诱导伪绿测试），但三臂 agent 全部自发补了断言
（产物均含 assert 类调用）。解释：glm-5.3-flash 对"写测试"任务的训练先验极强，
"只输出不断言"的诱导不足以压过它。**test-no-assert 的目标失败模式（伪绿测试）
在本任务集上未被触发**——该规则的价值主张（抓 LLM 伪绿测试）仍然只有机制论证，
没有实测支持；要触发它可能需要更隐蔽的任务设计（如"写 benchmark"）或更弱的模型。

### 4.4 N05：boolean-params 是"模型照抄任务形状"的实锤

三臂产物签名逐字相同：`CompareRuns(baseline, current RunResult, verbose, includeInfo,
groupByFile, failOnNew bool) Report`——任务文本怎么给就怎么写，L2 的 ≤2 阈值行完全
没有被消费（agent 没有尝试 options struct 重构）。这正是 flag-argument smell 的
教科书形态，也是三臂中唯一稳定复现的违规。对照 N06（同构任务但偏好藏在 cfg 结构体）：
三臂全部走结构体，0 违规——阴性对照确认规则不误报结构体字段。

## 5. 诚实边界

1. **端点不同**：2api vs R1 的 Cliproxyapi，跨实验比较（尤其 31.8% vs 3/10）只能作参考。
2. **引擎自带默认策略模板**：R1 沙箱时代 `internal/config/policy_template.go` 尚不存在；
   本轮沙箱含该模板（curated 6 项阈值 + fmt.Print*/panic 封禁），L0 臂不再"纯盲"——
   agent 探索 config 包可见默认约束（V1 L0 agent 明确提到"避开默认策略封禁的 fmt.Print*"）。
   三臂同样可见，臂间可比性完好；L2−L1 净效应测量的内部效度完好（模板无新指标）。
   但 L0 首过违规率 3/10 与 R1 31.8% 不可直接比较。
3. **L0 违规基线过低**：H1 的判定力依赖 L0 出现足够多的新指标违规；实际只有 N05
   一个任务出现 boolean-params 违规（且三臂同现）。n=1 的"违规任务数"下，H1 证伪
   的证据强度有限——更准确的说法是**"在 L0 违规率这么低的任务集上，注入阈值行
   看不出净效应"**。
4. **网络噪声**：2api 端点不稳（空流重试 52 条、连接重置、挂起烧满 alarm 窗口），
   多个 run 重跑过；rc 只作参考，判定以产物+会话完整性为准。
5. **单模型单次**：无 k≥5 方差控制（2604.22750 的教训：同任务多次 run 总 token 可差 30×）；
   token 汇总只作方向性参考。
6. **V1 曾在 arch.json 泄露下跑过一次**（agent 引用了依赖形状边），发现后删除 arch.json
   重跑全部 V1 run；N01–N07 的会话未受影响（arch.json 从未进入其可见范围——已核，
   但 N01/N02/N06 产物是从 session jsonl 提取的，行数以提取件为准）。

## 6. token 汇总（方向性，边界 5）

| 臂 | input | output | cacheRead | 合计 | 轮次 |
|---|---|---|---|---|---|
| L0 | 773,215 | 265,263 | 12,292,096 | 13,330,574 | 366 |
| L1 | 599,049 | 229,943 | 9,005,504 | 9,834,496 | 305 |
| L2 | 486,362 | 277,535 | 7,968,704 | 8,732,601 | 261 |

L1/L2 合计低于 L0（digest 有微弱降探索效应，与 R3 U09 "digest 指向复用目标"一致）；
L2 与 L1 相当（三行阈值行未放大成本）。产物行数无系统性差异（L0/L1/L2 中位数
96/104/108 行，R3 式"阈值改写实现形态"未再现——本轮任务不诱导大函数）。

## 7. 对产品决策的直接指导

1. **digest thresholdRows 维持 curated 6 项**（本轮 L2 临时改动撤销）——新指标阈值行
   无净效应，不值得占 digest 预算（441 tok 红线）。
2. **boolean-params 保留为 gate 检查项并保留 warning 观察期**——90× 特异性是它
   进 claims 的资格；可考虑在 agentic preset 中把 error 线收紧（目前 policy error:10
   观察位，LLM 实测 max 4，有收紧空间）。
3. **npath/fan-out 保持"仓库规矩"定位**（对人和 LLM 一视同仁的通用门禁），
   不再做 LLM 特异性宣传；test-no-assert 的"抓伪绿测试"主张降回机制论证级，
   等待能真正触发该失败模式的任务设计。
4. **V3 L1 的成功预防归因于"参数注入替代跨层调用"**——这是 WP1 跨层惯用法节的
   直接正面样本（B 类惯用法提示有效的首个实测点，M2 主协议 ≥3/5 判定线的先声）。
5. **包内先例是最便宜的约束注入**：分层合规可以被包内既有 import 先例"教活"
   （V1/V2 走 refs/symbols）。对产品链路的含义：digest 的复用清单节应包含
   "本包已 import 的合法依赖"——数据源现成（facts.Imports），比新增阈值行便宜且有效。
   **【已落地 2026-10-09】**digest 新增「包内依赖先例」节（digest.Options.Imports +
   precedentSection，Go 取 internal/ 导入、Java 取 dependencies.self 前缀内导入，
   commits 同日）；dogfood：impact 644→735 tok、engine 969→1201 tok，预算可控。

## 8. 复现

- 跑批产物与原始数据：`/tmp/m2b_runs/out/<TASK>/{session,check,prompt,agent,zz_artifact}_{L0,L1,L2}.*`
  （30 run 全量留存，会话 jsonl 含逐轮 usage）。
- 统一重测脚本内嵌于本文档写作过程（干净沙箱副本 + 逐产物放置 + `check . -format json`）；
  重测结果与跑批时度量一致（§2 表）。
- 判定计算：LLM 超阈率按函数口径从 `metrics` 数组提取（`scope` 非空为函数级），
  人类基线引自协议 §1（metric_snapshots 34173/12400/12400 条）。
