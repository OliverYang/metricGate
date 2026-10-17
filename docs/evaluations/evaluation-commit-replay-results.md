# 提交级研判：LLM 生成仓库 × 门禁纠正重放（实测）

日期：2026-10-09/10　状态：**已执行（dev-cockpit Go 全量；ace-ts TS 待前置）**
协议：[evaluation-commit-replay-protocol.md](evaluation-commit-replay-protocol.md)（预注册 2026-10-09）

> **判据版本（iter-31 补标）**：本报告的度量来自 `c701576`–`d5cb01c` 之间的工具链 + 157 行 `policy.yaml`，
> 时代最接近的复现组合是 `/tmp/mg-era-bin`（`d5cb01c` 构建）+ `git show d5cb01c:policy.yaml`——该组合在
> `396cd3cd8f` 上逐条复现归档值（2174 findings / 555 files / 222 errors）。**判据不可归因到确切 commit**，
> 所以凡引用本报告数字，同时引用 [data/commit-replay/summary.json](data/commit-replay/summary.json) 的
> `criterion_versions`。§2 那张「规则族触发总量」表有 **13/18 行无法从归档 JSON 复现**（口径不可考，已就地标注）；
> 「215/289 commit 触发 `test-no-assert`」可复现但含义被读错（= 1 个函数逐 commit 重复计数，见
> [evaluation-iter30-tna-recalibration.md](evaluation-iter30-tna-recalibration.md) §3）。
> 原始数据摘要已入库（`data/commit-replay/`）：此前 1.3 GB 归档 JSON 只在 `.data/` 下、从未被 git 跟踪。
>
> 全仓判据版本索引：[criterion-version-index.md](criterion-version-index.md)（每篇报告的采集日期 → 当时 main → 之后变过的判据）。

## 0. 一句话结论

对真实 LLM 生成仓库（marein/dev-cockpit，289 commits，README 自证 100% vibe coded）：**门禁在全部 289 个 commit 都有检出（0 个零检出 commit）；error 级 findings 经 findings-JSON 反馈修复 5/5 清零；副作用率 2 条 warning / 0 error；一轮收敛 1/5，远低于 R2 合成任务的 6/7——真实仓库比合成任务难修**。

## 1. 语料与执行口径

- marein/dev-cockpit：Go，289 commits（2026-05→2026-10），555 个 Go 文件 13.2 万行，README 自证「Coding agents write the code」。
- 工具：本仓 metricgate（HEAD c701576 之后、d5cb01c 前），本仓 policy.yaml（含 paper-metrics 观察 entries，error:off）。
- 检出口径：全仓 `check . -format json`（分层判定需跨包边，单目录不触发）；build（`go build ./...`）作为 API 幻觉代理，**289/289 全部 build OK——零 API 幻觉**。
- 修复代理：headless pi，2api / glm-5.3-flash（与 R1-R3 同通道），alarm 1800s/轮，≤2 轮，输入=findings JSON（error-first 全量含 warning）。
- 端点边界：与 R1（Cliproxyapi）不同，2api；仓库原生开发用 Claude Code，跨模型可比性受限。

## 2. Step 1 全量检出矩阵（H1，描述性）

289 个 commit 的检出时间序列（原始 JSON：`/tmp/replay/devcockpit/`，归档 `.data/commit-replay/`）：

- **0/289 零检出 commit**——最干净的首个 commit 也有 75 条（7 error）；findings 随仓库增长 75→2174 持续累积，无自然消亡趋势。
- 全历史规则族触发总量：fan-out 85,771 / cyclomatic 38,150 / npath 37,093 / cognitive 22,926 / function-length 22,361 / params-count 9,847 / file-length 5,296 / function-clone 4,912 / test-no-assert 3,492 / boolean-params 334 / call-chain-depth 279 / todo-comment 87 / silent-catch 55 / nesting-depth 44 / banned-os-exec 43 / empty-block 21 / banned-panic 3 / banned-fmt-Print* 3。
  - ⚠️ **这行里只有 5 个数字能从归档复现（2026-10-18 重算，见 [iter-30 重标定](evaluation-iter30-tna-recalibration.md) §5）**：fan-out 85,771 / cyclomatic 38,150 / npath 37,093 / cognitive 22,926 / function-length 22,361 与 `.data/commit-replay/devcockpit/*.json` 的 findings 实例数逐条相等。**其余 13 行既不等 findings、也不等 commit 数 / distinct file / distinct anchor / 快照超阈计数，口径不可考**（test-no-assert 归档实际 1,783，不是 3,492；nesting-depth 1,507，不是 44；banned-os-exec 3,388，不是 43；boolean-params / todo-comment / silent-catch / empty-block 归档为 0——前者 `error: off`，后三条在当时的策略里 `enabled: false` / 只配 java / 未配置）。引用本行时：**要么用归档 findings 数，要么注明口径待重算**，不要直接引用原行。
