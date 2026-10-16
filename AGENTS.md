# metricGate 项目约定

Edit 工具的 ops 语法和失败处理规则在全局 `~/.pi/agent/AGENTS.md` 里，每次会话都会加载，本文件不重复。

对话使用中文

## 步骤预算

**一步 = 一处改动 + 一次廉价验证，墙钟硬上限 5 分钟。**超过就停下优化这一步再重试，不要接着等——等下去的结论并不比重来更可信，而这一轮的时间已经被那一半花掉了。

超预算时按这个顺序优化，而不是「再跑一次看看」：

1. **拆细改动**：一处改动只做一件事（一个函数 / 一个 CSS 块 / 一张图），验证口径跟着只查这一件事。
2. **换便宜的观测**：能把全量验证换成定向验证就换（见下表），别为一行 CSS 跑整个测试树。
3. **缓存昂贵观测**：截图、真实仓库扫描这类结果写文件，改动没碰到输入就复用，不重拍/重扫。

本仓三档观测的实测耗时（2026-10-15 在 metricGate 自己树上量得）：

| 命令 | 内容 | 实测 |
|---|---|---|
| `bash scripts/cockpit-observe.sh quick` | `node --check` + `render_all` + 定向 web 契约测试 | **4.5s** |
| `bash scripts/cockpit-observe.sh fast` | 上述 + live smoke + `go build ./...` + `go test ./internal/cockpit/...` + `metricgate check` | **15.4s** |
| `bash scripts/cockpit-observe.sh shots "" <视图>` | 一批一个 Chrome、逐张关 target，detached | **10.5–14.6s/张** |

`quick` 是每步的默认验证。`fast` 和 `check_path`（整树闸门，写 `.metricgate/history.db`）是**收口级**观测，只在一批步骤做完后跑一次——把它们塞进每一步会把预算全花在重复验证上。

## 改动后

Go 改动完成后跑 `go build ./...`，再对改到的包跑 `go test ./internal/<包>/...`，两件事都通过才算完成。

## 闸门循环（metricGate MCP，接入在 .pi/mcp.json）

写代码时走三触点，不要绕过：

1. **生成前**：先调 `get_constraints`（拿本仓约束包）或 `find_symbols`（查可复用符号），不要凭空猜仓库规矩。
2. **每次产出后**：对刚写/改的每段代码调 `check_code`（虚拟文件名按扩展名选语言），把 findings 修完再继续，不要攒到最后。
3. **收尾**：`check_path`（必要时 `-diff` 口径）确认整树状态；pre-commit hook 会自动再跑一次 `check -diff`，error 级 finding 会直接拦住提交。

调用记录写入 `.metricgate/mcp-calls.jsonl`（默认开启），是 claims #15 的采集数据，不要删除或绕行。
