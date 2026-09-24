# metricgate 可解释选择、预算 context 与宿主 Agent Bundle 技术方案

状态：**设计提案，尚未实现**。本方案根据 Open Code Review 的公开设计模式重新设计，**不复制 OCR 代码/命令/内部实现**；沿用 metricgate 的 Go API、core/adapter 分层、统一 policy、facts/index、findings 和现有 diff/clone/arch 能力。

参考整理：[`open-codereview-reference.md`](open-codereview-reference.md)。
本项目优化目标与阶段评估框架：[`metricgate-open-codereview-optimization.md`](metricgate-open-codereview-optimization.md)。

## 0. 设计目标与非目标

### 必须实现的结果

1. `preview` 显示哪些路径会被检查、哪些会被排除、每个选择决定的原因；preview 与 `Engine.Run` 使用**同一个选择计划**。
2. `rules explain` 显示某语言/路径实际生效的规则、度量阈值和约束来源，不要求用户读 YAML 和 Go 装配代码。
3. Context 从“构造大字符串再裁剪”转为**原子条目预算**：明确包含/省略什么，不截断 JSON、签名或硬约束。
4. `bundle` 为宿主 Agent 输出版本化、只读、可审计的任务输入：diff + 路径选择 + policy constraints + facts + symbols + 现有 findings；metricgate 不调用 LLM。
5. CLI/MCP 共用 schema 与 builder；运行时输出确定性、JSON 可回归、隐私边界清楚。

### 非目标

- 本轮不加入 LLM client、prompt、自动评论过滤、重试循环或在线遥测。
- 不改变 `-diff` gate 语义、既有 policy 的 exclude 语义、SARIF 行号语义。
- 不宣称估算 token 数等于模型 tokenizer 的精确数；除非请求端提供对应模型 tokenizer。
- 不把全部仓库源代码打入 bundle；仅包含选中 diff 和按需、预算内事实。
- 不实现 OCR 式语义文件分组。包依赖/符号相关性仅作为确定性排序/关联信号，不让 LLM 决定选哪些文件。

## 1. 当前实现核对（方案基线）

代码核对时的关键事实：

- `internal/engine.Engine.collect` (`internal/engine/engine.go`) 直接 WalkDir，根据 `Config.Exclude` 的**目录名精确匹配**跳过目录；扩展名由 `parser.LanguageForFile` 限定。
- `internal/sources.Files` 只排 `.git` 并收支持扩展名，不使用 `policy.exclude`；`facts.Collect` 使用 `sources.Files`。
- 因而 **check 与 context 的文件集合目前可能不同**，例如 policy 默认排除 `testdata`，facts collector 并不排除它。
- `Config.Exclude` 是 `[]string`，当前语义是目录名，不是 OCR 式 glob；本方案不隐式改变语义。
- `config.LoadFile` 读 YAML 后的 `Config` 不保留值来源信息；只能读最终值，无法解释值是内置默认还是文件显式覆盖。
- `facts.Render(files,cfg,maxChars)` 有四级细节降级，最终 `cutTail` 仍可能截文本；budget 单位是字符，CLI/MCP 文档以“约 4 字符/token”解释，不是精确 token。
- MCP `get_constraints` 接收 `path`、`max_chars`，独立调用 `facts.Collect` + `facts.Render`。
- MCP `check_path` 调 `Engine.Run`；`list_stubs`、`find_symbols` 已存在；CLI `context` 与 MCP context 分别装配。
- `model.Finding` 当前 1-based 行/列，无 byte-span/hash evidence。

这些事实是设计约束：不能为 preview 另写一份“看起来相同”的过滤逻辑；不能让 context/bundle 静默漏掉 policy；不能把 token 估算伪装成精确计数；不能让 adapter 反向侵入 core。

## 2. 架构决策

### 2.1 新模块与依赖方向

沿用现有三层：

```text
CLI / MCP shells (cmd/*)
        ↓
Adapters (internal/bundle, internal/facts, internal/git, internal/sources)
        ↓
Core (internal/selection, internal/config, internal/engine, internal/model)
```

建议 package 归属：

