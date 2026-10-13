# 迭代 3：metric 维度趋势与阈值实验视图

日期：2026-10-11 · 对应 docs/roadmap.md 断点 A（metric_snapshots 表存了 13k+ 快照但没有任何查询面）

## 交付物

| 文件 | 内容 |
|---|---|
| `internal/store/metritrend.go` | `MetricTrend(limit)`：按 run×metric 聚合出 `MetricPoint{Samples,P50,P90,Max,Warn,Err,Violated}` 序列（最老在前）；`scanMetricTrend`/`absorbMetricRow` 分层；`MetricPoint.finish()` 在 Go 侧算百分位（SQLite 无 percentile 扩展） |
| `cmd/metricgate/trends.go` | `metricgate trends -metrics` 新视图：text（每 metric 一块表格）/json；metric 选择面板 |
| `internal/cockpit/dashboard.go` | Dashboard 增 `metricTrend` 字段（全局口径，20 run 窗口） |
| `internal/cockpit/web/{index.html,app.js,style.css}` | "METRIC EXPERIMENT" 面板：metric 下拉 + 每 run 表格；阈值变更行以虚线高亮（`.metric-step`）；选择跨刷新保持 |
| `internal/store/metritrend_test.go` | 4 测试：聚合+阈值阶梯、窗口与空库、metric 隔离、**分布不变性**（分布不变性单列一测，钉住实验的对照组） |

## 设计决策

- **快照表不新增列**：`warn_threshold`/`err_threshold` 每条快照本来就存了，阈值变更历史天然可查——"阈值变更前后对比"是纯查询问题，不是 schema 问题。
- **Violated 列是实验的因变量**：分布（p50/p90/max）是对照组。收紧阈值后 violated 阶梯上升而分布持平 = 纯政策效应；两者同动 = 代码也变了。这是"这次收紧值不值"的数据基础。
- **Distribution invariant 单测**：把"只改阈值分布不能动"从口头约定变成被钉住的回归测试。

## 吃狗粮实录

1. **真实阈值实验（合同要求）**：本仓 policy `cognitive-complexity warn 15→10` → 两次 `check` → `trends -metrics` 显示 run #523 (warn=15, violated=29) vs #524/#525 (warn=10, violated=152)：**violated +123，p50/p90/max 完全不动**。实验后恢复 policy。
2. **gate 抓住自己两次**：
   - `MetricTrend` cognitive 16 / function-length 59 / npath 600 → 拆 `scanMetricTrend` + `absorbMetricRow` 后清零。
   - `TestMetricTrendAggregatesAndThresholdStep` cyclomatic 16（阈值 15）→ 拆 `thresholdExperimentSeries` fixture + 主断言/分布不变性两测后清零。
3. **自研 DSL 抓住自研 DSL 的语义 bug（跨迭代联动）**：迭代 2 的狗粮规则 `go-test-func-naming` 只配 `exclude_regex` 时，把 `countDistinctRuns` 这类完全非测试函数也标了——exclude-only 走"违规模式"语义等于"标记一切不合规者"，从未声明谁是候选。修复：`dslOnMatch` 增加显式双模式——**只有 exclude_regex = 合规断言模式**（该类节点全部在范围内），**match+exclude = 违规模式**（match 选候选，exclude 豁免）；狗粮规则改为 `match_regex: '^(?i)(test|benchmark|fuzz|example)'` + `exclude_regex: '^(Test|...)[A-Z_]'` 后 `countDistinctRuns` 不再误报、`/tmp/dsldemo` 的 `alsobad` 仍正确命中。
4. **测试 seam 教训**：`saveRunForTimestamp` 首版 `defer tx.Rollback()` 没有 `Commit`，run 行被静默回滚（0 行）——探针输出"series = 0"而非报错，靠 `-v` 逐测对照定位。

最终：`go build ./...` OK；全测试仅剩预存 `internal/digest TestBuildTaskFilteredBans`；gofmt/diff-check 干净；self-gate PASSED（3 accepted + 0 error）。

## 五性核对

| 性质 | 结论 | 证据 |
|---|---|---|
| 可介入 | ✅ | gate 抓住 MetricTrend 与测试函数复杂度各 1 次，均为本轮拆分清零；DSL 误报当天修复 |
| 可审计 | ✅ | 阈值历史在快照里按 run 可查（Warn 列阶梯可见）；policy.yaml diff 即实验记录 |
| 可度量 | ✅ | 本迭代即度量面本身：p50/p90/max/violated per run；真实实验数据 #523 vs #524 |
| 可回溯 | ✅ | 每个点带 RunID+StartedAt，可回 findings 明细（RunFindings/快照同 run_id）；分布不变性测试防口径漂移 |
| 可观察 | ✅ | CLI `trends -metrics`（text/json）+ cockpit METRIC EXPERIMENT 面板，阈值变更行高亮 |

**闭环判据达成**：回答"这次阈值收紧值不值"有数据，不靠感觉——本仓实验 15→10 使 violated 29→152（+123）而分布持平，结论：收紧成本高（全仓 8.8% 函数将变 warning），维持 15 是数据支持的决定而非默认。

## 断点遗留

- cockpit `metricTrend` 目前仅全局口径；project-scoped 视图待项目身份沉淀更完整后补。
- 百分位在 Go 侧逐 run 计算，窗口极长（>1000 runs）时全量拉快照；现数据量（500 runs ≈ 13k 快照）无压力，量大后再考虑 SQL 预聚合。
