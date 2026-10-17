# 价值主张台账（claims ledger）

> 本文是**唯一的说法来源**。任何文档、README、对外介绍里出现的价值主张，
> 必须能在这里找到一行，并带着它的状态与证据指针。
> 与 `value-proof.md` 的分工：那篇讲**方法论**（该证明什么、什么会推翻它），
> 本文讲**结论**（现在相信什么、不信什么、还没测什么）。
>
> 最后更新：2026-10-12。状态会变，证据指针不会。
>
> **2026-10-12 外部对拍更新**（见下「外部对拍：metricgate vs `codex review`」）：
> 在存量债仓库上，#7 的度量阈值类 finding 有约一半是「数值未变的旧债」
> 已由迭代 10 的数值 delta 棘轮修正（31 error → 16 error，实测）。
> #15/#16/#17 仍为 ⏳：本实验测的是**代码审查覆盖**，不是 agent 循环采纳。

## 状态图例

| 标记 | 含义 |
|---|---|
| ✅ **成立** | 有实测数据或结构性论证支撑，可以对外说 |
| ❌ **证伪** | 实测数据**否定**了它，**禁止**再出现在任何文档/介绍里 |
| ⚠️ **部分** | 成立但有明确的适用边界，必须连边界一起说 |
| ⏳ **未测** | 合理但无证据。可以说"待验证"，**不能**说"能" |

## 台账

