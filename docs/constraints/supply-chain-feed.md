# 供应链告警 → 门禁规则：外部 CLI 适配层

> 状态：已落地 2026-10-01（scripts/osv2policy 编排 osv-scanner + scripts/osv2policy.sh + internal/feed 转换库）。
> 回答的产品问题（2026-09-30 产品问答轮）：「外部输入源能否动态注入 policy，让工
> 具的检出能力跟着外部告警走？」——供应链安全告警（CVE/OSV）是最理想的第一类输
> 入：生而结构化、纯查表判定、教科书级黑名单。
>
> 形态边界：**metricgate 本体不内置这条链**。osv-scanner 等外部工具保持外部，
> metricGate 只提供格式转换适配器——外部告警格式千变万化，把它们挡在可独立演
> 进的 scripts/ 适配层里，引擎与 CLI 不为任何具体外部工具长代码。

## 1. 这条链路是什么

```
osv-scanner scan --format json <项目>     ← 外部 CLI（依赖树权威）
        │ stdout JSON 报告
        ▼
scripts/osv2policy.sh                     ← 编排层（装了 osv-scanner 走在线扫描；
        │                                   拿到现成报告用 SKIP_SCAN= 离线跑）
        ▼
scripts/osv2policy（Go 适配器）            ← 解析 scanner 报告 → 派生禁令 → 查重
        │                                   → 合并 → 回读校验 → 写独立 patch
        ▼
patch.yaml  ——人批（审 + 定级 + fixture 烟测）——▶ .metricgate.yaml
        ▼
check / digest / status 三条通道自动生效
```

**policy 是数据，适配器只是在写数据**——注入过程零引擎代码、零发版。与 SOP
（constraint-conversion-sop.md）的关系：适配器只覆盖 SOP 第 1–3 步的「结构化输
入快车道」（条款天然是 `禁止 import X`，无需非标提取）；第 4–5 步（fixture 烟
测、严重级定级）仍然必须走，一步不省。

## 2. worked example：Log4Shell

```bash
# 0. 安装编排依赖（一次性）：brew install osv-scanner

# 1. 扫描目标项目并出候选（在线模式；已有报告时改用 SKIP_SCAN=report.json）
scripts/osv2policy.sh /path/to/target-project demo/policy-pilot-java.yaml patch.yaml
#    + [import] github.com/gogo/protobuf  GHSA-c3h9-896r-86jm (CVE-2021-3121)
#    = [import] org.apache.logging.log4j  GHSA-jfh8-c2jp-5v3q — 与现有 policy 重复
#    - npm left-pad  台账：暂无源码 import 词汇（依赖树工具管辖）
#    osv2policy: wrote patch.yaml — review the + rows, fixture-smoke (SOP steps 2-5), then commit

# 2. 人批：审 + 行、fixture 烟测（SOP 第 4 步：已知引入点必须命中、干净代码零误伤）
# 3. 定级：默认 warning 观察期；P0 资损类告警补 rules 键升 error
# 4. 合入 .metricgate.yaml —— 之后三条通道自动生效：
#    check 全量扫描秒级 / CI -diff 毫秒级 / constraints digest 让 agent 落笔前不写
```

只看计划不落盘（plan 模式，无第三参数）：`+` 新条目 / `=` 与现有 policy 重复 /
`-` 台账（暂无源码 import 词汇的生态）。已有同 match 禁令显示 `=` 时安全语义自
动叠加到既有条目（试点 policy 的 LOG-03 本就禁 log4j，Log4Shell 告警等于给既有
禁令叠上 CVE 语义，不重复落条目）。

派生条目形态（`BanRow`）：

```yaml
- kind: import
  match: "com.example.vuln"
  reason: "OSV GHSA-fake-0001 (CVE-2026-00001): <summary> — 供应链告警 tripwire
          （源码引入点拦截；影响 1.0.0 起、1.2.0 修复，版本判据由依赖树工具核验）；
          升级 com.example.vuln:demolib 后移除本条"
  use: "升级 com.example.vuln:demolib 至已修复版本后移除本条"
```

reason/use 都带 OSV ID + 影响区间 + **移除条件**——升级依赖后这条禁令该删，台账
不会烂尾。

## 3. 输入格式与派生规则（derive）

适配器吃两种输入（`feed.ParseAny` 形状嗅探）：

| 输入 | 形状 | 场景 |
|---|---|---|
| osv-scanner 报告 | `results[].packages[]{package{name,version,ecosystem}, vulnerabilities[]}` | 编排层在线扫描的标准形态 |
| OSV 告警原文 | 单 vuln 对象或 `{"vulns":[...]}` | 订阅源/GHSA API 拉下来的 JSON，无需先跑扫描 |

生态派生：

