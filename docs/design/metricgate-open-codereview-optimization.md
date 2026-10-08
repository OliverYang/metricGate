# metricgate 可用性优化方案（参考 Open Code Review）

> 输入参考：[`open-codereview-reference.md`](open-codereview-reference.md)，对应 OCR 官方文档及公开源码中的流水线、过滤链、工具契约、预算守卫、委托模式、会话检查点与 CI 设计。
>
> 目标不是把 LLM 评审器搬进 metricgate，而是借鉴其工程边界：**metricgate 继续负责可复现事实、约束和可验证工单；LLM 由宿主 Agent 调用**。所有建议按价值/侵入/隐私/估算 token 排序。
>
> 状态：本文是方案，不代表功能已经实现。应按优先级逐阶段设计、测试、狗粮；每阶段独立提交并以 `go test ./...`, `go vet ./...`, `metricgate check .` 验收。

## 1. 现状与目标

### 已有基础（截至 Phase B）

- Git diff / merge-base 过滤、SARIF、统一 findings。
- Go/Java 的 policy rules、度量、架构约束、函数 clone、Java CK、符号索引、stub worklist。
- `metricgate context` / MCP `get_constraints`：预算 capped 的结构事实与约束包。
- MCP `check_code`、`check_path`、`list_stubs`、`find_symbols`；CLI `symbols`、`api snapshot/diff`、`nearclones`、`hotspots`。
- Core 设计约束：engine 纯库；深类型/数据流分析走外部 CLI ingest；finding 有统一模型。

### 优化目标

1. **少传上下文**：宿主模型只拿到本次变更相关的事实、约束、锚点和任务范围，而非整仓 dump。
2. **不丢硬约束**：预算不足时先丢低价值详情，不截断结构化 JSON、policy 或必要证据。
3. **输出可落地**：finding 除消息外能精确定位源代码/改动，并验证位置仍有效。
4. **可解释可恢复**：文件为何被选择/排除、哪些 policy 生效、哪些结果已计算，均可查。
5. **零 LLM 核心依赖**：新能力以 deterministic CLI/MCP tools 暴露给宿主 agent。

## 2. OCR 设计的适用性筛选

| 设计 | 决策 | 原因 |
|---|---|---|
| Preview + 每文件排除原因 | **直接借鉴，P0** | 无 LLM、零 token，可诊断路径/语言/policy；metricgate 当前只有 `check` 实际运行结果，筛选原因不够可见 |
| 分层规则解析 + `rules explain` | **借鉴，P0** | dogfood 与组织策略复杂时，能解释值从默认/policy/override 哪层来；须保持一条约束一个明确最终值 |
| Delegate/bundle（宿主 LLM 执行） | **核心方向，P1** | 与 metricgate 的“可信事实层”完全一致；确定性生成 diff + constraints + symbols + facts，LLM 自主推理 |
| Token 预算守卫/优先级降级 | **直接借鉴，P1** | 当前用字符≈token 估算，且 tail-cut 可能丢结构；预算应在选内容前分配，精确保留硬约束 |
| Tool schema 最小权限 | **借鉴，P1** | 现有 MCP 工具可用；用 capability/profile 组合限制 agent 暴露面，防止不必要的全路径操作 |
| 证据锚定 + 锚点再验证 | **借鉴，P1** | findings 目前主要是行号；编辑后行号可能漂移，snippet/hash/span 可验证证据仍存在 |
| Session JSONL + manifest + compare/resume | **选择性借鉴，P2** | 对未来多工具 ingest / 大仓库分析有价值；当前本地确定性单次 check 很快，不值得立即加会话系统 |
| LLM plan/main/comment-filter 流水线 | **不放入 core** | 会把模型、prompt、重试、花费耦合进确定性库；宿主 agent 应承担这些职责 |
| 完整 Web viewer / OTel SDK | **暂缓** | 实施成本大于当前本地 CLI/MCP 痛点；先输出 JSONL/run manifest 和稳定 JSON schema |

## 3. 优先级排序与实施方案

### P0-A：`metricgate explain` 与 `check --preview`（1–2 天）

**用户问题**：文件没被扫描/规则没触发时，不知道是 exclude、扩展名、语言、解析失败还是 policy 开关。

**方案**

