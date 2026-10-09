# M2 主协议执行报告（B 类架构预防）：digest 生成期注入 3/5 防住，claims #18 证实

状态：**已执行（2026-10-09）**。协议见 [evaluation-m2-arch-protocol.md](evaluation-m2-arch-protocol.md)（预注册判定线未改动）。

## 0. 一句话

5 个 B 类架构诱导任务 × L0/L1 两臂：**3 个 L0 违规任务在 L1 全部防住（3/3；按最严口径含无信息对也是 3/5，达到预注册 ≥3/5 线）**——digest 的跨层惯用法节 + 包内依赖先例节在生成期把 B 类违规率从 100% 压到 0，且 L1 agent 的修法与 WP1 预设的 port-adapter 惯用法逐字吻合。claims #18 ⏳→⚠️。

## 1. 执行口径

- 通道 pi headless `--provider 2api --model glm-5.3-flash`（与 M2-B 同端点）；沙箱 /tmp/m2b_sandbox policy-blind；判定 `check . -config <真仓policy> -format json` 全仓（子目录 check 不解析跨包边，引擎语义）；产物逐臂保全 zz_artifact_<ARM>.go；超时 perl alarm 1800s + 重试一次。
- L1 digest 为当前 HEAD 冻结文件（含跨层惯用法节 + 包内依赖先例节，~770–840 tok）；B4 目标包 symbols 零内部导入，digest 无先例节（设计内对照组）。

## 2. 主结果表

| 任务 | 目标包→诱导 adapter | L0 layer-violation | L1 layer-violation | L1 防法 | 判定 |
|---|---|---|---|---|---|
| B1 | dist→store | 0（自带 stdlib sqlite + modernc.org/sqlite） | 0（仅 import model） | — | 无信息对 |
| B2 | deps→git | **1**（另 fan-out×2） | 0（仅 config/model） | 改动集作参数注入 ScanChanges(allow, changes) | **防住** |
| B3 | status→hotspots | **1** | 0（仅 model/parser） | `ChurnSource` port + 组装根注入 | **防住** |
| B4 | symbols→sources | 0（os.ReadDir stdlib 走目录，另 8 个 metric warning） | 0（fan-out×2；自创 FSRepoFileSource port 形状） | — | 无信息对 |
| B5 | refs→git | **1** | 0（仅 model/parser） | `ChangeSource` port + 组装根注入，并点名既有 `git.LoadChanges` 作实现 | **防住** |

H1-B（预注册）：L0 违规且 L1 无违规记防住 → **3/3**；分母取全 5 任务 → 3/5，两种口径均 ≥3/5 → **证实**。
H2-B（机制）：B3 报告原文「core 层不 import git 适配器、不碰 os/exec；…定义成 ChurnSource 接口由适配器实现，组装根注入」；B5 报告原文「端口（port）而非直接依赖…由组装根注入实现。未用复制代码绕过分层」——WP1 惯用法节被逐字消费。B2 报告亦引用「跨包引用属禁止方向」。

## 3. 机制发现

1. **B 类预防从 0/1（R3 U04）到 3/3（本轮）**，增量装置 = WP1 惯用法节（07:32）+ 包内依赖先例节（07:42）。B3/B5 是无包内 adapter 先例可模仿的（先例节只列 core 导入），防住只能来自惯用法节的「怎么做」指令——补上了 R3 结论「B 类约束只说禁什么不说怎么合规」的缺口。
2. **L0 的失败形状 = R1 U04 同构**（core 需要外层能力→直接 import adapter），三次复现（B2/B3/B5），说明没有注入时这是稳定默认行为，不是采样噪声。
3. **合规替代路线谱系**：L0 agent 也会走 stdlib（B1 自带 sqlite、B4 os.ReadDir）——「无违规」不等于「合惯例」；B4 L1 自创 FSRepoFileSource port 形状（symbols 无任何先例），提示惯用法节教会的是形状而非具体 API。

## 4. 诚实边界

- n=5、单 run/臂；无信息对 2 个使有效样本仅 3——判定线压线通过，方向性强但统计力弱。
- 两臂无法分离惯用法节 vs 先例节 vs 分层契约节的单独贡献（产品形态本就三节同注）。
- B4 L0 agent 违反单文件约束：`git init` + 3 次全树提交（base=205 文件快照、base2=+zz_eval_b4、final=改写）。工作树产物与量测不受影响（量测读工作树），记为完整性偏差；沙箱 .git 为该 agent 自建（M2-B 时沙箱无 .git）。
- 端点 2api 与 R1 Cliproxyapi 不同；2api 空转/重置仍在（B2 L1 两次 3600s 空转后第三次成功；B1 L1 尾部告警但产物完整）。
- L1 成本未与本轮 L0 做配对归因（M2-B 结论：同任务方差可达 30×，单点 token 对比不结论）。

## 5. 产品决定

1. claims #18 ⏳→⚠️：digest 生成期注入可防 B 类（5 任务 3 防，机制证据逐字）；三句话禁区相应收窄（#18 可在带样本数引用）。
2. 惯用法节与先例节保持常开（本次即是产品默认形态的实测）。
3. 后续不追第三臂拆分归因；B 类证据链下一步是真实采纳数据（claims #15/R6 calllog），不再是受控实验。
