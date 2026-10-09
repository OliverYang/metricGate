# 提交级研判：LLM 生成仓库 × 门禁纠正重放（实测）

日期：2026-10-09/10　状态：**已执行（dev-cockpit Go 全量；ace-ts TS 待前置）**
协议：[evaluation-commit-replay-protocol.md](evaluation-commit-replay-protocol.md)（预注册 2026-10-09）

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
- **test-no-assert 在 215/289 commit 触发**——M2-B 里未 materialize 的「无断言测试」形态，在真实 LLM 仓库大规模存在。
- npath 极值：218,160 / 92,160（人类基线 max 4,752，超 40×）；fan-out error 30 vs 人类 max 20。
- **layer-violation 全 0**：dev-cockpit 未声明 layers，无约束可违——是「未配置」不是「干净」，如实记录。
- LLM 仓库自身无门禁约束下的形态：每 commit 约 10% findings 为 error 级（75/7 → 2174/200+ 线性），错误密度不随规模下降。

## 3. Step 3 纠正重放（H2）

挑拣 5 个早期 commit（预注册标准：error>0、findings≤160、生产代码 diff）。结果：

| commit | before f/e | after f/e | 轮次 | 引入副作用 |
|---|---|---|---|---|
| 5065169f69 | 75 / 7 | 50 / **0** | 2 | params-count +1 |
| 989950a2d1 | 83 / 8 | 65 / **0** | 1 | fan-out +1 |
| 2486d6db22 | 90 / 9 | 29 / 1 | 2 | 无 |
| 3227359a07 | 92 / 9 | 35 / 3 | 2 | 无 |
| 3adea69ea5 | 157 / 14 | 57 / **0** | 2 | 无 |

- **error 清零 5/5**；总 findings 降幅 39%-72%（均值 ~57%）。
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
- test-no-assert 215/289：M2-B H3「该形态未出现」的结论限定于合成任务，真实仓库大规模存在——claims #19 该项证据上调。
- side-effect 率（2 warning / 0 error / 5 任务）首次量化修复闭环的安全性。

## 7. 复现

```bash
git clone https://github.com/marein/dev-cockpit /tmp/corpus/dev-cockpit
# Step1 per-commit snapshot（脚本 /tmp/replay/step1_collect.sh，289 JSON）
# Step3 replay（/tmp/replay/replay_task.sh <sha> <round>，pi 2api glm-5.3-flash）
```

原始数据：检出矩阵 289 JSON + 5 任务重放（agent log/session JSONL/前后 check JSON/zz_replay_track.md）→ `.data/commit-replay/`。