- 纯 core API：`Engine.ExplainPath(path) Decision`，不得执行规则/改状态。
- CLI：`metricgate check --preview [path] [-format text|json]`；只走 collect/filter/config resolve，不启动解析器（除非为解释语言/语法必需）。
- 输出每个候选路径：`status=included|excluded|unsupported`、`reason`、`language`、`matched_pattern`、`effective_policy_source`、`rule_ids`。
- 增加 `metricgate rules explain <path>`：显示 banned-api、metric threshold、architecture/clone/MI 的最终值及其来源（builtin/policy/override）。
- exclude 语义采用当前 metricgate 的明确规则；不照抄 OCR 的 include 绕过安全门机制，避免配置语义无意变化。

**验收**

- 固定 fixture 覆盖 `.go/.java/unsupported/excluded/testdata`、policy disabled、allowed_files。
- preview 输出 JSON 可被稳定 snapshot；运行时 `Duration < 100ms`（fixture <100 files）。
- preview 不写 DB、不产生 findings、不触发 git shell-out（除显式 `-diff` preview）。
- 比较 preview 与 `Engine.Run` 的 included 文件集合，差异必须为 0。

### P1-A：Token-aware Context Pack v2（2–3 天）

**当前问题**：字符数 `/4` 只是粗估；`cutTail` 可能截断 JSON、constraint 行或符号提示。OCR 的设计启示是分段预算守卫 + 保留完整轮次；metricgate 应改为“原子事实条目”而不是对原始字符串硬截。

**方案**

1. 定义 `PackItem{section, priority, text, required, source}` 原子项；section 至少为 hard constraints、edited-file signature、direct deps、near symbols、clone reuse、metrics/context notes。
2. 预算接口 `RenderOptions{MaxTokens, TokenCounter}`；Tokenizer 接口在 adapter 层；core 内默认 deterministic estimate（显式标记 approximate）。
3. 按优先级装箱（不拆 item）：硬约束 > 当前文件公开签名/依赖 > 相似符号/clone > 深层统计；低优先级被省略时输出 compact omission manifest（项数/来源/理由）。
4. 返回 `Pack{Text, EstimatedTokens, ExactTokens?, Included[], Omitted[]}`。保留旧 `Render(...,maxChars)` 包装器以兼容 CLI/MCP。
5. MCP `get_constraints` 增加 `token_budget` 可选参数与实际估算；工具描述提示“超过预算会按优先级省略完整条目”。

**验收**

- 1k/2k/6k budget 均严格不超 token estimate；绝不截断 UTF-8、行或结构化对象。
- hard constraints 在 budget 过小无法全部容纳时返回显式 `budget_exhausted`，不静默丢失/截断。
- 同输入同预算输出 bytes 稳定；与 v1 对比：fixture 下硬约束召回 100%，token 总数不增加；报告降低了多少字符/估算 token。
- 提供至少一个真实 tokenizer adapter（按当前 Go/Java 常用模型可配置）；无 tokenizer 时写清 approximate，不虚称准确 token。

### P1-B：Delegate Bundle / `metricgate bundle`（2–3 天）

这是从 OCR 委托模式借鉴、最贴近产品定位的功能。metricgate 不做 LLM 请求，只给宿主 Agent 一个可审计的“评审任务包”。

**结构**

```json
{
  "schema_version": 1,
  "run": {"mode":"diff","base":"...","head":"..."},
  "budget": {"max_tokens":6000,"estimated_tokens":...},
  "constraints": [...],
  "files": [{"path":"...","status":"included","reason":"changed","diff":"...",
             "facts":[...],"symbols":[...],"findings":[...]}],
  "omitted": [...],
  "instructions": {"report_only_changed_code":true,"finding_schema":"metricgate.finding.v1"}
}
```

- preview 与实际 bundle 共用一个 selection plan，防止“预览说扫描 A、bundle 却送 B”。
- diff 模式复用 `internal/git` adapter；未跟踪文件视为全新；行范围用现有 `diff.Range`。
- 不含 LLM key、不自动联网、不写用户源码；默认只 stdout，`-out` 文件权限 0600 或显式告知。
- `instructions` 是宿主 agent 的 machine-readable contract，不将自然语言变成 gate。
- JSON 可被 MCP `get_review_bundle` 调用，提供同一 schema，减少 CLI/MCP 分叉。

**验收**

- 输出可 JSON round-trip；schema version 必须显式；同一 Git 状态重复构建内容稳定。
- changed file selection 与 `check -diff` 集合一致；paths repo-relative、slash-normalized。
- bundle token budget 超限按 P1-A 原子项策略省略；硬约束/当前 diff 不静默丢失。
- token 对比：与“把整仓 context + 整文件送给 agent”基线相比，当前 diff bundle 至少降低 50% 的可发送字符数（按固定人工 fixture 测，诚实报告）。

