# 规则集成接口：LLM 提案，引擎裁决

> 状态：已落地 2026-10-01（`constraints rule` + internal/rulegen）。
> 对应需求（2026-10-01）：「通过 LLM 大模型描述即将接入的新规则的各种参数，
> 生成对应的规则，快速进行打包验证，走完约定好的入参出参流程就集成进工具，
> 不必关注本项目代码实现」+「每个节点可变更、回退、修改后继续生成」。
>
> 定位：流水线缺口 1 的最后一块——转化侧的验证与集成接口。八字原则：
> **LLM 提案，引擎裁决**。LLM 负责起草（它读得懂 policy 词汇就能产出合法
> spec）；判定与执行永远是确定性引擎（编译、fixture 打包、判分零 LLM 参与）。
> 红线不变：验证通过也不自动进 policy——合并永远是人的动作（SOP 4–5）。

## 1. 接口契约（入参 / 出参）

集成者（人或 LLM agent）只接触两个东西：一个 Spec JSON，一组 fixture 文件。
不需要读 metricgate 源码。

**入参**——Spec（字段与 policy.yaml 词汇同构，LLM 读过 policy 就会写）：

```json
{
  "lang": "java",
  "kind": "call",
  "match": "MessageDigest.getInstance",
  "args_ban": ["MD5", "MD2"],
  "reason": "MD5/MD2 已有已知碰撞，禁止用于摘要/签名",
  "use": "改用 SHA-256：MessageDigest.getInstance(\"SHA-256\")",
  "severity": "warning",
  "except_files": []
}
```

| 字段 | 说明 | 词汇边界 |
|---|---|---|
| `lang` | go / java | 其余语言 → 台账，不硬造 |
| `kind` | call / import / constructor（constructor 仅 java） | Go 工厂函数走 call |
| `match` | 禁令模式，尾 `*` 前缀匹配 | 与 policy banned-api 同语义 |
| `args_ban` | 仅当字符串字面量参数等于列内值才命中 | 可选 |
| `reason` / `use` | 为什么禁 / 合规替代写法 | 进 finding 消息与 digest |
| `severity` | 缺省 warning（观察期）；error 即红线 | 定级是人批动作 |
| `except_files` | 文件名后缀豁免 | 可选 |

**fixture 纪律**：至少一个 bad（已知必命中的反例）——**没有反例的规则不可信**
（零命中规则在真实扫描里无法区分「代码干净」与「规则静默失效」，SOP 教训）；
good（必须保持干净的正例）可选但强烈建议，它是误伤检验。

**出参**——VerifyResult（text 人类可读 / json 机器可读）：

```json
{
  "pass": true,
  "rule_id": "banned-MessageDigest-getInstance",
  "existing": false,
  "yaml": "……可直接合并的 policy 片段……",
  "policy_yaml": "……验证时实际运行的完整 fixture policy……",
  "fixtures": [{"name": "md5", "expect": "hit", "verdict": "pass", "hits": [...]}],
  "compiled": true, "errors": 1, "warnings": 0, "notes": [...]
}
```

判分语义：bad 恰好命中 → pass；bad 零命中 → fail（反例没打到禁令）；
good 命中 → fail（match 过宽，考虑收窄）；**其他规则在 fixture 上的命中不参与
本规则判分**——verdict 只回答「这条规则按预期工作吗」。

## 2. 用法与节点可控

```bash
metricgate constraints rule -spec rule.json -bad bad.java [-good good.java]
                            [-config 现有policy.yaml] [-format text|json] [-out 节点目录]
```

- `-spec -` 从 stdin 读 spec（agent 管道友好）；
- `-config` 对现有 policy 查重（同 kind+match → `existing: true` + 提示）；
- `-out` 把**每个节点落盘**：

```
nodes/
  spec.json            # spec 回显（可改）
  bad.java good.java   # fixture 原文（可改）
  policy-verified.yaml # 验证时真实运行的完整 policy（可改后作为输入重跑）
  policy-entry.yaml    # 可直接合并的 policy 片段
  report.json          # 完整验证报告
```

**变更 / 重跑 / 回退**：改任何一个节点文件后重跑同一条命令即继续生成；保留旧
文件即回退。全流程状态都在磁盘上，没有隐藏会话。

**验收后的合并**（人批，SOP 4–5）：PASS → 人审 spec 语义与 fixture 代表性 →
把 `policy-entry.yaml` 内容并入项目 policy 的 `languages.<lang>.banned-api` →
定级（观察期 warning；确证 P0 才升 error）→ 该规则进入 check/digest/status
三通道，digest 与 finding 消息自动携带 reason/use。

## 3. 引擎裁决的具体含义

验证不是模拟：`internal/rulegen` 把 spec 渲染成真实 policy.yaml
（含 severity override、unknown-dependency 静默消依赖噪声、fail_on: warning），
写入临时目录，跑**真实引擎**（config.LoadFile → engine.New → RunDetailed），
再按 RuleID + 文件把 findings 投影回 fixture 案例判分。LLM 看到的 PASS 意味着
「合并后引擎真的会这样打分」，不是「LLM 自评没问题」。

词汇校验（Validate）先行：lang/kind/severity 白名单 + match 非空——**LLM 产出
词汇表之外的描述会被确定性拒绝**并提示可用词汇，拒绝项进台账（原语缺口聚类
的输入），而不是被静默容忍。

## 4. 边界（诚实账）

- 本接口只覆盖 **ban 类规则**（call/import/constructor）——占团队约束的大多数；
  架构分层、阈值类、白名单类规则的接入仍走 policy 直接编辑（那些本来就有
  policy 词汇，缺的是验证反馈面，后续可扩展同款 Spec）。
- **判分只保证 fixture 上的行为**——fixture 不代表全项目分布，合并后的 FP 审计
  （真实项目跑 + 抽样）仍是定级的前置，自动化不替代。
- **merge-ready ≠ merge-authorized**：`policy-entry.yaml` 是片段不是落点，
  兼容「嵌进既有语言节」的合并方式（banned-api 键已存在时把列表项并进去）。
- constructor 仅 java（Go 无构造器形态）；FQN 构造（new java.text.X()）由
  引擎拼写匹配覆盖，generic `new Foo<T>()` 不可检（AST 边界，与引擎一致）。

## 5. 在两循环模型中的位置

Loop A（数据循环）的提案端从「人读文档人写条款」扩展到「LLM 读文档起草 spec」：
知识库文档 → LLM 产出 Spec JSON + fixtures → `constraints rule` 确定性验证 →
人批 → policy。转化侧 token 花费一次、随 policy 永久摊销；执行侧依旧 0 token。
与 `constraints extract`（确定性提取器，规范化表格）互补：extract 是快车道，
rule 接口是「extract 判不了的条款」与「agent 直接提案」的通用入口。