- `internal/selection`（core）：**纯函数**评估一个候选路径的选择结果；不遍历目录、不读文件、不打印、不 import facts。
- `internal/engine`（core）：持有运行时 `Config`，把已有 WalkDir 遍历改为调用 `selection.Plan` 的 decisions；Run 仍由同一个 plan 产出的 Included 路径驱动。
- `internal/facts`（adapter）：对已有 facts 与 policy 结果做 context pack item 提取；context 收集调用共享 selection，保证默认路径集合一致。
- `internal/contextpack` 不单独建成 core 包：第一阶段放在 `internal/facts`，避免 core 反向依赖 facts（当前 architecture policy 把 facts 归 adapter）。当非 facts 调用者出现时再评估抽包。
- `internal/bundle`（adapter）：组合 selection、`internal/git`、facts、engine 的现有只读 API，构造版本化 Bundle；CLI/MCP 只做参数转换和序列化。

依赖禁止：`internal/engine` 不 import `facts` / `git` / `bundle`；`selection` 不 import任何 IO 包；context pack 不调用 engine 解析第二次（应复用一个 selection result + facts facts）。

### 2.2 共享路径决策：`internal/selection`

纯数据类型建议：

```go
type Status string
const (
    Included Status = "included"
    Excluded Status = "excluded"
    Unsupported Status = "unsupported"
)

type Reason string
const (
    ReasonSupported = "supported_language"
    ReasonDirectoryExclude = "policy_exclude_directory"
    ReasonUnsupportedExtension = "unsupported_extension"
    ReasonExplicitPath = "explicit_path"
)

type Candidate struct {
    Path string // repo-relative, slash-normalized
    IsDir bool
}
type Decision struct {
    Path string `json:"path"`
    Status Status `json:"status"`
    Reason Reason `json:"reason"`
    Language string `json:"language,omitempty"`
    MatchedExclude string `json:"matchedExclude,omitempty"`
}
type Plan struct {
    Included []Decision
    Excluded []Decision
    Unsupported []Decision
    DirectoriesPruned []Decision // summary decision: do not enumerate descendants
}
func Evaluate(root string, candidates []Candidate, exclude []string) Plan
```

**语义规则**：

1. Root 自身不是 exclude 候选；保持现有 `Engine.collect` 的边界行为。
2. 目录 exclude 仍按现有“目录 basename 精确匹配”规则。命中后输出一个 `DirectoriesPruned` 决策，不需继续递归列举百万个被排除文件。
3. 只对 regular files 产生 file decision；命中已排目录的后代不会重复产生 decisions。
4. 文件扩展名映射通过 `parser.LanguageForFile(filepath.Base(path))` 的纯映射适配传入；selection 的输出必须记录对应 language，不复制硬编码 Go/Java 扩展名表。
5. 目录 traversal 的 IO 留在 caller adapter；engine 为了节省扫描可在 WalkDir 回调命中 exclude 时 `SkipDir`，同时记录 prune decision。Preview 使用相同 `EvaluateCandidate`，不需要完整枚举被剪枝目录。
6. Path 输出统一 repo-relative + `/`；原始 OS path 只留在内部读取层。
7. Binary 检测不纳入 P0（当前 Engine 未做）；unsupported 与 parse error 分开，不能预先把二进制误报为 unsupported language。

**单一来源实现策略**：不要让 `selection.Evaluate` 与 `Engine.collect` 分别重写规则。提供纯 `Decide(path,isDir,...)`；engine traversal 和 preview adapter 共调用。`Plan` 聚合是 adapter 逻辑。facts Collect 默认是否应用 `Exclude` 需明确 API：

- 保留当前 `facts.Collect(root)` 的兼容语义（unfiltered facts collection）以免 breaking public API。
- 新增 `facts.CollectSelected(root, cfg)` 或 bundle-only helper，内部复用 selection plan；`context` / `bundle` 明确传 policy 后改用它。
- 引擎和 CLI preview 共用相同 `Config.Exclude` decision；验证二者 Included 路径一致。

### 2.3 Preview JSON schema

`metricgate check --preview [path]` 输出 `PreviewReport`：