- **test-no-assert 在 215/289 commit 触发**——M2-B 里未 materialize 的「无断言测试」形态，在真实 LLM 仓库持续触发。
  - ⚠️ **「大规模存在」已被重算否证（2026-10-18，[iter-30 重标定](evaluation-iter30-tna-recalibration.md)）**：当时与现在的判据下 commit 计数都是 215/289、且与归档一致，但 215 个命中全部来自 `internal/eventbus/eventbus_test.go:40` **同一个函数**（自 commits.txt 第 75 个 `444b31ec97` 起，75..289 零漏网）；distinct anchor 只有 31 个、新判据下剩 1 个。findings 实例 1,783（旧）→ 233（iter-29 后）→ 215（再排除 benchmark）。那个函数源码注释明写靠「阻塞即死锁 → 超时」失败，属规则精度下界。**per-commit 计数不是 per-event 计数**：引用本行时请说「按 commit 计数 215/289」，不要说「215 个伪绿测试」。
- npath 极值：218,160 / 92,160（人类基线 max 4,752，超 40×）；fan-out error 30 vs 人类 max 20。
- **layer-violation 全 0**：dev-cockpit 未声明 layers，无约束可违——是「未配置」不是「干净」，如实记录。
- LLM 仓库自身无门禁约束下的形态：每 commit 约 10% findings 为 error 级（75/7 → 2174/200+ 线性），错误密度不随规模下降。

## 3. Step 3 纠正重放（H2）

挑拣 5 个早期 commit（预注册标准：error>0、findings≤160、生产代码 diff）。结果：

| commit | before f/e | after f/e | 轮次 | resolved | introduced |
|---|---|---|---|---|---|
| 5065169f69 | 75 / 7 | 50 / **0** | 2 | 28 | 3 |
| 989950a2d1 | 83 / 8 | 65 / **0** | 1 | 21 | 3 |
| 2486d6db22 | 90 / 9 | 29 / 1 | 2 | 73 | 12 |
| 3227359a07 | 92 / 9 | 35 / 3 | 2 | 65 | 9 |
| 3adea69ea5 | 157 / 14 | 57 / **0** | 2 | 105 | 4 |

- **error 清零 5/5**；总 findings 降幅 39%-72%（均值 ~57%）。
- 副作用口径（函数级锚定，逐条见 `.data/commit-replay/report/`）：resolved = 按（规则×文件×函数）锚定后消失的 findings；introduced = 新锚点 findings。**introduced 不等于「修复产生新问题」的主体**——大部分是重构把巨型函数拆成的小函数重新触发同类告警（如 scanTranscript/fan-out、execport 的 banned-os-exec 白名单内新锚点）；真正的恶化（同锚定值上升）每任务 ≤2 条（如 2486 的 registerRoutes fan-out 11→16、3adea 两个文件 file-length +45）。此前按行号匹配报的「2 条副作用」是行号漂移伪影，函数级锚定才是诚实口径。
- **H2 一轮收敛 1/5 < 预注册 6/10 → 证伪**（对照 R2 的 6/7）：真实仓库修复需要多轮；多数首轮被 1800s alarm 截断（agent_rc=142）但工作树已处于可用中间态，第二轮收尾。按协议如实判负——**findings 反馈的修复闭环在真实仓库上有效（error 清零）但不便宜（轮次 ×1.8）**。
- 残留 error（2486 1 条 / 3227 3 条）为第二轮未完成的 npath/fan-out 重构尾巴，属时间预算而非能力边界。

## 4. 行为学发现（R2 之外的新证据）

1. **agent 自发写隔离惯用法**：为消 banned-os-exec，新建 `internal/git/exec.go`，注释逐字「Package git is the single auditable place where this codebase shells out to external commands…(or inject them behind a port)」——R2 U09 / M2-B V3 的隔离模式在真实仓库第三次自发出现。
2. **泛化消 clone**：为消 function-clone 新建共享 handler（handlers_definitions.go），删重复而非复制。
3. **修复不加测试、不删测试**（仓库无测试文件，agent 未伪造测试凑绿——诚实边界：该仓库无测试可破坏）。
4. **H0 干净树对照（44aa99dc8b）**：prompt 仅要求「自行复核、只修确信问题」。agent 复检后把 8 个 error 判为真实问题修复（82→71，0 error，4 文件），banned-os-exec/banned-panic 判为「有意的架构约束实现」保留。**工具无误伤（确定性单跑 byte 一致）；误伤风险不在 check 而在代理的自主裁量**——代理会把 error 全修、warning 按判断保留，这是预期行为而非 FP。

## 5. 诚实边界

