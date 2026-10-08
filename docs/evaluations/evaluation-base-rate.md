# R1 基率实验：agent 生成代码违反本仓库约束的概率（实测）

> 本文是 [`value-proof.md`](../strategy/value-proof.md) 证据阶梯 **R1 级**的实测报告。
> 它测的是价值公式里唯一没测过的项 **P**：
> `价值 ≈ P(模型违反本仓库约束) × 每次拦截省下的返工 − 闸门成本(≈0)`。
>
> 数据采集：2026-09-27/28，单机。协议可复现，命令见 §7。

## 0. 一句话结论

**方向未被推翻**：22 个真实任务里 **7 个（31.8%）** 产出了至少一条本仓库约束的违规，
**2 个（9.1%）** 产出了 **error 级**违规 —— 即这道闸门会真实拦下它们。

但价值的**分布和项目原先的假设不同**，这比总数更重要：

| 类别 | 基率 P | 是否被编译器替代 | 读数 |
|---|---:|---|---|
| **C 度量阈值** | **18.2%** (4/22) | 否 | 最高产；全部 error 级都来自这里 |
| **A 禁用 API** | 9.1% (2/22) | 否 | **仅当任务把模型推向该行为时才触发**；未诱导的 20 个任务为 0/20 |
| **B 架构分层** | 4.5% (1/22) | **否（编译器接受）** | 自然产生、无诱导 —— 本次最有价值的一点 |
| D 重复 / B 依赖白名单 | 0% (0/22) | — | 本任务集未触达，**属设计局限，非结论** |
| **API 幻觉（编译失败）** | **0% (0/22)** | — | **负结果**：本实验不支持该价值主张 |

## 1. 协议

**变量**：同一模型家族，同一批任务，生成器**看不到本仓库的约束模型**。

- **生成器 = policy-blind agent**：在仓库的**副本**里工作，副本删除了
  `policy.yaml` / `.metricgate` / `docs` / `README.md` / `AGENTS.md` / `.pi`。
  保留完整源码 —— 即"裸 agent 能看到代码库、但看不到形式化约束"的基线。
- **任务 = 22 个**，全部**落在既有包内**（不是新包），这样分层规则、依赖白名单、
  既有 API 才有机会被触发。任务分三轮：
  - Round 1（T01–T12）：小、自足、单函数的"易"任务类。
  - Round 2（U01–U04、U06–U08）：**强制组合既有包 API** + **算法自由度**（cron 字段解析、
    LCS、CSV 行解析）的任务类。
  - Round 2 补充（U05、U09、U10）：CLI 子命令、**需要 git 历史**、**需要打印到 stdout**
    的任务，用于探测架构类与禁用 API 类。
- **度量**：把每个沙箱用**真实 policy**跑一遍
  `metricgate check . -config <真实 policy.yaml> -format json`，
  只看落在生成文件（`zz_eval_*`）上的 findings，按类聚合。
- **同时度量**：`go build ./...` 是否通过（**API 幻觉的客观代理**）。

**判定口径**（沿用 value-proof.md）：某类 `P ≥ 10–15%` ⇒ 该类值得做工具；
全类 `P < 5%` ⇒ 停止该方向。

## 2. 环境与污染控制

| 项 | 处理 |
|---|---|
| 约束泄露 | 副本中删除所有**显式枚举约束**的文件（policy、README、AGENTS、docs）。验证：`grep -rl "banned-api\|fmt.Print\*"` 在副本中为 0 命中 |
| 生成器隔离 | 每个沙箱独立副本，agent 之间互不可见；禁止读写真实仓库路径 |
| 任务提示 | 包含"可以阅读包内既有代码以了解风格与可用 API" —— 这是**真实的** agent 行为，但它**降低**了违规率，见 §5 |
| 校验 | 所有 build 结果由本人复验，不采信生成 agent 的自述 |

**过程中发现并修复的环境缺陷（记录在案）**：初次构建副本时用了
`rsync --exclude 'metricgate'`，该模式匹配**任意路径段**，把整个 `cmd/metricgate/`
目录也排除了。影响分析：Round 1 与 U01–U04/U06–U08 的任务全部位于 `internal/`，
其 findings 只取决于该文件本身 + 它参与的包图，`cmd/` 不在其中，且
`internal/** → cmd/**` 是禁止边、不存在，故**分层判定不变** ⇒ 数据有效。
U05 正好要往 `cmd/metricgate` 加代码，**该样本作废**，已在修正后的副本（锚定排除
`/metricgate`）上重跑，本文使用重跑结果。

## 3. 结果

### 3.1 逐任务