```json
{
  "schemaVersion": 1,
  "root": ".",
  "files": {"candidate": 18, "included": 12, "excluded": 4, "unsupported": 2},
  "included": [{"path":"internal/engine/engine.go","language":"go","reason":"supported_language"}],
  "excluded": [{"path":"testdata","reason":"policy_exclude_directory","matchedExclude":"testdata"}],
  "unsupported": [{"path":"README.md","reason":"unsupported_extension"}],
  "errors": []
}
```

Text mode以 summary + 每条 reason 显示。JSON 字段均有 schema version；路径稳定排序。IO 错误与“unsupported”分开，读取权限失败应是 `errors` 或命令错误，不可伪装为排除。

`check --preview` 不能调用 `Engine.Run` 再丢弃 findings，因为那会 parse/执行规则、产生成本和副作用；必须只生成 selection plan。Preview 不写趋势库，不触发外部 `git`，除非用户显式要求 diff-preview（后续单独设计）。

### 2.4 约束/规则 `explain`

现有 Config 无 provenance，不可从值可靠猜来源。引入明确解析结果而非反射猜测：

```go
type Source string // builtin | policy_file | rule_pack | command_override

type EffectiveValue[T any] struct {
    Value T
    Source Source
    SourcePath string // only for policy_file
    Explicit bool
}
```

配置兼容策略：

- 保留 `config.Load(data)` 作为现有 API；新增 `LoadWithSource(data, source)` 内部使用，`LoadFile(path)` 设 `SourcePath=path`，`Default()` 标注 builtin。
- 记录 YAML 实际显式设置的 key，不能把默认 YAML 展开后的值误称用户显式覆盖。首期只解释现有有用项：`languages.<lang>.rules.*`、`metrics.*`、`banned-api`、`architecture`、`clones`、`exclude`。
- Built-in rule metadata 从 `rules.RulesFor`（需要不含 policy bans 的纯默认集时用 `rules.ForLanguage`） 得到 ID/title/default severity/default enabled；最后应用 config override。对 data-driven banned rule 显示 `source=policy_file` 和 entry `match`，不是伪称内建 rule。
- 对每个 path 只报告其语言适用规则和 metrics；architecture/clones 是全局约束单列。输出顺序稳定。
- 命令建议 `metricgate rules explain <path> [-config policy.yaml] [-format json]`；可同时做 `metricgate explain <path>` alias，但先只保留一个主名以减小 CLI 面。

结果每条包括 `id/title/enabled/severity/effectiveOptions/source/sourcePath`。对无 policy override 的默认规则显示 builtin；对配置禁用的规则仍列出并显示 `enabled=false`，不能从清单中消失。

### 2.5 Token-aware 原子 Context Pack

#### 2.5.1 预算计数契约

建立轻量接口（首期放 `internal/facts`）：

```go
type TokenCounter interface {
    Count(text string) (n int, exact bool, err error)
    ID() string
}
```

- `CharEstimate`：兼容当前 `max_chars`，报告 `exact=false` 与算法 ID，不称精确 token。
- `ByteUpperBound`：可作为“通用上界”安全模式；UTF-8 字节数 ≤ 单字节 tokenizer 的 token 上界，但可能严重浪费预算，应由调用方显式选择。
- Exact model tokenizer 只能由未来 adapter 注册，必须携 tokenizer/model ID；禁止 core 为了 tokenizer 引入网络请求/模型库。
- 默认兼容模式继续接受 `max_chars`；新参数 `max_tokens` 与 `max_chars` 同时指定时报参数错误，避免含混。
**整包计数规则**：tokenization 可能跨 PackItem 边界合并 token，故逐 item token 数相加不能称为整包 exact。Exact counter 必须对候选拼接后的完整文本复算；可先用估算挑选，再整包复算，超限时移除最低优先级 optional item 并重装。分隔符、header、omission manifest 也计入预算。

#### 2.5.2 PackItem 与优先级

```go
type PackItem struct {
    ID string
    Section string
    Priority int // smaller = more important
    Text string // complete renderable item; never slice
    Required bool
    Source string // file:line, policy key, index record, clone class
    EstimatedTokens int
    Exact bool
}
type PackResult struct {
    Text string
    TokenBudget int
    TokensUsed int
    CounterID string
    Exact bool
    Included []PackRef
    Omitted []Omission
    BudgetExhausted bool
}
```