| # | 价值主张 | 状态 | 证据 | 一句话边界 |
|---|---|---|---|---|
| 1 | 检出比竞品更多违规 | ❌ | `competitor-research.md` | 覆盖面是用语言数/规则数/类型系统买的，必输。**不作为主张** |
| 2 | 降低 API 幻觉率 | ❌ | R1 §4.4：**0/22** | 编译器是免费的确定性检查器，agent 会 `go build` 自修，幻觉活不到最后 |
| 3 | 省下探索 token（帮你找到既有实现） | ❌ | R1b Arm B/C：**不用任何工具也 5/5 复用** | 发现成本本来就不是瓶颈（`evaluation-hint-and-cost.md` §3） |
| 4 | 防止重复造轮子 | ⚠️ | Arm A **0%** → D/E **80%** | 一句提示就做到 80%。剩余 20% 才是工具的，且**"验证"这一半提示永远做不到** |
| 5 | 强制组织约定（禁用 API / 不许写 stdout） | ✅ | R1 §4.3 | 价值不是"抓模型犯错"（无指向时 0/20），而是**模型不知道你的约定** |
| 6 | 架构分层：抓编译器看不见的依赖倒置 | ✅ | R1 §4.2（u04 自然产生 `core→adapter`，`go build` 通过） | 低频（4.5%）但**单次命中价值极高**；按"高后果、低频率"对待 |
| 7 | 度量阈值：抓复杂度/长度越界 | ✅ | R1 §4.1（2/2 的 error 级都在此）；**2026-10-12 外部对拍 boundary**：存量债仓库上需数值 delta 判据才不算旧债重报（迭代 10，error 31→16） | 集中在"需要自己设计控制流"的任务上；是否该拦属 policy 判断；**数值未变的阈值 finding 不算本次引入** |
| 8 | 验证"声称复用" vs "实际复用" | ✅ | Arm A（点名却重写）、Arm E（注释里点名 `repomap` 却重写） | **提示是请求，检查是事实。** 这是提示在定义上做不到的事 |
| 9 | 抓二阶后果（本次改动让依赖图变成了什么样） | ✅ | Arm D/E 的 `sdp-violation`；点③ 的 `layer-violation` | 全局属性，单文件视角看不见；只有图分析能回答 |
| 10 | 确定性：逐字节一致，可当闸门 | ✅ | 连续 5 次运行输出哈希相同；R4 设计 | 结构性的，竞品的非确定性检查器做不到 |
| 11 | 成本 ≈ 0（0 token、毫秒级） | ✅ | `evaluation.md` | 只证明"便宜"，不证明"有用"，必须叠在 6/7/8/9 之上 |
| 12 | 知识论证不随模型变强而衰减 | ✅ | `value-proof.md` §3（结构性论证，非实验） | 与"能力论证"（#2）相反：仓库约束不在权重里，且每天在变 |
| 13 | 容错解析：语法错误降级为 info，不中断整轮 | ✅ | 2026-09-28 修复后实测（见下"本轮修复"） | **修复前这条是假的**：finding 被覆盖丢弃、`ParseErrors` 从不写入 |
| 14 | 闭环收敛：每千 token 的违规避免数提升 | ⚠️ | R2 修复端点 **0.0041**、R3 预防端点 **0.0014** 违规避免/千 token（均高于弃置线 >0，H2 弱确认；`evaluation-r2-stability.md`、`evaluation-r3-grounding.md` §6） | 修复路径成立（7/7 一轮收敛为主）；预防路径 31.8%→4.5% 但**生成成本膨胀 1.74× 未分解**（A 臂同条件重跑待做）——引用时必须带这个未决项 |
| 15 | 闸门在真实 agent 循环里被调用、findings 被修掉 | ⏳ | **装置就绪且已接线**：`workflow.md` §4（MCP `-calllog` + `metricgate calls`）；2026-10-08 接入真实 harness（见下"接线落地"）；真实会话数据**采集中** | **它决定量级**：每任务调用 < 1 次 ⇒ 循环内通道是理论的。现在能回答了，缺的是真实数据。2026-10-12 外部对拍未触达本项（见下） |
| 16 | 拦下幻觉包 / 未声明的新依赖 | ⏳ | R1 未触达（任务都是单文件新增） | `unknown-dependency` 逻辑存在且有测试，但**基率未测** |
| 17 | 克隆检测 / 依赖白名单的基率 | ⏳ | R1 §5 局限 5：**0% 是设计局限，不是"无价值"的证据** | 单文件新增任务天然不产生克隆 |
| 18 | 防住 B 类架构违规（R3 唯一未防类别） | ⚠️ | **M2 主协议已跑（`evaluation-m2-arch-results.md`）**：5 个 B 类诱导任务 × L0/L1，3 个 L0 违规任务在 L1 全部防住（3/3；含无信息对最严口径 3/5，达预注册 ≥3/5 线）；H2 机制证据逐字——L1 agent 自发写 port（ChurnSource/ChangeSource）+ 组装根注入，与惯用法节措辞吻合。配套装置：digest 跨层惯用法节 + 包内依赖先例节（生成前）、layer-violation 修法后缀（修复期）、arch 漂移闸门（长周期） | 只能说：digest 生成期注入在小样本（n=3 有效对）上防住了 B 类；不能说：高置信预防率或拆分单节贡献（无第三臂） |
| 19 | 抓 LLM 特有的代码形态劣化（路径爆炸/扇出/旗标参数/伪绿测试） | ⚠️（部分） | **检查项已落地**（论文出处见 `docs/metrics.md` 与 `docs/capabilities.md`）；**M2-B 30 run 已测**（`evaluation-m2b-results.md`）：boolean-params 超阈率 2.17% vs 人类 0.024%（≈90×）——模型特有失败模式实锤；npath/fan-out 低于人类基线（0×/0.13×）——是通用质量门禁而非模型特异性；**提交级研判补充（`evaluation-commit-replay-results.md`，dev-cockpit 289 commits）：test-no-assert 在 215/289 commit 触发——伪绿测试形态在真实 LLM 仓库大规模存在（M2-B 任务集未 materialize 是任务集局限，非形态不存在）；npath 极值 218160 vs 人类 max 4752** | 只能说：boolean-params 对 LLM 代码有特异性价值；test-no-assert 在真实 LLM 仓库检出密集但无对照修复数据；不能说：npath/fan-out "抓 LLM 特有失败" |

## 已从文档中删除的说法

以下说法曾出现在文档里，已被证据推翻，**本轮已改写**。留档是为了防止复发。

| 位置 | 原说法 | 处理 |
|---|---|---|
| `README.md` MCP 段 | "ground themselves in real constraints instead of **hallucinating APIs**" | 改为"读约束 + 校验产物"，并指向本文与 `evaluation-hint-and-cost.md` |
| `ab-experiment.md` 任务集 | "**主要度量 API 幻觉率**" | 改为"度量是否复用既有符号" |
| `ab-experiment.md` 度量指标 | "**首要指标 = API 幻觉率**"（依据 CodeHalu/CodeMirage） | 降级为"只作噪声过滤"，主指标明确为每千 token 违规避免数 |
| `evaluation.md` 覆盖表 | "符号事实（**防 API 幻觉**的 grounding）" | 改为"生成前 grounding：既有符号与签名"；同表的"（路线图）重复检测、依赖图/分层"已落地，拆成两行 |
| `evaluation.md` Token 经济表 | "**facts 防幻觉**：消除'幻觉 API → 编译失败 → 再探索'整轮 turn" | 该行标注**已作废**（没有那一轮可省） |
| `research-notes.md` 三大赌注 | "(1) 事实/约束的 grounding 能**减少幻觉**" | 加口径对齐：赌注改为**知识论证**形态（模型不知道你的规则），并说明文献条件不同 |
| `research-harness-and-constraints-2026.md` §8 | 引用 CodeHalu/CodeMirage 的 17–30% 幻觉率作为支撑 | 加**口径对齐**：文献测裸生成、R1 测 agent 循环终态，两者不矛盾但落点不同 |
| `competitor-research.md` §对齐 | "A/B 的 API 幻觉率首要指标能被 P0-1/P1-2 改善" | 标注前提作废；P0-1/P1-2 的理由改为服务"生成前 grounding" |

