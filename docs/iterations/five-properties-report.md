# 五性总结报告 · 审美回路工程化验收

日期：2026-10-11（goal mv3oop7j-0d8hel task-6 收尾）

本报告汇总 5 次迭代（accept 账本 → 自定义规则 DSL → metric 维度 trend → layer 修复建议 → deps scan + 测试基线），对"可介入 / 可审计 / 可度量 / 可回溯 / 可观察"五性做最终核对：每性给出机制、跨迭代证据与仍存在的边界。

## 可介入

**机制**：介入点内建在开发动线里，不依赖模型或人的自觉——MCP gate 摘要随每次工具调用返回、CLI `check -diff` 是收尾判据、`accept add` 是显式让步入口、`deps scan` 把配置动作变成一条命令。

**证据**：
- 迭代 1（[iter-1](iter-1-accept-ledger.md)）：check 收尾口径自动读账本，无需额外动作；gate 当场抓住新写的 cmdAccept 认知复杂度 26 / cyc 17，拆分后才放行。
- 迭代 2（[iter-2](iter-2-dsl.md)）：DSL 规则命中经 /tmp/dsldemo 三段式验证（命中/不命中/新违规）。
- 迭代 3（[iter-3](iter-3-metric-trend.md)）：gate 抓住 MetricTrend 与测试函数复杂度各 1 次。
- 迭代 4（[iter-4](iter-4-layer-advise.md)）：gate 抓住 suggestFix 不可达分支与 fixture 认知错误；两类修复建议文本经 e2e 证实可直接执行。
- 迭代 5（[iter-5](iter-5-deps-scan.md)）：gate 抓住 scanQuery cyc 17 → 拆出 collectHits；test_metrics 基线让测试文件的豁免从逐行让步升级为策略声明。

**边界**：MCP 摘要只在调用了 metricgate 工具时出现——模型若全程不调用任何工具，兜底是 `check -diff` 收尾判据与 pre-commit/CI 层（见 docs/workflow.md 分层）。

## 可审计

**机制**：一切让步与豁免落在版本库内可 review 的文件里（accepted.yaml、policy.yaml），每条带 owner/reason/过期日或注释；判定信号可复算。

**证据**：
- 迭代 1：匿名让步在 Validate 被结构拒绝（"an anonymous concession defeats the ledger"）；过期条目 stale 视为 open 保持可见。
- 迭代 2：DSL 规则 ID slug 可 grep，`rules explain` 报 PolicySource=policy_file；坏条目静默丢弃的代价已记录为审计权衡。
- 迭代 4：建议判型信号（跨层 fan-in ≥2）可从 graph 复算，单测钉住判型边界。
- 迭代 5：草拟项带 file:line 证据；`accept remove` 显式记录撤销（本迭代即用它把 2 条逐行让步替换为 test_metrics 基线，审计面变小）。

**边界**：DSL 坏条目是静默丢弃（与 BansFromConfig 同策略），未在 gate 输出里提示丢弃数量——可观察性留有改进位。

## 可度量

**机制**：所有判定走同一确定性管线（findings → RunSummary → history.db → trends/JSON/SARIF），度量本身也有度量（metric 快照 p50/p90/max）。

**证据**：
- 迭代 3：真实阈值实验（cognitive warn 15→10）显示分布不变、violated 29→152 阶跃——"分布是控制变量，violated 是实验变量"由分布不变性测试钉死。
- 迭代 5：deps scan 输出 distinct/uncovered 计数；本仓 13 imports / 0 uncovered 与 go.mod 30 项声明一致。

**边界**：
- 迭代 4 遗留：修复建议的采纳率未度量（多少违规被按建议修复 vs 人工改判），需 calllog/cockpit 侧跨 run 追踪。
- metric trend 目前是 Cockpit 全局口径，项目维度趋势待项目身份落地。

## 可回溯

**机制**：finding/建议/快照随 run 持久化（run_id 贯穿 findings、快照、trends 点），历史不被覆盖。

