# 提交级「LLM 生成仓库 × 门禁纠正」研判协议

日期：2026-10-09　状态：**预注册，未执行**
前置证据：R1（31.8% 首过违规）、R2（7/7 修复收敛，6/7 一轮）、M2-B（boolean-params 90× LLM 特异）、M2 主协议（B 类 3/3 防住）。本实验把同样的门禁放到**真实存在的公开 LLM 生成仓库**上，做 commit 级检出 + 挑拣级纠正重放。

> **判据版本（iter-31 补标）**：执行见 [evaluation-commit-replay-results.md](evaluation-commit-replay-results.md)（2026-10-09/10），
> 判据介于 `c701576`–`d5cb01c`，policy 为 157 行 `policy.yaml`。此后 `test-no-assert` 判据改过两次
> （`ecd57b5` / `d2bcf9b`）、TS/JS 函数单元判据改过一次（`760f498`）——若重跑，**先固定判据版本再比数字**，
> 协议 §6「工具与策略必须同版本」这条前置是 iter-30 用一次错误结论换来的（只回退二进制、policy 用了晚 5 天的
> 186 行版本，一度把可信归档标成「不可复现」）。
>
> 全仓判据版本索引：[criterion-version-index.md](criterion-version-index.md)（每篇报告的采集日期 → 当时 main → 之后变过的判据）。

## 0. 一句话结论（预填）

（执行后回填）对真实 LLM 写的开源仓库，metricGate 能否检出质量问题、纠正重放能否量化改善、以及工具会不会误伤干净的 LLM 代码。

## 1. 研究问题与预注册假设

- **H1 检出力**：门禁在 LLM 生成仓库的 commit 时点能检出多少问题（按规则族分层：架构 A / 度量 M / 模式 R / 禁用 API B）。无阈值判定——本协议先产出描述性分布。
- **H2 纠正增益**（预注册判定线）：挑拣 commit 的 findings 喂给修复代理（findings JSON，≤3 轮，R2 协议），**一轮收敛 ≥ 6/10 修复任务 = 证实**；≤3/10 = 证伪（低于 R2 的 6/7 基线即说明真实仓库比合成任务难修）。
- **H3 质量差**（预注册判定线）：LLM 原版 per-function 超阈率 ≥ 2× 人类基线（trend DB：npath 2.195%、fan-out 5.516%、boolean-params 0.024%，M2-B 口径）= 证实；纠正重放后该率回落 = 增益证据。
- **H0 误伤率**（用户 2026-10-09 加入）：抽 ≥5 个**零检出 findings 的 commit** 走完整流水线（含修复代理）。预注册：误伤 = 干净 commit 上修复代理产生任何新 finding 或改动语义。**误伤率 > 0/5 = 记录为 FP 发现并归因**（预期 0，因为 check 是确定性的，干净输入必然干净输出；真正的风险在修复代理环节把干净代码改坏）。

## 2. 语料

| 仓库 | 语言 | commits | 角色 |
|---|---|---|---|
| marein/dev-cockpit | Go | 100+，细粒度，README 自证「100% vibe coded」 | 主语料，全 commit 检出 |
| kayba-ai/ace-ts | TypeScript | 119，细粒度，README 自证「written entirely by Claude Code running in a loop」 | 辅语料，全 commit 检出（前置：新增 tree-sitter-typescript grammar） |

排除与备注：NVlabs/vibetensor 仅 2 commits 且 C++/Python 超范围，只作定性引用（其论文自述的「Frankenstein Effect」— 组件各自鲁棒、组合后全局次优 — 正是 metricGate 全局耦合/分层检查的理论靶子）；wow-look-at-my/gosmopolitan 是 golang/go fork，体量过大，备选。

## 3. 方法

### Step 1 全量 commit 检出（零 LLM 成本）
对每个 commit checkout 后跑 `check . -format json`（全仓口径；分层判定需跨包边，不能用单目录）。产出：commit × 规则 检出矩阵 + 度量快照 + 时间序列（问题引入点 / 自然消亡点）。构建失败（`go build` / `tsc`）的 commit 单独标记为完整性数据，不算检出对象。

### Step 2 原版质量画像
LLM 仓库度量分布 vs trend DB 人类基线，同指标同阈值直接对比（per-function 口径，M2-B H3 方法）。

### Step 3 纠正重放（挑拣，LLM 成本）
预注册采样标准（≤10 个 commit，每个 ≤20 条 findings，总 findings 预算 50-100 条）：
1. 优先含 error 级或新规则（npath/fan-out/boolean-params/call-chain-depth/test-no-assert）findings 的 commit；
2. 规则族配额：架构 / 度量 / 模式至少各 2 个 commit；
3. findings 总数 >20 的 commit 不重放（无代表性且修不动）；
4. 纯文档 / CI / 测试 commit（diff 无生产源码）排除。

重放协议照 R2：findings JSON + 文件源码 → headless pi glm-5.3-flash 修复 → `check` 复检 → ≤3 轮。记录：轮次、token、修复前后度量、**副作用率 = 修复引入的新 findings 数**（R2 未测过的新指标，真实仓库上更有说服力）。修复不提交回原仓库——这是研判，不是贡献。

### Step 4 误伤对照（H0）
≥5 个零检出 commit 走同样的重放流水线（喂空 findings 或告知「无需修复」），验证：a) 工具确定性（同 commit 两次 check 结果 byte 一致）；b) 修复代理不产生改动或新问题。

## 4. 完整性与边界（预声明）

- 修复由同一模型（glm-5.3-flash，2api 端点）执行，非人类修复；与 R1-R3 端点一致，但外部仓库原生开发用的是 Claude Code，跨模型生成-修复可比性受限。
- dev-cockpit / ace-ts 是两个特定项目，非 LLM 生成代码的随机样本（自证「全 LLM 写」的公开仓库本身就是幸存者集合）。
- commit 级检出含「后来自然修复」的问题——H1 读数是时点检出力，不是缺陷终局数。
- 前置工程：tree-sitter-typescript grammar 引入 + adapter lowering + 指标/规则可跑性验证（~1d），预计覆盖 ace-ts 的度量族与模式族；禁用 API（banned-api）按 Go/Java 词表不可移植，TS 检出能力以 grammar 落地后实测为准。

## 5. 复现

```bash
# 语料克隆
git clone https://github.com/marein/dev-cockpit /tmp/corpus/dev-cockpit
git clone https://github.com/kayba-ai/ace-ts /tmp/corpus/ace-ts
# Step 1（示例，单 commit）
cd /tmp/corpus/dev-cockpit && git checkout <sha>
/path/to/metricgate check . -config /path/to/policy.yaml -format json > out/<sha>.json
```

原始数据（检出矩阵 / 重放会话 JSONL / 复检 JSON）归档至 `.data/commit-replay/`，照 M2-B 惯例。