| 任务 | 内容 | severity | 类别 |
|---|---|---|---|
| t01–t03, t05, t06 | 语言目录 / severity 生效值 / 比例 / Finding 构造 / 区间合并 | — | 干净 |
| **t04** | 路径模式匹配（`**`/`*`） | warning | C 度量（cognitive 21） |
| t07–t12 | id 规范化 / Top-N / 中位数 / 分数格式化 / 桩分类 / 相似度 | — | 干净 |
| u01–u03 | 目录摘要 / 最近运行计数 / Top 风险 | — | 干净 |
| **u04** | 影响面计数（组合 `facts` API） | warning | **B 架构（layer-violation）** |
| u05 | CLI 子命令 | — | 干净 |
| **u06** | cron 字段解析 | **error** + 2 warning | C 度量（CC 20 / cog 25 / len 63） |
| **u07** | LCS | warning | C 度量（CC 12） |
| **u08** | CSV 行解析 | **error** + 2 warning | C 度量（cog 31 / CC 14 / nesting 5） |
| **u09** | git 改动次数（需要 git 历史） | warning | **A 禁用 API（banned-os-exec）** |
| **u10** | 打印选择判定到 stdout | 2 warning | **A 禁用 API（banned-fmt-Print\*）** |

### 3.2 原始 findings（全部 12 条）

```
t04 [warning] cognitive-complexity  @ internal/selection/zz_eval_t04.go:62  matchSegments 21 (>15)
u04 [warning] layer-violation       @ internal/impact/zz_eval_u04.go:4     core(internal/impact) may not depend on adapter(internal/facts)
u06 [warning] cognitive-complexity  @ internal/config/zz_eval_u06.go:55    parseCronElement 25 (>15)
u06 [error]   cyclomatic-complexity @ internal/config/zz_eval_u06.go:55    parseCronElement 20 (>15)
u06 [warning] function-length       @ internal/config/zz_eval_u06.go:55    parseCronElement 63 (>50)
u07 [warning] cyclomatic-complexity @ internal/diff/zz_eval_u07.go:11      LCS 12 (>10)
u08 [error]   cognitive-complexity  @ internal/parser/zz_eval_u08.go:19    SplitCSVLine 31 (>25)
u08 [warning] cyclomatic-complexity @ internal/parser/zz_eval_u08.go:19    SplitCSVLine 14 (>10)
u08 [warning] nesting-depth         @ internal/parser/zz_eval_u08.go:19    SplitCSVLine 5 (>4)
u09 [warning] banned-os-exec        @ internal/engine/zz_eval_u09.go:5     os/exec is banned: only the git adapter and the CLI may shell out
u10 [warning] banned-fmt-Print*     @ internal/selection/zz_eval_u10.go:63 fmt.Printf is banned
u10 [warning] banned-fmt-Print*     @ internal/selection/zz_eval_u10.go:66 fmt.Printf is banned
```

## 4. 关键发现

### 4.1 度量阈值是最高产的一类，而且集中在"算法自由度"任务上

4 次命中全部来自**需要自己设计控制流**的任务（模式匹配、cron 解析、LCS、CSV 解析），
且 **2 个 error 级全部在此**（`parseCronElement` CC 20、`SplitCSVLine` cog 31）。
反观 Round 1 的小工具函数（12 个任务）只有 1 次命中。

**含义**：只要 agent 被要求"写一个解析器/匹配器"，越阈值的概率就显著上升。
这是 agent 工作流里最常见的一类任务。

### 4.2 架构违规会自然产生，而且编译器抓不到（本次最强的一点）

u04 的任务只是"复用包内既有的影响面分析，返回定义数/引用数/受影响文件数"。
agent 为了便利，直接 `import "metricgate/internal/facts"` —— 而 `internal/impact`
属 `core` 层、`internal/facts` 属 `adapter` 层，policy 声明 `core` 只能依赖 `core`。

**核实**：真实仓库的 `internal/impact` 并不依赖 `facts`，这条 `core→adapter` 边是
**新引入的**；`go build ./...` **通过**。

**含义**：这是"确定性策略层"不可替代的直接证据 —— 编译器、类型检查、linter
都不会报，只有把架构声明成数据才能抓。而且它**没有被我诱导**（任务描述里没有任何
"跨层"暗示）。

### 4.3 禁用 API 只在任务把模型推向该行为时才触发

2 次命中分别来自"请用你认为最直接的方式拿到 git 历史"（→ `os/exec`）和
"打印到**标准输出**"（→ `fmt.Printf`）。在另外 **20 个没有指向禁用行为**的任务里，
`banned-api` 基率是 **0/20**。

**含义**：`banned-api` 的价值**不是"抓模型犯错"**，而是**强制组织约定** ——
模型不知道你们规定"库代码不许写 stdout、不许 panic、只有 git 适配器能 shell out"。
这正是 value-proof.md §3 的"知识论证"，本次数据支持它。

### 4.4 API 幻觉：0/22（负结果，必须记下）

**22 个任务全部编译通过，零 API 幻觉。** 本实验**不支持**"facts / context 防幻觉"
这一价值主张。