优先级必须确定且在文档固定：

0. Required hard constraints（banned API、arch forbidden/layer、用户显式 rule constraints）。
1. 当前目标文件 exact signatures、package/import facts、当前 diff 的关联 definitions。
2. Changed-line findings / breaking API facts（bundle 请求时）。
3. Same package/direct dependency symbol hints。
4. Exact clones / near clones / package D 或 hotspot 提示。
5. 非目标文件扩展 facts、历史/装饰性信息。

装箱：同优先级按 stable `ID` 升序；只能加入整条 PackItem；若 Required 项无法全纳入则返回 `BudgetExhausted=true`，列出 overflow Required 条目或预算错误，不输出一个看似完整但实际上漏约束的 pack。Optional 超限则记入 `Omitted{ID,reason="budget"}`。

新增正式接口 `RenderBudgeted(files,cfg,options) (PackResult,error)`；CLI/MCP 改用此接口，可把 `budget_exhausted` 返回为结构化错误。旧 `Render(files,cfg,maxChars) string` 保留为 deprecated 兼容 wrapper（不破坏现有调用编译），其历史裁剪语义只为外部兼容，不作为安全预算保证；本项目自身 CLI/MCP 不再调用旧 wrapper。
#### 2.5.3 多次 parse 避免重复成本

现状 Render 收到 facts 后才渲染，但 clone/symbol index 基于 FileFacts，不需二次 tree parse。新设计继续使用调用方传入 facts/index；不让 `Render` 自己 `Collect` 路径。MCP `get_constraints` 一次 `CollectSelectedWithIndex`，CLI context 同一 service；避免 Preview + Bundle 各自解析造成三次扫描。

### 2.6 Delegate Bundle 结构与数据边界

定义版本化 `internal/bundle`（adapter layer，不在 core），只组装确定性结果：

```json
{
  "schemaVersion": 1,
  "run": {"root":".","mode":"workspace|range|commit|directory","base":"","head":""},
  "policy": {"digest":"sha256:...","source":"policy.yaml"},
  "selection": {"included":1,"excluded":0,"unsupported":0,"decisions":[...]},
  "budget": {"requested":6000,"used":...,"counter":"char-estimate-v1","exact":false,"budgetExhausted":false},
  "constraints": [{"id":"banned-fmt-Print*","severity":"warning","source":"policy_file"}],
  "files": [{"path":"...","language":"go","diff":{"ranges":[...]},"facts":{},"symbols":[]}],
  "omitted": [],
  "instructions": {"findingSchema":"metricgate.finding.v1","onlyReportChangedCode":true}
}
```
**Git 数据缺口**：当前 `internal/git.ChangedLines` 只返回 changed-line ranges，不返回 unified diff/hunks。D3 不应假定已有 diff-body API；必须在 `internal/git` adapter 新增 `LoadChanges(ChangeRequest) ([]FileChange,error)`（workspace/range/commit，返回 repo-relative path、status、hunks、added text），并让 `ChangedLines` 共用该解析结果或由其派生。shell-out 仍只在 `internal/git`，engine 继续只依赖 `internal/diff` range 类型。需用真实 Git 临时仓库测试 rename/delete/untracked/staged+unstaged/CRLF/quoted path。