## 现在能对外说的三句话

把台账里 ✅ 的部分压成三句，**不许多说**：

1. **它知道你的仓库的私有规则，而模型不知道** —— 分层方向、禁用 API、依赖白名单，
   这些不在任何权重里，也不在编译器里。（#5 #6 #12）
2. **它把"我照做了"变成一条可核对的事实** —— 同一份输出、同一个哈希、同一个退出码。
   提示给的是承诺，闸门给的是裁决。（#8 #10）
3. **它看得见一次改动在整张依赖图上做了什么** —— 这是任何单文件视角、
   任何提示、任何编译器都看不见的。（#9）

不能说的：更快找到代码（#3 ❌）、更少幻觉（#2 ❌）、防住了重复（#4 ⚠️ 只说 20%）、
检出更多（#1 ❌）。以及 #15 的量级断言（真实循环采纳率——装置已就绪、
数据还没到）；#19 只能说 boolean-params 的特异性（90×），不能外推到 npath/fan-out/test-no-assert。
更不能说的是「比 LLM 审查更准」或「能替代 LLM 审查」——外部对拍显示两者问的问题
不同（见下）。

## 外部对拍：metricgate vs `codex review`（2026-10-12）

一台真实审查会话与一个 LLM 审查器各看同一批提交，输出如何分工。

**装置**：`gitee-scan-2.0-portal`（Java，1187 文件，5636 提交）5 个提交；
MetricGate 侧 `check . -diff -base HEAD~1 -config .metricgate.yaml`（每 commit 独立 worktree 隔离）；
LLM 侧 `codex review --commit <sha>`。

**结果**：

| commit | MetricGate errors | 其中数值真的变差 | codex review |
|---|---|---|---|
| `4b5ca7ead2` | 10 | 7 | no findings |
| `16c289c48a` | 4 | 0 | no findings |
| `2ca4698cd4` | 9 | 3 | 不可用（上游 429） |
| `6d3664373d` | 3 | 2 | **1 个 P2（已核实真实）** |
| `d2575a780f` | 5 | 4 | no findings |

三条结论，都改变了产品判断：

1. **`-diff` 半数 error 是旧债重报，不是新引入**（31 error 里 30 个在父提交就已是 error）。
   本实验直接催生迭代 10 的数值 delta 棘轮：同类口径重跑降到 16 error。
   台账 #7 现在必须带这个边界说：**阈值类 finding 在存量债仓库上需要数值判据才算"新"**。
2. **LLM 抓到的是另一类问题**。唯一真实的 P2（`6d3664373d`）是跨文件授权缺陷：
   新 `OpenApiMsAuthInterceptor` 解析角色时漏了 `splitSpace.shenwan` 为 true 时的
   用户组角色分支，与同仓 `PostPermissionInterceptor` 的行为分叉。
   这类语义问题#7 的度量指标结构上表达不了。
3. **两者不矛盾**：MetricGate 回答「这块代码的复杂度/耦合多久没还」；
   LLM 回答「这次的逻辑对不对」。前者可以被 #7/#9 的指标精确量化，后者需要读代码。
   ❌ 不能说"我们的闸门能替代 LLM 审查"或反之。

**局限**（引用时必须带）：5 个提交中 1 个因上游 429 无 LLM 数据；LLM 运行单次 10–30 分钟
且输出 MB 级推理转录；未做人工交叉复核；均为 Java 单语言仓库。

复现：为每个 commit 建 detached worktree（`git worktree add --detach /tmp/rb/<sha> <sha>`），
在 worktree 内 `check . -diff -base HEAD~1`；LLM 侧 `codex review --commit <sha>`。

