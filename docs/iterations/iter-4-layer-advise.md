# 迭代 4：layer 违规的结构修复建议

日期：2026-10-11 · 对应 docs/roadmap.md 断点：试点 132 条 layer-violation 中 77 条同根因（类放错包，docs/pilot/conversion-map.md 教训 5），报告只有通用 port-adapter 文案，没有"类搬家/包归属"这类结构性建议。

## 交付物

| 文件 | 内容 |
|---|---|
| `internal/arch/advise.go` | `fanIn{pkgs, layers}`（每包被多少 distinct 包 / distinct 层依赖）+ `computeFanIn`（distinct from-layers 单独一遍，防同层导入者重复计数）+ `matcher.suggestFix(e)` 双分支建议 |
| `internal/arch/check.go` | `matcher` 加 `fanIn` 字段；`newMatcher(p, g, pkgs)` 签名带 graph（layerOf 先算再喂 computeFanIn）；`layerViolation` message 追加 `; suggest: ...` |
| `internal/arch/advise_test.go` | 3 测试：misplaced 共享包（试点根因形态）/一次性依赖/合法边不带建议（fixture 修正教训：service→interceptor 若不在 may_depend_on 里本身就是违规） |

## 设计决策

- **信号选 fan-in 的"层"维度而不是"包"维度**：被 2 个包依赖可能是同层正常复用；被 2 个 distinct 层依赖才是"这个包其实在共享层的位置上"的证据。这正是试点 `LoginContextHolder` 放在 interceptor 包被全项目引用的形状。
- **双分支而非三分支**：无层目标（unlayered）永远不会违规，第三个"声明缺失"分支是死代码，第一版里被识别并删除。
- **建议拼进 message 而不是新 finding 字段**：所有消费面（text/SARIF/MCP/MCP gate digest）零改动自动获得；加法字段是迭代 5 之后的事。
- 原有 port-adapter 文案保留：建议是**补充**判型（搬家 vs 改声明），机制性修法（port 接口、别复制代码绕层）仍然每条都带。

## 吃狗粮实录

1. **gate 抓住自己（迭代内第 1 次）**：第一版 suggestFix 三分支 + layerViolation 传参 `toLayered bool`，自查发现 unlayered 分支不可达——收成双分支，签名去掉布尔参数。
2. **死代码先删再过门**：初稿写了 sortLayerNames/joinQuoted 两个投机 helper，未接线即删除（gate 会抓 unexported 未用，先手清）。
3. **测试 fixture 教训**：TestAdviceNotOnLegalEdges 初版把 service→interceptor 当"合法边"，但 service 层没声明 may_depend_on——它本身就是违规，测试失败暴露的是 fixture 认知错误而非产品 bug；修正为显式 `p.Layers[1].MayDependOn = ["interceptor"]`。
4. **E2E 建议可执行性验证（合同要求）**：/tmp/layerdemo（handler/service/interceptor 三层，interceptor.Ctx 被 handler+service 引用）：
   - 形态 1（共享 misplaced）：两条违规都带 "misplaced; move it (or its shared parts) to a lower shared layer" → 按建议 `git mv interceptor shared` + policy 声明 shared 层为合法依赖 → gate PASSED。
   - 形态 2（一次性依赖）：handler→model 单边违规，带 "add \"model\" to layer \"handler\"'s may_depend_on; otherwise move the dependent code" → 按建议加 may_depend_on → gate PASSED。
   - 两形态建议文本均可直接执行，无需再读源码。
5. **demo 教训**：Go 包的 graph 身份是**相对 root 的目录名**（packageID），policy packages 要写 `["handler"]` 而不是 `["layerdemo/handler"]`——demo 首跑 0 findings 根因即此（imports 靠 byLast 索引能解析，但层声明匹配不上）。

最终：go build OK；arch/engine 测试过；gofmt/diff-check 干净；self-gate PASSED（3 accepted，0 error）。

## 五性核对

| 性质 | 结论 | 证据 |
|---|---|---|
| 可介入 | ✅ | gate 抓住 suggestFix 不可达分支（死代码/复杂度）与测试 fixture 认知错误；两类建议文本经 e2e 证实可执行 |
| 可审计 | ✅ | 建议判型信号（fan-in 层数）可从 graph 复算；单测钉住两形态判型边界，fixture 即审计记录 |
| 可度量 | ⚠️ 部分 | 建议命中率未度量（多少违规被按建议修复 vs 人工改判）；需 calllog/cockpit 侧后续追踪——记入断点遗留 |
| 可回溯 | ✅ | 建议拼在 finding message，随 run 存 history.db，RunFindings 可回溯当时建议 |
| 可观察 | ✅ | text 报告/SARIF/MCP/MCP gate digest 四个消费面零改动自动携带（message 通道）；/tmp/layerdemo 两形态肉眼可读 |

**闭环判据达成**：试点 77/132 同根因场景（misplaced shared package）与一次性依赖场景都有可执行的结构建议；合法边不产生噪音。

## 断点遗留

- 建议采纳率无度量（可度量性缺口）：finding→修复的闭环追踪需要跨 run 的 edge 状态对比，与迭代 3 的 metric trend 同源（run×edge 维度）。
- Java/其余语言只有 Go+Java 进 arch graph（Extract 仅两语言），建议通道天然覆盖两者；TS/JS/Py 等不在 pkg graph 内，层治理暂不涉及。