- `workspace` 由 Git adapter 收集 tracked diff + untracked files；`range` 使用 merge-base；`commit` 使用 parent diff。当前 `internal/git.ChangedLines` 只返回 changed-line ranges，不返回 unified diff/hunks。D3 必须在 `internal/git` adapter 新增 `LoadChanges(ChangeRequest) ([]FileChange,error)` 返回 repo-relative path/status/hunks/added text，再由同一解析结果派生 `ChangedLines`；shell-out 仍只在 git adapter。用真实 Git 临时仓库测试 rename/delete/untracked/staged+unstaged/CRLF/quoted path。
- 默认每个 file 只传 changed diff + compact facts；完整源代码需显式 `include_source=true`，默认 false。
- 不发送 API keys/env、系统路径、absolute path。绝不调用外网/LLM。
- Secret-like paths 的 hard deny 是 open decision：首期**不要未经用户确认就套入 OCR 敏感路径列表**（那会改变 metricgate 检查目标且产生隐式 blind spot）；bundle 若发现潜在 secret path，只返回 `privacyWarning`，支持显式 policy exclude。后续可研究独立 secrets detector ingest。
- `existingFindings` 默认省略：Bundle 是评审前输入，不默认调用 `Engine.Run`，避免 engine parse 与 facts collect 重复解析。若确认有需要，再显式增加 `include_existing_findings=true`；该模式必须在 budget/metadata 标明额外分析与耗时，并评估是否值得做中立 parse artifact/hook 复用 AST（不得让 engine import facts adapter）。
若显式开启 `include_existing_findings`，schema 才在 file item 中附 `existingFindings`；不启用时省略字段，而不是输出空数组以免宿主误解为“已检查且无 findings”。
- CLI `metricgate bundle [path] [-diff|-base] [-token-budget N] [-format json] [-out file]`；MCP 新 `get_review_bundle` 复用同 builder，默认只返回 JSON structuredContent，不返回长自然语言。
- `-out` 文件采用新建/覆盖语义须 CLI 文档明确；建议权限 0600（仅 Unix）并警告 bundle 可能包含源码 diff。
- schema compatibility：`schemaVersion` major bump；未知 future optional fields consumer MUST ignore。

### 2.7 Evidence Anchor

建议 `model.Finding` 新增 optional：

```go
type Evidence struct {
    StartByte uint `json:"startByte,omitempty"`
    EndByte uint `json:"endByte,omitempty"`
    SnippetHash string `json:"snippetHash,omitempty"` // SHA-256 of exact source bytes
    AnchorStatus string `json:"anchorStatus,omitempty"` // exact|line_only|unanchored
}
```

- 仅当 finding 来源能确定 AST node 时写 byte span/hash；metric file-level 保持 line_only。
- Hash 输入用 source byte range 原始字节，不 trim/normalize；便于 edit 后重验。
- SARIF locations 仍是权威编辑器位置；Evidence 放 result.properties，不改变 SARIF schema。
- Bundle 在 diff 范围外 evidence 只引用，不附大段 source；MCP check response 向后兼容。

### 2.8 `rules explain` 具体解析

CLI JSON `RuleExplanation{path,language,policySource,selection,items[]}`。解析顺序：

1. `LanguageForFile` 得 language；unknown 才是 unsupported。
2. Selection 决策先于 rules；excluded 文件依然可解释排除原因，但规则项为空/标为 not_applicable。
3. `rules.RulesFor(lang,cfg)` 返回 rule definitions（包含默认 severity/enabled）；对每个应用 rule 调 Config override 得 final 值。
4. Metrics 用 `metrics.DefaultThresholds` + `MetricThresholds` 算 effective warn/error/enabled。
5. Global constraints 列 architecture policy、clone config 与其 config source。
6. `policySource` 仅报告实际来源（builtin/policy file + path）；当前并没有 global user config/CLI rule override，方案不伪装有这些层。

## 3. 兼容与迁移

- `Engine.Run` 返回值、finding 字段、`facts.Render` 旧签名保持；逐阶段加 wrapper，测试稳定后才考虑 deprecated。
- `facts.Collect(root)` 保持当前 API 行为；新增显式 `CollectSelected(root,cfg)`，context CLI/MCP 切到 policy-aware 路径并在 release notes 说明 context 不再泄漏默认 exclude 的 testdata。若用户需要 all files，增加显式 `-all`/`include_excluded`，不可暗中扩大。
- `Config.Exclude` 现行目录名语义不改；后续 glob 支持必须单独版本化/文档化并测 include/exclude precedence。
- `context.max_chars` 保留；新增 `max_tokens` 参数。字段互斥；旧 client 未发送 token budget 时行为保留（char estimate）。
- `Finding` 新字段 `omitempty`，旧 JSON/SARIF 消费者可忽略。
- Bundle 是新 schema，不替代 SARIF 或 check JSON。

## 4. 五轮技术方案设计评审（对本方案的反方审查）

### 评审 1：API/包分层