## 接线落地（2026-10-08）

#15 的装置从"仓库里有"推进到"接进了真实 harness"：

| 落地项 | 位置/命令 | 状态 |
|---|---|---|
| pi harness 接入 MCP 闸门 | `.pi/mcp.json`（项目级）→ `go build -o /tmp/metricgate-mcp ./cmd/metricgate-mcp`，带 `-calllog 1` 启动 | 已配置（项目级优先于用户级；`.metricgate/` 已在 .gitignore） |
| pre-commit hook 安装 | `git config core.hooksPath hooks`（版本化共享，非拷贝） | 已安装 |
| 调用日志采集 | `.metricgate/mcp-calls.jsonl`（`metricgate calls` 汇总） | 0 数据起；#15 的 R6 证据由此持续累积 |

验证：MCP 注册 `pi mcp list` 可见；hook 装后任意 `git commit` 会跑 `check -diff`（三情形已验证：无提交仓库 exit 0、干净提交 exit 0、新增行越界 exit 1）。

## 本轮修复（代码 + 文档）

清理过程中发现的**真实缺陷**，不是格式化：

| 缺陷 | 位置 | 性质 |
|---|---|---|
| `parse-error` finding 被覆盖丢弃，`RunSummary.ParseErrors` 从不写入 | `engine.go`（文件路径 + `CheckSource` 两处）、`worker.go` | **文档承诺的能力实际不生效**（台账 #13）。语法错误的文件返回"0 errors, gate PASSED" —— 对闸门来说是最坏的失效方向（不可解析输入上的假阴性） |
| `git.countLines` 无人调用 | `internal/git/git.go` | 死代码（Go 不报未使用的函数） |
| `facts.goVis` 的 `for` 循环必然在首次迭代返回 | `internal/facts/facts.go` | 误导性写法，尾随 `return` 不可达 |
| `goReceiver` 的文档注释悬空（与函数之间隔了另一个函数） | `internal/facts/facts.go` | 注释错位 |
| `runtime.GOROOT()` 已废弃（Go 1.24+） | `internal/metrics/legacy_diff_test.go` | 改为问工具链，附带优雅降级 |
| 循环 append 可简化为 `append(...)` | `internal/clonedrift/clonedrift.go` | 冗余逻辑 |

验证：`go build ./...` ✅ · `go test ./...` 25 包 ✅ · `staticcheck ./...` **零告警** ✅ ·
`gofmt -l` 干净 ✅ · `metricgate check .` 仍是 **1 error / 66 warnings**（唯一 error 是既有的
`explain_cmd.go:45`）✅ · 语法错误文件现在正确报 `parse-error` + `parse errors:` ✅。

## 本轮新增：接线层（workflow）

台账里 **#15 是唯一还没测、且决定量级**的一条。本轮把它的**装置**做出来了，
并把闭环的三个接线点落地。全部本机验证，命令与真实输出抄在
[`workflow.md`](workflow.md)。

| 新增 | 位置 | 它让什么变成可核对的事实 |
|---|---|---|
| 本地调用日志（JSONL，默认关闭，仅本地，不记录源码） | `internal/calllog/` + `cmd/metricgate-mcp/calllog.go` | 工具调用次数 / 阶段 / 拦截率 / 收敛率 |
| `metricgate calls` 汇总入口 | `cmd/metricgate/calls_cmd.go` | 把日志压成一张表；`-since` / `-format json` |
| 埋点不可绕过 | `cmd/metricgate-mcp/tools.go` 的 `addTool` | 新加工具若不经过它，端到端测试会红（`e2e_test.go`） |
| pre-commit hook（已验证三种情形） | `hooks/pre-commit` | "这次提交新增的行"是否越界 |
| CI workflow | `.github/workflows/gate.yml` | 同上，在 PR 与 push to main 上（**判据已在本机验证；GitHub Actions 的运行环境尚未真跑过**） |
| agent 指令模板 | `workflow.md` §2 | 何时调什么、完成判据是什么 |

同时修掉两个**被这次接线暴露出来的真实缺陷**：

| 缺陷 | 位置 | 性质 |
|---|---|---|
| `-diff` 在"HEAD 还没有提交"的仓库上硬失败，且报错是 `requires a git repository` | `internal/git/changes.go` | 消息把读的人引去找一个本来就装着的 git；这正是任何人第一次装 pre-commit hook 会撞上的状态 |
| `-base <不存在的 ref>` 同样报 `requires a git repository` | 同上 | CI 忘了 `fetch-depth: 0` 时的标准故障，消息必须指名是哪个 ref |