但这**不足以否定**它，因为协议里有一个强缓解因素：任务提示明确允许
"阅读包内既有代码"。这等于手工替 agent 做了 `get_constraints` 想做的事。
要判定该主张，需要更严苛的协议（见 §6）。

## 5. 威胁与局限（诚实清单）

1. **单模型家族、单发、无 token 压力**：真实 agent 在长会话与预算压力下更不谨慎。
   本文所有数字应视为**下界**。
2. **提示词里"可读包内代码"**：显著降低违规率（尤其幻觉类）。这是最大的方法学让步。
3. **样本量 n=22**：只够看大效应（>20pp）。18.2% 与 9.1% 这类数字的置信区间很宽。
4. **任务由我编写**：可能无意识地偏向或偏离真实工作负载。
5. **D 重复 / B 依赖白名单未被触达**：任务是单文件新增，天然不产生克隆，
   也没有新增外部依赖。**0% 是设计局限，不是"该类无价值"的证据。**
6. **`cmd/` 缺失的副本**（已修，见 §2）：对 `internal/` 任务无影响，但 U05 作废重跑。
7. **"违规"≠"应该被拦"**：例如 `parseCronElement` 复杂度 20 是否真该拦，属 policy 判断。

## 6. 对产品方向的含义

按 value-proof.md 的判据（`P ≥ 10–15%` ⇒ 值得做）：

- **方向未被推翻**：ANY = 31.8%，error 级 = 9.1%，都远高于阈值。
  "9.1% 的 agent 任务会被这道闸门拦下"是一个可直接引用的数字。
- **投入应当重排**：本次数据把价值集中在 **度量阈值** 与 **架构分层** 两类，
  而不是项目原先最强调的 API 对齐。
  - 架构类的**单次命中价值极高**（编译器不可替代），即便基率只有 4.5%，
    也应按"高后果、低频率"对待，而不是按基率砍掉。
  - API 对齐类需要**更严苛的协议**才能判定：去掉"可读包内代码"的提示、
    改为多轮任务、加入 token 预算压力。**这是 R2 之前该补的实验。**
- **下一步**：进入 R2（A/B + 四臂消融，见 `ab-experiment.md`），
  但**任务集应当加权到"算法自由度"与"跨包组合"两类** —— 那是基率所在。

## 7. 复现命令

```bash
SRC=/path/to/metricgate          # 真实仓库
POL=$SRC/policy.yaml             # 真实策略

# 1) 构建 policy-blind 沙箱（注意锚定排除，否则会误删 cmd/metricgate）
D=/tmp/mg-eval
rm -rf $D && mkdir -p $D
cd $SRC && rsync -a \
  --exclude '.git' --exclude '.metricgate' --exclude '/docs' \
  --exclude '/policy.yaml' --exclude '/metricgate' --exclude '/metricgate-mcp' \
  --exclude '.workbuddy-ai' --exclude '/README.md' --exclude '/AGENTS.md' --exclude '/.pi' \
  ./ $D/
test -f $D/policy.yaml && echo "LEAK" || echo "clean"
(cd $D && go build ./...)        # 基线必须可编译

# 2) 让 policy-blind agent 在 $D 内按任务集新建文件（不得改动既有文件）

# 3) 用真实策略度量（只看 zz_eval_* 文件上的 findings）
(cd $D && $SRC/metricgate check . -config $POL -format json > /tmp/out.json)

# 4) 按类聚合（分类器：source==metric→C 度量；ruleId 前缀 banned-→A 禁用 API；
#    layer-violation/forbidden-edge/import-cycle/sdp-violation→B 架构；
#    unknown-dependency→B 依赖；function-clone/clone-drift/metric-regression→D 重复）
python3 agg.py /tmp/out.json
```

聚合脚本（本次使用，可直接复用）：

```python
import json, sys, collections
ARCH={"layer-violation","forbidden-edge","import-cycle","sdp-violation"}
CLONE={"function-clone","clone-drift","metric-regression"}
def cls(f):
    rid=f["ruleId"]
    if f.get("source")=="metric": return "C-metric"
    if rid.startswith("banned-"): return "A-banned-api"
    if rid in ARCH:              return "B-architecture"
    if rid=="unknown-dependency":return "B-dependency"
    if rid in CLONE:             return "D-clone"
    return "other:"+rid
d=json.load(open(sys.argv[1]))
gen=[f for f in d["findings"] if "zz_eval_" in f["file"]]
print(collections.Counter(cls(f) for f in gen))
```

## 8. 与既有文档的关系

- 本文 = value-proof.md 阶梯 **R1** 的实测。
- 下一步 **R2**（A/B + 消融）见 [`ab-experiment.md`](ab-experiment.md)。
- 引擎侧的确定性能力（检出/效率/token 经济）见 [`evaluation.md`](evaluation.md)。
