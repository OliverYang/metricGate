# M2 主协议（B 类）：digest 惯用法/先例注入能否在生成期防住架构违规

状态：**协议预注册（2026-10-09），未执行**。执行后在本文档追加「执行结果」节，不改预注册判定线。

## 0. 一句话

B 类（架构分层）是 R3 唯一未防住的违规类别（U04 0/1，修复兜底 6.59× 最贵）；
M2-B 给出首个正面样本（V3 L1 参数注入防住）但 n=1。本实验用 5 个新 B 类任务
× 2 臂，回答：**生成期注入 digest（跨层惯用法节 + 包内依赖先例节）能否把
B 类预防率从 0 拉到 ≥3/5**。这是 claims #18 的判定实验。

## 1. 装置（全部已落地）

- **跨层惯用法节**（WP1，2026-10-07）：digest「跨层惯用法」——需要跨层时在当前层
  定义 port、由被依赖层实现、组装根注入；禁止复制代码绕过分层。
- **layer-violation 修复指令后缀**（WP2）：违规消息携带 port-adapter 修法。
- **包内依赖先例节**（2026-10-09）：digest「包内依赖先例」——本包已验证的合法
  依赖方向（Go 取 internal/ 导入）。M2-B 机制发现：包内先例是最便宜的约束注入
  （V1/V2 agent 模仿先例即合规）。
- 判定器：`metricgate check . -config <真仓 policy> -format json`，
  layer-violation 规则（core→adapter 即 warning）。

## 2. 任务集（B1–B5，全部引擎实测可触发）

设计原则（M2-B V 组教训固化）：目标包为 core 层；任务只描述**能力需求**，
不点名 adapter 包（V3 式诱导）；五个目标包包内均无 adapter 导入先例
（模型不能靠模仿既有 adapter 用法"天然合规"）。

| 任务 | 目标包 | 任务文本（逐字用于 prompt） | 诱导方向 | 探针验证 |
|---|---|---|---|---|
| B1 | internal/dist | 给 dist 增加趋势持久化：把每次评估的分布快照存进仓库的本地趋势库，供跨运行对比读取。 | core→adapter (store) | ✅ layer-violation fire |
| B2 | internal/deps | 给 deps 增加变更感知：找出本次改动中引入了哪些新的第三方依赖，只报改动文件的 import 变化。 | core→adapter (git) | ✅ fire |
| B3 | internal/status | 给 status 增加热点维度：结合各文件最近的改动频率，给每条规则状态标注其所在文件的热度。 | core→adapter (hotspots) | ✅ fire |
| B4 | internal/symbols | 给 symbols 增加批量入口：遍历仓库根目录下的 Go 文件逐个建索引，跳过无法解析的文件。 | core→adapter (sources)；包内零先例（连 core 导入都没有） | ✅ fire |
| B5 | internal/refs | 给 refs 增加提交范围过滤：引用统计只统计某个提交范围内有改动的文件。 | core→adapter (git) | ✅ fire |

注：探针文件（导入 adapter 包的 20 行 zz 文件）在真仓 policy 下全部触发
`layer-violation (layer "core" (internal/X) may not depend on layer "adapter"
(internal/Y); allowed: [core] — fix: define a port ... composition root)`。
合法替代路线存在且不唯一：参数/函数注入（V3 L1 实证）、stdlib（os.ReadDir 等）、
把 I/O 留在调用方（package 注释明示）。测的正是 digest 能否把模型引向这些路线。

## 3. 臂与预注册判定

两臂（单 run/臂，R1–R3 与 M2-B 同口径；同任务文本、同 LEAD、同沙箱、同超时）：

- **L0**：无 digest（R1 基线臂）。
- **L1**：prompt 附加当前 digest（含跨层惯用法节 + 包内依赖先例节，~644–836 tok）。

**H1-B（claims #18 判定线）**：逐任务配对计数——L0 违规且 L1 无违规记为"防住"。
- ≥3/5：**证实**——claims #18 升 ⚠️（"digest 生成期注入可防 B 类，5 任务 3 防"）。
- 0/5：**证伪**——claims #18 降级为"仅修复期能力"（WP2 保留，WP1 惯用法节
  降为报告项）。
- 1–2/5：**部分**——claims #18 保持 ⏳，样本描述性报告，不外推。

**H2-B（机制证据，非判定线）**：L1 产物最终报告/推理中是否出现 port/注入/组装根
措辞或对先例节的引用（M2-B V3 L1 与 N06 L2 都有自发引用先例）。只作机制描述。

**完整性红线**（沿 R3）：30 产物 `go build ./...` 必须通过；沙箱 tracked 文件
改动 = 泄露事故（V3 L0 先例）；判定只看 zz_eval 文件的 findings。

## 4. 诚实边界

- 单 run/臂：token 与行为有臂内方差（M2-B N06 L1 三次才成功），防住/未防住
  都是单点观察；判定线设在 3/5 是为容忍 1 次方差，不能消除。
- L0/L1 之外无第三臂：无法把"防住"单独归因给惯用法节 vs 先例节 vs 阈值节——
  三节同时注入（这正是产品形态）。机制归因靠 H2-B 文本证据。
- B4 的合法 stdlib 路线（os.ReadDir）不违反分层但也不符合"仓库惯例"
  （sources.Files 集中文件列举）；产物走 stdlib 记为"合规但非惯用"，不计违规。
- 端点为 2api/glm-5.3-flash（与 M2-B 同），与 R1 的 Cliproxyapi 不同——
  与 R1 基率对比需注明端点差异。
- n=5，方向性证据；与 M2-B V 组（V3 L1 防住）合并解读，不单独外推。

## 5. 执行细节（沿 M2-B runner）

- 沙箱 /tmp/m2b_sandbox：policy-blind（无 policy.yaml/AGENTS.md/.pi/arch.json/
  .metricgate），go.mod 保持 `module metricgate`，跑前清 zz_eval_*，跑后核验
  tracked 文件未动、go.mod 未被改（V3 事故教训）。
- 判定 `check .`（全仓）——`check <子目录>` 不解析跨包边，layer-violation
  不触发（引擎语义，2026-10-09 实测）。
- 产物保全：每臂结束即拷贝 zz 文件到 out/<TASK>/zz_artifact_<ARM>.go。
- 会话拾取限 --private-tmp-m2b_sandbox-- 目录。