### P1-C：Evidence Anchors（1–2 天）

**方案**：在 `model.Finding` 新增可选 `Evidence{StartByte,EndByte,Snippet,SHA256}` 与 `AnchorStatus`；保持旧行字段兼容。

- tree-sitter findings 尽量取 node byte range；arch findings 从 import 节点；metric findings 从声明节点；外部 ingest 无 span 时仅保留 line range。
- 生成 `snippet_hash`，MCP `check_path` 或宿主在 patch 应用前能确认证据是否仍在原位。
- `startLine=0` / `anchorStatus=unanchored` 表示未定位，不凭空回填。
- SARIF 继续用 line/column；新增 properties 放 byte span/hash。

**验收**：Go/Java UTF-8 非 ASCII 前后、LF/CRLF、编辑前后锚点重验证；byte slice 必须落在 UTF-8 边界；旧 JSON/SARIF consumer 向后兼容。

### P2-A：只读 Session Manifest / Finding Compare（2–4 天，等 bundle/ingest 证明成本值得）

借鉴 OCR 的 resume/compare，但仅缓存确定性结果：`input_digest`（git tree/diff digest）、policy digest、tool version、file digests、findings schema version。

- 相同 `(input_digest, policy_digest, version)` 可以复用 metrics/index/findings；任何输入/policy 变化就失效。
- compare 分类 new / persisting / resolved / not-checked；“未检查”绝不算 resolved。
- 第一阶段只做 metadata + finding cache，不做任意运行续接，不持久化源码、不做 Web viewer。

**验收**：相同输入命中率 100%；改一文件只使其及受依赖图影响的包指标失效；policy 改动全局失效；缓存结果必须与冷跑逐字段一致。

### P2-B：Ingest 外部分析器（沿已有 B-6）

基于 `metricgate ingest --tool <adapter> --format sarif`，通过 CLI adapter 读 SARIF，映射统一 Finding。先 PMD SARIF 或 gitleaks，后 golangci-lint；工具不在时给可行动错误。设定工具/规则来源、版本、运行耗时与命中数元数据；不把第三方 binary 依赖引入 core。

## 4. 当前最优先的推荐

**下一项先做 P0-A `explain/preview`，再做 P1-A token-aware pack，再做 P1-B delegate bundle。**

原因：

- Explain/preview 最便宜、无模型依赖，先让 determinism 可见。
- Token-aware pack 是所有 LLM 接入共享的预算底座；先做它，避免 bundle/MCP 重复造裁剪逻辑。
- Delegate bundle 将 metricgate 从“可单独运行的 checker”转成可被任意 coding agent 调用的可信约束输入层，是与 OCR 委托设计结合后最具差异化的功能。
- Evidence anchors 可与 bundle 同阶段落地；Session resume / viewer 先延后，避免为毫秒级单次本地检查制造状态复杂度。

## 5. 风险与禁止事项

- **不复制 OCR 的 LLM 编排/评论过滤到 core**：模型、提示词、重试和花费仍归宿主 agent / 可选 adapter。
- **不把字符当准确 token**：没有 model tokenizer 时只能称 estimate，并输出误差或 lower/upper bound。
- **不以 tail truncation 处理结构包**：约束项和证据项按原子单位省略。
- **不增加在线遥测默认发送行为**：若未来加 telemetry，仅本地关闭为默认；不得采集 prompt、源码、API secret。
- **不把 session resume 作为下一项**：先证明 bundle/ingest 的冷跑成本和重复运行收益。
- **不把 testdata/测试定义写入生产 API surface**：API snapshot 的生产面过滤必须保留。

## 6. 推荐实施顺序

1. P0-A：`ExplainPath` / `rules explain` / `check --preview`，先写 fixture 与稳定 JSON schema。
2. P1-A：`PackItem` + TokenCounter + priority packer + omission manifest，兼容旧 Render。
3. P1-B：bundle JSON schema + CLI/MCP 双入口，共用一个 service。
4. P1-C：Evidence anchor 与 patch 后验证。
5. Dogfood：metricgate 自身跑 preview/bundle；发现不合理 selection 或不必要输出先修实现。
6. Evaluation：固定 3 个 repo fixture（本项目、Go 小仓、Java 小仓），量化冷/热运行耗时、context token、遗漏硬约束数、bundle/changed 文件一致率、锚点验证成功率。
7. P2 再决策：基于 B-6/实际 workload 数字决定是否做 Session manifest 或 ingest 适配。