- n=5 重放（预算上限 10，实际挑拣后 5）；单仓库；修复代理与生成模型不同源（Claude Code 生成 vs glm-5.3-flash 修复）。
- 「error 清零」以 metricGate 的判定为准，语义等价性未人工审（R2 同款边界）；本轮 build OK 5/5 + `go vet` OK（agent 自报）。
- alarm 1800s 截断使「轮次」读数偏保守（若放宽时间预算，一轮收敛率可能上升）。
- dev-cockpit 无 layers 声明 → B 类检查在本语料不可测；ace-ts（TS）待 tree-sitter grammar 前置后补测。

## 6. 与既有证据的关系

- H1 检出力：真实 LLM 仓库的 100% commit 检出率 + error 密度 ~10%，补上了 R1（合成任务 31.8%）之后「仓库尺度」的缺口。
- H2：R2 的 7/7 是合成任务单文件修复；本实验是仓库级多文件重构——修复闭环「有效但不便宜」的定量刻画。
- test-no-assert 215/289：M2-B H3「该形态未出现」的结论限定于合成任务——真实仓库里它**每个 commit 都在**，但集中在极少数函数（iter-30 重算：anchors 31→1）。**不足以把 claims #19 该项上调**，原上调已改判。
- side-effect 率（2 warning / 0 error / 5 任务）首次量化修复闭环的安全性。

## 7. 复现

```bash
git clone https://github.com/marein/dev-cockpit /tmp/corpus/dev-cockpit
# Step1 per-commit snapshot（脚本 /tmp/replay/step1_collect.sh，289 JSON）
# Step3 replay（/tmp/replay/replay_task.sh <sha> <round>，pi 2api glm-5.3-flash）
```

原始数据：检出矩阵 289 JSON + 5 任务重放（agent log/session JSONL/前后 check JSON/zz_replay_track.md）→ `.data/commit-replay/`。

## 8. 指标修复的工程价值与文献依据（2026-10-10 补）

「按指标修复有什么实际意义」的回答分三层，正反证据都列：

### 8.1 传统证据（有共识但带争议）

- **支持**：NIST SP 500-235《Structured Testing》(Watson & McCabe)——圈复杂度是测试路径数下界（CC=N ⇒ 至少 N 条独立测试路径），CC≤10 建议的官方出处；「修复杂度=降低测试成本」是最硬的机制论证。SonarSource Cognitive Complexity (Campbell, TechDebt 2018) 修正 CC 对嵌套的钝感，工业界（SonarQube）全面采用。Alves et al.《Deriving Metric Thresholds from Benchmark Data》确立「阈值=基准分布分位」方法论——本项目 trend DB 人类基线（npath p50=4/p90=48）同思路。
- **反对/边界**：Shepperd 1988《A critique of cyclomatic complexity as a software metric》(SEJ)——CC 理论基础与可靠性关联均被质疑，四十年未停。EMSE 2022《What really changes when developers intend to improve》——重构意图与指标变化常脱节。结论：传统场景下「指标修复=更好软件」只有相关性证据，且修复占人力，批评成立。

### 8.2 LLM 场景价值换了地基（本实验的意义所在）

1. **超阈形态是生成惯性不是设计权衡**：dev-cockpit 的 npath 218160 loadTranscript 无人「设计」过——拆它没有破坏设计意图的风险，Shepperd 式批评的主要前提消失。
2. **修复零人力边际成本**：本实验 5 任务 10 轮 ≈2.5M token 无人值守 error 清零 5/5——指标维护从「资深工程师的时间税」变成「循环内一次工具调用」，修复经济学被改写。
3. **修复实验为预防实验定价**：本实验测的是兜底成本（有效但贵，1/5 一轮收敛）；R3 测的是事前注入（同款信息 ~441 token 防住 C 类 5/5）。合起来：预防 441 tok vs 修复数十万 tok，这正是 digest 注入的经济依据。
4. **同类研究佐证**：arxiv 2508.00700 (ESEM 2025)——LLM 代码 bug 更少但难题上「引入人类代码中不存在的结构性问题」（本语料 npath 极值即实例）；VibeTensor (2601.16238)「Frankenstein Effect」——组件各自正确、组合后全局次优，全局层检查（分层/耦合）不可替代的直接文献例证。

### 8.3 本实验数据特有增量

- 0/289 零检出 + 线性累积无消亡：无门禁 LLM 仓库质量债单调增长的 commit 级实测（公开文献未见同粒度）。
- test-no-assert 215/289（按 commit 计数；独立事件只含 1 个函数，见 iter-30 重算）：伪绿测试形态确实长期驻留在 LLM 仓库里——比复杂度更独特的 LLM 信号，但**量级必须按 anchor 而非 commit 报**。
- 副作用率 2 warning / 0 error：修复闭环安全性首次量化。
