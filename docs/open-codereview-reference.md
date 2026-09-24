# Open Code Review 文档整理与工程设计参考

> 来源：Open Code Review 官方文档站 [open-codereview.ai/docs](https://open-codereview.ai/docs)，以及官方公开仓库 `alibaba/open-code-review` 中 `pages/src/content/docs/zh/` 的中文文档源。本次仓库源核对 commit：[`dc2beb56`](https://github.com/alibaba/open-code-review/commit/dc2beb56fa7a55a85918da4c4e43f5b0dc0dbca8)。本文是结构化摘要与工程设计摘录，不是逐字转载；具体 CLI 参数和版本以官方原文为准。
>
> 关联方案：见 [`metricgate-open-codereview-optimization.md`](metricgate-open-codereview-optimization.md)。

## 1. 项目定位与工作模式

Open Code Review（OCR）是面向代码评审的 CLI/Agent 系统。它把“工程确定性”与“LLM 判断”拆开：Git diff、文件筛选、规则选择与上下文检索由程序负责；模型专注评审，结构化评论再经锚定、过滤和输出。

官方 quickstart 描述三种评审输入：

- **Workspace**：staged + unstaged + untracked 工作区变更。
- **Range**：对 `merge-base(from,to)..to` 做分支区间评审。
- **Commit**：评审单个 commit 引入的变化。

另有 `scan` 全文件扫描、`--preview` 无 LLM 预览、Agent 委托模式（OCR 只提供文件范围与规则，宿主 Agent 负责推理）。

## 2. 主流水线

官方架构页描述的高层阶段：

1. Bootstrap：解析模型端点、规则模板、工具注册表。
2. Diff provider：得到统一 Diff 模型，支持 Workspace / Range / Commit。
3. 文件过滤与规则解析。
4. 可选语义分组：只给模型文件元数据（路径、状态、增删行数），把相关文件聚为一组；失败时退回逐文件。
5. 每组独立子任务并行执行：可选 Plan → 多轮 Review → 评论收集。
6. 评论锚定 / 校验 / 输出。

值得注意的边界：跨文件推理以分组为边界；其它组文件只能作为只读上下文，不能成为当前评论目标。这既帮助模型理解依赖，也限制“从上下文文件臆造问题”的误报。

## 3. 文件选择与规则治理

### 3.1 文件筛选顺序

OCR 文档给出的选择链（高优先级先执行）：

1. 二进制文件排除。
2. 内建敏感路径排除（例如凭据文件），不能被用户 include 覆盖。
3. 用户 exclude。
4. 用户 include 命中时保留，并绕过后续扩展名 / 默认路径过滤。
5. 支持扩展名检查。
6. 默认排除测试、fixture、生成物等路径。
7. diff/token 预算超限则标为 too-large，不进入 LLM。

仓库根噪声目录还会更早在 Git diff 层过滤。`--preview` 展示每个文件保留/排除的原因，不需要模型调用。

### 3.2 规则分层与追溯

规则按 custom CLI override → repository config → user-global config → system defaults 解析；每层缺失可静默跳过，第一条匹配的 path glob 决定规则正文。`rules check <path>` 显示最终来源层、匹配模式和生效规则；规则支持 include/exclude 与有序 path-specific rules。

### 3.3 可借鉴原则

- 排除理由必须可见；筛选规则必须可诊断。
- 高优先级的安全排除不能被普通 include 绕过。
- `include` 的语义要说清：它是绕过部分默认门的 override，还是严格白名单，不能模棱两可。
- “一条路径最终对应一条有效规则”的结果应该有只读 explain 命令。

## 4. 工具契约与评论锚定

官方内建工具按阶段分权：Plan 只有只读检索工具；Main 才能提交评论和结束任务。工具用途包括读文件、读 diff、搜代码、找文件、提交带锚点的评论和结束任务。注册表可替换/白名单化；冲突或拼写错误显式警告。

评论不是裸文本：包含内容、`existing_code` 锚点、可选 `suggestion_code`。锚定按顺序做：

1. 在 diff hunk 新侧 context+added 行中滑窗匹配；若失败尝试旧侧。
2. 再扫描完整的新文件。
3. 仍失败则可运行独立重新定位任务；最终无法锚定时用 `start_line=0` 明示“未定位”，不伪造行号。
4. 主流程结束后再解析一次行号，并有独立评论过滤阶段。

跨文件只读上下文不能直接成为评论目标；评论需对应本次变更文件的可观察证据。

## 5. 预算控制与记忆压缩

OCR 将提示词预算与 completion 输出预算分别配置，且在多个阶段做 token 守卫：

- 单文件 diff 在筛选阶段超预算则提前标出 `too_large`。
- 相关文件组再做总预算检查；组预算超限时拆成单文件组。
- 进入 LLM 前做 fail-fast token 预算检查，超限记录 warning 并跳过组。
- 多轮工具对话用 frozen / compress / active 三分区：固定 system/初始 user，保留最近完整轮次，把较早轮次摘要为带标记的 memory。
- 60% 软阈值异步启动压缩；80% 硬阈值同步压缩，保证下次请求前有足够预算。
- 压缩失败无法降至阈值下时，不继续发请求。

设计价值不只在“截短 prompt”：它维护关键约束与最新轮次，丢弃旧工具细节，并把压缩阶段做成可观测事件。

## 6. 会话、可恢复性与可观测性

- 每次评审以 append-only JSONL 存本地；session 可列举、查看、导出、比较、删除。
- Resume 不是盲目续跑：run manifest 校验模式、解析后的 Git 输入、规则/文件集和模型来源；不匹配则整体拒绝或只重跑损坏文件，不静默复用过期结果。
- Session compare 将问题分类为 new / persisting / resolved / not reviewed；最后一类不误算成“已修复”。匹配依据路径、类别、片段而非行号，可应用 rename 映射。
- Viewer 检查会话、逐文件状态、评论、对比及存储内容；遥测可记录阶段耗时和 token，但文档声明不传 prompt/响应内容。

## 7. 配置、安全与 CI

- 交互式 provider 配置与非交互 `config set` 共用同一配置模型；支持 built-in/custom provider、环境变量回退、按次覆盖。
- 凭据优先使用系统 Keychain/Secret Service 或命令取密，而非明文配置。
- MCP server 支持本地 stdio 与远程 Streamable HTTP；可工具白名单；对请求离机、用户级配置、秘密/私有代码风险有明确警告。
- CI 以 JSON 输出 + `audience=agent` 保证 stdout 纯净，评审评论与摘要分开发布；token budget 能保留部分结果；版本固定、并发可调。
- PR 可控文本通过环境变量传入脚本，避免 GitHub `${{ }}` 直接插入 shell 命令造成注入。

## 8. 对 metricgate 的适配矩阵

| OCR 设计 | 对 metricgate 可用性 | 初步结论 |
|---|---:|---|
| Explain/preview 的逐文件决策链 | 高 | 适合 `metricgate explain`，展示支持语言、排除规则、配置来源及最终适用限制 |
| 委托模式（OCR 提供确定性脚手架，宿主 Agent 推理） | 很高 | 可输出 diff + 事实 + constraints + 硬预算的结构化 review bundle；不在 metricgate 内调用 LLM |
| `existing_code` 精确锚点与未锚定标记 | 高 | findings 可附 source span/hash；MCP 编辑器消费时验证锚点，拒绝静默错位 |
| token 多级守卫与分区压缩 | 很高 | Context pack 需要真实 tokenizer/估算器、分区优先级与可解释裁剪；但预算超限不能截断 JSON/规则结构 |
| 有序规则解析 + explain | 高 | policy 需要来源/覆盖链说明；banned-api、metrics、architecture 等输出“为什么生效” |
| checkpoint/resume/session compare | 中高 | 适用于 expensive 的批量检查和 ingest；本地确定性检查本身通常太快，不应强行增加会话层 |
| 语义分组 + 并行 LLM 子任务 | 低（核心中）/高（外层） | 应由宿主 Agent/未来 adapter 编排；metricgate 保持纯确定性 core |
| MCP 工具白名单与阶段能力 | 高 | 新增 MCP 能力时 schema/工具范围最小化；远程工具明确数据外流提示 |
| LLM 评论过滤/重新定位 | 中 | metricgate 本身不做 LLM；可提供确定性 finding 校验/锚点校验，语义 false-positive filtering 留给宿主模型 |
| OTel 与会话 viewer | 中高 | 先做无内容遥测/JSONL run manifest；避免首期开发完整 Web viewer |

## 9. 官方来源索引

本次对照的文档源包括：`quickstart.md`、`installation.md`、`configuration.md`、
`cli-reference.md`、`review-rules.md`、`architecture.md`、`tools.md`、`mcp.md`、
`integrations/delegate.md`、`integrations/ci.md`、`integrations/agent-skill.md`、
`viewer.md`、`telemetry.md`、`faq.md`（均为仓库 `pages/src/content/docs/zh/` 下的文件）。

- [快速开始](https://open-codereview.ai/docs)（文档首页）
- [架构](https://open-codereview.ai/architecture/)
- [评审规则](https://open-codereview.ai/review-rules/)
- [CLI 参考](https://open-codereview.ai/cli-reference/)
- [配置](https://open-codereview.ai/configuration/)
- [工具](https://open-codereview.ai/tools/)
- [MCP](https://open-codereview.ai/mcp/)
- [委托模式](https://open-codereview.ai/integrations/delegate/)
- [CI 集成](https://open-codereview.ai/integrations/ci/)
- [Agent Skill](https://open-codereview.ai/integrations/agent-skill/)
- [会话 Viewer](https://open-codereview.ai/viewer/)
- [FAQ](https://open-codereview.ai/faq/)
- [公开文档源码（中文）](https://github.com/alibaba/open-code-review/tree/main/pages/src/content/docs/zh)

---

本地参考摘要生成于访问官方文档时；OCR 功能、版本和参数可能随上游更新而变化。实施前再次对照官方源码和当前 metricgate 的 API/策略。