质疑：为了共享 selector，能否直接让 engine 调 facts？**否**：facts 属 adapter，而 engine 是 core；会违反已落地的架构策略。裁定：selection 放 pure core；enumeration 与 rendering 留 adapter；facts/engine 共享 decision semantics，不共享上层 package。

### 评审 2：选择一致性与默认排除

质疑：`sources.Files` 与 engine.collect 现在行为不同；直接切 facts 到 Engine.Run 文件清单是否会破坏 context 调用？裁定：旧 Collect 先保留，新增 policy-aware API；context 的默认调用改 policy-aware 并在 release notes 说明。Preview 与 Run 的 included set 必须同一 selection function，测试逐路径集合相等。

### 评审 3：Token 真值与预算失败

质疑：`len(text)/4` 不是 token counter，可否在 JSON 后 tail trim？**否**：tail trim 会静默破坏硬约束/结构。裁定：counter 报 `exact` provenance；原子 item packer；required 超预算必须返回结构化 budget failure；近似计数显式显示 `approximate`，不声称严格模型 token 上限。

### 评审 4：bundle 隐私、路径与 deterministic

质疑：委托包传完整文件是否更方便？风险大于收益。裁定：默认 only changed diff + selected facts；源码全量显式 opt-in；绝不放 credentials/absolute path；不联网。Bundle 由 Git mode + config digest + path normalization 构成确定 schema；不调用 LLM。

### 评审 5：验收可证伪性与过度范围

质疑：以“省 token”“更好用”作验收是否可测？裁定：必须对固定 fixture 给字符/token counter 值、hard constraint recall=100%、preview/Run files set equality=100%、bundle bytes deterministic、diff range 等于 FilterSummary 输入。Session cache、viewer、LLM reviewer 不在首批；先以真实 bundle 使用数据决定。

## 5. 阶段实施与验收计划（本轮仅设计）

| 阶段 | 变更 | 主要测试/验收 | 预计 |
|---|---|---|---:|
| D0 | selection core + Engine.collect 接入 + CLI preview | supported/excluded/unsupported/root-file/permission fixture；Run/preview included set 完全相等；preview 无 side effect | 1–2d |
| D1 | config provenance + rules explain | 每类 rule/metric/default/policy override fixture；effective value 与真实 engine 行为一致 | 1–2d |
| D2 | PackItem/TokenCounter + Render wrapper + MCP metadata | 1k/2k/6k；hard constraints 无损；stable output；UTF-8 完整；budget exhaustion explicit | 2–3d |
| D3 | Bundle builder + CLI | workspace/range/commit/untracked fixture；schema roundtrip/stable bytes/privacy checks；changed file set 等于 check diff selection | 2–3d |
| D4 | MCP bundle + evidence anchors | MCP/CLI 结果共用 builder；Go/Java CRLF/non-ASCII anchor revalidation；SARIF compatibility | 1–2d |
| E | 狗粮、真实使用模拟、量化报告 | 3 repo fixture；selection parity；hard constraint recall；token char/estimate delta；耗时；隐私扫描 | 1–2d |

逐阶段提交；每阶段 `gofmt -l .`、`go vet ./...`、`go test ./...` 和 `metricgate check .`；失败必须做真实修复，不通过阈值豁免完成阶段。

## 6. 必须解决后才能开工的问题

这是实现前的明确技术选择，不是向用户提问：

1. Context default 是否遵循 policy excludes：**是**；同时保留显式 all-files 选项，解决 facts/check 当前漂移。
2. Token budget 默认计量：**沿用 chars compatibility；新增 counter kind/provenance**。若要求 hard token cap，必须注入准确 tokenizer；没有 tokenizer 时不声称 hard token guarantee。
3. Hard constraints 超预算：**返回 `budget_exhausted`，不截断、不悄悄丢弃**。
4. Bundle 默认源代码范围：**仅 diff**；`include_source` opt-in。
5. 远程 LLM/tool call：**本方案不做**，由宿主 Agent 控制。
6. API 来源 provenance：metricgate 目前单 policy 文件 + built-in defaults；source model 仅区分 builtin/policy file/explicit CLI override（若未来新增），不照搬 OCR 的 user-global 层级。
