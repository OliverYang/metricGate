# Phase B 评估报告：确定性度量/约束扩展

日期：本次自主实现周期。测量环境：macOS arm64，Go 1.26.2；同一工作区、同一 Go toolchain corpus。
方法按 [phase-b-design.md §7](../design/phase-b-design.md#7-量化评估协议) 预注册；数值不够支持结论时直接标为不可比。

## 1. Phase B 落地清单

| 子阶段 | 提交 | 新能力 | 验证 |
|---|---|---|---|
| B-1 | `4bdf399` | Martin Ca/Ce/I/A/D、SDP | 公式对拍；57 个包级快照（实现时自仓库） |
| B-2 | `433905c` | Halstead-V、函数级 MI | 手算锚点、边界测试；MI 进快照/metrics 表 |
| B-3 | `281ab9d` + fix | API surface snapshot/diff | self-diff 0 变更；缺失 API 模拟 exit 1；生产面 163 exports/19 packages |
| B-4 | `7ada898` + dogfood fix `9210583` | 函数级 Jaccard near-clone 报告 | Copy+rename+small edit fixture 检出；闸门驱动拆分复杂函数 |
| B-5 | 当前提交 | git churn × (100−MI) hotspots | numstat 手算、tiny-git repo、非 git 退出测试 |

完整设计依据、5 轮方案评审、裁定和 B-6 ingest 接口草案见 `docs/phase-b-design.md`。

## 2. 性能与资源

### Go toolchain 语料（`$(go env GOROOT)/src`）

同一二进制/配置三次 wall-clock：

| Run | 文件数 | Findings 数 | Metrics snapshots | wall clock |
|---:|---:|---:|---:|---:|
| 1 | 5,606 | 33,219 | 361,193 | 14.00 s |
| 2 | 5,606 | 33,219 | 361,193 | 13.99 s |
| 3 | 5,606 | 33,219 | 361,193 | 14.63 s |
| **中位数** | — | — | — | **14.00 s** |

解释：指标快照数上升是每个内部 package 新增 Ca/Ce/I/A/D 快照、每个函数新增 MI 快照造成的；near-clone 作为独立 CLI pass，不计入 `check`。本次平均耗时处于 14 秒量级。

**版本间基线不可比（诚实说明）**：尝试在 Phase A 最后提交 `03b338f` 建临时 worktree 复跑，但该提交树里没有 `cmd/metricgate`（只有 `cmd/metricgate-mcp`）；基线命令失败，先前产生的空输出/0 秒明确作废，未纳入表格。Phase A 公开实测（不同语料版本）为 Go toolchain 5,606 左右文件约 12.3s（A-3），但语料/工具改动状态不完全可复现，故只作历史参照，不计算百分比归因。

near-clone pass 是独立 `metricgate nearclones` 扫描，复杂度与时间应在需要时单独测；本轮未记录可靠的三次性能数值，避免把开发机一次性 warm-cache 时间冒充基准。其设计避免全量 O(n²)：按稀有 3-gram postings（出现于 ≤8 个函数）生成候选对。

## 3. Token/context 预算

- `metricgate context internal -max-chars 6000`：实测 **5,986 字符**，满足硬上限；约 1,497 tokens（按 4 chars/token 估算）。
- 新增 context 成本：Martin 包层摘要最多 3 行；clone near-match 默认不塞入 `context`（独立报告命令）；API snapshot 不进 prompt；MI 以单列进入 metrics CLI，不进 prompt。符号/clone 的 Phase A 区块仍受同一 `maxChars` degradation + tail-cut 约束。
- 未能提供“同一文件集合、B 前后 tokens”严格差值：Phase A 快照语料缺失，且现有 context pack 随当前代码树更新。未来可用固定 fixture golden pack 精确回归；本轮硬上限与实际字符数是可复验值。

## 4. 方法公式对拍与行为验证

- **Martin**：`I=Ce/(Ca+Ce)`，孤立包定义 I=0；`A=abstract/total`；`D=|A+I−1|`。测试对链式包精确校验 Ca/Ce/I/A/D；SDP 测试构造 I(u)=0.25、I(v)=2/3 的稳定包依赖不稳定包，精确验证违例方向与 import 锚点。真实 dogfood：57 包快照、0 SDP findings。
- **MI(Halstead-V)**：实现 `V=N·log₂(η)`，`MI=max(0,(171−5.2 lnV−0.23 CC−16.2 lnLOC)·100/171)`。手算 fixture `V=1000, CC=10, LOC=100 → MI≈34.03`；退化输入保证有限且在 [0,100]。
- **MI scope 校正（重要）**：初版按文件 LOC 套用经典 MI，dogfood 发现 100–800 LOC 文件的 ln(LOC) 项令 65/85 经典 bands 不可达（400 行文件理论上限约 44）；按 VS function-level scope 改为函数 MI，文件报告最差函数。这是方案文档预先承诺的公式/误报审查，不是事后隐瞒。
- **MI gate 校正**：真实仓库函数 MI 中位数 45，范围 37–71；经典 65/85 在本实现的 scale-restored 变体上过紧。按“异味只报告”纪律，默认阈值 0/0（不 gate），可在 policy 明确设阈值启用；MI 仍进入每函数快照和 `metrics` 的 MI 列。
- **API surface**：导出面为生产源码（排除 `_test.go` / `testdata`），Java 类成员单独以 receiver 入表。self snapshot → self diff 为 **0 API changes**；对子目录 `cmd` 与全仓基线比较，正确列出 removals 并 exit 1。Java 类成员与测试排除各有单测。
- **Near clone**：Jaccard 3-gram 集合 `|A∩B|/|A∪B|`，默认阈值 0.8、函数最小 10 行/40 token；测试夹具 copy+rename+少量改动达到阈值，不同形状不匹配。自仓库 `nearclones` 首跑 13 对，主要为相似测试样板（87–100%）；没有生产代码对。报告不闸门。
- **Hotspot**：`churn=Σ(added+deleted)`；`score=(fileChurn/maxChurn)×(100−MI)`。手算对拍：max churn 200、A=(100, MI50)→25，B=(200,MI90)→10，未测 MI 的 C=(50, debt100)→25（同分按 path 稳定排序）。非 git 目录返回错误；真实 CLI `-since '7.days ago' -max 5` 可复验。

## 5. Dogfooding 与修复记录

1. B-2 文件级 MI 初版导致 63 个 error（公式尺度/粒度不适合 gate）。没有压低 metric 阈值或豁免：改函数级 MI + default report-only，保留明确 policy opt-in。
2. B-3 自 diff 首次出现 fixture `clones.Original` 的跨 testdata 移动。发现 API surface 把 testdata/测试导出误当生产 API；修复为只快照生产文件，并加回归测试；当前 self diff 0 changes。
3. B-4 初版 `FindNear` cognitive 32 / cyclomatic 19、`ScanNear` cognitive 39 / cyclomatic 17 被自家闸门报 error。真实拆成 `shinglePostings`/`rarePairs`/`similarPairs`/`capPerAnchor` 与 `collectShingles`/`parseWith`/`fileShingles`/`findBody`，不豁免。
4. 最终 `metricgate check .`：**0 errors，46 warnings，gate PASSED**（警告是既有复杂度债与新指标信息项；新增 near-clone 不产生 findings）。`go test ./...` 14 个测试包通过，`go vet ./...` 通过，`gofmt -l .` 空；最终 2,606 metric snapshots。

## 6. 结论与边界

Phase B 为原有 deterministic constraint/context 管线增加：包稳定度量、函数维护性、SemVer API 闸门、近克隆分析、git 风险热点。它们没有引入 LLM 依赖；context 仍受 6k 字符预算约束。定量结果目前支持“单次全仓 check 约 14s/5606 文件、固定 context budget、API self diff 可复验”；**不支持**声称“比 Phase A 快/慢多少”或“near-clone 误报率为零”，因为严格同版本基线与人工盲审样本未收齐。未来如要发布误报率，应预先抽样 200 对并由独立 reviewer 标注。