**证据**：
- 迭代 1：finding 带 accepted 标记保留在 JSON/SARIF 中（不消失）；remove 有输出痕迹。
- 迭代 3：每个 trend 点带 RunID+StartedAt，可回 findings 明细；阈值历史按 run 可查。
- 迭代 4：建议拼在 finding message，随 run 存 history.db。
- 迭代 5：引入/覆盖判定与 checker 共用 `Matches` 边界语义，草拟结论可复算。

**边界**：C/C++ 引号 include 与尖括号的区分同时依赖两处 raw 文本保真——deps scan 的 `impHit.raw` 与 checker 侧 `cIncludePath` 的首字符判断；任一处的消费者若再 trim 掉引号，误报会复现（已加 `TestUnknownDependencyQuotedIncludeIsRepoLocal` 把守）。

## 可观察

**机制**：同一份 finding/message 通过四个消费面零改动可见——文本报告、SARIF、MCP 工具返回（含 gate 摘要）、Cockpit 面板；CLI 提供 text/json 双格式。

**证据**：
- 迭代 1：`[accepted by ledger]` 标注 + note 行 + Cockpit 让步面板三处可见。
- 迭代 3：CLI `trends -metrics` + Cockpit METRIC EXPERIMENT 面板，阈值变更行高亮。
- 迭代 4：text/SARIF/MCP/gate digest 四面自动携带建议（message 通道）。
- 迭代 5：`deps scan -format json` 直接是 dependencies.allow 草案。

**边界**：Cockpit 的项目维度视图（按项目分组的 runs/趋势/让步）依赖项目身份持久化，属路线图后续项。

## 后续修复（goal 之后）

**C/C++ 引号 include 误报**（迭代 5 遗留的消费侧缺陷）：`internal/rules/c_rules.go` 的 `cIncludePath` 原先用 `strings.Trim(path, "<>\"")` 同时剥掉引号与尖括号，丢掉了「引号=仓库内部头文件」的信息，于是 `#include "local.h"` 被当成未声明依赖告警（deps scan 侧因保留 raw 文本而未受影响，checker 侧受影响）。已修：引号开头直接返回 `""`，由 `unknownDepRule` 的空路径分支放过；尖括号仍走系统头集合与 allowlist。

**回归测试**：`internal/engine/unknown_dep_test.go` 新增 `TestUnknownDependencyQuotedIncludeIsRepoLocal`（C 与 C++ 子测试：`"local.h"` 与 `<stdio.h>` 静默，仅 `<openssl/ssl.h>` 报一次）与 `TestUnknownDependencyAngleIncludeRespectsAllowlist`（`<real-lib/api.h>` 被 `dependencies.allow: [real-lib]` 抑制，`<other-lib/api.h>` 仍报）。

**验证**：`gofmt` 干净；`go build ./...` 通过；`go test ./internal/rules/ ./internal/engine/ ./internal/deps/ -count=1` 全绿；`/tmp/cdefect` 复现从 2 findings 降到 1（仅 `main.c:3 openssl/ssl.h`）；`check . -diff -base HEAD` PASSED（run #559，1 accepted warning by ledger）。

## 总结论

五性在 5 次迭代中均闭环成立（唯一 ⚠️ 是迭代 4 的建议采纳率未度量，已记为遗留）。工具在开发 metricGate 本身的过程中全程咬合：5 次迭代里 gate 共抓住至少 6 处新代码缺陷（复杂度超标×4、死代码分支、DSL 误报、fixture 错误），修复均发生在合入前。回路的剩余断点不在工具面，而在推广面（项目身份、建议采纳追踪、DSL 坏条目提示）。

## 全量验证

- `go build ./...` 通过；`go test ./...` 仅预先存在的 internal/digest TestBuildTaskFilteredBans 失败（与本工作无关，git stash 复证过）
- `gofmt -l` 空；`git diff --check` 通过；`node --check` app.js 通过
- 自吃狗粮 `check . -diff -base HEAD`：PASSED（1 条 main.go:35 fan-out 让步，1 条 ignore 指令抑制）