| 生态 | 包坐标 | 派生禁令 | 语言 |
|---|---|---|---|
| Maven | `group:artifact` | ban `group`（importRule 精确+前缀匹配天然覆盖子包） | java |
| Go | 模块路径 | ban 模块路径原样 | go |
| npm/pypi/cargo… | — | **不派生，进台账**（Lang=""，plan 显示 `-`） | — |

scanner 报告的包上下文来自扫描结果本身：vulnerabilities[].affected 按
(ecosystem, name) 匹配当前包取 introduced/fixed 区间；同一漏洞跨多个 source 出
现只展开一次（去重）。Maven 坐标→import 前缀是**记录在案的启发式**（源码 import
不携带 artifact 坐标）：`com.fasterxml.jackson.core` 这类短 groupId 会过宽，人批
时收窄到实际包根（如 `com.fasterxml.jackson.databind`）——这正是 `=`/`+` 行必须
人过的原因之一。

查重（dedup）：`语言 \x00 kind \x00 match` 三元组对现有 policy 的全部 ban 全量去
重（空 kind 按 call 归一）。

## 4. 合并语义（Merge）与安全阀

文本级合并（非重排 YAML，保留注释/顺序/手写格式）：

- 语言节缺失 → 文件尾追加新语言节；
- 语言节在但无 `banned-api` 键 → 节内插入该键；
- 已有 `banned-api` → 追加到最后一个列表项之后；
- 检测到 tab 缩进 → 拒绝（YAML 禁 tab，宁可不写不写坏）；
- **写入前回读校验**：合并产物先 `yaml.Unmarshal` 试解析，失败即拒写；
- **产物永远不写 -config 输入**——patch 先落独立文件，人工审阅后合入；
- 产物恒以换行结尾（POSIX 文本语义）。

## 5. 检出语义与三条边界（诚实账）

适配器注入后，闸门能拦的是**源码 import 引入点**：

1. **版本盲**：源码 import 不带版本号——同一包 1.0.0（有洞）和 2.15.0（已修复）
   长得一样。所以语义是 **tripwire（临时全禁）**：精确版本区间判定归依赖树工具
   （osv-scanner / mvn dependency:tree）。reason/use 里的移除条件把「升级后拆
   雷」写进台账。
2. **只见直接引用**：传递依赖（代码没 import 但 classpath 上有）不在源码可见面
   内，归 osv-scanner。分工：osv-scanner 是依赖树权威，metricGate 是源码引入点
   tripwire——**唯一能在 LLM/开发者落笔前起作用的层**。
3. **Go 侧编译耦合**：Go module 下未引用的依赖编译不过，价值收窄为存量清单清理
   + 合并前拦截 + digest 接地。

严重级纪律与 SOP 一致：import ban 默认 warning（观察期）；error 级需在
`languages.<lang>.rules` 显式加 `banned-<match>: error` 键，且 fixture 烟测必须
先行。告警确认为 P0 资损类可当天升 error——这是链路里唯一需要人批的两处之一
（另一处是合入动作本身）。候选条目永远不自动进 policy，自动化止步于 patch。

## 6. 在动态迭代框架中的位置

对应 2026-09-30 产品问答的两循环模型：本链路是 **Loop A（数据循环）** 的第一块
接线的触发器——外部输入源（OSV 告警）→ 候选条目 → 人批 → policy 更新 → 检出能
力变化，全程零发版。它同时是「外部输入源匹配检出能力」的模板：输入结构化程度
越高（OSV 生而结构化），人批那笔越轻。

**适配层形态是刻意选择**：外部输入源会持续增加（事故报告已由 T1a playbook 覆
盖、规范文档已由 `constraints extract` 覆盖、旧 lint 配置待接），每种源一个
scripts/ 适配器，引擎词汇与 CLI 面不为具体外部格式膨胀——外部工具的格式演进被
隔离在适配器里。

## 7. 工具面速查

```bash
# 编排层（推荐入口）
scripts/osv2policy.sh <project-dir> [.metricgate.yaml] [patch-out.yaml]
#   在线：调用 PATH 上的 osv-scanner（OSV_SCANNER= 可覆盖路径；未装时给安装指引退出 127）
#   离线：SKIP_SCAN=/path/to/report.json 直接吃现成报告（无 osv-scanner 也能跑通链路）

# 适配器（编排层内部调用，也可单独使用）
go run ./scripts/osv2policy -in <report.json|-> [-config .metricgate.yaml]
                            [-out <patch.yaml>] [-format text|json]
```

- plan（默认，无 -out）：显示 `+` 新条目 / `=` 与现有 policy 重复 / `-` 台账；
- `-out <patch.yaml>`：文本级合并到独立产物 + 回读校验后写入（`out == config` 拒绝）；
- JSON 输出：`{added, existing, skipped}` 各带完整条目字段；
- 适配器内部复用 `internal/feed` 转换库（解析/派生/查重/合并，12 项单测）。
