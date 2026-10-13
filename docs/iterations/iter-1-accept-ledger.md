# 迭代 1：accept 审美让步账本 — 迭代日志

日期：2026-10-11 ｜ 状态：完成 ｜ 对应路线图：docs/roadmap.md 断点 B（inline ignore 匿名无账本）

## 交付物

| 层 | 文件 | 内容 |
|---|---|---|
| 账本 | `internal/accept/accept.go` | `Entry{RuleID,File,Line,Owner,Reason,Expires}`；`Load`（缺文件=空账本）/`Save`（tmp+rename 原子写）/精确三元组匹配/`Expired`/`Status{Open,Accepted,Stale}`/`Remove`/`Validate`（拒匿名无理由："an anonymous concession defeats the ledger"）；默认路径 `.metricgate/accepted.yaml` |
| CLI | `cmd/metricgate/accept_cmd.go` | `metricgate accept add/remove/list`（`main.go` 注册 + usage 更新）；add 必须 `-owner`/`-reason`，`-expires` RFC3339 可选 |
| 接线 | `cmd/metricgate/main.go` `applyAcceptLedger` | check 在报告前 annotate：`model.Finding` 新增 `Accepted bool`（omitempty，加法兼容；error 永不可 accept——fail_on 语义保护，stale 过期让步视为 open 回归 gate） |
| 报告 | `internal/report/text.go` | findings 行加 `[accepted by ledger]` 标注；gate 行显示 `N accepted warning(s) by ledger` |
| Cockpit | `internal/cockpit/dashboard.go` + web 三件 | `Dashboard.Accepted` 只读展示让步记录（owner/reason/过期 STALE 标记） |
| 狗粮 | `.metricgate/accepted.yaml` | 记录本仓 3 个预先存在 fan-out warning（calllog_test.go:43/176、main.go:35），owner=roadmap-iter1，2026-12-31 过期 |

## 测试与验证

- `internal/accept` 8 个单测：缺文件空账本、round trip、精确匹配拒绝邻近行、过期四态、Stale/Open/Accepted、Remove 重复条目、Validate 六种拒绝、Save 自动建目录。
- `/tmp/acceptdemo` e2e：`fail_on: warning` 下 test-no-assert 触发 FAILED(exit 1) → accept add → PASSED(exit 0，报告带标注) → remove → FAILED(exit 1) → 匿名 add 被拒。
- 狗粮：`check . -diff -base HEAD` → 0 errors，`gate: PASSED (fail_on=error, 3 accepted warning(s) by ledger)`，报告行带 `[accepted by ledger]`。
- 回归：`go build ./...`；全仓测试仅剩预先存在的 `internal/digest TestBuildTaskFilteredBans`（digest_test.go:389，git stash 已证与本次无关）；`node --check` app.js OK；gofmt clean。
- Cockpit API 实测：`/api/dashboard` 返回 3 条 accepted（修复了 Entry 缺 JSON tag 导致首探全 undefined 的问题——yaml tag 不等于 json tag）。

## 吃狗粮发生的事（真实记录）

1. **gate 抓住了我自己**：cmdAccept 首版单函数 cognitive 26 / cyc 17，双超阈值，被自家 `check` 报 error。拆成 `acceptAdd/acceptRemove/acceptList` 三个动词函数后清零——这正是断点 C 的解法预演（工具的断言让模型当场收敛）。
2. **拆分引发的连锁**：cmdCheck cyc 涨到 17 → 抽 `applyAcceptLedger`；main.go 超 800 行 file-length error → `accept_cmd.go` + `usage.go` 两个文件拆出。每个 error 修复都用 `check` 单文件复验，全链路无人工豁免。
3. **探针教训**：对单文件跑 check 时 root 是文件路径，`filepath.Join(root, DefaultPath)` 拼出非法路径 → 加 IsDir 保护；`go test` 的 calllog_test.go 单文件不进 sources.Files（check 显式文件列表语义），-diff 全仓口径才覆盖——契约以 `check . -diff` 为准。

## 五性核对（迭代 1 闭环判据）

| 性 | 结论 | 证据 |
|---|---|---|
| 可介入 | ✅ | check 收尾口径自动读账本，模型/人无需额外动作；accept add 即介入点 |
| 可审计 | ✅ | 每条让步有 owner+reason+过期日，进入版本库可 review；匿名条目在 Validate 被结构拒绝 |
| 可度量 | ✅ | gate 行报告 accepted 计数；报告行逐条标注；Cockpit 面板可见条目数 |
| 可回溯 | ✅ | accepted.yaml 持久化（原子写）；finding 保留在 JSON/SARIF 中带 accepted 标记（不消失）；remove 有输出痕迹 |
| 可观察 | ✅ | 文本报告 `[accepted by ledger]` + note 行 + cockpit 让步账本面板三处可见 |

## 断点与遗留（不阻塞迭代 2）

- **MCP 内联 gate（cmd/metricgate-mcp/gate.go）未接账本**：preamble 的 countSeverity 不读 accepted.yaml，MCP 路径上 accepted warning 仍会出现于 digest。CLI 是完成判据载体（AGENTS.md exit code），故闭环成立；MCP 接线记为后续增强（countSeverity 前同款 annotate 即可，改动点唯一）。
- 埋点：`metricgate accept add` 未写 calllog，Cockpit 无法区分"审查中让步"与"批量预登记"——迭代 3 度量留存时一并考虑。
- model.Finding.Accepted 只由 CLI 设置；`metricgate calls`/scorecard 不消费。