两条都改成了**只在出错路径上**做廉价探测（`rev-parse --git-dir` / `--verify`），
好消息路径零开销；仓库真的不存在时仍保留原来的通用措辞。
测试：`TestLoadChangesOnRepoWithoutCommitsNamesTheRealProblem`、
`TestLoadChangesUnknownBaseRefNamesTheRef`、`TestLoadChangesOutsideRepoKeepsTheGenericMessage`。

一个**必须写进文档的坑**（本轮实测）：

> `banned-api` 默认是 **warning** 级。只写 `banned-api:` 而不写
> `banned-api-severity: error` 时，闸门会报出 finding 然后**通过**
> （`fail_on: error` 只对 error 级生效）。想让规则真的拦住提交必须显式提级。


## 遗留（已识别，未处理）

这些都是**决策**，不是清理任务，所以留给维护者定：

1. **本仓库 `.metricgate.yaml` 的层声明漏了 6 个包**
   （`apisurface`/`bundle`/`clonedrift`/`clones`/`hotspots`/`symbols`）。
   补齐会把 2 条含糊的 `sdp-violation` 换成精确的 `layer-violation`，
   并要求决定 `facts.FileFacts` 是否下沉 core、`clones` 是否注入文件列表。
   证据与验证见 [`evaluation-hint-and-cost.md`](evaluations/evaluation-hint-and-cost.md) §4.5、§6。
2. **`sdp` 的默认值**是否应从"无条件 warning"改成"仅 `architecture` 段出现时启用"？
   现在它与其他需要显式配置的整图规则不一致 —— 零配置用户会收到一条自己没要的告警。
   这是行为变更，台账 #6 的"低频高后果"论证支持保留默认开启，但需要拍板。
3. **`unknown-dependency` 的基率未测**（台账 #16）。R1 的任务集是单文件新增，
   天然不产生新依赖，所以 0% 是设计局限。要判定它，需要一个"要求引入外部库"的任务组。
4. **`clones` / `dependencies` 两族的基率未测**（台账 #17），同上。
5. **R5（真实循环里的调用次数）** —— 决定台账 #15，也决定量级。
   **装置已就绪**（`workflow.md` §4）：打开 `-calllog`，跑一个真实任务，
   然后 `metricgate calls`。缺的不再是工具，是一次真实的会话数据。

## 复现

```bash
go build ./... && go test ./internal/...      # 34 包（当前状态；当时的 25 包见 2026-09-28 记录）
go run honnef.co/go/tools/cmd/staticcheck@latest ./...   # 零告警
gofmt -l internal/ cmd/                    # 空
./metricgate check .                       # 1 error（既有 explain_cmd.go:45）/ ~200 warnings

# 台账 #13 的复现：语法错误文件必须报 parse-error
mkdir -p /tmp/mg-broken && cd /tmp/mg-broken
printf 'package broken\n\nfunc F( {\n\treturn 1\n}\n' > broken.go
printf 'version: 1\n' > .metricgate.yaml
metricgate check .                         # 期望：[info] parse-error + "parse errors: broken.go"

# 台账 #15 的装置：埋点 → 汇总
go build -o metricgate ./cmd/metricgate && go build -o metricgate-mcp ./cmd/metricgate-mcp
# 用任意 MCP 客户端跑一次会话（服务端带 -calllog .metricgate/mcp-calls.jsonl），然后：
./metricgate calls                         # 调用次数 / 阶段 / 拦截率 / 收敛率
```

## 与既有文档的关系

- 方法论（该证明什么、什么会推翻它）→ [`value-proof.md`](strategy/value-proof.md)
- R1 基率 → [`evaluation-base-rate.md`](evaluations/evaluation-base-rate.md)
- R1b 重造率 → [`evaluation-reuse.md`](evaluations/evaluation-reuse.md)
- Arm D 提示 vs 闸门 → [`evaluation-prompt-vs-gate.md`](evaluations/evaluation-prompt-vs-gate.md)
- 含糊提示 / 探索成本 / SDP 调和 → [`evaluation-hint-and-cost.md`](evaluations/evaluation-hint-and-cost.md)
- 接线：指令模板 / hook / CI / 埋点 → [`workflow.md`](workflow.md)
- 能力清单（有什么功能）→ [`capabilities.md`](capabilities.md)
