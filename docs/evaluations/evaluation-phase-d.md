# Phase D/E 评估报告：preview / explain / token pack / delegate bundle

日期：本次自主实现周期。环境：macOS arm64，Go 1.26。
本报告覆盖 D0–D4（选择核心 → explain → 原子 token pack → delegate bundle →
evidence anchors）的落地与**可证伪验收**，对应设计文档
[docs/metricgate-ux-bundle-design.md §5](../design/metricgate-ux-bundle-design.md#5-阶段实施与验收计划本轮仅设计)
的 E 阶段。数值全部本机实测；不能测的命题直接标“未验证”，不写结论。

## 1. 落地清单

| 阶段 | 提交 | 能力 | 关键验收 |
|---|---|---|---|
| D0 | `3c819ef` 含 | `internal/selection` 纯选择核心 + `Engine.Preview` + CLI preview；Run/Preview 共用同一 selection | preview 与实际 Run 文件集合相等（测试） |
| D1 | `3c819ef` 含 | config provenance + `Engine.ExplainPath`（effective rule/metric/global 约束 + 来源） | effective 值与真实引擎行为一致（测试） |
| D2 | `3c819ef` 含 | `TokenCounter`/`PackItem` + `RenderBudgeted`（完整文本计数、原子项、required 不丢） | 1k/2k/6k 预算、required 无损、stable、UTF-8 完整、超限结构化失败（测试） |
| D3 | `3c819ef` | `internal/bundle` builder + CLI `bundle` + MCP `get_review_bundle`（同一 builder） | schema roundtrip / stable bytes / privacy / 变更集一致（测试） |
| D4 | `7686857` | `model.Evidence` 锚点（byte span + SHA-256 + anchorStatus），进 SARIF `properties` 与 check JSON | 规则命中=exact 且 hash 命中原始字节；度量=line_only；越界降级；CRLF/非 ASCII 字节偏移仍一致（测试） |
| — | `afb71d2` | 出站信任边界：`selection.SanitizePath` 统一所有 LLM 面向工件的路径 | 绝对 root 下 bundle/context/MCP 均 0 绝对路径泄漏（测试） |

## 2. 可证伪验收（设计文档 §5 评审 5 的五条硬指标）

| 指标 | 判定 | 结果 |
|---|---|---|
| preview/Run included set 相等 | 逐路径集合 | ✅ `TestPreviewMatchesRunSelectionWithoutParsing` |
| bundle bytes deterministic | 两次构建逐字节 | ✅ 实测 `cmp` **IDENTICAL**（另见单测） |
| diff range == FilterSummary 输入 | 1-based、变更文件有 range、未变更文件无 | ✅ `TestBundleDiffRangesFeedFilterAndEnforceHardConstraints` |
| hard constraint recall = 100% | 引擎启用的 rule 全部出现在 bundle constraints | ✅ 同上测试断言 |
| privacy（绝对路径不入出站工件） | 绝对 root 下扫描 | ✅ bundle/context/MCP 各 **0** 泄漏（`SanitizePath` 单测 + 实测） |

## 3. 量化：token / 字符预算（见 [§2.6](../design/metricgate-ux-bundle-design.md) “省 token”）

语料：本仓库自身（`/tmp` 拷贝，112 个 tracked 文件，即“3 repo fixture”中的“本项目”）。

| 发送内容 | 字符数 | 相对整仓源码 |
|---|---:|---:|
| 整仓源码（all tracked `*.go`） | 425,276 | 100% |
| `bundle`（diff + 选中 facts + 约束；compact JSON） | 6,832 | **1.6%（降 98.4%）** |
| `bundle` pretty 打印 | 9,683 | 2.3% |
| `metricgate context -max-chars 6000` | ≤6,000（硬上限） | — |

诚实边界：基线是“整仓源码全量发送”，这是**委托 bundle 要替换的最坏情况**；
若对方本来只发变更文件全文，则差距缩小（本例单文件全文基线约 8.2k 字符，
bundle 6.8k，仍更小，但优势有限）。**未测**：真实 LLM 会话内的 token 计费差异
（需真实 agent 接入，见 [ab-experiment.md](ab-experiment.md)）。

预算语义实测（本例）：

| `-max-chars` | used | budgetExhausted | 说明 |
|---:|---:|---|---|
| 6000 | 4,611 | false | 完整 bundle 装入 |
| 2000 | — | true（返回 `budget_exhausted`） | required（约束+diff）超限，不静默截断 |
| 600 | — | true | 同上 |

`-token-budget` 走 bytes/4 估算并在 `budget.exact=false` 标注近似，不对模型 token 作硬保证。

## 4. 性能

| 命令 | 语料 | 耗时 |
|---|---|---|
| `metricgate bundle . -max-chars 6000` | 本仓库 112 文件 | **0.06 s**（3 次 warm，稳定） |
| `metricgate check .`（全量） | 本仓库 | 见下（dogfood，0 error） |

bundle 耗时量级为毫秒—几十毫秒，符合“会话前脚手架的 ms 级”定位。**未做**冷/热分离
与 Go toolchain 大语料（Phase B 已记录 `check` 15s 量级；bundle 只处理变更集，成本随
变更集大小而非仓库大小增长）。

## 5. Dogfooding 与真实修复记录（与 Phase A/B 同纪律：不放宽阈值）

1. D3 落地后自家闸门先报 **11 个 error**（本次新增的 bundle/CLI/测试）。逐个真实重构：
   拆 `Build` 为 `validate/collectChanges/normalizeFacts/addFilesAndItems/fitEnvelope`；
   `cmdBundle` 抽独立文件并分解；`cmdRules` 渲染抽 helper；拆分超标测试函数。
2. dogfooding 抓出**真实仓库缺陷**：`.gitignore` 首行 `metricgate` 把 `cmd/metricgate/`
   目录一并忽略，导致 CLI 主源码 `main.go` **从未被 git 跟踪**。已修（只忽略根二进制）。
3. clone 规则报出新增测试 helper 与既有 helper 重复 → 新增 `internal/gittest` 共享真实
   Git 测试 fixture，`git`/`bundle` 测试复用，**消除 3 个 Type-2 clone**，不豁免。
4. D4 新测试圈复杂度 17（error）→ 拆 `findingByRule/assertExactAnchor/assertLineOnlyAnchor`，
   不调阈值。
5. 出站边界实测不一致（`context`/MCP 泄漏绝对路径、`bundle` 不泄漏）→ 收敛为唯一原语
   `selection.SanitizePath`，所有出站 producer 共用；本地审计保持诚实（可为绝对路径）。

最终：`metricgate check .` = **0 errors，gate PASSED**；`go test ./...` 17 包全绿；
`go vet ./...` 通过；`gofmt -l .` 空。

## 6. Evidence 锚点实测

对真实文件构造 `empty-block` 违规，`check . -format json` 与 `-sarif` 中：

```
startByte=42 endByte=46 snippetHash=sha256:037b08fb…c0cdd246 anchorStatus=exact
```

用 `sha256(src[42:46])`（`src[42:46] == b'{\n\t}'`）独立复算，**与报告的 hash 完全一致**。
即锚点是“可机器校验的证据”，不是装饰字段。

## 7. 结论与边界

**已证明（可复验）**：selection parity、bundle 逐字节 deterministic、diff range 可喂
`FilterSummary`、hard constraint 100% 覆盖、出站零绝对路径泄漏、bundle 相对整仓源码
降 98.4% 字符、evidence hash 与原始字节一致。

**未验证（诚实声明）**：对 LLM 生成质量/成本的改善仍是唯一未验证命题，需按
[ab-experiment.md](ab-experiment.md) 做真实 agent A/B；本报告不给未测结论。
Java 小仓 fixture 本轮未单独建（Java 路径由既有 engine/arch/stub 测试覆盖），
不把未跑的数据写进结论。
